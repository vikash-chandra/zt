package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"zerodha-trading/data"
	"zerodha-trading/execution"
	"zerodha-trading/selection"
	"zerodha-trading/strategy"
)

// runDailyStrategyScheduler schedules strategy actions for the day
func (tb *TradingBot) runDailyStrategyScheduler(loc *time.Location) {
	defer tb.wg.Done()

	tb.logger.Info("Daily Strategy scheduler loop started", nil)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	breadthLogged := false
	watchlistFiltered := false
	hardSquareOffDone := false
	broadEndDone := false

	for {
		select {
		case <-tb.ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().In(loc)
			hour := now.Hour()
			minute := now.Minute()
			second := now.Second()

			selectHour, selectMin, selectSec, err := data.ParseTimeHMS(tb.cfg.StockSelectTime)
			if err != nil {
				selectHour, selectMin, selectSec = 9, 0, 0
			}

			sqHour, sqMin, sqSec, err := data.ParseTimeHMS(tb.cfg.AutoSquareOffTime)
			if err != nil {
				sqHour, sqMin, sqSec = 15, 20, 0
			}

			selectBoundary := time.Date(now.Year(), now.Month(), now.Day(), selectHour, selectMin, selectSec, 0, loc)
			breadthBoundary := selectBoundary.Add(-1 * time.Minute)
			sqBoundary := time.Date(now.Year(), now.Month(), now.Day(), sqHour, sqMin, sqSec, 0, loc)

			// 1. Step 1: Pre-market breadth logging (1 minute before stock selection time)
			if !breadthLogged && !now.Before(breadthBoundary) && now.Hour() < 15 {
				tb.logger.Info(fmt.Sprintf("[EQUITY] Triggering %02d:%02d:%02d pre-market breadth calculations...", breadthBoundary.Hour(), breadthBoundary.Minute(), breadthBoundary.Second()), nil)
				if err := tb.logMarketBreadth(loc); err != nil {
					tb.logger.Error("Failed to run pre-market breadth check", map[string]interface{}{"error": err.Error()})
				}
				breadthLogged = true
			}

			// 2. Step 2: Dynamic Stock Selection Filter (exactly at stock selection time)
			if !watchlistFiltered && breadthLogged && !now.Before(selectBoundary) && now.Hour() < 15 {
				tb.logger.Info(fmt.Sprintf("[EQUITY] Triggering %02d:%02d:%02d dynamic watchlist filter...", selectHour, selectMin, selectSec), nil)
				if err := tb.selectWatchlist(loc, true); err != nil {
					tb.logger.Error("Failed to resolve dynamic watchlist selection", map[string]interface{}{"error": err.Error()})
				} else {
					watchlistFiltered = true
				}
			}

			// 3. Step 3: Lean WebSocket Subscription Transition at MorningBroadAggEnd (default: 09:45:00 IST)
			broadEndH, broadEndM, broadEndS, errBroadEnd := data.ParseTimeHMS(tb.cfg.MorningBroadAggEnd)
			if errBroadEnd != nil {
				broadEndH, broadEndM, broadEndS = 9, 45, 0
			}
			broadEndBoundary := time.Date(now.Year(), now.Month(), now.Day(), broadEndH, broadEndM, broadEndS, 0, loc)
			if !broadEndDone && !now.Before(broadEndBoundary) && now.Hour() < 15 {
				tb.logger.Info(fmt.Sprintf("[TICKER] Morning broad aggregation window ended at %02d:%02d:%02d IST. Unsubscribing non-watchlist instruments...", broadEndH, broadEndM, broadEndS), nil)
				tb.trimToActiveWatchlistSubscriptions()
				broadEndDone = true
			}

			// 4. Step 4: Hard Square-off Override (EOD)
			if !hardSquareOffDone && !now.Before(sqBoundary) {
				tb.logger.Info(fmt.Sprintf("[EQUITY] Triggering %02d:%02d:%02d hard square-off override...", sqHour, sqMin, sqSec), nil)
				tb.hardSquareOff()
				hardSquareOffDone = true
			}

			// Check options specific EOD auto square-off
			optSqH, optSqM, optSqS, errOptSq := data.ParseTimeHMS(tb.cfg.Options.AutoSquareOffTime)
			if errOptSq != nil {
				optSqH, optSqM, optSqS = 15, 15, 0
			}
			optSqBoundary := time.Date(now.Year(), now.Month(), now.Day(), optSqH, optSqM, optSqS, 0, loc)
			if !now.Before(optSqBoundary) && tb.optionsPosMgr != nil && tb.optionsPosMgr.GetActivePosition() != nil {
				tb.logger.Info(fmt.Sprintf("[OPTIONS] Triggering %02d:%02d:%02d auto square-off...", optSqH, optSqM, optSqS), nil)
				tb.hardSquareOffOptions()
			}

			// Reset daily state at midnight
			if hour == 0 && minute == 0 && second == 0 {
				breadthLogged = false
				watchlistFiltered = false
				hardSquareOffDone = false
				broadEndDone = false
				tb.setAutoSelectionDone(false)
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
	var ohlcData map[string]data.OHLCQuote
	if tb.kiteClient != nil {
		for attempt := 1; attempt <= 3; attempt++ {
			ohlcData, err = tb.kiteClient.GetOHLC(keys...)
			if err == nil && len(ohlcData) > 0 {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	if err != nil && len(ohlcData) == 0 {
		tb.logger.Warn("Failed to fetch Nifty 50 OHLC snapshot from Zerodha, defaulting global bias to BUY_ONLY", map[string]interface{}{"error": err.Error()})
		tb.globalBias = "BUY_ONLY"
		return nil
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

	tb.globalBias = "SELL_ONLY"
	if advances >= declines {
		tb.globalBias = "BUY_ONLY"
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
func (tb *TradingBot) selectWatchlist(loc *time.Location, force bool) error {
	if tb.globalBias == "" {
		_ = tb.logMarketBreadth(loc)
	}
	if tb.globalBias == "NO_TRADE" {
		tb.logger.Info("Global bias is NO_TRADE. Skipping watchlist dynamic selection.", map[string]interface{}{"bias": tb.globalBias})
		return nil
	}
	if tb.globalBias == "" {
		tb.globalBias = "BUY_ONLY"
	}

	todayStr := data.GetEffectiveTradingDate(time.Now())
	dbItems, errDb := tb.db.GetDailyWatchlist(tb.ctx, todayStr)
	hasAutomatedSelections := false
	if !force && errDb == nil && len(dbItems) > 0 {
		for _, item := range dbItems {
			if item.Selectors != "" && !strings.Contains(item.Selectors, "MANUAL") && item.Selectors != "MA" {
				hasAutomatedSelections = true
				break
			}
		}
	}

	if hasAutomatedSelections {
		tb.logger.Info("Found existing automated daily watchlist in database. Reconstructing state...", map[string]interface{}{
			"count": len(dbItems),
		})

		tb.watchlistMutex.Lock()
		tb.watchlist = make(map[string]int64)
		for _, strat := range tb.activeStrategies {
			tb.strategyWatchlists[strat.Name()] = make(map[string]int64)
		}

		var selectedTokens []int64
		tokenSet := make(map[int64]bool)

		for _, item := range dbItems {
			tb.watchlist[item.Symbol] = item.Token
			if !tokenSet[item.Token] {
				tokenSet[item.Token] = true
				selectedTokens = append(selectedTokens, item.Token)
			}

			isManual := strings.Contains(item.Selectors, "MANUAL") || strings.Contains(item.Selectors, "MA")
			if isManual {
				for _, strat := range tb.activeStrategies {
					if wList, ok := tb.strategyWatchlists[strat.Name()]; ok {
						wList[item.Symbol] = item.Token
					}
				}
			}

			// Parse selectors, format: "LOW_VOLUME:SECURITIES_FO,VANDE_BHARAT:SECTORAL"
			if item.Selectors != "" {
				parts := strings.Split(item.Selectors, ",")
				for _, part := range parts {
					subParts := strings.Split(part, ":")
					if len(subParts) >= 1 {
						stratName := subParts[0]
						if wList, ok := tb.strategyWatchlists[stratName]; ok {
							wList[item.Symbol] = item.Token
						}
					}
				}
			}
		}

		// Enforce directional bias
		tb.watchlistDirectionsMutex.Lock()
		tb.watchlistDirections = make(map[string]string)
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

		// Re-bind PDH/PDL for active strategies
		for _, strat := range tb.activeStrategies {
			if vbEngine, isVB := strat.(*strategy.VandeBharatEngine); isVB {
				tb.watchlistMutex.RLock()
				wList := tb.strategyWatchlists[strat.Name()]
				tb.watchlistMutex.RUnlock()

				for symbol, token := range wList {
					high, low, closeVal, err := tb.resolvePreviousDayHighLow(token, symbol, loc)
					if err != nil {
						tb.logger.Error("Failed to query previous day high/low for DB watchlist, using default fallback", map[string]interface{}{
							"symbol": symbol,
							"error":  err.Error(),
						})
						high, low, closeVal = 0.0, 0.0, 0.0
					}
					_, shiftPct := tb.resolveSymbolSelectorAndShift(symbol)
					shiftedHigh := selection.CalculateLevelShiftedPrice(high, shiftPct, 0.05)
					shiftedLow := selection.CalculateLevelShiftedPrice(low, shiftPct, 0.05)
					vbEngine.SetPreviousDayLevels(symbol, shiftedHigh, shiftedLow, closeVal)
				}
			} else if vbtEngine, isVBT := strat.(*strategy.VandeBharatTrapEngine); isVBT {
				tb.watchlistMutex.RLock()
				wList := tb.strategyWatchlists[strat.Name()]
				tb.watchlistMutex.RUnlock()

				for symbol, token := range wList {
					high, low, closeVal, err := tb.resolvePreviousDayHighLow(token, symbol, loc)
					if err != nil {
						tb.logger.Error("Failed to query previous day high/low for DB watchlist, using default fallback", map[string]interface{}{
							"symbol": symbol,
							"error":  err.Error(),
						})
						high, low, closeVal = 0.0, 0.0, 0.0
					}
					_, shiftPct := tb.resolveSymbolSelectorAndShift(symbol)
					shiftedHigh := selection.CalculateLevelShiftedPrice(high, shiftPct, 0.05)
					shiftedLow := selection.CalculateLevelShiftedPrice(low, shiftPct, 0.05)
					vbtEngine.SetPreviousDayLevels(symbol, shiftedHigh, shiftedLow, closeVal)
				}
			} else if es5Engine, isES5 := strat.(*strategy.EMAS5BreakoutEngine); isES5 {
				tb.watchlistMutex.RLock()
				wList := tb.strategyWatchlists[strat.Name()]
				tb.watchlistMutex.RUnlock()

				for symbol, token := range wList {
					high, low, closeVal, err := tb.resolvePreviousDayHighLow(token, symbol, loc)
					if err != nil {
						tb.logger.Error("Failed to query previous day high/low for DB watchlist, using default fallback", map[string]interface{}{
							"symbol": symbol,
							"error":  err.Error(),
						})
						high, low, closeVal = 0.0, 0.0, 0.0
					}
					_, shiftPct := tb.resolveSymbolSelectorAndShift(symbol)
					shiftedHigh := selection.CalculateLevelShiftedPrice(high, shiftPct, 0.05)
					shiftedLow := selection.CalculateLevelShiftedPrice(low, shiftPct, 0.05)
					es5Engine.SetPreviousDayLevels(symbol, shiftedHigh, shiftedLow, closeVal)
				}
			} else if lvEngine, isLV := strat.(*strategy.LowVolumeEngine); isLV {
				tb.watchlistMutex.RLock()
				wList := tb.strategyWatchlists[strat.Name()]
				tb.watchlistMutex.RUnlock()

				for symbol, token := range wList {
					high, low, _, err := tb.resolvePreviousDayHighLow(token, symbol, loc)
					if err != nil {
						tb.logger.Error("Failed to query previous day high/low for DB watchlist, using default fallback", map[string]interface{}{
							"symbol": symbol,
							"error":  err.Error(),
						})
						high, low = 0.0, 0.0
					}
					_, shiftPct := tb.resolveSymbolSelectorAndShift(symbol)
					shiftedHigh := selection.CalculateLevelShiftedPrice(high, shiftPct, 0.05)
					shiftedLow := selection.CalculateLevelShiftedPrice(low, shiftPct, 0.05)
					lvEngine.SetPreviousDayHighLow(symbol, shiftedHigh, shiftedLow)
				}
			}
		}

		// Re-subscribe websockets
		if tb.ticker != nil && len(selectedTokens) > 0 {
			go func() {
				// Wait for ticker connection
				for i := 0; i < 10; i++ {
					if tb.ticker.IsConnected() {
						break
					}
					time.Sleep(1 * time.Second)
				}
				tb.ticker.Subscribe(selectedTokens)
				tb.logger.Info("Subscribed ticker to saved database watchlist tokens", map[string]interface{}{
					"count": len(selectedTokens),
				})
			}()
		}

		// Trigger catchup sequence asynchronously
		go func() {
			symbolsCopy := make(map[string]int64)
			tb.watchlistMutex.RLock()
			for sym, tok := range tb.watchlist {
				symbolsCopy[sym] = tok
			}
			tb.watchlistMutex.RUnlock()

			for sym, tok := range symbolsCopy {
				time.Sleep(350 * time.Millisecond)
				go tb.catchUpHistoricalCandles(sym, tok)
			}
		}()

		return nil
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

		selector := tb.activeSelectors[selectorName]
		if selector == nil {
			selector = tb.activeSelectors[selection.NormalizeSelectorName(selectorName)]
		}
		if selector == nil {
			selector = tb.activeSelectors["FO"]
		}
		if selector == nil {
			selector = tb.activeSelectors["SECURITIES_FO"]
		}
		if selector == nil {
			selector = selection.NewSecuritiesFOSelector()
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

			// Resolve and bind PDH & PDL values
			if vbEngine, isVB := strat.(*strategy.VandeBharatEngine); isVB {
				for symbol, token := range wList {
					high, low, closeVal, err := tb.resolvePreviousDayHighLow(token, symbol, loc)
					if err != nil {
						tb.logger.Error("Failed to query previous day high/low, using default fallback", map[string]interface{}{
							"symbol": symbol,
							"error":  err.Error(),
						})
						high, low, closeVal = 0.0, 0.0, 0.0
					}
					_, shiftPct := tb.resolveSymbolSelectorAndShift(symbol)
					shiftedHigh := selection.CalculateLevelShiftedPrice(high, shiftPct, 0.05)
					shiftedLow := selection.CalculateLevelShiftedPrice(low, shiftPct, 0.05)
					vbEngine.SetPreviousDayLevels(symbol, shiftedHigh, shiftedLow, closeVal)
				}
			} else if vbtEngine, isVBT := strat.(*strategy.VandeBharatTrapEngine); isVBT {
				for symbol, token := range wList {
					high, low, closeVal, err := tb.resolvePreviousDayHighLow(token, symbol, loc)
					if err != nil {
						tb.logger.Error("Failed to query previous day high/low, using default fallback", map[string]interface{}{
							"symbol": symbol,
							"error":  err.Error(),
						})
						high, low, closeVal = 0.0, 0.0, 0.0
					}
					_, shiftPct := tb.resolveSymbolSelectorAndShift(symbol)
					shiftedHigh := selection.CalculateLevelShiftedPrice(high, shiftPct, 0.05)
					shiftedLow := selection.CalculateLevelShiftedPrice(low, shiftPct, 0.05)
					vbtEngine.SetPreviousDayLevels(symbol, shiftedHigh, shiftedLow, closeVal)
				}
			} else if es5Engine, isES5 := strat.(*strategy.EMAS5BreakoutEngine); isES5 {
				for symbol, token := range wList {
					high, low, closeVal, err := tb.resolvePreviousDayHighLow(token, symbol, loc)
					if err != nil {
						tb.logger.Error("Failed to query previous day high/low, using default fallback", map[string]interface{}{
							"symbol": symbol,
							"error":  err.Error(),
						})
						high, low, closeVal = 0.0, 0.0, 0.0
					}
					_, shiftPct := tb.resolveSymbolSelectorAndShift(symbol)
					shiftedHigh := selection.CalculateLevelShiftedPrice(high, shiftPct, 0.05)
					shiftedLow := selection.CalculateLevelShiftedPrice(low, shiftPct, 0.05)
					es5Engine.SetPreviousDayLevels(symbol, shiftedHigh, shiftedLow, closeVal)
				}
			} else if lvEngine, isLV := strat.(*strategy.LowVolumeEngine); isLV {
				for symbol, token := range wList {
					high, low, _, err := tb.resolvePreviousDayHighLow(token, symbol, loc)
					if err != nil {
						tb.logger.Error("Failed to query previous day high/low, using default fallback", map[string]interface{}{
							"symbol": symbol,
							"error":  err.Error(),
						})
						high, low = 0.0, 0.0
					}
					_, shiftPct := tb.resolveSymbolSelectorAndShift(symbol)
					shiftedHigh := selection.CalculateLevelShiftedPrice(high, shiftPct, 0.05)
					shiftedLow := selection.CalculateLevelShiftedPrice(low, shiftPct, 0.05)
					lvEngine.SetPreviousDayHighLow(symbol, shiftedHigh, shiftedLow)
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

	// Run Sector Scanner calculation to populate selected_sectors table if enabled
	if tb.cfg.SectorScannerEnabled && tb.kiteClient != nil {
		secSelector := selection.NewSectoralSelector(tb.cfg, tb.db)
		_, _ = secSelector.SelectStocks(tb.ctx, tb.logger.Logger, tb.kiteClient, tb.securityMaster, tb.globalBias, tb.cfg.SectorScannerTopN, tb.cfg.WatchlistMaxPctChange)
	}

	// Merge manual watchlist symbols configured in database for today
	manualWatchlist, mErr := tb.db.GetDailyManualWatchlist(tb.ctx, time.Now().In(loc))
	if mErr == nil && len(manualWatchlist) > 0 {
		tb.logger.Info("Merging manual daily watchlist symbols into active strategy watchlists...", map[string]interface{}{"manual_symbols": manualWatchlist})
		for _, rawItem := range manualWatchlist {
			itemParts := strings.Split(rawItem, ":")
			symbol := strings.TrimSpace(itemParts[0])
			assignedSelector := "PDH_PDL"
			if len(itemParts) > 1 && itemParts[1] != "" {
				assignedSelector = selection.NormalizeSelectorName(itemParts[1])
			}
			if symbol == "" {
				continue
			}

			tb.watchlistSelectorMapMutex.Lock()
			tb.watchlistSelectorMap[symbol] = assignedSelector
			tb.watchlistSelectorMapMutex.Unlock()

			token, tErr := tb.securityMaster.GetInstrumentToken(symbol)
			if tErr != nil || token <= 0 {
				token, tErr = tb.db.ResolveSymbolToken(tb.ctx, symbol)
			}
			if tErr != nil || token <= 0 {
				token, tErr = tb.securityMaster.ResolveAndAddSymbol(tb.ctx, symbol)
			}
			if tErr == nil && token > 0 {
				tb.watchlist[symbol] = token
				if !tokenSet[token] {
					tokenSet[token] = true
					selectedTokens = append(selectedTokens, token)
				}
				for _, strat := range tb.activeStrategies {
					if tb.strategyWatchlists[strat.Name()] == nil {
						tb.strategyWatchlists[strat.Name()] = make(map[string]int64)
					}
					tb.strategyWatchlists[strat.Name()][symbol] = token
					high, low, closeVal, _ := tb.resolvePreviousDayHighLow(token, symbol, loc)
					_, shiftPct := tb.resolveSymbolSelectorAndShift(symbol)
					shiftedHigh := selection.CalculateLevelShiftedPrice(high, shiftPct, 0.05)
					shiftedLow := selection.CalculateLevelShiftedPrice(low, shiftPct, 0.05)
					if vbEngine, isVB := strat.(*strategy.VandeBharatEngine); isVB {
						vbEngine.SetPreviousDayLevels(symbol, shiftedHigh, shiftedLow, closeVal)
					} else if vbtEngine, isVBT := strat.(*strategy.VandeBharatTrapEngine); isVBT {
						vbtEngine.SetPreviousDayLevels(symbol, shiftedHigh, shiftedLow, closeVal)
					} else if es5Engine, isES5 := strat.(*strategy.EMAS5BreakoutEngine); isES5 {
						es5Engine.SetPreviousDayLevels(symbol, shiftedHigh, shiftedLow, closeVal)
					} else if lvEngine, isLV := strat.(*strategy.LowVolumeEngine); isLV {
						lvEngine.SetPreviousDayHighLow(symbol, shiftedHigh, shiftedLow)
					}
				}
			}
		}
	}

	// Populate directional bias for the selected watchlist symbols from database
	tb.watchlistDirectionsMutex.Lock()
	tb.watchlistDirections = make(map[string]string)
	todayStr = time.Now().In(loc).Format("2006-01-02")
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

	if tb.cfg.BroadSubscribe {
		var newTokens []int64
		for _, token := range selectedTokens {
			if !tb.isBroadSubscriptionToken(token) {
				newTokens = append(newTokens, token)
			}
		}
		if len(newTokens) > 0 {
			tb.logger.Info("Subscribing to new dynamic watchlist symbols not in broad subscription", map[string]interface{}{"count": len(newTokens)})
			_ = tb.ticker.Subscribe(newTokens)
		}
	} else {
		tb.logger.Info("Watchlist selection complete. Swapping WebSocket ticker subscriptions...", map[string]interface{}{"count": len(selectedTokens)})
		_ = tb.ticker.Close()
		time.Sleep(1 * time.Second)
		if err := tb.ticker.Connect(tb.ctx, selectedTokens); err != nil {
			return fmt.Errorf("failed to reconnect ticker to unified watchlist: %w", err)
		}
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
			time.Sleep(350 * time.Millisecond)
			go tb.catchUpHistoricalCandles(sym, tok)
		}
	}()

	// Save newly selected watchlist to database for persistence
	dbItems = []data.DailyWatchlistItem{}
	for symbol, token := range tb.watchlist {
		var selectors []string
		for stratName, wList := range tb.strategyWatchlists {
			if _, exists := wList[symbol]; exists {
				selectorName := tb.strategySelectorMap[stratName]
				if selectorName == "" {
					selectorName = "SECURITIES_FO"
				}
				selectors = append(selectors, fmt.Sprintf("%s:%s", stratName, selectorName))
			}
		}
		for _, rawItem := range manualWatchlist {
			mParts := strings.Split(rawItem, ":")
			mSym := strings.TrimSpace(mParts[0])
			if mSym == symbol {
				assigned := "MA"
				if len(mParts) > 1 && mParts[1] != "" {
					assigned = mParts[1]
				}
				selectors = append(selectors, "MANUAL:"+assigned)
				break
			}
		}
		dbItems = append(dbItems, data.DailyWatchlistItem{
			Date:      todayStr,
			Symbol:    symbol,
			Token:     token,
			Selectors: strings.Join(selectors, ","),
		})
	}
	if len(dbItems) > 0 {
		errSave := tb.db.SaveDailyWatchlist(tb.ctx, dbItems)
		if errSave != nil {
			tb.logger.Error("Failed to save daily watchlist to database", map[string]interface{}{"error": errSave.Error()})
		} else {
			tb.logger.Info("Successfully saved daily watchlist to database", map[string]interface{}{"count": len(dbItems)})
		}
	}

	tb.setAutoSelectionDone(true)

	return nil
}

var catchUpSem = make(chan struct{}, 1)

// catchUpHistoricalCandles retrieves historical candles since 09:15 AM for active strategies
func (tb *TradingBot) catchUpHistoricalCandles(symbol string, token int64) {
	nowIST := time.Now().In(data.ISTLocation)
	today0915 := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 9, 15, 0, 0, data.ISTLocation).UTC()

	now := time.Now().UTC()
	if now.Before(today0915) {
		return
	}

	// Group active strategies by their configured timeframe
	stratsByTF := make(map[string][]strategy.Strategy)
	for _, strat := range tb.activeStrategies {
		tf := strat.CandleTimeFrame()
		if tf == "" {
			tf = "5m"
		}
		stratsByTF[tf] = append(stratsByTF[tf], strat)
	}

	for tf, targetStrats := range stratsByTF {
		tb.catchUpCandlesForTimeframe(symbol, token, tf, targetStrats, today0915, nowIST)
	}
}

func (tb *TradingBot) catchUpCandlesForTimeframe(symbol string, token int64, tf string, targetStrats []strategy.Strategy, today0915 time.Time, nowIST time.Time) {
	tableName := "candles_5m"
	apiInterval := "5minute"
	intervalMin := 5
	if tf == "1m" {
		tableName = "candles_1m"
		apiInterval = "minute"
		intervalMin = 1
	}

	// Calculate expected number of completed candles since 09:15 AM IST
	marketOpenIST := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 9, 15, 0, 0, data.ISTLocation)
	marketCloseIST := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 15, 30, 0, 0, data.ISTLocation)
	effectiveNow := nowIST
	if effectiveNow.After(marketCloseIST) {
		effectiveNow = marketCloseIST
	}
	expectedCount := int(effectiveNow.Sub(marketOpenIST).Minutes() / float64(intervalMin))
	if expectedCount < 1 {
		expectedCount = 1
	}

	// 1. Try to catch up from local DB only if DB has at least the expected completed candles
	dbCandles, dbErr := tb.db.GetCandlesForDayFromTable(tb.ctx, tableName, token, today0915)
	if dbErr == nil && len(dbCandles) >= expectedCount {
		tb.logger.Info("Successfully caught up candles from local database", map[string]interface{}{"symbol": symbol, "timeframe": tf, "count": len(dbCandles)})
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
			for _, strat := range targetStrats {
				strat.OnCandleClose(candle, symbol)
			}
		}
		return
	}

	var candles []data.HistoricalData
	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			time.Sleep(2 * time.Second)
		}

		func() {
			catchUpSem <- struct{}{}
			defer func() {
				time.Sleep(350 * time.Millisecond)
				<-catchUpSem
			}()
			var apiErr error
			candles, apiErr = tb.kiteClient.GetHistoricalData(int(token), apiInterval, today0915, time.Now().UTC(), false, false)
			if apiErr != nil {
				tb.logger.Warn("Failed to fetch historical candles for catch-up from Kite", map[string]interface{}{"error": apiErr.Error(), "symbol": symbol, "timeframe": tf})
			}
		}()

		if len(candles) > 0 {
			tb.logger.Info("Successfully fetched catch-up candles from Zerodha API", map[string]interface{}{
				"symbol":    symbol,
				"timeframe": tf,
				"count":     len(candles),
				"attempt":   attempt,
			})
			break
		}
	}

	if len(candles) == 0 {
		if dbErr == nil && len(dbCandles) > 0 {
			tb.logger.Info("Kite API failed or rate-limited; falling back to available database candles", map[string]interface{}{"symbol": symbol, "timeframe": tf, "count": len(dbCandles)})
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
				for _, strat := range targetStrats {
					strat.OnCandleClose(candle, symbol)
				}
			}
			return
		}
		tb.logger.Warn("Exited catch-up retry loop with 0 candles. Relying on live WebSockets.", map[string]interface{}{"symbol": symbol, "timeframe": tf})
		return
	}

	// Persist caught-up candles to database to protect API limits on future restarts today
	if err := tb.db.SaveHistoricalCandles(tb.ctx, token, candles, tableName); err != nil {
		tb.logger.Error("Failed to save catch-up historical candles to database", map[string]interface{}{"error": err.Error(), "symbol": symbol, "timeframe": tf})
	} else {
		tb.logger.Info("Saved catch-up historical candles to database", map[string]interface{}{"symbol": symbol, "timeframe": tf, "count": len(candles)})
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
			Time:      c.Date,
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
		for _, strat := range targetStrats {
			strat.OnCandleClose(candle, symbol)
		}
	}
}

// hardSquareOff closes all active positions and cancels pending orders
func (tb *TradingBot) hardSquareOff() {
	tb.logger.Warn(fmt.Sprintf("[LOW_VOLUME] Executing %s hard square-off override...", tb.cfg.AutoSquareOffTime), nil)

	// Fetch actual live positions from Zerodha to ignore manually executed trades
	livePositions, err := tb.kiteClient.GetPositions()
	activeMap := make(map[string]data.Position)
	if err == nil {
		for _, p := range livePositions.Net {
			if p.Product == "MIS" {
				activeMap[p.TradingSymbol] = p
			}
		}
	} else {
		tb.logger.Error("Failed to fetch live positions from Zerodha during EOD square-off", map[string]interface{}{"error": err.Error()})
	}

	positions := tb.riskMgr.GetOpenPositions()
	for orderID, pos := range positions {
		if err == nil {
			livePos, hasPos := activeMap[pos.Symbol]
			if !hasPos || livePos.Quantity == 0 {
				tb.logger.Info("Position already closed on Zerodha (manually executed). Cleaning up local state.", map[string]interface{}{
					"symbol":   pos.Symbol,
					"order_id": orderID,
				})
				if pos.BrokerSLOrderID != "" {
					tb.execMgr.CancelOrder(pos.BrokerSLOrderID)
				}
				tb.riskMgr.OnOrderClose(orderID, pos.LatestPrice, pos.Quantity)
				_ = tb.db.CloseOpenPosition(tb.ctx, orderID, pos.LatestPrice)
				continue
			}

			// If quantity is different, adjust it
			absLiveQty := int(math.Abs(float64(livePos.Quantity)))
			if absLiveQty != pos.Quantity {
				tb.logger.Warn("Tracked position quantity differs from Zerodha net position. Adjusting quantity.", map[string]interface{}{
					"symbol":       pos.Symbol,
					"tracked_qty":  pos.Quantity,
					"live_net_qty": absLiveQty,
				})
				pos.Quantity = absLiveQty
			}
		}
		// Cancel the broker-side SL order first if it exists
		if pos.BrokerSLOrderID != "" {
			tb.logger.Info("Cancelling broker-side stop-loss order during hard square-off", map[string]interface{}{
				"symbol":      pos.Symbol,
				"sl_order_id": pos.BrokerSLOrderID,
			})
			tb.execMgr.CancelOrder(pos.BrokerSLOrderID)
		}

		// Get the current exit price estimate
		tick := tb.ticker.GetLatestTick(pos.Token)
		var exitPrice float64
		if tick != nil {
			exitPrice = tick.LTP
		} else {
			exitPrice = pos.LatestPrice
		}

		if tb.execMgr.LiveTrading {
			var txnType string
			if pos.Side == "BUY" {
				txnType = "SELL"
			} else {
				txnType = "BUY"
			}

			// Place a MARKET order to guarantee position exit on Zerodha
			orderReq := execution.OrderRequest{
				TradingSymbol:   pos.Symbol,
				Exchange:        "NSE",
				Quantity:        pos.Quantity,
				TransactionType: txnType,
				OrderType:       execution.OrderTypeMarket,
				Product:         "MIS",
				Validity:        "DAY",
				Strategy:        pos.Strategy,
			}

			tb.logger.Info("Placing live market square-off order", map[string]interface{}{
				"symbol":   pos.Symbol,
				"qty":      pos.Quantity,
				"txn_type": txnType,
			})

			exitOrderID, err := tb.execMgr.PlaceOrder(orderReq)
			if err != nil {
				tb.logger.Error("Failed to place live market square-off order, trying LIMIT order fallback", map[string]interface{}{
					"symbol": pos.Symbol,
					"error":  err.Error(),
				})

				// Calculate marketable limit price
				tickSize := tb.getTickSize(pos.Symbol)
				var limitPrice float64
				if txnType == "SELL" {
					limitPrice = math.Round((exitPrice*0.95)/tickSize) * tickSize
				} else {
					limitPrice = math.Round((exitPrice*1.05)/tickSize) * tickSize
				}

				orderReq.OrderType = "LIMIT"
				orderReq.Price = &limitPrice

				tb.logger.Info("Placing live LIMIT fallback square-off order", map[string]interface{}{
					"symbol": pos.Symbol,
					"qty":    pos.Quantity,
					"price":  limitPrice,
				})

				exitOrderID, err = tb.execMgr.PlaceOrder(orderReq)
				if err != nil {
					tb.logger.Error("Failed to place live LIMIT fallback square-off order as well", map[string]interface{}{
						"symbol": pos.Symbol,
						"error":  err.Error(),
					})
					continue // Skip local close to avoid inconsistent state with broker
				}
			}

			tb.statusTracker.StartTracking(exitOrderID)
			tb.riskMgr.OnOrderClose(orderID, exitPrice, pos.Quantity)
			_ = tb.db.CloseOpenPosition(tb.ctx, orderID, exitPrice)
		} else {
			// In paper/simulation trading, simulate immediate fill and close locally
			tb.logger.Info("Simulating hard square-off exit", map[string]interface{}{
				"symbol": pos.Symbol,
				"price":  exitPrice,
			})
			tb.execMgr.CancelOrder(orderID)
			tb.riskMgr.OnOrderClose(orderID, exitPrice, pos.Quantity)
			_ = tb.db.CloseOpenPosition(tb.ctx, orderID, exitPrice)
		}
	}

	tb.logger.Info("[LOW_VOLUME] Hard square-off complete. Exposure is zero.", nil)

	// Also square off active options position if present
	tb.hardSquareOffOptions()
}

// hardSquareOffOptions closes active options position at EOD cutoff time
func (tb *TradingBot) hardSquareOffOptions() {
	if tb.optionsPosMgr == nil {
		return
	}

	optPos := tb.optionsPosMgr.GetActivePosition()
	if optPos == nil {
		return
	}

	tb.logger.Warn("[OPTIONS EOD AUTO SQUARE-OFF] Closing active option position for EOD", map[string]interface{}{
		"symbol": optPos.Symbol,
		"qty":    optPos.Quantity,
		"entry":  optPos.EntryPremium,
		"ltp":    optPos.LatestPrice,
	})

	exitPrice := optPos.LatestPrice
	if tb.cfg.Options.LiveTrading && tb.execMgr != nil {
		orderReq := execution.OrderRequest{
			TradingSymbol:   optPos.Symbol,
			Exchange:        "NFO",
			Quantity:        optPos.Quantity,
			TransactionType: "BUY",
			OrderType:       execution.OrderTypeMarket,
			Product:         "MIS",
			Validity:        "DAY",
			Strategy:        "OPTIONS_SUPERTREND",
		}
		exitOrderID, errExec := tb.execMgr.PlaceOrder(orderReq)
		if errExec == nil {
			tb.logger.Info("Options EOD square-off market order placed", map[string]interface{}{
				"order_id": exitOrderID,
			})
		}
	}

	pnl := tb.optionsPosMgr.OnTradeClosed(exitPrice, "EOD SQUARE-OFF")

	_ = tb.optionsPosMgr.SaveState(tb.ctx)
	tb.logger.Info("[OPTIONS EOD AUTO SQUARE-OFF] Options position square-off complete", map[string]interface{}{
		"symbol": optPos.Symbol,
		"pnl":    pnl,
	})
}

// queryPreviousDayHighLow retrieves high, low, and close of a stock for the previous trading day
func (tb *TradingBot) queryPreviousDayHighLow(token int64, loc *time.Location) (float64, float64, float64, time.Time, error) {
	// Find the most recent day where we have candles in DB prior to today
	nowIST := time.Now().In(loc)
	todayStart := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 0, 0, 0, 0, loc).UTC()

	lastTime, err := tb.db.GetLastCandleTimeBefore(tb.ctx, token, todayStart)
	if err != nil || lastTime.IsZero() {
		return 0, 0, 0, time.Time{}, fmt.Errorf("no historical date found for token %d: %w", token, err)
	}

	// The start and end of that previous trading day
	lastTimeIST := lastTime.In(loc)
	prevDayStart := time.Date(lastTimeIST.Year(), lastTimeIST.Month(), lastTimeIST.Day(), 0, 0, 0, 0, loc).UTC()
	prevDayEnd := time.Date(lastTimeIST.Year(), lastTimeIST.Month(), lastTimeIST.Day(), 23, 59, 59, 0, loc).UTC()

	high, low, closeVal, err := tb.db.GetPreviousDayOHLC(tb.ctx, token, prevDayStart, prevDayEnd)
	if err != nil {
		return 0, 0, 0, lastTimeIST, fmt.Errorf("failed to scan high/low/close: %w", err)
	}

	return high, low, closeVal, lastTimeIST, nil
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

// resolvePreviousDayHighLow retrieves high, low, and close for a token, fetching it from Zerodha first if not in database or stale
func (tb *TradingBot) resolvePreviousDayHighLow(token int64, symbol string, loc *time.Location) (float64, float64, float64, error) {
	high, low, closeVal, lastDate, err := tb.queryPreviousDayHighLow(token, loc)

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
	if err == nil && high > 0 && low > 0 && closeVal > 0 && !lastDate.Before(expectedPrevDay) {
		return high, low, closeVal, nil
	}

	// Not in database or stale, fetch from Zerodha
	tb.logger.Warn("Historical candles not found or stale in database. Fetching from Zerodha...", map[string]interface{}{
		"symbol": symbol,
	})

	if err := tb.fetchAndStorePreviousDayCandles(token, symbol, loc); err != nil {
		return 0, 0, 0, fmt.Errorf("failed to fetch and store previous day candles: %w", err)
	}

	// Re-query database now that we stored the candles
	high, low, closeVal, _, err = tb.queryPreviousDayHighLow(token, loc)
	return high, low, closeVal, err
}

// cacheWatchlistLeverage queries dynamic order margins from Zerodha for the watchlist symbols and caches their leverage factor.
func (tb *TradingBot) cacheWatchlistLeverage(symbols []string) {
	if len(symbols) == 0 {
		return
	}

	params := make([]data.OrderParams, 0, len(symbols))
	symbolPrices := make(map[string]float64)

	for _, symbol := range symbols {
		price := 500.0 // default fallback price
		token, err := tb.securityMaster.GetInstrumentToken(symbol)
		if err == nil {
			high, low, _, _, err := tb.queryPreviousDayHighLow(token, data.ISTLocation)
			if err == nil && high > 0 {
				price = (high + low) / 2.0
			}
		}

		symbolPrices[symbol] = price

		params = append(params, data.OrderParams{
			Exchange:        "NSE",
			TradingSymbol:   symbol,
			TransactionType: "BUY",
			Product:         "MIS",
			OrderType:       "MARKET",
			Quantity:        1,
			Price:           price,
		})
	}

	tb.logger.Info("Batch querying order margins from Zerodha for leverage caching...", map[string]interface{}{
		"symbols_count": len(symbols),
	})

	margins, err := tb.kiteClient.GetOrderMargins(params)
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

// trimToActiveWatchlistSubscriptions unsubscribes non-watchlist tokens at MorningBroadAggEnd (09:45 AM), keeping only active watchlist + index spot tokens + active options
func (tb *TradingBot) trimToActiveWatchlistSubscriptions() {
	tb.broadTokensMutex.RLock()
	allBroadTokens := make([]int64, 0, len(tb.broadSubscriptionTokens))
	for token := range tb.broadSubscriptionTokens {
		allBroadTokens = append(allBroadTokens, token)
	}
	tb.broadTokensMutex.RUnlock()

	activeTokensMap := make(map[int64]bool)

	// Keep all active watchlist symbols
	tb.watchlistMutex.RLock()
	for _, token := range tb.watchlist {
		activeTokensMap[token] = true
	}
	tb.watchlistMutex.RUnlock()

	// Keep all manual watchlist symbols from DB
	manualStocks, mErr := tb.db.GetDailyManualWatchlist(tb.ctx, time.Now().In(data.ISTLocation))
	if mErr == nil {
		for _, m := range manualStocks {
			parts := strings.Split(m, ":")
			sym := strings.TrimSpace(parts[0])
			if sym != "" {
				if tok, tErr := tb.db.ResolveSymbolToken(tb.ctx, sym); tErr == nil && tok > 0 {
					activeTokensMap[tok] = true
				}
			}
		}
	}

	// Keep all supported index spot tokens
	for _, spec := range data.GetAllSupportedIndices() {
		activeTokensMap[spec.SpotToken] = true
	}

	// Keep all active options position tokens
	if tb.optionsPosMgrs != nil {
		for _, mgr := range tb.optionsPosMgrs {
			if mgr != nil {
				if optPos := mgr.GetActivePosition(); optPos != nil {
					if optToken, err := tb.securityMaster.GetInstrumentToken(optPos.Symbol); err == nil && optToken > 0 {
						activeTokensMap[optToken] = true
					}
				}
			}
		}
	} else if tb.optionsPosMgr != nil {
		if optPos := tb.optionsPosMgr.GetActivePosition(); optPos != nil {
			if optToken, err := tb.securityMaster.GetInstrumentToken(optPos.Symbol); err == nil && optToken > 0 {
				activeTokensMap[optToken] = true
			}
		}
	}

	var tokensToUnsubscribe []int64
	for _, token := range allBroadTokens {
		if !activeTokensMap[token] {
			tokensToUnsubscribe = append(tokensToUnsubscribe, token)
		}
	}

	if len(tokensToUnsubscribe) > 0 {
		if err := tb.ticker.Unsubscribe(tokensToUnsubscribe); err != nil {
			tb.logger.Warn("Failed to unsubscribe non-watchlist tokens", map[string]interface{}{"error": err.Error()})
		} else {
			tb.logger.Info("Successfully unsubscribed non-watchlist tokens to keep WebSocket stream lean", map[string]interface{}{
				"unsubscribed_count": len(tokensToUnsubscribe),
				"active_count":       len(activeTokensMap),
			})
		}
	}
}
