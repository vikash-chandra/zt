package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
	"zerodha-trading/config"
	"zerodha-trading/data"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"go.uber.org/zap"
)

func main() {
	// 1. Diagnose basic internet connectivity to Zerodha API endpoint
	client := &http.Client{Timeout: 3 * time.Second}
	fmt.Println("[STEP 1] Testing basic network reachability to api.kite.trade...")
	resp, err := client.Get("https://api.kite.trade")
	if err != nil {
		log.Fatalf("❌ Network Reachability FAILED: %v\nCheck your internet connection, firewall, or DNS settings.", err)
	}
	resp.Body.Close()
	fmt.Println("✅ Network Reachability SUCCESS! Able to communicate with api.kite.trade.")

	// 2. Validate Credentials
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("\n[STEP 2] Testing connection with API Key: %s\n", cfg.APIKey)
	fmt.Printf("User ID: %s\n", cfg.UserID)

	kiteClient := kiteconnect.New(cfg.APIKey)
	kiteClient.SetAccessToken(cfg.AccessToken)

	fmt.Println("Requesting positions from Zerodha to check session validation...")
	_, err = kiteClient.GetPositions()
	if err != nil {
		log.Fatalf("❌ API Call FAILED: %v\nYour access token or API Key is likely EXPIRED/INVALID.", err)
	}
	fmt.Println("✅ API Call SUCCESS! Successfully connected to Zerodha and fetched positions.")

	// 3. Validate Database
	fmt.Printf("\n[STEP 3] Testing connection to Database %s on %s:%d...\n", cfg.DBName, cfg.DBHost, cfg.DBPort)
	logger, _ := zap.NewDevelopment()
	db, err := data.NewDatabase(
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		logger,
	)
	if err != nil {
		log.Fatalf("❌ Database Connection FAILED: %v\nEnsure your TimescaleDB Docker container is running.", err)
	}
	defer db.Close()
	fmt.Println("✅ Database Connection SUCCESS!")
}
