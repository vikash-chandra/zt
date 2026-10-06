package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Trade struct {
	ID        int     `json:"id"`
	Symbol    string  `json:"symbol"`
	Strategy  string  `json:"strategy"`
	Side      string  `json:"side"`
	Quantity  int     `json:"quantity"`
	EntryPrice float64 `json:"entry_price"`
	ExitPrice float64 `json:"exit_price"`
	PNL       float64 `json:"pnl"`
	EntryTime int64   `json:"entry_time"`
	ExitTime  int64   `json:"exit_time"`
	TimeHeld  int     `json:"time_held_minutes"`
	CreatedAt int64   `json:"created_at"`
}

func main() {
	resp, err := http.Get("http://3.7.29.3:8080/api/trades/all")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	var trades []Trade
	if err := json.NewDecoder(resp.Body).Decode(&trades); err != nil {
		panic(err)
	}

	loc, _ := time.LoadLocation("Asia/Kolkata")
	fmt.Printf("%-4s | %-10s | %-18s | %-4s | %-6s | %-9s | %-9s | %-8s | %-19s | %-19s\n",
		"ID", "SYMBOL", "STRATEGY", "SIDE", "QTY", "ENTRY", "EXIT", "PNL", "ENTRY_TIME", "EXIT_TIME")
	fmt.Println("-------------------------------------------------------------------------------------------------------------------------")

	for _, t := range trades {
		if t.Strategy == "VANDE_BHARAT" || t.Strategy == "VANDE_BHARAT_TRAP" {
			eSec := t.EntryTime
			if eSec > 1e11 {
				eSec = eSec / 1000
			}
			xSec := t.ExitTime
			if xSec > 1e11 {
				xSec = xSec / 1000
			}
			eT := time.Unix(eSec, 0).In(loc).Format("2006-01-02 15:04:05")
			xT := time.Unix(xSec, 0).In(loc).Format("2006-01-02 15:04:05")
			fmt.Printf("%-4d | %-10s | %-18s | %-4s | %-6d | %-9.2f | %-9.2f | %-8.2f | %-19s | %-19s\n",
				t.ID, t.Symbol, t.Strategy, t.Side, t.Quantity, t.EntryPrice, t.ExitPrice, t.PNL, eT, xT)
		}
	}
}
