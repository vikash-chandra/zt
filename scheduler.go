package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"zerodha-trading/data"
	"zerodha-trading/selection"
	"zerodha-trading/strategy"
)

// runDailyStrategyScheduler schedules strategy actions for the day
func (tb *TradingBot) runDailyStrategyScheduler(loc *time.Location) {
	defer tb.wg.Done()

	tb.logger.Info("Daily Strategy scheduler loop started", nil)

	selectHour, selectMin, err := parseTimeHM(tb.cfg.StockSelectTime)
	if err != nil {
		selectHour, selectMin = 9, 30
	}

	evgHour, evgMin, err := parseTimeHM(tb.cfg.EVGStockSelectTime)
	if err != nil {
		evgHour, evgMin = 9, 7
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	breadthLogged := false
	watchlistFiltered := false
	evgAdjSelectionDone := false
	evgStdSelectionDone := false
	hardSquareOffDone := false

	// Check database to see if today's pre-selection scans are already done to prevent duplicate runs on restart
	todayStr := time.Now().In(loc).Format("2006-01-02")
	if adjResults, err := tb.db.GetPreSelectionResults(todayStr, "ADJUSTED"); err == nil && len(adjResults) > 0 {
		evgAdjSelectionDone = true
		tb.logger.Info("Detected existing ADJUSTED pre-selection results for today in database. Skipping scan.", map[string]interface{}{"date": todayStr})
	}
	if stdResults, err := tb.db.GetPreSelectionResults(todayStr, "STANDARD"); err == nil && len(stdResults) > 0 {
		evgStdSelectionDone = true
		tb.logger.Info("Detected existing STANDARD pre-selection results for today in database. Skipping scan.", map[string]interface{}{"date": todayStr})
	}

	for {
		select {
		case <-tb.ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().In(loc)
			hour := now.Hour()
			minute := now.Minute()
			second := now.Second()

			selectBoundary := time.Date(now.Year(), now.Month(), now.Day(), selectHour, selectMin, 0, 0, loc)
			breadthBoundary := selectBoundary.Add(-1 * time.Minute)
			evgBoundaryAdj := time.Date(now.Year(), now.Month(), now.Day(), evgHour, evgMin, 0, 0, loc)
			evgBoundaryStd := time.Date(now.Year(), now.Month(), now.Day(), 9, 10, 0, 0, loc)

			// 0a. Step 0a: Equity Volume Gainers ADJUSTED pre-selection (exactly at EVG selection time, e.g., 09:07 AM)
			if !evgAdjSelectionDone && !now.Before(evgBoundaryAdj) && now.Hour() < 15 {
				tb.logger.Info(fmt.Sprintf("[EVG] Triggering %02d:%02d:00 Equity Volume Gainers ADJUSTED pre-selection...", evgHour, evgMin), nil)
				if err := tb.runEquityVolumeGainersPreSelection(loc, "ADJUSTED"); err != nil {
					tb.logger.Error("Failed to execute Adjusted pre-selection", map[string]interface{}{"error": err.Error()})
				} else {
					evgAdjSelectionDone = true
				}
			}

			// 0b. Step 0b: Equity Volume Gainers STANDARD pre-selection (exactly at 09:10 AM)
			if !evgStdSelectionDone && !now.Before(evgBoundaryStd) && now.Hour() < 15 {
				tb.logger.Info("[EVG] Triggering 09:10:00 Equity Volume Gainers STANDARD pre-selection...", nil)
				if err := tb.runEquityVolumeGainersPreSelection(loc, "STANDARD"); err != nil {
					tb.logger.Error("Failed to execute Standard pre-selection", map[string]interface{}{"error": err.Error()})
				} else {
					evgStdSelectionDone = true
				}
			}

			// 1. Step 1: Pre-market breadth logging (1 minute before stock selection time)
			if !breadthLogged && !now.Before(breadthBoundary) && now.Hour() < 15 {
				tb.logger.Info(fmt.Sprintf("[LOW_VOLUME] Triggering %02d:%02d:00 pre-market breadth calculations...", breadthBoundary.Hour(), breadthBoundary.Minute()), nil)
				if err := tb.logMarketBreadth(loc); err != nil {
					tb.logger.Error("Failed to run pre-market breadth check", map[string]interface{}{"error": err.Error()})
				} else {
					breadthLogged = true
				}
			}

			// 2. Step 2: Dynamic Stock Selection Filter (exactly at stock selection time)
			if !watchlistFiltered && breadthLogged && !now.Before(selectBoundary) && now.Hour() < 15 {
				tb.logger.Info(fmt.Sprintf("[LOW_VOLUME] Triggering %02d:%02d:00 dynamic watchlist filter...", selectHour, selectMin), nil)
				if err := tb.selectWatchlist(loc); err != nil {
					tb.logger.Error("Failed to resolve dynamic watchlist selection", map[string]interface{}{"error": err.Error()})
				} else {
					watchlistFiltered = true
				}
			}

			// 3. Step 7: Hard Square-off Override (03:15:00 PM)
			if !hardSquareOffDone && ((hour == 15 && minute >= 15) || hour > 15) {
				tb.logger.Info("[LOW_VOLUME] Triggering 03:15:00 PM hard square-off override...", nil)
				tb.hardSquareOff()
				hardSquareOffDone = true
			}

			// Reset daily state at midnight
			if hour == 0 && minute == 0 && second == 0 {
				breadthLogged = false
				watchlistFiltered = false
				evgAdjSelectionDone = false
				evgStdSelectionDone = false
				hardSquareOffDone = false
				for _, strat := range tb.activeStrategies {
					strat.Reset()
				}
				tb.globalBias = ""

				// Reset watchlist to empty
				tb.watchlistMutex.Lock()
				tb.watchlist = make(map[string]int64)
				tb.watchlistMutex.Unlock()
			}
		}
	}
}

// logMarketBreadth performs the pre-market Advance-Decline breadth calculation
func (tb *TradingBot) logMarketBreadth(loc *time.Location) error {
	nifty50Map, err := tb.securityMaster.GetNifty50Constituents(tb.ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch Nifty 50 constituents: %w", err)
	}

	var keys []string
	for symbol := range nifty50Map {
		keys = append(keys, "NSE:"+symbol)
	}

	tb.logger.Info("[LOW_VOLUME] Fetching Nifty 50 OHLC snapshot...", map[string]interface{}{"stocks": len(keys)})
	ohlcData, err := tb.kiteClient.GetOHLC(keys...)
	if err != nil {
		return fmt.Errorf("failed to fetch Nifty 50 OHLC snapshot from Zerodha: %w", err)
	}

	advances := 0
	declines := 0
	neutrals := 0

	type Detail struct {
		Symbol    string  `json:"symbol"`
		Open      float64 `json:"open"`
		LTP       float64 `json:"ltp"`
		PctChange float64 `json:"pct_change"`
		Category  string  `json:"category"`
	}
	var details []Detail

	for key, entry := range ohlcData {
		open := entry.OHLC.Open
		ltp := entry.LastPrice
		symbol := key[4:] // remove "NSE:"

		if open == 0 {
			continue
		}

		referencePrice := entry.OHLC.Close
		if referencePrice == 0 {
			referencePrice = open
		}
		pctChange := ((ltp - referencePrice) / referencePrice) * 100.0
		category := "NEUTRAL"
		if pctChange > 0.0 {
			category = "ADVANCE"
			advances++
		} else if pctChange < 0.0 {
			category = "DECLINE"
			declines++
		} else {
			neutrals++
		}

		details = append(details, Detail{
			Symbol:    symbol,
			Open:      open,
			LTP:       ltp,
			PctChange: pctChange,
			Category:  category,
		})
	}

	// Check if a manual bias is configured for today
	manualBias, err := tb.db.GetDailyBias(tb.ctx, time.Now().In(loc))
	if err != nil {
		tb.logger.Error("Failed to fetch daily bias from database", map[string]interface{}{"error": err.Error()})
	}

	if manualBias != "" {
		tb.globalBias = manualBias
		tb.logger.Info("[LOW_VOLUME] Using manual daily global bias from database", map[string]interface{}{
			"global_bias": tb.globalBias,
		})
	} else {
		tb.globalBias = "SELL_ONLY"
		if advances > declines {
			tb.globalBias = "BUY_ONLY"
		}
	}

	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("failed to marshal details JSON: %w", err)
	}

	err = tb.db.SaveMarketBreadthLog(tb.ctx, time.Now().In(loc), advances, declines, neutrals, tb.globalBias, string(detailsJSON))
	if err != nil {
		tb.logger.Error("Failed to save market breadth logs to database", map[string]interface{}{"error": err.Error()})
	}

	tb.logger.Info("[LOW_VOLUME] Daily global bias established", map[string]interface{}{
		"advances":    advances,
		"declines":    declines,
		"neutrals":    neutrals,
		"global_bias": tb.globalBias,
	})

	return nil
}

// selectWatchlist filters and aggregates the watchlist for all active strategies using their mapped selectors
func (tb *TradingBot) selectWatchlist(loc *time.Location) error {
	if tb.globalBias == "NO_TRADE" || tb.globalBias == "" {
		tb.logger.Info("Global bias is NO_TRADE or empty. Skipping watchlist dynamic selection.", map[string]interface{}{"bias": tb.globalBias})
		return nil
	}

	// Check if manual watchlist symbols are configured in the database for today
	manualWatchlist, err := tb.db.GetDailyManualWatchlist(tb.ctx, time.Now().In(loc))
	if err != nil {
		tb.logger.Error("Failed to fetch daily manual watchlist from database", map[string]interface{}{"error": err.Error()})
	}

	if len(manualWatchlist) > 0 {
		tb.logger.Info("Using manual daily watchlist from database. STRATEGY_WATCHLIST_SIZE constraint is discarded.", map[string]interface{}{
			"symbols":        manualWatchlist,
			"watchlist_size": tb.cfg.StrategyWatchlistSize,
			"symbols_count":  len(manualWatchlist),
		})

		for _, strat := range tb.activeStrategies {
			strat.Reset()
		}

		tb.watchlistMutex.Lock()
		tb.watchlist = make(map[string]int64)
		var selectedTokens []int64

		for _, symbol := range manualWatchlist {
			token, err := tb.securityMaster.GetInstrumentToken(symbol)
			if err != nil || token <= 0 {
				token, err = tb.db.ResolveSymbolToken(tb.ctx, symbol)
			}
			if err != nil || token <= 0 {
				token, err = tb.securityMaster.ResolveAndAddSymbol(tb.ctx, symbol)
			}
			if err == nil && token > 0 {
				tb.watchlist[symbol] = token
				selectedTokens = append(selectedTokens, token)
			} else {
				tb.logger.Error("Skipped manual watchlist symbol: failed to resolve token from Zerodha or DB", map[string]interface{}{
					"symbol": symbol,
					"error":  err.Error(),
				})
			}
		}

		// Also bind strategy watchlists Copy for rendering
		for _, strat := range tb.activeStrategies {
			tb.strategyWatchlists[strat.Name()] = tb.watchlist
			
			// If strategy is VANDE_BHARAT, resolve and bind the PDH & PDL values
			if strat.Name() == "VANDE_BHARAT" {
				vbEngine, isVB := strat.(*strategy.VandeBharatEngine)
				if isVB {
					for symbol, token := range tb.watchlist {
						high, low, err := tb.resolvePreviousDayHighLow(token, symbol, loc)
						if err != nil {
							tb.logger.Error("Failed to query previous day high/low for manual stock", map[string]interface{}{
								"symbol": symbol,
								"error":  err.Error(),
							})
							high, low = 0.0, 0.0
						}
						vbEngine.SetPreviousDayHighLow(symbol, high, low)
					}
				}
			}
		}
		// Cache leverage requirements for manual watchlist
		tb.cacheWatchlistLeverage(manualWatchlist)
		tb.watchlistMutex.Unlock()

		tb.logger.Info("Manual Watchlist selection complete. Swapping WebSocket ticker subscriptions...", map[string]interface{}{"count": len(selectedTokens)})

		_ = tb.ticker.Close()
		time.Sleep(1 * time.Second)
		if err := tb.ticker.Connect(tb.ctx, selectedTokens); err != nil {
			return fmt.Errorf("failed to reconnect ticker to manual watchlist: %w", err)
		}

		// Trigger catch up sequence
		go func() {
			time.Sleep(2 * time.Second)
			tb.watchlistMutex.RLock()
			symbolsCopy := make(map[string]int64)
			for sym, tok := range tb.watchlist {
				symbolsCopy[sym] = tok
			}
			tb.watchlistMutex.RUnlock()

			for sym, tok := range symbolsCopy {
				tb.catchUpHistoricalCandles(sym, tok)
			}
		}()

		return nil
	}

	for _, strat := range tb.activeStrategies {
		strat.Reset()
	}

	tb.watchlistMutex.Lock()
	tb.watchlist = make(map[string]int64)
	var selectedTokens []int64
	tokenSet := make(map[int64]bool)

	for _, strat := range tb.activeStrategies {
		// Look up mapped selector name, default to SECURITIES_FO if not set
		selectorName, exists := tb.strategySelectorMap[strat.Name()]
		if !exists || selectorName == "" {
			selectorName = "SECURITIES_FO"
		}

		selector, active := tb.activeSelectors[selectorName]
		if !active {
			tb.logger.Warn("Selector is not active or not initialized, defaulting to Securities F&O", map[string]interface{}{
				"strategy": strat.Name(),
				"selector": selectorName,
			})
			selector = tb.activeSelectors["SECURITIES_FO"]
		}

		if selector != nil {
			tb.logger.Info("Running stock selector for strategy", map[string]interface{}{
				"strategy": strat.Name(),
				"selector": selector.Name(),
			})
			wList, err := selector.SelectStocks(tb.ctx, tb.logger.Logger, tb.kiteClient, tb.securityMaster, tb.globalBias, tb.cfg.StrategyWatchlistSize, tb.cfg.WatchlistMaxPctChange)
			if err != nil {
				tb.logger.Error("Failed to select stocks for strategy", map[string]interface{}{
					"strategy": strat.Name(),
					"error":    err.Error(),
				})
				continue
			}

			tb.strategyWatchlists[strat.Name()] = wList

			// If strategy is VANDE_BHARAT, resolve and bind the PDH & PDL values
			if strat.Name() == "VANDE_BHARAT" {
				vbEngine, isVB := strat.(*strategy.VandeBharatEngine)
				if isVB {
					for symbol, token := range wList {
						high, low, err := tb.resolvePreviousDayHighLow(token, symbol, loc)
						if err != nil {
							tb.logger.Error("Failed to query previous day high/low, using default fallback", map[string]interface{}{
								"symbol": symbol,
								"error":  err.Error(),
							})
							high, low = 0.0, 0.0
						}
						vbEngine.SetPreviousDayHighLow(symbol, high, low)
					}
				}
			}

			for symbol, token := range wList {
				tb.watchlist[symbol] = token
				if !tokenSet[token] {
					tokenSet[token] = true
					selectedTokens = append(selectedTokens, token)
				}
			}
		}
	}

	// Populate directional bias for the selected watchlist symbols from database
	tb.watchlistDirectionsMutex.Lock()
	tb.watchlistDirections = make(map[string]string)
	todayStr := time.Now().In(loc).Format("2006-01-02")
	for _, ruleSet := range []string{"STANDARD", "ADJUSTED"} {
		results, err := tb.db.GetPreSelectionResults(todayStr, ruleSet)
		if err == nil {
			for _, res := range results {
				tb.watchlistDirections[res.Ticker] = res.PredictedDirection
			}
		}
	}
	tb.watchlistDirectionsMutex.Unlock()

	// Cache leverage requirements for unified watchlist symbols
	var activeSymbols []string
	for symbol := range tb.watchlist {
		activeSymbols = append(activeSymbols, symbol)
	}
	tb.cacheWatchlistLeverage(activeSymbols)

	tb.watchlistMutex.Unlock()

	tb.logger.Info("Watchlist selection complete. Swapping WebSocket ticker subscriptions...", map[string]interface{}{"count": len(selectedTokens)})

	_ = tb.ticker.Close()
	time.Sleep(1 * time.Second)
	if err := tb.ticker.Connect(tb.ctx, selectedTokens); err != nil {
		return fmt.Errorf("failed to reconnect ticker to unified watchlist: %w", err)
	}

	// Fetch historical candles since 09:15 AM to fill any gaps for the selected symbols
	go func() {
		// Run in background to avoid blocking
		time.Sleep(2 * time.Second)
		tb.watchlistMutex.RLock()
		symbolsCopy := make(map[string]int64)
		for sym, tok := range tb.watchlist {
			symbolsCopy[sym] = tok
		}
		tb.watchlistMutex.RUnlock()

		for sym, tok := range symbolsCopy {
			tb.catchUpHistoricalCandles(sym, tok)
		}
	}()

	return nil
}

// catchUpHistoricalCandles retrieves historical 5m candles since 09:15 AM
func (tb *TradingBot) catchUpHistoricalCandles(symbol string, token int64) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}
	nowIST := time.Now().In(loc)
	today0915 := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 9, 15, 0, 0, loc).UTC()

	now := time.Now().UTC()
	if now.Before(today0915) {
		return
	}

	// 1. Try to catch up from local DB first to avoid Kite API rate limits
	dbCandles, dbErr := tb.db.GetCandlesForDay(tb.ctx, token, today0915)
	if dbErr == nil && len(dbCandles) > 0 {
		tb.logger.Info("Successfully caught up candles from local database", map[string]interface{}{"symbol": symbol, "count": len(dbCandles)})
		for _, c := range dbCandles {
			color := "DOJI"
			if c.Close > c.Open {
				color = "GREEN"
			} else if c.Close < c.Open {
				color = "RED"
			}

			candle := &data.Candle{
				Token:     token,
				Time:      c.Time,
				Open:      c.Open,
				High:      c.High,
				Low:       c.Low,
				Close:     c.Close,
				Volume:    c.Volume,
				VWAP:      (c.Open + c.High + c.Low + c.Close) / 4.0,
				Bid:       c.Low,
				Ask:       c.High,
				TickCount: int(c.Volume / 10),
				Color:     color,
			}
			for _, strat := range tb.activeStrategies {
				strat.OnCandleClose(candle, symbol)
			}
		}
		return
	}

	// 2. Fallback to Zerodha API if local database has no candles
	tb.logger.Warn("Local database has no candles for catch-up. Falling back to Zerodha API.", map[string]interface{}{"symbol": symbol})
	time.Sleep(340 * time.Millisecond) // Respect rate limits
	candles, err := tb.kiteClient.GetHistoricalData(int(token), "5minute", today0915, now, false, false)
	if err != nil {
		tb.logger.Error("Failed to fetch historical candles for catch-up from Kite", map[string]interface{}{"error": err.Error(), "symbol": symbol})
		return
	}

	// Persist caught-up candles to database to protect API limits on future restarts today
	if err := tb.db.SaveHistoricalCandles(tb.ctx, token, candles, "candles_5m"); err != nil {
		tb.logger.Error("Failed to save catch-up historical candles to database", map[string]interface{}{"error": err.Error(), "symbol": symbol})
	} else {
		tb.logger.Info("Saved catch-up historical candles to database", map[string]interface{}{"symbol": symbol, "count": len(candles)})
	}

	for _, c := range candles {
		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}

		candle := &data.Candle{
			Token:     token,
			Time:      c.Date.Time,
			Open:      c.Open,
			High:      c.High,
			Low:       c.Low,
			Close:     c.Close,
			Volume:    int64(c.Volume),
			VWAP:      (c.Open + c.High + c.Low + c.Close) / 4.0,
			Bid:       c.Low,
			Ask:       c.High,
			TickCount: int(c.Volume / 10),
			Color:     color,
		}
		for _, strat := range tb.activeStrategies {
			strat.OnCandleClose(candle, symbol)
		}
	}
}

// hardSquareOff closes all active positions and cancels pending orders
func (tb *TradingBot) hardSquareOff() {
	tb.logger.Warn("[LOW_VOLUME] Executing 03:15:00 PM hard square-off override...", nil)

	positions := tb.riskMgr.GetOpenPositions()
	for orderID, pos := range positions {
		tb.execMgr.CancelOrder(orderID)

		tick := tb.ticker.GetLatestTick(pos.Token)
		var exitPrice float64
		if tick != nil {
			exitPrice = tick.LTP
		} else {
			exitPrice = pos.LatestPrice
		}

		tb.riskMgr.OnOrderClose(orderID, exitPrice, pos.Quantity)
	}

	tb.logger.Info("[LOW_VOLUME] Hard square-off complete. Exposure is zero.", nil)
}

// queryPreviousDayHighLow retrieves high and low of a stock for the previous trading day
func (tb *TradingBot) queryPreviousDayHighLow(token int64, loc *time.Location) (float64, float64, time.Time, error) {
	// Find the most recent day where we have candles in DB prior to today
	nowIST := time.Now().In(loc)
	todayStart := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 0, 0, 0, 0, loc).UTC()

	lastTime, err := tb.db.GetLastCandleTimeBefore(tb.ctx, token, todayStart)
	if err != nil || lastTime.IsZero() {
		return 0, 0, time.Time{}, fmt.Errorf("no historical date found for token %d: %w", token, err)
	}

	// The start and end of that previous trading day
	lastTimeIST := lastTime.In(loc)
	prevDayStart := time.Date(lastTimeIST.Year(), lastTimeIST.Month(), lastTimeIST.Day(), 0, 0, 0, 0, loc).UTC()
	prevDayEnd := time.Date(lastTimeIST.Year(), lastTimeIST.Month(), lastTimeIST.Day(), 23, 59, 59, 0, loc).UTC()

	high, low, err := tb.db.GetPreviousDayHighLow(tb.ctx, token, prevDayStart, prevDayEnd)
	if err != nil {
		return 0, 0, lastTimeIST, fmt.Errorf("failed to scan high/low: %w", err)
	}

	return high, low, lastTimeIST, nil
}

// fetchAndStorePreviousDayCandles searches backwards for the last active trading day,
// fetches its 5-minute candles from Zerodha, and saves them to the DB.
func (tb *TradingBot) fetchAndStorePreviousDayCandles(token int64, symbol string, loc *time.Location) error {
	nowIST := time.Now().In(loc)
	// Start searching from yesterday
	d := nowIST.AddDate(0, 0, -1)

	// Go back up to 7 days to find the last valid trading session (to cover long holidays/weekends)
	for i := 0; i < 7; i++ {
		// Skip weekends
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			d = d.AddDate(0, 0, -1)
			continue
		}

		startD := time.Date(d.Year(), d.Month(), d.Day(), 9, 15, 0, 0, loc)
		endD := time.Date(d.Year(), d.Month(), d.Day(), 15, 30, 0, 0, loc)

		tb.logger.Info("Attempting to fetch historical candles from Zerodha Kite for previous day resolution", map[string]interface{}{
			"symbol": symbol,
			"date":   d.Format("2006-01-02"),
		})

		candles, err := tb.kiteClient.GetHistoricalData(int(token), "5minute", startD, endD, false, false)
		if err != nil {
			// If we hit an API rate limit or other connection error, go back
			d = d.AddDate(0, 0, -1)
			continue
		}

		if len(candles) > 0 {
			// Found the last active trading session!
			tb.logger.Info("Found previous trading day data on Zerodha. Storing to database...", map[string]interface{}{
				"symbol":        symbol,
				"date":          d.Format("2006-01-02"),
				"candles_count": len(candles),
			})

			// Save to database
			err = tb.db.SaveHistoricalCandles(tb.ctx, token, candles, "candles_5m")
			if err != nil {
				return fmt.Errorf("failed to save historical candles to database: %w", err)
			}
			return nil
		}

		// If no candles were returned, this was probably a market holiday. Go back one day.
		d = d.AddDate(0, 0, -1)
	}

	return fmt.Errorf("could not find any active historical trading candles on Zerodha in the last 7 days for token %d", token)
}

// resolvePreviousDayHighLow retrieves high/low for a token, fetching it from Zerodha first if not in database or stale
func (tb *TradingBot) resolvePreviousDayHighLow(token int64, symbol string, loc *time.Location) (float64, float64, error) {
	high, low, lastDate, err := tb.queryPreviousDayHighLow(token, loc)
	
	// Determine the expected previous trading day (skipping weekends)
	nowIST := time.Now().In(loc)
	d := nowIST.AddDate(0, 0, -1)
	var expectedPrevDay time.Time
	for i := 0; i < 7; i++ {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			d = d.AddDate(0, 0, -1)
			continue
		}
		expectedPrevDay = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
		break
	}

	// If data in DB is from the expected previous day, we are good!
	if err == nil && high > 0 && low > 0 && !lastDate.Before(expectedPrevDay) {
		return high, low, nil
	}

	// Not in database or stale, fetch from Zerodha
	tb.logger.Warn("Historical candles not found or stale in database. Fetching from Zerodha...", map[string]interface{}{
		"symbol": symbol,
	})

	if err := tb.fetchAndStorePreviousDayCandles(token, symbol, loc); err != nil {
		return 0, 0, fmt.Errorf("failed to fetch and store previous day candles: %w", err)
	}

	// Re-query database now that we stored the candles
	high, low, _, err = tb.queryPreviousDayHighLow(token, loc)
	return high, low, err
}

// cacheWatchlistLeverage queries dynamic order margins from Zerodha for the watchlist symbols and caches their leverage factor.
func (tb *TradingBot) cacheWatchlistLeverage(symbols []string) {
	if len(symbols) == 0 {
		return
	}

	params := make([]kiteconnect.OrderMarginParam, 0, len(symbols))
	symbolPrices := make(map[string]float64)

	for _, symbol := range symbols {
		price := 500.0 // default fallback price
		token, err := tb.securityMaster.GetInstrumentToken(symbol)
		if err == nil {
			loc, _ := time.LoadLocation("Asia/Kolkata")
			if loc == nil {
				loc = time.Local
			}
			high, low, _, err := tb.queryPreviousDayHighLow(token, loc)
			if err == nil && high > 0 {
				price = (high + low) / 2.0
			}
		}

		symbolPrices[symbol] = price

		params = append(params, kiteconnect.OrderMarginParam{
			Exchange:        "NSE",
			Tradingsymbol:   symbol,
			TransactionType: "BUY",
			Variety:         "regular",
			Product:         "MIS",
			OrderType:       "MARKET",
			Quantity:        1,
			Price:           price,
		})
	}

	tb.logger.Info("Batch querying order margins from Zerodha for leverage caching...", map[string]interface{}{
		"symbols_count": len(symbols),
	})

	margins, err := tb.kiteClient.GetOrderMargins(kiteconnect.GetMarginParams{
		OrderParams: params,
	})
	if err != nil {
		tb.logger.Error("Failed to batch fetch order margins, using default 5x leverage fallback", map[string]interface{}{"error": err.Error()})
		tb.leverageMutex.Lock()
		for _, symbol := range symbols {
			tb.watchlistLeverage[symbol] = 5.0
		}
		tb.leverageMutex.Unlock()
		return
	}

	tb.leverageMutex.Lock()
	defer tb.leverageMutex.Unlock()

	for i, m := range margins {
		symbol := symbols[i]
		price := symbolPrices[symbol]
		margin := m.Total

		if margin > 0 {
			leverage := price / margin
			if leverage > 0 {
				tb.watchlistLeverage[symbol] = leverage
				tb.logger.Info("Cached stock leverage factor", map[string]interface{}{
					"symbol":   symbol,
					"price":    price,
					"margin":   margin,
					"leverage": leverage,
				})
				continue
			}
		}
		tb.watchlistLeverage[symbol] = 5.0
	}
}

// runEquityVolumeGainersPreSelection runs the 3-stage predictive selection algorithm and saves results
func (tb *TradingBot) runEquityVolumeGainersPreSelection(loc *time.Location, ruleSet string) error {
	tb.logger.Info(fmt.Sprintf("Starting Equity Volume Gainers pre-selection algorithm for %s...", ruleSet), nil)

	ctx := tb.ctx
	kc := tb.kiteClient

	// 1. Fetch active NSE instruments
	instruments, err := kc.GetInstrumentsByExchange("NSE")
	if err != nil {
		return fmt.Errorf("exchange discovery failed: %v", err)
	}

	universe := make(map[string]int)
	for _, inst := range instruments {
		if inst.Segment == "NSE" && inst.InstrumentType == "EQ" {
			if !strings.HasSuffix(inst.Tradingsymbol, "-BE") && !strings.HasSuffix(inst.Tradingsymbol, "-BZ") {
				universe[inst.Tradingsymbol] = inst.InstrumentToken
			}
		}
	}

	// 2. Load active F&O stock list
	foStocks, err := tb.securityMaster.GetFOStocks(ctx)
	if err != nil {
		tb.logger.Warn("Failed to fetch F&O stock list. Continuing with manual/liquid stocks only.", map[string]interface{}{"error": err.Error()})
	}

	// 3. Load liquid cash stock list from database cache
	var liquidStocks map[string]int64
	cachedLiquid, cErr := tb.db.GetMetadataCache(ctx, "liquid:stocks", time.Now().Add(-24*time.Hour))
	if cErr == nil {
		_ = json.Unmarshal([]byte(cachedLiquid), &liquidStocks)
	}
	if len(liquidStocks) == 0 {
		tb.logger.Warn("Liquid cash stocks cache not found or stale.", nil)
	}
	tb.logger.Info("Loaded universe for pre-selection", map[string]interface{}{"liquid_cash_count": len(liquidStocks), "fo_count": len(foStocks)})

	// Combine into a master symbol list
	masterSymbols := make(map[string]int64)
	for sym, token := range foStocks {
		masterSymbols[sym] = token
	}
	for sym, token := range liquidStocks {
		masterSymbols[sym] = token
	}

	var rawSymbols []string
	for sym := range masterSymbols {
		rawSymbols = append(rawSymbols, "NSE:"+sym)
	}

	tb.logger.Info("Fetching pre-open quotes for symbols in bulk batches...", map[string]interface{}{"count": len(rawSymbols)})
	
	// Query GetQuote in batches of 400
	quotesMap := make(kiteconnect.Quote)
	batchSize := 400
	for i := 0; i < len(rawSymbols); i += batchSize {
		end := i + batchSize
		if end > len(rawSymbols) {
			end = len(rawSymbols)
		}
		batch := rawSymbols[i:end]
		quotes, qErr := kc.GetQuote(batch...)
		if qErr != nil {
			tb.logger.Error("Failed to fetch quotes batch", map[string]interface{}{"error": qErr.Error(), "start": i})
			continue
		}
		for k, v := range quotes {
			quotesMap[k] = v
		}
		time.Sleep(340 * time.Millisecond)
	}

	tb.logger.Info("Successfully fetched quotes. Filtering candidates...", map[string]interface{}{"quotes_count": len(quotesMap)})

	// Now filter symbols down to the ones with active pre-open volume/gaps
	type Candidate struct {
		Symbol         string
		Token          int64
		LTP            float64
		Volume         int64
		GapPct         float64
		ImbalanceRatio float64
		Priority       float64 // Sort priority for historical analysis
	}

	var candidates []Candidate
	for key, q := range quotesMap {
		symbol := strings.TrimPrefix(key, "NSE:")
		token := masterSymbols[symbol]
		if token == 0 {
			continue
		}

		// Filter out penny stocks and extremely expensive stocks
		if q.LastPrice < 50.0 || q.LastPrice > 5000.0 {
			continue
		}

		// Calculate gap relative to yesterday's close
		yesterdayClose := q.OHLC.Close
		if yesterdayClose == 0 {
			yesterdayClose = q.LastPrice
		}
		gapPct := ((q.LastPrice - yesterdayClose) / yesterdayClose) * 100.0

		// Calculate pre-open buy/sell imbalance ratio
		var totalBuyQty, totalSellQty float64
		for _, bid := range q.Depth.Buy {
			totalBuyQty += float64(bid.Quantity)
		}
		for _, ask := range q.Depth.Sell {
			totalSellQty += float64(ask.Quantity)
		}
		if totalSellQty == 0 {
			totalSellQty = 1.0
		}
		imbalanceRatio := totalBuyQty / totalSellQty

		// Check if there is active volume or a gap
		// Filter: pre-open volume must be > 1000 shares OR gap must be > 0.5%
		if q.Volume > 1000 || math.Abs(gapPct) >= 0.5 {
			// Higher volume and higher gap gives higher priority to be screened
			priority := (float64(q.Volume) / 10000.0) + (math.Abs(gapPct) * 10.0)
			candidates = append(candidates, Candidate{
				Symbol:         symbol,
				Token:          token,
				LTP:            q.LastPrice,
				Volume:         int64(q.Volume),
				GapPct:         gapPct,
				ImbalanceRatio: imbalanceRatio,
				Priority:       priority,
			})
		}
	}

	// Sort candidates by priority desc
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Priority > candidates[j].Priority
	})

	// Limit historical analysis to the top 100 candidates to respect time and rate limits at 09:07 AM
	maxScreenCandidates := 100
	if len(candidates) < maxScreenCandidates {
		maxScreenCandidates = len(candidates)
	}
	screenPool := candidates[:maxScreenCandidates]

	tb.logger.Info("Selected candidates for EOD setup checks", map[string]interface{}{"count": len(screenPool)})

	// 4. Batch query yesterday's close and ADV (Average Daily Volume) from database cache
	setups := make(map[string]selection.HistoricalSetup)
	signals := make(map[string]selection.LivePreOpenSignal)
	advMap := make(map[string]float64)
	closeMap := make(map[string]float64)

	for _, cand := range screenPool {
		// Calculate EOD daily setup
		candles, err := tb.fetchHistoricalEODForPreSelection(int(cand.Token), loc)
		if err != nil || len(candles) < 5 {
			continue
		}

		// Sleep briefly to respect API rate limits (3 requests per second limit)
		time.Sleep(340 * time.Millisecond)

		n := len(candles)
		t1Candle := candles[n-1]

		var totalVol float64
		volPeriod := 20
		if n < 20 {
			volPeriod = n
		}
		for i := n - volPeriod; i < n; i++ {
			totalVol += float64(candles[i].Volume)
		}
		adv := totalVol / float64(volPeriod)
		if adv == 0 {
			continue
		}

		volMultiplier := float64(cand.Volume) / adv
		isVolDried := float64(candles[n-2].Volume) < (adv * 0.75)

		var priceSum float64
		pricePeriod := 5
		if n < 5 {
			pricePeriod = n
		}
		for i := n - pricePeriod; i < n; i++ {
			priceSum += candles[i].Close
		}
		meanPrice5d := priceSum / float64(pricePeriod)

		var varianceSum float64
		for i := n - pricePeriod; i < n; i++ {
			varianceSum += math.Pow(candles[i].Close-meanPrice5d, 2)
		}
		stdDev5d := math.Sqrt(varianceSum / float64(pricePeriod))
		compressionRatio := (stdDev5d / meanPrice5d) * 100
		isCompressed := compressionRatio < 1.6

		ema5 := selection.CalculateInlineEMA(candles, 5)
		ema20 := selection.CalculateInlineEMA(candles, 20)
		ema50 := selection.CalculateInlineEMA(candles, 50)

		emas := []float64{ema5, ema20, ema50}
		sort.Float64s(emas)
		emaSpread := ((emas[2] - emas[0]) / emas[0]) * 100
		emaConverged := emaSpread < 1.5

		setups[cand.Symbol] = selection.HistoricalSetup{
			IsCompressed:  isCompressed,
			EmaConverged:  emaConverged,
			IsVolDried:    isVolDried,
			LastClose:     t1Candle.Close,
			HistoricalADV: adv,
			VolMultiplier: volMultiplier,
		}
		advMap[cand.Symbol] = adv
		closeMap[cand.Symbol] = t1Candle.Close

		signals[cand.Symbol] = selection.LivePreOpenSignal{
			ImbalanceRatio:   cand.ImbalanceRatio,
			IndicativeGapPct: cand.GapPct,
			PreOpenVolVsADV:  float64(cand.Volume) / adv,
		}
	}

	tb.logger.Info("Aggregated EOD data for pre-selection", map[string]interface{}{"count": len(setups)})

	sessionDateStr := time.Now().In(loc).Format("2006-01-02")
	dbPredictions := make([]data.PreSelectionResult, 0)

	if ruleSet == "STANDARD" {
		// Run standard predictions
		predictionsStd := selection.PredictMarketOpen(setups, signals)
		for _, pred := range predictionsStd {
			dbPredictions = append(dbPredictions, data.PreSelectionResult{
				Date:               sessionDateStr,
				Ticker:             pred.Ticker,
				RuleSet:            "STANDARD",
				PredictedDirection: pred.PredictedDirection,
				ImbalanceRatio:     pred.ImbalanceRatio,
				IndicativeGapPct:   pred.IndicativeGapPct,
				PreOpenVolVsADV:    pred.PreOpenVolVsADV,
				ProbabilityScore:   pred.ProbabilityScore,
				Reason:             pred.Reason,
			})
		}
	} else if ruleSet == "ADJUSTED" {
		// Run adjusted predictions
		predictionsAdj := selection.PredictMarketOpenAdjusted(setups, signals)
		for _, pred := range predictionsAdj {
			dbPredictions = append(dbPredictions, data.PreSelectionResult{
				Date:               sessionDateStr,
				Ticker:             pred.Ticker,
				RuleSet:            "ADJUSTED",
				PredictedDirection: pred.PredictedDirection,
				ImbalanceRatio:     pred.ImbalanceRatio,
				IndicativeGapPct:   pred.IndicativeGapPct,
				PreOpenVolVsADV:    pred.PreOpenVolVsADV,
				ProbabilityScore:   pred.ProbabilityScore,
				Reason:             pred.Reason,
			})
		}
	}

	if err := tb.db.SavePreSelectionResults(dbPredictions); err != nil {
		return fmt.Errorf("failed to save prediction results: %v", err)
	}

	tb.logger.Info("Saved prediction results to database", map[string]interface{}{"rule_set": ruleSet, "count": len(dbPredictions)})
	return nil
}

// fetchHistoricalEODForPreSelection gets EOD candles from DB daily aggregations or dynamic API fallback
func (tb *TradingBot) fetchHistoricalEODForPreSelection(token int, loc *time.Location) ([]kiteconnect.HistoricalData, error) {
	candles, err := tb.db.GetHistoricalAggregatedCandles(int64(token))
	if err == nil && len(candles) >= 5 {
		return candles, nil
	}

	toTime := time.Now()
	fromTime := toTime.AddDate(0, 0, -60)
	candles, err = tb.kiteClient.GetHistoricalData(token, "day", fromTime, toTime, false, false)
	return candles, err
}
