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

	rows, err := db.Query("SELECT key, LENGTH(value), updated_at FROM metadata_cache")
	if err != nil {
		log.Fatalf("Failed to query metadata_cache: %v", err)
	}
	defer rows.Close()

	fmt.Println("Keys in metadata_cache:")
	for rows.Next() {
		var key, updated string
		var valLen int
		if err := rows.Scan(&key, &valLen, &updated); err != nil {
			log.Fatalf("Failed to scan row: %v", err)
		}
		fmt.Printf("Key: %s, Value Length: %d, Updated At: %s\n", key, valLen, updated)
	}
}
