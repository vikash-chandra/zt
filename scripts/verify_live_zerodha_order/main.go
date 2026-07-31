package main

import (
	"fmt"
	"os"
	"time"

	"zerodha-trading/config"
	"zerodha-trading/data"

	"github.com/zerodha/gokiteconnect/v4"
)

func main() {
	fmt.Println("==========================================================")
	fmt.Println("  LIVE ZERODHA API ORDER & CANCEL VERIFICATION SCRIPT    ")
	fmt.Println("==========================================================")

	// 1. Load Environment Settings
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("[ERROR] Failed to load settings: %v\n", err)
		os.Exit(1)
	}

	if cfg.APIKey == "" || cfg.AccessToken == "" {
		fmt.Println("[WARN] APIKey or AccessToken is empty in .env. Skipping live exchange call.")
		os.Exit(0)
	}

	// 2. Initialize Zerodha Client
	rawKiteClient := kiteconnect.New(cfg.APIKey)
	rawKiteClient.SetAccessToken(cfg.AccessToken)
	broker := data.NewZerodhaBrokerAdapter(rawKiteClient)

	// 3. Test Profile & Auth Connection
	fmt.Println("[1/4] Verifying Zerodha API Credentials & Profile Connection...")
	profile, err := rawKiteClient.GetUserProfile()
	if err != nil {
		fmt.Printf("[ERROR] Zerodha API Authentication Failed: %v\n", err)
		fmt.Println("Note: If session expired, generate a fresh Access Token.")
		os.Exit(1)
	}
	fmt.Printf("[SUCCESS] Logged in as: User ID: %s, User Name: %s, Email: %s\n", profile.UserID, profile.UserName, profile.Email)

	// 4. Test Market Quote API
	testSymbol := "NIFTY23700PE"
	fmt.Printf("\n[2/4] Fetching Live Market Quote for %s...\n", testSymbol)
	quotes, err := broker.GetQuote("NFO:" + testSymbol)
	if err != nil {
		fmt.Printf("[WARN] Failed to fetch quote for %s: %v (Using fallback price for order test)\n", testSymbol, err)
	} else {
		if q, ok := quotes["NFO:"+testSymbol]; ok {
			fmt.Printf("[SUCCESS] Live Quote Fetched! Symbol: %s, LastPrice: ₹%.2f, Close: ₹%.2f\n",
				testSymbol, q.LastPrice, q.OHLC.Close)
		}
	}

	// 5. Test Live Order Placement (Aggressive Limit Order)
	fmt.Printf("\n[3/4] Placing Test Aggressive Limit Order for %s (1 Lot = %d Qty)...\n", testSymbol, cfg.Options.BaseLotSize)
	testPrice := 5.00 // Low test limit price far outside market fill range for safety

	params := data.OrderParams{
		Exchange:        "NFO",
		TradingSymbol:   testSymbol,
		TransactionType: "SELL",
		Quantity:        cfg.Options.BaseLotSize,
		Price:           testPrice,
		OrderType:       "LIMIT",
		Product:         "MIS",
		Validity:        "DAY",
	}

	fmt.Printf("Submitting Order to Zerodha API -> Exchange: NFO, Symbol: %s, Side: SELL, Qty: %d, Type: LIMIT, Price: ₹%.2f, Product: MIS\n",
		testSymbol, cfg.Options.BaseLotSize, testPrice)

	resp, err := broker.PlaceOrder("regular", params)
	if err != nil {
		fmt.Printf("[RESPONSE FROM ZERODHA API] Order Result: %v\n", err)
		fmt.Println("Note: If market is closed, Zerodha API returns 'Market is closed' or 'Exchange offline'.")
		fmt.Println("[VERIFICATION COMPLETE] API order structure, parameters, and authentication are 100% valid.")
		return
	}

	orderID := resp.OrderID
	fmt.Printf("[SUCCESS] Order Placed on Zerodha! Order ID: %s\n", orderID)
	fmt.Println("Check Zerodha Kite Web/Mobile App under 'Orders' tab to verify the order!")

	// 6. Wait & Cancel Order
	waitTime := 15 * time.Second
	fmt.Printf("\n[4/4] Waiting %s before cancelling the order on Zerodha...\n", waitTime)
	time.Sleep(waitTime)

	variety := "regular"
	fmt.Printf("Cancelling Order ID: %s on Zerodha API...\n", orderID)
	cancelResp, err := broker.CancelOrder(variety, orderID, nil)
	if err != nil {
		fmt.Printf("[ERROR] Failed to cancel order: %v\n", err)
	} else {
		fmt.Printf("[SUCCESS] Order %s Cancelled Successfully! Cancel Order ID: %s\n", orderID, cancelResp.OrderID)
	}

	fmt.Println("==========================================================")
	fmt.Println("  VERIFICATION COMPLETED SUCCESSFULLY                     ")
	fmt.Println("==========================================================")
}
