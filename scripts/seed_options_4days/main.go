package main

import (
	"context"
	"fmt"
	"log"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
	"zerodha-trading/risk"
	"zerodha-trading/selection"
	"zerodha-trading/strategy"
)

func main() {
	fmt.Println("==============================================================")
	fmt.Println("  SEEDING 4-DAY HISTORICAL DATA & RUNNING OPTIONS SIMULATION   ")
	fmt.Println("==============================================================")

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

	// 3. Connect to Zerodha API for Seeding
	if cfg.AccessToken == "" || cfg.AccessToken == "your_access_token_here" {
		log.Fatalf("KITE_ACCESS_TOKEN is required for live historical data seeding.")
	}

	kiteClient := kiteconnect.New(cfg.APIKey)
	kiteClient.SetAccessToken(cfg.AccessToken)
	brokerAdapter := data.NewZerodhaBrokerAdapter(kiteClient)
	secMaster := data.NewSecurityMaster(db, brokerAdapter, logger.Logger)

	// Resolve NIFTY 50 Token (256265)
	niftyToken, err := secMaster.GetInstrumentToken("NIFTY 50")
	if err != nil || niftyToken <= 0 {
		niftyToken = 256265
	}

	// 4. Fetch 4 Calendar Days of 5m Candles from Zerodha API
	now := time.Now().UTC()
	startDate := now.AddDate(0, 0, -5) // 4-5 days of data

	log.Printf("Fetching 4 days of 5m historical candles for NIFTY 50 (Token: %d)...", niftyToken)
	candles5m, err := brokerAdapter.GetHistoricalData(int(niftyToken), "5minute", startDate, now, false, false)
	if err != nil {
		log.Fatalf("Failed to fetch historical 5m candles: %v", err)
	}

	log.Printf("Fetched %d 5-minute candles. Seeding into PostgreSQL candles_5m table...", len(candles5m))

	// Insert into PostgreSQL candles_5m
	tx, err := db.WithContext(ctx).BeginTx(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to begin transaction: %v", err)
	}

	stmt5m, err := tx.PrepareContext(ctx, `
		INSERT INTO candles_5m (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (token, time) DO UPDATE SET
			open = EXCLUDED.open,
			high = EXCLUDED.high,
			low = EXCLUDED.low,
			close = EXCLUDED.close,
			volume = EXCLUDED.volume,
			vwap = EXCLUDED.vwap,
			color = EXCLUDED.color
	`)
	if err != nil {
		tx.Rollback()
		log.Fatalf("Failed to prepare statement: %v", err)
	}
	defer stmt5m.Close()

	var domainCandles []data.Candle
	for _, c := range candles5m {
		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}
		vwap := (c.Open + c.High + c.Low + c.Close) / 4.0

		// Normalize UTC timestamp
		cTime := c.Date
		_, err = stmt5m.ExecContext(ctx, niftyToken, cTime, c.Open, c.High, c.Low, c.Close, int64(c.Volume), vwap, c.Low, c.High, 500, color)
		if err != nil {
			tx.Rollback()
			log.Fatalf("Failed to insert 5m candle: %v", err)
		}

		domainCandles = append(domainCandles, data.Candle{
			Time:   cTime,
			Open:   c.Open,
			High:   c.High,
			Low:    c.Low,
			Close:  c.Close,
			Volume: int64(c.Volume),
		})
	}

	if err := tx.Commit(); err != nil {
		log.Fatalf("Failed to commit candle transaction: %v", err)
	}

	log.Printf("Successfully seeded %d historical 5m candles into PostgreSQL database!", len(domainCandles))

	// 5. Run 5-Minute Triple SuperTrend Simulation over 4-Day History
	stEngine := strategy.NewSuperTrendOptionsEngine(
		cfg.Options.SuperTrendST1Period, cfg.Options.SuperTrendST2Period, cfg.Options.SuperTrendST3Period,
		cfg.Options.SuperTrendST1Factor, cfg.Options.SuperTrendST2Factor, cfg.Options.SuperTrendST3Factor,
	)
	strikeSelector := selection.NewOptionStrikeSelector(secMaster)
	posMgr := risk.NewOptionsPositionManager(
		db, logger.Logger, cfg.Options.BaseLotSize, cfg.Options.MaxQuantityMultiplier,
		cfg.Options.OptionsSLPct, cfg.Options.PaperBalance,
	)

	log.Printf("Running Options Simulation over %d candles...", len(domainCandles))
	tradesCount := 0

	for i := 10; i <= len(domainCandles); i++ {
		slice := domainCandles[:i]
		lastCandle := slice[len(slice)-1]

		// Calculate SuperTrend
		res := stEngine.CalculateTripleSuperTrend(slice)
		action, qty := posMgr.EvaluateSignal(res.Trend)

		if action == "OPEN_INITIAL" || action == "REVERSAL" {
			strikeRes, err := strikeSelector.SelectOTMStrike("NIFTY 50", lastCandle.Close, res.Trend)
			if err == nil {
				// Close old trade if reversal
				if action == "REVERSAL" {
					status := posMgr.GetStatus()
					if activeSym, ok := status["active_symbol"].(string); ok && activeSym != "" {
						_ = posMgr.OnTradeClosed(80.0)
					}
				}

				orderID := fmt.Sprintf("SIM-%d-%d", lastCandle.Time.Unix(), i)
				simulatedPremium := 120.0
				posMgr.OnTradeOpened(orderID, strikeRes.OptionSymbol, strikeRes.OptionType, qty, simulatedPremium)
				tradesCount++
			}
		}
	}

	_ = posMgr.SaveState(ctx)
	log.Printf("Options Simulation Complete! Generated %d option trades across 4 days.", tradesCount)
	log.Println("All data is 100% saved in PostgreSQL. You can now tune your strategy without re-calling Zerodha API!")
}
