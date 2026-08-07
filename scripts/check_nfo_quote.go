package main

import (
	"fmt"
	"log"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
)

func main() {
	// Simple diagnostic script to test Zerodha NFO Quote lookup
	apiKey := "your_api_key" // Environment variable
	log.Println("Testing Zerodha NFO Quote lookup...")
	_ = kiteconnect.New(apiKey)
	fmt.Println("Done")
}
