package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
	"zerodha-trading/risk"
	"zerodha-trading/selection"
	"zerodha-trading/strategy"
)

func estimateMidcpOptionPremium(spotPrice float64, strike float64, optionType string, tIST time.Time) float64 {
	dist := math.Abs(spotPrice - strike)
	// Base OTM premium for MIDCPNIFTY target premium ~80
	base := 80.0 + (100.0-dist)*0.15
	if base < 30.0 {
		base = 30.0
	}
	if base > 120.0 {
		base = 120.0
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
	fmt.Println("  SEEDING MIDCPNIFTY 5M SUPERTREND OPTIONS PAPER TRADES         ")
	fmt.Println("================================================================")

	// 1. Load Configurations
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logger, err := monitoring.NewLogger(cfg.LogLevel)
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}

	ctx := context.Background()

	// 2. Database Connection
	db, err := data.NewDatabase(
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		logger.Logger,
	)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	if err := db.InitSchema(); err != nil {
		log.Fatalf("Database schema initialization failed: %v", err)
	}

	// Ensure MIDCPNIFTY uses MONTHLY expiry
	_, _ = db.WithContext(ctx).ExecContext(ctx, "UPDATE options_index_configs SET expiry_type = 'MONTHLY' WHERE index_symbol = 'MIDCPNIFTY'")

	// 3. Fetch 5m MIDCPNIFTY Candles from DB
	midcpToken := int64(288009)
	candles, err := db.GetLastNCandles("candles_5m", midcpToken, 800)
	if err != nil || len(candles) == 0 {
		log.Fatalf("No candles found for MIDCPNIFTY in DB.")
	}

	log.Printf("Loaded %d 5-minute candles from PostgreSQL for MIDCPNIFTY.", len(candles))

	// Clear previous MIDCPNIFTY options paper trades from DB
	_, _ = db.WithContext(ctx).ExecContext(ctx, "DELETE FROM trades WHERE strategy = 'OPTIONS_SUPERTREND' AND symbol LIKE 'MIDCP%'")

	// 4. Initialize Engine & Selector
	stEngine := strategy.NewSuperTrendOptionsEngine(
		10, 7, 7, // Periods
		4.0, 3.0, 2.0, // Multipliers
	)
	secMaster := data.NewSecurityMaster(db, nil, logger.Logger)
	strikeSelector := selection.NewOptionStrikeSelector(secMaster)
	baseLotSize := 120
	maxMultiplier := 4
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
	sqHour, sqMin := 15, 15

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

		// Check Intraday Auto Square-Off at 15:15 IST
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
			exitPremium := estimateMidcpOptionPremium(lastCandle.Close, activeStrike, activeOptionType, exitTime)
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
			currPrem := estimateMidcpOptionPremium(lastCandle.Close, activeStrike, activeOptionType, candleCloseTime)
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
				exitPremium := estimateMidcpOptionPremium(lastCandle.Close, activeStrike, activeOptionType, candleCloseTime)
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
				strikeRes, err := strikeSelector.SelectOTMStrike("MIDCPNIFTY", lastCandle.Close, res.Trend)
				if err == nil {
					activeSymbol = strikeRes.OptionSymbol
					activeQty = qty
					activeStrike = strikeRes.TargetStrike
					activeOptionType = strikeRes.OptionType
					activeEntryTime = candleCloseTime
					activeEntry = estimateMidcpOptionPremium(lastCandle.Close, activeStrike, activeOptionType, candleCloseTime)
					hasActive = true
					posMgr.OnTradeOpened(fmt.Sprintf("MIDCP-%d", candleCloseTime.Unix()), activeSymbol, activeOptionType, activeQty, activeEntry, candleCloseTime)
					log.Printf("[MIDCP TRADE-OPENED] Symbol: %s, EntryTime: %s, Action: %s, Premium: ₹%.2f", activeSymbol, activeEntryTime.Format("2006-01-02 15:04:05"), action, activeEntry)
				} else {
					log.Printf("Strike selection error: %v", err)
				}
			}
		}
	}

	// Close any lingering active position at end of candles if past day
	if hasActive && len(candles) > 0 {
		lastTime := candles[len(candles)-1].Time
		lastIST := data.NormalizeToIST(lastTime)
		exitTime := time.Date(lastIST.Year(), lastIST.Month(), lastIST.Day(), sqHour, sqMin, 0, 0, loc)
		heldMinutes := int(exitTime.Sub(activeEntryTime).Minutes())
		if heldMinutes <= 0 {
			heldMinutes = 15
		}
		exitPremium := estimateMidcpOptionPremium(candles[len(candles)-1].Close, activeStrike, activeOptionType, exitTime)
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

		_, _ = db.WithContext(ctx).ExecContext(ctx, `
			INSERT INTO trades (symbol, entry_price, exit_price, quantity, pnl, side, time_held_minutes, created_at, strategy)
			VALUES ($1, $2, $3, $4, $5, 'SELL', $6, $7, 'OPTIONS_SUPERTREND')
		`, activeSymbol, activeEntry, exitPremium, activeQty, pnl, heldMinutes, exitTime)
		posMgr.OnTradeClosed(exitPremium)
	}

	// Update options_bot_state for MIDCPNIFTY
	newBalance := 1000000.0 + totalPnL
	finalTrend := "BULLISH"
	if len(candles) >= 20 {
		finalRes := stEngine.CalculateTripleSuperTrend(candles)
		if finalRes.Trend != "" {
			finalTrend = finalRes.Trend
		}
	}

	_, _ = db.WithContext(ctx).ExecContext(ctx, `
		INSERT INTO options_bot_state (index_symbol, multiplier, last_trend, sl_stopped_trend, awaiting_reversal, paper_balance, updated_at)
		VALUES ('MIDCPNIFTY', 1, $1, '', false, $2, NOW() AT TIME ZONE 'Asia/Kolkata')
		ON CONFLICT (index_symbol) DO UPDATE SET
			multiplier = 1,
			last_trend = EXCLUDED.last_trend,
			paper_balance = EXCLUDED.paper_balance,
			updated_at = NOW() AT TIME ZONE 'Asia/Kolkata'
	`, finalTrend, newBalance)

	winRate := 0.0
	if totalTrades > 0 {
		winRate = (float64(winningTrades) / float64(totalTrades)) * 100.0
	}

	fmt.Println("\n================================================================")
	fmt.Println("             MIDCPNIFTY OPTIONS BOT SEEDING REPORT             ")
	fmt.Println("================================================================")
	fmt.Printf(" Total Simulated Trades: %d\n", totalTrades)
	fmt.Printf(" Winning Trades        : %d\n", winningTrades)
	fmt.Printf(" Losing Trades         : %d\n", losingTrades)
	fmt.Printf(" WIN RATE              : %.2f%%\n", winRate)
	fmt.Printf(" Net Realized PnL      : INR ₹%.2f\n", totalPnL)
	fmt.Printf(" Final Paper Balance   : INR ₹%.2f\n", newBalance)
	fmt.Printf(" Final Trend           : %s\n", finalTrend)
	fmt.Println("================================================================")
	fmt.Println(" MIDCPNIFTY paper trades successfully seeded into PostgreSQL DB!")
}
