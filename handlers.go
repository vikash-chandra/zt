package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleDashboard serves the main HTML file
func (tb *TradingBot) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(dashboardHTML)
}

// handleRootRedirect redirects requests from / to /zt
func (tb *TradingBot) handleRootRedirect(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		http.Redirect(w, r, "/zt", http.StatusMovedPermanently)
		return
	}
	http.NotFound(w, r)
}

// handleWatchlist handles query to resolve active watchlist symbols and state
func (tb *TradingBot) handleWatchlist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	tb.watchlistMutex.RLock()
	wlCopy := make(map[string]int64)
	for k, v := range tb.watchlist {
		wlCopy[k] = v
	}
	tb.watchlistMutex.RUnlock()

	if len(wlCopy) == 0 {
		fallback, err := tb.db.GetWatchlistFallback(tb.ctx)
		if err == nil {
			for k, v := range fallback {
				wlCopy[k] = v
			}
		}
	}

	totalTrades, totalPnL, totalTxValue, _ := tb.db.GetTradingMetrics(tb.ctx)

	var pctOnAccount float64 = 0.0
	if tb.cfg.InitialCapital > 0 {
		pctOnAccount = (totalPnL / tb.cfg.InitialCapital) * 100.0
	}

	var pctOnMargin float64 = 0.0
	if totalTxValue > 0 {
		marginUtilized := totalTxValue / 5.0
		pctOnMargin = (totalPnL / marginUtilized) * 100.0
	}

	advances, declines, neutrals, globalBias, _ := tb.db.GetLatestMarketBreadth(tb.ctx)

	if globalBias == "" {
		globalBias = tb.globalBias
	}

	response := map[string]interface{}{
		"watchlist":         wlCopy,
		"global_bias":       globalBias,
		"advances":          advances,
		"declines":          declines,
		"neutrals":          neutrals,
		"stock_select_time": tb.cfg.StockSelectTime,
		"total_trades":      totalTrades,
		"total_pnl":         totalPnL,
		"pct_on_account":    pctOnAccount,
		"pct_on_margin":     pctOnMargin,
	}

	json.NewEncoder(w).Encode(response)
}

// handleCandles serves start-of-day candles for chart indicators
func (tb *TradingBot) handleCandles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		http.Error(w, `{"error":"symbol parameter required"}`, http.StatusBadRequest)
		return
	}

	tb.watchlistMutex.RLock()
	token, exists := tb.watchlist[symbol]
	tb.watchlistMutex.RUnlock()

	if !exists {
		var err error
		token, err = tb.db.ResolveSymbolToken(tb.ctx, symbol)
		if err != nil || token <= 0 {
			http.Error(w, `{"error":"symbol not found in watchlist or database cache"}`, http.StatusNotFound)
			return
		}
	}

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).UTC()

	candles, err := tb.db.GetCandlesForDay(tb.ctx, token, todayStart)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"database query failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	type APICandle struct {
		Time   int64   `json:"time"`
		Open   float64 `json:"open"`
		High   float64 `json:"high"`
		Low    float64 `json:"low"`
		Close  float64 `json:"close"`
		Volume int64   `json:"volume"`
	}

	list := make([]APICandle, 0)
	for _, c := range candles {
		list = append(list, APICandle{
			Time:   c.Time.In(loc).Unix(),
			Open:   c.Open,
			High:   c.High,
			Low:    c.Low,
			Close:  c.Close,
			Volume: c.Volume,
		})
	}

	json.NewEncoder(w).Encode(list)
}

// handleTrades returns filled orders today to mark entry/exits on chart
func (tb *TradingBot) handleTrades(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		http.Error(w, `{"error":"symbol parameter required"}`, http.StatusBadRequest)
		return
	}

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).UTC()

	trades, err := tb.db.GetTradesForSymbolToday(tb.ctx, symbol, todayStart)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"database query failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	type APITrade struct {
		Time            int64   `json:"time"`
		TransactionType string  `json:"transaction_type"`
		Price           float64 `json:"price"`
		Quantity        int     `json:"quantity"`
	}

	list := make([]APITrade, 0)
	for _, t := range trades {
		list = append(list, APITrade{
			Time:            t.Time.In(loc).Unix(),
			TransactionType: t.TransactionType,
			Price:           t.Price,
			Quantity:        t.Quantity,
		})
	}

	json.NewEncoder(w).Encode(list)
}

// handleTradesAll returns full trades history
func (tb *TradingBot) handleTradesAll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	history, err := tb.db.GetAllTradesHistory(tb.ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"database query failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	type TradeRecord struct {
		ID              int     `json:"id"`
		Symbol          string  `json:"symbol"`
		EntryPrice      float64 `json:"entry_price"`
		ExitPrice       float64 `json:"exit_price"`
		Quantity        int     `json:"quantity"`
		PnL             float64 `json:"pnl"`
		Side            string  `json:"side"`
		TimeHeldMinutes int     `json:"time_held_minutes"`
		CreatedAt       int64   `json:"created_at"`
		Strategy        string  `json:"strategy"`
	}

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}

	list := make([]TradeRecord, 0)
	for _, t := range history {
		list = append(list, TradeRecord{
			ID:              t.ID,
			Symbol:          t.Symbol,
			EntryPrice:      t.EntryPrice,
			ExitPrice:       t.ExitPrice,
			Quantity:        t.Quantity,
			PnL:             t.PnL,
			Side:            t.Side,
			TimeHeldMinutes: t.TimeHeldMinutes,
			CreatedAt:       t.CreatedAt.In(loc).Unix(),
			Strategy:        t.Strategy,
		})
	}

	json.NewEncoder(w).Encode(list)
}
