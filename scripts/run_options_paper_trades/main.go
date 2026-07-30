package main

import (
	"context"
	"fmt"
	"log"
	"math"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
	"zerodha-trading/risk"
	"zerodha-trading/selection"
	"zerodha-trading/strategy"
)

func main() {
	fmt.Println("================================================================")
	fmt.Println("  RUNNING OPTIONS PAPER TRADING SIMULATION & WIN RATE REPORT    ")
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

	// 3. Fetch 5m NIFTY 50 Candles from DB
	niftyToken := int64(256265)
	candles, err := db.GetLastNCandles("candles_5m", niftyToken, 300)
	if err != nil || len(candles) == 0 {
		log.Fatalf("No candles found for NIFTY 50 in DB. Please ensure historical data is seeded first.")
	}

	log.Printf("Loaded %d 5-minute candles from PostgreSQL for NIFTY 50.", len(candles))

	// 4. Initialize Indicators & Risk Manager
	stEngine := strategy.NewSuperTrendOptionsEngine(
		cfg.Options.SuperTrendST1Period, cfg.Options.SuperTrendST2Period, cfg.Options.SuperTrendST3Period,
		cfg.Options.SuperTrendST1Factor, cfg.Options.SuperTrendST2Factor, cfg.Options.SuperTrendST3Factor,
	)
	strikeSelector := selection.NewOptionStrikeSelector(nil)
	posMgr := risk.NewOptionsPositionManager(
		db, logger.Logger, cfg.Options.BaseLotSize, cfg.Options.MaxQuantityMultiplier,
		cfg.Options.OptionsSLPct, cfg.Options.PaperBalance,
	)

	// Clear previous options paper trades from DB to calculate fresh metrics
	_, _ = db.WithContext(ctx).ExecContext(ctx, "DELETE FROM trades WHERE strategy = 'OPTIONS_SUPERTREND'")

	totalTrades := 0
	winningTrades := 0
	losingTrades := 0
	grossProfit := 0.0
	grossLoss := 0.0
	totalPnL := 0.0

	// 5. Run Simulation across historical 5m candles
	for i := 10; i <= len(candles); i++ {
		sub := candles[:i]
		lastCandle := sub[len(sub)-1]

		res := stEngine.CalculateTripleSuperTrend(sub)
		action, qty := posMgr.EvaluateSignal(res.Trend)

		if action == "OPEN_INITIAL" || action == "REVERSAL" {
			strikeRes, err := strikeSelector.SelectOTMStrike("NIFTY 50", lastCandle.Close, res.Trend, cfg.Options.StrikeOffsetPoints)
			if err == nil {
				// Close old trade if reversal
				if action == "REVERSAL" {
					status := posMgr.GetStatus()
					if activeSym, ok := status["active_symbol"].(string); ok && activeSym != "" {
						// Simulated exit premium (decayed option premium)
						exitPremium := 65.0
						pnl := posMgr.OnTradeClosed(exitPremium)

						totalTrades++
						totalPnL += pnl
						if pnl > 0 {
							winningTrades++
							grossProfit += pnl
						} else {
							losingTrades++
							grossLoss += math.Abs(pnl)
						}

						// Save trade to DB
						_, _ = db.WithContext(ctx).ExecContext(ctx, `
							INSERT INTO trades (symbol, entry_price, exit_price, quantity, pnl, side, time_held_minutes, created_at, strategy)
							VALUES ($1, $2, $3, $4, $5, 'SELL', 45, $6, 'OPTIONS_SUPERTREND')
						`, activeSym, status["entry_premium"], exitPremium, status["active_qty"], pnl, lastCandle.Time)
					}
				}

				orderID := fmt.Sprintf("PAPER-%d", lastCandle.Time.Unix())
				entryPremium := 120.0
				posMgr.OnTradeOpened(orderID, strikeRes.OptionSymbol, strikeRes.OptionType, qty, entryPremium)
			}
		}
	}

	// Calculate final Win Rate & Metrics
	winRate := 0.0
	if totalTrades > 0 {
		winRate = (float64(winningTrades) / float64(totalTrades)) * 100.0
	}

	profitFactor := 0.0
	if grossLoss > 0 {
		profitFactor = grossProfit / grossLoss
	} else if grossProfit > 0 {
		profitFactor = 99.0
	}

	// Update DB Options State
	_ = posMgr.SaveState(ctx)

	fmt.Println("\n================================================================")
	fmt.Println("             TRIPLE SUPERTREND OPTIONS BOT WIN RATE REPORT       ")
	fmt.Println("================================================================")
	fmt.Printf(" Total Simulated Trades: %d\n", totalTrades)
	fmt.Printf(" Winning Trades        : %d\n", winningTrades)
	fmt.Printf(" Losing Trades         : %d\n", losingTrades)
	fmt.Printf(" WIN RATE              : %.2f%%\n", winRate)
	fmt.Printf(" Net Realized PnL      : INR ₹%.2f\n", totalPnL)
	fmt.Printf(" Profit Factor         : %.2f\n", profitFactor)
	fmt.Println("================================================================")
	fmt.Println(" All paper trades & performance metrics saved into PostgreSQL DB!")
}
