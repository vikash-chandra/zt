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
	log.Println("  SEED 5-YEAR DAILY CANDLES (2021-2026) FOR QUANT SCANNER INTO candles_1d ")
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

	loc := data.ISTLocation
	toDate := time.Now().In(loc)
	fromDate := toDate.AddDate(-5, 0, 0)

	log.Printf("📡 Seeding 5 years of daily candles from %s to %s...",
		fromDate.Format("2006-01-02"), toDate.Format("2006-01-02"))

	securityMaster := data.NewSecurityMaster(db, kiteClient, logger.Logger)
	universe, err := securityMaster.GetNifty500AndFOStocks(ctx)
	if err != nil || len(universe) == 0 {
		universe, _ = securityMaster.GetFOStocks(ctx)
	}
	if universe == nil {
		universe = make(map[string]int64)
	}
	universe["NIFTY 50"] = 256265
	universe["GOLD"] = 53491975
	universe["CRUDEOIL"] = 53493767

	log.Printf("📊 Target universe contains %d instruments. Beginning batch download...", len(universe))

	totalInserted := 0
	idx := 0
	for symbol, token := range universe {
		idx++
		hist1d, err := kiteClient.GetHistoricalData(int(token), "day", fromDate, toDate, false, false)
		if err != nil {
			log.Printf("[%d/%d] ⚠️ Failed for %s (Token %d): %v", idx, len(universe), symbol, token, err)
		} else if len(hist1d) > 0 {
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
				totalInserted += len(dailyCandles)
				log.Printf("[%d/%d] ✅ %s: saved %d daily candles (52W High/Low ready)", idx, len(universe), symbol, len(dailyCandles))
			} else {
				log.Printf("[%d/%d] ❌ DB error for %s: %v", idx, len(universe), symbol, err)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	log.Println("==========================================================================")
	log.Printf("🎉 5-YEAR DAILY CANDLE SEEDING COMPLETED!\n")
	log.Printf("   - Total Daily (1D) Candles Stored: %d\n", totalInserted)
	log.Println("==========================================================================")
}
