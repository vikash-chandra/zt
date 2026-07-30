package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
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
		"index":          cfg.Options.IndexSymbol,
		"base_lot":       cfg.Options.BaseLotSize,
		"strike_offset":  cfg.Options.StrikeOffsetPoints,
		"sl_pct":         cfg.Options.OptionsSLPct,
		"max_multiplier": cfg.Options.MaxQuantityMultiplier,
	})

	// 5. Signal Listener & 5m Candle Aggregation Loop
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

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
			// Fetch instrument token for NIFTY 50 index
			token, err := secMaster.GetInstrumentToken(cfg.Options.IndexSymbol)
			if err != nil || token <= 0 {
				token = 256265 // NIFTY 50 Zerodha index token
			}

			// Fetch recent 5m index candles for NIFTY 50
			candles, err := db.GetLastNCandles("candles_5m", token, 50)
			if err != nil || len(candles) < 10 {
				continue
			}

			// Evaluate Triple SuperTrend on 5m completed candle closes
			res := stEngine.CalculateTripleSuperTrend(candles)
			action, qty := posMgr.EvaluateSignal(res.Trend)

			if action == "OPEN_INITIAL" || action == "REVERSAL" {
				// Get latest index spot price
				lastSpot := candles[len(candles)-1].Close

				// Select OTM Strike
				strikeRes, err := strikeSelector.SelectOTMStrike(cfg.Options.IndexSymbol, lastSpot, res.Trend, cfg.Options.StrikeOffsetPoints)
				if err != nil {
					logger.Error("Failed to select OTM strike", map[string]interface{}{"error": err.Error()})
					continue
				}

				// If Reversal action: square off active trade first
				if action == "REVERSAL" {
					status := posMgr.GetStatus()
					if activeSym, ok := status["active_symbol"].(string); ok && activeSym != "" {
						_, _, _ = optionsExec.ExecuteOptionOrder(activeSym, "BUY", status["active_qty"].(int), status["latest_price"].(float64))
						_ = posMgr.OnTradeClosed(status["latest_price"].(float64))
					}
				}

				// Execute New Option Selling Trade
				simulatedPremium := 100.0 // Default premium for paper simulation
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
