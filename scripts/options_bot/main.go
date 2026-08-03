package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/execution"
	"zerodha-trading/monitoring"
	"zerodha-trading/risk"
	"zerodha-trading/selection"
	"zerodha-trading/strategy"

	"github.com/zerodha/gokiteconnect/v4"
)

func main() {
	fmt.Println("==================================================")
	fmt.Println("  5-MINUTE TRIPLE SUPERTREND OPTIONS BOT (GO)     ")
	fmt.Println("==================================================")

	// 1. Load Configurations
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("Failed to load settings: %v\n", err)
		os.Exit(1)
	}

	logger, err := monitoring.NewLogger(cfg.LogLevel)
	if err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 2. Initialize Database Connection
	db, err := data.NewDatabase(cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode, logger.Logger)
	if err != nil {
		logger.Error("Database connection failed", map[string]interface{}{"error": err.Error()})
		os.Exit(1)
	}
	if err := db.InitSchema(); err != nil {
		logger.Error("Database schema initialization failed", map[string]interface{}{"error": err.Error()})
		os.Exit(1)
	}

	// 3. Initialize Zerodha Client & Security Master
	rawKiteClient := kiteconnect.New(cfg.APIKey)
	rawKiteClient.SetAccessToken(cfg.AccessToken)
	kiteClient := data.NewZerodhaBrokerAdapter(rawKiteClient)
	secMaster := data.NewSecurityMaster(db, kiteClient, logger.Logger)

	// 4. Initialize Core Components
	stEngine := strategy.NewSuperTrendOptionsEngine(
		cfg.Options.SuperTrendST1Period, cfg.Options.SuperTrendST2Period, cfg.Options.SuperTrendST3Period,
		cfg.Options.SuperTrendST1Factor, cfg.Options.SuperTrendST2Factor, cfg.Options.SuperTrendST3Factor,
	)
	strikeSelector := selection.NewOptionStrikeSelector(secMaster)
	posMgr := risk.NewOptionsPositionManager(
		db, logger.Logger, cfg.Options.BaseLotSize, cfg.Options.MaxQuantityMultiplier,
		cfg.Options.OptionsSLPct, cfg.Options.PaperBalance,
	)
	if err := posMgr.LoadState(ctx); err != nil {
		logger.Warn("Failed to load options state from DB", map[string]interface{}{"error": err.Error()})
	}

	optionsExec := execution.NewOptionsExecutor(kiteClient, logger.Logger, cfg.Options.LiveTrading)

	modeStr := "DUMMY MODE (PAPER TRADING)"
	if cfg.Options.LiveTrading {
		modeStr = "REAL LIVE TRADING (ZERODHA EXCHANGE)"
	}
	logger.Info(fmt.Sprintf("Options Bot initialized in %s", modeStr), map[string]interface{}{
		"index":                 cfg.Options.IndexSymbol,
		"base_lot":              cfg.Options.BaseLotSize,
		"strike_offset":         cfg.Options.StrikeOffsetPoints,
		"sl_pct":                cfg.Options.OptionsSLPct,
		"max_multiplier":        cfg.Options.MaxQuantityMultiplier,
		"auto_square_off_time":  cfg.Options.AutoSquareOffTime,
	})

	// 5. Signal Listener & 5m Candle Aggregation Loop
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	loc, _ := time.LoadLocation("Asia/Kolkata")
	if loc == nil {
		loc = time.Local
	}

	var lastSeenDay string

	for {
		select {
		case <-ctx.Done():
			logger.Info("Options Bot shutting down gracefully...", nil)
			return
		case <-sigChan:
			logger.Info("Signal received, stopping Options Bot...", nil)
			_ = posMgr.SaveState(context.Background())
			return
		case <-ticker.C:
			nowIST := time.Now().In(loc)
			dayStr := nowIST.Format("2006-01-02")

			// Check day boundary reset
			if lastSeenDay != "" && dayStr != lastSeenDay {
				posMgr.ResetDailyMultiplier()
			}
			lastSeenDay = dayStr

			// Parse Auto Square-off Time from config (default 15:15)
			sqHour, sqMin := 15, 15
			if parts := strings.Split(cfg.Options.AutoSquareOffTime, ":"); len(parts) == 2 {
				fmt.Sscanf(parts[0], "%d", &sqHour)
				fmt.Sscanf(parts[1], "%d", &sqMin)
			}
			isEOD := (nowIST.Hour() > sqHour) || (nowIST.Hour() == sqHour && nowIST.Minute() >= sqMin)

			// Parse Last New Trade Time from config (default 15:00)
			lastH, lastM := 15, 0
			if parts := strings.Split(cfg.Options.LastNewTradeTime, ":"); len(parts) == 2 {
				fmt.Sscanf(parts[0], "%d", &lastH)
				fmt.Sscanf(parts[1], "%d", &lastM)
			}
			isPastLastNewTradeTime := (nowIST.Hour() > lastH) || (nowIST.Hour() == lastH && nowIST.Minute() >= lastM)

			// 1. If Position Active: Check 50% Stop-Loss & EOD Auto Square-Off
			status := posMgr.GetStatus()
			activeSym, _ := status["active_symbol"].(string)
			hasActive := activeSym != ""

			if hasActive {
				activeQty, _ := status["active_qty"].(int)
				entryPrem, _ := status["entry_premium"].(float64)

				// Fetch live quote for active option contract if in live mode
				ltp := entryPrem
				if cfg.Options.LiveTrading {
					if quotes, err := kiteClient.GetQuote("NFO:" + activeSym); err == nil {
						if q, ok := quotes["NFO:"+activeSym]; ok && q.LastPrice > 0 {
							ltp = q.LastPrice
						}
					}
				}

				// Check 50% SL breach
				if posMgr.CheckTick(ltp) {
					logger.Warn("[SL-HIT] Option premium breached 50% SL!", map[string]interface{}{"symbol": activeSym, "ltp": ltp})
					_, fillPrice, err := optionsExec.ExecuteOptionOrder(activeSym, "BUY", activeQty, ltp)
					if err == nil {
						realizedLoss := posMgr.OnSLHit(fillPrice)
						_, _ = db.WithContext(ctx).ExecContext(ctx, `
							INSERT INTO trades (symbol, entry_price, exit_price, quantity, pnl, side, time_held_minutes, created_at, strategy)
							VALUES ($1, $2, $3, $4, $5, 'SELL', $6, $7, 'OPTIONS_SUPERTREND')
						`, activeSym, entryPrem, fillPrice, activeQty, realizedLoss, 5, nowIST)
						_ = posMgr.SaveState(ctx)
						hasActive = false
					}
				} else if isEOD {
					// Check EOD Auto Square-Off
					logger.Info("[EOD AUTO SQUARE-OFF] Closing active option position for EOD", map[string]interface{}{"symbol": activeSym})
					_, fillPrice, err := optionsExec.ExecuteOptionOrder(activeSym, "BUY", activeQty, ltp)
					if err == nil {
						pnl := posMgr.OnTradeClosed(fillPrice)
						_, _ = db.WithContext(ctx).ExecContext(ctx, `
							INSERT INTO trades (symbol, entry_price, exit_price, quantity, pnl, side, time_held_minutes, created_at, strategy)
							VALUES ($1, $2, $3, $4, $5, 'SELL', $6, $7, 'OPTIONS_SUPERTREND')
						`, activeSym, entryPrem, fillPrice, activeQty, pnl, 15, nowIST)
						_ = posMgr.SaveState(ctx)
						hasActive = false
					}
				}
			}

			// 2. Fetch NIFTY 50 candles & evaluate SuperTrend signals
			token, err := secMaster.GetInstrumentToken(cfg.Options.IndexSymbol)
			if err != nil || token <= 0 {
				token = 256265 // NIFTY 50 Zerodha index token
			}
			candles, err := db.GetLastNCandles("candles_5m", token, 50)
			if err != nil || len(candles) < 10 {
				continue
			}

			res := stEngine.CalculateTripleSuperTrend(candles)
			action, qty := posMgr.EvaluateSignal(res.Trend)

			if !isEOD && !isPastLastNewTradeTime && (action == "OPEN_INITIAL" || action == "REVERSAL") {
				lastSpot := candles[len(candles)-1].Close
				strikeRes, err := strikeSelector.SelectOTMStrike(cfg.Options.IndexSymbol, lastSpot, res.Trend, cfg.Options.StrikeOffsetPoints)
				if err != nil {
					logger.Error("Failed to select OTM strike", map[string]interface{}{"error": err.Error()})
					continue
				}

				// If Reversal action: square off active trade first
				if action == "REVERSAL" && hasActive {
					activeQty, _ := status["active_qty"].(int)
					entryPrem, _ := status["entry_premium"].(float64)
					exitPrem := 65.0
					if cfg.Options.LiveTrading {
						if quotes, err := kiteClient.GetQuote("NFO:" + activeSym); err == nil {
							if q, ok := quotes["NFO:"+activeSym]; ok && q.LastPrice > 0 {
								exitPrem = q.LastPrice
							}
						}
					}

					_, fillPrice, err := optionsExec.ExecuteOptionOrder(activeSym, "BUY", activeQty, exitPrem)
					if err == nil {
						pnl := posMgr.OnTradeClosed(fillPrice)
						_, _ = db.WithContext(ctx).ExecContext(ctx, `
							INSERT INTO trades (symbol, entry_price, exit_price, quantity, pnl, side, time_held_minutes, created_at, strategy)
							VALUES ($1, $2, $3, $4, $5, 'SELL', $6, $7, 'OPTIONS_SUPERTREND')
						`, activeSym, entryPrem, fillPrice, activeQty, pnl, 45, nowIST)
					}
				}

				// Execute New Option Selling Trade
				simulatedPremium := 120.0
				if cfg.Options.LiveTrading {
					if quotes, err := kiteClient.GetQuote("NFO:" + strikeRes.OptionSymbol); err == nil {
						if q, ok := quotes["NFO:"+strikeRes.OptionSymbol]; ok && q.LastPrice > 0 {
							simulatedPremium = q.LastPrice
						}
					}
				}

				orderID, fillPrice, err := optionsExec.ExecuteOptionOrder(strikeRes.OptionSymbol, "SELL", qty, simulatedPremium)
				if err != nil {
					logger.Error("Failed to execute option order", map[string]interface{}{"error": err.Error(), "symbol": strikeRes.OptionSymbol})
					continue
				}

				posMgr.OnTradeOpened(orderID, strikeRes.OptionSymbol, strikeRes.OptionType, qty, fillPrice)
				_ = posMgr.SaveState(ctx)
			}
		}
	}
}
