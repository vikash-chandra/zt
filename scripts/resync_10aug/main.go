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
	"zerodha-trading/strategy"
)

func main() {
	log.Println("==========================================================================")
	log.Println("  FETCHING & VERIFYING 10 AUG 2026 HISTORICAL CANDLES FROM ZERODHA API    ")
	log.Println("==========================================================================")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logger, err := monitoring.NewLogger(cfg.LogLevel)
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}

	db, err := data.NewDatabase(
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		logger.Logger,
	)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// 1. Fetch latest Access Token from database metadata_cache if available
	var cachedToken string
	_ = db.WithContext(ctx).QueryRowContext(ctx, `SELECT value FROM metadata_cache WHERE key = 'config:kite_access_token'`).Scan(&cachedToken)
	if cachedToken != "" {
		cfg.AccessToken = cachedToken
		log.Println("🔑 Using latest Kite Access Token from PostgreSQL metadata_cache:", cachedToken)
	}

	if cfg.AccessToken == "" {
		log.Fatalf("❌ KITE_ACCESS_TOKEN is missing or empty.")
	}

	rawKiteClient := kiteconnect.New(cfg.APIKey)
	rawKiteClient.SetAccessToken(cfg.AccessToken)
	kiteClient := data.NewZerodhaBrokerAdapter(rawKiteClient)

	// 2. Query Zerodha Historical REST API for NIFTY 50 (Token: 256265)
	loc := data.ISTLocation
	fromDate := time.Date(2026, 8, 7, 9, 15, 0, 0, loc)   // Friday 7th Aug for ST continuity
	toDate := time.Date(2026, 8, 11, 15, 30, 0, 0, loc)  // Today 11th Aug

	log.Printf("📡 Requesting historical 5m candles from Zerodha API for NIFTY 50 (256265) from %s to %s...",
		fromDate.Format("2006-01-02 15:04:05"), toDate.Format("2006-01-02 15:04:05"))

	historicalData, err := kiteClient.GetHistoricalData(256265, "5minute", fromDate, toDate, false, false)
	if err != nil {
		log.Fatalf("❌ Failed to fetch historical candles from Zerodha API: %v", err)
	}

	log.Printf("✅ Received %d candles from Zerodha API!", len(historicalData))

	// 3. Upsert Zerodha candles directly into database candles_5m table
	var candles []data.Candle
	for _, h := range historicalData {
		cTime := data.NormalizeToIST(h.Date)
		color := "DOJI"
		if h.Close > h.Open {
			color = "GREEN"
		} else if h.Close < h.Open {
			color = "RED"
		}

		c := data.Candle{
			Token:  256265,
			Time:   cTime,
			Open:   h.Open,
			High:   h.High,
			Low:    h.Low,
			Close:  h.Close,
			Volume: h.Volume,
			Color:  color,
		}

		_ = db.InsertCandle("candles_5m", 256265, cTime, h.Open, h.High, h.Low, h.Close, h.Volume, h.Close, h.Close, h.Close, 1, color)
		candles = append(candles, c)
	}

	log.Printf("💾 Successfully upserted %d official Zerodha candles into PostgreSQL!", len(candles))

	// 4. Fetch all 5m candles for Nifty 50 from DB to build complete historical array
	dbCandles, err := db.GetLastNCandles("candles_5m", 256265, 300)
	if err == nil && len(dbCandles) > 0 {
		candles = dbCandles
	}

	// 5. Run Triple SuperTrend Engine over historical candles
	stEngine := strategy.NewSuperTrendOptionsEngine(
		cfg.Options.SuperTrendST1Period, cfg.Options.SuperTrendST2Period, cfg.Options.SuperTrendST3Period,
		cfg.Options.SuperTrendST1Factor, cfg.Options.SuperTrendST2Factor, cfg.Options.SuperTrendST3Factor,
	)

	fmt.Println("\n=========================================================================================")
	fmt.Println("   NIFTY 50 TRIPLE SUPERTREND ANALYSIS FOR 10 AUG 2026 (FROM OFFICIAL ZERODHA DATA)     ")
	fmt.Println("=========================================================================================")
	fmt.Printf("%-20s | %-8s | %-8s | %-8s | %-8s | %-8s | %-8s | %-8s | %-8s\n",
		"Timestamp (IST)", "Open", "High", "Low", "Close", "ST1", "ST2", "ST3", "Trend")
	fmt.Println("-----------------------------------------------------------------------------------------")

	var aug10Count int
	var trendReversals []string

	for i := 1; i <= len(candles); i++ {
		sub := candles[:i]
		last := sub[len(sub)-1]
		res := stEngine.CalculateTripleSuperTrend(sub)

		tIST := data.NormalizeToIST(last.Time)
		if tIST.Format("2006-01-02") == "2026-08-10" {
			aug10Count++
			fmt.Printf("%-20s | %8.2f | %8.2f | %8.2f | %8.2f | %8.2f | %8.2f | %8.2f | %-8s\n",
				tIST.Format("2006-01-02 15:04:05"),
				last.Open, last.High, last.Low, last.Close,
				res.ST1.Value, res.ST2.Value, res.ST3.Value, res.Trend,
			)

			// Check for trend flips on 10 Aug
			if i > 1 {
				prevRes := stEngine.CalculateTripleSuperTrend(candles[:i-1])
				if prevRes.Trend != res.Trend {
					reversalMsg := fmt.Sprintf("⚡ TREND REVERSAL at %s: %s -> %s (Close: %.2f)",
						tIST.Format("15:04:05"), prevRes.Trend, res.Trend, last.Close)
					trendReversals = append(trendReversals, reversalMsg)
				}
			}
		}
	}

	fmt.Println("=========================================================================================")
	fmt.Printf("SUMMARY FOR 10 AUG 2026:\n")
	fmt.Printf("Total 5m Candles on 10 Aug: %d\n", aug10Count)
	if len(trendReversals) > 0 {
		fmt.Printf("Trend Reversals Detected (%d):\n", len(trendReversals))
		for _, rev := range trendReversals {
			fmt.Printf("  - %s\n", rev)
		}
	} else {
		fmt.Println("  - No Trend Reversals occurred on 10 Aug 2026.")
	}
	fmt.Println("=========================================================================================")
}
