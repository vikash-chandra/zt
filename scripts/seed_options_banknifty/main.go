package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	"zerodha-trading/data"
	"zerodha-trading/monitoring"
	"zerodha-trading/risk"
	"zerodha-trading/selection"
	"zerodha-trading/strategy"
)

func estimateBankNiftyOptionPremium(spotPrice float64, strike float64, optionType string, tIST time.Time) float64 {
	dist := math.Abs(spotPrice - strike)
	// Base OTM premium for BANKNIFTY target premium ~250
	base := 250.0 + (300.0-dist)*0.20
	if base < 60.0 {
		base = 60.0
	}
	if base > 450.0 {
		base = 450.0
	}

	// Intraday decay factor: options lose ~50% value from 09:15 to 15:30
	minsFromOpen := float64(tIST.Hour()*60 + tIST.Minute() - (9*60 + 15))
	if minsFromOpen < 0 {
		minsFromOpen = 0
	}
	decayFraction := (minsFromOpen / 375.0)
	if decayFraction > 1.0 {
		decayFraction = 1.0
	}

	premium := base * (1.0 - 0.50*decayFraction)
	return math.Round(premium*20.0) / 20.0 // Round to nearest 0.05 tick
}

func main() {
	fmt.Println("================================================================")
	fmt.Println("  SEEDING BANK NIFTY 5M SUPERTREND OPTIONS PAPER TRADES         ")
	fmt.Println("================================================================")

	logger, err := monitoring.NewLogger("info")
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}

	ctx := context.Background()

	// Connect via tunnel on localhost:5433
	dbHost := "localhost"
	dbPort := 5433
	dbUser := "postgres"
	dbPassword := "trading_password"
	dbName := "zerodha_trading"
	dbSSLMode := "disable"

	db, err := data.NewDatabase(
		dbHost, dbPort, dbUser, dbPassword, dbName, dbSSLMode,
		logger.Logger,
	)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	// Ensure NIFTY BANK config in options_index_configs
	_, _ = db.WithContext(ctx).ExecContext(ctx, `
		INSERT INTO options_index_configs (
			index_symbol, is_active, is_live, base_lot_size, strike_step, 
			target_entry_premium, expiry_type, auto_square_off_time, last_new_trade_time,
			supertrend_cutoff_time, trail_sl_enabled, trail_sl_buffer_pct
		) VALUES (
			'NIFTY BANK', true, false, 15, 100.0, 
			250.0, 'MONTHLY', '15:14', '14:30', 
			'15:15', true, 5.0
		) ON CONFLICT (index_symbol) DO UPDATE SET
			is_active = true,
			base_lot_size = 15,
			strike_step = 100.0,
			target_entry_premium = 250.0,
			expiry_type = 'MONTHLY';
	`)

	// Fetch 5m BANK NIFTY Candles from DB (token 260105)
	bankToken := int64(260105)
	candles, err := db.GetLastNCandles("candles_5m", bankToken, 500)
	if err != nil || len(candles) == 0 {
		log.Fatalf("No candles found for BANK NIFTY in DB: %v", err)
	}

	log.Printf("Loaded %d 5-minute candles from PostgreSQL for BANK NIFTY.", len(candles))

	// Clear previous BANK NIFTY options paper trades from DB
	_, _ = db.WithContext(ctx).ExecContext(ctx, "DELETE FROM trades WHERE strategy = 'OPTIONS_SUPERTREND' AND symbol LIKE 'BANKNIFTY%'")

	// Initialize Engine & Selector
	stEngine := strategy.NewSuperTrendOptionsEngine(
		10, 7, 7,      // Periods
		4.0, 3.0, 2.0, // Multipliers
	)
	secMaster := data.NewSecurityMaster(db, nil, logger.Logger)
	strikeSelector := selection.NewOptionStrikeSelector(secMaster)
	baseLotSize := 15
	maxMultiplier := 3
	posMgr := risk.NewOptionsPositionManager(
		nil, logger.Logger, baseLotSize, maxMultiplier,
		50.0, 1000000.0,
	)

	totalTrades := 0
	winningTrades := 0
	losingTrades := 0
	grossProfit := 0.0
	grossLoss := 0.0
	totalPnL := 0.0

	var activeSymbol string
	var activeQty int
	var activeEntry float64
	var activeEntryTime time.Time
	var activeStrike float64
	var activeOptionType string
	hasActive := false

	loc := data.ISTLocation
	sqHour, sqMin := 15, 14

	var lastSeenDay string
	for i := 20; i <= len(candles); i++ {
		sub := candles[:i]
		lastCandle := sub[len(sub)-1]
		lastIST := data.NormalizeToIST(lastCandle.Time)
		candleCloseTime := lastIST.Add(5 * time.Minute)

		dayStr := candleCloseTime.Format("2006-01-02")
		if lastSeenDay != "" && dayStr != lastSeenDay {
			posMgr.ResetDailyMultiplier()
		}
		lastSeenDay = dayStr

		// Check Intraday Auto Square-Off at 15:14 IST
		isEOD := (candleCloseTime.Hour() > sqHour) || (candleCloseTime.Hour() == sqHour && candleCloseTime.Minute() >= sqMin)
		if hasActive && (isEOD || candleCloseTime.Format("2006-01-02") != activeEntryTime.Format("2006-01-02")) {
			exitTime := candleCloseTime
			if isEOD {
				exitTime = time.Date(candleCloseTime.Year(), candleCloseTime.Month(), candleCloseTime.Day(), sqHour, sqMin, 0, 0, loc)
			}
			heldMinutes := int(exitTime.Sub(activeEntryTime).Minutes())
			if heldMinutes <= 0 {
				heldMinutes = 15
			}
			exitPremium := estimateBankNiftyOptionPremium(lastCandle.Close, activeStrike, activeOptionType, exitTime)
			pnl := (activeEntry - exitPremium) * float64(activeQty)

			totalTrades++
			totalPnL += pnl
			if pnl > 0 {
				winningTrades++
				grossProfit += pnl
			} else {
				losingTrades++
				grossLoss += math.Abs(pnl)
			}

			_, err = db.WithContext(ctx).ExecContext(ctx, `
				INSERT INTO trades (symbol, entry_price, exit_price, quantity, pnl, side, time_held_minutes, created_at, strategy)
				VALUES ($1, $2, $3, $4, $5, 'SELL', $6, $7, 'OPTIONS_SUPERTREND')
			`, activeSymbol, activeEntry, exitPremium, activeQty, pnl, heldMinutes, exitTime)
			if err != nil {
				log.Printf("Failed to insert trade: %v", err)
			}
			posMgr.OnTradeClosed(exitPremium)
			hasActive = false
		}

		// Trailing SL breach
		if hasActive && !isEOD {
			currPrem := estimateBankNiftyOptionPremium(lastCandle.Close, activeStrike, activeOptionType, candleCloseTime)
			if posMgr.CheckTick(currPrem) {
				exitTime := candleCloseTime
				heldMinutes := int(exitTime.Sub(activeEntryTime).Minutes())
				if heldMinutes <= 0 {
					heldMinutes = 5
				}
				pnl := (activeEntry - currPrem) * float64(activeQty)
				totalTrades++
				totalPnL += pnl
				if pnl > 0 {
					winningTrades++
					grossProfit += pnl
				} else {
					losingTrades++
					grossLoss += math.Abs(pnl)
				}
				_, err = db.WithContext(ctx).ExecContext(ctx, `
					INSERT INTO trades (symbol, entry_price, exit_price, quantity, pnl, side, time_held_minutes, created_at, strategy)
					VALUES ($1, $2, $3, $4, $5, 'SELL', $6, $7, 'OPTIONS_SUPERTREND')
				`, activeSymbol, activeEntry, currPrem, activeQty, pnl, heldMinutes, exitTime)
				if err != nil {
					log.Printf("Failed to insert SL trade: %v", err)
				}
				posMgr.OnSLHit(currPrem)
				hasActive = false
			}
		}

		res := stEngine.CalculateTripleSuperTrend(sub)
		action, qty := posMgr.EvaluateSignal(res.Trend)

		// Only open new trades during market hours before 14:30 IST
		isPastCutoff := (candleCloseTime.Hour() > 14) || (candleCloseTime.Hour() == 14 && candleCloseTime.Minute() >= 30)
		if !isEOD && (action == "REVERSAL" || action == "OPEN_INITIAL") {
			if hasActive {
				exitPremium := estimateBankNiftyOptionPremium(lastCandle.Close, activeStrike, activeOptionType, candleCloseTime)
				pnl := (activeEntry - exitPremium) * float64(activeQty)
				heldMinutes := int(candleCloseTime.Sub(activeEntryTime).Minutes())
				if heldMinutes <= 0 {
					heldMinutes = 5
				}

				totalTrades++
				totalPnL += pnl
				if pnl > 0 {
					winningTrades++
					grossProfit += pnl
				} else {
					losingTrades++
					grossLoss += math.Abs(pnl)
				}

				_, err = db.WithContext(ctx).ExecContext(ctx, `
					INSERT INTO trades (symbol, entry_price, exit_price, quantity, pnl, side, time_held_minutes, created_at, strategy)
					VALUES ($1, $2, $3, $4, $5, 'SELL', $6, $7, 'OPTIONS_SUPERTREND')
				`, activeSymbol, activeEntry, exitPremium, activeQty, pnl, heldMinutes, candleCloseTime)
				if err != nil {
					log.Printf("Failed to insert reversal trade: %v", err)
				}
				posMgr.OnTradeClosed(exitPremium)
				hasActive = false
			}

			if !isPastCutoff {
				strikeRes, err := strikeSelector.SelectOTMStrike("NIFTY BANK", lastCandle.Close, res.Trend)
				if err == nil {
					activeSymbol = strikeRes.OptionSymbol
					activeQty = qty
					activeStrike = strikeRes.TargetStrike
					activeOptionType = strikeRes.OptionType
					activeEntryTime = candleCloseTime
					activeEntry = estimateBankNiftyOptionPremium(lastCandle.Close, activeStrike, activeOptionType, candleCloseTime)
					hasActive = true
					posMgr.OnTradeOpened(fmt.Sprintf("BANK-%d", candleCloseTime.Unix()), activeSymbol, activeOptionType, activeQty, activeEntry, candleCloseTime)
					log.Printf("[BANKNIFTY TRADE-OPENED] Symbol: %s, EntryTime: %s, Action: %s, Premium: ₹%.2f, Qty: %d",
						activeSymbol, activeEntryTime.Format("2006-01-02 15:04:05"), action, activeEntry, activeQty)
				} else {
					log.Printf("Strike selection error: %v", err)
				}
			}
		}
	}

	winRate := 0.0
	if totalTrades > 0 {
		winRate = (float64(winningTrades) / float64(totalTrades)) * 100.0
	}

	fmt.Println("\n================================================================")
	fmt.Printf("BANK NIFTY OPTIONS PAPER SEEDING COMPLETE\n")
	fmt.Printf("Total Trades   : %d\n", totalTrades)
	fmt.Printf("Winning Trades : %d (%.2f%%)\n", winningTrades, winRate)
	fmt.Printf("Losing Trades  : %d\n", losingTrades)
	fmt.Printf("Gross Profit   : ₹%.2f\n", grossProfit)
	fmt.Printf("Gross Loss     : ₹%.2f\n", grossLoss)
	fmt.Printf("Net PnL        : ₹%.2f\n", totalPnL)
	fmt.Println("================================================================")
}
