package main

import (
	"encoding/json"
	"fmt"
	"time"

	"zerodha-trading/data"
	"zerodha-trading/strategy"
)

// runLOWVOLUMEStrategyScheduler schedules strategy actions for the day
func (tb *TradingBot) runLOWVOLUMEStrategyScheduler(loc *time.Location) {
	defer tb.wg.Done()

	tb.logger.Info("[LOW_VOLUME] Strategy scheduler loop started", nil)

	selectHour, selectMin, err := parseTimeHM(tb.cfg.StockSelectTime)
	if err != nil {
		selectHour, selectMin = 9, 30
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	breadthLogged := false
	watchlistFiltered := false
	hardSquareOffDone := false

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
		tb.logger.Info("[LOW_VOLUME] Using manual daily watchlist from database", map[string]interface{}{
			"symbols": manualWatchlist,
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
			if err == nil && token > 0 {
				tb.watchlist[symbol] = token
				selectedTokens = append(selectedTokens, token)
			} else {
				tb.logger.Warn("Failed to resolve token for manual watchlist symbol", map[string]interface{}{"symbol": symbol})
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
						high, low, err := tb.queryPreviousDayHighLow(token, loc)
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
			wList, err := selector.SelectStocks(tb.ctx, tb.logger.Logger, tb.kiteClient, tb.securityMaster, tb.globalBias, tb.cfg.WatchlistSize, tb.cfg.WatchlistMaxPctChange)
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
						high, low, err := tb.queryPreviousDayHighLow(token, loc)
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

	tb.logger.Info("[LOW_VOLUME] Catching up historical 5-minute candles...", map[string]interface{}{
		"symbol": symbol,
		"from":   today0915,
	})

	candles, err := tb.kiteClient.GetHistoricalData(int(token), "5minute", today0915, now, false, false)
	if err != nil {
		tb.logger.Error("Failed to fetch historical candles for catch-up", map[string]interface{}{"error": err.Error(), "symbol": symbol})
		return
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
func (tb *TradingBot) queryPreviousDayHighLow(token int64, loc *time.Location) (float64, float64, error) {
	// Find the most recent day where we have candles in DB prior to today
	nowIST := time.Now().In(loc)
	todayStart := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 0, 0, 0, 0, loc).UTC()

	lastTime, err := tb.db.GetLastCandleTimeBefore(tb.ctx, token, todayStart)
	if err != nil || lastTime.IsZero() {
		return 0, 0, fmt.Errorf("no historical date found for token %d: %w", token, err)
	}

	// The start and end of that previous trading day
	lastTimeIST := lastTime.In(loc)
	prevDayStart := time.Date(lastTimeIST.Year(), lastTimeIST.Month(), lastTimeIST.Day(), 0, 0, 0, 0, loc).UTC()
	prevDayEnd := time.Date(lastTimeIST.Year(), lastTimeIST.Month(), lastTimeIST.Day(), 23, 59, 59, 0, loc).UTC()

	high, low, err := tb.db.GetPreviousDayHighLow(tb.ctx, token, prevDayStart, prevDayEnd)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to scan high/low: %w", err)
	}

	return high, low, nil
}
