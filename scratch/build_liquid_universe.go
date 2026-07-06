package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
)

func main() {
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
	kc := kiteconnect.New(cfg.APIKey)
	kc.SetAccessToken(cfg.AccessToken)

	fmt.Println("Discovering active NSE EQ instruments...")
	instruments, err := kc.GetInstrumentsByExchange("NSE")
	if err != nil {
		log.Fatalf("Failed to fetch NSE instruments: %v", err)
	}

	var rawSymbols []string
	symbolTokenMap := make(map[string]int64)

	for _, inst := range instruments {
		if inst.Segment == "NSE" && inst.InstrumentType == "EQ" {
			// Skip illiquid category suffixes
			if !strings.HasSuffix(inst.Tradingsymbol, "-BE") && !strings.HasSuffix(inst.Tradingsymbol, "-BZ") {
				rawSymbols = append(rawSymbols, "NSE:"+inst.Tradingsymbol)
				symbolTokenMap[inst.Tradingsymbol] = int64(inst.InstrumentToken)
			}
		}
	}

	fmt.Printf("Querying quotes for %d NSE EQ instruments in batches...\n", len(rawSymbols))

	liquidStocks := make(map[string]int64)

	// Fetch quotes in batches of 400 to prevent payload limit issues
	batchSize := 400
	for i := 0; i < len(rawSymbols); i += batchSize {
		end := i + batchSize
		if end > len(rawSymbols) {
			end = len(rawSymbols)
		}
		batch := rawSymbols[i:end]

		// Call GetQuote
		quotes, err := kc.GetQuote(batch...)
		if err != nil {
			log.Printf("Batch fetch failed for %d to %d: %v", i, end, err)
			continue
		}

		for key, q := range quotes {
			symbol := strings.TrimPrefix(key, "NSE:")
			token := symbolTokenMap[symbol]

			// Filter Criteria:
			// 1. Price between 50 and 5000 (standard liquid retail price zone)
			// 2. Traded volume today > 50,000 shares (ensures basic liquidity)
			if q.LastPrice >= 50.0 && q.LastPrice <= 5000.0 && q.Volume > 50000 {
				liquidStocks[symbol] = token
			}
		}

		// Brief sleep to respect API limits
		time.Sleep(350 * time.Millisecond)
	}

	fmt.Printf("Discovered %d active liquid cash equities.\n", len(liquidStocks))

	// Save to database metadata_cache
	dataBytes, err := json.Marshal(liquidStocks)
	if err != nil {
		log.Fatalf("Failed to marshal liquid stocks: %v", err)
	}

	err = db.SaveMetadataCache(ctx, "liquid:stocks", string(dataBytes))
	if err != nil {
		log.Fatalf("Failed to save liquid stocks to database cache: %v", err)
	}

	fmt.Println("Successfully saved liquid cash stocks list to database cache under key 'liquid:stocks'.")
}
