package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	dsn := "host=127.0.0.1 port=5432 user=postgres password=trading_password dbname=zerodha_trading sslmode=disable"
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("Database ping failed: %v", err)
	}

	fmt.Println("Successfully connected to database.")

	// Load fo:stocks and nifty50
	var foData string
	err = db.QueryRow("SELECT value FROM metadata_cache WHERE key = 'fo:stocks'").Scan(&foData)
	if err != nil {
		log.Fatalf("Failed to query fo:stocks cache: %v", err)
	}

	var foMap map[string]int64
	if err := json.Unmarshal([]byte(foData), &foMap); err != nil {
		log.Fatalf("Failed to unmarshal fo:stocks: %v", err)
	}

	// 1. Check database candles counts
	var count5m int
	err = db.QueryRow("SELECT COUNT(*) FROM candles_5m").Scan(&count5m)
	if err != nil {
		log.Fatalf("Failed to query candles_5m count: %v", err)
	}
	fmt.Printf("Total candles in candles_5m: %d\n", count5m)

	// Check counts for today (2026-07-06)
	var countToday int
	err = db.QueryRow("SELECT COUNT(*) FROM candles_5m WHERE time >= '2026-07-06 00:00:00+00'").Scan(&countToday)
	if err != nil {
		log.Fatalf("Failed to query today's candles count: %v", err)
	}
	fmt.Printf("Candles in candles_5m for today (2026-07-06): %d\n", countToday)

	// Check last candle time in DB
	var lastCandleTime time.Time
	err = db.QueryRow("SELECT MAX(time) FROM candles_5m").Scan(&lastCandleTime)
	if err != nil {
		log.Fatalf("Failed to query last candle time: %v", err)
	}
	fmt.Printf("Most recent candle time in database: %s\n", lastCandleTime)

	// 2. Check watchlist symbols and PDH/PDL resolution
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}
	
	// Query some active symbols in watchlist
	symbols := []string{"COLPAL", "HDFCBANK", "SBIN", "CIPLA", "INDUSINDBK"}
	fmt.Println("\nResolving Previous Day High/Low for sample symbols:")
	for _, sym := range symbols {
		token, ok := foMap[sym]
		if !ok {
			fmt.Printf("Symbol %s: not found in foMap\n", sym)
			continue
		}

		// Query previous day high/low
		nowIST := time.Now().In(loc)
		todayStart := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 0, 0, 0, 0, loc).UTC()

		var lastTime time.Time
		err = db.QueryRow("SELECT MAX(time) FROM candles_5m WHERE token = $1 AND time < $2", token, todayStart).Scan(&lastTime)
		if err != nil {
			fmt.Printf("Symbol %s (Token %d): No historical candles before today. Error: %v\n", sym, token, err)
			continue
		}

		if lastTime.IsZero() {
			fmt.Printf("Symbol %s (Token %d): Last candle time prior to today is zero/empty.\n", sym, token)
			continue
		}

		lastTimeIST := lastTime.In(loc)
		prevDayStart := time.Date(lastTimeIST.Year(), lastTimeIST.Month(), lastTimeIST.Day(), 0, 0, 0, 0, loc).UTC()
		prevDayEnd := time.Date(lastTimeIST.Year(), lastTimeIST.Month(), lastTimeIST.Day(), 23, 59, 59, 0, loc).UTC()

		var high, low float64
		err = db.QueryRow("SELECT MAX(high), MIN(low) FROM candles_5m WHERE token = $1 AND time >= $2 AND time <= $3", token, prevDayStart, prevDayEnd).Scan(&high, &low)
		if err != nil {
			fmt.Printf("Symbol %s (Token %d): Failed to query high/low. Error: %v\n", sym, token, err)
		} else {
			fmt.Printf("Symbol %s (Token %d): Last Date: %s, PDH: %.2f, PDL: %.2f\n", sym, token, lastTimeIST.Format("2006-01-02"), high, low)
		}
	}

	// 3. Check if there are any orders placed today
	var orderCount int
	err = db.QueryRow("SELECT COUNT(*) FROM orders").Scan(&orderCount)
	if err != nil {
		log.Fatalf("Failed to query order count: %v", err)
	}
	fmt.Printf("\nTotal orders in database: %d\n", orderCount)
}
