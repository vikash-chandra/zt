package main

import (
	"fmt"
	"log"
	"math"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"zerodha-trading/config"
)

func main() {
	fmt.Println("==================================================================")
	fmt.Println("         BIOCON LIVE TRADE MULTI-EXIT TESTER WITH SL             ")
	fmt.Println("==================================================================")

	// Load configuration
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

	// Initialize Kite client
	kiteClient := kiteconnect.New(cfg.APIKey)
	kiteClient.SetAccessToken(cfg.AccessToken)

	symbol := "BIOCON"
	exchangeSymbol := "NSE:" + symbol

	// Step 1: Query current LTP for BUY order limit price calculation
	fmt.Printf("Fetching current LTP for %s...\n", symbol)
	ltps, err := kiteClient.GetLTP(exchangeSymbol)
	if err != nil {
		log.Fatalf("Failed to fetch LTP: %v", err)
	}
	currentLTP := ltps[exchangeSymbol].LastPrice
	fmt.Printf("Current LTP: INR %.2f\n", currentLTP)

	// Set limit price slightly above LTP to ensure instant execution
	buyLimitPrice := currentLTP + 0.50

	fmt.Printf("1. Placing Live LIMIT BUY Order for 2 shares of %s at INR %.2f...\n", symbol, buyLimitPrice)
	buyOrder, err := kiteClient.PlaceOrder("regular", kiteconnect.OrderParams{
		Exchange:        "NSE",
		Tradingsymbol:   symbol,
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
	for i := 0; i < 15; i++ {
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
		log.Fatalf("Timeout: Buy order did not complete within 15 seconds.")
	}

	// Step 2: Set Stop-Loss Order (using standard 1% rule)
	// Calculate SL Price (1% below Buy Price, rounded to the nearest 0.05 tick)
	slTriggerPrice := math.Floor((buyPrice*0.99)/0.05) * 0.05
	fmt.Printf("\nStop-Loss Rule Trigger Price (1%% below Buy Price): INR %.2f\n", slTriggerPrice)

	fmt.Println("Placing Live Stop-Loss Market (SL-M) order for 2 shares...")
	slOrder, err := kiteClient.PlaceOrder("regular", kiteconnect.OrderParams{
		Exchange:        "NSE",
		Tradingsymbol:   symbol,
		TransactionType: "SELL",
		Quantity:        2,
		OrderType:       "SL-M",
		TriggerPrice:    slTriggerPrice,
		Product:         "MIS",
		Validity:        "DAY",
	})
	if err != nil {
		log.Fatalf("Failed to place Stop-Loss order: %v", err)
	}
	slOrderID := slOrder.OrderID
	fmt.Printf("✓ Stop-Loss Order Placed! Order ID: %s\n", slOrderID)

	// Step 3: Sleep for 5 minutes and sell 1 share
	fmt.Println("\nWaiting for 5 minutes before exiting the first share...")
	time.Sleep(5 * time.Minute)

	// Fetch fresh LTP for first exit
	fmt.Printf("Fetching current LTP for %s (First Exit)...\n", symbol)
	ltps, err = kiteClient.GetLTP(exchangeSymbol)
	if err == nil && len(ltps) > 0 {
		currentLTP = ltps[exchangeSymbol].LastPrice
	}
	sellLimitPrice1 := currentLTP - 0.50

	// Modify the Stop-Loss Order: Reduce quantity to 1 share first
	fmt.Println("Modifying Stop-Loss order quantity from 2 to 1 share...")
	_, err = kiteClient.ModifyOrder("regular", slOrderID, kiteconnect.OrderParams{
		Quantity:     1,
		OrderType:    "SL-M",
		TriggerPrice: slTriggerPrice,
	})
	if err != nil {
		log.Printf("WARNING: Failed to modify Stop-Loss order quantity: %v. Proceeding to exit first share...", err)
	} else {
		fmt.Println("✓ Stop-Loss Order modified successfully (Quantity: 1 share remaining).")
	}

	fmt.Printf("Placing LIMIT SELL Order for 1 share of %s at INR %.2f (First Exit)...\n", symbol, sellLimitPrice1)
	sellOrder1, err := kiteClient.PlaceOrder("regular", kiteconnect.OrderParams{
		Exchange:        "NSE",
		Tradingsymbol:   symbol,
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
		for i := 0; i < 15; i++ {
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

	// Step 4: Wait until 3:15 PM IST and sell the remaining 1 share
	now := time.Now().In(loc)
	targetTime := time.Date(now.Year(), now.Month(), now.Day(), 15, 15, 0, 0, loc)
	sleepDuration := targetTime.Sub(now)

	if sleepDuration > 0 {
		fmt.Printf("\nWaiting for %s (until 3:15 PM IST) before exiting the remaining share...\n", sleepDuration.Round(time.Second))
		time.Sleep(sleepDuration)
	} else {
		fmt.Println("\nIt is already past 3:15 PM IST. Exiting remaining share immediately...")
	}

	// Cancel the remaining Stop-Loss Order first
	fmt.Println("Cancelling the remaining Stop-Loss order...")
	_, err = kiteClient.CancelOrder("regular", slOrderID, nil)
	if err != nil {
		log.Printf("WARNING: Failed to cancel Stop-Loss order: %v. It may have already triggered.", err)
	} else {
		fmt.Println("✓ Stop-Loss Order cancelled successfully.")
	}

	// Fetch fresh LTP for final exit
	fmt.Printf("Fetching current LTP for %s (Final Exit)...\n", symbol)
	ltps, err = kiteClient.GetLTP(exchangeSymbol)
	if err == nil && len(ltps) > 0 {
		currentLTP = ltps[exchangeSymbol].LastPrice
	}
	sellLimitPrice2 := currentLTP - 0.50

	fmt.Printf("Placing LIMIT SELL Order for the remaining 1 share of %s at INR %.2f (Final Exit)...\n", symbol, sellLimitPrice2)
	sellOrder2, err := kiteClient.PlaceOrder("regular", kiteconnect.OrderParams{
		Exchange:        "NSE",
		Tradingsymbol:   symbol,
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
	for i := 0; i < 15; i++ {
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
	fmt.Println("               BIOCON TRADE TEST COMPLETE                         ")
	fmt.Println("==================================================================")
}
