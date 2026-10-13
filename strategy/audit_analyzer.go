package strategy

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// AuditAnalyzer provides on-demand diagnostic and lifecycle auditing for any stock and strategy
type AuditAnalyzer struct {
	logger     *zap.Logger
	db         *data.Database
	secMaster  *data.SecurityMaster
	indicators *Indicators
}

// NewAuditAnalyzer creates a new instance of AuditAnalyzer
func NewAuditAnalyzer(logger *zap.Logger, db *data.Database, secMaster *data.SecurityMaster) *AuditAnalyzer {
	return &AuditAnalyzer{
		logger:     logger,
		db:         db,
		secMaster:  secMaster,
		indicators: &Indicators{logger: logger},
	}
}

// getFloatParam safely extracts a float64 parameter from a generic map across aliases and type representations
func getFloatParam(p map[string]interface{}, defaultVal float64, keys ...string) float64 {
	if p == nil {
		return defaultVal
	}
	for _, k := range keys {
		if val, ok := p[k]; ok && val != nil {
			switch v := val.(type) {
			case float64:
				return v
			case float32:
				return float64(v)
			case int:
				return float64(v)
			case int64:
				return float64(v)
			case json.Number:
				if f, err := v.Float64(); err == nil {
					return f
				}
			case string:
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					return f
				}
			}
		}
	}
	return defaultVal
}

// getIntParam safely extracts an int parameter from a generic map across aliases and type representations
func getIntParam(p map[string]interface{}, defaultVal int, keys ...string) int {
	if p == nil {
		return defaultVal
	}
	for _, k := range keys {
		if val, ok := p[k]; ok && val != nil {
			switch v := val.(type) {
			case int:
				return v
			case int64:
				return int(v)
			case float64:
				return int(v)
			case json.Number:
				if i, err := v.Int64(); err == nil {
					return int(i)
				}
			case string:
				if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					return i
				}
			}
		}
	}
	return defaultVal
}

// loadStrategyConfig retrieves dynamic configuration from app_system_configs for a specific strategy
func (a *AuditAnalyzer) loadStrategyConfig(sysConfigs map[string]map[string]string, stratName string, requestedTimeframe ...string) AppliedStrategyConfig {
	appliedTimeframe := "5m"
	tradeEndTime := "14:30:30"
	useBrokerSL := true
	attachedRR := "DYNAMIC_TRAILING_SL"
	params := make(map[string]interface{})

	switch stratName {
	case "VANDE_BHARAT":
		appliedTimeframe = "5m"
		tradeEndTime = "11:00:00"
		params["master_max_pct"] = 1.8
		params["master_max_wick_pct"] = 40.0
		params["sl_min_pct"] = 0.50
		params["sl_max_pct"] = 1.00
		params["min_gap_pct"] = 2.0
		params["sl_buffer_pct"] = 0.10
		params["min_candles_to_ignore"] = 2
	case "VANDE_BHARAT_TRAP":
		appliedTimeframe = "5m"
		tradeEndTime = "11:00:00"
		params["fake_master_max_pct"] = 3.0
		params["genuine_master_max_pct"] = 1.8
		params["genuine_master_max_wick_pct"] = 40.0
		params["sl_min_pct"] = 0.5
		params["sl_max_pct"] = 1.0
		params["min_candles_to_ignore"] = 0
	case "LOW_VOLUME":
		appliedTimeframe = "5m"
		tradeEndTime = "10:45:00"
		attachedRR = "PARTIAL_BOOK_COST_SL"
		params["min_candles_to_ignore"] = 3
	case "FAKE_BREAKOUT":
		appliedTimeframe = "1m"
		tradeEndTime = "11:00:00"
		params["gap_up_min_pct"] = 4.0
		params["gap_up_max_pct"] = 8.0
		params["gap_down_min_pct"] = 4.0
		params["gap_down_max_pct"] = 8.0
		params["master_max_wick_pct"] = 40.0
		params["confirm_max_pct"] = 1.0
		params["min_candles_to_ignore"] = 0
	case "EMAS5_BREAKOUT":
		appliedTimeframe = "5m"
		tradeEndTime = "14:30:30"
		params["rally_candles"] = 5
		params["min_rebound_pct"] = 0.50
		params["master_max_pct"] = 2.00
		params["master_max_wick_pct"] = 40.0
		params["max_inside_candles"] = 1
		params["confirm_max_pct"] = 1.00
		params["ema_touch_buffer_pct"] = 0.10
		params["sl_buffer_pct"] = 0.10
		params["max_entry_distance_pct"] = 0.35
		params["max_setup_wait_candles"] = 6
		params["min_pdh_pdl_retrace_pct"] = 0.50
		params["arc_bounce_tolerance_pct"] = 0.30
	}

	if sysConfigs != nil {
		// A. Read generic JSON object for this strategy from TRADING_STRATEGY table
		if tStratMap := sysConfigs["TRADING_STRATEGY"]; tStratMap != nil {
			if rawVal, ok := tStratMap[stratName]; ok && strings.HasPrefix(strings.TrimSpace(rawVal), "{") {
				var dynMap map[string]interface{}
				if err := json.Unmarshal([]byte(rawVal), &dynMap); err == nil {
					for k, v := range dynMap {
						params[k] = v
					}
					if tf, ok := dynMap["candle_time_frame"].(string); ok && tf != "" {
						appliedTimeframe = tf
					}
					if tet, ok := dynMap["trade_end_time"].(string); ok && tet != "" {
						tradeEndTime = tet
					}
					if arr, ok := dynMap["attached_risk_reward"].(string); ok && arr != "" {
						attachedRR = arr
					}
					if ubs, ok := dynMap["use_broker_sl"].(bool); ok {
						useBrokerSL = ubs
					}
				}
			}
		}

		// B. Read flat key-values from EQUITY_STRATEGY table using strategy prefix
		if eqStratMap := sysConfigs["EQUITY_STRATEGY"]; eqStratMap != nil {
			prefix := ""
			switch stratName {
			case "VANDE_BHARAT":
				prefix = "vb_"
			case "VANDE_BHARAT_TRAP":
				prefix = "vbt_"
			case "LOW_VOLUME":
				prefix = "lv_"
			case "FAKE_BREAKOUT":
				prefix = "fb_"
			case "EMAS5_BREAKOUT":
				prefix = "es5_"
			}

			if prefix != "" {
				for k, v := range eqStratMap {
					if strings.HasPrefix(k, prefix) {
						subKey := strings.TrimPrefix(k, prefix)
						if subKey == "candle_timeframe" && v != "" {
							appliedTimeframe = v
						} else if subKey == "trade_end_time" && v != "" {
							tradeEndTime = v
						} else if subKey == "use_broker_sl" {
							useBrokerSL = (v == "true")
						} else if f, err := strconv.ParseFloat(v, 64); err == nil {
							params[subKey] = f
						} else if i, err := strconv.Atoi(v); err == nil {
							params[subKey] = i
						} else {
							params[subKey] = v
						}
					}
				}
			}
		}

		// D. Global and per-strategy Market Trend Alignment settings
		if eqStratMap := sysConfigs["EQUITY_STRATEGY"]; eqStratMap != nil {
			globalMarketTrendEnabled := false
			if v, ok := eqStratMap["market_trend_alignment_enabled"]; ok {
				globalMarketTrendEnabled = strings.ToLower(v) == "true"
			}
			params["market_trend_alignment_enabled"] = globalMarketTrendEnabled

			niftyEmaPeriod := 20
			if v, ok := eqStratMap["market_trend_nifty_ema_period"]; ok {
				if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
					niftyEmaPeriod = parsed
				}
			}
			params["market_trend_nifty_ema_period"] = niftyEmaPeriod

			prefix := ""
			switch stratName {
			case "VANDE_BHARAT":
				prefix = "vb_"
			case "VANDE_BHARAT_TRAP":
				prefix = "vbt_"
			case "LOW_VOLUME":
				prefix = "lv_"
			case "FAKE_BREAKOUT":
				prefix = "fb_"
			case "EMAS5_BREAKOUT":
				prefix = "es5_"
			}

			enforceTrend := globalMarketTrendEnabled
			if prefix != "" {
				if v, ok := eqStratMap[prefix+"enforce_market_trend"]; ok {
					enforceTrend = strings.ToLower(v) == "true" || globalMarketTrendEnabled
				}
			}
			if dynEnforce, ok := params["enforce_market_trend_alignment"].(bool); ok && dynEnforce {
				enforceTrend = true
			}
			if dynEnforce, ok := params["enforce_market_trend"].(bool); ok && dynEnforce {
				enforceTrend = true
			}
			params["enforce_market_trend"] = enforceTrend
		}
	}

	// C. Explicit timeframe override
	if len(requestedTimeframe) > 0 && strings.TrimSpace(requestedTimeframe[0]) != "" {
		appliedTimeframe = strings.TrimSpace(requestedTimeframe[0])
	}

	return AppliedStrategyConfig{
		StrategyName:    stratName,
		CandleTimeframe: appliedTimeframe,
		TradeEndTime:    tradeEndTime,
		UseBrokerSL:     useBrokerSL,
		AttachedRR:      attachedRR,
		Parameters:      params,
	}
}

// AuditStock audits a stock for a specific date and strategy, returning a full diagnostic response
func (a *AuditAnalyzer) AuditStock(ctx context.Context, symbol, dateStr, strategyFilter string, requestedTimeframe ...string) (*StockAuditResponse, error) {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if sym == "" {
		return nil, fmt.Errorf("symbol cannot be empty")
	}

	if dateStr == "" {
		nowIST := time.Now().In(data.ISTLocation)
		dateStr = nowIST.Format("2006-01-02")
	}

	strat := strings.ToUpper(strings.TrimSpace(strategyFilter))
	if strat == "" {
		strat = "ALL"
	}

	resp := &StockAuditResponse{
		Symbol:           sym,
		Date:             dateStr,
		SelectedStrategy: strat,
		Events:           make([]data.StrategyEvent, 0),
	}

	// 1. Fetch any stored real-time strategy events from DB
	storedEvents, err := a.db.GetStrategyEvents(ctx, sym, dateStr, strat)
	if err == nil && len(storedEvents) > 0 {
		resp.Events = append(resp.Events, storedEvents...)
	}

	// 2. Fetch today's executed trades from DB to correlate fills/exits
	trades, _ := a.db.GetHistoricalTradesByDate(ctx, dateStr, sym)
	var matchingTrades []data.TradeHistoryRecord
	for _, tr := range trades {
		if strings.EqualFold(tr.Symbol, sym) {
			matchingTrades = append(matchingTrades, tr)
		}
	}
	resp.DaySummary.TradesTakenCount = len(matchingTrades)

	// Resolve instrument token
	var token int64
	if a.secMaster != nil {
		token, _ = a.secMaster.GetInstrumentToken(sym)
		if token <= 0 {
			token, _ = a.secMaster.ResolveAndAddSymbol(ctx, sym)
		}
	}

	// 2. Load Dynamic Strategy Parameters from app_system_configs (Zero Hardcoding)
	var sysConfigs map[string]map[string]string
	if a.db != nil {
		sysConfigs, _ = a.db.GetAllSystemConfigs(ctx)
	}

	primaryStrat := strat
	if primaryStrat == "ALL" {
		primaryStrat = "EMAS5_BREAKOUT"
	}
	appliedConfig := a.loadStrategyConfig(sysConfigs, primaryStrat, requestedTimeframe...)

	resp.ConfiguredTimeframe = appliedConfig.CandleTimeframe
	resp.AppliedConfig = appliedConfig
	resp.CandleDiagnostics = make([]CandleDiagnosticItem, 0)

	// 3. Query candles across both 5m and 1m timeframes for complete diagnostic auditing
	var candles1m []data.Candle
	var candles5m []data.Candle
	if token > 0 {
		candles5m, _ = a.db.GetCandlesWithHistory(ctx, token, dateStr, "5m", 150)
		candles1m, _ = a.db.GetCandlesWithHistory(ctx, token, dateStr, "1m", 450)
	}

	// Filter today's candles and identify the exact immediate previous trading day strictly before dateStr
	var todayCandles1m []data.Candle
	var lastPrevDate1m string
	for _, c := range candles1m {
		cTimeIST := data.NormalizeToIST(c.Time)
		cDateStr := cTimeIST.Format("2006-01-02")
		if cDateStr == dateStr {
			todayCandles1m = append(todayCandles1m, c)
		} else if cDateStr < dateStr && cDateStr > lastPrevDate1m {
			lastPrevDate1m = cDateStr
		}
	}
	var prevDayCandles1m []data.Candle
	if lastPrevDate1m != "" {
		for _, c := range candles1m {
			cTimeIST := data.NormalizeToIST(c.Time)
			if cTimeIST.Format("2006-01-02") == lastPrevDate1m {
				prevDayCandles1m = append(prevDayCandles1m, c)
			}
		}
	}

	var todayCandles5m []data.Candle
	var lastPrevDate5m string
	for _, c := range candles5m {
		cTimeIST := data.NormalizeToIST(c.Time)
		cDateStr := cTimeIST.Format("2006-01-02")
		if cDateStr == dateStr {
			todayCandles5m = append(todayCandles5m, c)
		} else if cDateStr < dateStr && cDateStr > lastPrevDate5m {
			lastPrevDate5m = cDateStr
		}
	}
	var prevDayCandles5m []data.Candle
	if lastPrevDate5m != "" {
		for _, c := range candles5m {
			cTimeIST := data.NormalizeToIST(c.Time)
			if cTimeIST.Format("2006-01-02") == lastPrevDate5m {
				prevDayCandles5m = append(prevDayCandles5m, c)
			}
		}
	}

	// Fallback to most recent trading date with candle history if requested date has 0 candles
	if len(todayCandles5m) == 0 && len(todayCandles1m) == 0 {
		effectiveDate := lastPrevDate5m
		if effectiveDate == "" {
			effectiveDate = lastPrevDate1m
		}
		if effectiveDate != "" {
			dateStr = effectiveDate
			resp.Date = dateStr

			// Re-filter candles using effectiveDate
			todayCandles1m = nil
			lastPrevDate1m = ""
			for _, c := range candles1m {
				cTimeIST := data.NormalizeToIST(c.Time)
				cDateStr := cTimeIST.Format("2006-01-02")
				if cDateStr == dateStr {
					todayCandles1m = append(todayCandles1m, c)
				} else if cDateStr < dateStr && cDateStr > lastPrevDate1m {
					lastPrevDate1m = cDateStr
				}
			}
			prevDayCandles1m = nil
			if lastPrevDate1m != "" {
				for _, c := range candles1m {
					cTimeIST := data.NormalizeToIST(c.Time)
					if cTimeIST.Format("2006-01-02") == lastPrevDate1m {
						prevDayCandles1m = append(prevDayCandles1m, c)
					}
				}
			}

			todayCandles5m = nil
			lastPrevDate5m = ""
			for _, c := range candles5m {
				cTimeIST := data.NormalizeToIST(c.Time)
				cDateStr := cTimeIST.Format("2006-01-02")
				if cDateStr == dateStr {
					todayCandles5m = append(todayCandles5m, c)
				} else if cDateStr < dateStr && cDateStr > lastPrevDate5m {
					lastPrevDate5m = cDateStr
				}
			}
			prevDayCandles5m = nil
			if lastPrevDate5m != "" {
				for _, c := range candles5m {
					cTimeIST := data.NormalizeToIST(c.Time)
					if cTimeIST.Format("2006-01-02") == lastPrevDate5m {
						prevDayCandles5m = append(prevDayCandles5m, c)
					}
				}
			}

			// Re-query events and trades for effectiveDate
			storedEvents, err = a.db.GetStrategyEvents(ctx, sym, dateStr, strat)
			if err == nil && len(storedEvents) > 0 {
				resp.Events = storedEvents
			}
			trades, _ = a.db.GetHistoricalTradesByDate(ctx, dateStr, sym)
			matchingTrades = nil
			for _, tr := range trades {
				if strings.EqualFold(tr.Symbol, sym) {
					matchingTrades = append(matchingTrades, tr)
				}
			}
			resp.DaySummary.TradesTakenCount = len(matchingTrades)
		}
	}

	// Calculate Day Summary & Reference Levels
	if len(todayCandles5m) > 0 {
		resp.DaySummary.Open = todayCandles5m[0].Open
		dayHigh := todayCandles5m[0].High
		dayLow := todayCandles5m[0].Low
		for _, c := range todayCandles5m {
			if c.High > dayHigh {
				dayHigh = c.High
			}
			if c.Low < dayLow {
				dayLow = c.Low
			}
		}
		resp.DaySummary.High = dayHigh
		resp.DaySummary.Low = dayLow
		resp.DaySummary.Close = todayCandles5m[len(todayCandles5m)-1].Close
		if resp.DaySummary.Open > 0 {
			resp.DaySummary.RangePct = ((dayHigh - dayLow) / resp.DaySummary.Open) * 100.0
		}
	} else if len(todayCandles1m) > 0 {
		resp.DaySummary.Open = todayCandles1m[0].Open
		dayHigh := todayCandles1m[0].High
		dayLow := todayCandles1m[0].Low
		for _, c := range todayCandles1m {
			if c.High > dayHigh {
				dayHigh = c.High
			}
			if c.Low < dayLow {
				dayLow = c.Low
			}
		}
		resp.DaySummary.High = dayHigh
		resp.DaySummary.Low = dayLow
		resp.DaySummary.Close = todayCandles1m[len(todayCandles1m)-1].Close
		if resp.DaySummary.Open > 0 {
			resp.DaySummary.RangePct = ((dayHigh - dayLow) / resp.DaySummary.Open) * 100.0
		}
	}

	// Calculate PDH / PDL / PDClose strictly from the immediate previous trading day
	if len(prevDayCandles5m) > 0 {
		pdHigh := prevDayCandles5m[0].High
		pdLow := prevDayCandles5m[0].Low
		for _, c := range prevDayCandles5m {
			if c.High > pdHigh {
				pdHigh = c.High
			}
			if c.Low < pdLow {
				pdLow = c.Low
			}
		}
		resp.DaySummary.PDH = pdHigh
		resp.DaySummary.PDL = pdLow
		resp.DaySummary.PDClose = prevDayCandles5m[len(prevDayCandles5m)-1].Close
	} else if len(prevDayCandles1m) > 0 {
		pdHigh := prevDayCandles1m[0].High
		pdLow := prevDayCandles1m[0].Low
		for _, c := range prevDayCandles1m {
			if c.High > pdHigh {
				pdHigh = c.High
			}
			if c.Low < pdLow {
				pdLow = c.Low
			}
		}
		resp.DaySummary.PDH = pdHigh
		resp.DaySummary.PDL = pdLow
		resp.DaySummary.PDClose = prevDayCandles1m[len(prevDayCandles1m)-1].Close
	}

	// 3.5. Load NIFTY 50 5m candles for Market Trend Alignment
	niftyEmaPeriod := 20
	if p, ok := appliedConfig.Parameters["market_trend_nifty_ema_period"].(int); ok && p > 0 {
		niftyEmaPeriod = p
	}
	niftyCandles5m, _ := a.db.GetCandlesWithHistory(ctx, 256265, dateStr, "5m", 150)
	type niftyTrendPoint struct {
		LTP     float64
		EMA     float64
		Bullish bool
	}
	niftyTrendBySec := make(map[int64]niftyTrendPoint)
	if len(niftyCandles5m) > 0 {
		nCloses := make([]float64, len(niftyCandles5m))
		for i, nc := range niftyCandles5m {
			nCloses[i] = nc.Close
		}
		nEMAs := a.indicators.CalculateEMA(nCloses, niftyEmaPeriod)
		for i, nc := range niftyCandles5m {
			if i < len(nEMAs) && nEMAs[i] > 0 {
				nUnix := data.NormalizeToIST(nc.Time).Unix()
				nFloored := (nUnix / 300) * 300
				niftyTrendBySec[nFloored] = niftyTrendPoint{
					LTP:     nc.Close,
					EMA:     nEMAs[i],
					Bullish: nc.Close >= nEMAs[i],
				}
			}
		}
	}

	// 4. If stored events were not recorded or more detail is needed, run deterministic replay simulation
	replayEvents := make([]data.StrategyEvent, 0)
	if strat == "ALL" || strat == "EMAS5_BREAKOUT" {
		es5Cfg := appliedConfig
		if strat == "ALL" {
			es5Cfg = a.loadStrategyConfig(sysConfigs, "EMAS5_BREAKOUT", requestedTimeframe...)
		}
		var es5AllCandles []data.Candle
		var es5TodayCandles []data.Candle
		if es5Cfg.CandleTimeframe == "1m" && len(todayCandles1m) > 0 {
			es5AllCandles = candles1m
			es5TodayCandles = todayCandles1m
		} else {
			es5AllCandles = candles5m
			es5TodayCandles = todayCandles5m
		}
		es5Events, es5Diags := a.replayEMAS5(sym, es5AllCandles, es5TodayCandles, resp.DaySummary, matchingTrades, es5Cfg, resp.Events)
		replayEvents = append(replayEvents, es5Events...)
		if strat == "EMAS5_BREAKOUT" || (strat == "ALL" && len(resp.CandleDiagnostics) == 0) {
			resp.CandleDiagnostics = es5Diags
		}
	}

	if strat == "ALL" || strat == "VANDE_BHARAT" {
		vbCfg := appliedConfig
		if strat == "ALL" {
			vbCfg = a.loadStrategyConfig(sysConfigs, "VANDE_BHARAT", requestedTimeframe...)
		}
		var vbCandles []data.Candle
		if vbCfg.CandleTimeframe == "1m" && len(todayCandles1m) > 0 {
			vbCandles = todayCandles1m
		} else if len(todayCandles5m) > 0 {
			vbCandles = todayCandles5m
		} else {
			vbCandles = todayCandles1m
		}
		vbEvents, vbDiags := a.replayVandeBharat(sym, vbCandles, resp.DaySummary, matchingTrades, vbCfg)
		replayEvents = append(replayEvents, vbEvents...)
		if strat == "VANDE_BHARAT" {
			resp.CandleDiagnostics = vbDiags
			resp.ConfiguredTimeframe = vbCfg.CandleTimeframe
		}
	}

	if strat == "ALL" || strat == "VANDE_BHARAT_TRAP" {
		vbtCfg := appliedConfig
		if strat == "ALL" {
			vbtCfg = a.loadStrategyConfig(sysConfigs, "VANDE_BHARAT_TRAP", requestedTimeframe...)
		}
		var vbtCandles []data.Candle
		if vbtCfg.CandleTimeframe == "1m" && len(todayCandles1m) > 0 {
			vbtCandles = todayCandles1m
		} else if len(todayCandles5m) > 0 {
			vbtCandles = todayCandles5m
		} else {
			vbtCandles = todayCandles1m
		}
		vbtEvents, vbtDiags := a.replayVandeBharatTrap(sym, vbtCandles, resp.DaySummary, matchingTrades, vbtCfg)
		replayEvents = append(replayEvents, vbtEvents...)
		if strat == "VANDE_BHARAT_TRAP" || (strat == "ALL" && len(resp.CandleDiagnostics) == 0) {
			resp.CandleDiagnostics = vbtDiags
			resp.ConfiguredTimeframe = vbtCfg.CandleTimeframe
		}
	}

	if strat == "ALL" || strat == "LOW_VOLUME" {
		lvCfg := appliedConfig
		if strat == "ALL" {
			lvCfg = a.loadStrategyConfig(sysConfigs, "LOW_VOLUME", requestedTimeframe...)
		}
		var lvCandles []data.Candle
		if lvCfg.CandleTimeframe == "1m" && len(todayCandles1m) > 0 {
			lvCandles = todayCandles1m
		} else {
			lvCandles = todayCandles5m
		}
		lvEvents, lvDiags := a.replayLowVolume(sym, lvCandles, resp.DaySummary, matchingTrades, lvCfg, resp.Events)
		replayEvents = append(replayEvents, lvEvents...)
		if strat == "LOW_VOLUME" || (strat == "ALL" && len(resp.CandleDiagnostics) == 0) {
			resp.CandleDiagnostics = lvDiags
			resp.ConfiguredTimeframe = lvCfg.CandleTimeframe
		}
	}

	if strat == "ALL" || strat == "FAKE_BREAKOUT" {
		fbCfg := appliedConfig
		if strat == "ALL" {
			fbCfg = a.loadStrategyConfig(sysConfigs, "FAKE_BREAKOUT", requestedTimeframe...)
		}
		var fbCandles []data.Candle
		if fbCfg.CandleTimeframe == "1m" && len(todayCandles1m) > 0 {
			fbCandles = todayCandles1m
		} else if len(todayCandles5m) > 0 {
			fbCandles = todayCandles5m
		} else {
			fbCandles = todayCandles1m
		}
		fbEvents, fbDiags := a.replayFakeBreakout(sym, fbCandles, resp.DaySummary, matchingTrades, fbCfg)
		replayEvents = append(replayEvents, fbEvents...)
		if strat == "FAKE_BREAKOUT" || (strat == "ALL" && len(resp.CandleDiagnostics) == 0) {
			resp.CandleDiagnostics = fbDiags
			resp.ConfiguredTimeframe = fbCfg.CandleTimeframe
		}
	}

	// 4.5. Enrich Candle Diagnostics with NIFTY Trend & Enforce Trend Alignment Mismatch
	enforceTrend := false
	if ef, ok := appliedConfig.Parameters["enforce_market_trend"].(bool); ok {
		enforceTrend = ef
	}

	for i := range resp.CandleDiagnostics {
		diag := &resp.CandleDiagnostics[i]
		if diag.Details == nil {
			diag.Details = make(map[string]interface{})
		}
		var diagUnix int64
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", diag.Time, data.ISTLocation); err == nil {
			diagUnix = t.Unix()
		} else if t, err := time.ParseInLocation("15:04", diag.Time, data.ISTLocation); err == nil {
			dateParsed, _ := time.ParseInLocation("2006-01-02", dateStr, data.ISTLocation)
			fullT := time.Date(dateParsed.Year(), dateParsed.Month(), dateParsed.Day(), t.Hour(), t.Minute(), 0, 0, data.ISTLocation)
			diagUnix = fullT.Unix()
		} else if t, err := time.ParseInLocation("15:04:05", diag.Time, data.ISTLocation); err == nil {
			dateParsed, _ := time.ParseInLocation("2006-01-02", dateStr, data.ISTLocation)
			fullT := time.Date(dateParsed.Year(), dateParsed.Month(), dateParsed.Day(), t.Hour(), t.Minute(), t.Second(), 0, data.ISTLocation)
			diagUnix = fullT.Unix()
		}

		if diagUnix > 0 {
			floored := (diagUnix / 300) * 300
			var np niftyTrendPoint
			var found bool
			if p, ok := niftyTrendBySec[floored]; ok {
				np = p
				found = true
			} else {
				for back := int64(300); back <= 900; back += 300 {
					if p, ok := niftyTrendBySec[floored-back]; ok {
						np = p
						found = true
						break
					}
				}
			}

			if found {
				trendStr := "BULLISH"
				if !np.Bullish {
					trendStr = "BEARISH"
				}
				diag.Details["nifty_ltp"] = np.LTP
				diag.Details["nifty_ema"] = np.EMA
				diag.Details["nifty_ema_period"] = niftyEmaPeriod
				diag.Details["nifty_trend"] = trendStr

				// Identify direction of this candle / setup
				var dirStr string
				if d, ok := diag.Details["direction"].(string); ok && d != "" {
					dirStr = strings.ToUpper(d)
				} else if a, ok := diag.Details["action"].(string); ok && a != "" {
					if strings.Contains(strings.ToUpper(a), "BUY") {
						dirStr = "BUY"
					} else if strings.Contains(strings.ToUpper(a), "SELL") {
						dirStr = "SELL"
					}
				}
				if dirStr == "" {
					for _, crit := range diag.PassedCriteria {
						if strings.Contains(crit, "Bullish") || strings.Contains(crit, "GREEN") || strings.Contains(crit, "U-Shape trough") || strings.Contains(crit, "PDH") {
							dirStr = "BUY"
							break
						}
						if strings.Contains(crit, "Bearish") || strings.Contains(crit, "RED") || strings.Contains(crit, "Inverted U-Shape") || strings.Contains(crit, "PDL") {
							dirStr = "SELL"
							break
						}
					}
				}

				// Add NIFTY 50 EMA telemetry badge to PassedCriteria / Warnings for qualified setup/confirmation/trade candles
				if diag.Verdict == "PASS" || diag.Verdict == "ARMED" {
					aboveBelow := "below"
					if np.Bullish {
						aboveBelow = "above"
					}

					isAligned := (dirStr == "BUY" && np.Bullish) || (dirStr == "SELL" && !np.Bullish)
					isOpposing := (dirStr == "BUY" && !np.Bullish) || (dirStr == "SELL" && np.Bullish)

					if isAligned {
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Market Trend Aligned: NIFTY 50 (₹%.2f) %s %d EMA (₹%.2f) — %s Trend", np.LTP, aboveBelow, niftyEmaPeriod, np.EMA, trendStr),
						)
					} else if isOpposing {
						gatedMsg := "BUY trades gated unless trend flips"
						if dirStr == "SELL" {
							gatedMsg = "SELL trades gated unless trend flips"
						}
						diag.Warnings = append(diag.Warnings,
							fmt.Sprintf("Market Trend Opposing: NIFTY 50 (₹%.2f) is %s %d EMA (₹%.2f) — %s Trend (%s)", np.LTP, strings.ToUpper(aboveBelow), niftyEmaPeriod, np.EMA, trendStr, gatedMsg),
						)
					} else {
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("NIFTY 50 (₹%.2f) %s %d EMA (₹%.2f) — %s Trend", np.LTP, aboveBelow, niftyEmaPeriod, np.EMA, trendStr),
						)
					}
				}

				if enforceTrend && (diag.Status == "BREAKOUT_TRIGGERED" || diag.Status == "TRADE_TAKEN") {
					isBuy := true
					if dirStr == "SELL" {
						isBuy = false
					}
					var mismatchReason string
					if isBuy && !np.Bullish {
						mismatchReason = fmt.Sprintf("Market Trend Alignment Mismatch: NIFTY 50 (₹%.2f) is BELOW %d EMA (₹%.2f) — BUY trades blocked", np.LTP, niftyEmaPeriod, np.EMA)
					} else if !isBuy && np.Bullish {
						mismatchReason = fmt.Sprintf("Market Trend Alignment Mismatch: NIFTY 50 (₹%.2f) is ABOVE %d EMA (₹%.2f) — SELL trades blocked", np.LTP, niftyEmaPeriod, np.EMA)
					}

					if mismatchReason != "" {
						if diag.Status == "TRADE_TAKEN" {
							diag.RejectionReasons = append(diag.RejectionReasons, mismatchReason+" (Executed trade violated Market Trend Alignment)")
							diag.Verdict = "VIOLATION"
						} else {
							diag.Status = "TRADE_SKIPPED"
							diag.Verdict = "REJECT"
							diag.RejectionReasons = append(diag.RejectionReasons, mismatchReason)
						}
						evtTime := time.Unix(diagUnix, 0).In(data.ISTLocation)

						// Filter out simulated TRADE_TAKEN events around this candle to prevent phantom trade counts
						cleanReplay := make([]data.StrategyEvent, 0, len(replayEvents))
						for _, re := range replayEvents {
							if re.Stage == "TRADE_TAKEN" && math.Abs(re.EventTime.Sub(evtTime).Seconds()) < 300 {
								continue
							}
							cleanReplay = append(cleanReplay, re)
						}
						replayEvents = cleanReplay

						replayEvents = append(replayEvents, data.StrategyEvent{
							EventTime:    evtTime,
							Symbol:       sym,
							Strategy:     primaryStrat,
							Stage:        "TRADE_SKIPPED",
							Severity:     "WARNING",
							Direction:    map[bool]string{true: "BUY", false: "SELL"}[isBuy],
							Title:        fmt.Sprintf("%s Trade Skipped: Market Trend Alignment Mismatch", primaryStrat),
							Reason:       mismatchReason,
							TriggerPrice: diag.Close,
							Details: map[string]interface{}{
								"nifty_ltp":   np.LTP,
								"nifty_ema":   np.EMA,
								"nifty_trend": trendStr,
								"reason":      mismatchReason,
							},
						})
					}
				}
			}
		}
	}

	// Deduplicate / Merge replay events with stored events
	if len(resp.Events) == 0 {
		resp.Events = replayEvents
	} else {
		// Merge any missing replay events
		seen := make(map[string]bool)
		for _, e := range resp.Events {
			key := fmt.Sprintf("%s_%s_%s", e.Stage, e.Strategy, e.EventTime.Format("15:04:05"))
			seen[key] = true
		}
		for _, re := range replayEvents {
			key := fmt.Sprintf("%s_%s_%s", re.Stage, re.Strategy, re.EventTime.Format("15:04:05"))
			if !seen[key] {
				resp.Events = append(resp.Events, re)
				seen[key] = true
			}
		}
	}

	// Sort events chronologically
	sort.Slice(resp.Events, func(i, j int) bool {
		return resp.Events[i].EventTime.Before(resp.Events[j].EventTime)
	})

	// 5. Compute Setups Count and Final Status
	setupCount := 0
	lastStatus := "NO_SETUP_FORMED"
	for _, ev := range resp.Events {
		if ev.Stage == "SETUP_FORMED" {
			setupCount++
			lastStatus = "SETUP_FORMED"
		} else if ev.Stage == "CONFIRMATION_ARMED" {
			lastStatus = "ARMED_WAITING"
		} else if ev.Stage == "TRADE_TAKEN" {
			lastStatus = "TRADE_TAKEN"
		} else if ev.Stage == "TRADE_SKIPPED" {
			lastStatus = "TRADE_SKIPPED"
		} else if ev.Stage == "SETUP_INVALIDATED" {
			lastStatus = "SETUP_INVALIDATED"
		} else if ev.Stage == "SETUP_EXPIRED" {
			lastStatus = "SETUP_EXPIRED"
		}
	}
	resp.DaySummary.SetupsFormedCount = setupCount
	if resp.DaySummary.TradesTakenCount > 0 {
		resp.DaySummary.FinalStatus = "TRADE_TAKEN"
	} else {
		resp.DaySummary.FinalStatus = lastStatus
	}

	// 6. Generate Insights & Parameter Recommendations
	resp.AnalysisInsights = a.generateInsights(sym, strat, resp.DaySummary, resp.Events, matchingTrades)

	return resp, nil
}

// replayEMAS5 simulates the EMA S5 Breakout Strategy across configured candles (default 5m)
func (a *AuditAnalyzer) replayEMAS5(symbol string, allCandles, todayCandles []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord, appCfg AppliedStrategyConfig, liveEvents ...[]data.StrategyEvent) ([]data.StrategyEvent, []CandleDiagnosticItem) {
	events := make([]data.StrategyEvent, 0, 16)
	diagnostics := make([]CandleDiagnosticItem, 0, len(todayCandles))
	if len(allCandles) < 10 {
		return events, diagnostics
	}

	var storedEvents []data.StrategyEvent
	if len(liveEvents) > 0 {
		storedEvents = liveEvents[0]
	}
	hasLiveEvents := len(storedEvents) > 0

	// Helper to check if an actual trade was executed on a candle in this direction
	hasRealTradeOnCandle := func(cTime time.Time, dir string) *data.TradeHistoryRecord {
		for i := range trades {
			tr := &trades[i]
			if strings.EqualFold(tr.Symbol, symbol) && tr.Side == dir {
				trTime := data.NormalizeToIST(tr.CreatedAt)
				if tr.EntryTime.After(time.Time{}) {
					trTime = data.NormalizeToIST(tr.EntryTime)
				}
				if !trTime.Before(cTime) && trTime.Before(cTime.Add(5*time.Minute)) {
					return tr
				}
			}
		}
		return nil
	}

	// Helper to check if a live breakout trigger was emitted in telemetry on this candle
	hasLiveTriggerOnCandle := func(cTime time.Time, dir string) bool {
		for _, ev := range storedEvents {
			if ev.Stage == "BREAKOUT_TRIGGER" || ev.Stage == "TRADE_TAKEN" || ev.Stage == "TRADE_ORDER_PLACED" {
				if ev.Direction == dir {
					evTime := data.NormalizeToIST(ev.EventTime)
					if !evTime.Before(cTime) && evTime.Before(cTime.Add(5*time.Minute)) {
						return true
					}
				}
			}
		}
		return false
	}

	// Dynamic Parameter extraction
	rallyCandles := getIntParam(appCfg.Parameters, 5, "rally_candles", "rally_candles_count")
	minReboundPct := getFloatParam(appCfg.Parameters, 0.50, "min_rebound_pct")
	masterMaxPct := getFloatParam(appCfg.Parameters, 2.00, "master_max_pct")
	masterMaxWickPct := getFloatParam(appCfg.Parameters, 40.0, "master_max_wick_pct")
	maxInsideCandles := getIntParam(appCfg.Parameters, 1, "max_inside_candles")
	confirmMaxPct := getFloatParam(appCfg.Parameters, 1.00, "confirm_max_pct")
	confirmMasterMultiplier := getFloatParam(appCfg.Parameters, 1.50, "confirm_master_multiplier", "es5_confirm_master_multiplier")
	confirmMasterMinSizePct := getFloatParam(appCfg.Parameters, 0.40, "confirm_master_min_size_pct", "es5_confirm_master_min_size_pct")
	emaTouchBufferPct := getFloatParam(appCfg.Parameters, 0.10, "ema_touch_buffer_pct")
	tradeEndTime := "14:30:30"
	if appCfg.TradeEndTime != "" {
		tradeEndTime = data.NormalizeTimeHHMMSS(appCfg.TradeEndTime)
	}
	slBufferPct := getFloatParam(appCfg.Parameters, 0.10, "sl_buffer_pct")
	maxSetupWaitCandles := getIntParam(appCfg.Parameters, 6, "max_setup_wait_candles")
	maxTradesPerStock := getIntParam(appCfg.Parameters, 2, "max_trades_per_stock")
	minPDHPDLRetracePct := getFloatParam(appCfg.Parameters, 0.50, "min_pdh_pdl_retrace_pct")
	arcBounceTolerancePct := getFloatParam(appCfg.Parameters, 0.30, "arc_bounce_tolerance_pct", "es5_arc_bounce_tolerance_pct")
	maxEntryDistancePct := getFloatParam(appCfg.Parameters, 0.35, "max_entry_distance_pct")

	// Instantiate strategy engine to reuse exact U-shape geometry validator
	engine := NewEMAS5BreakoutEngine(a.logger, maxTradesPerStock, rallyCandles, minReboundPct, masterMaxPct, maxInsideCandles, confirmMaxPct)
	engine.SetConfirmMasterMultiplier(confirmMasterMultiplier)
	engine.SetConfirmMasterMinSizePct(confirmMasterMinSizePct)
	engine.SetTradeEndTime(tradeEndTime)
	engine.SetEMATouchBufferPct(emaTouchBufferPct)
	engine.SetMasterMaxWickPct(masterMaxWickPct)
	engine.SetSLBufferPct(slBufferPct)
	engine.SetMaxSetupWaitCandles(maxSetupWaitCandles)
	engine.SetMinPDHPDLRetracePct(minPDHPDLRetracePct)
	engine.SetArcBounceTolerancePct(arcBounceTolerancePct)
	engine.SetMaxEntryDistancePct(maxEntryDistancePct)

	// Extract closes and compute EMA 10 and EMA 20
	closes := make([]float64, len(allCandles))
	for i, c := range allCandles {
		closes[i] = c.Close
	}

	ema10Values := a.indicators.CalculateEMA(closes, 10)
	ema20Values := a.indicators.CalculateEMA(closes, 20)

	// Map times to indices in allCandles
	timeToIndex := make(map[string]int, len(allCandles))
	for i, c := range allCandles {
		timeToIndex[data.NormalizeToIST(c.Time).Format("2006-01-02 15:04:05")] = i
	}

	// Locate start of today session
	todayStartIdx := -1
	for i, c := range allCandles {
		cTimeIST := data.NormalizeToIST(c.Time)
		if cTimeIST.Hour() == 9 && cTimeIST.Minute() >= 15 {
			todayStartIdx = i
			break
		}
	}
	if todayStartIdx < 0 && len(allCandles) > 0 {
		todayStartIdx = 0
	}

	var activeMaster *data.Candle
	var masterDir string
	var insideCount int
	var activeConfirm *data.Candle
	var confirmCandleIdx = -1
	tradesCount := 0

	// Nested Child Setup Tracking
	var childMaster *data.Candle
	var childConfirm *data.Candle
	var childConfirmIdx = -1
	var childInsideCount int

	clearChild := func() {
		childMaster = nil
		childConfirm = nil
		childConfirmIdx = -1
		childInsideCount = 0
	}
	clearAllSetup := func() {
		activeMaster = nil
		activeConfirm = nil
		confirmCandleIdx = -1
		insideCount = 0
		clearChild()
	}

	for _, c := range todayCandles {
		cTimeIST := data.NormalizeToIST(c.Time)
		timeStr := cTimeIST.Format("15:04:05")
		timeDisplay := cTimeIST.Format("15:04")
		key := cTimeIST.Format("2006-01-02 15:04:05")
		idx, ok := timeToIndex[key]
		if !ok {
			continue
		}

		e10 := 0.0
		e20 := 0.0
		if idx >= 0 && idx < len(ema10Values) {
			e10 = ema10Values[idx]
		}
		if idx >= 0 && idx < len(ema20Values) {
			e20 = ema20Values[idx]
		}

		cRange := c.High - c.Low
		rangePct := 0.0
		if c.Close > 0 {
			rangePct = (cRange / c.Close) * 100.0
		}
		body := math.Abs(c.Close - c.Open)
		wickSize := cRange - body
		wickPct := 0.0
		if cRange > 0 {
			wickPct = (wickSize / cRange) * 100.0
		}

		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}

		diag := CandleDiagnosticItem{
			Time:             timeDisplay,
			Open:             c.Open,
			High:             c.High,
			Low:              c.Low,
			Close:            c.Close,
			Volume:           c.Volume,
			Color:            color,
			EMA10:            e10,
			EMA20:            e20,
			RangePct:         rangePct,
			WickPct:          wickPct,
			RejectionReasons: make([]string, 0),
			PassedCriteria:   make([]string, 0),
			Details:          make(map[string]interface{}),
		}

		cTimeCopy := cTimeIST

		// 1. Warmup index check
		if idx < 20 {
			diag.Status = "WARMUP"
			diag.Verdict = "INFO"
			diag.RejectionReasons = append(diag.RejectionReasons, "Indicator warm-up (EMA 10 & 20 requires history)")
			diagnostics = append(diagnostics, diag)
			continue
		}

		// 3. State: Active Confirmation Armed (Awaiting Breakout Trigger)
		if activeMaster != nil && activeConfirm != nil {
			if timeStr >= tradeEndTime {
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "EMAS5_BREAKOUT",
					Stage:      "SETUP_EXPIRED",
					Direction:  masterDir,
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("Entry cutoff time %s IST reached without trigger fill", tradeEndTime),
					Details: map[string]interface{}{
						"cutoff_time": tradeEndTime,
					},
				})
				diag.Status = "EXPIRED"
				diag.Verdict = "REJECTED"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Cutoff time %s IST reached without trigger breakout", tradeEndTime))
				activeMaster = nil
				activeConfirm = nil
				confirmCandleIdx = -1
				insideCount = 0
				diagnostics = append(diagnostics, diag)
				continue
			}

			// Stale Setup Expiry Guard: Invalidate setup if breakout is not triggered within maxSetupWaitCandles
			candlesWaited := idx - confirmCandleIdx
			if maxSetupWaitCandles > 0 && confirmCandleIdx >= 0 && candlesWaited >= maxSetupWaitCandles {
				if childConfirm != nil {
					candlesWaitedChild := idx - childConfirmIdx
					if maxSetupWaitCandles > 0 && childConfirmIdx >= 0 && candlesWaitedChild >= maxSetupWaitCandles {
						events = append(events, data.StrategyEvent{
							EventTime:  cTimeIST,
							Symbol:     symbol,
							Strategy:   "EMAS5_BREAKOUT",
							Stage:      "SETUP_EXPIRED",
							Direction:  masterDir,
							CandleTime: &cTimeCopy,
							Reason:     fmt.Sprintf("Both parent and child setups expired (%d candles waited)", maxSetupWaitCandles),
						})
						diag.Status = "SETUP_EXPIRED"
						diag.Verdict = "REJECTED"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Setup expired: Exceeded %d wait candles without breakout", maxSetupWaitCandles))
						clearAllSetup()
						diagnostics = append(diagnostics, diag)
						continue
					}
					// Promote Child Setup to Parent Setup
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_PROMOTED",
						Direction:  masterDir,
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Parent setup expired after %d candles. Child setup promoted (Trigger ₹%.2f, SL ₹%.2f).", candlesWaited, childConfirm.High, childConfirm.Low),
					})
					activeMaster = childMaster
					activeConfirm = childConfirm
					confirmCandleIdx = childConfirmIdx
					clearChild()
				} else if childMaster != nil {
					// Promote Child Master to Parent Master
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_PROMOTED",
						Direction:  masterDir,
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Parent setup expired after %d candles. Child Master promoted to primary Master (Master High ₹%.2f, Low ₹%.2f). Awaiting confirmation.", candlesWaited, childMaster.High, childMaster.Low),
					})
					activeMaster = childMaster
					activeConfirm = nil
					confirmCandleIdx = -1
					insideCount = childInsideCount
					clearChild()
					diag.Status = "MASTER_ARMED"
					diag.Verdict = "PASS"
					diag.PassedCriteria = append(diag.PassedCriteria, "Parent confirmation expired. Child Master promoted to primary Master.")
					diagnostics = append(diagnostics, diag)
					continue
				} else {
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_EXPIRED",
						Direction:  masterDir,
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Setup expired: Breakout not triggered within %d candles after confirmation (%d candles waited)", maxSetupWaitCandles, candlesWaited),
						Details: map[string]interface{}{
							"candles_waited":   candlesWaited,
							"max_wait_candles": maxSetupWaitCandles,
						},
					})
					diag.Status = "SETUP_EXPIRED"
					diag.Verdict = "REJECTED"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Setup expired: Exceeded %d wait candles without breakout", maxSetupWaitCandles))
					clearAllSetup()
					diagnostics = append(diagnostics, diag)
					continue
				}
			}

			if activeMaster == nil || activeConfirm == nil {
				continue
			}

			if masterDir == "BUY" {
				// Child invalidation: if closed candle breaches Child Master Low without breaching Parent Master Low
				if childMaster != nil && c.Low < childMaster.Low && c.Low >= activeMaster.Low {
					failReason := fmt.Sprintf("Nested Child Master Rejected: Candle Low ₹%.2f breached Child Master Low ₹%.2f. Child setup dropped; Parent confirmation trigger (₹%.2f) remains active.", c.Low, childMaster.Low, activeConfirm.High)
					events = append(events, data.StrategyEvent{
						EventTime:    cTimeIST,
						Symbol:       symbol,
						Strategy:     "EMAS5_BREAKOUT",
						Stage:        "CHILD_SETUP_INVALIDATED",
						Direction:    "BUY",
						CandleTime:   &cTimeCopy,
						CandleOpen:   c.Open,
						CandleHigh:   c.High,
						CandleLow:    c.Low,
						CandleClose:  c.Close,
						CandleVolume: c.Volume,
						Reason:       failReason,
						Details: map[string]interface{}{
							"candle_low":          c.Low,
							"child_master_low":    childMaster.Low,
							"parent_confirm_high": activeConfirm.High,
						},
					})
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Nested Child Master rejected: Candle Low ₹%.2f breached Child Master Low ₹%.2f (Parent confirmation trigger ₹%.2f remains active)", c.Low, childMaster.Low, activeConfirm.High))
					clearChild()
				}

				// Parent Invalidation: if closed candle breaches Master Low before breakout
				if c.Low < activeMaster.Low {
					reason := fmt.Sprintf("Parent Setup Invalidated: Candle Low ₹%.2f breached Master Low support ₹%.2f before breakout trigger", c.Low, activeMaster.Low)
					rejectionReason := fmt.Sprintf("Breached Master Low support ₹%.2f (Candle Low: ₹%.2f)", activeMaster.Low, c.Low)
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "BUY",
						CandleTime: &cTimeCopy,
						Reason:     reason,
						Details: map[string]interface{}{
							"candle_low": c.Low,
							"master_low": activeMaster.Low,
						},
					})
					diag.Status = "INVALIDATED"
					diag.Verdict = "REJECTED"
					diag.RejectionReasons = append(diag.RejectionReasons, rejectionReason)
					clearAllSetup()
					diagnostics = append(diagnostics, diag)
					continue
				}

				// Dual Breakout Trigger Check: Check Child Confirmation first if armed, otherwise Parent Confirmation
				var triggeredSetup *data.Candle
				var triggeredMaster *data.Candle
				if childConfirm != nil && c.High > childConfirm.High {
					triggeredSetup = childConfirm
					triggeredMaster = childMaster
				} else if activeConfirm != nil && c.High > activeConfirm.High {
					triggeredSetup = activeConfirm
					triggeredMaster = activeMaster
				}

				if triggeredSetup != nil {
					realTrade := hasRealTradeOnCandle(cTimeIST, "BUY")
					liveTrigger := hasLiveTriggerOnCandle(cTimeIST, "BUY")

					if realTrade != nil {
						tradesCount++
						events = append(events, data.StrategyEvent{
							EventTime:     cTimeIST,
							Symbol:        symbol,
							Strategy:      "EMAS5_BREAKOUT",
							Stage:         "TRADE_TAKEN",
							Direction:     "BUY",
							TriggerPrice:  realTrade.EntryPrice,
							SLPrice:       triggeredMaster.Low,
							ExecutedPrice: realTrade.EntryPrice,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Live trade executed: %d shares at ₹%.2f (SL: ₹%.2f, PnL: ₹%.2f)", realTrade.Quantity, realTrade.EntryPrice, realTrade.ExitPrice, realTrade.PnL),
							Details: map[string]interface{}{
								"trigger_price": realTrade.EntryPrice,
								"sl_price":      triggeredMaster.Low,
								"pnl":           realTrade.PnL,
								"quantity":      realTrade.Quantity,
							},
						})
						diag.Status = "TRADE_TAKEN"
						diag.Verdict = "PASS"
						diag.Details["action"] = "BUY"
						diag.Details["trigger_price"] = triggeredSetup.High
						diag.Details["entry_price"] = realTrade.EntryPrice
						diag.Details["sl_price"] = triggeredMaster.Low
						diag.Details["pnl"] = realTrade.PnL
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Live trade executed: %d shares @ ₹%.2f", realTrade.Quantity, realTrade.EntryPrice),
							fmt.Sprintf("Initial SL placed at Master Low ₹%.2f", triggeredMaster.Low),
							fmt.Sprintf("Trade PnL: ₹%.2f", realTrade.PnL),
						)
						clearAllSetup()
						diagnostics = append(diagnostics, diag)
						continue
					}

					// If live telemetry exists and no live trade/order occurred:
					// Do not invent a dummy trade! Setup remains armed awaiting live fill.
					if hasLiveEvents && !liveTrigger {
						diag.Status = "AWAITING_TRIGGER"
						diag.Verdict = "ARMED"
						diag.Details["trigger_price"] = triggeredSetup.High
						diag.Details["sl_price"] = triggeredMaster.Low
						diag.Details["candles_waited"] = candlesWaited
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("High ₹%.2f reached trigger high ₹%.2f, but no live order was placed; setup remained armed", c.High, triggeredSetup.High))
						diagnostics = append(diagnostics, diag)
						continue
					}

					// Offline simulation mode: check if trade is possible under current configuration
					slDist := triggeredSetup.High - triggeredMaster.Low
					maxAllowedDistPct := maxEntryDistancePct
					if maxAllowedDistPct <= 0 {
						maxAllowedDistPct = 0.35
					}
					maxAllowedEntryPrice := triggeredSetup.High * (1.0 + maxAllowedDistPct/100.0)
					if c.Open > maxAllowedEntryPrice {
						events = append(events, data.StrategyEvent{
							EventTime:    cTimeIST,
							Symbol:       symbol,
							Strategy:     "EMAS5_BREAKOUT",
							Stage:        "TRADE_SKIPPED",
							Direction:    "BUY",
							TriggerPrice: triggeredSetup.High,
							SLPrice:      triggeredMaster.Low,
							CandleTime:   &cTimeCopy,
							Reason:       fmt.Sprintf("Breakout skipped: Entry price ₹%.2f exceeds max entry distance (%.2f%% above trigger ₹%.2f)", c.Open, maxAllowedDistPct, triggeredSetup.High),
							Details: map[string]interface{}{
								"trigger_price": triggeredSetup.High,
								"sl_price":      triggeredMaster.Low,
								"candle_open":   c.Open,
								"max_allowed":   maxAllowedEntryPrice,
							},
						})
						diag.Status = "TRADE_SKIPPED"
						diag.Verdict = "REJECTED"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Trade skipped: Entry price ₹%.2f exceeds max entry distance (%.2f%% above trigger ₹%.2f)", c.Open, maxAllowedDistPct, triggeredSetup.High))
						diag.Details["trigger_price"] = triggeredSetup.High
						diag.Details["sl_price"] = triggeredMaster.Low
						clearAllSetup()
						diagnostics = append(diagnostics, diag)
						continue
					}
					if tradesCount >= maxTradesPerStock {
						events = append(events, data.StrategyEvent{
							EventTime:     cTimeIST,
							Symbol:        symbol,
							Strategy:      "EMAS5_BREAKOUT",
							Stage:         "TRADE_SKIPPED",
							Direction:     "BUY",
							TriggerPrice:  triggeredSetup.High,
							SLPrice:       triggeredMaster.Low,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Breakout triggered above Confirmation High ₹%.2f (Skipped: max trades per stock %d/%d reached)", triggeredSetup.High, tradesCount, maxTradesPerStock),
							Details: map[string]interface{}{
								"trigger_price": triggeredSetup.High,
								"sl_price":      triggeredMaster.Low,
								"max_trades":    maxTradesPerStock,
								"trades_count":  tradesCount,
							},
						})
						diag.Status = "TRADE_SKIPPED"
						diag.Verdict = "REJECTED"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Trade not possible: Max trades per stock reached (%d/%d executed)", tradesCount, maxTradesPerStock))
						diag.Details["trigger_price"] = triggeredSetup.High
						diag.Details["sl_price"] = triggeredMaster.Low
					} else {
						events = append(events, data.StrategyEvent{
							EventTime:     cTimeIST,
							Symbol:        symbol,
							Strategy:      "EMAS5_BREAKOUT",
							Stage:         "TRADE_TAKEN",
							Direction:     "BUY",
							TriggerPrice:  triggeredSetup.High,
							SLPrice:       triggeredMaster.Low,
							ExecutedPrice: triggeredSetup.High,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Breakout triggered above Confirmation High ₹%.2f", triggeredSetup.High),
							Details: map[string]interface{}{
								"trigger_price": triggeredSetup.High,
								"sl_price":      triggeredMaster.Low,
								"target_1":      triggeredSetup.High + (slDist * 1.5),
							},
						})
						tradesCount++
						diag.Status = "BREAKOUT_TRIGGERED"
						diag.Verdict = "PASS"
						diag.Details["trigger_price"] = triggeredSetup.High
						diag.Details["sl_price"] = triggeredMaster.Low
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Breakout triggered: High ₹%.2f broke Confirmation High ₹%.2f", c.High, triggeredSetup.High),
							fmt.Sprintf("Buy Entry @ ₹%.2f, SL @ ₹%.2f", triggeredSetup.High, triggeredMaster.Low),
							fmt.Sprintf("Target 1 (1:1.5 RR): ₹%.2f", triggeredSetup.High+(slDist*1.5)),
						)
					}
					clearAllSetup()
					diagnostics = append(diagnostics, diag)
					continue
				} else {
					// Check if a fresh Master candle formed during waiting period
					ema10Upper := e10 * (1.0 + emaTouchBufferPct/100.0)
					ema10Lower := e10 * (1.0 - emaTouchBufferPct/100.0)
					ema20Upper := e20 * (1.0 + emaTouchBufferPct/100.0)
					ema20Lower := e20 * (1.0 - emaTouchBufferPct/100.0)
					touchesEMABuy := (c.Low <= ema10Upper && c.High >= ema10Lower) ||
						(c.Low <= ema20Upper && c.High >= ema20Lower)
					touchesPDH := false
					if summary.PDH > 0 {
						pdhUpper := summary.PDH * (1.0 + emaTouchBufferPct/100.0)
						pdhLower := summary.PDH * (1.0 - emaTouchBufferPct/100.0)
						touchesPDH = c.Low <= pdhUpper && c.High >= pdhLower
					}
					closesAboveAll := c.Close > e10 && c.Close > e20
					if summary.PDH > 0 && c.Close <= summary.PDH {
						closesAboveAll = false
					}
					if c.Close > c.Open && (touchesEMABuy || touchesPDH) && closesAboveAll && rangePct <= masterMaxPct && wickPct <= masterMaxWickPct {
						isValid, lowestLow, candlesSinceLowest, reboundPct, pdhRetracePct := engine.validateBuyUShape(allCandles, idx, summary.PDH, e10, e20)
						if isValid {
							if activeConfirm != nil && c.High > activeConfirm.High {
								// Expanding Master outside parent confirmation: supersedes!
								reason := fmt.Sprintf("Master Re-anchored: Candle High ₹%.2f broke above previous confirmation trigger ₹%.2f and qualified as a new expanding primary Master candle (+%.2f%% rebound from trough ₹%.2f)", c.High, activeConfirm.High, reboundPct, lowestLow)
								events = append(events, data.StrategyEvent{
									EventTime:    cTimeIST,
									Symbol:       symbol,
									Strategy:     "EMAS5_BREAKOUT",
									Stage:        "SETUP_SUPERSEDED",
									Direction:    "BUY",
									TriggerPrice: c.High,
									SLPrice:      c.Low,
									CandleTime:   &cTimeCopy,
									CandleOpen:   c.Open,
									CandleHigh:   c.High,
									CandleLow:    c.Low,
									CandleClose:  c.Close,
									CandleVolume: c.Volume,
									Reason:       reason,
									Details: map[string]interface{}{
										"prev_trigger":          activeConfirm.High,
										"prev_sl":               activeMaster.Low,
										"new_master_high":       c.High,
										"new_master_low":        c.Low,
										"rebound_pct":           reboundPct,
										"pdh_retrace_pct":       pdhRetracePct,
										"lowest_low":            lowestLow,
										"candles_since_lowest":  candlesSinceLowest,
									},
								})
								cCopy := c
								activeMaster = &cCopy
								activeConfirm = nil
								confirmCandleIdx = -1
								insideCount = 0
								masterDir = "BUY"
								clearChild()
								diag.Status = "MASTER_REANCHORED"
								diag.Verdict = "PASS"
								diag.Details["lowest_low"] = lowestLow
								diag.Details["rebound_pct"] = reboundPct
								diag.Details["pdh_retrace_pct"] = pdhRetracePct
								diag.Details["candles_since_highest"] = candlesSinceLowest
								diag.PassedCriteria = append(diag.PassedCriteria,
									fmt.Sprintf("Master Re-anchored: Candle broke above pending confirmation trigger ₹%.2f and qualified as stronger primary Master", activeConfirm.High),
									fmt.Sprintf("Bullish GREEN candle (Close ₹%.2f > Open ₹%.2f)", c.Close, c.Open),
									fmt.Sprintf("Range %.2f%% <= max %.2f%%, Wick %.2f%% <= max %.2f%%", rangePct, masterMaxPct, wickPct, masterMaxWickPct),
									fmt.Sprintf("Closed above EMA10 (₹%.2f), EMA20 (₹%.2f) & PDH (₹%.2f)", e10, e20, summary.PDH),
									fmt.Sprintf("U-Shape rebound +%.2f%% from trough ₹%.2f", reboundPct, lowestLow),
								)
								diagnostics = append(diagnostics, diag)
								continue
							} else if c.Low >= activeMaster.Low {
								// Inside Master: forms Child Master without deleting activeConfirm!
								cCopy := c
								childMaster = &cCopy
								childConfirm = nil
								childConfirmIdx = -1
								childInsideCount = 0
								reason := fmt.Sprintf("Nested Master Formed: Candle formed inside parent range [Parent Low ₹%.2f - Parent Trigger ₹%.2f] meeting all Master criteria (+%.2f%% rebound from trough ₹%.2f). Parent confirmation (Trigger ₹%.2f) preserved.", activeMaster.Low, activeConfirm.High, reboundPct, lowestLow, activeConfirm.High)
								events = append(events, data.StrategyEvent{
									EventTime:    cTimeIST,
									Symbol:       symbol,
									Strategy:     "EMAS5_BREAKOUT",
									Stage:        "NESTED_MASTER_FORMED",
									Direction:    "BUY",
									TriggerPrice: c.High,
									SLPrice:      c.Low,
									CandleTime:   &cTimeCopy,
									CandleOpen:   c.Open,
									CandleHigh:   c.High,
									CandleLow:    c.Low,
									CandleClose:  c.Close,
									CandleVolume: c.Volume,
									Reason:       reason,
									Details: map[string]interface{}{
										"child_master_high":   c.High,
										"child_master_low":    c.Low,
										"parent_confirm_high": activeConfirm.High,
										"parent_master_low":   activeMaster.Low,
										"rebound_pct":         reboundPct,
										"pdh_retrace_pct":     pdhRetracePct,
										"lowest_low":          lowestLow,
									},
								})
								diag.Status = "NESTED_MASTER_FORMED"
								diag.Verdict = "PASS"
								diag.Details["child_master_high"] = c.High
								diag.Details["child_master_low"] = c.Low
								diag.Details["parent_confirm_high"] = activeConfirm.High
								diag.PassedCriteria = append(diag.PassedCriteria,
									fmt.Sprintf("Nested Child Master formed inside parent range [₹%.2f - ₹%.2f]", activeMaster.Low, activeConfirm.High),
									fmt.Sprintf("Parent confirmation trigger ₹%.2f preserved and active", activeConfirm.High),
									fmt.Sprintf("Bullish GREEN candle (Close ₹%.2f > Open ₹%.2f)", c.Close, c.Open),
									fmt.Sprintf("Range %.2f%% <= max %.2f%%, Wick %.2f%% <= max %.2f%%", rangePct, masterMaxPct, wickPct, masterMaxWickPct),
									fmt.Sprintf("Child range [₹%.2f - ₹%.2f] armed for subsequent Child Confirmation", c.Low, c.High),
								)
								diagnostics = append(diagnostics, diag)
								continue
							}
						}
					}

					// If not a new Master, test if candle qualifies as Child Confirmation or increments child inside count
					if childMaster != nil && childConfirm == nil {
						if c.High > childMaster.High {
							confirmRangePct := rangePct
							masterSize := childMaster.High - childMaster.Low
							confirmSize := c.High - c.Low
							sizeMultiplierValid := true
							if confirmRangePct > confirmMasterMinSizePct && confirmMasterMultiplier > 0 && masterSize > 0 {
								sizeMultiplierValid = confirmSize <= (masterSize*confirmMasterMultiplier + 1e-6)
							}
							if c.Close > c.Open && c.Low >= childMaster.Low && confirmRangePct <= confirmMaxPct && sizeMultiplierValid {
								cCopy := c
								childConfirm = &cCopy
								childConfirmIdx = idx
								reason := fmt.Sprintf("Child Confirmation Armed: Buy trigger set above Child High ₹%.2f, SL @ Child Master Low ₹%.2f (Size ₹%.2f, %.2fx of Child Master ₹%.2f). Parent confirmation (Trigger ₹%.2f) also active.", c.High, childMaster.Low, confirmSize, confirmSize/masterSize, masterSize, activeConfirm.High)
								events = append(events, data.StrategyEvent{
									EventTime:    cTimeIST,
									Symbol:       symbol,
									Strategy:     "EMAS5_BREAKOUT",
									Stage:        "CHILD_CONFIRMATION_ARMED",
									Direction:    "BUY",
									TriggerPrice: c.High,
									SLPrice:      childMaster.Low,
									CandleTime:   &cTimeCopy,
									CandleOpen:   c.Open,
									CandleHigh:   c.High,
									CandleLow:    c.Low,
									CandleClose:  c.Close,
									CandleVolume: c.Volume,
									Reason:       reason,
									Details: map[string]interface{}{
										"child_trigger":       c.High,
										"child_sl":            childMaster.Low,
										"parent_confirm_high": activeConfirm.High,
										"confirm_size":        confirmSize,
										"master_size":         masterSize,
										"range_pct":           confirmRangePct,
									},
								})
								diag.Status = "CHILD_CONFIRMATION_ARMED"
								diag.Verdict = "PASS"
								diag.Details["direction"] = "BUY"
								diag.PassedCriteria = append(diag.PassedCriteria,
									fmt.Sprintf("Child Confirmation armed: Buy above Child High ₹%.2f, SL @ ₹%.2f", c.High, childMaster.Low),
									fmt.Sprintf("High ₹%.2f broke Child Master High ₹%.2f", c.High, childMaster.High),
									fmt.Sprintf("Low ₹%.2f held above Child Master Low ₹%.2f", c.Low, childMaster.Low),
									fmt.Sprintf("Bullish GREEN candle (Close ₹%.2f > Open ₹%.2f)", c.Close, c.Open),
									fmt.Sprintf("Range %.2f%% <= max %.2f%%, Size %.2fx <= %.2fx", confirmRangePct, confirmMaxPct, confirmSize/masterSize, confirmMasterMultiplier),
									fmt.Sprintf("Parent confirmation trigger ₹%.2f preserved and active", activeConfirm.High),
								)
								diagnostics = append(diagnostics, diag)
								continue
							} else {
								var childFailReasons []string
								if c.Close <= c.Open {
									childFailReasons = append(childFailReasons, fmt.Sprintf("Candle is not GREEN (Close ₹%.2f <= Open ₹%.2f)", c.Close, c.Open))
								}
								if c.Low < childMaster.Low {
									childFailReasons = append(childFailReasons, fmt.Sprintf("Low ₹%.2f breached Child Master Low ₹%.2f", c.Low, childMaster.Low))
								}
								if confirmRangePct > confirmMaxPct {
									childFailReasons = append(childFailReasons, fmt.Sprintf("Range %.2f%% exceeded max %.2f%%", confirmRangePct, confirmMaxPct))
								}
								if !sizeMultiplierValid {
									childFailReasons = append(childFailReasons, fmt.Sprintf("Size ₹%.2f exceeded %.2fx Child Master size ₹%.2f (max ₹%.2f)", confirmSize, confirmMasterMultiplier, masterSize, masterSize*confirmMasterMultiplier))
								}
								failStr := strings.Join(childFailReasons, "; ")
								events = append(events, data.StrategyEvent{
									EventTime:    cTimeIST,
									Symbol:       symbol,
									Strategy:     "EMAS5_BREAKOUT",
									Stage:        "CHILD_CONFIRMATION_REJECTED",
									Direction:    "BUY",
									CandleTime:   &cTimeCopy,
									CandleOpen:   c.Open,
									CandleHigh:   c.High,
									CandleLow:    c.Low,
									CandleClose:  c.Close,
									CandleVolume: c.Volume,
									Reason:       fmt.Sprintf("Child Confirmation Rejected: Broke Child Master High ₹%.2f but failed confirmation criteria (%s). Child setup dropped; Parent confirmation (Trigger ₹%.2f) remains active.", childMaster.High, failStr, activeConfirm.High),
									Details: map[string]interface{}{
										"child_master_high":   childMaster.High,
										"parent_confirm_high": activeConfirm.High,
										"rejection_reasons":   childFailReasons,
									},
								})
								diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Child Confirmation rejected: %s (Parent trigger ₹%.2f preserved)", failStr, activeConfirm.High))
								clearChild()
							}
						} else if c.High <= childMaster.High && c.Low >= childMaster.Low {
							childInsideCount++
							if childInsideCount > maxInsideCandles {
								events = append(events, data.StrategyEvent{
									EventTime:    cTimeIST,
									Symbol:       symbol,
									Strategy:     "EMAS5_BREAKOUT",
									Stage:        "CHILD_SETUP_INVALIDATED",
									Direction:    "BUY",
									CandleTime:   &cTimeCopy,
									Reason:       fmt.Sprintf("Nested Child Master Rejected: Exceeded maximum inside consolidation candles (%d > %d allowed). Child setup dropped; Parent confirmation (Trigger ₹%.2f) remains active.", childInsideCount, maxInsideCandles, activeConfirm.High),
									Details: map[string]interface{}{
										"child_inside_count":  childInsideCount,
										"max_inside_allowed":  maxInsideCandles,
										"parent_confirm_high": activeConfirm.High,
									},
								})
								diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Nested Child Master dropped: %d inside consolidation candles exceeded max %d allowed (Parent trigger ₹%.2f preserved)", childInsideCount, maxInsideCandles, activeConfirm.High))
								clearChild()
							}
						}
					}
					// Also check if trend reversed to SELL Master candidate during waiting window
					touchesEMASell := (c.High >= ema10Lower && c.Low <= ema10Upper) ||
						(c.High >= ema20Lower && c.Low <= ema20Upper)
					touchesPDL := false
					if summary.PDL > 0 {
						pdlUpper := summary.PDL * (1.0 + emaTouchBufferPct/100.0)
						pdlLower := summary.PDL * (1.0 - emaTouchBufferPct/100.0)
						touchesPDL = c.High >= pdlLower && c.Low <= pdlUpper
					}
					closesBelowAll := c.Close < e10 && c.Close < e20
					if summary.PDL > 0 && c.Close >= summary.PDL {
						closesBelowAll = false
					}
					if c.Close < c.Open && (touchesEMASell || touchesPDL) && closesBelowAll && rangePct <= masterMaxPct && wickPct <= masterMaxWickPct {
						isValid, highestHigh, candlesSinceHighest, dropPct, pdlRetracePct := engine.validateSellInvertedUShape(allCandles, idx, summary.PDL, e10, e20)
						if isValid {
							events = append(events, data.StrategyEvent{
								EventTime:    cTimeIST,
								Symbol:       symbol,
								Strategy:     "EMAS5_BREAKOUT",
								Stage:        "SETUP_SUPERSEDED",
								Direction:    "SELL",
								TriggerPrice: c.Low,
								SLPrice:      c.High,
								CandleTime:   &cTimeCopy,
								CandleOpen:   c.Open,
								CandleHigh:   c.High,
								CandleLow:    c.Low,
								CandleClose:  c.Close,
								CandleVolume: c.Volume,
								Reason:       fmt.Sprintf("Pending BUY setup superseded by reversal SELL Master candle at %s (High ₹%.2f, Low ₹%.2f)", cTimeIST.Format("15:04"), c.High, c.Low),
								Details: map[string]interface{}{
									"prev_trigger":          activeConfirm.High,
									"prev_sl":               activeMaster.Low,
									"new_master_high":       c.High,
									"new_master_low":        c.Low,
									"drop_pct":              dropPct,
									"pdl_retrace_pct":       pdlRetracePct,
									"highest_high":          highestHigh,
									"candles_since_highest": candlesSinceHighest,
								},
							})
							cCopy := c
							activeMaster = &cCopy
							activeConfirm = nil
							confirmCandleIdx = -1
							insideCount = 0
							masterDir = "SELL"
							diag.Status = "MASTER_REANCHORED"
							diag.Verdict = "PASS"
							diag.Details["highest_high"] = highestHigh
							diag.Details["drop_pct"] = dropPct
							diag.Details["pdl_retrace_pct"] = pdlRetracePct
							diag.Details["candles_since_highest"] = candlesSinceHighest
							diag.PassedCriteria = append(diag.PassedCriteria,
								"Pending BUY setup superseded: Trend reversed, fresh SELL Master Candle formed during waiting window",
								fmt.Sprintf("Bearish RED candle (Close ₹%.2f < Open ₹%.2f)", c.Close, c.Open),
								fmt.Sprintf("Range %.2f%% <= max %.2f%%, Wick %.2f%% <= max %.2f%%", rangePct, masterMaxPct, wickPct, masterMaxWickPct),
								fmt.Sprintf("Closed below EMA10 (₹%.2f), EMA20 (₹%.2f) & PDL (₹%.2f)", e10, e20, summary.PDL),
								fmt.Sprintf("Inverted U-Shape drop -%.2f%% from peak ₹%.2f", dropPct, highestHigh),
							)
							diagnostics = append(diagnostics, diag)
							continue
						}
					}

					// Still armed and waiting for breakout trigger
					diag.Status = "AWAITING_TRIGGER"
					diag.Verdict = "ARMED"
					diag.Details["trigger_price"] = activeConfirm.High
					diag.Details["sl_price"] = activeMaster.Low
					diag.Details["candles_waited"] = candlesWaited
					diagnostics = append(diagnostics, diag)
					continue
				}

			} else if masterDir == "SELL" {
				// Child invalidation: if closed candle breaches Child Master High without breaching Parent Master High
				if childMaster != nil && c.High > childMaster.High && c.High <= activeMaster.High {
					failReason := fmt.Sprintf("Nested Child Master Rejected: Candle High ₹%.2f breached Child Master High ₹%.2f. Child setup dropped; Parent confirmation trigger (₹%.2f) remains active.", c.High, childMaster.High, activeConfirm.Low)
					events = append(events, data.StrategyEvent{
						EventTime:    cTimeIST,
						Symbol:       symbol,
						Strategy:     "EMAS5_BREAKOUT",
						Stage:        "CHILD_SETUP_INVALIDATED",
						Direction:    "SELL",
						CandleTime:   &cTimeCopy,
						CandleOpen:   c.Open,
						CandleHigh:   c.High,
						CandleLow:    c.Low,
						CandleClose:  c.Close,
						CandleVolume: c.Volume,
						Reason:       failReason,
						Details: map[string]interface{}{
							"candle_high":        c.High,
							"child_master_high":  childMaster.High,
							"parent_confirm_low": activeConfirm.Low,
						},
					})
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Nested Child Master rejected: Candle High ₹%.2f breached Child Master High ₹%.2f (Parent confirmation trigger ₹%.2f remains active)", c.High, childMaster.High, activeConfirm.Low))
					clearChild()
				}

				// Parent Invalidation: if closed candle breaches Master High before breakdown
				if c.High > activeMaster.High {
					reason := fmt.Sprintf("Parent Setup Invalidated: Candle High ₹%.2f breached Master High resistance ₹%.2f before breakdown trigger", c.High, activeMaster.High)
					rejectionReason := fmt.Sprintf("Breached Master High resistance ₹%.2f (Candle High: ₹%.2f)", activeMaster.High, c.High)
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "SELL",
						CandleTime: &cTimeCopy,
						Reason:     reason,
						Details: map[string]interface{}{
							"candle_high": c.High,
							"master_high": activeMaster.High,
						},
					})
					diag.Status = "INVALIDATED"
					diag.Verdict = "REJECTED"
					diag.RejectionReasons = append(diag.RejectionReasons, rejectionReason)
					clearAllSetup()
					diagnostics = append(diagnostics, diag)
					continue
				}

				// Dual Breakdown Trigger Check: Check Child Confirmation first if armed, otherwise Parent Confirmation
				var triggeredSetup *data.Candle
				var triggeredMaster *data.Candle
				if childConfirm != nil && c.Low < childConfirm.Low {
					triggeredSetup = childConfirm
					triggeredMaster = childMaster
				} else if activeConfirm != nil && c.Low < activeConfirm.Low {
					triggeredSetup = activeConfirm
					triggeredMaster = activeMaster
				}

				if triggeredSetup != nil {
					realTrade := hasRealTradeOnCandle(cTimeIST, "SELL")
					liveTrigger := hasLiveTriggerOnCandle(cTimeIST, "SELL")

					if realTrade != nil {
						tradesCount++
						events = append(events, data.StrategyEvent{
							EventTime:     cTimeIST,
							Symbol:        symbol,
							Strategy:      "EMAS5_BREAKOUT",
							Stage:         "TRADE_TAKEN",
							Direction:     "SELL",
							TriggerPrice:  realTrade.EntryPrice,
							SLPrice:       triggeredMaster.High,
							ExecutedPrice: realTrade.EntryPrice,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Live trade executed: %d shares at ₹%.2f (SL: ₹%.2f, PnL: ₹%.2f)", realTrade.Quantity, realTrade.EntryPrice, realTrade.ExitPrice, realTrade.PnL),
							Details: map[string]interface{}{
								"trigger_price": realTrade.EntryPrice,
								"sl_price":      triggeredMaster.High,
								"pnl":           realTrade.PnL,
								"quantity":      realTrade.Quantity,
							},
						})
						diag.Status = "TRADE_TAKEN"
						diag.Verdict = "PASS"
						diag.Details["action"] = "SELL"
						diag.Details["trigger_price"] = triggeredSetup.Low
						diag.Details["entry_price"] = realTrade.EntryPrice
						diag.Details["sl_price"] = triggeredMaster.High
						diag.Details["pnl"] = realTrade.PnL
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Live trade executed: %d shares @ ₹%.2f", realTrade.Quantity, realTrade.EntryPrice),
							fmt.Sprintf("Initial SL placed at Master High ₹%.2f", triggeredMaster.High),
							fmt.Sprintf("Trade PnL: ₹%.2f", realTrade.PnL),
						)
						clearAllSetup()
						diagnostics = append(diagnostics, diag)
						continue
					}

					// If live telemetry exists and no live trade/order occurred:
					// Do not invent a dummy trade! Setup remains armed awaiting live fill.
					if hasLiveEvents && !liveTrigger {
						diag.Status = "AWAITING_TRIGGER"
						diag.Verdict = "ARMED"
						diag.Details["trigger_price"] = triggeredSetup.Low
						diag.Details["sl_price"] = triggeredMaster.High
						diag.Details["candles_waited"] = candlesWaited
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Price reached trigger low ₹%.2f (low ₹%.2f) but no live order was placed; setup remained armed", triggeredSetup.Low, c.Low))
						diagnostics = append(diagnostics, diag)
						continue
					}

					// Offline simulation mode: check if trade is possible under current configuration
					slDist := triggeredMaster.High - triggeredSetup.Low
					maxAllowedDistPct := maxEntryDistancePct
					if maxAllowedDistPct <= 0 {
						maxAllowedDistPct = 0.35
					}
					minAllowedEntryPrice := triggeredSetup.Low * (1.0 - maxAllowedDistPct/100.0)
					if c.Open < minAllowedEntryPrice {
						events = append(events, data.StrategyEvent{
							EventTime:    cTimeIST,
							Symbol:       symbol,
							Strategy:     "EMAS5_BREAKOUT",
							Stage:        "TRADE_SKIPPED",
							Direction:    "SELL",
							TriggerPrice: triggeredSetup.Low,
							SLPrice:      triggeredMaster.High,
							CandleTime:   &cTimeCopy,
							Reason:       fmt.Sprintf("Breakdown skipped: Entry price ₹%.2f falls below max entry distance (%.2f%% below trigger ₹%.2f)", c.Open, maxAllowedDistPct, triggeredSetup.Low),
							Details: map[string]interface{}{
								"trigger_price": triggeredSetup.Low,
								"sl_price":      triggeredMaster.High,
								"candle_open":   c.Open,
								"min_allowed":   minAllowedEntryPrice,
							},
						})
						diag.Status = "TRADE_SKIPPED"
						diag.Verdict = "REJECTED"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Trade skipped: Entry price ₹%.2f falls below max entry distance (%.2f%% below trigger ₹%.2f)", c.Open, maxAllowedDistPct, triggeredSetup.Low))
						diag.Details["trigger_price"] = triggeredSetup.Low
						diag.Details["sl_price"] = triggeredMaster.High
						clearAllSetup()
						diagnostics = append(diagnostics, diag)
						continue
					}
					if tradesCount >= maxTradesPerStock {
						events = append(events, data.StrategyEvent{
							EventTime:     cTimeIST,
							Symbol:        symbol,
							Strategy:      "EMAS5_BREAKOUT",
							Stage:         "TRADE_SKIPPED",
							Direction:     "SELL",
							TriggerPrice:  triggeredSetup.Low,
							SLPrice:       triggeredMaster.High,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Breakdown triggered below Confirmation Low ₹%.2f (Skipped: max trades per stock %d/%d reached)", triggeredSetup.Low, tradesCount, maxTradesPerStock),
							Details: map[string]interface{}{
								"trigger_price": triggeredSetup.Low,
								"sl_price":      triggeredMaster.High,
								"max_trades":    maxTradesPerStock,
								"trades_count":  tradesCount,
							},
						})
						diag.Status = "TRADE_SKIPPED"
						diag.Verdict = "REJECTED"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Trade not possible: Max trades per stock reached (%d/%d executed)", tradesCount, maxTradesPerStock))
						diag.Details["trigger_price"] = triggeredSetup.Low
						diag.Details["sl_price"] = triggeredMaster.High
					} else {
						events = append(events, data.StrategyEvent{
							EventTime:     cTimeIST,
							Symbol:        symbol,
							Strategy:      "EMAS5_BREAKOUT",
							Stage:         "TRADE_TAKEN",
							Direction:     "SELL",
							TriggerPrice:  triggeredSetup.Low,
							SLPrice:       triggeredMaster.High,
							ExecutedPrice: triggeredSetup.Low,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Breakdown triggered below Confirmation Low ₹%.2f", triggeredSetup.Low),
							Details: map[string]interface{}{
								"trigger_price": triggeredSetup.Low,
								"sl_price":      triggeredMaster.High,
								"target_1":      triggeredSetup.Low - (slDist * 1.5),
							},
						})
						tradesCount++
						diag.Status = "BREAKDOWN_TRIGGERED"
						diag.Verdict = "PASS"
						diag.Details["trigger_price"] = triggeredSetup.Low
						diag.Details["sl_price"] = triggeredMaster.High
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Breakdown triggered: Low ₹%.2f broke Confirmation Low ₹%.2f", c.Low, triggeredSetup.Low),
							fmt.Sprintf("Sell Entry @ ₹%.2f, SL @ ₹%.2f", triggeredSetup.Low, triggeredMaster.High),
							fmt.Sprintf("Target 1 (1:1.5 RR): ₹%.2f", triggeredSetup.Low-(slDist*1.5)),
						)
					}
					clearAllSetup()
					diagnostics = append(diagnostics, diag)
					continue
				} else {
					// Check if a fresh Master candle formed during waiting period
					ema10Upper := e10 * (1.0 + emaTouchBufferPct/100.0)
					ema10Lower := e10 * (1.0 - emaTouchBufferPct/100.0)
					ema20Upper := e20 * (1.0 + emaTouchBufferPct/100.0)
					ema20Lower := e20 * (1.0 - emaTouchBufferPct/100.0)
					touchesEMASell := (c.High >= ema10Lower && c.Low <= ema10Upper) ||
						(c.High >= ema20Lower && c.Low <= ema20Upper)
					touchesPDL := false
					if summary.PDL > 0 {
						pdlUpper := summary.PDL * (1.0 + emaTouchBufferPct/100.0)
						pdlLower := summary.PDL * (1.0 - emaTouchBufferPct/100.0)
						touchesPDL = c.High >= pdlLower && c.Low <= pdlUpper
					}
					closesBelowAll := c.Close < e10 && c.Close < e20
					if summary.PDL > 0 && c.Close >= summary.PDL {
						closesBelowAll = false
					}
					if c.Close < c.Open && (touchesEMASell || touchesPDL) && closesBelowAll && rangePct <= masterMaxPct && wickPct <= masterMaxWickPct {
						isValid, highestHigh, candlesSinceHighest, dropPct, pdlRetracePct := engine.validateSellInvertedUShape(allCandles, idx, summary.PDL, e10, e20)
						if isValid {
							if activeConfirm != nil && c.Low < activeConfirm.Low {
								// Expanding Master outside parent confirmation: supersedes!
								reason := fmt.Sprintf("Master Re-anchored: Candle Low ₹%.2f broke below previous confirmation trigger ₹%.2f and qualified as a new expanding primary Master candle (-%.2f%% drop from peak ₹%.2f)", c.Low, activeConfirm.Low, dropPct, highestHigh)
								events = append(events, data.StrategyEvent{
									EventTime:    cTimeIST,
									Symbol:       symbol,
									Strategy:     "EMAS5_BREAKOUT",
									Stage:        "SETUP_SUPERSEDED",
									Direction:    "SELL",
									TriggerPrice: c.Low,
									SLPrice:      c.High,
									CandleTime:   &cTimeCopy,
									CandleOpen:   c.Open,
									CandleHigh:   c.High,
									CandleLow:    c.Low,
									CandleClose:  c.Close,
									CandleVolume: c.Volume,
									Reason:       reason,
									Details: map[string]interface{}{
										"prev_trigger":          activeConfirm.Low,
										"prev_sl":               activeMaster.High,
										"new_master_high":       c.High,
										"new_master_low":        c.Low,
										"drop_pct":              dropPct,
										"pdl_retrace_pct":       pdlRetracePct,
										"highest_high":          highestHigh,
										"candles_since_highest": candlesSinceHighest,
									},
								})
								cCopy := c
								activeMaster = &cCopy
								activeConfirm = nil
								confirmCandleIdx = -1
								insideCount = 0
								masterDir = "SELL"
								clearChild()
								diag.Status = "MASTER_REANCHORED"
								diag.Verdict = "PASS"
								diag.Details["highest_high"] = highestHigh
								diag.Details["drop_pct"] = dropPct
								diag.Details["pdl_retrace_pct"] = pdlRetracePct
								diag.Details["candles_since_highest"] = candlesSinceHighest
								diag.PassedCriteria = append(diag.PassedCriteria,
									fmt.Sprintf("Master Re-anchored: Candle broke below pending confirmation trigger ₹%.2f and qualified as stronger primary Master", activeConfirm.Low),
									fmt.Sprintf("Bearish RED candle (Close ₹%.2f < Open ₹%.2f)", c.Close, c.Open),
									fmt.Sprintf("Range %.2f%% <= max %.2f%%, Wick %.2f%% <= max %.2f%%", rangePct, masterMaxPct, wickPct, masterMaxWickPct),
									fmt.Sprintf("Closed below EMA10 (₹%.2f), EMA20 (₹%.2f) & PDL (₹%.2f)", e10, e20, summary.PDL),
									fmt.Sprintf("Inverted U-Shape drop -%.2f%% from peak ₹%.2f", dropPct, highestHigh),
								)
								diagnostics = append(diagnostics, diag)
								continue
							} else if c.High <= activeMaster.High {
								// Inside Master: forms Child Master without deleting activeConfirm!
								cCopy := c
								childMaster = &cCopy
								childConfirm = nil
								childConfirmIdx = -1
								childInsideCount = 0
								reason := fmt.Sprintf("Nested Master Formed: Candle formed inside parent range [Parent Trigger ₹%.2f - Parent High ₹%.2f] meeting all Master criteria (-%.2f%% drop from peak ₹%.2f). Parent confirmation (Trigger ₹%.2f) preserved.", activeConfirm.Low, activeMaster.High, dropPct, highestHigh, activeConfirm.Low)
								events = append(events, data.StrategyEvent{
									EventTime:    cTimeIST,
									Symbol:       symbol,
									Strategy:     "EMAS5_BREAKOUT",
									Stage:        "NESTED_MASTER_FORMED",
									Direction:    "SELL",
									TriggerPrice: c.Low,
									SLPrice:      c.High,
									CandleTime:   &cTimeCopy,
									CandleOpen:   c.Open,
									CandleHigh:   c.High,
									CandleLow:    c.Low,
									CandleClose:  c.Close,
									CandleVolume: c.Volume,
									Reason:       reason,
									Details: map[string]interface{}{
										"child_master_high":  c.High,
										"child_master_low":   c.Low,
										"parent_confirm_low": activeConfirm.Low,
										"parent_master_high": activeMaster.High,
										"drop_pct":           dropPct,
										"pdl_retrace_pct":    pdlRetracePct,
										"highest_high":       highestHigh,
									},
								})
								diag.Status = "NESTED_MASTER_FORMED"
								diag.Verdict = "PASS"
								diag.Details["child_master_high"] = c.High
								diag.Details["child_master_low"] = c.Low
								diag.Details["parent_confirm_low"] = activeConfirm.Low
								diag.PassedCriteria = append(diag.PassedCriteria,
									fmt.Sprintf("Nested Child Master formed inside parent range [₹%.2f - ₹%.2f]", activeConfirm.Low, activeMaster.High),
									fmt.Sprintf("Parent confirmation trigger ₹%.2f preserved and active", activeConfirm.Low),
									fmt.Sprintf("Bearish RED candle (Close ₹%.2f < Open ₹%.2f)", c.Close, c.Open),
									fmt.Sprintf("Range %.2f%% <= max %.2f%%, Wick %.2f%% <= max %.2f%%", rangePct, masterMaxPct, wickPct, masterMaxWickPct),
									fmt.Sprintf("Child range [₹%.2f - ₹%.2f] armed for subsequent Child Confirmation", c.Low, c.High),
								)
								diagnostics = append(diagnostics, diag)
								continue
							}
						}
					}

					// If not a new Master, test if candle qualifies as Child Confirmation or increments child inside count
					if childMaster != nil && childConfirm == nil {
						if c.Low < childMaster.Low {
							confirmRangePct := rangePct
							masterSize := childMaster.High - childMaster.Low
							confirmSize := c.High - c.Low
							sizeMultiplierValid := true
							if confirmRangePct > confirmMasterMinSizePct && confirmMasterMultiplier > 0 && masterSize > 0 {
								sizeMultiplierValid = confirmSize <= (masterSize*confirmMasterMultiplier + 1e-6)
							}
							if c.Close < c.Open && c.High <= childMaster.High && confirmRangePct <= confirmMaxPct && sizeMultiplierValid {
								cCopy := c
								childConfirm = &cCopy
								childConfirmIdx = idx
								reason := fmt.Sprintf("Child Confirmation Armed: Sell trigger set below Child Low ₹%.2f, SL @ Child Master High ₹%.2f (Size ₹%.2f, %.2fx of Child Master ₹%.2f). Parent confirmation (Trigger ₹%.2f) also active.", c.Low, childMaster.High, confirmSize, confirmSize/masterSize, masterSize, activeConfirm.Low)
								events = append(events, data.StrategyEvent{
									EventTime:    cTimeIST,
									Symbol:       symbol,
									Strategy:     "EMAS5_BREAKOUT",
									Stage:        "CHILD_CONFIRMATION_ARMED",
									Direction:    "SELL",
									TriggerPrice: c.Low,
									SLPrice:      childMaster.High,
									CandleTime:   &cTimeCopy,
									CandleOpen:   c.Open,
									CandleHigh:   c.High,
									CandleLow:    c.Low,
									CandleClose:  c.Close,
									CandleVolume: c.Volume,
									Reason:       reason,
									Details: map[string]interface{}{
										"child_trigger":      c.Low,
										"child_sl":           childMaster.High,
										"parent_confirm_low": activeConfirm.Low,
										"confirm_size":       confirmSize,
										"master_size":        masterSize,
										"range_pct":          confirmRangePct,
									},
								})
								diag.Status = "CHILD_CONFIRMATION_ARMED"
								diag.Verdict = "PASS"
								diag.Details["direction"] = "SELL"
								diag.PassedCriteria = append(diag.PassedCriteria,
									fmt.Sprintf("Child Confirmation armed: Sell below Child Low ₹%.2f, SL @ ₹%.2f", c.Low, childMaster.High),
									fmt.Sprintf("Low ₹%.2f broke Child Master Low ₹%.2f", c.Low, childMaster.Low),
									fmt.Sprintf("High ₹%.2f held below Child Master High ₹%.2f", c.High, childMaster.High),
									fmt.Sprintf("Bearish RED candle (Close ₹%.2f < Open ₹%.2f)", c.Close, c.Open),
									fmt.Sprintf("Range %.2f%% <= max %.2f%%, Size %.2fx <= %.2fx", confirmRangePct, confirmMaxPct, confirmSize/masterSize, confirmMasterMultiplier),
									fmt.Sprintf("Parent confirmation trigger ₹%.2f preserved and active", activeConfirm.Low),
								)
								diagnostics = append(diagnostics, diag)
								continue
							} else {
								var childFailReasons []string
								if c.Close >= c.Open {
									childFailReasons = append(childFailReasons, fmt.Sprintf("Candle is not RED (Close ₹%.2f >= Open ₹%.2f)", c.Close, c.Open))
								}
								if c.High > childMaster.High {
									childFailReasons = append(childFailReasons, fmt.Sprintf("High ₹%.2f breached Child Master High ₹%.2f", c.High, childMaster.High))
								}
								if confirmRangePct > confirmMaxPct {
									childFailReasons = append(childFailReasons, fmt.Sprintf("Range %.2f%% exceeded max %.2f%%", confirmRangePct, confirmMaxPct))
								}
								if !sizeMultiplierValid {
									childFailReasons = append(childFailReasons, fmt.Sprintf("Size ₹%.2f exceeded %.2fx Child Master size ₹%.2f (max ₹%.2f)", confirmSize, confirmMasterMultiplier, masterSize, masterSize*confirmMasterMultiplier))
								}
								failStr := strings.Join(childFailReasons, "; ")
								events = append(events, data.StrategyEvent{
									EventTime:    cTimeIST,
									Symbol:       symbol,
									Strategy:     "EMAS5_BREAKOUT",
									Stage:        "CHILD_CONFIRMATION_REJECTED",
									Direction:    "SELL",
									CandleTime:   &cTimeCopy,
									CandleOpen:   c.Open,
									CandleHigh:   c.High,
									CandleLow:    c.Low,
									CandleClose:  c.Close,
									CandleVolume: c.Volume,
									Reason:       fmt.Sprintf("Child Confirmation Rejected: Broke Child Master Low ₹%.2f but failed confirmation criteria (%s). Child setup dropped; Parent confirmation (Trigger ₹%.2f) remains active.", childMaster.Low, failStr, activeConfirm.Low),
									Details: map[string]interface{}{
										"child_master_low":   childMaster.Low,
										"parent_confirm_low": activeConfirm.Low,
										"rejection_reasons":  childFailReasons,
									},
								})
								diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Child Confirmation rejected: %s (Parent trigger ₹%.2f preserved)", failStr, activeConfirm.Low))
								clearChild()
							}
						} else if c.Low >= childMaster.Low && c.High <= childMaster.High {
							childInsideCount++
							if childInsideCount > maxInsideCandles {
								events = append(events, data.StrategyEvent{
									EventTime:    cTimeIST,
									Symbol:       symbol,
									Strategy:     "EMAS5_BREAKOUT",
									Stage:        "CHILD_SETUP_INVALIDATED",
									Direction:    "SELL",
									CandleTime:   &cTimeCopy,
									Reason:       fmt.Sprintf("Nested Child Master Rejected: Exceeded maximum inside consolidation candles (%d > %d allowed). Child setup dropped; Parent confirmation (Trigger ₹%.2f) remains active.", childInsideCount, maxInsideCandles, activeConfirm.Low),
									Details: map[string]interface{}{
										"child_inside_count":  childInsideCount,
										"max_inside_allowed":  maxInsideCandles,
										"parent_confirm_low": activeConfirm.Low,
									},
								})
								diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Nested Child Master dropped: %d inside consolidation candles exceeded max %d allowed (Parent trigger ₹%.2f preserved)", childInsideCount, maxInsideCandles, activeConfirm.Low))
								clearChild()
							}
						}
					}
					// Also check if trend reversed to BUY Master candidate during waiting window
					touchesEMABuy := (c.Low <= ema10Upper && c.High >= ema10Lower) ||
						(c.Low <= ema20Upper && c.High >= ema20Lower)
					touchesPDH := false
					if summary.PDH > 0 {
						pdhUpper := summary.PDH * (1.0 + emaTouchBufferPct/100.0)
						pdhLower := summary.PDH * (1.0 - emaTouchBufferPct/100.0)
						touchesPDH = c.Low <= pdhUpper && c.High >= pdhLower
					}
					closesAboveAll := c.Close > e10 && c.Close > e20
					if summary.PDH > 0 && c.Close <= summary.PDH {
						closesAboveAll = false
					}
					if c.Close > c.Open && (touchesEMABuy || touchesPDH) && closesAboveAll && rangePct <= masterMaxPct && wickPct <= masterMaxWickPct {
						isValid, lowestLow, candlesSinceLowest, reboundPct, pdhRetracePct := engine.validateBuyUShape(allCandles, idx, summary.PDH, e10, e20)
						if isValid {
							events = append(events, data.StrategyEvent{
								EventTime:    cTimeIST,
								Symbol:       symbol,
								Strategy:     "EMAS5_BREAKOUT",
								Stage:        "SETUP_SUPERSEDED",
								Direction:    "BUY",
								TriggerPrice: c.High,
								SLPrice:      c.Low,
								CandleTime:   &cTimeCopy,
								CandleOpen:   c.Open,
								CandleHigh:   c.High,
								CandleLow:    c.Low,
								CandleClose:  c.Close,
								CandleVolume: c.Volume,
								Reason:       fmt.Sprintf("Pending SELL setup superseded by reversal BUY Master candle at %s (High ₹%.2f, Low ₹%.2f)", cTimeIST.Format("15:04"), c.High, c.Low),
								Details: map[string]interface{}{
									"prev_trigger":          activeConfirm.Low,
									"prev_sl":               activeMaster.High,
									"new_master_high":       c.High,
									"new_master_low":        c.Low,
									"rebound_pct":           reboundPct,
									"pdh_retrace_pct":       pdhRetracePct,
									"lowest_low":            lowestLow,
									"candles_since_lowest":  candlesSinceLowest,
								},
							})
							cCopy := c
							activeMaster = &cCopy
							activeConfirm = nil
							confirmCandleIdx = -1
							insideCount = 0
							masterDir = "BUY"
							diag.Status = "MASTER_REANCHORED"
							diag.Verdict = "PASS"
							diag.Details["lowest_low"] = lowestLow
							diag.Details["rebound_pct"] = reboundPct
							diag.Details["pdh_retrace_pct"] = pdhRetracePct
							diag.Details["candles_since_highest"] = candlesSinceLowest
							diag.PassedCriteria = append(diag.PassedCriteria,
								"Pending SELL setup superseded: Trend reversed, fresh BUY Master Candle formed during waiting window",
								fmt.Sprintf("Bullish GREEN candle (Close ₹%.2f > Open ₹%.2f)", c.Close, c.Open),
								fmt.Sprintf("Range %.2f%% <= max %.2f%%, Wick %.2f%% <= max %.2f%%", rangePct, masterMaxPct, wickPct, masterMaxWickPct),
								fmt.Sprintf("Closed above EMA10 (₹%.2f), EMA20 (₹%.2f) & PDH (₹%.2f)", e10, e20, summary.PDH),
								fmt.Sprintf("U-Shape rebound +%.2f%% from trough ₹%.2f", reboundPct, lowestLow),
							)
							diagnostics = append(diagnostics, diag)
							continue
						}
					}

					// Still armed and waiting for breakdown trigger
					diag.Status = "AWAITING_TRIGGER"
					diag.Verdict = "ARMED"
					diag.Details["trigger_price"] = activeConfirm.Low
					diag.Details["sl_price"] = activeMaster.High
					diag.Details["candles_waited"] = candlesWaited
					diagnostics = append(diagnostics, diag)
					continue
				}
			}
		}

		// 4. State: Master Candle Established (Awaiting Confirmation Candle)
		if activeMaster != nil && activeConfirm == nil {
			if masterDir == "BUY" {
				// 1. Master Low Invalidation Guard / Liquidity Sweep Check:
				if c.Low < activeMaster.Low {
					// Universal Master Re-Anchoring: Check if c independently qualifies as a NEW Master candle!
					ema10Upper := e10 * (1.0 + emaTouchBufferPct/100.0)
					ema10Lower := e10 * (1.0 - emaTouchBufferPct/100.0)
					ema20Upper := e20 * (1.0 + emaTouchBufferPct/100.0)
					ema20Lower := e20 * (1.0 - emaTouchBufferPct/100.0)
					touchesEMA := (c.Low <= ema10Upper && c.High >= ema10Lower) ||
						(c.Low <= ema20Upper && c.High >= ema20Lower)

					touchesPDH := false
					if summary.PDH > 0 {
						pdhUpper := summary.PDH * (1.0 + emaTouchBufferPct/100.0)
						pdhLower := summary.PDH * (1.0 - emaTouchBufferPct/100.0)
						touchesPDH = c.Low <= pdhUpper && c.High >= pdhLower
					}
					touchesAnyLevel := touchesEMA || touchesPDH
					closesAboveAll := c.Close > e10 && c.Close > e20
					if summary.PDH > 0 && c.Close <= summary.PDH {
						closesAboveAll = false
					}

					if c.Close > c.Open && touchesAnyLevel && closesAboveAll && rangePct <= masterMaxPct && wickPct <= masterMaxWickPct {
						isValid, lowestLow, candlesSinceLowest, reboundPct, pdhRetracePct := engine.validateBuyUShape(allCandles, idx, summary.PDH, e10, e20)
						if isValid {
							cCopy := c
							activeMaster = &cCopy
							insideCount = 0
							events = append(events, data.StrategyEvent{
								EventTime:    cTimeIST,
								Symbol:       symbol,
								Strategy:     "EMAS5_BREAKOUT",
								Stage:        "MASTER_REANCHORED",
								Direction:    "BUY",
								TriggerPrice: c.High,
								SLPrice:      c.Low,
								CandleTime:   &cTimeCopy,
								CandleOpen:   c.Open,
								CandleHigh:   c.High,
								CandleLow:    c.Low,
								CandleClose:  c.Close,
								CandleVolume: c.Volume,
								Reason:       fmt.Sprintf("Master Candle Re-anchored (BUY U-Shape, Rebound: +%.2f%%, PDH Retrace: +%.2f%%, Range: %.2f%%)", reboundPct, pdhRetracePct, rangePct),
								Details: map[string]interface{}{
									"lowest_low":           lowestLow,
									"candles_since_lowest": candlesSinceLowest,
									"rebound_pct":          reboundPct,
									"pdh_retrace_pct":      pdhRetracePct,
									"range_pct":            rangePct,
									"wick_pct":             wickPct,
									"ema10":                e10,
									"ema20":                e20,
								},
							})
							diag.Status = "MASTER_REANCHORED"
							diag.Verdict = "PASS"
							diag.Details["lowest_low"] = lowestLow
							diag.Details["rebound_pct"] = reboundPct
							diag.Details["pdh_retrace_pct"] = pdhRetracePct
							diag.Details["candles_since_lowest"] = candlesSinceLowest
							diag.PassedCriteria = append(diag.PassedCriteria,
								"Candle independently met all Master criteria: Re-anchored as NEW Master Candle",
								fmt.Sprintf("Bullish GREEN candle (Close ₹%.2f > Open ₹%.2f)", c.Close, c.Open),
								fmt.Sprintf("Range %.2f%% <= max %.2f%%, Wick %.2f%% <= max %.2f%%", rangePct, masterMaxPct, wickPct, masterMaxWickPct),
								fmt.Sprintf("Closed above EMA10 (₹%.2f), EMA20 (₹%.2f) & PDH (₹%.2f)", e10, e20, summary.PDH),
								fmt.Sprintf("U-Shape rebound +%.2f%% from trough ₹%.2f (formed %d candles ago)", reboundPct, lowestLow, candlesSinceLowest),
							)
							diagnostics = append(diagnostics, diag)
							continue
						}
					}

					// Otherwise, breached Master Low -> Invalidate
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "BUY",
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Candle Low ₹%.2f breached Master Low ₹%.2f", c.Low, activeMaster.Low),
						Details: map[string]interface{}{
							"candle_low": c.Low,
							"master_low": activeMaster.Low,
						},
					})
					diag.Status = "INVALIDATED"
					diag.Verdict = "REJECTED"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Breached Master Low ₹%.2f (Low: ₹%.2f)", activeMaster.Low, c.Low))
					activeMaster = nil
					diagnostics = append(diagnostics, diag)
					continue
				}

				// 2. Breakout of Master High:
				if c.High > activeMaster.High {
					// Confirmation Candle check:
					masterSize := activeMaster.High - activeMaster.Low
					confirmSize := c.High - c.Low
					sizeMultiplierValid := true
					if rangePct > confirmMasterMinSizePct && confirmMasterMultiplier > 0 && masterSize > 0 {
						sizeMultiplierValid = confirmSize <= (masterSize*confirmMasterMultiplier + 1e-6)
					}
					if c.Close > c.Open && c.Low >= activeMaster.Low && rangePct <= confirmMaxPct && sizeMultiplierValid {
						cCopy := c
						activeConfirm = &cCopy
						confirmCandleIdx = idx
						events = append(events, data.StrategyEvent{
							EventTime:    cTimeIST,
							Symbol:       symbol,
							Strategy:     "EMAS5_BREAKOUT",
							Stage:        "CONFIRMATION_ARMED",
							Direction:    "BUY",
							TriggerPrice: c.High,
							SLPrice:      activeMaster.Low,
							CandleTime:   &cTimeCopy,
							CandleOpen:   c.Open,
							CandleHigh:   c.High,
							CandleLow:    c.Low,
							CandleClose:  c.Close,
							CandleVolume: c.Volume,
							Reason:       fmt.Sprintf("Confirmation Candle armed: Buy above ₹%.2f, SL @ ₹%.2f (Size %.2f vs Master %.2f)", c.High, activeMaster.Low, confirmSize, masterSize),
							Details: map[string]interface{}{
								"range_pct":       rangePct,
								"confirm_size":    confirmSize,
								"master_size":     masterSize,
								"size_multiplier": confirmSize / masterSize,
								"ema10":           e10,
								"ema20":           e20,
							},
						})
						diag.Status = "CONFIRMATION_ARMED"
						diag.Verdict = "PASS"
						diag.Details["direction"] = "BUY"
						diag.Details["trigger_price"] = c.High
						diag.Details["sl_price"] = activeMaster.Low
						diag.Details["confirm_size"] = confirmSize
						diag.Details["master_size"] = masterSize
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Bullish GREEN candle (Close ₹%.2f > Open ₹%.2f)", c.Close, c.Open),
							fmt.Sprintf("High ₹%.2f broke Master High ₹%.2f", c.High, activeMaster.High),
							fmt.Sprintf("Low ₹%.2f held above Master Low ₹%.2f", c.Low, activeMaster.Low),
							fmt.Sprintf("Range %.2f%% <= max %.2f%%", rangePct, confirmMaxPct),
						)
						if rangePct > confirmMasterMinSizePct {
							diag.PassedCriteria = append(diag.PassedCriteria,
								fmt.Sprintf("Candle size ₹%.2f <= max %.2fx Master size ₹%.2f (max ₹%.2f)", confirmSize, confirmMasterMultiplier, masterSize, masterSize*confirmMasterMultiplier),
							)
						} else {
							diag.PassedCriteria = append(diag.PassedCriteria,
								fmt.Sprintf("Candle range %.2f%% <= min %.2f%% threshold (exempt from size multiplier filter)", rangePct, confirmMasterMinSizePct),
							)
						}
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Armed BUY trigger above ₹%.2f, SL @ ₹%.2f", c.High, activeMaster.Low),
						)
						diagnostics = append(diagnostics, diag)
						continue
					}

					// Broke Master High, but failed confirmation: Check if c independently qualifies as a NEW Master candle!
					ema10Upper := e10 * (1.0 + emaTouchBufferPct/100.0)
					ema10Lower := e10 * (1.0 - emaTouchBufferPct/100.0)
					ema20Upper := e20 * (1.0 + emaTouchBufferPct/100.0)
					ema20Lower := e20 * (1.0 - emaTouchBufferPct/100.0)
					touchesEMA := (c.Low <= ema10Upper && c.High >= ema10Lower) ||
						(c.Low <= ema20Upper && c.High >= ema20Lower)

					touchesPDH := false
					if summary.PDH > 0 {
						pdhUpper := summary.PDH * (1.0 + emaTouchBufferPct/100.0)
						pdhLower := summary.PDH * (1.0 - emaTouchBufferPct/100.0)
						touchesPDH = c.Low <= pdhUpper && c.High >= pdhLower
					}
					touchesAnyLevel := touchesEMA || touchesPDH
					closesAboveAll := c.Close > e10 && c.Close > e20
					if summary.PDH > 0 && c.Close <= summary.PDH {
						closesAboveAll = false
					}

					if c.Close > c.Open && touchesAnyLevel && closesAboveAll && rangePct <= masterMaxPct && wickPct <= masterMaxWickPct {
						isValid, lowestLow, candlesSinceLowest, reboundPct, pdhRetracePct := engine.validateBuyUShape(allCandles, idx, summary.PDH, e10, e20)
						if isValid {
							cCopy := c
							activeMaster = &cCopy
							insideCount = 0
							events = append(events, data.StrategyEvent{
								EventTime:    cTimeIST,
								Symbol:       symbol,
								Strategy:     "EMAS5_BREAKOUT",
								Stage:        "MASTER_REANCHORED",
								Direction:    "BUY",
								TriggerPrice: c.High,
								SLPrice:      c.Low,
								CandleTime:   &cTimeCopy,
								CandleOpen:   c.Open,
								CandleHigh:   c.High,
								CandleLow:    c.Low,
								CandleClose:  c.Close,
								CandleVolume: c.Volume,
								Reason:       fmt.Sprintf("Master Candle Re-anchored from Confirmation (BUY U-Shape, Rebound: +%.2f%%, PDH Retrace: +%.2f%%, Range: %.2f%%)", reboundPct, pdhRetracePct, rangePct),
								Details: map[string]interface{}{
									"lowest_low":           lowestLow,
									"candles_since_lowest": candlesSinceLowest,
									"rebound_pct":          reboundPct,
									"pdh_retrace_pct":      pdhRetracePct,
									"range_pct":            rangePct,
									"wick_pct":             wickPct,
									"ema10":                e10,
									"ema20":                e20,
								},
							})
							diag.Status = "MASTER_REANCHORED"
							diag.Verdict = "PASS"
							diag.Details["lowest_low"] = lowestLow
							diag.Details["rebound_pct"] = reboundPct
							diag.Details["pdh_retrace_pct"] = pdhRetracePct
							diag.Details["candles_since_lowest"] = candlesSinceLowest
							diag.PassedCriteria = append(diag.PassedCriteria,
								"Candle independently met all Master criteria: Re-anchored as NEW Master Candle",
								fmt.Sprintf("Bullish GREEN candle (Close ₹%.2f > Open ₹%.2f)", c.Close, c.Open),
								fmt.Sprintf("Range %.2f%% <= max %.2f%%, Wick %.2f%% <= max %.2f%%", rangePct, masterMaxPct, wickPct, masterMaxWickPct),
								fmt.Sprintf("Closed above EMA10 (₹%.2f), EMA20 (₹%.2f) & PDH (₹%.2f)", e10, e20, summary.PDH),
								fmt.Sprintf("U-Shape rebound +%.2f%% from trough ₹%.2f (formed %d candles ago)", reboundPct, lowestLow, candlesSinceLowest),
							)
							diagnostics = append(diagnostics, diag)
							continue
						}
					}

					// Otherwise, failed confirmation
					failReason := "Broke Master High but failed confirmation criteria"
					if !sizeMultiplierValid {
						failReason = fmt.Sprintf("Confirmation candle size ₹%.2f exceeded max %.2fx of Master candle size ₹%.2f (max allowed ₹%.2f)", confirmSize, confirmMasterMultiplier, masterSize, masterSize*confirmMasterMultiplier)
					}
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "BUY",
						CandleTime: &cTimeCopy,
						Reason:     failReason,
					})
					diag.Status = "INVALIDATED"
					diag.Verdict = "REJECTED"
					diag.RejectionReasons = append(diag.RejectionReasons, failReason)
					activeMaster = nil
					diagnostics = append(diagnostics, diag)
					continue
				}

				// 3. Inside Candle Consolidation (c.High <= activeMaster.High && c.Low >= activeMaster.Low):
				// Universal Master Re-Anchoring: Check if c independently qualifies as a NEW Master candle!
				ema10Upper := e10 * (1.0 + emaTouchBufferPct/100.0)
				ema10Lower := e10 * (1.0 - emaTouchBufferPct/100.0)
				ema20Upper := e20 * (1.0 + emaTouchBufferPct/100.0)
				ema20Lower := e20 * (1.0 - emaTouchBufferPct/100.0)
				touchesEMA := (c.Low <= ema10Upper && c.High >= ema10Lower) ||
					(c.Low <= ema20Upper && c.High >= ema20Lower)

				touchesPDH := false
				if summary.PDH > 0 {
					pdhUpper := summary.PDH * (1.0 + emaTouchBufferPct/100.0)
					pdhLower := summary.PDH * (1.0 - emaTouchBufferPct/100.0)
					touchesPDH = c.Low <= pdhUpper && c.High >= pdhLower
				}
				touchesAnyLevel := touchesEMA || touchesPDH
				closesAboveAll := c.Close > e10 && c.Close > e20
				if summary.PDH > 0 && c.Close <= summary.PDH {
					closesAboveAll = false
				}

				if c.Close > c.Open && touchesAnyLevel && closesAboveAll && rangePct <= masterMaxPct && wickPct <= masterMaxWickPct {
					isValid, lowestLow, candlesSinceLowest, reboundPct, pdhRetracePct := engine.validateBuyUShape(allCandles, idx, summary.PDH, e10, e20)
					if isValid {
						cCopy := c
						activeMaster = &cCopy
						insideCount = 0
						events = append(events, data.StrategyEvent{
							EventTime:    cTimeIST,
							Symbol:       symbol,
							Strategy:     "EMAS5_BREAKOUT",
							Stage:        "MASTER_REANCHORED",
							Direction:    "BUY",
							TriggerPrice: c.High,
							SLPrice:      c.Low,
							CandleTime:   &cTimeCopy,
							CandleOpen:   c.Open,
							CandleHigh:   c.High,
							CandleLow:    c.Low,
							CandleClose:  c.Close,
							CandleVolume: c.Volume,
							Reason:       fmt.Sprintf("Master Candle Re-anchored (BUY U-Shape, Rebound: +%.2f%%, PDH Retrace: +%.2f%%, Range: %.2f%%)", reboundPct, pdhRetracePct, rangePct),
							Details: map[string]interface{}{
								"lowest_low":           lowestLow,
								"candles_since_lowest": candlesSinceLowest,
								"rebound_pct":          reboundPct,
								"pdh_retrace_pct":      pdhRetracePct,
								"range_pct":            rangePct,
								"wick_pct":             wickPct,
								"ema10":                e10,
								"ema20":                e20,
							},
						})
						diag.Status = "MASTER_REANCHORED"
						diag.Verdict = "PASS"
						diag.Details["lowest_low"] = lowestLow
						diag.Details["rebound_pct"] = reboundPct
						diag.Details["pdh_retrace_pct"] = pdhRetracePct
						diag.Details["candles_since_lowest"] = candlesSinceLowest
						diag.PassedCriteria = append(diag.PassedCriteria,
							"Candle independently met all Master criteria: Re-anchored as NEW Master Candle",
							fmt.Sprintf("Bullish GREEN candle (Close ₹%.2f > Open ₹%.2f)", c.Close, c.Open),
							fmt.Sprintf("Range %.2f%% <= max %.2f%%, Wick %.2f%% <= max %.2f%%", rangePct, masterMaxPct, wickPct, masterMaxWickPct),
							fmt.Sprintf("Closed above EMA10 (₹%.2f), EMA20 (₹%.2f) & PDH (₹%.2f)", e10, e20, summary.PDH),
							fmt.Sprintf("U-Shape rebound +%.2f%% from trough ₹%.2f (formed %d candles ago)", reboundPct, lowestLow, candlesSinceLowest),
						)
						diagnostics = append(diagnostics, diag)
						continue
					}
				}

				// Standard inside candle consolidation count
				insideCount++
				if insideCount > maxInsideCandles {
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "BUY",
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Exceeded maximum inside candles consolidation limit (%d inside candles)", maxInsideCandles),
					})
					diag.Status = "INVALIDATED"
					diag.Verdict = "REJECTED"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Exceeded max inside candles consolidation limit (%d > %d allowed)", insideCount, maxInsideCandles))
					activeMaster = nil
					diagnostics = append(diagnostics, diag)
					continue
				}

				diag.Status = "INSIDE_CANDLE"
				diag.Verdict = "INFO"
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Consolidation inside candle %d of %d allowed", insideCount, maxInsideCandles),
					fmt.Sprintf("Price remained within Master range [₹%.2f - ₹%.2f]", activeMaster.Low, activeMaster.High),
				)
				diagnostics = append(diagnostics, diag)
				continue

			} else if masterDir == "SELL" {
				// 1. Master High Invalidation Guard / Liquidity Sweep Check:
				if c.High > activeMaster.High {
					// Universal Master Re-Anchoring: Check if c independently qualifies as a NEW Master candle!
					ema10Upper := e10 * (1.0 + emaTouchBufferPct/100.0)
					ema10Lower := e10 * (1.0 - emaTouchBufferPct/100.0)
					ema20Upper := e20 * (1.0 + emaTouchBufferPct/100.0)
					ema20Lower := e20 * (1.0 - emaTouchBufferPct/100.0)
					touchesEMA := (c.High >= ema10Lower && c.Low <= ema10Upper) ||
						(c.High >= ema20Lower && c.Low <= ema20Upper)

					touchesPDL := false
					if summary.PDL > 0 {
						pdlUpper := summary.PDL * (1.0 + emaTouchBufferPct/100.0)
						pdlLower := summary.PDL * (1.0 - emaTouchBufferPct/100.0)
						touchesPDL = c.High >= pdlLower && c.Low <= pdlUpper
					}
					touchesAnyLevel := touchesEMA || touchesPDL
					closesBelowAll := c.Close < e10 && c.Close < e20
					if summary.PDL > 0 && c.Close >= summary.PDL {
						closesBelowAll = false
					}

					if c.Close < c.Open && touchesAnyLevel && closesBelowAll && rangePct <= masterMaxPct && wickPct <= masterMaxWickPct {
						isValid, highestHigh, candlesSinceHighest, dropPct, pdlRetracePct := engine.validateSellInvertedUShape(allCandles, idx, summary.PDL, e10, e20)
						if isValid {
							cCopy := c
							activeMaster = &cCopy
							insideCount = 0
							events = append(events, data.StrategyEvent{
								EventTime:    cTimeIST,
								Symbol:       symbol,
								Strategy:     "EMAS5_BREAKOUT",
								Stage:        "MASTER_REANCHORED",
								Direction:    "SELL",
								TriggerPrice: c.Low,
								SLPrice:      c.High,
								CandleTime:   &cTimeCopy,
								CandleOpen:   c.Open,
								CandleHigh:   c.High,
								CandleLow:    c.Low,
								CandleClose:  c.Close,
								CandleVolume: c.Volume,
								Reason:       fmt.Sprintf("Master Candle Re-anchored (SELL Inverted U-Shape, Drop: -%.2f%%, PDL Retrace: +%.2f%%, Range: %.2f%%)", dropPct, pdlRetracePct, rangePct),
								Details: map[string]interface{}{
									"highest_high":          highestHigh,
									"candles_since_highest": candlesSinceHighest,
									"drop_pct":              dropPct,
									"pdl_retrace_pct":       pdlRetracePct,
									"range_pct":             rangePct,
									"wick_pct":              wickPct,
									"ema10":                 e10,
									"ema20":                 e20,
								},
							})
							diag.Status = "MASTER_REANCHORED"
							diag.Verdict = "PASS"
							diag.Details["highest_high"] = highestHigh
							diag.Details["drop_pct"] = dropPct
							diag.Details["pdl_retrace_pct"] = pdlRetracePct
							diag.Details["candles_since_highest"] = candlesSinceHighest
							diag.PassedCriteria = append(diag.PassedCriteria,
								"Candle independently met all Master criteria: Re-anchored as NEW Master Candle",
								fmt.Sprintf("Bearish RED candle (Close ₹%.2f < Open ₹%.2f)", c.Close, c.Open),
								fmt.Sprintf("Range %.2f%% <= max %.2f%%, Wick %.2f%% <= max %.2f%%", rangePct, masterMaxPct, wickPct, masterMaxWickPct),
								fmt.Sprintf("Closed below EMA10 (₹%.2f), EMA20 (₹%.2f) & PDL (₹%.2f)", e10, e20, summary.PDL),
								fmt.Sprintf("Inverted U-Shape drop -%.2f%% from peak ₹%.2f (formed %d candles ago)", dropPct, highestHigh, candlesSinceHighest),
							)
							diagnostics = append(diagnostics, diag)
							continue
						}
					}

					// Otherwise, breached Master High -> Invalidate
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "SELL",
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f", c.High, activeMaster.High),
						Details: map[string]interface{}{
							"candle_high": c.High,
							"master_high": activeMaster.High,
						},
					})
					diag.Status = "INVALIDATED"
					diag.Verdict = "REJECTED"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f", c.High, activeMaster.High))
					activeMaster = nil
					diagnostics = append(diagnostics, diag)
					continue
				}

				// 2. Breakdown of Master Low:
				if c.Low < activeMaster.Low {
					// Confirmation Candle check:
					masterSize := activeMaster.High - activeMaster.Low
					confirmSize := c.High - c.Low
					sizeMultiplierValid := true
					if rangePct > confirmMasterMinSizePct && confirmMasterMultiplier > 0 && masterSize > 0 {
						sizeMultiplierValid = confirmSize <= (masterSize*confirmMasterMultiplier + 1e-6)
					}
					if c.Close < c.Open && c.High <= activeMaster.High && rangePct <= confirmMaxPct && sizeMultiplierValid {
						cCopy := c
						activeConfirm = &cCopy
						confirmCandleIdx = idx
						events = append(events, data.StrategyEvent{
							EventTime:    cTimeIST,
							Symbol:       symbol,
							Strategy:     "EMAS5_BREAKOUT",
							Stage:        "CONFIRMATION_ARMED",
							Direction:    "SELL",
							TriggerPrice: c.Low,
							SLPrice:      activeMaster.High,
							CandleTime:   &cTimeCopy,
							CandleOpen:   c.Open,
							CandleHigh:   c.High,
							CandleLow:    c.Low,
							CandleClose:  c.Close,
							CandleVolume: c.Volume,
							Reason:       fmt.Sprintf("Confirmation Candle armed: Sell below ₹%.2f, SL @ ₹%.2f (Size %.2f vs Master %.2f)", c.Low, activeMaster.High, confirmSize, masterSize),
							Details: map[string]interface{}{
								"range_pct":       rangePct,
								"confirm_size":    confirmSize,
								"master_size":     masterSize,
								"size_multiplier": confirmSize / masterSize,
								"ema10":           e10,
								"ema20":           e20,
							},
						})
						diag.Status = "CONFIRMATION_ARMED"
						diag.Verdict = "PASS"
						diag.Details["direction"] = "SELL"
						diag.Details["trigger_price"] = c.Low
						diag.Details["sl_price"] = activeMaster.High
						diag.Details["confirm_size"] = confirmSize
						diag.Details["master_size"] = masterSize
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Bearish RED candle (Close ₹%.2f < Open ₹%.2f)", c.Close, c.Open),
							fmt.Sprintf("Low ₹%.2f broke Master Low ₹%.2f", c.Low, activeMaster.Low),
							fmt.Sprintf("High ₹%.2f held below Master High ₹%.2f", c.High, activeMaster.High),
							fmt.Sprintf("Range %.2f%% <= max %.2f%%", rangePct, confirmMaxPct),
						)
						if rangePct > confirmMasterMinSizePct {
							diag.PassedCriteria = append(diag.PassedCriteria,
								fmt.Sprintf("Candle size ₹%.2f <= max %.2fx Master size ₹%.2f (max ₹%.2f)", confirmSize, confirmMasterMultiplier, masterSize, masterSize*confirmMasterMultiplier),
							)
						} else {
							diag.PassedCriteria = append(diag.PassedCriteria,
								fmt.Sprintf("Candle range %.2f%% <= min %.2f%% threshold (exempt from size multiplier filter)", rangePct, confirmMasterMinSizePct),
							)
						}
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Armed SELL trigger below ₹%.2f, SL @ ₹%.2f", c.Low, activeMaster.High),
						)
						diagnostics = append(diagnostics, diag)
						continue
					}

					// Broke Master Low, but failed confirmation: Check if c independently qualifies as a NEW Master candle!
					ema10Upper := e10 * (1.0 + emaTouchBufferPct/100.0)
					ema10Lower := e10 * (1.0 - emaTouchBufferPct/100.0)
					ema20Upper := e20 * (1.0 + emaTouchBufferPct/100.0)
					ema20Lower := e20 * (1.0 - emaTouchBufferPct/100.0)
					touchesEMA := (c.High >= ema10Lower && c.Low <= ema10Upper) ||
						(c.High >= ema20Lower && c.Low <= ema20Upper)

					touchesPDL := false
					if summary.PDL > 0 {
						pdlUpper := summary.PDL * (1.0 + emaTouchBufferPct/100.0)
						pdlLower := summary.PDL * (1.0 - emaTouchBufferPct/100.0)
						touchesPDL = c.High >= pdlLower && c.Low <= pdlUpper
					}
					touchesAnyLevel := touchesEMA || touchesPDL
					closesBelowAll := c.Close < e10 && c.Close < e20
					if summary.PDL > 0 && c.Close >= summary.PDL {
						closesBelowAll = false
					}

					if c.Close < c.Open && touchesAnyLevel && closesBelowAll && rangePct <= masterMaxPct && wickPct <= masterMaxWickPct {
						isValid, highestHigh, candlesSinceHighest, dropPct, pdlRetracePct := engine.validateSellInvertedUShape(allCandles, idx, summary.PDL, e10, e20)
						if isValid {
							cCopy := c
							activeMaster = &cCopy
							insideCount = 0
							events = append(events, data.StrategyEvent{
								EventTime:    cTimeIST,
								Symbol:       symbol,
								Strategy:     "EMAS5_BREAKOUT",
								Stage:        "MASTER_REANCHORED",
								Direction:    "SELL",
								TriggerPrice: c.Low,
								SLPrice:      c.High,
								CandleTime:   &cTimeCopy,
								CandleOpen:   c.Open,
								CandleHigh:   c.High,
								CandleLow:    c.Low,
								CandleClose:  c.Close,
								CandleVolume: c.Volume,
								Reason:       fmt.Sprintf("Master Candle Re-anchored from Confirmation (SELL Inverted U-Shape, Drop: -%.2f%%, PDL Retrace: -%.2f%%, Range: %.2f%%)", dropPct, pdlRetracePct, rangePct),
								Details: map[string]interface{}{
									"highest_high":          highestHigh,
									"candles_since_highest": candlesSinceHighest,
									"drop_pct":              dropPct,
									"pdl_retrace_pct":       pdlRetracePct,
									"range_pct":             rangePct,
									"wick_pct":              wickPct,
									"ema10":                 e10,
									"ema20":                 e20,
								},
							})
							diag.Status = "MASTER_REANCHORED"
							diag.Verdict = "PASS"
							diag.Details["highest_high"] = highestHigh
							diag.Details["drop_pct"] = dropPct
							diag.Details["pdl_retrace_pct"] = pdlRetracePct
							diag.Details["candles_since_highest"] = candlesSinceHighest
							diag.PassedCriteria = append(diag.PassedCriteria,
								"Candle independently met all Master criteria: Re-anchored as NEW Master Candle",
								fmt.Sprintf("Bearish RED candle (Close ₹%.2f < Open ₹%.2f)", c.Close, c.Open),
								fmt.Sprintf("Range %.2f%% <= max %.2f%%, Wick %.2f%% <= max %.2f%%", rangePct, masterMaxPct, wickPct, masterMaxWickPct),
								fmt.Sprintf("Closed below EMA10 (₹%.2f), EMA20 (₹%.2f) & PDL (₹%.2f)", e10, e20, summary.PDL),
								fmt.Sprintf("Inverted U-Shape drop -%.2f%% from peak ₹%.2f (formed %d candles ago)", dropPct, highestHigh, candlesSinceHighest),
							)
							diagnostics = append(diagnostics, diag)
							continue
						}
					}

					// Otherwise, failed confirmation
					failReason := "Broke Master Low but failed confirmation criteria"
					if !sizeMultiplierValid {
						failReason = fmt.Sprintf("Confirmation candle size ₹%.2f exceeded max %.2fx of Master candle size ₹%.2f (max allowed ₹%.2f)", confirmSize, confirmMasterMultiplier, masterSize, masterSize*confirmMasterMultiplier)
					}
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "SELL",
						CandleTime: &cTimeCopy,
						Reason:     failReason,
					})
					diag.Status = "INVALIDATED"
					diag.Verdict = "REJECTED"
					diag.RejectionReasons = append(diag.RejectionReasons, failReason)
					activeMaster = nil
					diagnostics = append(diagnostics, diag)
					continue
				}

				// 3. Inside Candle Consolidation (c.Low >= activeMaster.Low && c.High <= activeMaster.High):
				// Universal Master Re-Anchoring: Check if c independently qualifies as a NEW Master candle!
				ema10Upper := e10 * (1.0 + emaTouchBufferPct/100.0)
				ema10Lower := e10 * (1.0 - emaTouchBufferPct/100.0)
				ema20Upper := e20 * (1.0 + emaTouchBufferPct/100.0)
				ema20Lower := e20 * (1.0 - emaTouchBufferPct/100.0)
				touchesEMA := (c.High >= ema10Lower && c.Low <= ema10Upper) ||
					(c.High >= ema20Lower && c.Low <= ema20Upper)

				touchesPDL := false
				if summary.PDL > 0 {
					pdlUpper := summary.PDL * (1.0 + emaTouchBufferPct/100.0)
					pdlLower := summary.PDL * (1.0 - emaTouchBufferPct/100.0)
					touchesPDL = c.High >= pdlLower && c.Low <= pdlUpper
				}
				touchesAnyLevel := touchesEMA || touchesPDL
				closesBelowAll := c.Close < e10 && c.Close < e20
				if summary.PDL > 0 && c.Close >= summary.PDL {
					closesBelowAll = false
				}

				if c.Close < c.Open && touchesAnyLevel && closesBelowAll && rangePct <= masterMaxPct && wickPct <= masterMaxWickPct {
					isValid, highestHigh, candlesSinceHighest, dropPct, pdlRetracePct := engine.validateSellInvertedUShape(allCandles, idx, summary.PDL, e10, e20)
					if isValid {
						cCopy := c
						activeMaster = &cCopy
						insideCount = 0
						events = append(events, data.StrategyEvent{
							EventTime:    cTimeIST,
							Symbol:       symbol,
							Strategy:     "EMAS5_BREAKOUT",
							Stage:        "MASTER_REANCHORED",
							Direction:    "SELL",
							TriggerPrice: c.Low,
							SLPrice:      c.High,
							CandleTime:   &cTimeCopy,
							CandleOpen:   c.Open,
							CandleHigh:   c.High,
							CandleLow:    c.Low,
							CandleClose:  c.Close,
							CandleVolume: c.Volume,
							Reason:       fmt.Sprintf("Master Candle Re-anchored (SELL Inverted U-Shape, Drop: -%.2f%%, PDL Retrace: +%.2f%%, Range: %.2f%%)", dropPct, pdlRetracePct, rangePct),
							Details: map[string]interface{}{
								"highest_high":          highestHigh,
								"candles_since_highest": candlesSinceHighest,
								"drop_pct":              dropPct,
								"pdl_retrace_pct":       pdlRetracePct,
								"range_pct":             rangePct,
								"wick_pct":              wickPct,
								"ema10":                 e10,
								"ema20":                 e20,
							},
						})
						diag.Status = "MASTER_REANCHORED"
						diag.Verdict = "PASS"
						diag.Details["highest_high"] = highestHigh
						diag.Details["drop_pct"] = dropPct
						diag.Details["pdl_retrace_pct"] = pdlRetracePct
						diag.Details["candles_since_highest"] = candlesSinceHighest
						diag.PassedCriteria = append(diag.PassedCriteria,
							"Candle independently met all Master criteria: Re-anchored as NEW Master Candle",
							fmt.Sprintf("Bearish RED candle (Close ₹%.2f < Open ₹%.2f)", c.Close, c.Open),
							fmt.Sprintf("Range %.2f%% <= max %.2f%%, Wick %.2f%% <= max %.2f%%", rangePct, masterMaxPct, wickPct, masterMaxWickPct),
							fmt.Sprintf("Closed below EMA10 (₹%.2f), EMA20 (₹%.2f) & PDL (₹%.2f)", e10, e20, summary.PDL),
							fmt.Sprintf("Inverted U-Shape drop -%.2f%% from peak ₹%.2f (formed %d candles ago)", dropPct, highestHigh, candlesSinceHighest),
						)
						diagnostics = append(diagnostics, diag)
						continue
					}
				}

				// Standard inside candle consolidation count
				insideCount++
				if insideCount > maxInsideCandles {
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "SELL",
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Exceeded maximum inside candles consolidation limit (%d inside candles)", maxInsideCandles),
					})
					diag.Status = "INVALIDATED"
					diag.Verdict = "REJECTED"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Exceeded max inside candles consolidation limit (%d > %d allowed)", insideCount, maxInsideCandles))
					activeMaster = nil
					diagnostics = append(diagnostics, diag)
					continue
				}

				diag.Status = "INSIDE_CANDLE"
				diag.Verdict = "INFO"
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Consolidation inside candle %d of %d allowed", insideCount, maxInsideCandles),
					fmt.Sprintf("Price remained within Master range [₹%.2f - ₹%.2f]", activeMaster.Low, activeMaster.High),
				)
				diagnostics = append(diagnostics, diag)
				continue
			}
		}

		// 5. State: Scanning for New Master Candle Formation
		if activeMaster == nil {
			if timeStr >= tradeEndTime {
				if diag.Status != "INVALIDATED" {
					diag.Status = "PAST_CUTOFF"
				}
				diag.Verdict = "INFO"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle time %s is past trade cutoff %s IST", timeStr, tradeEndTime))
				diagnostics = append(diagnostics, diag)
				continue
			}

			candlesToday := idx - todayStartIdx + 1
			if candlesToday < rallyCandles+1 {
				if diag.Status != "INVALIDATED" {
					diag.Status = "WARMUP"
				}
				diag.Verdict = "INFO"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Pre-setup rally requirement: session candle %d of %d required before Master search", candlesToday, rallyCandles+1))
				diagnostics = append(diagnostics, diag)
				continue
			}

			// Evaluate BUY Setup
			if c.Close > c.Open { // GREEN
				reasons := make([]string, 0)
				if rangePct > masterMaxPct {
					reasons = append(reasons, fmt.Sprintf("Range %.2f%% exceeds max allowed %.2f%%", rangePct, masterMaxPct))
				}
				if wickPct > masterMaxWickPct {
					reasons = append(reasons, fmt.Sprintf("Wick %.2f%% exceeds max allowed %.2f%%", wickPct, masterMaxWickPct))
				}

				ema10Upper := e10 * (1.0 + emaTouchBufferPct/100.0)
				ema10Lower := e10 * (1.0 - emaTouchBufferPct/100.0)
				ema20Upper := e20 * (1.0 + emaTouchBufferPct/100.0)
				ema20Lower := e20 * (1.0 - emaTouchBufferPct/100.0)
				touchesEMA := (c.Low <= ema10Upper && c.High >= ema10Lower) ||
					(c.Low <= ema20Upper && c.High >= ema20Lower)

				touchesPDH := false
				if summary.PDH > 0 {
					pdhUpper := summary.PDH * (1.0 + emaTouchBufferPct/100.0)
					pdhLower := summary.PDH * (1.0 - emaTouchBufferPct/100.0)
					touchesPDH = c.Low <= pdhUpper && c.High >= pdhLower
				}
				touchesAnyLevel := touchesEMA || touchesPDH

				closesAboveAll := c.Close > e10 && c.Close > e20
				if summary.PDH > 0 && c.Close <= summary.PDH {
					closesAboveAll = false
					reasons = append(reasons, fmt.Sprintf("Close ₹%.2f <= PDH ₹%.2f (Must close above PDH)", c.Close, summary.PDH))
				}
				if c.Close <= e10 {
					reasons = append(reasons, fmt.Sprintf("Close ₹%.2f <= EMA10 ₹%.2f", c.Close, e10))
				}
				if c.Close <= e20 {
					reasons = append(reasons, fmt.Sprintf("Close ₹%.2f <= EMA20 ₹%.2f", c.Close, e20))
				}
				if !touchesAnyLevel {
					reasons = append(reasons, fmt.Sprintf("No EMA/PDH touch within %.2f%% buffer (High ₹%.2f, Low ₹%.2f vs EMA10 ₹%.2f, EMA20 ₹%.2f, PDH ₹%.2f)", emaTouchBufferPct, c.High, c.Low, e10, e20, summary.PDH))
				}

				if touchesAnyLevel && closesAboveAll && rangePct <= masterMaxPct && wickPct <= masterMaxWickPct {
					isValid, lowestLow, candlesSinceLowest, reboundPct, pdhRetracePct := engine.validateBuyUShape(allCandles, idx, summary.PDH, e10, e20)
					if isValid {
						cCopy := c
						activeMaster = &cCopy
						masterDir = "BUY"
						insideCount = 0
						events = append(events, data.StrategyEvent{
							EventTime:    cTimeIST,
							Symbol:       symbol,
							Strategy:     "EMAS5_BREAKOUT",
							Stage:        "SETUP_FORMED",
							Direction:    "BUY",
							TriggerPrice: c.High,
							SLPrice:      c.Low,
							CandleTime:   &cTimeCopy,
							CandleOpen:   c.Open,
							CandleHigh:   c.High,
							CandleLow:    c.Low,
							CandleClose:  c.Close,
							CandleVolume: c.Volume,
							Reason:       fmt.Sprintf("Master Candle Formed (BUY U-Shape, Rebound: +%.2f%%, PDH Retrace: +%.2f%%, Range: %.2f%%)", reboundPct, pdhRetracePct, rangePct),
							Details: map[string]interface{}{
								"rally_candles":        rallyCandles,
								"lowest_low":           lowestLow,
								"candles_since_lowest": candlesSinceLowest,
								"rebound_pct":          reboundPct,
								"pdh_retrace_pct":      pdhRetracePct,
								"range_pct":            rangePct,
								"wick_pct":             wickPct,
								"ema10":                e10,
								"ema20":                e20,
							},
						})
						diag.Status = "MASTER_ESTABLISHED"
						diag.Verdict = "PASS"
						diag.Details["direction"] = "BUY"
						diag.Details["lowest_low"] = lowestLow
						diag.Details["rebound_pct"] = reboundPct
						diag.Details["pdh_retrace_pct"] = pdhRetracePct
						diag.Details["candles_since_lowest"] = candlesSinceLowest

						var touchedLevels []string
						if c.Low <= ema10Upper && c.High >= ema10Lower {
							touchedLevels = append(touchedLevels, fmt.Sprintf("EMA10 (₹%.2f)", e10))
						}
						if c.Low <= ema20Upper && c.High >= ema20Lower {
							touchedLevels = append(touchedLevels, fmt.Sprintf("EMA20 (₹%.2f)", e20))
						}
						if touchesPDH {
							touchedLevels = append(touchedLevels, fmt.Sprintf("PDH (₹%.2f)", summary.PDH))
						}
						levelTouchStr := "EMA/PDH"
						if len(touchedLevels) > 0 {
							levelTouchStr = strings.Join(touchedLevels, " & ")
						}

						closeStr := fmt.Sprintf("Closed above EMA10 (₹%.2f) & EMA20 (₹%.2f)", e10, e20)
						if summary.PDH > 0 {
							closeStr = fmt.Sprintf("Closed above EMA10 (₹%.2f), EMA20 (₹%.2f) & PDH (₹%.2f)", e10, e20, summary.PDH)
						}

						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Bullish GREEN candle (Close ₹%.2f > Open ₹%.2f)", c.Close, c.Open),
							fmt.Sprintf("Touched %s within %.2f%% buffer", levelTouchStr, emaTouchBufferPct),
							closeStr,
							fmt.Sprintf("Range %.2f%% <= max %.2f%%", rangePct, masterMaxPct),
							fmt.Sprintf("Wick %.2f%% <= max %.2f%%", wickPct, masterMaxWickPct),
							fmt.Sprintf("U-Shape trough ₹%.2f formed %d candles ago (≥ %d required)", lowestLow, candlesSinceLowest, rallyCandles),
						)
						if minPDHPDLRetracePct > 0 && summary.PDH > 0 {
							diag.PassedCriteria = append(diag.PassedCriteria,
								fmt.Sprintf("Peak before retrace reached +%.2f%% above PDH (≥ +%.2f%% threshold)", pdhRetracePct, minPDHPDLRetracePct),
							)
						}
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Rebound +%.2f%% from trough (≥ %.2f%% threshold)", reboundPct, minReboundPct),
							"Bullish U-Shape arc confirmed (healthy EMA retest/pullback geometry)",
						)
						diagnostics = append(diagnostics, diag)
						continue
					} else {
						if minPDHPDLRetracePct > 0 && summary.PDH > 0 && pdhRetracePct < minPDHPDLRetracePct {
							if pdhRetracePct < 0 {
								reasons = append(reasons, fmt.Sprintf("U-Shape failed: Peak before retrace stayed %.2f%% below PDH (required ≥ +%.2f%% above PDH)", math.Abs(pdhRetracePct), minPDHPDLRetracePct))
							} else {
								reasons = append(reasons, fmt.Sprintf("U-Shape failed: Peak before retrace reached +%.2f%% above PDH (< %.2f%% threshold)", pdhRetracePct, minPDHPDLRetracePct))
							}
						} else if candlesSinceLowest < rallyCandles {
							reasons = append(reasons, fmt.Sprintf("U-Shape failed: Lowest Low ₹%.2f formed %d candles ago (< %d required)", lowestLow, candlesSinceLowest, rallyCandles))
						} else if reboundPct < minReboundPct {
							reasons = append(reasons, fmt.Sprintf("U-Shape failed: Rebound +%.2f%% < %.2f%% threshold", reboundPct, minReboundPct))
						} else {
							reasons = append(reasons, "U-Shape arc failed: broken arc or V-spike detected")
						}
					}
				}

				if diag.Status != "INVALIDATED" {
					diag.Status = "REJECTED"
				}
				diag.Verdict = "REJECTED"
				diag.RejectionReasons = append(diag.RejectionReasons, reasons...)
				diagnostics = append(diagnostics, diag)
				continue

			} else if c.Close < c.Open { // RED
				reasons := make([]string, 0)
				if rangePct > masterMaxPct {
					reasons = append(reasons, fmt.Sprintf("Range %.2f%% exceeds max allowed %.2f%%", rangePct, masterMaxPct))
				}
				if wickPct > masterMaxWickPct {
					reasons = append(reasons, fmt.Sprintf("Wick %.2f%% exceeds max allowed %.2f%%", wickPct, masterMaxWickPct))
				}

				ema10Upper := e10 * (1.0 + emaTouchBufferPct/100.0)
				ema10Lower := e10 * (1.0 - emaTouchBufferPct/100.0)
				ema20Upper := e20 * (1.0 + emaTouchBufferPct/100.0)
				ema20Lower := e20 * (1.0 - emaTouchBufferPct/100.0)
				touchesEMA := (c.High >= ema10Lower && c.Low <= ema10Upper) ||
					(c.High >= ema20Lower && c.Low <= ema20Upper)

				touchesPDL := false
				if summary.PDL > 0 {
					pdlUpper := summary.PDL * (1.0 + emaTouchBufferPct/100.0)
					pdlLower := summary.PDL * (1.0 - emaTouchBufferPct/100.0)
					touchesPDL = c.High >= pdlLower && c.Low <= pdlUpper
				}
				touchesAnyLevel := touchesEMA || touchesPDL

				closesBelowAll := c.Close < e10 && c.Close < e20
				if summary.PDL > 0 && c.Close >= summary.PDL {
					closesBelowAll = false
					reasons = append(reasons, fmt.Sprintf("Close ₹%.2f >= PDL ₹%.2f (Must close below PDL)", c.Close, summary.PDL))
				}
				if c.Close >= e10 {
					reasons = append(reasons, fmt.Sprintf("Close ₹%.2f >= EMA10 ₹%.2f", c.Close, e10))
				}
				if c.Close >= e20 {
					reasons = append(reasons, fmt.Sprintf("Close ₹%.2f >= EMA20 ₹%.2f", c.Close, e20))
				}
				if !touchesAnyLevel {
					reasons = append(reasons, fmt.Sprintf("No EMA/PDL touch within %.2f%% buffer (High ₹%.2f, Low ₹%.2f vs EMA10 ₹%.2f, EMA20 ₹%.2f, PDL ₹%.2f)", emaTouchBufferPct, c.High, c.Low, e10, e20, summary.PDL))
				}

				if touchesAnyLevel && closesBelowAll && rangePct <= masterMaxPct && wickPct <= masterMaxWickPct {
					isValid, highestHigh, candlesSinceHighest, dropPct, pdlRetracePct := engine.validateSellInvertedUShape(allCandles, idx, summary.PDL, e10, e20)
					if isValid {
						cCopy := c
						activeMaster = &cCopy
						masterDir = "SELL"
						insideCount = 0
						events = append(events, data.StrategyEvent{
							EventTime:    cTimeIST,
							Symbol:       symbol,
							Strategy:     "EMAS5_BREAKOUT",
							Stage:        "SETUP_FORMED",
							Direction:    "SELL",
							TriggerPrice: c.Low,
							SLPrice:      c.High,
							CandleTime:   &cTimeCopy,
							CandleOpen:   c.Open,
							CandleHigh:   c.High,
							CandleLow:    c.Low,
							CandleClose:  c.Close,
							CandleVolume: c.Volume,
							Reason:       fmt.Sprintf("Master Candle Formed (SELL Inverted U-Shape, Drop: -%.2f%%, PDL Retrace: -%.2f%%, Range: %.2f%%)", dropPct, pdlRetracePct, rangePct),
							Details: map[string]interface{}{
								"rally_candles":         rallyCandles,
								"highest_high":          highestHigh,
								"candles_since_highest": candlesSinceHighest,
								"drop_pct":              dropPct,
								"pdl_retrace_pct":       pdlRetracePct,
								"range_pct":             rangePct,
								"wick_pct":              wickPct,
								"ema10":                 e10,
								"ema20":                 e20,
							},
						})
						diag.Status = "MASTER_ESTABLISHED"
						diag.Verdict = "PASS"
						diag.Details["direction"] = "SELL"
						diag.Details["highest_high"] = highestHigh
						diag.Details["drop_pct"] = dropPct
						diag.Details["pdl_retrace_pct"] = pdlRetracePct
						diag.Details["candles_since_highest"] = candlesSinceHighest

						var touchedLevels []string
						if c.High >= ema10Lower && c.Low <= ema10Upper {
							touchedLevels = append(touchedLevels, fmt.Sprintf("EMA10 (₹%.2f)", e10))
						}
						if c.High >= ema20Lower && c.Low <= ema20Upper {
							touchedLevels = append(touchedLevels, fmt.Sprintf("EMA20 (₹%.2f)", e20))
						}
						if touchesPDL {
							touchedLevels = append(touchedLevels, fmt.Sprintf("PDL (₹%.2f)", summary.PDL))
						}
						levelTouchStr := "EMA/PDL"
						if len(touchedLevels) > 0 {
							levelTouchStr = strings.Join(touchedLevels, " & ")
						}

						closeStr := fmt.Sprintf("Closed below EMA10 (₹%.2f) & EMA20 (₹%.2f)", e10, e20)
						if summary.PDL > 0 {
							closeStr = fmt.Sprintf("Closed below EMA10 (₹%.2f), EMA20 (₹%.2f) & PDL (₹%.2f)", e10, e20, summary.PDL)
						}

						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Bearish RED candle (Close ₹%.2f < Open ₹%.2f)", c.Close, c.Open),
							fmt.Sprintf("Touched %s within %.2f%% buffer", levelTouchStr, emaTouchBufferPct),
							closeStr,
							fmt.Sprintf("Range %.2f%% <= max %.2f%%", rangePct, masterMaxPct),
							fmt.Sprintf("Wick %.2f%% <= max %.2f%%", wickPct, masterMaxWickPct),
							fmt.Sprintf("Inverted U-Shape peak ₹%.2f formed %d candles ago (≥ %d required)", highestHigh, candlesSinceHighest, rallyCandles),
						)
						if minPDHPDLRetracePct > 0 && summary.PDL > 0 {
							diag.PassedCriteria = append(diag.PassedCriteria,
								fmt.Sprintf("Pre-retrace trough dropped -%.2f%% below PDL (≥ -%.2f%% threshold)", math.Abs(pdlRetracePct), minPDHPDLRetracePct),
							)
						}
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Drop -%.2f%% from peak (≥ %.2f%% threshold)", dropPct, minReboundPct),
							"Inverted U-Shape arc confirmed (healthy EMA retest/pullback geometry)",
						)
						diagnostics = append(diagnostics, diag)
						continue
					} else {
						if minPDHPDLRetracePct > 0 && summary.PDL > 0 && pdlRetracePct < minPDHPDLRetracePct {
							if pdlRetracePct < 0 {
								reasons = append(reasons, fmt.Sprintf("Inverted U-Shape failed: Trough before retrace stayed %.2f%% above PDL (required ≥ -%.2f%% below PDL)", math.Abs(pdlRetracePct), minPDHPDLRetracePct))
							} else {
								reasons = append(reasons, fmt.Sprintf("Inverted U-Shape failed: Trough before retrace dropped -%.2f%% below PDL (< %.2f%% threshold)", pdlRetracePct, minPDHPDLRetracePct))
							}
						} else if candlesSinceHighest < rallyCandles {
							reasons = append(reasons, fmt.Sprintf("Inverted U-Shape failed: Highest High ₹%.2f formed %d candles ago (< %d required)", highestHigh, candlesSinceHighest, rallyCandles))
						} else if dropPct < minReboundPct {
							reasons = append(reasons, fmt.Sprintf("Inverted U-Shape failed: Drop -%.2f%% < %.2f%% threshold", dropPct, minReboundPct))
						} else {
							reasons = append(reasons, "Inverted U-Shape arc failed: broken arc or V-spike detected")
						}
					}
				}

				if diag.Status != "INVALIDATED" {
					diag.Status = "REJECTED"
				}
				diag.Verdict = "REJECTED"
				diag.RejectionReasons = append(diag.RejectionReasons, reasons...)
				diagnostics = append(diagnostics, diag)
				continue
			} else {
				// DOJI
				if diag.Status != "INVALIDATED" {
					diag.Status = "REJECTED"
				}
				diag.Verdict = "REJECTED"
				diag.RejectionReasons = append(diag.RejectionReasons, "DOJI candle (Open == Close)")
				diagnostics = append(diagnostics, diag)
				continue
			}
		}

		diagnostics = append(diagnostics, diag)
	}

	return events, diagnostics
}

// replayVandeBharat simulates the Vande Bharat Momentum Strategy across candles with complete diagnostics
func (a *AuditAnalyzer) replayVandeBharat(symbol string, todayCandles []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord, appCfg ...AppliedStrategyConfig) ([]data.StrategyEvent, []CandleDiagnosticItem) {
	events := make([]data.StrategyEvent, 0, 8)
	diagnostics := make([]CandleDiagnosticItem, 0, len(todayCandles))
	if len(todayCandles) < 2 {
		return events, diagnostics
	}

	masterMaxPct := 3.0
	masterMaxWickPct := 75.0
	slMinPct := 0.05
	slMaxPct := 2.0
	minGapPct := 0.0
	tradeEndTime := "11:00:00"

	if len(appCfg) > 0 {
		if appCfg[0].TradeEndTime != "" {
			tradeEndTime = data.NormalizeTimeHHMMSS(appCfg[0].TradeEndTime)
		}
		p := appCfg[0].Parameters
		masterMaxPct = getFloatParam(p, masterMaxPct, "master_max_pct", "stock_max_day_change_pct")
		masterMaxWickPct = getFloatParam(p, masterMaxWickPct, "master_max_wick_pct")
		slMinPct = getFloatParam(p, slMinPct, "sl_min_pct", "confirm_min_pct")
		slMaxPct = getFloatParam(p, slMaxPct, "sl_max_pct", "confirm_max_pct")
		minGapPct = getFloatParam(p, minGapPct, "min_gap_pct")
	}

	actualTimeframe := "5m"
	if len(appCfg) > 0 && appCfg[0].CandleTimeframe != "" {
		actualTimeframe = data.NormalizeCandleTimeframe(appCfg[0].CandleTimeframe)
	}
	if actualTimeframe == "1m" {
		if slMinPct <= 0 || slMinPct >= 0.30 {
			slMinPct = 0.05
		}
	} else {
		if slMinPct <= 0 {
			slMinPct = 0.50
		}
	}

	pdh := summary.PDH
	pdl := summary.PDL
	pdClose := summary.PDClose

	var activeMaster *data.Candle
	var dir string
	var triggerPrice, slPrice float64
	var isArmed bool
	var tradeExecuted bool

	for i, c := range todayCandles {
		cTimeIST := data.NormalizeToIST(c.Time)
		cTimeCopy := cTimeIST
		timeStr := cTimeIST.Format("15:04:05")
		cRange := c.High - c.Low
		cRangePct := 0.0
		if c.Close > 0 {
			cRangePct = (cRange / c.Close) * 100.0
		}
		cBody := math.Abs(c.Close - c.Open)
		cWick := cRange - cBody
		cWickPct := 0.0
		if cRange > 0 {
			cWickPct = (cWick / cRange) * 100.0
		}
		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}

		diag := CandleDiagnosticItem{
			Time:             cTimeIST.Format("15:04"),
			Open:             c.Open,
			High:             c.High,
			Low:              c.Low,
			Close:            c.Close,
			Volume:           c.Volume,
			Color:            color,
			RangePct:         cRangePct,
			WickPct:          cWickPct,
			RejectionReasons: make([]string, 0),
			PassedCriteria:   make([]string, 0),
			Details:          make(map[string]interface{}),
		}

		if i == 0 {
			isMasterBuy := c.Close > pdh && pdh > 0 && c.Close > c.Open
			isMasterSell := c.Close < pdl && pdl > 0 && c.Close < c.Open

			if !isMasterBuy && !isMasterSell {
				diag.Status = "MASTER_REJECTED"
				diag.Verdict = "REJECT"
				if c.Close > pdh && c.Close <= c.Open {
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("1st candle closed above PDH (₹%.2f) but was not GREEN (Open: ₹%.2f, Close: ₹%.2f)", pdh, c.Open, c.Close))
				} else if c.Close < pdl && c.Close >= c.Open {
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("1st candle closed below PDL (₹%.2f) but was not RED (Open: ₹%.2f, Close: ₹%.2f)", pdl, c.Open, c.Close))
				} else {
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("09:15 Close ₹%.2f did not break PDH (₹%.2f) or PDL (₹%.2f)", c.Close, pdh, pdl))
				}
				diag.Details["pdh"] = pdh
				diag.Details["pdl"] = pdl
				diagnostics = append(diagnostics, diag)
				continue
			}

			// Check Opening Gap requirement (matches VandeBharatEngine.OnCandleClose)
			if pdClose > 0 && minGapPct > 0 {
				gapBuyPct := ((c.Open - pdClose) / pdClose) * 100.0
				gapSellPct := ((pdClose - c.Open) / pdClose) * 100.0
				if isMasterBuy && gapBuyPct < minGapPct {
					diag.Status = "MASTER_REJECTED"
					diag.Verdict = "REJECT"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Opening gap +%.2f%% < min required %.2f%% from Yesterday's Close (₹%.2f)", gapBuyPct, minGapPct, pdClose))
					diag.Details["gap_pct"] = gapBuyPct
					diag.Details["min_gap_pct"] = minGapPct
					diag.Details["pd_close"] = pdClose
					diagnostics = append(diagnostics, diag)
					continue
				} else if isMasterSell && gapSellPct < minGapPct {
					diag.Status = "MASTER_REJECTED"
					diag.Verdict = "REJECT"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Opening gap -%.2f%% < min required %.2f%% from Yesterday's Close (₹%.2f)", gapSellPct, minGapPct, pdClose))
					diag.Details["gap_pct"] = gapSellPct
					diag.Details["min_gap_pct"] = minGapPct
					diag.Details["pd_close"] = pdClose
					diagnostics = append(diagnostics, diag)
					continue
				}
			}

			if cRangePct > masterMaxPct || cWickPct > masterMaxWickPct {
				diag.Status = "MASTER_REJECTED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("09:15 Candle failed Master criteria (Range: %.2f%% > %.2f%% or Wick: %.2f%% > %.2f%%)", cRangePct, masterMaxPct, cWickPct, masterMaxWickPct))
				diag.Details["range_pct"] = cRangePct
				diag.Details["wick_pct"] = cWickPct
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT",
					Stage:      "SETUP_INVALIDATED",
					CandleTime: &cTimeCopy,
					Reason:     diag.RejectionReasons[0],
				})
				diagnostics = append(diagnostics, diag)
				continue
			}

			dir = "BUY"
			refLevel := pdh
			if isMasterSell {
				dir = "SELL"
				refLevel = pdl
			}
			cCopy := c
			activeMaster = &cCopy

			diag.Status = "MASTER_ESTABLISHED"
			diag.Verdict = "PASS"
			diag.Details["direction"] = dir
			diag.Details["pdh"] = pdh
			diag.Details["pdl"] = pdl
			diag.Details["range_pct"] = cRangePct
			diag.Details["wick_pct"] = cWickPct
			refName := "PDH"
			if isMasterSell {
				refName = "PDL"
			}
			diag.PassedCriteria = append(diag.PassedCriteria,
				fmt.Sprintf("09:15 Master formed: %s candle broke %s (Close ₹%.2f vs %s ₹%.2f)", color, refName, c.Close, refName, refLevel),
				fmt.Sprintf("Range %.2f%% <= max %.2f%%", cRangePct, masterMaxPct),
				fmt.Sprintf("Wick %.2f%% <= max %.2f%%", cWickPct, masterMaxWickPct),
			)

			events = append(events, data.StrategyEvent{
				EventTime:    cTimeIST,
				Symbol:       symbol,
				Strategy:     "VANDE_BHARAT",
				Stage:        "SETUP_FORMED",
				Direction:    dir,
				TriggerPrice: c.High,
				SLPrice:      c.Low,
				CandleTime:   &cTimeCopy,
				CandleOpen:   c.Open,
				CandleHigh:   c.High,
				CandleLow:    c.Low,
				CandleClose:  c.Close,
				CandleVolume: c.Volume,
				Reason:       fmt.Sprintf("Vande Bharat %s Master Formed [09:15] (Close ₹%.2f vs Ref ₹%.2f, Range %.2f%%, Wick %.2f%%)", dir, c.Close, refLevel, cRangePct, cWickPct),
				Details: map[string]interface{}{
					"pdh":       pdh,
					"pdl":       pdl,
					"pd_close":  pdClose,
					"range_pct": cRangePct,
					"wick_pct":  cWickPct,
				},
			})
			diagnostics = append(diagnostics, diag)
			continue
		}

		if i == 1 {
			if activeMaster == nil {
				diag.Status = "NO_MASTER"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, "No valid Master candle established at 09:15")
				diagnostics = append(diagnostics, diag)
				continue
			}

			if cRangePct < slMinPct || cRangePct > slMaxPct {
				diag.Status = "SL_RANGE_VIOLATION"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("%s Candle 2 failed SL Range threshold (%.2f%% not between %.2f%% and %.2f%%)", cTimeIST.Format("15:04"), cRangePct, slMinPct, slMaxPct))
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT",
					Stage:      "SETUP_INVALIDATED",
					Direction:  dir,
					CandleTime: &cTimeCopy,
					Reason:     diag.RejectionReasons[0],
				})
				activeMaster = nil
				diagnostics = append(diagnostics, diag)
				continue
			}

			if dir == "BUY" {
				if c.Low < activeMaster.Low {
					diag.Status = "SETUP_INVALIDATED"
					diag.Verdict = "FAIL"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle 2 Low ₹%.2f breached Master Low ₹%.2f", c.Low, activeMaster.Low))
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "VANDE_BHARAT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "BUY",
						CandleTime: &cTimeCopy,
						Reason:     diag.RejectionReasons[0],
					})
					activeMaster = nil
					diagnostics = append(diagnostics, diag)
					continue
				}
				slPrice = c.Low
				if c.High > activeMaster.High {
					if c.Close <= activeMaster.Low || c.Close <= c.Open {
						diag.Status = "SETUP_INVALIDATED"
						diag.Verdict = "FAIL"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle 2 broke Master High but closed RED/DOJI (Shooting Star: Open ₹%.2f, Close ₹%.2f)", c.Open, c.Close))
						events = append(events, data.StrategyEvent{
							EventTime:  cTimeIST,
							Symbol:     symbol,
							Strategy:   "VANDE_BHARAT",
							Stage:      "SETUP_INVALIDATED",
							Direction:  "BUY",
							CandleTime: &cTimeCopy,
							Reason:     diag.RejectionReasons[0],
						})
						activeMaster = nil
						diagnostics = append(diagnostics, diag)
						continue
					}
					triggerPrice = c.High
				} else {
					triggerPrice = activeMaster.High
				}
			} else {
				if c.High > activeMaster.High {
					diag.Status = "SETUP_INVALIDATED"
					diag.Verdict = "FAIL"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle 2 High ₹%.2f breached Master High ₹%.2f", c.High, activeMaster.High))
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "VANDE_BHARAT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "SELL",
						CandleTime: &cTimeCopy,
						Reason:     diag.RejectionReasons[0],
					})
					activeMaster = nil
					diagnostics = append(diagnostics, diag)
					continue
				}
				slPrice = c.High
				if c.Low < activeMaster.Low {
					if c.Close >= activeMaster.High || c.Close >= c.Open {
						diag.Status = "SETUP_INVALIDATED"
						diag.Verdict = "FAIL"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle 2 broke Master Low but closed GREEN/DOJI (Hammer: Open ₹%.2f, Close ₹%.2f)", c.Open, c.Close))
						events = append(events, data.StrategyEvent{
							EventTime:  cTimeIST,
							Symbol:     symbol,
							Strategy:   "VANDE_BHARAT",
							Stage:      "SETUP_INVALIDATED",
							Direction:  "SELL",
							CandleTime: &cTimeCopy,
							Reason:     diag.RejectionReasons[0],
						})
						activeMaster = nil
						diagnostics = append(diagnostics, diag)
						continue
					}
					triggerPrice = c.Low
				} else {
					triggerPrice = activeMaster.Low
				}
			}

			isArmed = true
			diag.Status = "CONFIRMATION_ARMED"
			diag.Verdict = "PASS"
			diag.Details["trigger_price"] = triggerPrice
			diag.Details["sl_price"] = slPrice
			diag.Details["rule"] = "Rule 2 (Master Extreme Trigger)"
			if (dir == "BUY" && c.High > activeMaster.High) || (dir == "SELL" && c.Low < activeMaster.Low) {
				diag.Details["rule"] = "Rule 1 (Confirmation Extreme Trigger)"
			}
			diag.PassedCriteria = append(diag.PassedCriteria,
				fmt.Sprintf("Candle 2 armed %s setup via %s", dir, diag.Details["rule"]),
				fmt.Sprintf("SL Range %.2f%% within [%.2f%% - %.2f%%]", cRangePct, slMinPct, slMaxPct),
				fmt.Sprintf("Armed Trigger @ ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
			)

			events = append(events, data.StrategyEvent{
				EventTime:    cTimeIST,
				Symbol:       symbol,
				Strategy:     "VANDE_BHARAT",
				Stage:        "CONFIRMATION_ARMED",
				Direction:    dir,
				TriggerPrice: triggerPrice,
				SLPrice:      slPrice,
				CandleTime:   &cTimeCopy,
				CandleOpen:   c.Open,
				CandleHigh:   c.High,
				CandleLow:    c.Low,
				CandleClose:  c.Close,
				CandleVolume: c.Volume,
				Reason:       fmt.Sprintf("Setup Armed @ 09:20 AM: Trigger @ ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
				Details: map[string]interface{}{
					"sl_range_pct": cRangePct,
					"sl_price":     slPrice,
				},
			})
			diagnostics = append(diagnostics, diag)
			continue
		}

		// Subsequent candles (index >= 2)
		if tradeExecuted {
			diag.Status = "IN_TRADE"
			diag.Verdict = "PASS"
			diag.Details["trigger_price"] = triggerPrice
			diag.Details["sl_price"] = slPrice
			diagnostics = append(diagnostics, diag)
			continue
		}

		if !isArmed {
			diag.Status = "NO_SETUP"
			diag.Verdict = "REJECT"
			diagnostics = append(diagnostics, diag)
			continue
		}

		if timeStr >= tradeEndTime {
			diag.Status = "SETUP_EXPIRED"
			diag.Verdict = "EXPIRED"
			diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Entry cutoff time %s IST reached without trade execution", tradeEndTime))
			events = append(events, data.StrategyEvent{
				EventTime:  cTimeIST,
				Symbol:     symbol,
				Strategy:   "VANDE_BHARAT",
				Stage:      "SETUP_EXPIRED",
				Direction:  dir,
				CandleTime: &cTimeCopy,
				Reason:     diag.RejectionReasons[0],
			})
			isArmed = false
			diagnostics = append(diagnostics, diag)
			continue
		}

		if dir == "BUY" {
			if c.Low < activeMaster.Low {
				diag.Status = "SETUP_INVALIDATED"
				diag.Verdict = "FAIL"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle Low ₹%.2f breached Master Low ₹%.2f before breakout", c.Low, activeMaster.Low))
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "BUY",
					CandleTime: &cTimeCopy,
					Reason:     diag.RejectionReasons[0],
				})
				isArmed = false
				diagnostics = append(diagnostics, diag)
				continue
			}

			if c.High >= triggerPrice {
				diag.Status = "BREAKOUT_TRIGGER"
				diag.Verdict = "ENTRY"
				tgt := triggerPrice + (math.Abs(triggerPrice-slPrice) * 1.5)
				diag.Details["trigger_price"] = triggerPrice
				diag.Details["sl_price"] = slPrice
				diag.Details["executed_price"] = triggerPrice
				diag.Details["target_price"] = tgt
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Breakout trade executed @ ₹%.2f", triggerPrice),
					fmt.Sprintf("Initial SL @ ₹%.2f", slPrice),
					fmt.Sprintf("Target 1 (1:1.5 RR): ₹%.2f", tgt),
				)
				events = append(events, data.StrategyEvent{
					EventTime:     cTimeIST,
					Symbol:        symbol,
					Strategy:      "VANDE_BHARAT",
					Stage:         "TRADE_TAKEN",
					Direction:     "BUY",
					TriggerPrice:  triggerPrice,
					SLPrice:       slPrice,
					ExecutedPrice: triggerPrice,
					CandleTime:    &cTimeCopy,
					Reason:        fmt.Sprintf("Breakout trade executed @ ₹%.2f (Target 1: ₹%.2f)", triggerPrice, tgt),
				})
				isArmed = false
				tradeExecuted = true
				diagnostics = append(diagnostics, diag)
				continue
			}
		} else {
			if c.High > activeMaster.High {
				diag.Status = "SETUP_INVALIDATED"
				diag.Verdict = "FAIL"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f before breakdown", c.High, activeMaster.High))
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "SELL",
					CandleTime: &cTimeCopy,
					Reason:     diag.RejectionReasons[0],
				})
				isArmed = false
				diagnostics = append(diagnostics, diag)
				continue
			}

			if c.Low <= triggerPrice {
				diag.Status = "BREAKOUT_TRIGGER"
				diag.Verdict = "ENTRY"
				tgt := triggerPrice - (math.Abs(triggerPrice-slPrice) * 1.5)
				diag.Details["trigger_price"] = triggerPrice
				diag.Details["sl_price"] = slPrice
				diag.Details["executed_price"] = triggerPrice
				diag.Details["target_price"] = tgt
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Breakdown trade executed @ ₹%.2f", triggerPrice),
					fmt.Sprintf("Initial SL @ ₹%.2f", slPrice),
					fmt.Sprintf("Target 1 (1:1.5 RR): ₹%.2f", tgt),
				)
				events = append(events, data.StrategyEvent{
					EventTime:     cTimeIST,
					Symbol:        symbol,
					Strategy:      "VANDE_BHARAT",
					Stage:         "TRADE_TAKEN",
					Direction:     "SELL",
					TriggerPrice:  triggerPrice,
					SLPrice:       slPrice,
					ExecutedPrice: triggerPrice,
					CandleTime:    &cTimeCopy,
					Reason:        fmt.Sprintf("Breakdown trade executed @ ₹%.2f (Target 1: ₹%.2f)", triggerPrice, tgt),
				})
				isArmed = false
				tradeExecuted = true
				diagnostics = append(diagnostics, diag)
				continue
			}
		}

		diag.Status = "AWAITING_TRIGGER"
		diag.Verdict = "ARMED"
		diag.Details["trigger_price"] = triggerPrice
		diag.Details["sl_price"] = slPrice
		diagnostics = append(diagnostics, diag)
	}

	return events, diagnostics
}

// replayVandeBharatTrap simulates the Vande Bharat Trap Strategy across 5m candles
func (a *AuditAnalyzer) replayVandeBharatTrap(symbol string, today5m []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord, appCfg ...AppliedStrategyConfig) ([]data.StrategyEvent, []CandleDiagnosticItem) {
	events := make([]data.StrategyEvent, 0, 8)
	diagnostics := make([]CandleDiagnosticItem, 0, len(today5m))
	if len(today5m) < 1 {
		return events, diagnostics
	}
	pdh := summary.PDH
	pdl := summary.PDL

	fakeMasterMaxPct := 3.0
	genuineMasterMaxPct := 1.8
	genuineMasterMaxWickPct := 40.0
	slMinPct := 0.5
	slMaxPct := 1.0
	tradeEndTime := "11:00:00"

	if len(appCfg) > 0 {
		if appCfg[0].TradeEndTime != "" {
			tradeEndTime = data.NormalizeTimeHHMMSS(appCfg[0].TradeEndTime)
		}
		p := appCfg[0].Parameters
		fakeMasterMaxPct = getFloatParam(p, fakeMasterMaxPct, "fake_master_max_pct", "master_max_pct")
		genuineMasterMaxPct = getFloatParam(p, genuineMasterMaxPct, "genuine_master_max_pct", "master_max_pct")
		genuineMasterMaxWickPct = getFloatParam(p, genuineMasterMaxWickPct, "genuine_master_max_wick_pct", "master_max_wick_pct")
		slMinPct = getFloatParam(p, slMinPct, "sl_min_pct", "confirm_min_pct")
		slMaxPct = getFloatParam(p, slMaxPct, "sl_max_pct", "confirm_max_pct")
	}

	var fakeMaster *data.Candle
	var genuineMaster *data.Candle
	var genuineMasterIdx int = -1
	var trapDir string
	var triggerPrice, slPrice float64
	var isArmed bool
	var tradeExecuted bool
	var setupInvalidated bool
	var setupExpired bool

	for i, c := range today5m {
		cTimeIST := data.NormalizeToIST(c.Time)
		cTimeCopy := cTimeIST
		timeStr := cTimeIST.Format("15:04:05")
		timeHM := cTimeIST.Format("15:04")

		cRange := c.High - c.Low
		cRangePct := 0.0
		if c.Close > 0 {
			cRangePct = (cRange / c.Close) * 100.0
		}
		cBody := math.Abs(c.Close - c.Open)
		cWick := cRange - cBody
		cWickPct := 0.0
		if cRange > 0 {
			cWickPct = (cWick / cRange) * 100.0
		}
		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}

		diag := CandleDiagnosticItem{
			Time:             timeHM,
			Open:             c.Open,
			High:             c.High,
			Low:              c.Low,
			Close:            c.Close,
			Volume:           c.Volume,
			Color:            color,
			RangePct:         cRangePct,
			WickPct:          cWickPct,
			RejectionReasons: make([]string, 0),
			PassedCriteria:   make([]string, 0),
			Details:          make(map[string]interface{}),
		}

		// 1. Fake Master Candle (09:15 AM IST)
		if i == 0 {
			if cRange <= 0 || c.Close <= 0 {
				diag.Status = "MASTER_REJECTED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, "09:15 Candle has zero range")
				diagnostics = append(diagnostics, diag)
				continue
			}

			// BUY Fake Master: Closes > PDH, but body is RED (Close < Open)
			isFakeMasterBuy := c.Close > pdh && c.Close < c.Open && pdh > 0
			// SELL Fake Master: Closes < PDL, but body is GREEN (Close > Open)
			isFakeMasterSell := c.Close < pdl && c.Close > c.Open && pdl > 0

			if !isFakeMasterBuy && !isFakeMasterSell {
				diag.Status = "MASTER_REJECTED"
				diag.Verdict = "REJECT"
				if c.Close > pdh && c.Close >= c.Open {
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("09:15 Close ₹%.2f broke PDH (₹%.2f) with GREEN candle (continuation, not a trap)", c.Close, pdh))
				} else if c.Close < pdl && c.Close <= c.Open {
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("09:15 Close ₹%.2f broke PDL (₹%.2f) with RED candle (continuation, not a trap)", c.Close, pdl))
				} else {
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("09:15 Close ₹%.2f did not break PDH (₹%.2f) or PDL (₹%.2f)", c.Close, pdh, pdl))
				}
				diagnostics = append(diagnostics, diag)
				continue
			}

			if cRangePct > fakeMasterMaxPct {
				diag.Status = "MASTER_REJECTED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("09:15 Candle range %.2f%% exceeds max Fake Master threshold %.2f%%", cRangePct, fakeMasterMaxPct))
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT_TRAP",
					Stage:      "SETUP_INVALIDATED",
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("09:15 Candle failed Fake Master criteria (Range: %.2f%% > %.2f%%)", cRangePct, fakeMasterMaxPct),
				})
				diagnostics = append(diagnostics, diag)
				continue
			}

			trapDir = "BUY"
			if isFakeMasterSell {
				trapDir = "SELL"
			}
			cCopy := c
			fakeMaster = &cCopy

			diag.Status = "SETUP_FORMED"
			diag.Verdict = "PASS"
			diag.PassedCriteria = append(diag.PassedCriteria,
				fmt.Sprintf("Fake Master formed (%s Trap): 09:15 Opposite body color outside PDH/PDL", trapDir),
				fmt.Sprintf("Range %.2f%% <= max %.2f%%", cRangePct, fakeMasterMaxPct),
			)
			events = append(events, data.StrategyEvent{
				EventTime:    cTimeIST,
				Symbol:       symbol,
				Strategy:     "VANDE_BHARAT_TRAP",
				Stage:        "SETUP_FORMED",
				Direction:    trapDir,
				TriggerPrice: c.High,
				SLPrice:      c.Low,
				CandleTime:   &cTimeCopy,
				CandleOpen:   c.Open,
				CandleHigh:   c.High,
				CandleLow:    c.Low,
				CandleClose:  c.Close,
				CandleVolume: c.Volume,
				Reason:       fmt.Sprintf("Fake Master Candle Formed (09:15 AM %s Trap: Opposite body color outside PDH/PDL)", trapDir),
				Details: map[string]interface{}{
					"pdh":       pdh,
					"pdl":       pdl,
					"range_pct": cRangePct,
				},
			})
			diagnostics = append(diagnostics, diag)
			continue
		}

		// Subsequent candles
		if tradeExecuted {
			diag.Status = "MONITORING"
			diag.Verdict = "INFO"
			diag.PassedCriteria = append(diag.PassedCriteria, "Trade already executed; monitoring position")
			diagnostics = append(diagnostics, diag)
			continue
		}

		if setupInvalidated {
			diag.Status = "MONITORING"
			diag.Verdict = "INFO"
			diag.RejectionReasons = append(diag.RejectionReasons, "Setup previously invalidated")
			diagnostics = append(diagnostics, diag)
			continue
		}

		if setupExpired {
			diag.Status = "PAST_CUTOFF"
			diag.Verdict = "INFO"
			diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Past trade cutoff %s IST", tradeEndTime))
			diagnostics = append(diagnostics, diag)
			continue
		}

		if fakeMaster == nil {
			diag.Status = "MONITORING"
			diag.Verdict = "INFO"
			diag.RejectionReasons = append(diag.RejectionReasons, "No Fake Master established at 09:15")
			diagnostics = append(diagnostics, diag)
			continue
		}

		// Check cutoff
		if timeStr >= tradeEndTime {
			setupExpired = true
			isArmed = false
			diag.Status = "SETUP_EXPIRED"
			diag.Verdict = "REJECT"
			diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Entry cutoff time %s IST reached", tradeEndTime))
			events = append(events, data.StrategyEvent{
				EventTime:  cTimeIST,
				Symbol:     symbol,
				Strategy:   "VANDE_BHARAT_TRAP",
				Stage:      "SETUP_EXPIRED",
				Direction:  trapDir,
				CandleTime: &cTimeCopy,
				Reason:     fmt.Sprintf("Entry cutoff time %s IST reached without trade execution", tradeEndTime),
			})
			diagnostics = append(diagnostics, diag)
			continue
		}

		// 2. Scan for Genuine Master Formation if not yet found
		if genuineMaster == nil {
			isBreach := (trapDir == "BUY" && (c.High > fakeMaster.High || c.Close > fakeMaster.High)) ||
				(trapDir == "SELL" && (c.Low < fakeMaster.Low || c.Close < fakeMaster.Low))

			if isBreach {
				if cRangePct <= genuineMasterMaxPct && cWickPct <= genuineMasterMaxWickPct {
					cCopy := c
					genuineMaster = &cCopy
					genuineMasterIdx = i

					diag.Status = "GENUINE_MASTER_ESTABLISHED"
					diag.Verdict = "PASS"
					diag.PassedCriteria = append(diag.PassedCriteria,
						fmt.Sprintf("Genuine Master Formed: Breached Fake Master extreme (Range: %.2f%% <= %.2f%%, Wick: %.2f%% <= %.2f%%)", cRangePct, genuineMasterMaxPct, cWickPct, genuineMasterMaxWickPct),
					)
					events = append(events, data.StrategyEvent{
						EventTime:    cTimeIST,
						Symbol:       symbol,
						Strategy:     "VANDE_BHARAT_TRAP",
						Stage:        "SETUP_FORMED",
						Direction:    trapDir,
						TriggerPrice: c.High,
						SLPrice:      c.Low,
						CandleTime:   &cTimeCopy,
						CandleOpen:   c.Open,
						CandleHigh:   c.High,
						CandleLow:    c.Low,
						CandleClose:  c.Close,
						CandleVolume: c.Volume,
						Reason:       fmt.Sprintf("Genuine Master Candle Formed (Breached Fake Master, Range: %.2f%% <= %.2f%%)", cRangePct, genuineMasterMaxPct),
						Details: map[string]interface{}{
							"range_pct": cRangePct,
							"wick_pct":  cWickPct,
						},
					})
					diagnostics = append(diagnostics, diag)
					continue
				} else {
					setupInvalidated = true
					diag.Status = "SETUP_INVALIDATED"
					diag.Verdict = "REJECT"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Breached Fake Master but failed Genuine Master criteria (Range: %.2f%% > %.2f%% or Wick: %.2f%% > %.2f%%)", cRangePct, genuineMasterMaxPct, cWickPct, genuineMasterMaxWickPct))
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "VANDE_BHARAT_TRAP",
						Stage:      "SETUP_INVALIDATED",
						Direction:  trapDir,
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Candle breached Fake Master but failed Master criteria (Range: %.2f%% > %.2f%% or Wick: %.2f%% > %.2f%%)", cRangePct, genuineMasterMaxPct, cWickPct, genuineMasterMaxWickPct),
					})
					diagnostics = append(diagnostics, diag)
					continue
				}
			} else {
				diag.Status = "AWAITING_GENUINE_MASTER"
				diag.Verdict = "INFO"
				diag.RejectionReasons = append(diag.RejectionReasons, "Price inside Fake Master range; awaiting Genuine Master formation")
				diagnostics = append(diagnostics, diag)
				continue
			}
		}

		// 3. Candle immediately following Genuine Master (SL Anchor)
		if i == genuineMasterIdx+1 {
			// Invalidation: Opposite breach
			if trapDir == "BUY" && c.Low < genuineMaster.Low {
				setupInvalidated = true
				diag.Status = "SETUP_INVALIDATED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("2nd Candle Low ₹%.2f breached Master Low ₹%.2f", c.Low, genuineMaster.Low))
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT_TRAP",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "BUY",
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("2nd Candle Low ₹%.2f breached Master Low ₹%.2f", c.Low, genuineMaster.Low),
				})
				diagnostics = append(diagnostics, diag)
				continue
			} else if trapDir == "SELL" && c.High > genuineMaster.High {
				setupInvalidated = true
				diag.Status = "SETUP_INVALIDATED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("2nd Candle High ₹%.2f breached Master High ₹%.2f", c.High, genuineMaster.High))
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT_TRAP",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "SELL",
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("2nd Candle High ₹%.2f breached Master High ₹%.2f", c.High, genuineMaster.High),
				})
				diagnostics = append(diagnostics, diag)
				continue
			}

			if cRangePct < slMinPct || cRangePct > slMaxPct {
				setupInvalidated = true
				diag.Status = "SETUP_INVALIDATED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("2nd Candle failed SL range criteria (%.2f%% not between %.2f%% and %.2f%%)", cRangePct, slMinPct, slMaxPct))
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT_TRAP",
					Stage:      "SETUP_INVALIDATED",
					Direction:  trapDir,
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("2nd Candle failed SL range criteria (%.2f%% not between %.2f%% and %.2f%%)", cRangePct, slMinPct, slMaxPct),
				})
				diagnostics = append(diagnostics, diag)
				continue
			}

			if trapDir == "BUY" {
				slPrice = c.Low
				if c.High > genuineMaster.High {
					if c.Close <= genuineMaster.Low || c.Close <= c.Open {
						setupInvalidated = true
						diag.Status = "SETUP_INVALIDATED"
						diag.Verdict = "REJECT"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("2nd Candle broke Master High but closed RED/DOJI (Shooting Star Rejection)"))
						events = append(events, data.StrategyEvent{
							EventTime:  cTimeIST,
							Symbol:     symbol,
							Strategy:   "VANDE_BHARAT_TRAP",
							Stage:      "SETUP_INVALIDATED",
							Direction:  "BUY",
							CandleTime: &cTimeCopy,
							Reason:     fmt.Sprintf("2nd Candle broke Master High but closed RED/DOJI (Shooting Star Rejection: Open ₹%.2f, Close ₹%.2f)", c.Open, c.Close),
						})
						diagnostics = append(diagnostics, diag)
						continue
					}
					triggerPrice = c.High
				} else {
					triggerPrice = genuineMaster.High
				}
			} else {
				slPrice = c.High
				if c.Low < genuineMaster.Low {
					if c.Close >= genuineMaster.High || c.Close >= c.Open {
						setupInvalidated = true
						diag.Status = "SETUP_INVALIDATED"
						diag.Verdict = "REJECT"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("2nd Candle broke Master Low but closed GREEN/DOJI (Hammer Rejection)"))
						events = append(events, data.StrategyEvent{
							EventTime:  cTimeIST,
							Symbol:     symbol,
							Strategy:   "VANDE_BHARAT_TRAP",
							Stage:      "SETUP_INVALIDATED",
							Direction:  "SELL",
							CandleTime: &cTimeCopy,
							Reason:     fmt.Sprintf("2nd Candle broke Master Low but closed GREEN/DOJI (Hammer Rejection: Open ₹%.2f, Close ₹%.2f)", c.Open, c.Close),
						})
						diagnostics = append(diagnostics, diag)
						continue
					}
					triggerPrice = c.Low
				} else {
					triggerPrice = genuineMaster.Low
				}
			}

			isArmed = true
			diag.Status = "CONFIRMATION_ARMED"
			diag.Verdict = "PASS"
			diag.Details["trigger_price"] = triggerPrice
			diag.Details["sl_price"] = slPrice
			diag.PassedCriteria = append(diag.PassedCriteria,
				fmt.Sprintf("Trap Setup Armed: Trigger @ ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
				fmt.Sprintf("SL range %.2f%% within limits (%.2f%% - %.2f%%)", cRangePct, slMinPct, slMaxPct),
			)
			events = append(events, data.StrategyEvent{
				EventTime:    cTimeIST,
				Symbol:       symbol,
				Strategy:     "VANDE_BHARAT_TRAP",
				Stage:        "CONFIRMATION_ARMED",
				Direction:    trapDir,
				TriggerPrice: triggerPrice,
				SLPrice:      slPrice,
				CandleTime:   &cTimeCopy,
				CandleOpen:   c.Open,
				CandleHigh:   c.High,
				CandleLow:    c.Low,
				CandleClose:  c.Close,
				CandleVolume: c.Volume,
				Reason:       fmt.Sprintf("Trap Setup Armed: Trigger @ ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
				Details: map[string]interface{}{
					"sl_range_pct": cRangePct,
					"sl_price":     slPrice,
				},
			})
			diagnostics = append(diagnostics, diag)
			continue
		}

		// 4. Armed Waiting & Execution (Candle 3 onwards after Genuine Master)
		if !isArmed {
			diag.Status = "MONITORING"
			diag.Verdict = "INFO"
			diag.RejectionReasons = append(diag.RejectionReasons, "No active setup armed")
			diagnostics = append(diagnostics, diag)
			continue
		}

		if trapDir == "BUY" {
			if c.Low < genuineMaster.Low {
				setupInvalidated = true
				isArmed = false
				diag.Status = "INVALIDATED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle Low ₹%.2f breached Master Low ₹%.2f before breakout", c.Low, genuineMaster.Low))
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT_TRAP",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "BUY",
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("Candle Low ₹%.2f breached Master Low ₹%.2f before breakout", c.Low, genuineMaster.Low),
				})
				diagnostics = append(diagnostics, diag)
				continue
			}

			if c.High >= triggerPrice {
				tradeExecuted = true
				isArmed = false
				tgt := triggerPrice + (math.Abs(triggerPrice-slPrice) * 1.5)
				diag.Status = "BREAKOUT_TRIGGERED"
				diag.Verdict = "PASS"
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Trap breakout trade executed @ ₹%.2f (Target 1: ₹%.2f)", triggerPrice, tgt),
					fmt.Sprintf("Buy Entry @ ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
				)
				events = append(events, data.StrategyEvent{
					EventTime:     cTimeIST,
					Symbol:        symbol,
					Strategy:      "VANDE_BHARAT_TRAP",
					Stage:         "TRADE_TAKEN",
					Direction:     "BUY",
					TriggerPrice:  triggerPrice,
					SLPrice:       slPrice,
					ExecutedPrice: triggerPrice,
					CandleTime:    &cTimeCopy,
					Reason:        fmt.Sprintf("Trap breakout trade executed @ ₹%.2f (Target 1: ₹%.2f)", triggerPrice, tgt),
				})
				diagnostics = append(diagnostics, diag)
				continue
			}

			diag.Status = "AWAITING_TRIGGER"
			diag.Verdict = "ARMED"
			diag.Details["trigger_price"] = triggerPrice
			diag.Details["sl_price"] = slPrice
			diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("High ₹%.2f held below trigger ₹%.2f; awaiting breakout", c.High, triggerPrice))
			diagnostics = append(diagnostics, diag)
			continue
		} else {
			if c.High > genuineMaster.High {
				setupInvalidated = true
				isArmed = false
				diag.Status = "INVALIDATED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f before breakdown", c.High, genuineMaster.High))
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT_TRAP",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "SELL",
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f before breakdown", c.High, genuineMaster.High),
				})
				diagnostics = append(diagnostics, diag)
				continue
			}

			if c.Low <= triggerPrice {
				tradeExecuted = true
				isArmed = false
				tgt := triggerPrice - (math.Abs(triggerPrice-slPrice) * 1.5)
				diag.Status = "BREAKDOWN_TRIGGERED"
				diag.Verdict = "PASS"
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Trap breakdown trade executed @ ₹%.2f (Target 1: ₹%.2f)", triggerPrice, tgt),
					fmt.Sprintf("Sell Entry @ ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
				)
				events = append(events, data.StrategyEvent{
					EventTime:     cTimeIST,
					Symbol:        symbol,
					Strategy:      "VANDE_BHARAT_TRAP",
					Stage:         "TRADE_TAKEN",
					Direction:     "SELL",
					TriggerPrice:  triggerPrice,
					SLPrice:       slPrice,
					ExecutedPrice: triggerPrice,
					CandleTime:    &cTimeCopy,
					Reason:        fmt.Sprintf("Trap breakdown trade executed @ ₹%.2f (Target 1: ₹%.2f)", triggerPrice, tgt),
				})
				diagnostics = append(diagnostics, diag)
				continue
			}

			diag.Status = "AWAITING_TRIGGER"
			diag.Verdict = "ARMED"
			diag.Details["trigger_price"] = triggerPrice
			diag.Details["sl_price"] = slPrice
			diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Low ₹%.2f held above trigger ₹%.2f; awaiting breakdown", c.Low, triggerPrice))
			diagnostics = append(diagnostics, diag)
			continue
		}
	}

	return events, diagnostics
}

// replayLowVolume simulates Low Volume Scalp strategy
// replayLowVolume simulates Low Volume Scalp strategy
func (a *AuditAnalyzer) replayLowVolume(symbol string, today5m []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord, appCfg AppliedStrategyConfig, liveEvents ...[]data.StrategyEvent) ([]data.StrategyEvent, []CandleDiagnosticItem) {
	events := make([]data.StrategyEvent, 0)
	diagnostics := make([]CandleDiagnosticItem, 0, len(today5m))
	if len(today5m) < 2 {
		return events, diagnostics
	}

	tradeEndTime := "10:45:00"
	if appCfg.TradeEndTime != "" {
		tradeEndTime = data.NormalizeTimeHHMMSS(appCfg.TradeEndTime)
	}
	minCandlesToIgnore := getIntParam(appCfg.Parameters, 3, "min_candles_to_ignore")

	hasRealTradeOnCandle := func(cTime time.Time, dir string) *data.TradeHistoryRecord {
		for i := range trades {
			tr := &trades[i]
			if strings.EqualFold(tr.Symbol, symbol) && tr.Side == dir {
				trTime := data.NormalizeToIST(tr.CreatedAt)
				if tr.EntryTime.After(time.Time{}) {
					trTime = data.NormalizeToIST(tr.EntryTime)
				}
				if !trTime.Before(cTime) && trTime.Before(cTime.Add(5*time.Minute)) {
					return tr
				}
			}
		}
		return nil
	}

	c1 := today5m[0]
	c1TimeIST := data.NormalizeToIST(c1.Time)
	if c1TimeIST.Hour() != 9 || c1TimeIST.Minute() != 15 {
		return events, diagnostics
	}

	// 1. Check 1st candle PDH/PDL qualification
	var dir string
	if summary.PDH > 0 && c1.Close > summary.PDH {
		dir = "BUY"
	} else if summary.PDL > 0 && c1.Close < summary.PDL {
		dir = "SELL"
	}

	// Track lowest volume candle among completed session candles
	var lowestVol int64 = -1
	var setupCandle *data.Candle
	tradeExecuted := false

	for i, c := range today5m {
		cTimeIST := data.NormalizeToIST(c.Time)
		cTimeCopy := cTimeIST
		timeStr := cTimeIST.Format("15:04:05")
		cRange := c.High - c.Low
		rangePct := 0.0
		if c.Close > 0 {
			rangePct = (cRange / c.Close) * 100.0
		}
		body := math.Abs(c.Close - c.Open)
		wickSize := cRange - body
		wickPct := 0.0
		if cRange > 0 {
			wickPct = (wickSize / cRange) * 100.0
		}
		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}

		diag := CandleDiagnosticItem{
			Time:             cTimeIST.Format("15:04"),
			Open:             c.Open,
			High:             c.High,
			Low:              c.Low,
			Close:            c.Close,
			Volume:           c.Volume,
			Color:            color,
			RangePct:         rangePct,
			WickPct:          wickPct,
			RejectionReasons: make([]string, 0),
			PassedCriteria:   make([]string, 0),
			Details:          make(map[string]interface{}),
		}

		if i == 0 {
			if dir == "" {
				diag.Status = "MASTER_REJECTED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("09:15 Close ₹%.2f did not break PDH (₹%.2f) or PDL (₹%.2f)", c.Close, summary.PDH, summary.PDL))
				diagnostics = append(diagnostics, diag)
				continue
			}

			events = append(events, data.StrategyEvent{
				EventTime:    cTimeIST,
				Symbol:       symbol,
				Strategy:     "LOW_VOLUME",
				Stage:        "SETUP_FORMED",
				Direction:    dir,
				CandleTime:   &cTimeCopy,
				CandleOpen:   c.Open,
				CandleHigh:   c.High,
				CandleLow:    c.Low,
				CandleClose:  c.Close,
				CandleVolume: c.Volume,
				Reason:       fmt.Sprintf("Low Volume Master qualified: 09:15 candle closed beyond PD level (%s)", dir),
			})

			diag.Status = "MASTER_ESTABLISHED"
			diag.Verdict = "PASS"
			refName := "PDH"
			refVal := summary.PDH
			if dir == "SELL" {
				refName = "PDL"
				refVal = summary.PDL
			}
			diag.PassedCriteria = append(diag.PassedCriteria,
				fmt.Sprintf("09:15 Master formed: %s candle broke %s (Close ₹%.2f vs %s ₹%.2f)", color, refName, c.Close, refName, refVal),
				fmt.Sprintf("Candle Volume: %d", c.Volume),
			)
			diagnostics = append(diagnostics, diag)
			continue
		}

		if dir == "" {
			diag.Status = "NO_MASTER"
			diag.Verdict = "REJECT"
			diag.RejectionReasons = append(diag.RejectionReasons, "No valid Master established at 09:15 (Close within PDH/PDL)")
			diagnostics = append(diagnostics, diag)
			continue
		}

		if i < minCandlesToIgnore {
			diag.Status = "MONITORING"
			diag.Verdict = "REJECT"
			diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Within initial %d candles to ignore (candle %d)", minCandlesToIgnore, i+1))
			diagnostics = append(diagnostics, diag)
			continue
		}

		// Check if real trade occurred on this candle
		realTrade := hasRealTradeOnCandle(cTimeIST, dir)
		if realTrade != nil {
			tradeExecuted = true
			diag.Status = "TRADE_TAKEN"
			diag.Verdict = "PASS"
			diag.PassedCriteria = append(diag.PassedCriteria,
				fmt.Sprintf("Live trade executed: %d shares @ ₹%.2f", realTrade.Quantity, realTrade.EntryPrice),
				fmt.Sprintf("Initial SL placed @ ₹%.2f", realTrade.ExitPrice),
				fmt.Sprintf("Trade PnL: ₹%.2f", realTrade.PnL),
			)
			diagnostics = append(diagnostics, diag)
			continue
		}

		if tradeExecuted {
			diag.Status = "IN_TRADE"
			diag.Verdict = "PASS"
			diag.PassedCriteria = append(diag.PassedCriteria, "Position active / trade already completed")
			diagnostics = append(diagnostics, diag)
			continue
		}

		if timeStr >= tradeEndTime {
			diag.Status = "SETUP_EXPIRED"
			diag.Verdict = "EXPIRED"
			diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Entry cutoff time %s IST reached", tradeEndTime))
			events = append(events, data.StrategyEvent{
				EventTime:  cTimeIST,
				Symbol:     symbol,
				Strategy:   "LOW_VOLUME",
				Stage:      "SETUP_EXPIRED",
				Direction:  dir,
				CandleTime: &cTimeCopy,
				Reason:     fmt.Sprintf("Entry cutoff time %s IST reached", tradeEndTime),
			})
			diagnostics = append(diagnostics, diag)
			continue
		}

		// Update setup candle if current candle volume is lowest so far
		if lowestVol == -1 || c.Volume < lowestVol {
			lowestVol = c.Volume
			cCopy := c
			setupCandle = &cCopy

			var triggerPrice, slPrice float64
			if dir == "BUY" {
				triggerPrice = setupCandle.High
				slPrice = setupCandle.Low
			} else {
				triggerPrice = setupCandle.Low
				slPrice = setupCandle.High
			}

			isColorMatch := (dir == "BUY" && c.Close < c.Open) || (dir == "SELL" && c.Close > c.Open)
			if isColorMatch {
				events = append(events, data.StrategyEvent{
					EventTime:    cTimeIST,
					Symbol:       symbol,
					Strategy:     "LOW_VOLUME",
					Stage:        "CONFIRMATION_ARMED",
					Direction:    dir,
					TriggerPrice: triggerPrice,
					SLPrice:      slPrice,
					CandleTime:   &cTimeCopy,
					CandleOpen:   c.Open,
					CandleHigh:   c.High,
					CandleLow:    c.Low,
					CandleClose:  c.Close,
					CandleVolume: c.Volume,
					Reason:       fmt.Sprintf("Setup Candle updated with lowest volume (%d). Trigger @ ₹%.2f, SL @ ₹%.2f", lowestVol, triggerPrice, slPrice),
				})
				diag.Status = "CONFIRMATION_ARMED"
				diag.Verdict = "PASS"
				diag.Details["trigger_price"] = triggerPrice
				diag.Details["sl_price"] = slPrice
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Lowest volume (%d) qualified as Setup Candle", c.Volume),
					fmt.Sprintf("Candle color %s matches %s bias", color, dir),
					fmt.Sprintf("Armed %s trigger @ ₹%.2f, SL @ ₹%.2f", dir, triggerPrice, slPrice),
				)
			} else {
				diag.Status = "MONITORING"
				diag.Verdict = "REJECT"
				reqColor := "RED"
				if dir == "SELL" {
					reqColor = "GREEN"
				}
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Lowest volume (%d) but candle is %s (requires %s for %s bias)", c.Volume, color, reqColor, dir))
			}
			diagnostics = append(diagnostics, diag)
			continue
		} else if setupCandle != nil {
			// In LowVolumeEngine: only consider setup candle if it is immediately previous completed candle
			if i > 0 && !today5m[i-1].Time.Equal(setupCandle.Time) {
				diag.Status = "MONITORING"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, "Setup Candle expired (not the immediately preceding completed candle)")
				diagnostics = append(diagnostics, diag)
				continue
			}
			var triggerPrice, slPrice float64
			if dir == "BUY" {
				triggerPrice = setupCandle.High
				slPrice = setupCandle.Low
			} else {
				triggerPrice = setupCandle.Low
				slPrice = setupCandle.High
			}

			if dir == "BUY" && c.High >= setupCandle.High {
				slDist := setupCandle.High - setupCandle.Low
				tgt := setupCandle.High + (slDist * 1.5)
				events = append(events, data.StrategyEvent{
					EventTime:     cTimeIST,
					Symbol:        symbol,
					Strategy:      "LOW_VOLUME",
					Stage:         "TRADE_TAKEN",
					Direction:     "BUY",
					TriggerPrice:  setupCandle.High,
					SLPrice:       setupCandle.Low,
					ExecutedPrice: setupCandle.High,
					CandleTime:    &cTimeCopy,
					Reason:        fmt.Sprintf("Low Volume breakout trade executed @ ₹%.2f (Target: ₹%.2f)", setupCandle.High, tgt),
				})
				tradeExecuted = true
				diag.Status = "BREAKOUT_TRIGGERED"
				diag.Verdict = "PASS"
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Breakout triggered: High ₹%.2f broke Setup High ₹%.2f", c.High, setupCandle.High),
					fmt.Sprintf("Buy Entry @ ₹%.2f, SL @ ₹%.2f", setupCandle.High, setupCandle.Low),
					fmt.Sprintf("Target 1: ₹%.2f", tgt),
				)
				diagnostics = append(diagnostics, diag)
				continue
			} else if dir == "SELL" && c.Low <= setupCandle.Low {
				slDist := setupCandle.High - setupCandle.Low
				tgt := setupCandle.Low - (slDist * 1.5)
				events = append(events, data.StrategyEvent{
					EventTime:     cTimeIST,
					Symbol:        symbol,
					Strategy:      "LOW_VOLUME",
					Stage:         "TRADE_TAKEN",
					Direction:     "SELL",
					TriggerPrice:  setupCandle.Low,
					SLPrice:       setupCandle.High,
					ExecutedPrice: setupCandle.Low,
					CandleTime:    &cTimeCopy,
					Reason:        fmt.Sprintf("Low Volume breakdown trade executed @ ₹%.2f (Target: ₹%.2f)", setupCandle.Low, tgt),
				})
				tradeExecuted = true
				diag.Status = "BREAKDOWN_TRIGGERED"
				diag.Verdict = "PASS"
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Breakdown triggered: Low ₹%.2f broke Setup Low ₹%.2f", c.Low, setupCandle.Low),
					fmt.Sprintf("Sell Entry @ ₹%.2f, SL @ ₹%.2f", setupCandle.Low, setupCandle.High),
					fmt.Sprintf("Target 1: ₹%.2f", tgt),
				)
				diagnostics = append(diagnostics, diag)
				continue
			} else {
				diag.Status = "AWAITING_TRIGGER"
				diag.Verdict = "ARMED"
				diag.Details["trigger_price"] = triggerPrice
				diag.Details["sl_price"] = slPrice
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Volume %d > setup candle (%d); awaiting breakout of ₹%.2f", c.Volume, lowestVol, triggerPrice))
				diagnostics = append(diagnostics, diag)
				continue
			}
		} else {
			diag.Status = "MONITORING"
			diag.Verdict = "INFO"
			diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Volume %d > lowest seen (%d)", c.Volume, lowestVol))
			diagnostics = append(diagnostics, diag)
			continue
		}
	}

	return events, diagnostics
}

// replayFakeBreakout simulates Fake Breakout strategy
func (a *AuditAnalyzer) replayFakeBreakout(symbol string, todayCandles []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord, appCfg ...AppliedStrategyConfig) ([]data.StrategyEvent, []CandleDiagnosticItem) {
	events := make([]data.StrategyEvent, 0, 8)
	diagnostics := make([]CandleDiagnosticItem, 0, len(todayCandles))
	if len(todayCandles) < 1 {
		return events, diagnostics
	}

	gapUpMinPct := 4.0
	gapUpMaxPct := 8.0
	gapDownMinPct := 4.0
	gapDownMaxPct := 8.0
	confirmMaxPct := 1.0
	masterMaxWickPct := 40.0
	tradeEndTime := "11:00:00"

	if len(appCfg) > 0 {
		if appCfg[0].TradeEndTime != "" {
			tradeEndTime = data.NormalizeTimeHHMMSS(appCfg[0].TradeEndTime)
		}
		p := appCfg[0].Parameters
		gapUpMinPct = getFloatParam(p, gapUpMinPct, "gap_up_min_pct", "gapup_min_pct")
		gapUpMaxPct = getFloatParam(p, gapUpMaxPct, "gap_up_max_pct", "gapup_max_pct")
		gapDownMinPct = getFloatParam(p, gapDownMinPct, "gap_down_min_pct", "gapdown_min_pct")
		gapDownMaxPct = getFloatParam(p, gapDownMaxPct, "gap_down_max_pct", "gapdown_max_pct")
		confirmMaxPct = getFloatParam(p, confirmMaxPct, "confirm_max_pct", "max_confirmation_pct")
		masterMaxWickPct = getFloatParam(p, masterMaxWickPct, "master_max_wick_pct")
	}

	pdClose := summary.PDClose
	if pdClose <= 0 && len(todayCandles) > 0 {
		pdClose = todayCandles[0].Open
	}

	var activeMaster *data.Candle
	var dir string
	var triggerPrice, slPrice float64
	var isArmed bool
	var tradeExecuted bool
	var setupInvalidated bool
	var setupExpired bool

	for i, c := range todayCandles {
		cTimeIST := data.NormalizeToIST(c.Time)
		cTimeCopy := cTimeIST
		timeStr := cTimeIST.Format("15:04:05")
		timeHM := cTimeIST.Format("15:04")

		cRange := c.High - c.Low
		cRangePct := 0.0
		if c.Close > 0 {
			cRangePct = (cRange / c.Close) * 100.0
		}
		cBody := math.Abs(c.Close - c.Open)
		cWick := cRange - cBody
		cWickPct := 0.0
		if cRange > 0 {
			cWickPct = (cWick / cRange) * 100.0
		}
		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}

		diag := CandleDiagnosticItem{
			Time:             timeHM,
			Open:             c.Open,
			High:             c.High,
			Low:              c.Low,
			Close:            c.Close,
			Volume:           c.Volume,
			Color:            color,
			RangePct:         cRangePct,
			WickPct:          cWickPct,
			RejectionReasons: make([]string, 0),
			PassedCriteria:   make([]string, 0),
			Details:          make(map[string]interface{}),
		}

		// 1. First Candle (09:15 AM IST)
		if i == 0 {
			if cRange <= 0 {
				diag.Status = "MASTER_REJECTED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, "09:15 Candle has zero range")
				diagnostics = append(diagnostics, diag)
				continue
			}

			gapUpPct := ((c.Open - pdClose) / pdClose) * 100.0
			gapDownPct := ((pdClose - c.Open) / pdClose) * 100.0
			diag.Details["gap_up_pct"] = gapUpPct
			diag.Details["gap_down_pct"] = gapDownPct
			diag.Details["pd_close"] = pdClose

			isRed := c.Close < c.Open
			isGreen := c.Close > c.Open

			var upperWick, lowerWick float64
			if isRed {
				upperWick = c.High - c.Open
				lowerWick = c.Close - c.Low
			} else if isGreen {
				upperWick = c.High - c.Close
				lowerWick = c.Open - c.Low
			}
			totalWickPct := ((upperWick + lowerWick) / cRange) * 100.0
			diag.Details["total_wick_pct"] = totalWickPct

			if totalWickPct > masterMaxWickPct {
				diag.Status = "MASTER_REJECTED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons,
					fmt.Sprintf("09:15 Candle wicks (%.2f%%) exceed max allowed (%.2f%%)", totalWickPct, masterMaxWickPct))
				diagnostics = append(diagnostics, diag)
				continue
			}

			if isRed && gapUpPct >= gapUpMinPct && gapUpPct <= gapUpMaxPct {
				dir = "SELL"
				cCopy := c
				activeMaster = &cCopy
				diag.Status = "MASTER_ESTABLISHED"
				diag.Verdict = "PASS"
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Bearish RED candle (Close ₹%.2f < Open ₹%.2f)", c.Close, c.Open),
					fmt.Sprintf("Excessive opening gap up +%.2f%% matches SELL exhaustion trap (allowed: %.2f%% - %.2f%%)", gapUpPct, gapUpMinPct, gapUpMaxPct),
					fmt.Sprintf("Total wick %.2f%% <= max %.2f%%", totalWickPct, masterMaxWickPct),
					fmt.Sprintf("Master candle established: Low @ ₹%.2f, High @ ₹%.2f", c.Low, c.High),
				)
				events = append(events, data.StrategyEvent{
					EventTime:    cTimeIST,
					Symbol:       symbol,
					Strategy:     "FAKE_BREAKOUT",
					Stage:        "SETUP_FORMED",
					Direction:    "SELL",
					CandleTime:   &cTimeCopy,
					CandleOpen:   c.Open,
					CandleHigh:   c.High,
					CandleLow:    c.Low,
					CandleClose:  c.Close,
					CandleVolume: c.Volume,
					Reason:       fmt.Sprintf("Fake Breakout 09:15 Master formed: SELL (Gap: +%.2f%%)", gapUpPct),
				})
				diagnostics = append(diagnostics, diag)
				continue
			} else if isGreen && gapDownPct >= gapDownMinPct && gapDownPct <= gapDownMaxPct {
				dir = "BUY"
				cCopy := c
				activeMaster = &cCopy
				diag.Status = "MASTER_ESTABLISHED"
				diag.Verdict = "PASS"
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Bullish GREEN candle (Close ₹%.2f > Open ₹%.2f)", c.Close, c.Open),
					fmt.Sprintf("Excessive opening gap down -%.2f%% matches BUY bounce trap (allowed: %.2f%% - %.2f%%)", gapDownPct, gapDownMinPct, gapDownMaxPct),
					fmt.Sprintf("Total wick %.2f%% <= max %.2f%%", totalWickPct, masterMaxWickPct),
					fmt.Sprintf("Master candle established: High @ ₹%.2f, Low @ ₹%.2f", c.High, c.Low),
				)
				events = append(events, data.StrategyEvent{
					EventTime:    cTimeIST,
					Symbol:       symbol,
					Strategy:     "FAKE_BREAKOUT",
					Stage:        "SETUP_FORMED",
					Direction:    "BUY",
					CandleTime:   &cTimeCopy,
					CandleOpen:   c.Open,
					CandleHigh:   c.High,
					CandleLow:    c.Low,
					CandleClose:  c.Close,
					CandleVolume: c.Volume,
					Reason:       fmt.Sprintf("Fake Breakout 09:15 Master formed: BUY (Gap: -%.2f%%)", gapDownPct),
				})
				diagnostics = append(diagnostics, diag)
				continue
			} else {
				diag.Status = "MASTER_REJECTED"
				diag.Verdict = "REJECT"
				if gapUpPct < gapUpMinPct && gapDownPct < gapDownMinPct {
					diag.RejectionReasons = append(diag.RejectionReasons,
						fmt.Sprintf("Opening gap (+%.2f%% / -%.2f%%) does not meet min threshold (%.2f%% / %.2f%%) from Yesterday's Close (₹%.2f)", gapUpPct, gapDownPct, gapUpMinPct, gapDownMinPct, pdClose))
				} else if isRed && gapUpPct > gapUpMaxPct {
					diag.RejectionReasons = append(diag.RejectionReasons,
						fmt.Sprintf("Opening gap up +%.2f%% exceeds max allowed %.2f%%", gapUpPct, gapUpMaxPct))
				} else if isGreen && gapDownPct > gapDownMaxPct {
					diag.RejectionReasons = append(diag.RejectionReasons,
						fmt.Sprintf("Opening gap down -%.2f%% exceeds max allowed %.2f%%", gapDownPct, gapDownMaxPct))
				} else if isGreen && gapUpPct >= gapUpMinPct {
					diag.RejectionReasons = append(diag.RejectionReasons,
						fmt.Sprintf("Opening gap up +%.2f%% but candle closed GREEN (Close ₹%.2f > Open ₹%.2f); requires counter-trend RED exhaustion candle", gapUpPct, c.Close, c.Open))
				} else if isRed && gapDownPct >= gapDownMinPct {
					diag.RejectionReasons = append(diag.RejectionReasons,
						fmt.Sprintf("Opening gap down -%.2f%% but candle closed RED (Close ₹%.2f < Open ₹%.2f); requires counter-trend GREEN bounce candle", gapDownPct, c.Close, c.Open))
				} else {
					diag.RejectionReasons = append(diag.RejectionReasons,
						fmt.Sprintf("09:15 Candle did not qualify as Fake Breakout Master (Gap: +%.2f%% / -%.2f%%)", gapUpPct, gapDownPct))
				}
				diagnostics = append(diagnostics, diag)
				continue
			}
		}

		// 2. Second Candle: Confirmation Check
		if i == 1 {
			if activeMaster == nil {
				diag.Status = "MONITORING"
				diag.Verdict = "INFO"
				diag.RejectionReasons = append(diag.RejectionReasons, "No 09:15 Master candle established")
				diagnostics = append(diagnostics, diag)
				continue
			}

			if cRangePct > confirmMaxPct {
				diag.Status = "SETUP_INVALIDATED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons,
					fmt.Sprintf("Candle 2 range %.2f%% exceeds max confirmation threshold %.2f%%", cRangePct, confirmMaxPct))
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "FAKE_BREAKOUT",
					Stage:      "SETUP_INVALIDATED",
					Direction:  dir,
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("Candle 2 range %.2f%% exceeds max confirmation threshold %.2f%%", cRangePct, confirmMaxPct),
				})
				setupInvalidated = true
				diagnostics = append(diagnostics, diag)
				continue
			}

			if dir == "SELL" {
				isRed := c.Close < c.Open
				breaksMasterLow := c.Low < activeMaster.Low
				if !isRed || !breaksMasterLow {
					diag.Status = "SETUP_INVALIDATED"
					diag.Verdict = "REJECT"
					if !isRed {
						diag.RejectionReasons = append(diag.RejectionReasons, "Candle 2 closed GREEN or DOJI; requires RED confirmation")
					}
					if !breaksMasterLow {
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle 2 Low ₹%.2f did not break Master Low ₹%.2f", c.Low, activeMaster.Low))
					}
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "FAKE_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "SELL",
						CandleTime: &cTimeCopy,
						Reason:     "Candle 2 failed SELL confirmation: did not break Master Low as a RED candle",
					})
					setupInvalidated = true
					diagnostics = append(diagnostics, diag)
					continue
				}

				triggerPrice = c.Low
				slPrice = c.High
				isArmed = true

				diag.Status = "CONFIRMATION_ARMED"
				diag.Verdict = "PASS"
				diag.Details["trigger_price"] = triggerPrice
				diag.Details["sl_price"] = slPrice
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Bearish RED confirmation candle (Close ₹%.2f < Open ₹%.2f)", c.Close, c.Open),
					fmt.Sprintf("Low ₹%.2f broke Master Low ₹%.2f", c.Low, activeMaster.Low),
					fmt.Sprintf("Range %.2f%% <= max %.2f%%", cRangePct, confirmMaxPct),
					fmt.Sprintf("Armed SELL trigger below ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
				)
				events = append(events, data.StrategyEvent{
					EventTime:    cTimeIST,
					Symbol:       symbol,
					Strategy:     "FAKE_BREAKOUT",
					Stage:        "CONFIRMATION_ARMED",
					Direction:    "SELL",
					TriggerPrice: triggerPrice,
					SLPrice:      slPrice,
					CandleTime:   &cTimeCopy,
					CandleOpen:   c.Open,
					CandleHigh:   c.High,
					CandleLow:    c.Low,
					CandleClose:  c.Close,
					CandleVolume: c.Volume,
					Reason:       fmt.Sprintf("Fake Breakout Armed: Trigger @ ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
				})
				diagnostics = append(diagnostics, diag)
				continue
			} else {
				// BUY confirmation
				isGreen := c.Close > c.Open
				breaksMasterHigh := c.High > activeMaster.High
				if !isGreen || !breaksMasterHigh {
					diag.Status = "SETUP_INVALIDATED"
					diag.Verdict = "REJECT"
					if !isGreen {
						diag.RejectionReasons = append(diag.RejectionReasons, "Candle 2 closed RED or DOJI; requires GREEN confirmation")
					}
					if !breaksMasterHigh {
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle 2 High ₹%.2f did not break Master High ₹%.2f", c.High, activeMaster.High))
					}
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "FAKE_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "BUY",
						CandleTime: &cTimeCopy,
						Reason:     "Candle 2 failed BUY confirmation: did not break Master High as a GREEN candle",
					})
					setupInvalidated = true
					diagnostics = append(diagnostics, diag)
					continue
				}

				triggerPrice = c.High
				slPrice = c.Low
				isArmed = true

				diag.Status = "CONFIRMATION_ARMED"
				diag.Verdict = "PASS"
				diag.Details["trigger_price"] = triggerPrice
				diag.Details["sl_price"] = slPrice
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Bullish GREEN confirmation candle (Close ₹%.2f > Open ₹%.2f)", c.Close, c.Open),
					fmt.Sprintf("High ₹%.2f broke Master High ₹%.2f", c.High, activeMaster.High),
					fmt.Sprintf("Range %.2f%% <= max %.2f%%", cRangePct, confirmMaxPct),
					fmt.Sprintf("Armed BUY trigger above ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
				)
				events = append(events, data.StrategyEvent{
					EventTime:    cTimeIST,
					Symbol:       symbol,
					Strategy:     "FAKE_BREAKOUT",
					Stage:        "CONFIRMATION_ARMED",
					Direction:    "BUY",
					TriggerPrice: triggerPrice,
					SLPrice:      slPrice,
					CandleTime:   &cTimeCopy,
					CandleOpen:   c.Open,
					CandleHigh:   c.High,
					CandleLow:    c.Low,
					CandleClose:  c.Close,
					CandleVolume: c.Volume,
					Reason:       fmt.Sprintf("Fake Breakout Armed: Trigger @ ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
				})
				diagnostics = append(diagnostics, diag)
				continue
			}
		}

		// 3. Subsequent Candles (09:25 AM onwards)
		if tradeExecuted {
			diag.Status = "MONITORING"
			diag.Verdict = "INFO"
			diag.PassedCriteria = append(diag.PassedCriteria, "Trade already executed; monitoring position")
			diagnostics = append(diagnostics, diag)
			continue
		}

		if setupInvalidated {
			diag.Status = "MONITORING"
			diag.Verdict = "INFO"
			diag.RejectionReasons = append(diag.RejectionReasons, "Setup previously invalidated")
			diagnostics = append(diagnostics, diag)
			continue
		}

		if setupExpired {
			diag.Status = "PAST_CUTOFF"
			diag.Verdict = "INFO"
			diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Past trade cutoff %s IST", tradeEndTime))
			diagnostics = append(diagnostics, diag)
			continue
		}

		if !isArmed || activeMaster == nil {
			diag.Status = "MONITORING"
			diag.Verdict = "INFO"
			diag.RejectionReasons = append(diag.RejectionReasons, "No active setup armed")
			diagnostics = append(diagnostics, diag)
			continue
		}

		// Cutoff check
		if timeStr >= tradeEndTime {
			setupExpired = true
			isArmed = false
			diag.Status = "SETUP_EXPIRED"
			diag.Verdict = "REJECT"
			diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Entry cutoff time %s IST reached", tradeEndTime))
			events = append(events, data.StrategyEvent{
				EventTime:  cTimeIST,
				Symbol:     symbol,
				Strategy:   "FAKE_BREAKOUT",
				Stage:      "SETUP_EXPIRED",
				Direction:  dir,
				CandleTime: &cTimeCopy,
				Reason:     fmt.Sprintf("Entry cutoff time %s IST reached", tradeEndTime),
			})
			diagnostics = append(diagnostics, diag)
			continue
		}

		if dir == "SELL" {
			// Invalidation: Candle High breached Master High
			if c.High > activeMaster.High {
				setupInvalidated = true
				isArmed = false
				diag.Status = "INVALIDATED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f before breakdown", c.High, activeMaster.High))
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "FAKE_BREAKOUT",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "SELL",
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f before breakdown", c.High, activeMaster.High),
				})
				diagnostics = append(diagnostics, diag)
				continue
			}

			// Breakdown Trigger: Low penetrates triggerPrice
			if c.Low <= triggerPrice {
				tradeExecuted = true
				isArmed = false
				slDist := slPrice - triggerPrice
				tgt := triggerPrice - (slDist * 1.5)
				diag.Status = "BREAKDOWN_TRIGGERED"
				diag.Verdict = "PASS"
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Breakdown executed: Low ₹%.2f penetrated trigger ₹%.2f", c.Low, triggerPrice),
					fmt.Sprintf("Sell Entry @ ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
					fmt.Sprintf("Target: ₹%.2f", tgt),
				)
				events = append(events, data.StrategyEvent{
					EventTime:     cTimeIST,
					Symbol:        symbol,
					Strategy:      "FAKE_BREAKOUT",
					Stage:         "TRADE_TAKEN",
					Direction:     "SELL",
					TriggerPrice:  triggerPrice,
					SLPrice:       slPrice,
					ExecutedPrice: triggerPrice,
					CandleTime:    &cTimeCopy,
					Reason:        fmt.Sprintf("Fake Breakout breakdown executed @ ₹%.2f (Target: ₹%.2f)", triggerPrice, tgt),
				})
				diagnostics = append(diagnostics, diag)
				continue
			}

			// Still awaiting breakdown
			diag.Status = "AWAITING_TRIGGER"
			diag.Verdict = "ARMED"
			diag.Details["trigger_price"] = triggerPrice
			diag.Details["sl_price"] = slPrice
			diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Low ₹%.2f held above trigger ₹%.2f; awaiting breakdown", c.Low, triggerPrice))
			diagnostics = append(diagnostics, diag)
			continue
		} else {
			// BUY: Invalidation: Candle Low breached Master Low
			if c.Low < activeMaster.Low {
				setupInvalidated = true
				isArmed = false
				diag.Status = "INVALIDATED"
				diag.Verdict = "REJECT"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle Low ₹%.2f breached Master Low ₹%.2f before breakout", c.Low, activeMaster.Low))
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "FAKE_BREAKOUT",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "BUY",
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("Candle Low ₹%.2f breached Master Low ₹%.2f before breakout", c.Low, activeMaster.Low),
				})
				diagnostics = append(diagnostics, diag)
				continue
			}

			// Breakout Trigger: High penetrates triggerPrice
			if c.High >= triggerPrice {
				tradeExecuted = true
				isArmed = false
				slDist := triggerPrice - slPrice
				tgt := triggerPrice + (slDist * 1.5)
				diag.Status = "BREAKOUT_TRIGGERED"
				diag.Verdict = "PASS"
				diag.PassedCriteria = append(diag.PassedCriteria,
					fmt.Sprintf("Breakout executed: High ₹%.2f penetrated trigger ₹%.2f", c.High, triggerPrice),
					fmt.Sprintf("Buy Entry @ ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
					fmt.Sprintf("Target: ₹%.2f", tgt),
				)
				events = append(events, data.StrategyEvent{
					EventTime:     cTimeIST,
					Symbol:        symbol,
					Strategy:      "FAKE_BREAKOUT",
					Stage:         "TRADE_TAKEN",
					Direction:     "BUY",
					TriggerPrice:  triggerPrice,
					SLPrice:       slPrice,
					ExecutedPrice: triggerPrice,
					CandleTime:    &cTimeCopy,
					Reason:        fmt.Sprintf("Fake Breakout breakout executed @ ₹%.2f (Target: ₹%.2f)", triggerPrice, tgt),
				})
				diagnostics = append(diagnostics, diag)
				continue
			}

			// Still awaiting breakout
			diag.Status = "AWAITING_TRIGGER"
			diag.Verdict = "ARMED"
			diag.Details["trigger_price"] = triggerPrice
			diag.Details["sl_price"] = slPrice
			diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("High ₹%.2f held below trigger ₹%.2f; awaiting breakout", c.High, triggerPrice))
			diagnostics = append(diagnostics, diag)
			continue
		}
	}

	return events, diagnostics
}

// generateInsights produces dynamic post-session analysis and parameter tuning advice
func (a *AuditAnalyzer) generateInsights(symbol, strategy string, summary StockDaySummary, events []data.StrategyEvent, trades []data.TradeHistoryRecord) StockAnalysisInsights {
	insights := StockAnalysisInsights{
		ImprovementSuggestions: make([]string, 0),
		GeometricQuality:       "MODERATE",
	}

	if len(events) == 0 {
		insights.Summary = fmt.Sprintf("No valid setups formed for %s under strategy %s on this date.", symbol, strategy)
		insights.ImprovementSuggestions = append(insights.ImprovementSuggestions, "Ensure stock has sufficient intraday range (> 1.2%) and volume to satisfy Master candle thresholds.")
		insights.GeometricQuality = "N/A"
		return insights
	}

	// Calculate average SL Distance %
	var totalSLDistPct float64
	var slCount int
	for _, ev := range events {
		if ev.TriggerPrice > 0 && ev.SLPrice > 0 {
			distPct := (math.Abs(ev.TriggerPrice-ev.SLPrice) / ev.TriggerPrice) * 100.0
			totalSLDistPct += distPct
			slCount++
		}
	}
	if slCount > 0 {
		insights.SLDistancePct = totalSLDistPct / float64(slCount)
	}

	// Build summary
	var setupEv, takeEv, skipEv, invalEv *data.StrategyEvent
	for i := range events {
		ev := &events[i]
		if ev.Stage == "SETUP_FORMED" {
			setupEv = ev
		} else if ev.Stage == "TRADE_TAKEN" {
			takeEv = ev
		} else if ev.Stage == "TRADE_SKIPPED" {
			skipEv = ev
		} else if ev.Stage == "SETUP_INVALIDATED" {
			invalEv = ev
		}
	}

	if takeEv != nil {
		insights.Summary = fmt.Sprintf("Trade successfully executed at %s for %s @ ₹%.2f. Setup satisfied all confirmation rules.", takeEv.EventTime.Format("15:04 IST"), symbol, takeEv.ExecutedPrice)
		insights.GeometricQuality = "EXCELLENT"
		insights.ImprovementSuggestions = append(insights.ImprovementSuggestions, "Monitor trailing SL progression on multi-stage high water marks.")
	} else if skipEv != nil {
		insights.Summary = fmt.Sprintf("Setup formed and armed, but trade was skipped at %s: %s", skipEv.EventTime.Format("15:04 IST"), skipEv.Reason)
		if strings.Contains(skipEv.Reason, "sizing") || strings.Contains(skipEv.Reason, "Zero quantity") {
			insights.ImprovementSuggestions = append(insights.ImprovementSuggestions, "Stop-Loss distance exceeded the configured risk-per-trade allocation resulting in 0 shares. Adjust max risk per trade or tighten SL anchor.")
		}
	} else if invalEv != nil {
		if setupEv != nil {
			insights.Summary = fmt.Sprintf("Setup was detected at %s but got invalidated at %s: %s", setupEv.EventTime.Format("15:04 IST"), invalEv.EventTime.Format("15:04 IST"), invalEv.Reason)
		} else {
			insights.Summary = fmt.Sprintf("Setup got invalidated at %s: %s", invalEv.EventTime.Format("15:04 IST"), invalEv.Reason)
		}
		insights.GeometricQuality = "POOR"
		if strings.Contains(invalEv.Reason, "breached Master") {
			insights.ImprovementSuggestions = append(insights.ImprovementSuggestions, "Stock experienced strong counter-trend rejection breaking the opposite Master extreme.")
		} else if strings.Contains(invalEv.Reason, "inside candles") {
			insights.ImprovementSuggestions = append(insights.ImprovementSuggestions, "Consolidation lingered too long without directional momentum (> 1 inside candle).")
		}
	} else {
		insights.Summary = fmt.Sprintf("Master setup identified at %s with %d total lifecycle events.", events[0].EventTime.Format("15:04 IST"), len(events))
	}

	if insights.SLDistancePct > 2.0 {
		insights.ImprovementSuggestions = append(insights.ImprovementSuggestions, fmt.Sprintf("Average SL distance was wide (%.2f%%). High SL distance reduces capital efficiency.", insights.SLDistancePct))
	}

	return insights
}
