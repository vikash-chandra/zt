package main

import (
	"fmt"
	"log"

	"zerodha-trading/config"
	"zerodha-trading/data"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	kc := kiteconnect.New(cfg.APIKey)
	kc.SetAccessToken(cfg.AccessToken)
	broker := data.NewZerodhaBrokerAdapter(kc)

	orders, err := broker.GetOrders()
	if err != nil {
		log.Fatalf("Failed to get orders: %v", err)
	}

	fmt.Println("=== ALL ZERODHA SENSEX ORDERS TODAY ===")
	for _, o := range orders {
		if o.Tradingsymbol == "SENSEX26OCT73400PE" || o.Tradingsymbol == "SENSEX26OCT75200CE" {
			fmt.Printf("ID: %s | Symbol: %s | Side: %s | Qty: %d | Status: %s | Trigger: %.2f | Price: %.2f | PlacedAt: %s\n",
				o.OrderID, o.Tradingsymbol, o.TransactionType, o.Quantity, o.Status, o.TriggerPrice, o.Price, o.OrderTimestamp.Format("15:04:05"))
		}
	}

	pos, err := broker.GetPositions()
	if err != nil {
		log.Fatalf("Failed to get positions: %v", err)
	}

	fmt.Println("\n=== ALL SENSEX NET POSITIONS ON ZERODHA ===")
	for _, p := range pos.Net {
		if p.Tradingsymbol == "SENSEX26OCT73400PE" || p.Tradingsymbol == "SENSEX26OCT75200CE" {
			fmt.Printf("Symbol: %s | NetQty: %d | BuyQty: %d | SellQty: %d | BuyAvg: %.2f | SellAvg: %.2f | M2M: %.2f | PnL: %.2f\n",
				p.Tradingsymbol, p.Quantity, p.BuyQuantity, p.SellQuantity, p.BuyPrice, p.SellPrice, p.M2M, p.PNL)
		}
	}
}
