package main

import (
	"context"
	"log"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
)

func main() {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create logger
	logger, err := monitoring.NewLogger(cfg.LogLevel)
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}

	// Connect to database
	db, err := data.NewDatabase(
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		logger.Logger,
	)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	// Initialize schema if not already initialized
	if err := db.InitSchema(); err != nil {
		log.Fatalf("Schema initialization failed: %v", err)
	}

	// Verify Kite credentials
	if cfg.AccessToken == "" || cfg.AccessToken == "your_access_token_here" {
		log.Fatalf("KITE_ACCESS_TOKEN is not configured. Live historical seeding requires a valid access token.")
	}

	// Create Kite Client
	kiteClient := kiteconnect.New(cfg.APIKey)
	kiteClient.SetAccessToken(cfg.AccessToken)

	// Create security master (uses DB context and Kite client)
	ctx := context.Background()
	securityMaster := data.NewSecurityMaster(db.WithContext(ctx), kiteClient, logger.Logger)

	// Fetch Nifty 50 constituents from Zerodha Connect API
	watchlist, err := securityMaster.GetNifty50Constituents(ctx)
	if err != nil {
		log.Fatalf("Failed to fetch Nifty 50 constituents: %v", err)
	}

	log.Printf("Seeding 1 week of live historical data for %d Nifty 50 instruments...", len(watchlist))

	// Define time bounds (last 7 calendar days)
	now := time.Now().UTC()
	startDate := now.AddDate(0, 0, -7)

	total1mInserted := 0
	total5mInserted := 0

	tx, err := db.WithContext(ctx).BeginTx(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to begin transaction: %v", err)
	}

	stmt1m, err := tx.PrepareContext(ctx, `
		INSERT INTO candles_1m (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (token, time) DO NOTHING
	`)
	if err != nil {
		tx.Rollback()
		log.Fatalf("Failed to prepare statement 1m: %v", err)
	}
	defer stmt1m.Close()

	stmt5m, err := tx.PrepareContext(ctx, `
		INSERT INTO candles_5m (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (token, time) DO NOTHING
	`)
	if err != nil {
		tx.Rollback()
		log.Fatalf("Failed to prepare statement 5m: %v", err)
	}
	defer stmt5m.Close()

	// Format dates for historical API
	for symbol, token := range watchlist {
		log.Printf("Fetching real historical candles for %s (Token: %d)...", symbol, token)

		// Fetch 1m candles
		candles1m, err := kiteClient.GetHistoricalData(int(token), "minute", startDate, now, false, false)
		if err != nil {
			tx.Rollback()
			log.Fatalf("Failed to fetch 1m historical data for %s: %v", symbol, err)
		}

		// Fetch 5m candles
		candles5m, err := kiteClient.GetHistoricalData(int(token), "5minute", startDate, now, false, false)
		if err != nil {
			tx.Rollback()
			log.Fatalf("Failed to fetch 5m historical data for %s: %v", symbol, err)
		}

		// Insert 1m candles
		for _, c := range candles1m {
			color := "DOJI"
			if c.Close > c.Open {
				color = "GREEN"
			} else if c.Close < c.Open {
				color = "RED"
			}
			vwap := (c.Open + c.High + c.Low + c.Close) / 4.0

			_, err = stmt1m.ExecContext(ctx, token, c.Date.Time, c.Open, c.High, c.Low, c.Close, int64(c.Volume), vwap, c.Low, c.High, 100, color)
			if err != nil {
				tx.Rollback()
				log.Fatalf("Failed to insert live 1m candle: %v", err)
			}
			total1mInserted++
		}

		// Insert 5m candles
		for _, c := range candles5m {
			color := "DOJI"
			if c.Close > c.Open {
				color = "GREEN"
			} else if c.Close < c.Open {
				color = "RED"
			}
			vwap := (c.Open + c.High + c.Low + c.Close) / 4.0

			_, err = stmt5m.ExecContext(ctx, token, c.Date.Time, c.Open, c.High, c.Low, c.Close, int64(c.Volume), vwap, c.Low, c.High, 500, color)
			if err != nil {
				tx.Rollback()
				log.Fatalf("Failed to insert live 5m candle: %v", err)
			}
			total5mInserted++
		}

		// Sleep to respect Zerodha API rate limits (3 requests per second limit)
		time.Sleep(350 * time.Millisecond)
	}

	if err := tx.Commit(); err != nil {
		log.Fatalf("Failed to commit transaction: %v", err)
	}

	log.Printf("Successfully fetched and seeded %d 1-minute and %d 5-minute REAL historical candles from Zerodha API!", total1mInserted, total5mInserted)
}
