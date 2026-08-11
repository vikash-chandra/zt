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
	log.Println("==========================================================================")
	log.Println("  CLEAN TRUNCATE & RE-SEED PAST 20 DAYS HISTORICAL CANDLES FROM ZERODHA API ")
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

	// 1. Truncate existing candle data from PostgreSQL
	log.Println("🧹 Truncating existing candle tables (candles_5m, candles_1m, candles_1d)...")
	_, err = db.WithContext(ctx).ExecContext(ctx, "TRUNCATE TABLE candles_5m, candles_1m, candles_1d;")
	if err != nil {
		log.Fatalf("❌ Failed to truncate candle tables: %v", err)
	}
	log.Println("✅ Successfully cleared all candle tables!")

	// 2. Fetch latest Zerodha Access Token from DB metadata_cache if available
	var cachedToken string
	_ = db.WithContext(ctx).QueryRowContext(ctx, `SELECT value FROM metadata_cache WHERE key = 'config:kite_access_token'`).Scan(&cachedToken)
	if cachedToken != "" {
		cfg.AccessToken = cachedToken
		log.Println("🔑 Loaded active Kite Access Token from PostgreSQL metadata_cache:", cachedToken)
	}

	if cfg.AccessToken == "" {
		log.Fatalf("❌ KITE_ACCESS_TOKEN is missing or empty.")
	}

	rawKiteClient := kiteconnect.New(cfg.APIKey)
	rawKiteClient.SetAccessToken(cfg.AccessToken)
	kiteClient := data.NewZerodhaBrokerAdapter(rawKiteClient)

	// 3. Define 20-Day Time Horizon (2026-07-20 00:00:00 IST to NOW)
	loc := data.ISTLocation
	fromDate := time.Date(2026, 7, 20, 0, 0, 0, 0, loc)
	toDate := time.Now().In(loc)

	log.Printf("📡 Seeding past 20 days historical candles from %s to %s...",
		fromDate.Format("2006-01-02"), toDate.Format("2006-01-02"))

	// 4. Build Watchlist (NIFTY 50 + Key F&O Stocks)
	securityMaster := data.NewSecurityMaster(db, kiteClient, logger.Logger)
	watchlist, err := securityMaster.GetNifty50Constituents(ctx)
	if err != nil {
		watchlist = make(map[string]int64)
	}
	watchlist["NIFTY 50"] = 256265

	log.Printf("📊 Target watchlist contains %d instruments. Beginning batch download...", len(watchlist))

	var total5mInserted int
	var total1dInserted int

	for symbol, token := range watchlist {
		log.Printf("📥 Fetching 5m candles for %s (Token: %d)...", symbol, token)

		// A. Fetch 5m candles
		hist5m, err := kiteClient.GetHistoricalData(int(token), "5minute", fromDate, toDate, false, false)
		if err != nil {
			log.Printf("⚠️ Failed to fetch 5m candles for %s: %v", symbol, err)
		} else {
			for _, h := range hist5m {
				cTime := data.NormalizeToIST(h.Date)
				color := "DOJI"
				if h.Close > h.Open {
					color = "GREEN"
				} else if h.Close < h.Open {
					color = "RED"
				}
				err := db.InsertCandle("candles_5m", token, cTime, h.Open, h.High, h.Low, h.Close, h.Volume, h.Close, h.Close, h.Close, 1, color)
				if err == nil {
					total5mInserted++
				}
			}
			log.Printf("  └─ Saved %d 5m candles for %s", len(hist5m), symbol)
		}

		// B. Fetch Daily (1d) candles
		hist1d, err := kiteClient.GetHistoricalData(int(token), "day", fromDate, toDate, false, false)
		if err != nil {
			log.Printf("⚠️ Failed to fetch 1d candles for %s: %v", symbol, err)
		} else {
			var dailyCandles []data.Candle
			for _, h := range hist1d {
				cTime := data.NormalizeToIST(h.Date)
				dailyCandles = append(dailyCandles, data.Candle{
					Token:  token,
					Time:   cTime,
					Open:   h.Open,
					High:   h.High,
					Low:    h.Low,
					Close:  h.Close,
					Volume: h.Volume,
				})
			}
			if err := db.UpsertDailyCandles(ctx, dailyCandles); err == nil {
				total1dInserted += len(dailyCandles)
			}
		}

		// Rate limit protection
		time.Sleep(100 * time.Millisecond)
	}

	log.Println("==========================================================================")
	log.Printf("🎉 PAST 20 DAYS HISTORICAL SEED COMPLETE!\n")
	log.Printf("   - Total 5m Candles Upserted: %d\n", total5mInserted)
	log.Printf("   - Total 1d Candles Upserted: %d\n", total1dInserted)
	log.Println("==========================================================================")
}
