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
	"zerodha-trading/selection"
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
	todayStr := data.GetEffectiveTradingDate(nowIST)

	// Get select time from config
	selectHour, selectMin, errTime := parseTimeHM(tb.cfg.StockSelectTime)
	if errTime != nil {
		selectHour, selectMin = 9, 25
	}
	selectTime := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), selectHour, selectMin, 0, 0, data.ISTLocation)

	wlCopy := make(map[string]int64)
	symbolStrats := make(map[string][]string)

	// Try fetching the saved watchlist from DB for todayStr (effective trading date) first
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
						} else if selectorName == "MA" || selectorName == "MANUAL" {
							shortName = "MA"
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
	} else if nowIST.Before(selectTime) {
		// Before 09:25 AM IST (on a new day before stock selection runs), show all F&O stocks as fallback
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

	// Also check manual watchlist and add to active watchlist map with tag MA
	manualSymbols, errManual := tb.db.GetDailyManualWatchlist(tb.ctx, time.Now())
	if errManual == nil && len(manualSymbols) > 0 {
		for _, sym := range manualSymbols {
			sym = strings.TrimSpace(sym)
			if sym != "" && !tb.IsStockExcluded(sym) {
				alreadyHasMA := false
				for _, sName := range symbolStrats[sym] {
					if sName == "MA" || sName == "M" {
						alreadyHasMA = true
						break
					}
				}
				if !alreadyHasMA {
					symbolStrats[sym] = append(symbolStrats[sym], "MA")
				}

				// Ensure manual stock is in active watchlist and wlCopy if token can be resolved
				token := tb.resolveSymbolToken(tb.ctx, sym)
				if token > 0 {
					wlCopy[sym] = token
					tb.watchlistMutex.Lock()
					tb.watchlist[sym] = token
					tb.watchlistMutex.Unlock()
					if tb.ticker != nil {
						tb.ticker.Subscribe([]int64{token})
					}
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

	// Filter out manually excluded stocks from watchlist response
	tb.excludedStocksMutex.RLock()
	for sym := range wlCopy {
		if tb.excludedStocks[sym] {
			delete(wlCopy, sym)
			delete(symbolStrats, sym)
		}
	}
	tb.excludedStocksMutex.RUnlock()

	response := map[string]interface{}{
		"watchlist":               wlCopy,
		"watchlist_strategies":    symbolStrats,
		"watchlist_selectors":     tb.watchlistSelectorMap,
		"stock_selection_configs": tb.stockSelectionConfigs,
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
	symbol := strings.TrimSpace(strings.ToUpper(r.URL.Query().Get("symbol")))
	if symbol == "" {
		http.Error(w, `{"error":"symbol parameter required"}`, http.StatusBadRequest)
		return
	}
	symbol = normalizeSymbolAlias(symbol)

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
		effDateStr := data.GetEffectiveTradingDate(time.Now())
		parsedDate, _ := time.ParseInLocation("2006-01-02", effDateStr, data.ISTLocation)
		dayStart = time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 0, 0, 0, 0, data.ISTLocation).UTC()
	}

	type APICandle struct {
		Time    int64   `json:"time"`
		Open    float64 `json:"open"`
		High    float64 `json:"high"`
		Low     float64 `json:"low"`
		Close   float64 `json:"close"`
		Volume  int64   `json:"volume"`
		VWAP    float64 `json:"vwap"`
		Color   string  `json:"color"`
		EMAFast float64 `json:"ema_fast"`
		EMASlow float64 `json:"ema_slow"`
		PDH     float64 `json:"pdh"`
		PDL     float64 `json:"pdl"`
	}

	// 1. Time range & market hours check
	locTime := dayStart.In(data.ISTLocation)
	now := time.Now().In(data.ISTLocation)
	isToday := locTime.Year() == now.Year() && locTime.Month() == now.Month() && locTime.Day() == now.Day()
	isMarketHours := (now.Hour() > 9 || (now.Hour() == 9 && now.Minute() >= 15)) && (now.Hour() < 15 || (now.Hour() == 15 && now.Minute() <= 35))

	// 2. Fetch candles from database for target date
	candles, err := tb.db.GetCandlesForDate(tb.ctx, token, dayStart)
	if (err != nil || len(candles) == 0) && tb.kiteClient != nil {
		// Fall back to Zerodha API only if database has 0 candles for this date
		startTime := time.Date(locTime.Year(), locTime.Month(), locTime.Day(), 9, 15, 0, 0, data.ISTLocation)
		endTime := time.Date(locTime.Year(), locTime.Month(), locTime.Day(), 15, 30, 0, 0, data.ISTLocation)

		if !startTime.After(now) {
			if endTime.After(now) {
				endTime = now
			}
			apiCandles, apiErr := tb.kiteClient.GetHistoricalData(int(token), "5minute", startTime, endTime, false, false)
			if apiErr == nil && len(apiCandles) > 0 {
				_ = tb.db.SaveHistoricalCandles(tb.ctx, token, apiCandles, "candles_5m")
				converted := make([]data.CandleRecord, 0, len(apiCandles))
				for _, ac := range apiCandles {
					converted = append(converted, data.CandleRecord{
						Time:   data.NormalizeToIST(ac.Date),
						Open:   ac.Open,
						High:   ac.High,
						Low:    ac.Low,
						Close:  ac.Close,
						Volume: int64(ac.Volume),
					})
				}
				candles = converted
			}
		}
	} else if isToday && isMarketHours && len(candles) > 0 && tb.kiteClient != nil {
		// If live market hours and candles might need catchup, run catchup in background without blocking UI
		lastCandleTime := data.NormalizeToIST(candles[len(candles)-1].Time)
		if now.Sub(lastCandleTime) > 6*time.Minute {
			go tb.catchUpHistoricalCandles(symbol, token)
		}
	}

	// 3. Fallback: If target date has 0 candles in DB & Zerodha API, fetch the most recent available candles from DB
	if len(candles) == 0 {
		recentCandles, qErr := tb.db.GetLastNCandles("candles_5m", token, 100)
		if qErr == nil && len(recentCandles) > 0 {
			converted := make([]data.CandleRecord, 0, len(recentCandles))
			for _, rc := range recentCandles {
				converted = append(converted, data.CandleRecord{
					Time:   data.NormalizeToIST(rc.Time),
					Open:   rc.Open,
					High:   rc.High,
					Low:    rc.Low,
					Close:  rc.Close,
					Volume: rc.Volume,
				})
			}
			candles = converted
		}
	}

	// 4. Compute Fast & Slow EMAs and resolve PDH/PDL over historical context + target day candles
	priorCandles, _ := tb.db.GetHistoricalCandlesBeforeDate(tb.ctx, token, dayStart, 100)
	if len(priorCandles) == 0 && tb.kiteClient != nil {
		histStart := locTime.AddDate(0, 0, -4)
		histEnd := locTime.Add(-1 * time.Minute)
		if apiPrior, apiErr := tb.kiteClient.GetHistoricalData(int(token), "5minute", histStart, histEnd, false, false); apiErr == nil && len(apiPrior) > 0 {
			_ = tb.db.SaveHistoricalCandles(tb.ctx, token, apiPrior, "candles_5m")
			if reQueried, qErr := tb.db.GetHistoricalCandlesBeforeDate(tb.ctx, token, dayStart, 100); qErr == nil && len(reQueried) > 0 {
				priorCandles = reQueried
			}
		}
	}

	// Compute PDH & PDL directly from the most recent previous day in priorCandles
	var pdh, pdl float64
	if len(priorCandles) > 0 {
		lastDateStr := priorCandles[len(priorCandles)-1].Time.Format("2006-01-02")
		maxH := 0.0
		minL := 9999999.0
		count := 0
		for _, pc := range priorCandles {
			if pc.Time.Format("2006-01-02") == lastDateStr {
				if pc.High > maxH {
					maxH = pc.High
				}
				if pc.Low < minL && pc.Low > 0 {
					minL = pc.Low
				}
				count++
			}
		}
		if count > 0 && maxH > 0 && minL < 9999999 {
			pdh = maxH
			pdl = minL
		}
	}

	historyCloses := make([]float64, len(priorCandles))
	for i, pc := range priorCandles {
		historyCloses[i] = pc.Close
	}

	targetCloses := make([]float64, len(candles))
	for i, c := range candles {
		targetCloses[i] = c.Close
	}
	allCloses := append(historyCloses, targetCloses...)

	ind := strategy.NewIndicators(tb.logger.Logger, 20, 14, 10)
	allFastEMAs := ind.CalculateEMA(allCloses, tb.cfg.EMAFastPeriod)
	allSlowEMAs := ind.CalculateEMA(allCloses, tb.cfg.EMASlowPeriod)

	offset := len(historyCloses)
	list := make([]APICandle, 0, len(candles))
	for i, c := range candles {
		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}

		vwap := (c.Open + c.High + c.Low + c.Close) / 4.0

		var fastVal, slowVal float64
		idx := offset + i
		if idx < len(allFastEMAs) {
			fastVal = allFastEMAs[idx]
		}
		if idx < len(allSlowEMAs) {
			slowVal = allSlowEMAs[idx]
		}

		list = append(list, APICandle{
			Time:    data.NormalizeToIST(c.Time).Unix(),
			Open:    c.Open,
			High:    c.High,
			Low:     c.Low,
			Close:   c.Close,
			Volume:  c.Volume,
			VWAP:    vwap,
			Color:   color,
			EMAFast: fastVal,
			EMASlow: slowVal,
			PDH:     pdh,
			PDL:     pdl,
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
		Status          string  `json:"status"`
		ExpiryDate      string  `json:"expiry_date"`
	}

	normalizeUnix := func(t time.Time) int64 {
		if t.IsZero() {
			return 0
		}
		return data.NormalizeToIST(t).Unix()
	}

	list := make([]TradeRecord, 0)
	for _, t := range history {
		createdUnix := normalizeUnix(t.CreatedAt)

		var exitUnix int64 = 0
		if !t.ExitTime.IsZero() && t.Status != "LIVE" {
			exitUnix = normalizeUnix(t.ExitTime)
		}

		entryTime := t.EntryTime
		if entryTime.IsZero() {
			entryTime = t.CreatedAt
		}
		entryUnix := normalizeUnix(entryTime)

		list = append(list, TradeRecord{
			ID:              t.ID,
			Symbol:          t.Symbol,
			EntryPrice:      t.EntryPrice,
			ExitPrice:       t.ExitPrice,
			Quantity:        t.Quantity,
			PnL:             t.PnL,
			Side:            t.Side,
			TimeHeldMinutes: t.TimeHeldMinutes,
			EntryTime:       entryUnix,
			ExitTime:        exitUnix,
			CreatedAt:       createdUnix,
			Strategy:        t.Strategy,
			Status:          t.Status,
			ExpiryDate:      t.ExpiryDate,
		})
	}

	json.NewEncoder(w).Encode(list)
}

// handleDailyManualWatchlist handles getting and setting manual stock selections
func (tb *TradingBot) handleDailyManualWatchlist(w http.ResponseWriter, r *http.Request) {
	nowInLoc := time.Now().In(data.ISTLocation)

	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		targetDate := nowInLoc
		if dParam := r.URL.Query().Get("date"); dParam != "" {
			if pDate, pErr := time.ParseInLocation("2006-01-02", dParam, data.ISTLocation); pErr == nil {
				targetDate = pDate
			}
		}
		symbols, err := tb.db.GetDailyManualWatchlist(tb.ctx, targetDate)
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
			"date":    targetDate.Format("2006-01-02"),
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

		effTodayStr := data.GetEffectiveTradingDate(nowInLoc)
		effTodayDate, _ := time.ParseInLocation("2006-01-02", effTodayStr, data.ISTLocation)
		targetDayStart := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, data.ISTLocation)
		effDayStart := time.Date(effTodayDate.Year(), effTodayDate.Month(), effTodayDate.Day(), 0, 0, 0, 0, data.ISTLocation)

		if targetDayStart.Before(effDayStart) {
			http.Error(w, "Cannot set manual stocks for past dates", http.StatusBadRequest)
			return
		}

		targetStr := targetDate.Format("2006-01-02")

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

		// Validate each symbol against SecurityMaster / DB / NSE token resolver
		var validItems []string
		var validSymbolsCleaned string
		var invalidSymbols []string
		var validNames []string
		var wItems []data.DailyWatchlistItem

		if cleanedSymbols != "" && cleanedSymbols != "CALCULATE" {
			rawParts := strings.Split(cleanedSymbols, ",")
			for _, rawItem := range rawParts {
				parts := strings.Split(rawItem, ":")
				sym := normalizeSymbolAlias(strings.TrimSpace(strings.ToUpper(parts[0])))
				assignedSel := "PDH_PDL"
				if len(parts) > 1 && parts[1] != "" {
					assignedSel = selection.NormalizeSelectorName(parts[1])
				}
				if sym == "" {
					continue
				}

				token := tb.resolveSymbolToken(tb.ctx, sym)
				if token <= 0 {
					invalidSymbols = append(invalidSymbols, sym)
					continue
				}

				validItemStr := fmt.Sprintf("%s:%s", sym, assignedSel)
				validItems = append(validItems, validItemStr)
				validNames = append(validNames, sym)
				wItems = append(wItems, data.DailyWatchlistItem{
					Date:      targetStr,
					Symbol:    sym,
					Token:     token,
					Selectors: "MANUAL:" + assignedSel,
				})
			}
			validSymbolsCleaned = strings.Join(validItems, ",")
		}

		// If user entered symbols but all were invalid, reject with error
		if cleanedSymbols != "" && cleanedSymbols != "CALCULATE" && len(validItems) == 0 && len(invalidSymbols) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "error",
				"success": false,
				"error":   fmt.Sprintf("Invalid stock symbol(s): %s. Not found on NSE master.", strings.Join(invalidSymbols, ", ")),
				"message": fmt.Sprintf("Invalid stock symbol(s): %s. Not found on NSE master.", strings.Join(invalidSymbols, ", ")),
			})
			return
		}

		if validSymbolsCleaned == "" || cleanedSymbols == "CALCULATE" {
			err = tb.db.DeleteDailyManualWatchlist(tb.ctx, targetDate)
		} else {
			err = tb.db.SaveDailyManualWatchlist(tb.ctx, targetDate, validSymbolsCleaned)
		}

		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to save daily manual watchlist: %v", err), http.StatusInternalServerError)
			return
		}

		// Persist validated items into daily_watchlists table
		if len(wItems) > 0 {
			if saveErr := tb.db.SaveDailyWatchlist(tb.ctx, wItems); saveErr != nil {
				tb.logger.Error("Failed to persist manual watchlist to daily_watchlists table", map[string]interface{}{"error": saveErr.Error()})
			}
		}

		// Register & subscribe validated manual watchlist symbols in-memory
		if len(wItems) > 0 {
			for _, wItem := range wItems {
				sym := wItem.Symbol
				parts := strings.Split(wItem.Selectors, ":")
				assignedSel := "PDH_PDL"
				if len(parts) > 1 && parts[1] != "" {
					assignedSel = parts[1]
				}

				tb.watchlistSelectorMapMutex.Lock()
				tb.watchlistSelectorMap[sym] = assignedSel
				tb.watchlistSelectorMapMutex.Unlock()

				tb.ClearStockExclusion(sym)
				token := wItem.Token
				if token > 0 {
					tb.watchlistMutex.Lock()
					tb.watchlist[sym] = token
					for _, strat := range tb.activeStrategies {
						if tb.strategyWatchlists[strat.Name()] == nil {
							tb.strategyWatchlists[strat.Name()] = make(map[string]int64)
						}
						tb.strategyWatchlists[strat.Name()][sym] = token
						high, low, _ := tb.resolvePreviousDayHighLow(token, sym, data.ISTLocation)
						_, shiftPct := tb.resolveSymbolSelectorAndShift(sym)
						shiftedHigh := selection.CalculateLevelShiftedPrice(high, shiftPct, 0.05)
						shiftedLow := selection.CalculateLevelShiftedPrice(low, shiftPct, 0.05)
						if vbEngine, isVB := strat.(*strategy.VandeBharatEngine); isVB {
							vbEngine.SetPreviousDayHighLow(sym, shiftedHigh, shiftedLow)
						} else if lvEngine, isLV := strat.(*strategy.LowVolumeEngine); isLV {
							lvEngine.SetPreviousDayHighLow(sym, shiftedHigh, shiftedLow)
						}
					}
					tb.watchlistMutex.Unlock()
					if tb.ticker != nil {
						tb.ticker.Subscribe([]int64{token})
					}
					go tb.catchUpHistoricalCandles(sym, token)
				}
			}
		}

		responseMsg := fmt.Sprintf("Daily manual watchlist for %s set to %s", targetStr, validSymbolsCleaned)
		if len(invalidSymbols) > 0 {
			responseMsg = fmt.Sprintf("Saved valid stocks (%s). Ignored invalid symbol(s): %s", strings.Join(validNames, ", "), strings.Join(invalidSymbols, ", "))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"success": true,
			"message": responseMsg,
			"symbols": validSymbolsCleaned,
		})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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

// handleConfigAll returns all database-driven configurations, server time, and restart permissions
func (tb *TradingBot) handleConfigAll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ctx := r.Context()

	// 1. Fetch options configs
	optConfigs, err := tb.db.GetAllOptionsIndexConfigs(ctx)
	if err != nil {
		tb.logger.Error("Failed to fetch options index configs", map[string]interface{}{"error": err.Error()})
		optConfigs = []data.OptionsIndexConfig{}
	}

	// 2. Fetch system configs
	sysConfigs, err := tb.db.GetAllSystemConfigs(ctx)
	if err != nil {
		tb.logger.Error("Failed to fetch system configs", map[string]interface{}{"error": err.Error()})
		sysConfigs = make(map[string]map[string]string)
	}

	// 3. Time Gate calculation
	nowIST := time.Now().In(data.ISTLocation)
	currentSecOfDay := nowIST.Hour()*3600 + nowIST.Minute()*60 + nowIST.Second()
	allowedBefore := tb.cfg.RestartAllowedBefore
	if allowedBefore == "" {
		allowedBefore = "09:15:00"
	}
	allowedAfter := tb.cfg.RestartAllowedAfter
	if allowedAfter == "" {
		allowedAfter = "15:45:00"
	}
	beforeSec := data.ParseTimeToSeconds(allowedBefore)
	afterSec := data.ParseTimeToSeconds(allowedAfter)

	restartAllowed := currentSecOfDay < beforeSec || currentSecOfDay >= afterSec

	response := map[string]interface{}{
		"options_configs":  optConfigs,
		"system_configs":   sysConfigs,
		"server_time_ist":  nowIST.Format("2006-01-02 15:04:05 IST"),
		"current_hm":       nowIST.Format("15:04:05"),
		"restart_allowed":  restartAllowed,
		"restart_window": map[string]string{
			"allowed_before": allowedBefore,
			"allowed_after":  allowedAfter,
		},
		"supported_indices": []map[string]string{
			{"name": "NIFTY 50", "clean": "NIFTY", "exchange": "NFO"},
			{"name": "NIFTY BANK", "clean": "BANKNIFTY", "exchange": "NFO"},
			{"name": "BSE SENSEX", "clean": "SENSEX", "exchange": "BFO"},
			{"name": "FINNIFTY", "clean": "FINNIFTY", "exchange": "NFO"},
			{"name": "MIDCPNIFTY", "clean": "MIDCPNIFTY", "exchange": "NFO"},
		},
	}

	json.NewEncoder(w).Encode(response)
}

// handleConfigSave receives updated configuration parameters and saves them in PostgreSQL
func (tb *TradingBot) handleConfigSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		OptionsConfigs []data.OptionsIndexConfig   `json:"options_configs"`
		SystemConfigs  map[string]map[string]string `json:"system_configs"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Invalid JSON: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// 1. Save Options Index Configs (normalize times to HH:MM:SS)
	for i := range req.OptionsConfigs {
		// Enforce BaseLotSize is a multiple of default lot size (e.g. 65 for NIFTY, 15 for BANKNIFTY, 20 for SENSEX, 120 for MIDCPNIFTY)
		spec, _ := data.ResolveIndexSpec(req.OptionsConfigs[i].IndexSymbol)
		defLot := spec.BaseLotSize
		if defLot <= 0 {
			defLot = 65
		}
		if req.OptionsConfigs[i].BaseLotSize < defLot {
			req.OptionsConfigs[i].BaseLotSize = defLot
		} else if req.OptionsConfigs[i].BaseLotSize%defLot != 0 {
			rem := req.OptionsConfigs[i].BaseLotSize % defLot
			req.OptionsConfigs[i].BaseLotSize = req.OptionsConfigs[i].BaseLotSize - rem
			if req.OptionsConfigs[i].BaseLotSize == 0 {
				req.OptionsConfigs[i].BaseLotSize = defLot
			}
		}

		req.OptionsConfigs[i].LastNewTradeTime = data.NormalizeTimeHHMMSS(req.OptionsConfigs[i].LastNewTradeTime)
		req.OptionsConfigs[i].AutoSquareOffTime = data.NormalizeTimeHHMMSS(req.OptionsConfigs[i].AutoSquareOffTime)
		req.OptionsConfigs[i].SuperTrendCutoffTime = data.NormalizeTimeHHMMSS(req.OptionsConfigs[i].SuperTrendCutoffTime)
		if err := tb.db.SaveOptionsIndexConfig(ctx, &req.OptionsConfigs[i]); err != nil {
			tb.logger.Error("Failed to save options index config", map[string]interface{}{"index": req.OptionsConfigs[i].IndexSymbol, "error": err.Error()})
			http.Error(w, fmt.Sprintf(`{"error":"Failed to save options config for %s: %s"}`, req.OptionsConfigs[i].IndexSymbol, err.Error()), http.StatusInternalServerError)
			return
		}
	}

	// 2. Save System Configs (normalize times to HH:MM:SS)
	if len(req.SystemConfigs) > 0 {
		for _, kv := range req.SystemConfigs {
			for k, v := range kv {
				if strings.Contains(k, "time") || strings.Contains(k, "allowed_before") || strings.Contains(k, "allowed_after") {
					kv[k] = data.NormalizeTimeHHMMSS(v)
				}
			}
		}
		if err := tb.db.SaveSystemConfigsBatch(ctx, req.SystemConfigs); err != nil {
			tb.logger.Error("Failed to save system configs batch", map[string]interface{}{"error": err.Error()})
			http.Error(w, fmt.Sprintf(`{"error":"Failed to save system configs: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
	}

	// 3. Reload modular strategies and risk configurations immediately into memory
	tb.loadModularStrategyConfigs()

	tb.logger.Info("Strategy and system settings successfully saved to database and reloaded", map[string]interface{}{
		"indices_count": len(req.OptionsConfigs),
		"categories":    len(req.SystemConfigs),
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Settings saved to database and applied in-memory successfully.",
	})
}

// handleSystemRestart handles time-gated bot restart requests from the UI
func (tb *TradingBot) handleSystemRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	nowIST := time.Now().In(data.ISTLocation)
	currentSecOfDay := nowIST.Hour()*3600 + nowIST.Minute()*60 + nowIST.Second()
	allowedBefore := tb.cfg.RestartAllowedBefore
	if allowedBefore == "" {
		allowedBefore = "09:15:00"
	}
	allowedAfter := tb.cfg.RestartAllowedAfter
	if allowedAfter == "" {
		allowedAfter = "15:45:00"
	}
	beforeSec := data.ParseTimeToSeconds(allowedBefore)
	afterSec := data.ParseTimeToSeconds(allowedAfter)

	// Strict Market Hours Restart Lock: Block restart between allowedBefore and allowedAfter
	if currentSecOfDay >= beforeSec && currentSecOfDay < afterSec {
		tb.logger.Warn("Bot restart rejected: locked during live market hours", map[string]interface{}{
			"current_time":   nowIST.Format("15:04:05"),
			"allowed_before": allowedBefore,
			"allowed_after":  allowedAfter,
		})
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("Bot restart is strictly locked during live market hours (%s - %s IST) to protect active trading positions.", allowedBefore, allowedAfter),
		})
		return
	}

	tb.logger.Info("Bot restart initiated from UI. Shutting down process for auto-restart...", map[string]interface{}{
		"current_time": nowIST.Format("15:04:05"),
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"status":  "restarting",
		"message": "Bot restart triggered. The system will reconnect in 3 seconds.",
	})

	go func() {
		time.Sleep(500 * time.Millisecond)
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
			SELECT date::TEXT, symbol, token, selectors
			FROM daily_watchlists
			WHERE date = $1
			ORDER BY symbol ASC
		`, dateParam)
	} else {
		rows, err = tb.db.QueryContext(tb.ctx, `
			SELECT date::TEXT, symbol, token, selectors
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
		Date            string   `json:"date"`
		Symbol          string   `json:"symbol"`
		Token           int64    `json:"token"`
		PrimarySelector string   `json:"primary_selector"`
		ShiftPct        float64  `json:"shift_pct"`
		PriorityRank    int      `json:"priority_rank"`
		Selectors       []string `json:"selectors"`
	}

	list := make([]Item, 0)
	for rows.Next() {
		var date, symbol, selectorsStr string
		var token int64
		if err := rows.Scan(&date, &symbol, &token, &selectorsStr); err != nil {
			continue
		}
		var selectors []string
		primarySelector := "PDH_PDL"

		tb.watchlistSelectorMapMutex.RLock()
		if s, ok := tb.watchlistSelectorMap[symbol]; ok && s != "" {
			primarySelector = s
		}
		tb.watchlistSelectorMapMutex.RUnlock()

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
					if primarySelector == "PDH_PDL" && selectorName != "" {
						primarySelector = selection.NormalizeSelectorName(selectorName)
					}
				}
			}
		}

		shiftPct := 0.0
		priorityRank := 1
		if cfg, exists := tb.stockSelectionConfigs[primarySelector]; exists {
			shiftPct = cfg.LevelShiftPct
			priorityRank = cfg.PriorityRank
		}

		list = append(list, Item{
			Date:            date,
			Symbol:          symbol,
			Token:           token,
			PrimarySelector: primarySelector,
			ShiftPct:        shiftPct,
			PriorityRank:    priorityRank,
			Selectors:       selectors,
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

	seenSymbols := make(map[string]bool)
	if tb.optionsPosMgrs != nil {
		for _, mgr := range tb.optionsPosMgrs {
			if mgr == nil {
				continue
			}
			optPos := mgr.GetActivePosition()
			if optPos != nil && !seenSymbols[optPos.Symbol] {
				seenSymbols[optPos.Symbol] = true
				exp := optPos.Expiry
				if exp == "" {
					exp = optPos.ExpiryDate
				}
				if exp == "" {
					exp = risk.GetUpcomingOptionExpiry(optPos.CreatedAt)
				}
				latestPrice := optPos.LatestPrice
				if latestPrice <= 0 {
					latestPrice = optPos.EntryPremium
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
					LatestPrice:     latestPrice,
					Strategy:        "OPTIONS_SUPERTREND",
					CreatedAt:       optPos.CreatedAt,
					BrokerSLOrderID: "",
				})
			}
		}
	} else if tb.optionsPosMgr != nil {
		if optPos := tb.optionsPosMgr.GetActivePosition(); optPos != nil {
			exp := optPos.Expiry
			if exp == "" {
				exp = optPos.ExpiryDate
			}
			if exp == "" {
				exp = risk.GetUpcomingOptionExpiry(optPos.CreatedAt)
			}
			latestPrice := optPos.LatestPrice
			if latestPrice <= 0 {
				latestPrice = optPos.EntryPremium
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
				LatestPrice:     latestPrice,
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
	indexParam := r.URL.Query().Get("index")
	if indexParam == "" {
		indexParam = r.URL.Query().Get("symbol")
	}
	if indexParam == "" {
		indexParam = tb.cfg.Options.IndexSymbol
	}

	spec, _ := data.ResolveIndexSpec(indexParam)
	mgr := tb.GetOptionsPosManager(spec.Name)

	var status map[string]interface{}
	if mgr != nil {
		status = mgr.GetStatus()
	} else if tb.optionsPosMgr != nil {
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
	status["index"] = spec.Name
	status["clean_prefix"] = spec.CleanPrefix
	status["spot_token"] = spec.SpotToken
	status["options_exchange"] = spec.OptionsExchange
	status["base_lot_size"] = spec.BaseLotSize
	status["strike_step"] = spec.StrikeStep
	status["active_indices"] = tb.cfg.Options.ActiveIndices
	status["live_indices"] = tb.cfg.Options.LiveIndices
	status["global_live"] = tb.cfg.Options.LiveTrading
	status["supported_indices"] = data.GetAllSupportedIndices()
	status["live_trading"] = tb.cfg.Options.IsIndexLiveTrading(spec.Name)
	status["trade_mode"] = tb.cfg.Options.TradeMode
	status["auto_square_off_time"] = tb.cfg.Options.AutoSquareOffTime
	status["last_new_trade_time"] = tb.cfg.Options.LastNewTradeTime
	status["target_entry_premium"] = tb.cfg.Options.TargetEntryPremium
	status["expiry_type"] = tb.cfg.Options.ExpiryType
	status["next_month_days"] = tb.cfg.Options.NextMonthDays
	status["st1_params"] = fmt.Sprintf("(%d, %g)", tb.cfg.Options.SuperTrendST1Period, tb.cfg.Options.SuperTrendST1Factor)
	status["st2_params"] = fmt.Sprintf("(%d, %g)", tb.cfg.Options.SuperTrendST2Period, tb.cfg.Options.SuperTrendST2Factor)
	status["st3_params"] = fmt.Sprintf("(%d, %g)", tb.cfg.Options.SuperTrendST3Period, tb.cfg.Options.SuperTrendST3Factor)
	status["trail_sl_enabled"] = tb.cfg.Options.TrailSLEnabled
	status["trail_sl_pct"] = tb.cfg.Options.TrailSLPct

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
		WHERE strategy = 'OPTIONS_SUPERTREND' AND (symbol LIKE $1 OR $1 = '')
	`, spec.CleanPrefix+"%").Scan(&totalTrades, &winTrades, &totalPnL)

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

// handleOptionsIndices returns configured active indices and all supported index specs
func (tb *TradingBot) handleOptionsIndices(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	res := map[string]interface{}{
		"active_indices":    tb.cfg.Options.ActiveIndices,
		"live_indices":      tb.cfg.Options.LiveIndices,
		"global_live":       tb.cfg.Options.LiveTrading,
		"supported_indices": data.GetAllSupportedIndices(),
	}
	json.NewEncoder(w).Encode(res)
}

// handleOptionsReset clears active position state in memory and database
func (tb *TradingBot) handleOptionsReset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	indexParam := r.URL.Query().Get("index")
	if indexParam == "" {
		indexParam = r.URL.Query().Get("symbol")
	}
	if indexParam != "" {
		spec, _ := data.ResolveIndexSpec(indexParam)
		mgr := tb.GetOptionsPosManager(spec.Name)
		if mgr != nil {
			mgr.ClearActivePosition(tb.ctx)
		}
		if tb.db != nil {
			_, _ = tb.db.WithContext(tb.ctx).ExecContext(tb.ctx, "DELETE FROM options_bot_state WHERE index_symbol = $1", spec.Name)
		}
	} else {
		if tb.optionsPosMgrs != nil {
			for _, mgr := range tb.optionsPosMgrs {
				if mgr != nil {
					mgr.ClearActivePosition(tb.ctx)
				}
			}
		} else if tb.optionsPosMgr != nil {
			tb.optionsPosMgr.ClearActivePosition(tb.ctx)
		}
		if tb.db != nil {
			_, _ = tb.db.WithContext(tb.ctx).ExecContext(tb.ctx, "TRUNCATE options_bot_state; DELETE FROM trades WHERE created_at >= '2026-08-07 00:00:00';")
		}
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
	symbol := r.URL.Query().Get("index")
	if symbol == "" {
		symbol = r.URL.Query().Get("symbol")
	}
	if symbol == "" {
		symbol = "NIFTY 50"
	}

	spec, _ := data.ResolveIndexSpec(symbol)
	token := spec.SpotToken
	if token <= 0 {
		token = 256265
	}

	nowIST := time.Now().In(data.ISTLocation)
	isMarketHours := (nowIST.Hour() > 9 || (nowIST.Hour() == 9 && nowIST.Minute() >= 15)) && (nowIST.Hour() < 15 || (nowIST.Hour() == 15 && nowIST.Minute() <= 35))

	candles, err := tb.db.GetLastNCandles("candles_5m", token, 500)
	if (err != nil || len(candles) == 0) && tb.kiteClient != nil {
		tb.ensureOptionsHistoricalData(spec.Name)
		candles, _ = tb.db.GetLastNCandles("candles_5m", token, 500)
	} else if isMarketHours && len(candles) > 0 && tb.kiteClient != nil {
		lastCandleTime := data.NormalizeToIST(candles[len(candles)-1].Time)
		if nowIST.Sub(lastCandleTime) > 6*time.Minute {
			go tb.ensureOptionsHistoricalData(spec.Name)
		}
	}

	cutoffSecOfDay := data.ParseTimeToSeconds(tb.cfg.Options.SuperTrendCutoffTime)

	// Deduplicate candles by 5-minute floored Unix timestamp and sort chronologically in IST
	seenTimes := make(map[int64]bool)
	uniqueCandles := make([]data.Candle, 0, len(candles))
	for _, c := range candles {
		tIST := data.NormalizeToIST(c.Time)
		cSecOfDay := tIST.Hour()*3600 + tIST.Minute()*60 + tIST.Second()
		if cSecOfDay > cutoffSecOfDay {
			continue // Exclude post-cutoff EOD candles
		}
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
		if tr.Strategy == "OPTIONS_SUPERTREND" && strings.HasPrefix(tr.Symbol, spec.CleanPrefix) {
			entryTime := tr.EntryTime
			if entryTime.IsZero() {
				entryTime = tr.CreatedAt.Add(-time.Duration(tr.TimeHeldMinutes) * time.Minute)
			}
			entryTimeIST := data.NormalizeToIST(entryTime)
			if entryTimeIST.Hour() < 9 || (entryTimeIST.Hour() == 9 && entryTimeIST.Minute() < 15) {
				entryTimeIST = time.Date(entryTimeIST.Year(), entryTimeIST.Month(), entryTimeIST.Day(), 9, 15, 0, 0, data.ISTLocation)
			}
			entryKey := formatKey(entryTimeIST)

			if strings.Contains(tr.Symbol, "PE") {
				entryTradeMap[entryKey] = "ENTRY_SELL_PE"
			} else if strings.Contains(tr.Symbol, "CE") {
				entryTradeMap[entryKey] = "ENTRY_SELL_CE"
			}

			// Only attach exit markers for completed/closed trades (never for LIVE active trades)
			if tr.Status != "LIVE" {
				exitTime := tr.ExitTime
				if exitTime.IsZero() {
					exitTime = tr.CreatedAt
				}
				if !exitTime.IsZero() {
					exitTimeIST := data.NormalizeToIST(exitTime)
					exitKey := formatKey(exitTimeIST)
					statusUpper := strings.ToUpper(tr.Status)
					if exitTimeIST.Hour() == 15 && exitTimeIST.Minute() >= 14 {
						exitTradeMap[exitKey] = "EXIT_EOD"
					} else if strings.Contains(statusUpper, "SL") || strings.Contains(statusUpper, "STOP") || (tr.EntryPrice > 0 && tr.ExitPrice >= tr.EntryPrice*1.45) {
						exitTradeMap[exitKey] = "EXIT_SL"
					} else if strings.Contains(statusUpper, "EOD") {
						exitTradeMap[exitKey] = "EXIT_EOD"
					} else if strings.Contains(statusUpper, "REVERSAL") {
						exitTradeMap[exitKey] = "EXIT_REVERSAL"
					} else if tr.PnL >= 0 {
						exitTradeMap[exitKey] = "EXIT_PROFIT"
					} else {
						exitTradeMap[exitKey] = "EXIT_REVERSAL"
					}
				}
			}
		}
	}

	// Also attach active live options trade entry marker if present for this index
	if mgr := tb.GetOptionsPosManager(spec.Name); mgr != nil {
		if optPos := mgr.GetActivePosition(); optPos != nil {
			entryKey := formatKey(optPos.CreatedAt)
			if strings.Contains(optPos.Symbol, "PE") {
				entryTradeMap[entryKey] = "ENTRY_SELL_PE"
			} else if strings.Contains(optPos.Symbol, "CE") {
				entryTradeMap[entryKey] = "ENTRY_SELL_CE"
			}
		}
	}

	series := stEngine.CalculateTripleSuperTrendSeries(candles)
	var allPoints []IndicatorPoint
	for i, c := range candles {
		res := series[i]
		cKey := formatKey(c.Time)

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
			Time:   c.Time.Unix(),
			Open:   c.Open,
			High:   c.High,
			Low:    c.Low,
			Close:  c.Close,
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

	todayStr := data.GetEffectiveTradingDate(time.Now())
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
	indexParam := r.URL.Query().Get("index")
	if indexParam == "" {
		indexParam = r.URL.Query().Get("symbol")
	}
	isLive := tb.cfg.Options.LiveTrading
	if indexParam != "" {
		isLive = tb.cfg.Options.IsIndexLiveTrading(indexParam)
	}

	resp := map[string]interface{}{
		"success":        true,
		"live_trading":   isLive,
		"global_live":    tb.cfg.Options.LiveTrading,
		"live_indices":   tb.cfg.Options.LiveIndices,
		"active_indices": tb.cfg.Options.ActiveIndices,
		"trade_mode":     tb.cfg.Options.TradeMode,
		"read_only":      true,
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
	if len(results) > 20 {
		results = results[:20]
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

// handleExcludeStock handles permanently deleting a stock from trade selection and database
func (tb *TradingBot) handleExcludeStock(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost && r.Method != http.MethodDelete && r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Action string `json:"action"` // "delete"
		Symbol string `json:"symbol"`
		Date   string `json:"date"`   // optional YYYY-MM-DD
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Symbol == "" {
		req.Symbol = r.URL.Query().Get("symbol")
		req.Action = r.URL.Query().Get("action")
		req.Date = r.URL.Query().Get("date")
	}

	symbol := normalizeSymbolAlias(strings.TrimSpace(strings.ToUpper(req.Symbol)))

	if symbol == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Symbol is required"})
		return
	}

	nowInLoc := time.Now().In(data.ISTLocation)
	effTodayStr := data.GetEffectiveTradingDate(nowInLoc)
	targetDateStr := req.Date
	if targetDateStr == "" {
		targetDateStr = effTodayStr
	}
	targetDate, _ := time.ParseInLocation("2006-01-02", targetDateStr, data.ISTLocation)

	if targetDateStr < effTodayStr {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Cannot perform trade selection actions on previous day data",
		})
		return
	}

	// 1. Check if a trade is currently active for this symbol in RiskManager
	if tb.riskMgr != nil {
		positions := tb.riskMgr.GetOpenPositions()
		for _, pos := range positions {
			if pos.Symbol == symbol && pos.Quantity != 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"error":   fmt.Sprintf("Cannot delete stock while a trade is currently active for %s", symbol),
				})
				return
			}
		}
	}

	// 2. Check if an options trade is active for this symbol
	if tb.optionsPosMgr != nil {
		if optPos := tb.optionsPosMgr.GetActivePosition(); optPos != nil {
			if strings.Contains(optPos.Symbol, symbol) {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"error":   fmt.Sprintf("Cannot delete stock while an options trade is active for %s", symbol),
				})
				return
			}
		}
	}

	// 3. Remove from active watchlist map across all strategy engines & unsubscribe ticker
	tb.watchlistMutex.Lock()
	token := tb.watchlist[symbol]
	delete(tb.watchlist, symbol)
	for stratName := range tb.strategyWatchlists {
		if tb.strategyWatchlists[stratName] != nil {
			delete(tb.strategyWatchlists[stratName], symbol)
		}
	}
	tb.watchlistMutex.Unlock()

	tb.watchlistSelectorMapMutex.Lock()
	delete(tb.watchlistSelectorMap, symbol)
	tb.watchlistSelectorMapMutex.Unlock()

	tb.ClearStockExclusion(symbol)

	if token > 0 && tb.ticker != nil {
		tb.ticker.Unsubscribe([]int64{token})
	}

	// 4. Permanently delete from PostgreSQL database tables (daily_watchlists & daily_manual_watchlist)
	if err := tb.db.DeleteDailyWatchlistStock(tb.ctx, targetDateStr, symbol); err != nil {
		tb.logger.Error("Failed to delete stock from daily_watchlists table", map[string]interface{}{"error": err.Error(), "symbol": symbol})
	}
	if err := tb.db.RemoveSymbolFromDailyManualWatchlist(tb.ctx, targetDate, symbol); err != nil {
		tb.logger.Error("Failed to remove stock from daily_manual_watchlist table", map[string]interface{}{"error": err.Error(), "symbol": symbol})
	}

	tb.logger.Info("Permanently deleted stock from trade selection and database", map[string]interface{}{"symbol": symbol, "date": targetDateStr})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Stock %s permanently deleted from watchlist and database", symbol),
		"symbol":  symbol,
	})
}

// handleUpdateDailyWatchlistStrategy handles changing the stock selection strategy on an individual stock
func (tb *TradingBot) handleUpdateDailyWatchlistStrategy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Date     string `json:"date"`     // optional YYYY-MM-DD
		Symbol   string `json:"symbol"`   // e.g. "TCS"
		Selector string `json:"selector"` // e.g. "52WH_52WL"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	symbol := normalizeSymbolAlias(strings.TrimSpace(strings.ToUpper(req.Symbol)))
	if symbol == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Symbol is required"})
		return
	}

	nowInLoc := time.Now().In(data.ISTLocation)
	effTodayStr := data.GetEffectiveTradingDate(nowInLoc)
	targetDateStr := req.Date
	if targetDateStr == "" {
		targetDateStr = effTodayStr
	}
	targetDate, _ := time.ParseInLocation("2006-01-02", targetDateStr, data.ISTLocation)

	if targetDateStr < effTodayStr {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Cannot modify selection strategy for previous day data",
		})
		return
	}

	normSelector := selection.NormalizeSelectorName(req.Selector)
	if normSelector == "" {
		normSelector = "PDH_PDL"
	}

	// 1. Update in PostgreSQL daily_watchlists table
	if err := tb.db.UpdateDailyWatchlistSelector(tb.ctx, targetDateStr, symbol, "MANUAL:"+normSelector); err != nil {
		tb.logger.Error("Failed to update daily_watchlists selector", map[string]interface{}{"error": err.Error()})
	}

	// 2. Update in PostgreSQL daily_manual_watchlist table
	if err := tb.db.UpdateSymbolInDailyManualWatchlist(tb.ctx, targetDate, symbol, normSelector); err != nil {
		tb.logger.Error("Failed to update daily_manual_watchlist", map[string]interface{}{"error": err.Error()})
	}

	// 3. Update in-memory selector map
	tb.watchlistSelectorMapMutex.Lock()
	tb.watchlistSelectorMap[symbol] = normSelector
	tb.watchlistSelectorMapMutex.Unlock()

	// 4. Update level shifted High/Low on active strategy engines
	token := tb.resolveSymbolToken(tb.ctx, symbol)
	if token > 0 {
		high, low, _ := tb.resolvePreviousDayHighLow(token, symbol, data.ISTLocation)
		_, shiftPct := tb.resolveSymbolSelectorAndShift(symbol)
		shiftedHigh := selection.CalculateLevelShiftedPrice(high, shiftPct, 0.05)
		shiftedLow := selection.CalculateLevelShiftedPrice(low, shiftPct, 0.05)
		tb.watchlistMutex.Lock()
		for _, strat := range tb.activeStrategies {
			if vbEngine, isVB := strat.(*strategy.VandeBharatEngine); isVB {
				vbEngine.SetPreviousDayHighLow(symbol, shiftedHigh, shiftedLow)
			} else if lvEngine, isLV := strat.(*strategy.LowVolumeEngine); isLV {
				lvEngine.SetPreviousDayHighLow(symbol, shiftedHigh, shiftedLow)
			}
		}
		tb.watchlistMutex.Unlock()
	}

	tb.logger.Info("Updated stock selection strategy", map[string]interface{}{
		"symbol":   symbol,
		"selector": normSelector,
		"date":     targetDateStr,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"message":  fmt.Sprintf("Selection strategy for %s updated to %s", symbol, normSelector),
		"symbol":   symbol,
		"selector": normSelector,
	})
}

// normalizeSymbolAlias normalizes common symbol aliases to official NSE/BSE tradingsymbols
func normalizeSymbolAlias(symbol string) string {
	sym := strings.TrimSpace(strings.ToUpper(symbol))
	switch sym {
	case "SBI":
		return "SBIN"
	case "L&T":
		return "LT"
	case "BAJAJ AUTO":
		return "BAJAJ-AUTO"
	case "M&M", "MM":
		return "M&M"
	case "NIFTY":
		return "NIFTY 50"
	case "BANKNIFTY":
		return "NIFTY BANK"
	}
	return sym
}

// resolveSymbolToken resolves a symbol token from DB, security master, or Zerodha API
func (tb *TradingBot) resolveSymbolToken(ctx context.Context, symbol string) int64 {
	normSym := normalizeSymbolAlias(symbol)

	token, err := tb.db.ResolveSymbolToken(ctx, normSym)
	if err == nil && token > 0 {
		return token
	}
	if normSym != symbol {
		token, err = tb.db.ResolveSymbolToken(ctx, symbol)
		if err == nil && token > 0 {
			return token
		}
	}

	if tb.securityMaster != nil {
		token, err = tb.securityMaster.GetInstrumentToken(normSym)
		if err == nil && token > 0 {
			return token
		}
		if normSym != symbol {
			token, err = tb.securityMaster.GetInstrumentToken(symbol)
			if err == nil && token > 0 {
				return token
			}
		}

		token, err = tb.securityMaster.ResolveAndAddSymbol(ctx, normSym)
		if err == nil && token > 0 {
			return token
		}
		if normSym != symbol {
			token, err = tb.securityMaster.ResolveAndAddSymbol(ctx, symbol)
			if err == nil && token > 0 {
				return token
			}
		}
	}
	return 0
}
