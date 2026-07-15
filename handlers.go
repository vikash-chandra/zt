package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"zerodha-trading/config"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
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

	ticks, loss := tb.ticker.GetMetrics()
	connected := tb.ticker.IsConnected()

	response := map[string]interface{}{
		"watchlist":               wlCopy,
		"global_bias":             globalBias,
		"advances":                advances,
		"declines":                declines,
		"neutrals":                neutrals,
		"stock_select_time":       tb.cfg.StockSelectTime,
		"evg_stock_select_time":   tb.cfg.EVGStockSelectTime,
		"total_trades":            totalTrades,
		"total_pnl":               totalPnL,
		"pct_on_account":          pctOnAccount,
		"pct_on_margin":           pctOnMargin,
		"initial_capital":         tb.cfg.InitialCapital,
		"manual_bias_cutoff":      tb.cfg.ManualBiasCutoff,
		"manual_watchlist_cutoff": tb.cfg.ManualWatchlistCutoff,
		"auto_square_off_time":    tb.cfg.AutoSquareOffTime,
		"ticker_ticks":            ticks,
		"ticker_loss":             loss,
		"ticker_connected":        connected,
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

	dateStr := r.URL.Query().Get("date")

	tb.watchlistMutex.RLock()
	token, exists := tb.watchlist[symbol]
	tb.watchlistMutex.RUnlock()

	if !exists {
		var err error
		token, err = tb.db.ResolveSymbolToken(tb.ctx, symbol)
		if err != nil || token <= 0 {
			token, err = tb.securityMaster.GetInstrumentToken(symbol)
			if err != nil || token <= 0 {
				token, err = tb.securityMaster.ResolveAndAddSymbol(tb.ctx, symbol)
			}
		}
	}

	if token <= 0 {
		http.Error(w, `{"error":"symbol not found on Zerodha or database cache"}`, http.StatusNotFound)
		return
	}

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}

	var dayStart time.Time
	if dateStr != "" {
		parsedDate, err := time.ParseInLocation("2006-01-02", dateStr, loc)
		if err == nil {
			dayStart = time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 0, 0, 0, 0, loc).UTC()
		}
	}

	if dayStart.IsZero() {
		now := time.Now().In(loc)
		dayStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).UTC()
	}

	type APICandle struct {
		Time   int64   `json:"time"`
		Open   float64 `json:"open"`
		High   float64 `json:"high"`
		Low    float64 `json:"low"`
		Close  float64 `json:"close"`
		Volume int64   `json:"volume"`
		VWAP   float64 `json:"vwap"`
		Color  string  `json:"color"`
	}

	// Calculate expected candles for this date
	locTime := dayStart.In(loc)
	expectedCandles := 75 // Default count for a full past market day
	now := time.Now().In(loc)
	isToday := locTime.Year() == now.Year() && locTime.Month() == now.Month() && locTime.Day() == now.Day()
	if isToday {
		marketStart := time.Date(now.Year(), now.Month(), now.Day(), 9, 15, 0, 0, loc)
		marketEnd := time.Date(now.Year(), now.Month(), now.Day(), 15, 30, 0, 0, loc)
		if now.Before(marketStart) {
			expectedCandles = 0
		} else {
			refTime := now
			if refTime.After(marketEnd) {
				refTime = marketEnd
			}
			expectedCandles = int(refTime.Sub(marketStart).Minutes()) / 5
		}
	}

	tolerance := 0
	if isToday {
		tolerance = 1
	}

	// 1. Try fetching from the database first for the specific day range
	candles, err := tb.db.GetCandlesForDate(tb.ctx, token, dayStart)
	if err == nil && len(candles) >= (expectedCandles-tolerance) && len(candles) > 0 {
		list := make([]APICandle, 0)
		for _, c := range candles {
			color := "DOJI"
			if c.Close > c.Open {
				color = "GREEN"
			} else if c.Close < c.Open {
				color = "RED"
			}
			vwap := (c.Open + c.High + c.Low + c.Close) / 4.0
			list = append(list, APICandle{
				Time:   normalizeTime(c.Time, loc).Unix(),
				Open:   c.Open,
				High:   c.High,
				Low:    c.Low,
				Close:  c.Close,
				Volume: c.Volume,
				VWAP:   vwap,
				Color:  color,
			})
		}
		json.NewEncoder(w).Encode(list)
		return
	}

	// 2. Fall back to Zerodha API if database has incomplete candles
	startTime := time.Date(locTime.Year(), locTime.Month(), locTime.Day(), 9, 15, 0, 0, loc)
	endTime := time.Date(locTime.Year(), locTime.Month(), locTime.Day(), 15, 30, 0, 0, loc)

	if startTime.After(now) {
		// Requested date is in the future
		json.NewEncoder(w).Encode([]APICandle{})
		return
	}
	if endTime.After(now) {
		endTime = now
	}

	if tb.kiteClient == nil {
		http.Error(w, `{"error":"Zerodha API client not initialized for fallback"}`, http.StatusInternalServerError)
		return
	}

	tb.logger.Info("Database has no candles for date, falling back to Zerodha API", map[string]interface{}{
		"symbol":     symbol,
		"date":       locTime.Format("2006-01-02"),
		"start_time": startTime.Format("15:04:05"),
		"end_time":   endTime.Format("15:04:05"),
	})

	apiCandles, apiErr := tb.kiteClient.GetHistoricalData(int(token), "5minute", startTime, endTime, false, false)
	if apiErr != nil {
		tb.logger.Error("Zerodha API fallback failed", map[string]interface{}{"error": apiErr.Error(), "symbol": symbol})
		http.Error(w, fmt.Sprintf(`{"error":"Zerodha API fallback failed: %s"}`, apiErr.Error()), http.StatusInternalServerError)
		return
	}

	// 3. Cache API candles to database asynchronously to protect Zerodha limits
	if len(apiCandles) > 0 {
		go func() {
			if err := tb.db.SaveHistoricalCandles(tb.ctx, token, apiCandles, "candles_5m"); err != nil {
				tb.logger.Error("Failed to save fallback candles to database", map[string]interface{}{"error": err.Error(), "symbol": symbol})
			}
		}()
	}

	list := make([]APICandle, 0)
	for _, c := range apiCandles {
		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}
		vwap := (c.Open + c.High + c.Low + c.Close) / 4.0
		list = append(list, APICandle{
			Time:   normalizeTime(c.Date.Time, loc).Unix(),
			Open:   c.Open,
			High:   c.High,
			Low:    c.Low,
			Close:  c.Close,
			Volume: int64(c.Volume),
			VWAP:   vwap,
			Color:  color,
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

// handleDailyBias handles getting and setting manual bias configuration
func (tb *TradingBot) handleDailyBias(w http.ResponseWriter, r *http.Request) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.UTC
	}
	nowInLoc := time.Now().In(loc)

	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		bias, err := tb.db.GetDailyBias(tb.ctx, nowInLoc)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get daily bias: %v", err), http.StatusInternalServerError)
			return
		}
		response := map[string]interface{}{
			"date": nowInLoc.Format("2006-01-02"),
			"bias": bias,
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			Date string `json:"date"` // optional, YYYY-MM-DD
			Bias string `json:"bias"` // BUY_ONLY, SELL_ONLY, NO_TRADE, CALCULATE
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON request body", http.StatusBadRequest)
			return
		}

		var targetDate time.Time
		if req.Date == "" {
			targetDate = nowInLoc
		} else {
			parsedDate, err := time.ParseInLocation("2006-01-02", req.Date, loc)
			if err != nil {
				http.Error(w, "Invalid date format. Expected YYYY-MM-DD", http.StatusBadRequest)
				return
			}
			targetDate = parsedDate
		}

		todayStr := nowInLoc.Format("2006-01-02")
		targetStr := targetDate.Format("2006-01-02")

		if targetStr == todayStr {
			cutoffHour := 9
			cutoffMinute := 28
			if _, sScanErr := fmt.Sscanf(tb.cfg.ManualBiasCutoff, "%d:%d", &cutoffHour, &cutoffMinute); sScanErr != nil {
				tb.logger.Error("Failed to parse MANUAL_BIAS_CUTOFF configuration, using default 09:28", map[string]interface{}{"val": tb.cfg.ManualBiasCutoff, "error": sScanErr.Error()})
				cutoffHour = 9
				cutoffMinute = 28
			}

			cutOffTime := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), cutoffHour, cutoffMinute, 0, 0, loc)
			if nowInLoc.After(cutOffTime) || nowInLoc.Equal(cutOffTime) {
				http.Error(w, fmt.Sprintf("Cannot set or change daily bias after %s IST", tb.cfg.ManualBiasCutoff), http.StatusBadRequest)
				return
			}
		} else if targetDate.Before(time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), 0, 0, 0, 0, loc)) {
			http.Error(w, "Cannot set daily bias for past dates", http.StatusBadRequest)
			return
		}

		switch req.Bias {
		case "BUY_ONLY", "SELL_ONLY", "NO_TRADE":
			err = tb.db.SaveDailyBias(tb.ctx, targetDate, req.Bias)
		case "CALCULATE", "":
			err = tb.db.DeleteDailyBias(tb.ctx, targetDate)
		default:
			http.Error(w, "Invalid bias value. Allowed values: BUY_ONLY, SELL_ONLY, NO_TRADE, CALCULATE", http.StatusBadRequest)
			return
		}

		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to save daily bias: %v", err), http.StatusInternalServerError)
			return
		}

		if targetStr == todayStr {
			if req.Bias == "CALCULATE" || req.Bias == "" {
				tb.globalBias = ""
			} else {
				tb.globalBias = req.Bias
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": fmt.Sprintf("Daily bias for %s set to %s", targetStr, req.Bias),
		})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// handleDailyManualWatchlist handles getting and setting manual stock selections
func (tb *TradingBot) handleDailyManualWatchlist(w http.ResponseWriter, r *http.Request) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.UTC
	}
	nowInLoc := time.Now().In(loc)

	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		symbols, err := tb.db.GetDailyManualWatchlist(tb.ctx, nowInLoc)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get manual watchlist: %v", err), http.StatusInternalServerError)
			return
		}
		var symStr string
		for i, s := range symbols {
			if i > 0 {
				symStr += ","
			}
			symStr += s
		}
		response := map[string]interface{}{
			"date":    nowInLoc.Format("2006-01-02"),
			"symbols": symStr,
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			Date    string `json:"date"`    // optional, YYYY-MM-DD
			Symbols string `json:"symbols"` // comma-separated symbols (e.g. SBIN,TCS)
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON request body", http.StatusBadRequest)
			return
		}

		var targetDate time.Time
		if req.Date == "" {
			targetDate = nowInLoc
		} else {
			parsedDate, err := time.ParseInLocation("2006-01-02", req.Date, loc)
			if err != nil {
				http.Error(w, "Invalid date format. Expected YYYY-MM-DD", http.StatusBadRequest)
				return
			}
			targetDate = parsedDate
		}

		todayStr := nowInLoc.Format("2006-01-02")
		targetStr := targetDate.Format("2006-01-02")

		if targetStr == todayStr {
			cutoffHour := 9
			cutoffMinute := 25
			if _, sScanErr := fmt.Sscanf(tb.cfg.ManualWatchlistCutoff, "%d:%d", &cutoffHour, &cutoffMinute); sScanErr != nil {
				tb.logger.Error("Failed to parse MANUAL_WATCHLIST_CUTOFF configuration, using default 09:25", map[string]interface{}{"val": tb.cfg.ManualWatchlistCutoff, "error": sScanErr.Error()})
				cutoffHour = 9
				cutoffMinute = 25
			}

			cutOffTime := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), cutoffHour, cutoffMinute, 0, 0, loc)
			if nowInLoc.After(cutOffTime) || nowInLoc.Equal(cutOffTime) {
				http.Error(w, fmt.Sprintf("Cannot set or change manual stocks after %s IST", tb.cfg.ManualWatchlistCutoff), http.StatusBadRequest)
				return
			}
		} else if targetDate.Before(time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), 0, 0, 0, 0, loc)) {
			http.Error(w, "Cannot set manual stocks for past dates", http.StatusBadRequest)
			return
		}

		var cleanedSymbols string
		var current string
		for i := 0; i < len(req.Symbols); i++ {
			c := req.Symbols[i]
			if c == ',' {
				if len(current) > 0 {
					if len(cleanedSymbols) > 0 {
						cleanedSymbols += ","
					}
					cleanedSymbols += current
					current = ""
				}
			} else {
				if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
					if c >= 'a' && c <= 'z' {
						c = c - 'a' + 'A'
					}
					current += string(c)
				}
			}
		}
		if len(current) > 0 {
			if len(cleanedSymbols) > 0 {
				cleanedSymbols += ","
			}
			cleanedSymbols += current
		}

		if cleanedSymbols == "" || cleanedSymbols == "CALCULATE" {
			err = tb.db.DeleteDailyManualWatchlist(tb.ctx, targetDate)
		} else {
			err = tb.db.SaveDailyManualWatchlist(tb.ctx, targetDate, cleanedSymbols)
		}

		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to save daily manual watchlist: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": fmt.Sprintf("Daily manual watchlist for %s set to %s", targetStr, cleanedSymbols),
		})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// handlePreSelections returns all pre-selection results for a given date and rule set
func (tb *TradingBot) handlePreSelections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	dateStr := r.URL.Query().Get("date")
	if dateStr == "" {
		var err error
		dateStr, err = tb.db.GetLatestPreSelectionDate()
		if err != nil || dateStr == "" {
			dateStr = time.Now().Format("2006-01-02")
		}
	}

	ruleSet := r.URL.Query().Get("rule_set")
	if ruleSet == "" {
		ruleSet = "STANDARD"
	}

	results, err := tb.db.GetPreSelectionResults(dateStr, ruleSet)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to query pre-selections: %v", err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(results)
}

var (
	lastTokenExchange  time.Time
	tokenExchangeMutex sync.Mutex
)

// handleConfigAccessToken handles updating the KITE_ACCESS_TOKEN from the UI
func (tb *TradingBot) handleConfigAccessToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		RequestToken string `json:"request_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	requestToken := strings.TrimSpace(req.RequestToken)
	prefix := strings.TrimSpace(tb.cfg.TokenPrefix)
	if prefix != "" && strings.HasPrefix(requestToken, prefix) {
		requestToken = strings.TrimPrefix(requestToken, prefix)
	}

	if requestToken == "" {
		http.Error(w, "Request token cannot be empty", http.StatusBadRequest)
		return
	}

	// Enforce Timing and Rate Limits on Request Token Exchange
	if tb.cfg.APIKey != "api_key" && tb.cfg.APIKey != "test_key" {
		loc, err := time.LoadLocation("Asia/Kolkata")
		if err != nil {
			loc = time.Local
		}
		nowIST := time.Now().In(loc)

		// 1. Timing check: must be 07:30 AM to 10:00 AM IST
		startLimit := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 7, 30, 0, 0, loc)
		endLimit := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 10, 0, 0, 0, loc)
		if nowIST.Before(startLimit) || nowIST.After(endLimit) {
			tb.logger.Warn("Request token exchange blocked: outside allowed window (07:30 AM - 10:00 AM IST)", map[string]interface{}{
				"current_time": nowIST.Format("15:04:05"),
			})
			http.Error(w, `{"error":"Request token exchange is only allowed between 07:30 AM and 10:00 AM IST"}`, http.StatusForbidden)
			return
		}

		// 2. Frequency check: at most 1 request every 5 minutes globally
		tokenExchangeMutex.Lock()
		if !lastTokenExchange.IsZero() && time.Since(lastTokenExchange) < 5*time.Minute {
			remaining := 5*time.Minute - time.Since(lastTokenExchange)
			tokenExchangeMutex.Unlock()
			tb.logger.Warn("Request token exchange blocked: rate limit active", map[string]interface{}{
				"cooldown_remaining": fmt.Sprintf("%.1fs", 0.0),
			})
			http.Error(w, fmt.Sprintf(`{"error":"Request token exchange is rate-limited. Please wait another %.1f seconds"}`, remaining.Seconds()), http.StatusTooManyRequests)
			return
		}
		lastTokenExchange = time.Now()
		tokenExchangeMutex.Unlock()
	}

	var rawToken string
	if tb.cfg.APIKey == "api_key" || tb.cfg.APIKey == "test_key" {
		// Mock token generation for unit testing
		rawToken = requestToken
	} else {
		if tb.cfg.APIKey == "" || tb.cfg.APISecret == "" {
			http.Error(w, `{"error":"API_KEY or API_SECRET is not configured in .env"}`, http.StatusBadRequest)
			return
		}

		tb.logger.Info("Exchanging request token dynamically via Zerodha API...", map[string]interface{}{"request_token": requestToken})

		// Exchange request token for access token using Zerodha API
		client := kiteconnect.New(tb.cfg.APIKey)
		session, err := client.GenerateSession(requestToken, tb.cfg.APISecret)
		if err != nil {
			tb.logger.Error("Failed to generate session from request token", map[string]interface{}{"error": err.Error()})
			http.Error(w, fmt.Sprintf(`{"error":"Zerodha GenerateSession failed: %s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		rawToken = session.AccessToken
		tb.logger.Info("Successfully exchanged request token for access token", map[string]interface{}{"user_name": session.UserName})
	}

	// 1. Update memory configuration
	tb.cfg.AccessToken = rawToken
	if tb.kiteClient != nil {
		tb.kiteClient.SetAccessToken(rawToken)
	}
	if tb.ticker != nil {
		tb.ticker.SetAccessToken(rawToken)
	}

	// 2. Save back to database metadata cache to persist across container restarts (using postgres volume)
	if tb.db != nil {
		if err := tb.db.SaveMetadataCache(tb.ctx, "config:kite_access_token", rawToken); err != nil {
			tb.logger.Error("Failed to save KITE_ACCESS_TOKEN to database cache", map[string]interface{}{"error": err.Error()})
		}
	}

	// 3. Save back to .env file to persist across restarts in non-docker environments
	if err := config.SaveAccessTokenToEnv(".env", rawToken); err != nil {
		tb.logger.Error("Failed to save KITE_ACCESS_TOKEN to .env", map[string]interface{}{"error": err.Error()})
		// Do not return error response to user, since the in-memory update worked
	}

	tb.logger.Info("Successfully updated KITE_ACCESS_TOKEN dynamically from UI", nil)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Access token updated successfully. Container self-restart triggered.",
	})

	// Trigger container restart by exiting the process. Docker/K8s will automatically restart it.
	go func() {
		tb.logger.Info("Initiating container self-restart in 1.5 seconds to apply the new access token...", nil)
		time.Sleep(1500 * time.Millisecond)
		os.Exit(0)
	}()
}

// normalizeTime normalizes timezones between seeded UTC-named times and live UTC times.
func normalizeTime(t time.Time, loc *time.Location) time.Time {
	if t.Hour() >= 9 {
		// Seeded UTC-named time (e.g. 09:15 UTC actually means 09:15 IST)
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
	}
	// Live UTC time (e.g. 03:45 UTC is 09:15 IST)
	return t.In(loc)
}
