package main

import (
	"fmt"
	"log"
	"time"
	"zerodha-trading/config"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
)

func main() {
	fmt.Println("==================================================================")
	fmt.Println("             ZERODHA LIVE TRADE MULTI-EXIT TESTER                ")
	fmt.Println("==================================================================")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if !cfg.LiveTrading {
		log.Fatalf("Test Aborted: LIVE_TRADING is set to false in your .env. Set LIVE_TRADING=true to run this live test.")
	}

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}

	// Initialize Kite Client
	kiteClient := kiteconnect.New(cfg.APIKey)
	kiteClient.SetAccessToken(cfg.AccessToken)

	// Step 1: Query current LTP for BUY order limit calculation
	fmt.Println("Fetching current LTP for HDFCLIFE...")
	ltps, err := kiteClient.GetLTP("NSE:HDFCLIFE")
	if err != nil {
		log.Fatalf("Failed to fetch LTP: %v", err)
	}
	currentLTP := ltps["NSE:HDFCLIFE"].LastPrice
	fmt.Printf("Current LTP: INR %.2f\n", currentLTP)

	// Set limit price slightly above LTP to ensure instant execution
	buyLimitPrice := currentLTP + 2.0

	fmt.Printf("1. Placing Live LIMIT BUY Order for 2 shares of HDFCLIFE at INR %.2f...\n", buyLimitPrice)
	buyOrder, err := kiteClient.PlaceOrder("regular", kiteconnect.OrderParams{
		Exchange:        "NSE",
		Tradingsymbol:   "HDFCLIFE",
		TransactionType: "BUY",
		Quantity:        2,
		OrderType:       "LIMIT",
		Price:           buyLimitPrice,
		Product:         "MIS",
		Validity:        "DAY",
	})
	if err != nil {
		log.Fatalf("Failed to place BUY order: %v", err)
	}
	fmt.Printf("✓ Buy Order Placed! Order ID: %s\n", buyOrder.OrderID)

	// Poll for execution status
	fmt.Println("Waiting for order to fill...")
	var buyPrice float64
	for i := 0; i < 10; i++ {
		orders, err := kiteClient.GetOrderHistory(buyOrder.OrderID)
		if err == nil && len(orders) > 0 {
			latest := orders[len(orders)-1]
			if latest.Status == "COMPLETE" {
				buyPrice = latest.AveragePrice
				fmt.Printf("✓ Buy Order Completed! Average Fill Price: INR %.2f\n", buyPrice)
				break
			} else if latest.Status == "REJECTED" || latest.Status == "CANCELLED" {
				log.Fatalf("Buy Order was rejected/cancelled: %s", latest.StatusMessage)
			}
		}
		time.Sleep(1 * time.Second)
	}

	if buyPrice == 0 {
		log.Fatalf("Timeout: Buy order did not complete within 10 seconds.")
	}

	// Step 2: Sleep for 5 minutes and sell 1 share
	fmt.Println("\nWaiting for 5 minutes before selling the first share...")
	time.Sleep(5 * time.Minute)

	// Fetch fresh LTP for first exit
	fmt.Println("Fetching current LTP for HDFCLIFE (First Exit)...")
	ltps, err = kiteClient.GetLTP("NSE:HDFCLIFE")
	if err == nil && len(ltps) > 0 {
		currentLTP = ltps["NSE:HDFCLIFE"].LastPrice
	}
	sellLimitPrice1 := currentLTP - 2.0

	fmt.Printf("2. Placing Live LIMIT SELL Order for 1 share of HDFCLIFE at INR %.2f (First Exit)...\n", sellLimitPrice1)
	sellOrder1, err := kiteClient.PlaceOrder("regular", kiteconnect.OrderParams{
		Exchange:        "NSE",
		Tradingsymbol:   "HDFCLIFE",
		TransactionType: "SELL",
		Quantity:        1,
		OrderType:       "LIMIT",
		Price:           sellLimitPrice1,
		Product:         "MIS",
		Validity:        "DAY",
	})
	if err != nil {
		log.Printf("ERROR: Failed to place first exit order: %v", err)
	} else {
		fmt.Printf("✓ First Exit Order Placed! Order ID: %s\n", sellOrder1.OrderID)
		// Poll
		for i := 0; i < 10; i++ {
			orders, err := kiteClient.GetOrderHistory(sellOrder1.OrderID)
			if err == nil && len(orders) > 0 {
				latest := orders[len(orders)-1]
				if latest.Status == "COMPLETE" {
					fmt.Printf("✓ First Exit Completed! Average Fill Price: INR %.2f\n", latest.AveragePrice)
					break
				}
			}
			time.Sleep(1 * time.Second)
		}
	}

	// Step 3: Wait until 3:15 PM and sell the remaining 1 share
	now := time.Now().In(loc)
	targetTime := time.Date(now.Year(), now.Month(), now.Day(), 15, 15, 0, 0, loc)
	sleepDuration := targetTime.Sub(now)

	if sleepDuration > 0 {
		fmt.Printf("\nWaiting for %s (until 3:15 PM IST) before selling the remaining share...\n", sleepDuration.Round(time.Second))
		time.Sleep(sleepDuration)
	} else {
		fmt.Println("\nIt is already past 3:15 PM IST. Exiting remaining share immediately...")
	}

	// Fetch fresh LTP for final exit
	fmt.Println("Fetching current LTP for HDFCLIFE (Final Exit)...")
	ltps, err = kiteClient.GetLTP("NSE:HDFCLIFE")
	if err == nil && len(ltps) > 0 {
		currentLTP = ltps["NSE:HDFCLIFE"].LastPrice
	}
	sellLimitPrice2 := currentLTP - 2.0

	fmt.Printf("3. Placing Live LIMIT SELL Order for 1 share of HDFCLIFE at INR %.2f (Final Exit)...\n", sellLimitPrice2)
	sellOrder2, err := kiteClient.PlaceOrder("regular", kiteconnect.OrderParams{
		Exchange:        "NSE",
		Tradingsymbol:   "HDFCLIFE",
		TransactionType: "SELL",
		Quantity:        1,
		OrderType:       "LIMIT",
		Price:           sellLimitPrice2,
		Product:         "MIS",
		Validity:        "DAY",
	})
	if err != nil {
		log.Fatalf("Failed to place final exit order: %v", err)
	}
	fmt.Printf("✓ Final Exit Order Placed! Order ID: %s\n", sellOrder2.OrderID)

	// Poll
	var finalPrice float64
	for i := 0; i < 10; i++ {
		orders, err := kiteClient.GetOrderHistory(sellOrder2.OrderID)
		if err == nil && len(orders) > 0 {
			latest := orders[len(orders)-1]
			if latest.Status == "COMPLETE" {
				finalPrice = latest.AveragePrice
				fmt.Printf("✓ Final Exit Completed! Average Fill Price: INR %.2f\n", finalPrice)
				break
			}
		}
		time.Sleep(1 * time.Second)
	}

	fmt.Println("\n==================================================================")
	fmt.Println("               MULTI-EXIT TEST COMPLETE                           ")
	fmt.Println("==================================================================")
}
