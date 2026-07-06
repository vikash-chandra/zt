package main

import (
	"fmt"
	"log"
	"zerodha-trading/config"
	"zerodha-trading/data"

	"go.uber.org/zap"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	db, err := data.NewDatabase(
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		zap.NewNop(),
	)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	var count5m, count1m, countPre int
	
	_ = db.QueryRow("SELECT COUNT(*) FROM candles_5m").Scan(&count5m)
	_ = db.QueryRow("SELECT COUNT(*) FROM candles_1m").Scan(&count1m)
	_ = db.QueryRow("SELECT COUNT(*) FROM pre_selection_results").Scan(&countPre)

	fmt.Printf("\n=== DATABASE STATE REPORT ===\n")
	fmt.Printf("candles_5m row count: %d\n", count5m)
	fmt.Printf("candles_1m row count: %d\n", count1m)
	fmt.Printf("pre_selection_results row count: %d\n", countPre)

	if count5m == 0 {
		fmt.Println("⚠️  Warning: candles_5m is completely empty! You need to run the data seeder.")
	}
	if countPre == 0 {
		fmt.Println("⚠️  Warning: pre_selection_results is empty!")
	}
}
