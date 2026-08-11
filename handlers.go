package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/risk"
	"zerodha-trading/strategy"
)

// handleDashboard serves the main HTML file
func (tb *TradingBot) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
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

	nowIST := time.Now().In(data.ISTLocation)
	todayStr := nowIST.Format("2006-01-02")

	// Get select time from config
	selectHour, selectMin, errTime := parseTimeHM(tb.cfg.StockSelectTime)
	if errTime != nil {
		selectHour, selectMin = 9, 25
	}
	selectTime := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), selectHour, selectMin, 0, 0, data.ISTLocation)

	wlCopy := make(map[string]int64)
	symbolStrats := make(map[string][]string)

	if nowIST.Before(selectTime) {
		// Before 09:25 AM, show all F&O stocks
		allStocks, errStocks := tb.db.GetAllFOStocks(tb.ctx)
		if errStocks == nil && len(allStocks) > 0 {
			wlCopy = allStocks
		} else {
			// Fallback to in-memory if DB call fails
			tb.watchlistMutex.RLock()
			for k, v := range tb.watchlist {
				wlCopy[k] = v
			}
			tb.watchlistMutex.RUnlock()
		}
	} else {
		// After 09:25 AM, show only the saved watchlist from DB
		dbItems, errItems := tb.db.GetDailyWatchlist(tb.ctx, todayStr)
		if errItems == nil && len(dbItems) > 0 {
			for _, item := range dbItems {
				wlCopy[item.Symbol] = item.Token

				// Reconstruct symbolStrats from selectors string
				if item.Selectors != "" {
					parts := strings.Split(item.Selectors, ",")
					for _, part := range parts {
						subParts := strings.Split(part, ":")
						if len(subParts) >= 2 {
							selectorName := subParts[1]
							shortName := "FO"
							if selectorName == "SECTORAL" || selectorName == "SECTORAL_SELECTOR" {
								shortName = "SEC"
							} else if selectorName == "EQUITY_VOLUME_GAINERS" {
								shortName = "EVG"
							} else if selectorName == "SECURITIES_FO" {
								shortName = "FO"
							} else {
								shortName = selectorName
							}

							// Check duplicate
							alreadyHas := false
							for _, existing := range symbolStrats[item.Symbol] {
								if existing == shortName {
									alreadyHas = true
									break
								}
							}
							if !alreadyHas {
								symbolStrats[item.Symbol] = append(symbolStrats[item.Symbol], shortName)
							}
						}
					}
				}
			}
		} else {
			// Fallback to in-memory if DB has no records yet
			tb.watchlistMutex.RLock()
			for k, v := range tb.watchlist {
				wlCopy[k] = v
			}
			tb.watchlistMutex.RUnlock()

			// Reconstruct from strategyWatchlists in memory
			tb.watchlistMutex.RLock()
			for stratName, wList := range tb.strategyWatchlists {
				selectorName := tb.strategySelectorMap[stratName]
				shortName := "FO"
				if selectorName == "SECTORAL" || selectorName == "SECTORAL_SELECTOR" {
					shortName = "SEC"
				} else if selectorName == "EQUITY_VOLUME_GAINERS" {
					shortName = "EVG"
				} else if selectorName == "SECURITIES_FO" {
					shortName = "FO"
				} else if selectorName != "" {
					shortName = selectorName
				}
				for sym := range wList {
					alreadyHas := false
					for _, existing := range symbolStrats[sym] {
						if existing == shortName {
							alreadyHas = true
							break
						}
					}
					if !alreadyHas {
						symbolStrats[sym] = append(symbolStrats[sym], shortName)
					}
				}
			}
			tb.watchlistMutex.RUnlock()
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

	// Also check manual watchlist
	manualSymbols, errManual := tb.db.GetDailyManualWatchlist(tb.ctx, time.Now())
	if errManual == nil && len(manualSymbols) > 0 {
		for _, sym := range manualSymbols {
			sym = strings.TrimSpace(sym)
			if sym != "" {
				alreadyHasM := false
				for _, sName := range symbolStrats[sym] {
					if sName == "M" {
						alreadyHasM = true
						break
					}
				}
				if !alreadyHasM {
					symbolStrats[sym] = append(symbolStrats[sym], "M")
				}
			}
		}
	}

	sectors, err := tb.db.GetSelectedSectors(tb.ctx, todayStr)
	if err != nil {
		sectors = []data.SelectedSectorRecord{}
	}

	var openPositions interface{} = nil
	if tb.riskMgr != nil {
		openPositions = tb.riskMgr.GetOpenPositions()
	}

	response := map[string]interface{}{
		"watchlist":               wlCopy,
		"watchlist_strategies":    symbolStrats,
		"selected_sectors":        sectors,
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
		"open_positions":          openPositions,
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

	var dayStart time.Time
	if dateStr != "" {
		parsedDate, err := time.ParseInLocation("2006-01-02", dateStr, data.ISTLocation)
		if err == nil {
			dayStart = time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 0, 0, 0, 0, data.ISTLocation).UTC()
		}
	}

	if dayStart.IsZero() {
		now := time.Now().In(data.ISTLocation)
		dayStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, data.ISTLocation).UTC()
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
	locTime := dayStart.In(data.ISTLocation)
	expectedCandles := 75 // Default count for a full past market day
	now := time.Now().In(data.ISTLocation)
	isToday := locTime.Year() == now.Year() && locTime.Month() == now.Month() && locTime.Day() == now.Day()
	if isToday {
		marketStart := time.Date(now.Year(), now.Month(), now.Day(), 9, 15, 0, 0, data.ISTLocation)
		marketEnd := time.Date(now.Year(), now.Month(), now.Day(), 15, 30, 0, 0, data.ISTLocation)
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
		list := make([]APICandle, 0, len(candles))
		for _, c := range candles {
			color := "DOJI"
			if c.Close > c.Open {
				color = "GREEN"
			} else if c.Close < c.Open {
				color = "RED"
			}
			vwap := (c.Open + c.High + c.Low + c.Close) / 4.0
			list = append(list, APICandle{
				Time:   c.Time.Unix(), // Time is already normalized by GetCandlesForDate
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
	startTime := time.Date(locTime.Year(), locTime.Month(), locTime.Day(), 9, 15, 0, 0, data.ISTLocation)
	endTime := time.Date(locTime.Year(), locTime.Month(), locTime.Day(), 15, 30, 0, 0, data.ISTLocation)

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
		tb.logger.Error("Zerodha API fallback failed, trying to return available DB candles", map[string]interface{}{"error": apiErr.Error(), "symbol": symbol})
		if len(candles) > 0 {
			list := make([]APICandle, 0, len(candles))
			for _, c := range candles {
				color := "DOJI"
				if c.Close > c.Open {
					color = "GREEN"
				} else if c.Close < c.Open {
					color = "RED"
				}
				vwap := (c.Open + c.High + c.Low + c.Close) / 4.0
				list = append(list, APICandle{
					Time:   c.Time.Unix(),
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
			Time:   normalizeTime(c.Date).Unix(),
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

	now := time.Now().In(data.ISTLocation)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, data.ISTLocation).UTC()

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
			Time:            data.NormalizeToIST(t.Time).Unix(),
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
		EntryTime       int64   `json:"entry_time"`
		ExitTime        int64   `json:"exit_time"`
		CreatedAt       int64   `json:"created_at"`
		Strategy        string  `json:"strategy"`
	}

	list := make([]TradeRecord, 0)
	for _, t := range history {
		createdTime := data.NormalizeToIST(t.CreatedAt)
		if createdTime.Hour() < 9 {
			createdTime = createdTime.Add(5*time.Hour + 30*time.Minute)
		}

		exitTime := data.NormalizeToIST(t.ExitTime)
		if t.ExitTime.IsZero() {
			exitTime = createdTime
		} else if exitTime.Hour() < 9 {
			exitTime = exitTime.Add(5*time.Hour + 30*time.Minute)
		}

		entryTime := data.NormalizeToIST(t.EntryTime)
		if t.EntryTime.IsZero() {
			timeHeld := t.TimeHeldMinutes
			if timeHeld <= 0 {
				timeHeld = 1
			}
			entryTime = exitTime.Add(-time.Duration(timeHeld) * time.Minute)
		} else if entryTime.Hour() < 9 {
			entryTime = entryTime.Add(5*time.Hour + 30*time.Minute)
		}

		list = append(list, TradeRecord{
			ID:              t.ID,
			Symbol:          t.Symbol,
			EntryPrice:      t.EntryPrice,
			ExitPrice:       t.ExitPrice,
			Quantity:        t.Quantity,
			PnL:             t.PnL,
			Side:            t.Side,
			TimeHeldMinutes: t.TimeHeldMinutes,
			EntryTime:       entryTime.Unix(),
			ExitTime:        exitTime.Unix(),
			CreatedAt:       createdTime.Unix(),
			Strategy:        t.Strategy,
		})
	}

	json.NewEncoder(w).Encode(list)
}

// handleDailyBias handles getting and setting manual bias configuration
func (tb *TradingBot) handleDailyBias(w http.ResponseWriter, r *http.Request) {
	nowInLoc := time.Now().In(data.ISTLocation)

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

		var err error
		var targetDate time.Time
		if req.Date == "" {
			targetDate = nowInLoc
		} else {
			parsedDate, pErr := time.ParseInLocation("2006-01-02", req.Date, data.ISTLocation)
			if pErr != nil {
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

			cutOffTime := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), cutoffHour, cutoffMinute, 0, 0, data.ISTLocation)
			if nowInLoc.After(cutOffTime) || nowInLoc.Equal(cutOffTime) {
				http.Error(w, fmt.Sprintf("Cannot set or change daily bias after %s IST", tb.cfg.ManualBiasCutoff), http.StatusBadRequest)
				return
			}
		} else if targetDate.Before(time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), 0, 0, 0, 0, data.ISTLocation)) {
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
	nowInLoc := time.Now().In(data.ISTLocation)

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

		var err error
		var targetDate time.Time
		if req.Date == "" {
			targetDate = nowInLoc
		} else {
			parsedDate, pErr := time.ParseInLocation("2006-01-02", req.Date, data.ISTLocation)
			if pErr != nil {
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

			cutOffTime := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), cutoffHour, cutoffMinute, 0, 0, data.ISTLocation)
			if nowInLoc.After(cutOffTime) || nowInLoc.Equal(cutOffTime) {
				http.Error(w, fmt.Sprintf("Cannot set or change manual stocks after %s IST", tb.cfg.ManualWatchlistCutoff), http.StatusBadRequest)
				return
			}
		} else if targetDate.Before(time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), 0, 0, 0, 0, data.ISTLocation)) {
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
		nowIST := time.Now().In(data.ISTLocation)

		// 1. Timing check: must be 07:30 AM to 10:00 AM IST
		startLimit := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 7, 30, 0, 0, data.ISTLocation)
		endLimit := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 10, 0, 0, 0, data.ISTLocation)
		if nowIST.Before(startLimit) || nowIST.After(endLimit) {
			tb.logger.Warn("Request token exchange blocked: outside allowed window (07:30 AM - 10:00 AM IST)", map[string]interface{}{
				"current_time": nowIST.Format("15:04:05"),
			})
			http.Error(w, `{"error":"Request token exchange is only allowed between 07:30 AM and 10:00 AM IST"}`, http.StatusForbidden)
			return
		}

		// 2. Frequency check: at most 1 request every 10 seconds globally
		tokenExchangeMutex.Lock()
		if !lastTokenExchange.IsZero() && time.Since(lastTokenExchange) < 10*time.Second {
			remaining := 10*time.Second - time.Since(lastTokenExchange)
			tokenExchangeMutex.Unlock()
			tb.logger.Warn("Request token exchange blocked: rate limit active", map[string]interface{}{
				"cooldown_remaining": fmt.Sprintf("%.1fs", remaining.Seconds()),
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
		accessToken, err := tb.kiteClient.GenerateSession(requestToken, tb.cfg.APISecret)
		if err != nil {
			tb.logger.Error("Failed to generate session from request token", map[string]interface{}{"error": err.Error()})
			http.Error(w, fmt.Sprintf(`{"error":"Zerodha GenerateSession failed: %s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		rawToken = accessToken
		tb.logger.Info("Successfully exchanged request token for access token", nil)
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

// normalizeTime normalizes timezones between UTC and IST
func normalizeTime(t time.Time) time.Time {
	return data.NormalizeToIST(t)
}

// handleDailyWatchlistsHistory returns all records from daily_watchlists table
func (tb *TradingBot) handleDailyWatchlistsHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	dateParam := r.URL.Query().Get("date")
	var rows *sql.Rows
	var err error

	if dateParam != "" {
		rows, err = tb.db.QueryContext(tb.ctx, `
			SELECT date::TEXT, symbol, selectors
			FROM daily_watchlists
			WHERE date = $1
			ORDER BY symbol ASC
		`, dateParam)
	} else {
		rows, err = tb.db.QueryContext(tb.ctx, `
			SELECT date::TEXT, symbol, selectors
			FROM daily_watchlists
			ORDER BY date DESC, symbol ASC
		`)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type Item struct {
		Date      string   `json:"date"`
		Symbol    string   `json:"symbol"`
		Selectors []string `json:"selectors"`
	}

	var list []Item
	for rows.Next() {
		var date, symbol, selectorsStr string
		if err := rows.Scan(&date, &symbol, &selectorsStr); err != nil {
			continue
		}
		var selectors []string
		if selectorsStr != "" {
			parts := strings.Split(selectorsStr, ",")
			for _, part := range parts {
				subParts := strings.Split(part, ":")
				if len(subParts) >= 2 {
					selectorName := subParts[1]
					shortName := "FO"
					if selectorName == "SECTORAL" || selectorName == "SECTORAL_SELECTOR" {
						shortName = "SEC"
					} else if selectorName == "EQUITY_VOLUME_GAINERS" {
						shortName = "EVG"
					} else if selectorName == "SECURITIES_FO" {
						shortName = "FO"
					} else {
						shortName = selectorName
					}
					selectors = append(selectors, shortName)
				}
			}
		}
		list = append(list, Item{
			Date:      date,
			Symbol:    symbol,
			Selectors: selectors,
		})
	}

	json.NewEncoder(w).Encode(list)
}

// handleActivePositions serves current active positions in a flat list
func (tb *TradingBot) handleActivePositions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var openPositions interface{} = nil
	if tb.riskMgr != nil {
		openPositions = tb.riskMgr.GetOpenPositions()
	}

	// Convert map to slice for easier frontend consumption
	type PosDetail struct {
		OrderID         string    `json:"order_id"`
		Symbol          string    `json:"symbol"`
		Expiry          string    `json:"expiry"`
		Quantity        int       `json:"quantity"`
		EntryPrice      float64   `json:"entry_price"`
		Side            string    `json:"side"`
		SLPrice         float64   `json:"sl_price"`
		TargetPrice     float64   `json:"target_price"`
		LatestPrice     float64   `json:"latest_price"`
		Strategy        string    `json:"strategy"`
		CreatedAt       time.Time `json:"created_at"`
		BrokerSLOrderID string    `json:"broker_sl_order_id"`
	}

	list := make([]PosDetail, 0)
	if openPositions != nil {
		posMap := openPositions.(map[string]*risk.Position)
		for _, pos := range posMap {
			list = append(list, PosDetail{
				OrderID:         pos.OrderID,
				Symbol:          pos.Symbol,
				Expiry:          "INTRADAY",
				Quantity:        pos.Quantity,
				EntryPrice:      pos.EntryPrice,
				Side:            pos.Side,
				SLPrice:         pos.SLPrice,
				TargetPrice:     pos.Target1Price,
				LatestPrice:     pos.LatestPrice,
				Strategy:        pos.Strategy,
				CreatedAt:       pos.CreatedAt,
				BrokerSLOrderID: pos.BrokerSLOrderID,
			})
		}
	}

	if tb.optionsPosMgr != nil {
		if optPos := tb.optionsPosMgr.GetActivePosition(); optPos != nil {
			exp := optPos.Expiry
			if exp == "" {
				exp = risk.GetUpcomingOptionExpiry(optPos.CreatedAt)
			}
			list = append(list, PosDetail{
				OrderID:         optPos.OrderID,
				Symbol:          optPos.Symbol,
				Expiry:          exp,
				Quantity:        optPos.Quantity,
				EntryPrice:      optPos.EntryPremium,
				Side:            optPos.Side,
				SLPrice:         optPos.SLPrice,
				TargetPrice:     0,
				LatestPrice:     optPos.LatestPrice,
				Strategy:        "OPTIONS_SUPERTREND",
				CreatedAt:       optPos.CreatedAt,
				BrokerSLOrderID: "",
			})
		}
	}

	json.NewEncoder(w).Encode(list)
}

// handleOptionsState serves live Options Bot state & win rate performance metrics
func (tb *TradingBot) handleOptionsState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var status map[string]interface{}
	if tb.optionsPosMgr != nil {
		status = tb.optionsPosMgr.GetStatus()
	} else {
		status = map[string]interface{}{
			"multiplier":        1,
			"last_trend":        "NEUTRAL",
			"sl_stopped_trend":  "",
			"awaiting_reversal": false,
			"paper_balance":     1000000.0,
			"has_active_trade":  false,
		}
	}
	status["live_trading"] = tb.cfg.Options.LiveTrading
	status["trade_mode"] = tb.cfg.Options.TradeMode
	status["auto_square_off_time"] = tb.cfg.Options.AutoSquareOffTime
	status["last_new_trade_time"] = tb.cfg.Options.LastNewTradeTime
	status["st1_params"] = fmt.Sprintf("(%d, %g)", tb.cfg.Options.SuperTrendST1Period, tb.cfg.Options.SuperTrendST1Factor)
	status["st2_params"] = fmt.Sprintf("(%d, %g)", tb.cfg.Options.SuperTrendST2Period, tb.cfg.Options.SuperTrendST2Factor)
	status["st3_params"] = fmt.Sprintf("(%d, %g)", tb.cfg.Options.SuperTrendST3Period, tb.cfg.Options.SuperTrendST3Factor)

	// Query Win Rate & Options Trades Metrics from DB
	var totalTrades, winTrades int
	var totalPnL float64
	ctx := tb.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	_ = tb.db.WithContext(ctx).QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN pnl > 0 THEN 1 ELSE 0 END), 0), COALESCE(SUM(pnl), 0)
		FROM trades
		WHERE strategy = 'OPTIONS_SUPERTREND'
	`).Scan(&totalTrades, &winTrades, &totalPnL)

	winRate := 0.0
	if totalTrades > 0 {
		winRate = (float64(winTrades) / float64(totalTrades)) * 100.0
	}
	status["total_options_trades"] = totalTrades
	status["win_trades"] = winTrades
	status["win_rate_pct"] = winRate
	status["total_options_pnl"] = totalPnL

	json.NewEncoder(w).Encode(status)
}

// handleOptionsReset clears active position state in memory and database
func (tb *TradingBot) handleOptionsReset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if tb.optionsPosMgr != nil {
		tb.optionsPosMgr.ClearActivePosition(tb.ctx)
	}
	if tb.db != nil {
		_, _ = tb.db.WithContext(tb.ctx).ExecContext(tb.ctx, "TRUNCATE options_bot_state; DELETE FROM trades WHERE created_at >= '2026-08-07 00:00:00';")
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "message": "Options position state cleared"})
}

// handleOptionsExpectedMove calculates analytical expected move bounds and contract sensitivity
// fetching strictly from Zerodha Server API or PostgreSQL Database (no static fallbacks)
func (tb *TradingBot) handleOptionsExpectedMove(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		symbol = "NIFTY 50"
	}

	token, err := tb.securityMaster.GetInstrumentToken(symbol)
	if err != nil || token <= 0 {
		token = 256265 // NIFTY 50 token
	}

	// 1. Spot & VIX: fetch strictly from Zerodha API or PostgreSQL DB
	spot := 0.0
	vix := 0.0

	if tb.kiteClient != nil {
		if quotes, err := tb.kiteClient.GetQuote("NSE:INDIA VIX", "NSE:NIFTY 50"); err == nil {
			if q, ok := quotes["NSE:INDIA VIX"]; ok && q.LastPrice > 0 {
				vix = q.LastPrice
			}
			if q, ok := quotes["NSE:NIFTY 50"]; ok && q.LastPrice > 0 {
				spot = q.LastPrice
			}
		}
	}

	if spot <= 0 && tb.db != nil {
		candles, err := tb.db.GetLastNCandles("candles_5m", token, 1)
		if err == nil && len(candles) > 0 && candles[len(candles)-1].Close > 0 {
			spot = candles[len(candles)-1].Close
		}
	}

	if vix <= 0 && tb.db != nil {
		vixToken, err := tb.securityMaster.GetInstrumentToken("INDIA VIX")
		if err != nil || vixToken <= 0 {
			vixToken = 264969 // INDIA VIX token
		}
		vixCandles, err := tb.db.GetLastNCandles("candles_5m", vixToken, 1)
		if err == nil && len(vixCandles) > 0 && vixCandles[len(vixCandles)-1].Close > 0 {
			vix = vixCandles[len(vixCandles)-1].Close
		}
	}

	contractSym := r.URL.Query().Get("contract")
	contractLtp := 0.0
	isCall := true

	if tb.optionsPosMgr != nil {
		if optPos := tb.optionsPosMgr.GetActivePosition(); optPos != nil {
			if contractSym == "" {
				contractSym = optPos.Symbol
			}
			contractLtp = optPos.LatestPrice
		}
	}

	if strings.Contains(contractSym, "PE") {
		isCall = false
	}

	atmStrike := 0.0
	strike := 0.0
	if spot > 0 {
		atmStrike = math.Round(spot/50.0) * 50.0
		strike = atmStrike
	}

	// Extract strike from contract symbol if provided (e.g. NIFTY...24900CE)
	if contractSym != "" {
		if re := regexp.MustCompile(`(\d{5})(CE|PE)$`); re.MatchString(contractSym) {
			matches := re.FindStringSubmatch(contractSym)
			if len(matches) == 3 {
				if sVal, err := strconv.ParseFloat(matches[1], 64); err == nil && sVal > 0 {
					strike = sVal
				}
			}
		}
	}

	// Fetch live quote or DB price for target option contract
	if contractSym != "" && tb.kiteClient != nil {
		if quotes, err := tb.kiteClient.GetQuote("NFO:" + contractSym); err == nil {
			if q, ok := quotes["NFO:"+contractSym]; ok && q.LastPrice > 0 {
				contractLtp = q.LastPrice
			}
		}
	}

	if contractLtp <= 0 && contractSym != "" && tb.db != nil && tb.securityMaster != nil {
		if cToken, err := tb.securityMaster.GetInstrumentToken(contractSym); err == nil && cToken > 0 {
			if c, err := tb.db.GetLastNCandles("candles_5m", cToken, 1); err == nil && len(c) > 0 && c[0].Close > 0 {
				contractLtp = c[0].Close
			}
		}
	}

	// 2. Fetch ATM Straddle Price strictly from Zerodha API or PostgreSQL DB
	straddlePrice := 0.0
	if spot > 0 {
		straddlePrice = tb.fetchLiveOrDBATMStraddleQuote(spot, contractSym)
	}

	res := data.CalculateExpectedMove(spot, vix, straddlePrice, contractSym, contractLtp, strike, isCall, time.Now())
	json.NewEncoder(w).Encode(res)
}

// fetchLiveOrDBATMStraddleQuote queries Zerodha API or PostgreSQL database candles for ATM CE and PE contracts
func (tb *TradingBot) fetchLiveOrDBATMStraddleQuote(spot float64, referenceContract string) float64 {
	if spot <= 0 {
		return 0.0
	}

	atmStrike := math.Round(spot/50.0) * 50.0
	var atmCE, atmPE string

	if referenceContract != "" {
		re := regexp.MustCompile(`^(NIFTY\w*?)(\d{5})(CE|PE)$`)
		if matches := re.FindStringSubmatch(referenceContract); len(matches) >= 3 {
			prefix := matches[1]
			atmCE = fmt.Sprintf("%s%dCE", prefix, int(atmStrike))
			atmPE = fmt.Sprintf("%s%dPE", prefix, int(atmStrike))
		}
	}

	if (atmCE == "" || atmPE == "") && tb.kiteClient != nil {
		instruments, err := tb.kiteClient.GetInstrumentsByExchange("NFO")
		if err == nil {
			var earliestExpiry time.Time
			for _, inst := range instruments {
				if inst.Name == "NIFTY" && inst.Segment == "NFO-OPT" && inst.Strike == atmStrike {
					if earliestExpiry.IsZero() || inst.Expiry.Before(earliestExpiry) {
						if !inst.Expiry.Before(time.Now().AddDate(0, 0, -1)) {
							earliestExpiry = inst.Expiry
						}
					}
				}
			}

			if !earliestExpiry.IsZero() {
				for _, inst := range instruments {
					if inst.Name == "NIFTY" && inst.Segment == "NFO-OPT" && inst.Strike == atmStrike && inst.Expiry.Equal(earliestExpiry) {
						if inst.InstrumentType == "CE" {
							atmCE = inst.TradingSymbol
						} else if inst.InstrumentType == "PE" {
							atmPE = inst.TradingSymbol
						}
					}
				}
			}
		}
	}

	if atmCE == "" || atmPE == "" {
		return 0.0
	}

	ceLtp := 0.0
	peLtp := 0.0

	// 1. Query Zerodha Server API live quotes
	if tb.kiteClient != nil {
		if quotes, err := tb.kiteClient.GetQuote("NFO:"+atmCE, "NFO:"+atmPE); err == nil {
			if q, ok := quotes["NFO:"+atmCE]; ok && q.LastPrice > 0 {
				ceLtp = q.LastPrice
			}
			if q, ok := quotes["NFO:"+atmPE]; ok && q.LastPrice > 0 {
				peLtp = q.LastPrice
			}
		}
	}

	// 2. Query PostgreSQL Database candles if live quotes are unquoted
	if ceLtp <= 0 && tb.securityMaster != nil && tb.db != nil {
		if ceToken, err := tb.securityMaster.GetInstrumentToken(atmCE); err == nil && ceToken > 0 {
			if c, err := tb.db.GetLastNCandles("candles_5m", ceToken, 1); err == nil && len(c) > 0 && c[0].Close > 0 {
				ceLtp = c[0].Close
			}
		}
	}

	if peLtp <= 0 && tb.securityMaster != nil && tb.db != nil {
		if peToken, err := tb.securityMaster.GetInstrumentToken(atmPE); err == nil && peToken > 0 {
			if c, err := tb.db.GetLastNCandles("candles_5m", peToken, 1); err == nil && len(c) > 0 && c[0].Close > 0 {
				peLtp = c[0].Close
			}
		}
	}

	if ceLtp > 0 && peLtp > 0 {
		return ceLtp + peLtp
	} else if ceLtp > 0 {
		return ceLtp * 2.0
	} else if peLtp > 0 {
		return peLtp * 2.0
	}

	return 0.0
}

// handleOptionsSuperTrends serves 5m historical candles with ST1, ST2, ST3 line values and signal markers
func (tb *TradingBot) handleOptionsSuperTrends(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		symbol = "NIFTY 50"
	}

	token, err := tb.securityMaster.GetInstrumentToken(symbol)
	if err != nil || token <= 0 {
		token = 256265 // NIFTY 50 token
	}

	candles, err := tb.db.GetLastNCandles("candles_5m", token, 500)
	needSync := err != nil || len(candles) == 0
	if !needSync && len(candles) > 0 {
		latestTime := candles[len(candles)-1].Time.In(data.ISTLocation)
		now := time.Now().In(data.ISTLocation)
		if now.Hour() >= 9 && now.Hour() <= 15 && now.Sub(latestTime) > 10*time.Minute {
			needSync = true
		}
	}

	if needSync {
		tb.ensureNifty50OptionsHistoricalData()
		candles, _ = tb.db.GetLastNCandles("candles_5m", token, 500)
	}

	// Deduplicate candles by 5-minute floored Unix timestamp and sort chronologically in IST
	seenTimes := make(map[int64]bool)
	uniqueCandles := make([]data.Candle, 0, len(candles))
	for _, c := range candles {
		tIST := data.NormalizeToIST(c.Time)
		tUnix := (tIST.Unix() / 300) * 300
		if !seenTimes[tUnix] {
			seenTimes[tUnix] = true
			c.Time = tIST
			uniqueCandles = append(uniqueCandles, c)
		}
	}
	sort.Slice(uniqueCandles, func(i, j int) bool {
		return uniqueCandles[i].Time.Before(uniqueCandles[j].Time)
	})
	candles = uniqueCandles

	dateStr := r.URL.Query().Get("date")

	// Auto fallback if dateStr specified but has 0 candles in DB
	if dateStr != "" && len(candles) > 0 {
		hasDate := false
		for _, c := range candles {
			if data.NormalizeToIST(c.Time).Format("2006-01-02") == dateStr {
				hasDate = true
				break
			}
		}
		if !hasDate {
			dateStr = data.NormalizeToIST(candles[len(candles)-1].Time).Format("2006-01-02")
		}
	}

	stEngine := strategy.NewSuperTrendOptionsEngine(
		tb.cfg.Options.SuperTrendST1Period, tb.cfg.Options.SuperTrendST2Period, tb.cfg.Options.SuperTrendST3Period,
		tb.cfg.Options.SuperTrendST1Factor, tb.cfg.Options.SuperTrendST2Factor, tb.cfg.Options.SuperTrendST3Factor,
	)

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

	// Query executed options trades to mark actual trade entries and exits on chart candles
	optTrades, _ := tb.db.GetAllTradesHistory(r.Context())
	entryTradeMap := make(map[string]string) // "YYYY-MM-DD HH:MM" -> ENTRY signal
	exitTradeMap := make(map[string]string)  // "YYYY-MM-DD HH:MM" -> EXIT signal

	formatKey := func(t time.Time) string {
		tIST := data.NormalizeToIST(t)
		flooredMin := (tIST.Minute() / 5) * 5
		return fmt.Sprintf("%04d-%02d-%02d %02d:%02d", tIST.Year(), tIST.Month(), tIST.Day(), tIST.Hour(), flooredMin)
	}

	for _, tr := range optTrades {
		if tr.Strategy == "OPTIONS_SUPERTREND" {
			entryTime := tr.EntryTime
			if entryTime.IsZero() {
				entryTime = tr.CreatedAt.Add(-time.Duration(tr.TimeHeldMinutes) * time.Minute)
			}
			exitTime := tr.ExitTime
			if exitTime.IsZero() {
				exitTime = tr.CreatedAt
			}

			// Market Hour Alignment Guard for Chart Markers:
			// If entryTime occurred before market open (09:15 AM) on the trade day, align marker to 09:15 AM candle
			entryTimeIST := data.NormalizeToIST(entryTime)
			if entryTimeIST.Hour() < 9 || (entryTimeIST.Hour() == 9 && entryTimeIST.Minute() < 15) {
				entryTimeIST = time.Date(entryTimeIST.Year(), entryTimeIST.Month(), entryTimeIST.Day(), 9, 15, 0, 0, data.ISTLocation)
			}

			entryKey := formatKey(entryTimeIST)
			exitKey := formatKey(exitTime)

			if strings.Contains(tr.Symbol, "PE") {
				entryTradeMap[entryKey] = "ENTRY_SELL_PE"
			} else if strings.Contains(tr.Symbol, "CE") {
				entryTradeMap[entryKey] = "ENTRY_SELL_CE"
			}

			exitTimeIST := data.NormalizeToIST(exitTime)
			if exitTimeIST.Hour() == 15 && exitTimeIST.Minute() >= 14 {
				exitTradeMap[exitKey] = "EXIT_EOD"
			} else if tr.EntryPrice > 0 && tr.ExitPrice >= tr.EntryPrice*1.45 {
				exitTradeMap[exitKey] = "EXIT_SL"
			} else if tr.PnL >= 0 {
				exitTradeMap[exitKey] = "EXIT_PROFIT"
			} else {
				exitTradeMap[exitKey] = "EXIT_REVERSAL"
			}
		}
	}

	// Also attach active live options trade entry marker if present
	if tb.optionsPosMgr != nil {
		if optPos := tb.optionsPosMgr.GetActivePosition(); optPos != nil {
			entryKey := formatKey(optPos.CreatedAt)
			if strings.Contains(optPos.Symbol, "PE") {
				entryTradeMap[entryKey] = "ENTRY_SELL_PE"
			} else if strings.Contains(optPos.Symbol, "CE") {
				entryTradeMap[entryKey] = "ENTRY_SELL_CE"
			}
		}
	}

	var allPoints []IndicatorPoint
	for i := 1; i <= len(candles); i++ {
		sub := candles[:i]
		last := sub[len(sub)-1]

		res := stEngine.CalculateTripleSuperTrend(sub)
		cKey := formatKey(last.Time)

		// Build signal markers strictly from executed trade records and active live positions
		var sigParts []string
		if entrySig, exists := entryTradeMap[cKey]; exists {
			sigParts = append(sigParts, entrySig)
		}
		if exitSig, exists := exitTradeMap[cKey]; exists {
			sigParts = append(sigParts, exitSig)
		}

		sig := ""
		if len(sigParts) > 0 {
			sig = strings.Join(sigParts, ",")
		}

		allPoints = append(allPoints, IndicatorPoint{
			Time:   last.Time.Unix(),
			Open:   last.Open,
			High:   last.High,
			Low:    last.Low,
			Close:  last.Close,
			ST1:    res.ST1.Value,
			ST2:    res.ST2.Value,
			ST3:    res.ST3.Value,
			Trend:  res.Trend,
			Signal: sig,
		})
	}

	// Filter points to return Target Date + Previous Trading Day for multi-day continuity
	dateCandleCount := make(map[string]int)
	for _, pt := range allPoints {
		dStr := time.Unix(pt.Time, 0).In(data.ISTLocation).Format("2006-01-02")
		dateCandleCount[dStr]++
	}

	todayStr := time.Now().In(data.ISTLocation).Format("2006-01-02")
	var uniqueDates []string
	seenDateMap := make(map[string]bool)
	for _, pt := range allPoints {
		dStr := time.Unix(pt.Time, 0).In(data.ISTLocation).Format("2006-01-02")
		if !seenDateMap[dStr] {
			seenDateMap[dStr] = true
			// Include valid trading sessions (at least 5 candles) or today's live session
			if dateCandleCount[dStr] >= 5 || dStr == todayStr {
				uniqueDates = append(uniqueDates, dStr)
			}
		}
	}

	allowedDates := make(map[string]bool)
	targetDate := dateStr
	if targetDate == "" && len(uniqueDates) > 0 {
		targetDate = uniqueDates[len(uniqueDates)-1]
	}

	if targetDate != "" {
		allowedDates[targetDate] = true
		for idx, dStr := range uniqueDates {
			if dStr == targetDate {
				if idx > 0 {
					allowedDates[uniqueDates[idx-1]] = true // Include previous trading day for multi-day continuity
				}
				break
			}
		}
	}

	var list []IndicatorPoint
	for _, pt := range allPoints {
		candDate := time.Unix(pt.Time, 0).In(data.ISTLocation).Format("2006-01-02")
		if allowedDates[candDate] {
			list = append(list, pt)
		}
	}

	json.NewEncoder(w).Encode(list)
}

// handleOptionsMode GETs options bot mode configuration strictly loaded from .env environment settings
func (tb *TradingBot) handleOptionsMode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	resp := map[string]interface{}{
		"success":      true,
		"live_trading": tb.cfg.Options.LiveTrading,
		"trade_mode":   tb.cfg.Options.TradeMode,
		"read_only":    true,
	}
	json.NewEncoder(w).Encode(resp)
}

// handleScannerResults returns stock scanner results from DB by date (or latest if unspecified)
func (tb *TradingBot) handleScannerResults(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	dateStr := r.URL.Query().Get("date")
	results, err := tb.db.GetScannerResultsByDate(r.Context(), dateStr)
	if err != nil {
		tb.logger.Error("Failed to fetch scanner results", map[string]interface{}{"error": err.Error()})
		http.Error(w, "Failed to fetch scanner results", http.StatusInternalServerError)
		return
	}
	if results == nil {
		results = []data.DBScanResult{}
	}
	json.NewEncoder(w).Encode(results)
}

// handleScannerDates returns a list of distinct historical scan dates stored in PostgreSQL
func (tb *TradingBot) handleScannerDates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	dates, err := tb.db.GetScannerDates(r.Context())
	if err != nil {
		tb.logger.Error("Failed to fetch scanner dates", map[string]interface{}{"error": err.Error()})
		http.Error(w, "Failed to fetch scanner dates", http.StatusInternalServerError)
		return
	}
	if dates == nil {
		dates = []string{}
	}
	json.NewEncoder(w).Encode(dates)
}

// handleScannerRun triggers an immediate manual scan run in background
func (tb *TradingBot) handleScannerRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	if tb.scanner == nil {
		http.Error(w, "Scanner engine not initialized", http.StatusInternalServerError)
		return
	}

	if atomic.LoadInt32(&tb.isScannerRunning) == 1 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"status":  "RUNNING",
			"message": "Live scan is currently in progress in background across all NSE Cash & F&O stocks...",
		})
		return
	}

	atomic.StoreInt32(&tb.isScannerRunning, 1)

	// Launch scan asynchronously in background goroutine
	go func() {
		defer atomic.StoreInt32(&tb.isScannerRunning, 0)
		tb.logger.Info("Starting background quant stock scan across all NSE Cash & F&O stocks...", nil)

		results, err := tb.scanner.RunScan(context.Background())
		if err != nil {
			tb.logger.Error("Background quant stock scan failed", map[string]interface{}{"error": err.Error()})
			return
		}

		var dbResults []data.DBScanResult
		for _, res := range results {
			dbResults = append(dbResults, data.DBScanResult{
				Symbol:            res.Symbol,
				Segment:           res.Segment,
				BreakoutType:      string(res.BreakoutType),
				Direction:         res.Direction,
				MomentumDays:      res.MomentumDays,
				PctChange1D:       res.PctChange1D,
				PctChange3D:       res.PctChange3D,
				RangePctChange:    res.RangePctChange,
				YearlyHigh:        res.YearlyHigh,
				YearlyLow:         res.YearlyLow,
				AllTimeHigh:       res.AllTimeHigh,
				AllTimeLow:        res.AllTimeLow,
				Volume1D:          res.Volume1D,
				VolumeADV:         res.VolumeADV,
				VolumeMultiplier:  res.VolumeMultiplier,
				ConfidenceScore:   res.ConfidenceScore,
				QuantDirection:    string(res.QuantDirection),
				RecommendedAction: res.RecommendedAct,
				NewsSummary:       res.NewsSummary,
				NewsSentiment:     res.NewsSentiment,
				CreatedAt:         res.CreatedAt,
			})
		}

		if saveErr := tb.db.SaveScannerResults(context.Background(), dbResults); saveErr != nil {
			tb.logger.Error("Failed to save background scan results to database", map[string]interface{}{"error": saveErr.Error()})
		} else {
			tb.logger.Info("Background quant stock scan saved to database successfully", map[string]interface{}{"total": len(dbResults)})
		}
	}()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"status":  "STARTED",
		"message": "Live scan started in background across all NSE Cash & F&O stocks. Results will update automatically.",
	})
}
