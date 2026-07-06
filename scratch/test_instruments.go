package main

import (
	"fmt"
	"log"
	"time"
	"zerodha-trading/config"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	kiteClient := kiteconnect.New(cfg.APIKey)
	kiteClient.SetAccessToken(cfg.AccessToken)

	fmt.Println("Attempting to fetch instruments list from Zerodha exchange 'NSE'...")
	startTime := time.Now()
	instruments, err := kiteClient.GetInstrumentsByExchange("NSE")
	if err != nil {
		log.Fatalf("❌ FAILED: %v", err)
	}
	duration := time.Since(startTime)

	fmt.Printf("✅ SUCCESS! Fetched %d instruments in %v\n", len(instruments), duration)
}
