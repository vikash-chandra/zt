package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"zerodha-trading/config"

	_ "github.com/lib/pq"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Cutoff time: July 27, 2026 00:00:00 IST (2026-07-26 18:30:00 UTC)
	cutoffUTC := "2026-07-26 18:30:00"

	fmt.Println("🧹 Starting database candle cleanup (keeping data from 27/07/2026 onwards)...")

	// Delete from candles_5m
	res5m, err := db.Exec("DELETE FROM candles_5m WHERE time < $1", cutoffUTC)
	if err != nil {
		log.Fatalf("Failed to clean candles_5m: %v", err)
	}
	rows5m, _ := res5m.RowsAffected()
	fmt.Printf("✅ Deleted %d old candles from candles_5m (older than 27/07/2026)\n", rows5m)

	// Delete from candles_1m
	res1m, err := db.Exec("DELETE FROM candles_1m WHERE time < $1", cutoffUTC)
	if err != nil {
		log.Fatalf("Failed to clean candles_1m: %v", err)
	}
	rows1m, _ := res1m.RowsAffected()
	fmt.Printf("✅ Deleted %d old candles from candles_1m (older than 27/07/2026)\n", rows1m)

	// Report remaining candle counts
	var count5m, count1m int
	_ = db.QueryRow("SELECT COUNT(*) FROM candles_5m").Scan(&count5m)
	_ = db.QueryRow("SELECT COUNT(*) FROM candles_1m").Scan(&count1m)

	var minTime5m, maxTime5m time.Time
	_ = db.QueryRow("SELECT COALESCE(MIN(time), NOW()), COALESCE(MAX(time), NOW()) FROM candles_5m").Scan(&minTime5m, &maxTime5m)

	fmt.Printf("\n📊 Cleanup Summary:\n")
	fmt.Printf("   • Remaining candles_5m: %d (Range: %s to %s)\n", count5m, minTime5m.Format("2006-01-02 15:04"), maxTime5m.Format("2006-01-02 15:04"))
	fmt.Printf("   • Remaining candles_1m: %d\n", count1m)
}
