package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
)

func main() {
	dsn := "host=127.0.0.1 port=5432 user=postgres password=trading_password dbname=zerodha_trading sslmode=disable"
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	fmt.Println("Querying first 20 rows of security_master:")
	rows, err := db.Query("SELECT symbol, token, instrument_type, expiry FROM security_master LIMIT 20")
	if err != nil {
		log.Fatalf("Failed to query security_master: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sym, instrumentType, expiry string
		var token int64
		if err := rows.Scan(&sym, &token, &instrumentType, &expiry); err != nil {
			log.Fatalf("Failed to scan row: %v", err)
		}
		fmt.Printf("Symbol: %s, Token: %d, Type: %s, Expiry: %s\n", sym, token, instrumentType, expiry)
	}
}
