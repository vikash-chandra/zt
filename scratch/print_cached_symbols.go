package main

import (
	"database/sql"
	"encoding/json"
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

	// 1. Query Nifty 50 constituents
	var niftyData string
	err = db.QueryRow("SELECT value FROM metadata_cache WHERE key = 'nifty50:constituents'").Scan(&niftyData)
	if err != nil {
		fmt.Printf("Failed to get Nifty 50 constituents cache: %v\n", err)
	} else {
		var niftyMap map[string]int64
		if err := json.Unmarshal([]byte(niftyData), &niftyMap); err != nil {
			fmt.Printf("Failed to unmarshal Nifty 50 constituents: %v\n", err)
		} else {
			fmt.Printf("Nifty 50 constituents count: %d\n", len(niftyMap))
		}
	}

	// 2. Query fo:stocks
	var foData string
	err = db.QueryRow("SELECT value FROM metadata_cache WHERE key = 'fo:stocks'").Scan(&foData)
	if err != nil {
		fmt.Printf("Failed to get fo:stocks cache: %v\n", err)
	} else {
		var foMap map[string]int64
		if err := json.Unmarshal([]byte(foData), &foMap); err != nil {
			fmt.Printf("Failed to unmarshal fo:stocks: %v\n", err)
		} else {
			fmt.Printf("SRF token is: %d\n", foMap["SRF"])
		}
	}
}
