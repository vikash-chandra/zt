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

	// Query created_at for 2026-07-06
	var created06 string
	_ = db.QueryRow("SELECT MAX(created_at)::TEXT FROM pre_selection_results WHERE date = '2026-07-06'").Scan(&created06)

	// Query created_at for 2026-07-07
	var created07 string
	_ = db.QueryRow("SELECT MAX(created_at)::TEXT FROM pre_selection_results WHERE date = '2026-07-07'").Scan(&created07)

	fmt.Printf("\n=== CREATION TIMESTAMPS ===\n")
	fmt.Printf("Latest created_at for '2026-07-06': %s\n", created06)
	fmt.Printf("Latest created_at for '2026-07-07': %s\n", created07)
}
