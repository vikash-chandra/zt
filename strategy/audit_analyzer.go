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
		appliedTimeframe = "1m"
		tradeEndTime = "11:00:00"
		params["master_max_pct"] = 3.0
		params["master_max_wick_pct"] = 75.0
		params["sl_min_pct"] = 0.05
		params["sl_max_pct"] = 2.0
		params["min_gap_pct"] = 2.0
		params["sl_buffer_pct"] = 0.10
	case "VANDE_BHARAT_TRAP":
		appliedTimeframe = "5m"
		tradeEndTime = "11:00:00"
		params["fake_master_max_pct"] = 3.0
		params["genuine_master_max_pct"] = 1.8
		params["genuine_master_max_wick_pct"] = 40.0
		params["sl_min_pct"] = 0.5
		params["sl_max_pct"] = 1.0
	case "LOW_VOLUME":
		appliedTimeframe = "5m"
		tradeEndTime = "14:30:30"
		attachedRR = "PARTIAL_BOOK_COST_SL"
		params["min_candles_to_ignore"] = 2
	case "FAKE_BREAKOUT":
		appliedTimeframe = "1m"
		tradeEndTime = "14:30:30"
		params["gap_up_min_pct"] = 4.0
		params["gap_up_max_pct"] = 8.0
		params["gap_down_min_pct"] = 4.0
		params["gap_down_max_pct"] = 8.0
		params["master_max_wick_pct"] = 40.0
		params["confirm_max_pct"] = 1.0
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
		vbtEvents := a.replayVandeBharatTrap(sym, todayCandles5m, resp.DaySummary, matchingTrades, vbtCfg)
		replayEvents = append(replayEvents, vbtEvents...)
	}

	if strat == "ALL" || strat == "LOW_VOLUME" {
		lvCfg := appliedConfig
		if strat == "ALL" {
			lvCfg = a.loadStrategyConfig(sysConfigs, "LOW_VOLUME", requestedTimeframe...)
		}
		lvEvents, lvDiags := a.replayLowVolume(sym, todayCandles5m, resp.DaySummary, matchingTrades, lvCfg, resp.Events)
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
		} else {
			fbCandles = todayCandles5m
		}
		fbEvents := a.replayFakeBreakout(sym, fbCandles, resp.DaySummary, matchingTrades, fbCfg)
		replayEvents = append(replayEvents, fbEvents...)
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
	resp.DaySummary.FinalStatus = lastStatus

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

	// Instantiate strategy engine to reuse exact U-shape geometry validator
	engine := NewEMAS5BreakoutEngine(a.logger, maxTradesPerStock, rallyCandles, minReboundPct, masterMaxPct, maxInsideCandles, confirmMaxPct)
	engine.SetTradeEndTime(tradeEndTime)
	engine.SetEMATouchBufferPct(emaTouchBufferPct)
	engine.SetMasterMaxWickPct(masterMaxWickPct)
	engine.SetSLBufferPct(slBufferPct)
	engine.SetMaxSetupWaitCandles(maxSetupWaitCandles)
	engine.SetMinPDHPDLRetracePct(minPDHPDLRetracePct)
	engine.SetArcBounceTolerancePct(arcBounceTolerancePct)

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
				activeMaster = nil
				activeConfirm = nil
				confirmCandleIdx = -1
				insideCount = 0
				diagnostics = append(diagnostics, diag)
				continue
			}

			if masterDir == "BUY" {
				// Invalidation: if closed candle breaches Master Low before breakout
				if c.Low < activeMaster.Low {
					reason := fmt.Sprintf("Candle Low ₹%.2f breached Master Low ₹%.2f before breakout", c.Low, activeMaster.Low)
					rejectionReason := fmt.Sprintf("Breached Master Low ₹%.2f (Candle Low: ₹%.2f)", activeMaster.Low, c.Low)
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
					activeMaster = nil
					activeConfirm = nil
					confirmCandleIdx = -1
					insideCount = 0
				} else if c.High > activeConfirm.High {
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
							SLPrice:       activeMaster.Low,
							ExecutedPrice: realTrade.EntryPrice,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Live trade executed: %d shares at ₹%.2f (SL: ₹%.2f, PnL: ₹%.2f)", realTrade.Quantity, realTrade.EntryPrice, realTrade.ExitPrice, realTrade.PnL),
							Details: map[string]interface{}{
								"trigger_price": realTrade.EntryPrice,
								"sl_price":      activeMaster.Low,
								"pnl":           realTrade.PnL,
								"quantity":      realTrade.Quantity,
							},
						})
						diag.Status = "TRADE_TAKEN"
						diag.Verdict = "PASS"
						diag.Details["action"] = "BUY"
						diag.Details["trigger_price"] = activeConfirm.High
						diag.Details["entry_price"] = realTrade.EntryPrice
						diag.Details["sl_price"] = activeMaster.Low
						diag.Details["pnl"] = realTrade.PnL
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Live trade executed: %d shares @ ₹%.2f", realTrade.Quantity, realTrade.EntryPrice),
							fmt.Sprintf("Initial SL placed at Master Low ₹%.2f", activeMaster.Low),
							fmt.Sprintf("Trade PnL: ₹%.2f", realTrade.PnL),
						)
						activeMaster = nil
						activeConfirm = nil
						confirmCandleIdx = -1
						insideCount = 0
						diagnostics = append(diagnostics, diag)
						continue
					}

					// If live telemetry exists and no live trade/order occurred:
					// Do not invent a dummy trade! Setup remains armed awaiting live fill.
					if hasLiveEvents && !liveTrigger {
						diag.Status = "AWAITING_TRIGGER"
						diag.Verdict = "ARMED"
						diag.Details["trigger_price"] = activeConfirm.High
						diag.Details["sl_price"] = activeMaster.Low
						diag.Details["candles_waited"] = candlesWaited
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("High ₹%.2f reached trigger high ₹%.2f, but no live order was placed; setup remained armed", c.High, activeConfirm.High))
						diagnostics = append(diagnostics, diag)
						continue
					}

					// Offline simulation mode: check if trade is possible under current configuration
					slDist := activeConfirm.High - activeMaster.Low
					if tradesCount >= maxTradesPerStock {
						events = append(events, data.StrategyEvent{
							EventTime:     cTimeIST,
							Symbol:        symbol,
							Strategy:      "EMAS5_BREAKOUT",
							Stage:         "TRADE_SKIPPED",
							Direction:     "BUY",
							TriggerPrice:  activeConfirm.High,
							SLPrice:       activeMaster.Low,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Breakout triggered above Confirmation High ₹%.2f (Skipped: max trades per stock %d/%d reached)", activeConfirm.High, tradesCount, maxTradesPerStock),
							Details: map[string]interface{}{
								"trigger_price": activeConfirm.High,
								"sl_price":      activeMaster.Low,
								"max_trades":    maxTradesPerStock,
								"trades_count":  tradesCount,
							},
						})
						diag.Status = "TRADE_SKIPPED"
						diag.Verdict = "REJECTED"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Trade not possible: Max trades per stock reached (%d/%d executed)", tradesCount, maxTradesPerStock))
						diag.Details["trigger_price"] = activeConfirm.High
						diag.Details["sl_price"] = activeMaster.Low
					} else {
						events = append(events, data.StrategyEvent{
							EventTime:     cTimeIST,
							Symbol:        symbol,
							Strategy:      "EMAS5_BREAKOUT",
							Stage:         "TRADE_TAKEN",
							Direction:     "BUY",
							TriggerPrice:  activeConfirm.High,
							SLPrice:       activeMaster.Low,
							ExecutedPrice: activeConfirm.High,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Breakout triggered above Confirmation High ₹%.2f", activeConfirm.High),
							Details: map[string]interface{}{
								"trigger_price": activeConfirm.High,
								"sl_price":      activeMaster.Low,
								"target_1":      activeConfirm.High + (slDist * 1.5),
							},
						})
						tradesCount++
						diag.Status = "BREAKOUT_TRIGGERED"
						diag.Verdict = "PASS"
						diag.Details["trigger_price"] = activeConfirm.High
						diag.Details["sl_price"] = activeMaster.Low
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Breakout triggered: High ₹%.2f broke Confirmation High ₹%.2f", c.High, activeConfirm.High),
							fmt.Sprintf("Buy Entry @ ₹%.2f, SL @ ₹%.2f", activeConfirm.High, activeMaster.Low),
							fmt.Sprintf("Target 1 (1:1.5 RR): ₹%.2f", activeConfirm.High+(slDist*1.5)),
						)
					}
					activeMaster = nil
					activeConfirm = nil
					confirmCandleIdx = -1
					insideCount = 0
					diagnostics = append(diagnostics, diag)
					continue
				} else {
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
				// Invalidation: if closed candle breaches Master High before breakdown
				if c.High > activeMaster.High {
					reason := fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f before breakdown", c.High, activeMaster.High)
					rejectionReason := fmt.Sprintf("Breached Master High ₹%.2f (Candle High: ₹%.2f)", activeMaster.High, c.High)
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
					activeMaster = nil
					activeConfirm = nil
					confirmCandleIdx = -1
					insideCount = 0
				} else if c.Low < activeConfirm.Low {
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
							SLPrice:       activeMaster.High,
							ExecutedPrice: realTrade.EntryPrice,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Live trade executed: %d shares at ₹%.2f (SL: ₹%.2f, PnL: ₹%.2f)", realTrade.Quantity, realTrade.EntryPrice, realTrade.ExitPrice, realTrade.PnL),
							Details: map[string]interface{}{
								"trigger_price": realTrade.EntryPrice,
								"sl_price":      activeMaster.High,
								"pnl":           realTrade.PnL,
								"quantity":      realTrade.Quantity,
							},
						})
						diag.Status = "TRADE_TAKEN"
						diag.Verdict = "PASS"
						diag.Details["action"] = "SELL"
						diag.Details["trigger_price"] = activeConfirm.Low
						diag.Details["entry_price"] = realTrade.EntryPrice
						diag.Details["sl_price"] = activeMaster.High
						diag.Details["pnl"] = realTrade.PnL
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Live trade executed: %d shares @ ₹%.2f", realTrade.Quantity, realTrade.EntryPrice),
							fmt.Sprintf("Initial SL placed at Master High ₹%.2f", activeMaster.High),
							fmt.Sprintf("Trade PnL: ₹%.2f", realTrade.PnL),
						)
						activeMaster = nil
						activeConfirm = nil
						confirmCandleIdx = -1
						insideCount = 0
						diagnostics = append(diagnostics, diag)
						continue
					}

					// If live telemetry exists and no live trade/order occurred:
					// Do not invent a dummy trade! Setup remains armed awaiting live fill.
					if hasLiveEvents && !liveTrigger {
						diag.Status = "AWAITING_TRIGGER"
						diag.Verdict = "ARMED"
						diag.Details["trigger_price"] = activeConfirm.Low
						diag.Details["sl_price"] = activeMaster.High
						diag.Details["candles_waited"] = candlesWaited
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Price reached trigger low ₹%.2f (low ₹%.2f) but no live order was placed; setup remained armed", activeConfirm.Low, c.Low))
						diagnostics = append(diagnostics, diag)
						continue
					}

					// Offline simulation mode: check if trade is possible under current configuration
					slDist := activeMaster.High - activeConfirm.Low
					if tradesCount >= maxTradesPerStock {
						events = append(events, data.StrategyEvent{
							EventTime:     cTimeIST,
							Symbol:        symbol,
							Strategy:      "EMAS5_BREAKOUT",
							Stage:         "TRADE_SKIPPED",
							Direction:     "SELL",
							TriggerPrice:  activeConfirm.Low,
							SLPrice:       activeMaster.High,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Breakdown triggered below Confirmation Low ₹%.2f (Skipped: max trades per stock %d/%d reached)", activeConfirm.Low, tradesCount, maxTradesPerStock),
							Details: map[string]interface{}{
								"trigger_price": activeConfirm.Low,
								"sl_price":      activeMaster.High,
								"max_trades":    maxTradesPerStock,
								"trades_count":  tradesCount,
							},
						})
						diag.Status = "TRADE_SKIPPED"
						diag.Verdict = "REJECTED"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Trade not possible: Max trades per stock reached (%d/%d executed)", tradesCount, maxTradesPerStock))
						diag.Details["trigger_price"] = activeConfirm.Low
						diag.Details["sl_price"] = activeMaster.High
					} else {
						events = append(events, data.StrategyEvent{
							EventTime:     cTimeIST,
							Symbol:        symbol,
							Strategy:      "EMAS5_BREAKOUT",
							Stage:         "TRADE_TAKEN",
							Direction:     "SELL",
							TriggerPrice:  activeConfirm.Low,
							SLPrice:       activeMaster.High,
							ExecutedPrice: activeConfirm.Low,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Breakdown triggered below Confirmation Low ₹%.2f", activeConfirm.Low),
							Details: map[string]interface{}{
								"trigger_price": activeConfirm.Low,
								"sl_price":      activeMaster.High,
								"target_1":      activeConfirm.Low - (slDist * 1.5),
							},
						})
						tradesCount++
						diag.Status = "BREAKDOWN_TRIGGERED"
						diag.Verdict = "PASS"
						diag.Details["trigger_price"] = activeConfirm.Low
						diag.Details["sl_price"] = activeMaster.High
						diag.PassedCriteria = append(diag.PassedCriteria,
							fmt.Sprintf("Breakdown triggered: Low ₹%.2f broke Confirmation Low ₹%.2f", c.Low, activeConfirm.Low),
							fmt.Sprintf("Sell Entry @ ₹%.2f, SL @ ₹%.2f", activeConfirm.Low, activeMaster.High),
							fmt.Sprintf("Target 1 (1:1.5 RR): ₹%.2f", activeConfirm.Low-(slDist*1.5)),
						)
					}
					activeMaster = nil
					activeConfirm = nil
					confirmCandleIdx = -1
					insideCount = 0
					diagnostics = append(diagnostics, diag)
					continue
				} else {
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
				if c.Low < activeMaster.Low {
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "BUY",
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Candle Low ₹%.2f breached Master Low ₹%.2f (Opposite breach)", c.Low, activeMaster.Low),
						Details: map[string]interface{}{
							"candle_low": c.Low,
							"master_low": activeMaster.Low,
						},
					})
					diag.Status = "INVALIDATED"
					diag.Verdict = "REJECTED"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Breached Master Low ₹%.2f (Low: ₹%.2f)", activeMaster.Low, c.Low))
					activeMaster = nil
				} else if c.High > activeMaster.High {
					if c.Close <= activeMaster.Low || c.Close <= c.Open {
						events = append(events, data.StrategyEvent{
							EventTime:  cTimeIST,
							Symbol:     symbol,
							Strategy:   "EMAS5_BREAKOUT",
							Stage:      "SETUP_INVALIDATED",
							Direction:  "BUY",
							CandleTime: &cTimeCopy,
							Reason:     "Confirmation candidate broke Master High but failed to close GREEN (Bull trap rejection)",
						})
						diag.Status = "INVALIDATED"
						diag.Verdict = "REJECTED"
						diag.RejectionReasons = append(diag.RejectionReasons, "Broke Master High but failed to close GREEN above Master Low (Rejection)")
						activeMaster = nil
						diagnostics = append(diagnostics, diag)
						continue
					}
					if rangePct > confirmMaxPct {
						events = append(events, data.StrategyEvent{
							EventTime:  cTimeIST,
							Symbol:     symbol,
							Strategy:   "EMAS5_BREAKOUT",
							Stage:      "SETUP_INVALIDATED",
							Direction:  "BUY",
							CandleTime: &cTimeCopy,
							Reason:     fmt.Sprintf("Confirmation candidate range %.2f%% exceeded max allowed %.2f%%", rangePct, confirmMaxPct),
						})
						diag.Status = "INVALIDATED"
						diag.Verdict = "REJECTED"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Confirmation range %.2f%% exceeded max allowed %.2f%%", rangePct, confirmMaxPct))
						activeMaster = nil
						diagnostics = append(diagnostics, diag)
						continue
					}

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
						Reason:       fmt.Sprintf("Confirmation Candle armed: Buy above ₹%.2f, SL @ ₹%.2f", c.High, activeMaster.Low),
						Details: map[string]interface{}{
							"range_pct": rangePct,
							"ema10":     e10,
							"ema20":     e20,
						},
					})
					diag.Status = "CONFIRMATION_ARMED"
					diag.Verdict = "PASS"
					diag.Details["trigger_price"] = c.High
					diag.Details["sl_price"] = activeMaster.Low
					diag.PassedCriteria = append(diag.PassedCriteria,
						fmt.Sprintf("Bullish GREEN candle (Close ₹%.2f > Open ₹%.2f)", c.Close, c.Open),
						fmt.Sprintf("High ₹%.2f broke Master High ₹%.2f", c.High, activeMaster.High),
						fmt.Sprintf("Low ₹%.2f held above Master Low ₹%.2f", c.Low, activeMaster.Low),
						fmt.Sprintf("Range %.2f%% <= max %.2f%%", rangePct, confirmMaxPct),
						fmt.Sprintf("Armed BUY trigger above ₹%.2f, SL @ ₹%.2f", c.High, activeMaster.Low),
					)
					diagnostics = append(diagnostics, diag)
					continue
				} else {
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
				}

			} else if masterDir == "SELL" {
				if c.High > activeMaster.High {
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "SELL",
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f (Opposite breach)", c.High, activeMaster.High),
						Details: map[string]interface{}{
							"candle_high": c.High,
							"master_high": activeMaster.High,
						},
					})
					diag.Status = "INVALIDATED"
					diag.Verdict = "REJECTED"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Breached Master High ₹%.2f (High: ₹%.2f)", activeMaster.High, c.High))
					activeMaster = nil
				} else if c.Low < activeMaster.Low {
					if c.Close >= activeMaster.High || c.Close >= c.Open {
						events = append(events, data.StrategyEvent{
							EventTime:  cTimeIST,
							Symbol:     symbol,
							Strategy:   "EMAS5_BREAKOUT",
							Stage:      "SETUP_INVALIDATED",
							Direction:  "SELL",
							CandleTime: &cTimeCopy,
							Reason:     "Confirmation candidate broke Master Low but failed to close RED (Bear trap rejection)",
						})
						diag.Status = "INVALIDATED"
						diag.Verdict = "REJECTED"
						diag.RejectionReasons = append(diag.RejectionReasons, "Broke Master Low but failed to close RED below Master High (Rejection)")
						activeMaster = nil
						diagnostics = append(diagnostics, diag)
						continue
					}
					if rangePct > confirmMaxPct {
						events = append(events, data.StrategyEvent{
							EventTime:  cTimeIST,
							Symbol:     symbol,
							Strategy:   "EMAS5_BREAKOUT",
							Stage:      "SETUP_INVALIDATED",
							Direction:  "SELL",
							CandleTime: &cTimeCopy,
							Reason:     fmt.Sprintf("Confirmation candidate range %.2f%% exceeded max allowed %.2f%%", rangePct, confirmMaxPct),
						})
						diag.Status = "INVALIDATED"
						diag.Verdict = "REJECTED"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Confirmation range %.2f%% exceeded max allowed %.2f%%", rangePct, confirmMaxPct))
						activeMaster = nil
						diagnostics = append(diagnostics, diag)
						continue
					}

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
						Reason:       fmt.Sprintf("Confirmation Candle armed: Sell below ₹%.2f, SL @ ₹%.2f", c.Low, activeMaster.High),
						Details: map[string]interface{}{
							"range_pct": rangePct,
							"ema10":     e10,
							"ema20":     e20,
						},
					})
					diag.Status = "CONFIRMATION_ARMED"
					diag.Verdict = "PASS"
					diag.Details["trigger_price"] = c.Low
					diag.Details["sl_price"] = activeMaster.High
					diag.PassedCriteria = append(diag.PassedCriteria,
						fmt.Sprintf("Bearish RED candle (Close ₹%.2f < Open ₹%.2f)", c.Close, c.Open),
						fmt.Sprintf("Low ₹%.2f broke Master Low ₹%.2f", c.Low, activeMaster.Low),
						fmt.Sprintf("High ₹%.2f held below Master High ₹%.2f", c.High, activeMaster.High),
						fmt.Sprintf("Range %.2f%% <= max %.2f%%", rangePct, confirmMaxPct),
						fmt.Sprintf("Armed SELL trigger below ₹%.2f, SL @ ₹%.2f", c.Low, activeMaster.High),
					)
					diagnostics = append(diagnostics, diag)
					continue
				} else {
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
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("09:20 Candle 2 failed SL Range threshold (%.2f%% not between %.2f%% and %.2f%%)", cRangePct, slMinPct, slMaxPct))
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
func (a *AuditAnalyzer) replayVandeBharatTrap(symbol string, today5m []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord, appCfg ...AppliedStrategyConfig) []data.StrategyEvent {
	events := make([]data.StrategyEvent, 0)
	if len(today5m) < 3 {
		return events
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

	// 1. Fake Master Candle (09:15 AM IST)
	c1 := today5m[0]
	c1TimeIST := data.NormalizeToIST(c1.Time)
	c1TimeCopy := c1TimeIST
	c1Range := c1.High - c1.Low
	if c1Range <= 0 || c1.Close <= 0 {
		return events
	}
	c1RangePct := (c1Range / c1.Close) * 100.0

	// BUY Fake Master: Closes > PDH, but body is RED (Close < Open)
	isFakeMasterBuy := c1.Close > pdh && c1.Close < c1.Open && pdh > 0
	// SELL Fake Master: Closes < PDL, but body is GREEN (Close > Open)
	isFakeMasterSell := c1.Close < pdl && c1.Close > c1.Open && pdl > 0

	if !isFakeMasterBuy && !isFakeMasterSell {
		return events
	}

	if c1RangePct > fakeMasterMaxPct {
		events = append(events, data.StrategyEvent{
			EventTime:  c1TimeIST,
			Symbol:     symbol,
			Strategy:   "VANDE_BHARAT_TRAP",
			Stage:      "SETUP_INVALIDATED",
			CandleTime: &c1TimeCopy,
			Reason:     fmt.Sprintf("09:15 Candle failed Fake Master criteria (Range: %.2f%% > %.2f%%)", c1RangePct, fakeMasterMaxPct),
		})
		return events
	}

	trapDir := "BUY"
	if isFakeMasterSell {
		trapDir = "SELL"
	}

	events = append(events, data.StrategyEvent{
		EventTime:    c1TimeIST,
		Symbol:       symbol,
		Strategy:     "VANDE_BHARAT_TRAP",
		Stage:        "SETUP_FORMED",
		Direction:    trapDir,
		TriggerPrice: c1.High,
		SLPrice:      c1.Low,
		CandleTime:   &c1TimeCopy,
		CandleOpen:   c1.Open,
		CandleHigh:   c1.High,
		CandleLow:    c1.Low,
		CandleClose:  c1.Close,
		CandleVolume: c1.Volume,
		Reason:       fmt.Sprintf("Fake Master Candle Formed (09:15 AM %s Trap: Opposite body color outside PDH/PDL)", trapDir),
		Details: map[string]interface{}{
			"pdh":       pdh,
			"pdl":       pdl,
			"range_pct": c1RangePct,
		},
	})

	var genuineMaster *data.Candle
	var genuineMasterIdx int

	// 2. Scan for Genuine Master Formation
	for i := 1; i < len(today5m); i++ {
		c := today5m[i]
		cTimeIST := data.NormalizeToIST(c.Time)
		cTimeCopy := cTimeIST

		if cTimeIST.Format("15:04:05") >= tradeEndTime {
			break
		}

		cRange := c.High - c.Low
		if cRange <= 0 || c.Close <= 0 {
			continue
		}
		bodySize := math.Abs(c.Close - c.Open)
		wickSize := cRange - bodySize
		rangePct := (cRange / c.Close) * 100.0
		wickPct := (wickSize / cRange) * 100.0

		if trapDir == "BUY" {
			if c.High > c1.High || c.Close > c1.High {
				if rangePct <= genuineMasterMaxPct && wickPct <= genuineMasterMaxWickPct {
					cCopy := c
					genuineMaster = &cCopy
					genuineMasterIdx = i
					events = append(events, data.StrategyEvent{
						EventTime:    cTimeIST,
						Symbol:       symbol,
						Strategy:     "VANDE_BHARAT_TRAP",
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
						Reason:       fmt.Sprintf("Genuine Master Candle Formed (Breached Fake Master High ₹%.2f, Range: %.2f%% <= %.2f%%)", c1.High, rangePct, genuineMasterMaxPct),
						Details: map[string]interface{}{
							"range_pct": rangePct,
							"wick_pct":  wickPct,
						},
					})
					break
				} else {
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "VANDE_BHARAT_TRAP",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "BUY",
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Candle breached Fake Master High but failed Master criteria (Range: %.2f%% > %.2f%% or Wick: %.2f%% > %.2f%%)", rangePct, genuineMasterMaxPct, wickPct, genuineMasterMaxWickPct),
					})
					return events
				}
			}
		} else {
			if c.Low < c1.Low || c.Close < c1.Low {
				if rangePct <= genuineMasterMaxPct && wickPct <= genuineMasterMaxWickPct {
					cCopy := c
					genuineMaster = &cCopy
					genuineMasterIdx = i
					events = append(events, data.StrategyEvent{
						EventTime:    cTimeIST,
						Symbol:       symbol,
						Strategy:     "VANDE_BHARAT_TRAP",
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
						Reason:       fmt.Sprintf("Genuine Master Candle Formed (Breached Fake Master Low ₹%.2f, Range: %.2f%% <= %.2f%%)", c1.Low, rangePct, genuineMasterMaxPct),
						Details: map[string]interface{}{
							"range_pct": rangePct,
							"wick_pct":  wickPct,
						},
					})
					break
				} else {
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "VANDE_BHARAT_TRAP",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "SELL",
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Candle breached Fake Master Low but failed Master criteria (Range: %.2f%% > %.2f%% or Wick: %.2f%% > %.2f%%)", rangePct, genuineMasterMaxPct, wickPct, genuineMasterMaxWickPct),
					})
					return events
				}
			}
		}
	}

	if genuineMaster == nil || genuineMasterIdx+1 >= len(today5m) {
		return events
	}

	// 3. 2nd Candle (SL Anchor) immediately following Genuine Master
	secondCandle := today5m[genuineMasterIdx+1]
	secondTimeIST := data.NormalizeToIST(secondCandle.Time)
	secondTimeCopy := secondTimeIST
	secondRange := secondCandle.High - secondCandle.Low
	if secondRange <= 0 || secondCandle.Close <= 0 {
		return events
	}
	secondRangePct := (secondRange / secondCandle.Close) * 100.0

	// Invalidation: Opposite breach on Candle 2
	if trapDir == "BUY" && secondCandle.Low < genuineMaster.Low {
		events = append(events, data.StrategyEvent{
			EventTime:  secondTimeIST,
			Symbol:     symbol,
			Strategy:   "VANDE_BHARAT_TRAP",
			Stage:      "SETUP_INVALIDATED",
			Direction:  "BUY",
			CandleTime: &secondTimeCopy,
			Reason:     fmt.Sprintf("2nd Candle Low ₹%.2f breached Master Low ₹%.2f", secondCandle.Low, genuineMaster.Low),
		})
		return events
	} else if trapDir == "SELL" && secondCandle.High > genuineMaster.High {
		events = append(events, data.StrategyEvent{
			EventTime:  secondTimeIST,
			Symbol:     symbol,
			Strategy:   "VANDE_BHARAT_TRAP",
			Stage:      "SETUP_INVALIDATED",
			Direction:  "SELL",
			CandleTime: &secondTimeCopy,
			Reason:     fmt.Sprintf("2nd Candle High ₹%.2f breached Master High ₹%.2f", secondCandle.High, genuineMaster.High),
		})
		return events
	}

	if secondRangePct < slMinPct || secondRangePct > slMaxPct {
		events = append(events, data.StrategyEvent{
			EventTime:  secondTimeIST,
			Symbol:     symbol,
			Strategy:   "VANDE_BHARAT_TRAP",
			Stage:      "SETUP_INVALIDATED",
			Direction:  trapDir,
			CandleTime: &secondTimeCopy,
			Reason:     fmt.Sprintf("2nd Candle failed SL range criteria (%.2f%% not between %.2f%% and %.2f%%)", secondRangePct, slMinPct, slMaxPct),
		})
		return events
	}

	var triggerPrice, slPrice float64
	if trapDir == "BUY" {
		slPrice = secondCandle.Low
		if secondCandle.High > genuineMaster.High {
			if secondCandle.Close <= genuineMaster.Low || secondCandle.Close <= secondCandle.Open {
				events = append(events, data.StrategyEvent{
					EventTime:  secondTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT_TRAP",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "BUY",
					CandleTime: &secondTimeCopy,
					Reason:     fmt.Sprintf("2nd Candle broke Master High but closed RED/DOJI (Shooting Star Rejection: Open ₹%.2f, Close ₹%.2f)", secondCandle.Open, secondCandle.Close),
				})
				return events
			}
			triggerPrice = secondCandle.High
		} else {
			triggerPrice = genuineMaster.High
		}
	} else {
		slPrice = secondCandle.High
		if secondCandle.Low < genuineMaster.Low {
			if secondCandle.Close >= genuineMaster.High || secondCandle.Close >= secondCandle.Open {
				events = append(events, data.StrategyEvent{
					EventTime:  secondTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT_TRAP",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "SELL",
					CandleTime: &secondTimeCopy,
					Reason:     fmt.Sprintf("2nd Candle broke Master Low but closed GREEN/DOJI (Hammer Rejection: Open ₹%.2f, Close ₹%.2f)", secondCandle.Open, secondCandle.Close),
				})
				return events
			}
			triggerPrice = secondCandle.Low
		} else {
			triggerPrice = genuineMaster.Low
		}
	}

	events = append(events, data.StrategyEvent{
		EventTime:    secondTimeIST,
		Symbol:       symbol,
		Strategy:     "VANDE_BHARAT_TRAP",
		Stage:        "CONFIRMATION_ARMED",
		Direction:    trapDir,
		TriggerPrice: triggerPrice,
		SLPrice:      slPrice,
		CandleTime:   &secondTimeCopy,
		CandleOpen:   secondCandle.Open,
		CandleHigh:   secondCandle.High,
		CandleLow:    secondCandle.Low,
		CandleClose:  secondCandle.Close,
		CandleVolume: secondCandle.Volume,
		Reason:       fmt.Sprintf("Trap Setup Armed: Trigger @ ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
		Details: map[string]interface{}{
			"sl_range_pct": secondRangePct,
			"sl_price":     slPrice,
		},
	})

	// 4. Subsequent Candles: Armed Waiting & Execution
	for i := genuineMasterIdx + 2; i < len(today5m); i++ {
		c := today5m[i]
		cTimeIST := data.NormalizeToIST(c.Time)
		cTimeCopy := cTimeIST
		timeStr := cTimeIST.Format("15:04:05")

		if timeStr >= tradeEndTime {
			events = append(events, data.StrategyEvent{
				EventTime:  cTimeIST,
				Symbol:     symbol,
				Strategy:   "VANDE_BHARAT_TRAP",
				Stage:      "SETUP_EXPIRED",
				Direction:  trapDir,
				CandleTime: &cTimeCopy,
				Reason:     fmt.Sprintf("Entry cutoff time %s IST reached without trade execution", tradeEndTime),
			})
			break
		}

		if trapDir == "BUY" {
			if c.Low < genuineMaster.Low {
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT_TRAP",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "BUY",
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("Candle Low ₹%.2f breached Master Low ₹%.2f before breakout", c.Low, genuineMaster.Low),
				})
				break
			}

			if c.High >= triggerPrice {
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
					Reason:        fmt.Sprintf("Trap breakout trade executed @ ₹%.2f (Target 1: ₹%.2f)", triggerPrice, triggerPrice+(math.Abs(triggerPrice-slPrice)*1.5)),
				})
				break
			}
		} else {
			if c.High > genuineMaster.High {
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT_TRAP",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "SELL",
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f before breakdown", c.High, genuineMaster.High),
				})
				break
			}

			if c.Low <= triggerPrice {
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
					Reason:        fmt.Sprintf("Trap breakdown trade executed @ ₹%.2f (Target 1: ₹%.2f)", triggerPrice, triggerPrice-(math.Abs(triggerPrice-slPrice)*1.5)),
				})
				break
			}
		}
	}

	return events
}

// replayLowVolume simulates Low Volume Scalp strategy
// replayLowVolume simulates Low Volume Scalp strategy
func (a *AuditAnalyzer) replayLowVolume(symbol string, today5m []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord, appCfg AppliedStrategyConfig, liveEvents ...[]data.StrategyEvent) ([]data.StrategyEvent, []CandleDiagnosticItem) {
	events := make([]data.StrategyEvent, 0)
	diagnostics := make([]CandleDiagnosticItem, 0, len(today5m))
	if len(today5m) < 2 {
		return events, diagnostics
	}

	tradeEndTime := "10:00:00"
	if appCfg.TradeEndTime != "" {
		tradeEndTime = data.NormalizeTimeHHMMSS(appCfg.TradeEndTime)
	}

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
func (a *AuditAnalyzer) replayFakeBreakout(symbol string, todayCandles []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord, appCfg ...AppliedStrategyConfig) []data.StrategyEvent {
	events := make([]data.StrategyEvent, 0)
	if len(todayCandles) < 2 {
		return events
	}

	gapUpMinPct := 0.50
	gapDownMinPct := 0.50
	tradeEndTime := "14:30:30"

	if len(appCfg) > 0 {
		if appCfg[0].TradeEndTime != "" {
			tradeEndTime = data.NormalizeTimeHHMMSS(appCfg[0].TradeEndTime)
		}
		p := appCfg[0].Parameters
		gapUpMinPct = getFloatParam(p, gapUpMinPct, "gap_up_min_pct", "gapup_min_pct")
		gapDownMinPct = getFloatParam(p, gapDownMinPct, "gap_down_min_pct", "gapdown_min_pct")
	}

	c1 := todayCandles[0]
	c1TimeIST := data.NormalizeToIST(c1.Time)
	c1TimeCopy := c1TimeIST
	if c1TimeIST.Hour() != 9 || c1TimeIST.Minute() != 15 {
		return events
	}

	pdClose := summary.PDClose
	if pdClose <= 0 {
		pdClose = c1.Open
	}

	gapUpPct := ((c1.Open - pdClose) / pdClose) * 100.0
	gapDownPct := ((pdClose - c1.Open) / pdClose) * 100.0

	var dir string
	if c1.Close < c1.Open && gapUpPct >= gapUpMinPct {
		dir = "SELL"
	} else if c1.Close > c1.Open && gapDownPct >= gapDownMinPct {
		dir = "BUY"
	} else {
		return events
	}

	events = append(events, data.StrategyEvent{
		EventTime:    c1TimeIST,
		Symbol:       symbol,
		Strategy:     "FAKE_BREAKOUT",
		Stage:        "SETUP_FORMED",
		Direction:    dir,
		CandleTime:   &c1TimeCopy,
		CandleOpen:   c1.Open,
		CandleHigh:   c1.High,
		CandleLow:    c1.Low,
		CandleClose:  c1.Close,
		CandleVolume: c1.Volume,
		Reason:       fmt.Sprintf("Fake Breakout 09:15 Master formed: %s (Gap: %.2f%%)", dir, math.Max(gapUpPct, gapDownPct)),
	})

	// Candle 2 (09:20): Confirmation
	c2 := todayCandles[1]
	c2TimeIST := data.NormalizeToIST(c2.Time)
	c2TimeCopy := c2TimeIST

	var triggerPrice, slPrice float64
	if dir == "SELL" {
		if c2.Close >= c2.Open || c2.Low >= c1.Low {
			events = append(events, data.StrategyEvent{
				EventTime:  c2TimeIST,
				Symbol:     symbol,
				Strategy:   "FAKE_BREAKOUT",
				Stage:      "SETUP_INVALIDATED",
				Direction:  "SELL",
				CandleTime: &c2TimeCopy,
				Reason:     "Candle 2 failed SELL confirmation: did not break Master Low as a RED candle",
			})
			return events
		}
		triggerPrice = c2.Low
		slPrice = c2.High
	} else {
		if c2.Close <= c2.Open || c2.High <= c1.High {
			events = append(events, data.StrategyEvent{
				EventTime:  c2TimeIST,
				Symbol:     symbol,
				Strategy:   "FAKE_BREAKOUT",
				Stage:      "SETUP_INVALIDATED",
				Direction:  "BUY",
				CandleTime: &c2TimeCopy,
				Reason:     "Candle 2 failed BUY confirmation: did not break Master High as a GREEN candle",
			})
			return events
		}
		triggerPrice = c2.High
		slPrice = c2.Low
	}

	events = append(events, data.StrategyEvent{
		EventTime:    c2TimeIST,
		Symbol:       symbol,
		Strategy:     "FAKE_BREAKOUT",
		Stage:        "CONFIRMATION_ARMED",
		Direction:    dir,
		TriggerPrice: triggerPrice,
		SLPrice:      slPrice,
		CandleTime:   &c2TimeCopy,
		CandleOpen:   c2.Open,
		CandleHigh:   c2.High,
		CandleLow:    c2.Low,
		CandleClose:  c2.Close,
		CandleVolume: c2.Volume,
		Reason:       fmt.Sprintf("Fake Breakout Armed: Trigger @ ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
	})

	// Subsequent candles (09:25 AM onward)
	for i := 2; i < len(todayCandles); i++ {
		c := todayCandles[i]
		cTimeIST := data.NormalizeToIST(c.Time)
		cTimeCopy := cTimeIST
		timeStr := cTimeIST.Format("15:04:05")

		if timeStr >= tradeEndTime {
			events = append(events, data.StrategyEvent{
				EventTime:  cTimeIST,
				Symbol:     symbol,
				Strategy:   "FAKE_BREAKOUT",
				Stage:      "SETUP_EXPIRED",
				Direction:  dir,
				CandleTime: &cTimeCopy,
				Reason:     fmt.Sprintf("Entry cutoff time %s IST reached", tradeEndTime),
			})
			break
		}

		if dir == "SELL" {
			if c.High > c1.High {
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "FAKE_BREAKOUT",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "SELL",
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f before breakdown", c.High, c1.High),
				})
				break
			}
			if c.Low <= triggerPrice {
				slDist := slPrice - triggerPrice
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
					Reason:        fmt.Sprintf("Fake Breakout breakdown executed @ ₹%.2f (Target: ₹%.2f)", triggerPrice, triggerPrice-(slDist*1.5)),
				})
				break
			}
		} else {
			if c.Low < c1.Low {
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "FAKE_BREAKOUT",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "BUY",
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("Candle Low ₹%.2f breached Master Low ₹%.2f before breakout", c.Low, c1.Low),
				})
				break
			}
			if c.High >= triggerPrice {
				slDist := triggerPrice - slPrice
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
					Reason:        fmt.Sprintf("Fake Breakout breakout executed @ ₹%.2f (Target: ₹%.2f)", triggerPrice, triggerPrice+(slDist*1.5)),
				})
				break
			}
		}
	}

	return events
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
