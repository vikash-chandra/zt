package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
)

type Candle struct {
	Time   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
}

func main() {
	dsn := "host=127.0.0.1 port=5432 user=postgres password=trading_password dbname=zerodha_trading sslmode=disable"
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}

	// 1. Get fo:stocks map
	var foData string
	err = db.QueryRow("SELECT value FROM metadata_cache WHERE key = 'fo:stocks'").Scan(&foData)
	if err != nil {
		log.Fatalf("Failed to get fo:stocks: %v", err)
	}
	var foMap map[string]int64
	if err := json.Unmarshal([]byte(foData), &foMap); err != nil {
		log.Fatalf("Failed to unmarshal fo:stocks: %v", err)
	}

	// Check if there are any orders placed today
	var todayOrdersCount int
	err = db.QueryRow("SELECT COUNT(*) FROM orders WHERE created_at >= '2026-07-06 00:00:00+05:30'").Scan(&todayOrdersCount)
	if err != nil {
		log.Printf("Failed to query today's orders: %v", err)
	}
	fmt.Printf("Orders placed today (2026-07-06): %d\n\n", todayOrdersCount)

	// Simulate LOW_VOLUME strategy for today's data (2026-07-06)
	fmt.Println("=== Simulating LOW_VOLUME Strategy for today ===")
	for sym, token := range foMap {
		// Fetch today's candles for this token
		rows, err := db.Query("SELECT time, open, high, low, close, volume FROM candles_5m WHERE token = $1 AND time >= '2026-07-06 00:00:00+00' ORDER BY time ASC", token)
		if err != nil {
			log.Fatalf("Failed to query candles for %s: %v", sym, err)
		}

		var candles []Candle
		for rows.Next() {
			var c Candle
			if err := rows.Scan(&c.Time, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume); err != nil {
				rows.Close()
				log.Fatalf("Failed to scan candle: %v", err)
			}
			candles = append(candles, c)
		}
		rows.Close()

		if len(candles) == 0 {
			continue
		}

		// Run LOW_VOLUME setup candle identification
		var lowestVolIdx int = -1
		var lowestVol int64 = -1
		
		for idx, c := range candles {
			// Skip candles before market open (09:15) or after check window
			tIST := c.Time.In(loc)
			timeStr := tIST.Format("15:04")
			if timeStr < "09:15" {
				continue
			}

			if lowestVol == -1 || c.Volume < lowestVol {
				lowestVol = c.Volume
				lowestVolIdx = idx
			}
		}

		if lowestVolIdx != -1 {
			setupCandle := candles[lowestVolIdx]
			setupIST := setupCandle.Time.In(loc)
			
			// Is the Setup candle RED? (for BUY_ONLY bias)
			isRed := setupCandle.Close < setupCandle.Open
			
			// Check if any subsequent candle broke above setup high inside active trading window (09:30 to 10:45)
			// Wait! LOW_VOLUME only allows trigger on the IMMEDIATELY NEXT candle after the setup candle closed!
			// Let's check if the next candle broke out.
			nextIdx := lowestVolIdx + 1
			if nextIdx < len(candles) {
				nextCandle := candles[nextIdx]
				nextIST := nextCandle.Time.In(loc)
				nextTimeStr := nextIST.Format("15:04")
				
				// Trading window check
				inWindow := nextTimeStr >= "09:30" && nextTimeStr <= "10:45"
				
				if isRed && nextCandle.High > setupCandle.High {
					fmt.Printf("Symbol %s (Token %d): Setup Candle closed at %s (Vol: %d, High: %.2f, Low: %.2f). Next candle at %s (High: %.2f) BROKE above it! (In trading window: %v)\n",
						sym, token, setupIST.Format("15:04"), setupCandle.Volume, setupCandle.High, setupCandle.Low, nextIST.Format("15:04"), nextCandle.High, inWindow)
				}
			}
		}
	}
}
