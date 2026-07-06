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

	var count06, count07 int
	_ = db.QueryRow("SELECT COUNT(*) FROM pre_selection_results WHERE date = '2026-07-06'").Scan(&count06)
	_ = db.QueryRow("SELECT COUNT(*) FROM pre_selection_results WHERE date = '2026-07-07'").Scan(&count07)

	fmt.Printf("\n=== RECORD COUNTS BY DATE ===\n")
	fmt.Printf("Records for '2026-07-06' (Today): %d\n", count06)
	fmt.Printf("Records for '2026-07-07' (Tomorrow/Future): %d\n", count07)

	// Fetch some samples
	rows, err := db.Query("SELECT ticker, predicted_direction, probability_score FROM pre_selection_results WHERE date = '2026-07-06' LIMIT 5")
	if err == nil {
		defer rows.Close()
		fmt.Println("\nSamples for 2026-07-06:")
		for rows.Next() {
			var symbol, direction string
			var score float64
			if rows.Scan(&symbol, &direction, &score) == nil {
				fmt.Printf("- %s: %s (Score: %f)\n", symbol, direction, score)
			}
		}
	}
}
