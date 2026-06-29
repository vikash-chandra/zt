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

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM candles_5m").Scan(&count)
	if err != nil {
		log.Fatalf("Query failed: %v", err)
	}
	fmt.Printf("Total 5m candles: %d\n", count)

	rows, err := db.Query("SELECT token, time, open, close FROM candles_5m ORDER BY time DESC LIMIT 5")
	if err != nil {
		log.Fatalf("Query failed: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var token int64
		var t time.Time
		var open, close float64
		if err := rows.Scan(&token, &t, &open, &close); err != nil {
			log.Fatalf("Scan failed: %v", err)
		}
		fmt.Printf("Token: %d | Time: %s | Unix: %d | Open: %.2f | Close: %.2f\n",
			token, t.Format("2006-01-02 15:04:02 MST"), t.Unix(), open, close)
	}
}
