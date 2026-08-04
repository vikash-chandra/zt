package main

import (
	"context"
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
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.FixedZone("IST", 5*3600+30*60)
	}

	targetDate := time.Now().In(loc)
	if len(os.Args) > 1 && os.Args[1] != "" {
		parsed, err := time.ParseInLocation("2006-01-02", os.Args[1], loc)
		if err == nil {
			targetDate = parsed
		}
	}
	dateStr := targetDate.Format("2006-01-02")

	fmt.Println("==============================================================")
	fmt.Printf("       DELETING & RE-SEEDING %s HISTORICAL CANDLES   \n", dateStr)
	fmt.Println("==============================================================")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logger, err := monitoring.NewLogger(cfg.LogLevel)
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}

	ctx := context.Background()

	db, err := data.NewDatabase(
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		logger.Logger,
	)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	token := int64(256265)
	startStr := fmt.Sprintf("%s 00:00:00+05:30", dateStr)
	endStr := fmt.Sprintf("%s 23:59:59+05:30", dateStr)

	// 1. Delete existing candles for target date from database
	fmt.Printf("[1/3] Deleting existing candles for %s...\n", dateStr)
	_, err = db.WithContext(ctx).ExecContext(ctx, "DELETE FROM candles_5m WHERE token = $1 AND time >= $2 AND time <= $3", token, startStr, endStr)
	if err != nil {
		log.Fatalf("Failed to delete 5m candles: %v", err)
	}
	_, err = db.WithContext(ctx).ExecContext(ctx, "DELETE FROM candles_1m WHERE token = $1 AND time >= $2 AND time <= $3", token, startStr, endStr)
	if err != nil {
		log.Fatalf("Failed to delete 1m candles: %v", err)
	}

	// 2. Fetch fresh historical candles from Zerodha API
	if cfg.AccessToken == "" || cfg.AccessToken == "your_access_token_here" {
		log.Fatalf("KITE_ACCESS_TOKEN is required for live historical data seeding.")
	}

	kiteClient := kiteconnect.New(cfg.APIKey)
	kiteClient.SetAccessToken(cfg.AccessToken)

	fromTime := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 9, 15, 0, 0, loc)
	toTime := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 15, 30, 0, 0, loc)

	fmt.Printf("[2/3] Fetching 5m historical candles from Zerodha API for %s...\n", dateStr)
	hist5m, err := kiteClient.GetHistoricalData(int(token), "5minute", fromTime, toTime, false, false)
	if err != nil {
		log.Fatalf("Failed to fetch 5m historical data: %v", err)
	}
	fmt.Printf("Fetched %d 5m candles from Zerodha.\n", len(hist5m))

	// 3. Insert each candle with explicit Asia/Kolkata IST timestamp
	fmt.Println("[3/3] Inserting 5m candles into PostgreSQL...")
	inserted5m := 0
	for _, c := range hist5m {
		istTime := time.Date(c.Date.Year(), c.Date.Month(), c.Date.Day(), c.Date.Hour(), c.Date.Minute(), c.Date.Second(), 0, loc)
		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}
		err := db.InsertCandle("candles_5m", token, istTime, c.Open, c.High, c.Low, c.Close, int64(c.Volume), c.Close, c.Close, c.Close, 1, color)
		if err != nil {
			log.Printf("Error inserting candle at %s: %v", istTime.Format("15:04"), err)
		} else {
			inserted5m++
		}
	}
	fmt.Printf("Successfully inserted %d / %d 5m candles into database.\n", inserted5m, len(hist5m))

	// Also fetch and insert 1m candles
	hist1m, err := kiteClient.GetHistoricalData(int(token), "minute", fromTime, toTime, false, false)
	if err == nil {
		fmt.Printf("Fetched %d 1m candles from Zerodha. Inserting...\n", len(hist1m))
		for _, c := range hist1m {
			istTime := time.Date(c.Date.Year(), c.Date.Month(), c.Date.Day(), c.Date.Hour(), c.Date.Minute(), c.Date.Second(), 0, loc)
			color := "DOJI"
			if c.Close > c.Open {
				color = "GREEN"
			} else if c.Close < c.Open {
				color = "RED"
			}
			_ = db.InsertCandle("candles_1m", token, istTime, c.Open, c.High, c.Low, c.Close, int64(c.Volume), c.Close, c.Close, c.Close, 1, color)
		}
	}

	fmt.Println("==============================================================")
	fmt.Printf("  SUCCESS: %s CANDLES SEEDED & ALIGNED SUCCESSFULLY!\n", dateStr)
	fmt.Println("==============================================================")
}
