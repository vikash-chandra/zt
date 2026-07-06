package main

import (
	"fmt"
	"log"
	"time"
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

	// 1. Get distinct dates
	rows, err := db.Query("SELECT DISTINCT date::TEXT FROM pre_selection_results ORDER BY date::TEXT DESC")
	if err != nil {
		log.Fatalf("Failed to get distinct dates: %v", err)
	}
	defer rows.Close()

	fmt.Println("\n=== DISTINCT DATES IN PRE_SELECTION_RESULTS ===")
	for rows.Next() {
		var dateStr string
		if err := rows.Scan(&dateStr); err == nil {
			fmt.Printf("- %s\n", dateStr)
		}
	}

	// 2. Get latest date
	latestDate, err := db.GetLatestPreSelectionDate()
	if err != nil {
		fmt.Printf("GetLatestPreSelectionDate failed: %v\n", err)
	} else {
		fmt.Printf("\nGetLatestPreSelectionDate() returned: %q\n", latestDate)
	}

	// 3. Today's date check
	localNow := time.Now()
	fmt.Printf("Local Time Now: %s\n", localNow.Format("2006-01-02 15:04:05"))
	fmt.Printf("Local Today: %s\n", localNow.Format("2006-01-02"))
}
