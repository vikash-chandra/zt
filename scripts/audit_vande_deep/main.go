package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"
)

type ApiCandle struct {
	Time      int64   `json:"time"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Close     float64 `json:"close"`
	Volume    int64   `json:"volume"`
	Color     string  `json:"color"`
	PctChange float64 `json:"pct_change"`
}

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
}

func main() {
	loc, _ := time.LoadLocation("Asia/Kolkata")

	resp, err := http.Get("http://3.7.29.3:8080/api/trades/all")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	var allTrades []Trade
	if err := json.NewDecoder(resp.Body).Decode(&allTrades); err != nil {
		panic(err)
	}

	var targetTrades []Trade
	for _, t := range allTrades {
		if t.Strategy == "VANDE_BHARAT" || t.Strategy == "VANDE_BHARAT_TRAP" {
			targetTrades = append(targetTrades, t)
		}
	}

	sort.Slice(targetTrades, func(i, j int) bool {
		return targetTrades[i].ID > targetTrades[j].ID
	})

	fmt.Printf("Analyzing %d Vande Bharat & Trap trades...\n\n", len(targetTrades))

	for _, t := range targetTrades {
		eSec := t.EntryTime
		if eSec > 1e11 {
			eSec = eSec / 1000
		}
		xSec := t.ExitTime
		if xSec > 1e11 {
			xSec = xSec / 1000
		}
		eTime := time.Unix(eSec, 0).In(loc)
		xTime := time.Unix(xSec, 0).In(loc)
		dateStr := eTime.Format("2006-01-02")

		fmt.Println("==========================================================================================")
		fmt.Printf("TRADE ID: %d | %s | %s | %s | Qty: %d | Entry: %.2f | Time: %s | PNL: %.2f (Exit: %.2f @ %s)\n",
			t.ID, t.Symbol, t.Strategy, t.Side, t.Quantity, t.EntryPrice, eTime.Format("2006-01-02 15:04:05"), t.PNL, t.ExitPrice, xTime.Format("15:04:05"))
		fmt.Println("==========================================================================================")

		// Fetch 5m candles for that date
		candleUrl := fmt.Sprintf("http://3.7.29.3:8080/api/candles?symbol=%s&timeframe=5m&date=%s", t.Symbol, dateStr)
		cResp, err := http.Get(candleUrl)
		if err != nil {
			fmt.Printf("Failed to fetch candles: %v\n", err)
			continue
		}
		var candles []ApiCandle
		_ = json.NewDecoder(cResp.Body).Decode(&candles)
		cResp.Body.Close()

		if len(candles) == 0 {
			fmt.Printf("No 5m candles returned from API for %s on %s\n\n", t.Symbol, dateStr)
			continue
		}

		// Also fetch previous day's candles to find PDH and PDL
		// Let's get the date of previous day
		prevDate := eTime.AddDate(0, 0, -1)
		for prevDate.Weekday() == time.Saturday || prevDate.Weekday() == time.Sunday {
			prevDate = prevDate.AddDate(0, 0, -1)
		}
		prevDateStr := prevDate.Format("2006-01-02")
		prevUrl := fmt.Sprintf("http://3.7.29.3:8080/api/candles?symbol=%s&timeframe=5m&date=%s", t.Symbol, prevDateStr)
		pResp, err := http.Get(prevUrl)
		var prevCandles []ApiCandle
		if err == nil {
			_ = json.NewDecoder(pResp.Body).Decode(&prevCandles)
			pResp.Body.Close()
		}

		var pdh, pdl, pdc float64
		if len(prevCandles) > 0 {
			pdl = 99999999.0
			for _, pc := range prevCandles {
				if pc.High > pdh {
					pdh = pc.High
				}
				if pc.Low < pdl {
					pdl = pc.Low
				}
			}
			pdc = prevCandles[len(prevCandles)-1].Close
		}
		fmt.Printf("Previous Trading Day (%s): PDH = %.2f | PDL = %.2f | PDC = %.2f\n", prevDateStr, pdh, pdl, pdc)

		limit := 10
		if len(candles) < limit {
			limit = len(candles)
		}
		for i := 0; i < limit; i++ {
			c := candles[i]
			cTime := time.Unix(c.Time, 0).In(loc)
			cRange := c.High - c.Low
			cRangePct := (cRange / c.Close) * 100
			body := math.Abs(c.Close - c.Open)
			wick := cRange - body
			wickPct := 0.0
			if cRange > 0 {
				wickPct = (wick / cRange) * 100
			}
			color := "DOJI"
			if c.Close > c.Open {
				color = "GREEN"
			} else if c.Close < c.Open {
				color = "RED"
			}
			fmt.Printf("  Candle %2d [%s] O: %8.2f H: %8.2f L: %8.2f C: %8.2f | %5s | Rng: %5.2f%% | Wick: %5.1f%%\n",
				i+1, cTime.Format("15:04"), c.Open, c.High, c.Low, c.Close, color, cRangePct, wickPct)
		}
		fmt.Println()
	}
}
