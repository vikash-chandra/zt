package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Config load error: %v", err)
	}

	logger, _ := monitoring.NewLogger(cfg.LogLevel)
	db, err := data.NewDatabase(cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode, logger.Logger)
	if err != nil {
		log.Fatalf("DB error: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	var dbToken string
	err = db.WithContext(ctx).QueryRowContext(ctx, "SELECT value FROM metadata_cache WHERE key = 'config:kite_access_token'").Scan(&dbToken)
	if err == nil && dbToken != "" {
		cfg.AccessToken = dbToken
	}

	rawKiteClient := kiteconnect.New(cfg.APIKey)
	rawKiteClient.SetAccessToken(cfg.AccessToken)
	kiteClient := data.NewZerodhaBrokerAdapter(rawKiteClient)
	token := 897537 // TITAN

	loc, _ := time.LoadLocation("Asia/Kolkata")
	fromDate := time.Date(2026, 8, 28, 9, 15, 0, 0, loc)
	toDate := time.Date(2026, 8, 28, 15, 30, 0, 0, loc)

	log.Println("Fetching 1m candles for TITAN...")
	c1m, err := kiteClient.GetHistoricalData(token, "minute", fromDate, toDate, false, false)
	if err != nil {
		log.Printf("Error fetching 1m: %v", err)
	} else {
		log.Printf("Fetched %d 1m candles", len(c1m))
		for _, c := range c1m {
			color := "DOJI"
			if c.Close > c.Open {
				color = "GREEN"
			} else if c.Close < c.Open {
				color = "RED"
			}
			vwap := (c.Open + c.High + c.Low + c.Close) / 4.0
			_, _ = db.WithContext(ctx).ExecContext(ctx, `
				INSERT INTO candles_1m (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
				ON CONFLICT (token, time) DO UPDATE SET open = EXCLUDED.open, high = EXCLUDED.high, low = EXCLUDED.low, close = EXCLUDED.close, volume = EXCLUDED.volume, vwap = EXCLUDED.vwap, color = EXCLUDED.color
			`, token, c.Date, c.Open, c.High, c.Low, c.Close, int64(c.Volume), vwap, c.Low, c.High, 100, color)
		}
	}

	log.Println("Fetching 5m candles for TITAN...")
	c5m, err := kiteClient.GetHistoricalData(token, "5minute", fromDate, toDate, false, false)
	if err != nil {
		log.Printf("Error fetching 5m: %v", err)
	} else {
		log.Printf("Fetched %d 5m candles", len(c5m))
		for _, c := range c5m {
			color := "DOJI"
			if c.Close > c.Open {
				color = "GREEN"
			} else if c.Close < c.Open {
				color = "RED"
			}
			vwap := (c.Open + c.High + c.Low + c.Close) / 4.0
			_, _ = db.WithContext(ctx).ExecContext(ctx, `
				INSERT INTO candles_5m (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
				ON CONFLICT (token, time) DO UPDATE SET open = EXCLUDED.open, high = EXCLUDED.high, low = EXCLUDED.low, close = EXCLUDED.close, volume = EXCLUDED.volume, vwap = EXCLUDED.vwap, color = EXCLUDED.color
			`, token, c.Date, c.Open, c.High, c.Low, c.Close, int64(c.Volume), vwap, c.Low, c.High, 500, color)
		}
	}

	b1m, _ := json.MarshalIndent(c1m, "", "  ")
	_ = os.WriteFile("titan_1m.json", b1m, 0644)
	b5m, _ := json.MarshalIndent(c5m, "", "  ")
	_ = os.WriteFile("titan_5m.json", b5m, 0644)

	fmt.Println("Done seeding TITAN!")
}
