package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

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

	brokerClient := data.NewZerodhaBrokerAdapter(cfg.APIKey, cfg.AccessToken, logger.Logger)
	token := int64(897537) // TITAN

	loc, _ := time.LoadLocation("Asia/Kolkata")
	fromDate := time.Date(2026, 8, 28, 9, 15, 0, 0, loc)
	toDate := time.Date(2026, 8, 28, 15, 30, 0, 0, loc)

	log.Println("Fetching 1m candles for TITAN...")
	c1m, err := brokerClient.GetHistoricalCandles(ctx, token, "minute", fromDate, toDate)
	if err != nil {
		log.Printf("Error fetching 1m: %v", err)
	} else {
		log.Printf("Fetched %d 1m candles", len(c1m))
		for _, c := range c1m {
			_ = db.InsertCandle(ctx, "candles_1m", token, c)
		}
	}

	log.Println("Fetching 5m candles for TITAN...")
	c5m, err := brokerClient.GetHistoricalCandles(ctx, token, "5minute", fromDate, toDate)
	if err != nil {
		log.Printf("Error fetching 5m: %v", err)
	} else {
		log.Printf("Fetched %d 5m candles", len(c5m))
		for _, c := range c5m {
			_ = db.InsertCandle(ctx, "candles_5m", token, c)
		}
	}

	b1m, _ := json.MarshalIndent(c1m, "", "  ")
	_ = os.WriteFile("titan_1m.json", b1m, 0644)
	b5m, _ := json.MarshalIndent(c5m, "", "  ")
	_ = os.WriteFile("titan_5m.json", b5m, 0644)

	fmt.Println("Done seeding TITAN!")
}
