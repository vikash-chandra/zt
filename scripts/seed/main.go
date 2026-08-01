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
	log.Println("==============================================================")
	log.Println("  CLEAN TRUNCATE & RE-SEED HISTORICAL CANDLES FROM 27/07/2026 ")
	log.Println("==============================================================")

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

	ctx := context.Background()

	// 1. TRUNCATE ALL CANDLE TABLES BEFORE RESEEDING
	log.Println("🧹 Truncating all existing candle data from candles_5m and candles_1m...")
	_, err = db.WithContext(ctx).ExecContext(ctx, "TRUNCATE TABLE candles_5m, candles_1m;")
	if err != nil {
		log.Fatalf("Failed to truncate candle tables: %v", err)
	}
	log.Println("✅ Candle tables truncated successfully.")

	// Verify Kite credentials
	if cfg.AccessToken == "" || cfg.AccessToken == "your_access_token_here" {
		log.Fatalf("KITE_ACCESS_TOKEN is not configured. Live historical seeding requires a valid access token.")
	}

	// Create Kite Client
	rawKiteClient := kiteconnect.New(cfg.APIKey)
	rawKiteClient.SetAccessToken(cfg.AccessToken)
	kiteClient := data.NewZerodhaBrokerAdapter(rawKiteClient)

	// Create security master
	securityMaster := data.NewSecurityMaster(db, kiteClient, logger.Logger)

	// Fetch Nifty 50 constituents from Zerodha Connect API
	watchlist, err := securityMaster.GetNifty50Constituents(ctx)
	if err != nil {
		log.Fatalf("Failed to fetch Nifty 50 constituents: %v", err)
	}
	watchlist["NIFTY 50"] = 256265

	log.Printf("Seeding historical candle data for %d Nifty 50 instruments starting from 27/07/2026...", len(watchlist))

	// Define time bounds: 27/07/2026 00:00:00 IST (2026-07-26 18:30:00 UTC) to NOW
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.FixedZone("IST", 5.5*3600)
	}
	startDate := time.Date(2026, 7, 27, 0, 0, 0, 0, loc).UTC()
	now := time.Now().UTC()

	total1mInserted := 0
	total5mInserted := 0

	tx, err := db.WithContext(ctx).BeginTx(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to begin transaction: %v", err)
	}

	stmt1m, err := tx.PrepareContext(ctx, `
		INSERT INTO candles_1m (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (token, time) DO UPDATE SET
			open = EXCLUDED.open, high = EXCLUDED.high, low = EXCLUDED.low, close = EXCLUDED.close, volume = EXCLUDED.volume, vwap = EXCLUDED.vwap, color = EXCLUDED.color
	`)
	if err != nil {
		tx.Rollback()
		log.Fatalf("Failed to prepare statement 1m: %v", err)
	}
	defer stmt1m.Close()

	stmt5m, err := tx.PrepareContext(ctx, `
		INSERT INTO candles_5m (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (token, time) DO UPDATE SET
			open = EXCLUDED.open, high = EXCLUDED.high, low = EXCLUDED.low, close = EXCLUDED.close, volume = EXCLUDED.volume, vwap = EXCLUDED.vwap, color = EXCLUDED.color
	`)
	if err != nil {
		tx.Rollback()
		log.Fatalf("Failed to prepare statement 5m: %v", err)
	}
	defer stmt5m.Close()

	// Fetch and insert historical candles
	for symbol, token := range watchlist {
		log.Printf("Fetching real historical candles for %s (Token: %d)...", symbol, token)

		// Fetch 1m candles
		candles1m, err := kiteClient.GetHistoricalData(int(token), "minute", startDate, now, false, false)
		if err != nil {
			log.Printf("Warning: Failed to fetch 1m historical data for %s: %v", symbol, err)
		} else {
			for _, c := range candles1m {
				if !data.IsMarketHoursCandle(c.Date) {
					continue
				}
				color := "DOJI"
				if c.Close > c.Open {
					color = "GREEN"
				} else if c.Close < c.Open {
					color = "RED"
				}
				vwap := (c.Open + c.High + c.Low + c.Close) / 4.0

				_, err = stmt1m.ExecContext(ctx, token, c.Date, c.Open, c.High, c.Low, c.Close, int64(c.Volume), vwap, c.Low, c.High, 100, color)
				if err == nil {
					total1mInserted++
				}
			}
		}

		// Fetch 5m candles
		candles5m, err := kiteClient.GetHistoricalData(int(token), "5minute", startDate, now, false, false)
		if err != nil {
			log.Printf("Warning: Failed to fetch 5m historical data for %s: %v", symbol, err)
		} else {
			for _, c := range candles5m {
				if !data.IsMarketHoursCandle(c.Date) {
					continue
				}
				color := "DOJI"
				if c.Close > c.Open {
					color = "GREEN"
				} else if c.Close < c.Open {
					color = "RED"
				}
				vwap := (c.Open + c.High + c.Low + c.Close) / 4.0

				_, err = stmt5m.ExecContext(ctx, token, c.Date, c.Open, c.High, c.Low, c.Close, int64(c.Volume), vwap, c.Low, c.High, 500, color)
				if err == nil {
					total5mInserted++
				}
			}
		}

		// Sleep to respect Zerodha API rate limits (3 requests per second limit)
		time.Sleep(350 * time.Millisecond)
	}

	if err := tx.Commit(); err != nil {
		log.Fatalf("Failed to commit transaction: %v", err)
	}

	log.Printf("🎉 Clean Re-seed Complete! Seeded %d 1-minute and %d 5-minute candles starting from 27/07/2026!", total1mInserted, total5mInserted)
}
