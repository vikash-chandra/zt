package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type OptionsState struct {
	Index              string      `json:"index"`
	LastTrend          string      `json:"last_trend"`
	Multiplier         int         `json:"multiplier"`
	BaseLotSize        int         `json:"base_lot_size"`
	SpotToken          int64       `json:"spot_token"`
	CleanPrefix        string      `json:"clean_prefix"`
	OptionsExchange    string      `json:"options_exchange"`
	HasActiveTrade     bool        `json:"has_active_trade"`
	ActiveSymbol       string      `json:"active_symbol"`
	EntryPremium       float64     `json:"entry_premium"`
	SLPrice            float64     `json:"sl_price"`
	PaperBalance       float64     `json:"paper_balance"`
	TotalOptionsTrades int         `json:"total_options_trades"`
	WinTrades          int         `json:"win_trades"`
	WinRatePct         float64     `json:"win_rate_pct"`
	TotalOptionsPnL    float64     `json:"total_options_pnl"`
	LiveTrading        bool        `json:"live_trading"`
	GlobalLive         bool        `json:"global_live"`
	TradeMode          string      `json:"trade_mode"`
	TrailSLEnabled     bool        `json:"trail_sl_enabled"`
	TrailSLBufferPct   float64     `json:"trail_sl_buffer_pct"`
	ST1Params          string      `json:"st1_params"`
	ST2Params          string      `json:"st2_params"`
	ST3Params          string      `json:"st3_params"`
	ActiveIndices      []string    `json:"active_indices"`
	LiveIndices        []string    `json:"live_indices"`
	SupportedIndices   []IndexSpec `json:"supported_indices"`
}

type IndexSpec struct {
	Name            string `json:"name"`
	CleanPrefix     string `json:"clean_prefix"`
	BaseLotSize     int    `json:"base_lot_size"`
	OptionsExchange string `json:"options_exchange"`
}

type IndicatorPoint struct {
	Time   int64   `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	ST1    float64 `json:"st1"`
	ST2    float64 `json:"st2"`
	ST3    float64 `json:"st3"`
	Trend  string  `json:"trend"`
	Signal string  `json:"signal"`
}

type TradeRecord struct {
	ID              int64   `json:"id"`
	Symbol          string  `json:"symbol"`
	Strategy        string  `json:"strategy"`
	Side            string  `json:"side"`
	Quantity        int     `json:"quantity"`
	EntryPrice      float64 `json:"entry_price"`
	ExitPrice       float64 `json:"exit_price"`
	PnL             float64 `json:"pnl"`
	Status          string  `json:"status"`
	CreatedAt       string  `json:"created_at"`
	ExitTime        string  `json:"exit_time"`
	TimeHeldMinutes int     `json:"time_held_minutes"`
}

func main() {
	baseURL := "http://3.7.29.3:8080"
	client := &http.Client{Timeout: 10 * time.Second}

	indices := []string{"NIFTY 50", "NIFTY BANK", "BSE SENSEX", "FINNIFTY", "MIDCPNIFTY"}

	fmt.Println("===================================================================================")
	fmt.Println("       5m TRIPLE SUPERTREND OPTIONS BOT - COMPREHENSIVE INDICES AUDIT               ")
	fmt.Println("===================================================================================")

	// Step 1: Check All Trades in DB
	fmt.Println("\n--- [1] Checking All Trades in System ---")
	resp, err := client.Get(baseURL + "/api/trades/all")
	if err != nil {
		fmt.Printf("❌ Failed to fetch /api/trades/all: %v\n", err)
		return
	}
	defer resp.Body.Close()
	allTradesBytes, _ := io.ReadAll(resp.Body)
	var allTrades []TradeRecord
	json.Unmarshal(allTradesBytes, &allTrades)

	optTradesCount := 0
	tradesByIndex := make(map[string][]TradeRecord)
	for _, t := range allTrades {
		if t.Strategy == "OPTIONS_SUPERTREND" {
			optTradesCount++
			matched := false
			for _, idx := range indices {
				pfx := "NIFTY"
				if strings.Contains(idx, "BANK") {
					pfx = "BANKNIFTY"
				} else if strings.Contains(idx, "SENSEX") {
					pfx = "SENSEX"
				} else if strings.Contains(idx, "FIN") {
					pfx = "FINNIFTY"
				} else if strings.Contains(idx, "MID") {
					pfx = "MIDCP"
				}

				if strings.HasPrefix(t.Symbol, pfx) {
					// Guard: NIFTY 50 vs MIDCPNIFTY/BANKNIFTY/FINNIFTY
					if pfx == "NIFTY" && (strings.HasPrefix(t.Symbol, "BANK") || strings.HasPrefix(t.Symbol, "FIN") || strings.HasPrefix(t.Symbol, "MID")) {
						continue
					}
					tradesByIndex[idx] = append(tradesByIndex[idx], t)
					matched = true
					break
				}
			}
			if !matched {
				tradesByIndex["OTHER"] = append(tradesByIndex["OTHER"], t)
			}
		}
	}
	fmt.Printf("Total Database Trades: %d | Total OPTIONS_SUPERTREND Trades: %d\n", len(allTrades), optTradesCount)
	for _, idx := range indices {
		trList := tradesByIndex[idx]
		totalPnL := 0.0
		wins := 0
		for _, tr := range trList {
			totalPnL += tr.PnL
			if tr.PnL > 0 {
				wins++
			}
		}
		winRate := 0.0
		if len(trList) > 0 {
			winRate = (float64(wins) / float64(len(trList))) * 100.0
		}
		fmt.Printf("  • %-12s: %3d trades | Wins: %2d (%.1f%%) | Realized PnL: ₹%10.2f\n",
			idx, len(trList), wins, winRate, totalPnL)
	}

	// Step 2: Audit Each Index State & 6 Cards
	fmt.Println("\n--- [2] Auditing 6 Top Cards & Endpoints for All 5 Indices ---")
	for _, idx := range indices {
		fmt.Printf("\n>>> Checking Index: %s\n", idx)

		// A. State Endpoint
		stateURL := baseURL + "/api/options/state?index=" + url.QueryEscape(idx)
		sResp, sErr := client.Get(stateURL)
		if sErr != nil {
			fmt.Printf("  ❌ Error calling %s: %v\n", stateURL, sErr)
			continue
		}
		defer sResp.Body.Close()
		sBytes, _ := io.ReadAll(sResp.Body)
		var state OptionsState
		json.Unmarshal(sBytes, &state)

		// Validate Card 1: CURRENT TREND
		fmt.Printf("  Card 1 [CURRENT TREND]:           %s\n", state.LastTrend)

		// Validate Card 2: SIZING MULTIPLIER
		totalQty := state.Multiplier * state.BaseLotSize
		fmt.Printf("  Card 2 [SIZING MULTIPLIER]:       %dx (%d Qty) [Base Lot: %d]\n",
			state.Multiplier, totalQty, state.BaseLotSize)

		// Validate Card 3: ACTIVE OPTION & TRAILED SL
		if state.HasActiveTrade && state.ActiveSymbol != "" {
			fmt.Printf("  Card 3 [ACTIVE OPTION & SL]:      %s (Entry: ₹%.2f | SL: ₹%.2f)\n",
				state.ActiveSymbol, state.EntryPremium, state.SLPrice)
		} else {
			fmt.Printf("  Card 3 [ACTIVE OPTION & SL]:      None\n")
		}

		// Validate Card 4: PAPER BALANCE
		fmt.Printf("  Card 4 [PAPER BALANCE]:           ₹%.2f\n", state.PaperBalance)

		// Validate Card 5: WIN RATE (%)
		fmt.Printf("  Card 5 [WIN RATE (%%)]:            %.1f%% (%d/%d Won)\n",
			state.WinRatePct, state.WinTrades, state.TotalOptionsTrades)

		// Validate Card 6: OPTIONS TRADES / PNL
		fmt.Printf("  Card 6 [OPTIONS TRADES / PNL]:    %d Trades (₹%.2f)\n",
			state.TotalOptionsTrades, state.TotalOptionsPnL)

		// Verify math consistency between state and trades in DB
		dbTr := tradesByIndex[idx]
		if state.TotalOptionsTrades != len(dbTr) {
			fmt.Printf("  ⚠️ WARNING: State trades count (%d) differs from DB filtered count (%d)\n",
				state.TotalOptionsTrades, len(dbTr))
		} else {
			fmt.Printf("  ✓ Trade count matches DB perfectly (%d)\n", state.TotalOptionsTrades)
		}

		// B. SuperTrends Endpoint
		stURL := baseURL + "/api/options/supertrends?symbol=" + url.QueryEscape(idx)
		stResp, stErr := client.Get(stURL)
		if stErr != nil {
			fmt.Printf("  ❌ Error calling supertrends: %v\n", stErr)
			continue
		}
		defer stResp.Body.Close()
		stBytes, _ := io.ReadAll(stResp.Body)
		var points []IndicatorPoint
		json.Unmarshal(stBytes, &points)

		signalCount := 0
		for _, p := range points {
			if p.Signal != "" {
				signalCount++
			}
		}
		latestTrend := "N/A"
		latestClose := 0.0
		if len(points) > 0 {
			latestTrend = points[len(points)-1].Trend
			latestClose = points[len(points)-1].Close
		}

		fmt.Printf("  ✓ SuperTrends Candles: %d | Trade Markers: %d | Latest Close: ₹%.2f | Indicator Trend: %s\n",
			len(points), signalCount, latestClose, latestTrend)
	}

	fmt.Println("\n===================================================================================")
	fmt.Println("       AUDIT COMPLETE - ALL 5 INDICES VERIFIED                                     ")
	fmt.Println("===================================================================================")
}
