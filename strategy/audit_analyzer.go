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

// AuditStock audits a stock for a specific date and strategy, returning a full diagnostic response
func (a *AuditAnalyzer) AuditStock(ctx context.Context, symbol, dateStr, strategyFilter string) (*StockAuditResponse, error) {
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

	// 2. Load Dynamic Strategy Parameters from app_system_configs
	var sysConfigs map[string]map[string]string
	if a.db != nil {
		sysConfigs, _ = a.db.GetAllSystemConfigs(ctx)
	}

	// Strategy defaults
	appliedTimeframe := "5m"
	tradeEndTime := "14:30:30"
	rallyCandles := 6
	minReboundPct := 0.40
	masterMaxPct := 1.00
	masterMaxWickPct := 80.0
	maxInsideCandles := 3
	confirmMaxPct := 0.75
	emaTouchBufferPct := 0.01
	slBufferPct := 0.10
	maxEntryDistancePct := 0.35
	maxSetupWaitCandles := 6
	useBrokerSL := true
	attachedRR := "PARTIAL_BOOK_COST_SL"

	if sysConfigs != nil {
		tStratMap := sysConfigs["TRADING_STRATEGY"]
		eqStratMap := sysConfigs["EQUITY_STRATEGY"]

		targetKey := strat
		if targetKey == "ALL" {
			targetKey = "EMAS5_BREAKOUT"
		}

		if tStratMap != nil {
			if rawVal, ok := tStratMap[targetKey]; ok && strings.HasPrefix(strings.TrimSpace(rawVal), "{") {
				var parsed struct {
					CandleTimeFrame     string  `json:"candle_time_frame"`
					AttachedRiskReward  string  `json:"attached_risk_reward"`
					TradeEndTime        string  `json:"trade_end_time"`
					SLBufferPct         float64 `json:"sl_buffer_pct"`
					UseBrokerSL         bool    `json:"use_broker_sl"`
					MasterMaxPct        float64 `json:"master_max_pct"`
					MasterMaxWickPct    float64 `json:"master_max_wick_pct"`
					ConfirmMaxPct       float64 `json:"confirm_max_pct"`
					RallyCandles        int     `json:"rally_candles"`
					MinReboundPct       float64 `json:"min_rebound_pct"`
					MaxInsideCandles    int     `json:"max_inside_candles"`
					EMATouchBufferPct   float64 `json:"ema_touch_buffer_pct"`
					MaxEntryDistancePct float64 `json:"max_entry_distance_pct"`
					MaxSetupWaitCandles int     `json:"max_setup_wait_candles"`
				}
				if err := json.Unmarshal([]byte(rawVal), &parsed); err == nil {
					if parsed.CandleTimeFrame != "" {
						appliedTimeframe = parsed.CandleTimeFrame
					}
					if parsed.TradeEndTime != "" {
						tradeEndTime = parsed.TradeEndTime
					}
					if parsed.RallyCandles > 0 {
						rallyCandles = parsed.RallyCandles
					}
					if parsed.MinReboundPct > 0 {
						minReboundPct = parsed.MinReboundPct
					}
					if parsed.MasterMaxPct > 0 {
						masterMaxPct = parsed.MasterMaxPct
					}
					if parsed.MasterMaxWickPct > 0 {
						masterMaxWickPct = parsed.MasterMaxWickPct
					}
					if parsed.MaxInsideCandles > 0 {
						maxInsideCandles = parsed.MaxInsideCandles
					}
					if parsed.ConfirmMaxPct > 0 {
						confirmMaxPct = parsed.ConfirmMaxPct
					}
					if parsed.EMATouchBufferPct > 0 {
						emaTouchBufferPct = parsed.EMATouchBufferPct
					}
					if parsed.SLBufferPct > 0 {
						slBufferPct = parsed.SLBufferPct
					}
					if parsed.MaxEntryDistancePct > 0 {
						maxEntryDistancePct = parsed.MaxEntryDistancePct
					}
					if parsed.MaxSetupWaitCandles > 0 {
						maxSetupWaitCandles = parsed.MaxSetupWaitCandles
					}
					if parsed.AttachedRiskReward != "" {
						attachedRR = parsed.AttachedRiskReward
					}
					useBrokerSL = parsed.UseBrokerSL
				}
			}
		}

		if eqStratMap != nil {
			if v, ok := eqStratMap["es5_candle_timeframe"]; ok && v != "" {
				appliedTimeframe = v
			}
			if v, ok := eqStratMap["es5_trade_end_time"]; ok && v != "" {
				tradeEndTime = v
			}
			if v, err := strconv.Atoi(eqStratMap["es5_rally_candles_count"]); err == nil && v > 0 {
				rallyCandles = v
			}
			if v, err := strconv.ParseFloat(eqStratMap["es5_min_rebound_pct"], 64); err == nil && v > 0 {
				minReboundPct = v
			}
			if v, err := strconv.ParseFloat(eqStratMap["es5_master_max_pct"], 64); err == nil && v > 0 {
				masterMaxPct = v
			}
			if v, err := strconv.ParseFloat(eqStratMap["es5_master_max_wick_pct"], 64); err == nil && v > 0 {
				masterMaxWickPct = v
			}
			if v, err := strconv.Atoi(eqStratMap["es5_max_inside_candles"]); err == nil && v > 0 {
				maxInsideCandles = v
			}
			if v, err := strconv.ParseFloat(eqStratMap["es5_confirm_max_pct"], 64); err == nil && v > 0 {
				confirmMaxPct = v
			}
			if v, err := strconv.ParseFloat(eqStratMap["es5_ema_touch_buffer_pct"], 64); err == nil && v > 0 {
				emaTouchBufferPct = v
			}
			if v, err := strconv.ParseFloat(eqStratMap["es5_sl_buffer_pct"], 64); err == nil && v > 0 {
				slBufferPct = v
			}
		}
	}

	appliedConfig := AppliedStrategyConfig{
		StrategyName:    strat,
		CandleTimeframe: appliedTimeframe,
		TradeEndTime:    tradeEndTime,
		UseBrokerSL:     useBrokerSL,
		AttachedRR:      attachedRR,
		Parameters: map[string]interface{}{
			"rally_candles":          rallyCandles,
			"min_rebound_pct":        minReboundPct,
			"master_max_pct":         masterMaxPct,
			"master_max_wick_pct":    masterMaxWickPct,
			"max_inside_candles":     maxInsideCandles,
			"confirm_max_pct":        confirmMaxPct,
			"ema_touch_buffer_pct":   emaTouchBufferPct,
			"trade_end_time":         tradeEndTime,
			"sl_buffer_pct":          slBufferPct,
			"max_entry_distance_pct": maxEntryDistancePct,
			"max_setup_wait_candles": maxSetupWaitCandles,
		},
	}

	resp.ConfiguredTimeframe = appliedTimeframe
	resp.AppliedConfig = appliedConfig
	resp.CandleDiagnostics = make([]CandleDiagnosticItem, 0)

	// 3. Selectively query candles based on configured timeframe (eliminates redundant DB query load)
	var candles1m []data.Candle
	var candles5m []data.Candle
	if token > 0 {
		if appliedTimeframe == "1m" {
			candles1m, _ = a.db.GetCandlesWithHistory(ctx, token, dateStr, "1m", 150)
			if len(candles1m) == 0 {
				candles5m, _ = a.db.GetCandlesWithHistory(ctx, token, dateStr, "5m", 150)
			} else if strat == "ALL" {
				candles5m, _ = a.db.GetCandlesWithHistory(ctx, token, dateStr, "5m", 150)
			}
		} else {
			candles5m, _ = a.db.GetCandlesWithHistory(ctx, token, dateStr, "5m", 150)
			if len(candles5m) == 0 {
				candles1m, _ = a.db.GetCandlesWithHistory(ctx, token, dateStr, "1m", 150)
			}
		}
	}

	// Filter today's candles in IST
	var todayCandles1m []data.Candle
	var prevDayCandles1m []data.Candle
	for _, c := range candles1m {
		cTimeIST := data.NormalizeToIST(c.Time)
		cDateStr := cTimeIST.Format("2006-01-02")
		if cDateStr == dateStr {
			todayCandles1m = append(todayCandles1m, c)
		} else if cDateStr < dateStr {
			prevDayCandles1m = append(prevDayCandles1m, c)
		}
	}

	var todayCandles5m []data.Candle
	var prevDayCandles5m []data.Candle
	for _, c := range candles5m {
		cTimeIST := data.NormalizeToIST(c.Time)
		cDateStr := cTimeIST.Format("2006-01-02")
		if cDateStr == dateStr {
			todayCandles5m = append(todayCandles5m, c)
		} else if cDateStr < dateStr {
			prevDayCandles5m = append(prevDayCandles5m, c)
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

	// Calculate PDH / PDL / PDClose
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

	// Select candles according to configured timeframe
	var es5AllCandles []data.Candle
	var es5TodayCandles []data.Candle
	if appliedTimeframe == "5m" {
		es5AllCandles = candles5m
		es5TodayCandles = todayCandles5m
	} else {
		es5AllCandles = candles1m
		es5TodayCandles = todayCandles1m
	}

	// 4. If stored events were not recorded or more detail is needed, run deterministic replay simulation
	replayEvents := make([]data.StrategyEvent, 0)
	if strat == "ALL" || strat == "EMAS5_BREAKOUT" {
		es5Events, es5Diags := a.replayEMAS5(sym, es5AllCandles, es5TodayCandles, resp.DaySummary, matchingTrades, appliedConfig)
		replayEvents = append(replayEvents, es5Events...)
		resp.CandleDiagnostics = es5Diags
	}

	if strat == "ALL" || strat == "VANDE_BHARAT" {
		vbEvents := a.replayVandeBharat(sym, todayCandles5m, resp.DaySummary, matchingTrades)
		replayEvents = append(replayEvents, vbEvents...)
	}

	if strat == "ALL" || strat == "VANDE_BHARAT_TRAP" {
		vbtEvents := a.replayVandeBharatTrap(sym, todayCandles5m, resp.DaySummary, matchingTrades)
		replayEvents = append(replayEvents, vbtEvents...)
	}

	if strat == "ALL" || strat == "LOW_VOLUME" {
		lvEvents := a.replayLowVolume(sym, todayCandles5m, resp.DaySummary, matchingTrades)
		replayEvents = append(replayEvents, lvEvents...)
	}

	if strat == "ALL" || strat == "FAKE_BREAKOUT" {
		fbEvents := a.replayFakeBreakout(sym, todayCandles5m, resp.DaySummary, matchingTrades)
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
func (a *AuditAnalyzer) replayEMAS5(symbol string, allCandles, todayCandles []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord, appCfg AppliedStrategyConfig) ([]data.StrategyEvent, []CandleDiagnosticItem) {
	events := make([]data.StrategyEvent, 0, 16)
	diagnostics := make([]CandleDiagnosticItem, 0, len(todayCandles))
	if len(allCandles) < 10 {
		return events, diagnostics
	}

	// Dynamic Parameter extraction
	rallyCandles := 6
	if v, ok := appCfg.Parameters["rally_candles"].(int); ok && v > 0 {
		rallyCandles = v
	} else if v, ok := appCfg.Parameters["rally_candles"].(float64); ok && v > 0 {
		rallyCandles = int(v)
	}

	minReboundPct := 0.40
	if v, ok := appCfg.Parameters["min_rebound_pct"].(float64); ok && v > 0 {
		minReboundPct = v
	}

	masterMaxPct := 1.00
	if v, ok := appCfg.Parameters["master_max_pct"].(float64); ok && v > 0 {
		masterMaxPct = v
	}

	masterMaxWickPct := 80.0
	if v, ok := appCfg.Parameters["master_max_wick_pct"].(float64); ok && v > 0 {
		masterMaxWickPct = v
	}

	maxInsideCandles := 3
	if v, ok := appCfg.Parameters["max_inside_candles"].(int); ok && v >= 0 {
		maxInsideCandles = v
	} else if v, ok := appCfg.Parameters["max_inside_candles"].(float64); ok && v >= 0 {
		maxInsideCandles = int(v)
	}

	confirmMaxPct := 0.75
	if v, ok := appCfg.Parameters["confirm_max_pct"].(float64); ok && v > 0 {
		confirmMaxPct = v
	}

	emaTouchBufferPct := 0.01
	if v, ok := appCfg.Parameters["ema_touch_buffer_pct"].(float64); ok && v >= 0 {
		emaTouchBufferPct = v
	}

	tradeEndTime := "14:30:30"
	if appCfg.TradeEndTime != "" {
		tradeEndTime = data.NormalizeTimeHHMMSS(appCfg.TradeEndTime)
	}

	slBufferPct := 0.10
	if v, ok := appCfg.Parameters["sl_buffer_pct"].(float64); ok && v >= 0 {
		slBufferPct = v
	}

	// Instantiate strategy engine to reuse exact U-shape geometry validator
	engine := NewEMAS5BreakoutEngine(a.logger, 2, rallyCandles, minReboundPct, masterMaxPct, maxInsideCandles, confirmMaxPct)
	engine.SetTradeEndTime(tradeEndTime)
	engine.SetEMATouchBufferPct(emaTouchBufferPct)
	engine.SetMasterMaxWickPct(masterMaxWickPct)
	engine.SetSLBufferPct(slBufferPct)

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
	tradeTakenToday := false

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

		// 2. Trade already filled check
		if tradeTakenToday {
			diag.Status = "TRADE_ALREADY_TAKEN"
			diag.Verdict = "INFO"
			diag.RejectionReasons = append(diag.RejectionReasons, "Max trades for today reached (trade already executed)")
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
				diagnostics = append(diagnostics, diag)
				continue
			}

			if masterDir == "BUY" {
				if c.Low < activeMaster.Low {
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "BUY",
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Candle Low ₹%.2f breached Master Low ₹%.2f before breakout", c.Low, activeMaster.Low),
						Details: map[string]interface{}{
							"candle_low": c.Low,
							"master_low": activeMaster.Low,
						},
					})
					diag.Status = "INVALIDATED"
					diag.Verdict = "REJECTED"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Breached Master Low ₹%.2f (Candle Low: ₹%.2f)", activeMaster.Low, c.Low))
					activeMaster = nil
					activeConfirm = nil
					diagnostics = append(diagnostics, diag)
					continue
				}

				if c.High > activeConfirm.High {
					runawayPct := 0.0
					if summary.PDL > 0 {
						runawayPct = ((activeConfirm.High - summary.PDL) / summary.PDL) * 100.0
					}
					slDist := activeConfirm.High - activeMaster.Low
					if runawayPct > 3.0 {
						events = append(events, data.StrategyEvent{
							EventTime:    cTimeIST,
							Symbol:       symbol,
							Strategy:     "EMAS5_BREAKOUT",
							Stage:        "TRADE_SKIPPED",
							Direction:    "BUY",
							TriggerPrice: activeConfirm.High,
							SLPrice:      activeMaster.Low,
							CandleTime:   &cTimeCopy,
							Reason:       fmt.Sprintf("Runaway filter blocked entry: Price moved +%.2f%% from PDL exceeding 3.00%% threshold", runawayPct),
							Details: map[string]interface{}{
								"runaway_pct": runawayPct,
								"sl_dist":     slDist,
							},
						})
						diag.Status = "TRADE_SKIPPED"
						diag.Verdict = "REJECTED"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Runaway filter blocked entry: Move +%.2f%% from PDL > 3.00%%", runawayPct))
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
						tradeTakenToday = true
						diag.Status = "BREAKOUT_TRIGGERED"
						diag.Verdict = "PASS"
						diag.Details["trigger_price"] = activeConfirm.High
						diag.Details["sl_price"] = activeMaster.Low
					}
					activeMaster = nil
					activeConfirm = nil
					diagnostics = append(diagnostics, diag)
					continue
				}

				// Still armed and waiting
				diag.Status = "CONFIRMATION_ARMED"
				diag.Verdict = "PASS"
				diag.Details["trigger_price"] = activeConfirm.High
				diag.Details["sl_price"] = activeMaster.Low
				diagnostics = append(diagnostics, diag)
				continue

			} else if masterDir == "SELL" {
				if c.High > activeMaster.High {
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "SELL",
						CandleTime: &cTimeCopy,
						Reason:     fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f before breakdown", c.High, activeMaster.High),
						Details: map[string]interface{}{
							"candle_high": c.High,
							"master_high": activeMaster.High,
						},
					})
					diag.Status = "INVALIDATED"
					diag.Verdict = "REJECTED"
					diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Breached Master High ₹%.2f (Candle High: ₹%.2f)", activeMaster.High, c.High))
					activeMaster = nil
					activeConfirm = nil
					diagnostics = append(diagnostics, diag)
					continue
				}

				if c.Low < activeConfirm.Low {
					runawayPct := 0.0
					if summary.PDH > 0 {
						runawayPct = ((summary.PDH - activeConfirm.Low) / summary.PDH) * 100.0
					}
					slDist := activeMaster.High - activeConfirm.Low
					if runawayPct > 3.0 {
						events = append(events, data.StrategyEvent{
							EventTime:    cTimeIST,
							Symbol:       symbol,
							Strategy:     "EMAS5_BREAKOUT",
							Stage:        "TRADE_SKIPPED",
							Direction:    "SELL",
							TriggerPrice: activeConfirm.Low,
							SLPrice:      activeMaster.High,
							CandleTime:   &cTimeCopy,
							Reason:       fmt.Sprintf("Runaway filter blocked entry: Price dropped -%.2f%% from PDH exceeding 3.00%% threshold", runawayPct),
							Details: map[string]interface{}{
								"runaway_pct": runawayPct,
								"sl_dist":     slDist,
							},
						})
						diag.Status = "TRADE_SKIPPED"
						diag.Verdict = "REJECTED"
						diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Runaway filter blocked entry: Drop -%.2f%% from PDH > 3.00%%", runawayPct))
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
						tradeTakenToday = true
						diag.Status = "BREAKDOWN_TRIGGERED"
						diag.Verdict = "PASS"
						diag.Details["trigger_price"] = activeConfirm.Low
						diag.Details["sl_price"] = activeMaster.High
					}
					activeMaster = nil
					activeConfirm = nil
					diagnostics = append(diagnostics, diag)
					continue
				}

				// Still armed and waiting
				diag.Status = "CONFIRMATION_ARMED"
				diag.Verdict = "PASS"
				diag.Details["trigger_price"] = activeConfirm.Low
				diag.Details["sl_price"] = activeMaster.High
				diagnostics = append(diagnostics, diag)
				continue
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
					diagnostics = append(diagnostics, diag)
					continue
				}

				if c.High > activeMaster.High {
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
					diagnostics = append(diagnostics, diag)
					continue
				}

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
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Consolidation inside candle %d of %d allowed", insideCount, maxInsideCandles))
				diagnostics = append(diagnostics, diag)
				continue

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
					diagnostics = append(diagnostics, diag)
					continue
				}

				if c.Low < activeMaster.Low {
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
					diagnostics = append(diagnostics, diag)
					continue
				}

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
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Consolidation inside candle %d of %d allowed", insideCount, maxInsideCandles))
				diagnostics = append(diagnostics, diag)
				continue
			}
		}

		// 5. State: Scanning for New Master Candle Formation
		if activeMaster == nil && !tradeTakenToday {
			if timeStr >= tradeEndTime {
				diag.Status = "PAST_CUTOFF"
				diag.Verdict = "INFO"
				diag.RejectionReasons = append(diag.RejectionReasons, fmt.Sprintf("Candle time %s is past trade cutoff %s IST", timeStr, tradeEndTime))
				diagnostics = append(diagnostics, diag)
				continue
			}

			candlesToday := idx - todayStartIdx + 1
			if candlesToday < rallyCandles+1 {
				diag.Status = "WARMUP"
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
					isValid, lowestLow, candlesSinceLowest, reboundPct := engine.validateBuyUShape(allCandles, idx)
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
							Reason:       fmt.Sprintf("Master Candle Formed (BUY U-Shape, Rebound: +%.2f%%, Range: %.2f%%)", reboundPct, rangePct),
							Details: map[string]interface{}{
								"rally_candles":        rallyCandles,
								"lowest_low":           lowestLow,
								"candles_since_lowest": candlesSinceLowest,
								"rebound_pct":          reboundPct,
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
						diag.Details["candles_since_lowest"] = candlesSinceLowest
						diagnostics = append(diagnostics, diag)
						continue
					} else {
						if candlesSinceLowest < rallyCandles {
							reasons = append(reasons, fmt.Sprintf("U-Shape failed: Lowest Low ₹%.2f formed %d candles ago (< %d required)", lowestLow, candlesSinceLowest, rallyCandles))
						} else if reboundPct < minReboundPct {
							reasons = append(reasons, fmt.Sprintf("U-Shape failed: Rebound +%.2f%% < %.2f%% threshold", reboundPct, minReboundPct))
						} else {
							reasons = append(reasons, "U-Shape arc failed: broken arc or V-spike detected")
						}
					}
				}

				diag.Status = "REJECTED"
				diag.Verdict = "REJECTED"
				diag.RejectionReasons = reasons
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
					isValid, highestHigh, candlesSinceHighest, dropPct := engine.validateSellInvertedUShape(allCandles, idx)
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
							Reason:       fmt.Sprintf("Master Candle Formed (SELL Inverted U-Shape, Drop: -%.2f%%, Range: %.2f%%)", dropPct, rangePct),
							Details: map[string]interface{}{
								"rally_candles":          rallyCandles,
								"highest_high":           highestHigh,
								"candles_since_highest": candlesSinceHighest,
								"drop_pct":               dropPct,
								"range_pct":              rangePct,
								"wick_pct":               wickPct,
								"ema10":                  e10,
								"ema20":                  e20,
							},
						})
						diag.Status = "MASTER_ESTABLISHED"
						diag.Verdict = "PASS"
						diag.Details["highest_high"] = highestHigh
						diag.Details["drop_pct"] = dropPct
						diag.Details["candles_since_highest"] = candlesSinceHighest
						diagnostics = append(diagnostics, diag)
						continue
					} else {
						if candlesSinceHighest < rallyCandles {
							reasons = append(reasons, fmt.Sprintf("Inverted U-Shape failed: Highest High ₹%.2f formed %d candles ago (< %d required)", highestHigh, candlesSinceHighest, rallyCandles))
						} else if dropPct < minReboundPct {
							reasons = append(reasons, fmt.Sprintf("Inverted U-Shape failed: Drop -%.2f%% < %.2f%% threshold", dropPct, minReboundPct))
						} else {
							reasons = append(reasons, "Inverted U-Shape arc failed: broken arc or V-spike detected")
						}
					}
				}

				diag.Status = "REJECTED"
				diag.Verdict = "REJECTED"
				diag.RejectionReasons = reasons
				diagnostics = append(diagnostics, diag)
				continue
			} else {
				// DOJI
				diag.Status = "REJECTED"
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

// replayVandeBharat simulates the Vande Bharat Momentum Strategy across 5m candles
func (a *AuditAnalyzer) replayVandeBharat(symbol string, today5m []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord) []data.StrategyEvent {
	events := make([]data.StrategyEvent, 0)
	if len(today5m) < 2 {
		return events
	}

	pdh := summary.PDH
	pdl := summary.PDL
	pdClose := summary.PDClose

	// Candle 1 (09:15 AM)
	c1 := today5m[0]
	c1TimeIST := data.NormalizeToIST(c1.Time)
	c1TimeCopy := c1TimeIST
	c1Range := c1.High - c1.Low
	if c1Range <= 0 || c1.Close <= 0 {
		return events
	}
	c1RangePct := (c1Range / c1.Close) * 100.0
	c1Wick := (c1.High - math.Max(c1.Open, c1.Close)) + (math.Min(c1.Open, c1.Close) - c1.Low)
	c1WickPct := (c1Wick / c1Range) * 100.0

	isMasterBuy := c1.Close > pdh && pdh > 0
	isMasterSell := c1.Close < pdl && pdl > 0

	if !isMasterBuy && !isMasterSell {
		return events
	}

	if c1RangePct > 3.0 || c1WickPct > 60.0 {
		events = append(events, data.StrategyEvent{
			EventTime:  c1TimeIST,
			Symbol:     symbol,
			Strategy:   "VANDE_BHARAT",
			Stage:      "SETUP_INVALIDATED",
			CandleTime: &c1TimeCopy,
			Reason:     fmt.Sprintf("09:15 Candle failed Master criteria (Range: %.2f%% > 3.00%% or Wick: %.2f%% > 60.0%%)", c1RangePct, c1WickPct),
		})
		return events
	}

	dir := "BUY"
	if isMasterSell {
		dir = "SELL"
	}

	events = append(events, data.StrategyEvent{
		EventTime:    c1TimeIST,
		Symbol:       symbol,
		Strategy:     "VANDE_BHARAT",
		Stage:        "SETUP_FORMED",
		Direction:    dir,
		TriggerPrice: c1.High,
		SLPrice:      c1.Low,
		CandleTime:   &c1TimeCopy,
		CandleOpen:   c1.Open,
		CandleHigh:   c1.High,
		CandleLow:    c1.Low,
		CandleClose:  c1.Close,
		CandleVolume: c1.Volume,
		Reason:       fmt.Sprintf("Master Candle Formed (09:15 AM, %s Breakout from PDH/PDL)", dir),
		Details: map[string]interface{}{
			"pdh":       pdh,
			"pdl":       pdl,
			"pd_close":  pdClose,
			"range_pct": c1RangePct,
			"wick_pct":  c1WickPct,
		},
	})

	// Candle 2 (09:20 AM) - SL Anchor
	c2 := today5m[1]
	c2TimeIST := data.NormalizeToIST(c2.Time)
	c2TimeCopy := c2TimeIST
	if c2.Close <= 0 {
		return events
	}
	c2RangePct := ((c2.High - c2.Low) / c2.Close) * 100.0

	if c2RangePct < 0.05 || c2RangePct > 1.0 {
		events = append(events, data.StrategyEvent{
			EventTime:  c2TimeIST,
			Symbol:     symbol,
			Strategy:   "VANDE_BHARAT",
			Stage:      "SETUP_INVALIDATED",
			Direction:  dir,
			CandleTime: &c2TimeCopy,
			Reason:     fmt.Sprintf("09:20 Candle 2 failed SL Range threshold (%.2f%% not between 0.05%% and 1.00%%)", c2RangePct),
		})
		return events
	}

	var triggerPrice, slPrice float64
	if dir == "BUY" {
		if c2.Low < c1.Low {
			events = append(events, data.StrategyEvent{
				EventTime:  c2TimeIST,
				Symbol:     symbol,
				Strategy:   "VANDE_BHARAT",
				Stage:      "SETUP_INVALIDATED",
				Direction:  "BUY",
				CandleTime: &c2TimeCopy,
				Reason:     fmt.Sprintf("Candle 2 Low ₹%.2f breached Master Low ₹%.2f", c2.Low, c1.Low),
			})
			return events
		}
		slPrice = c2.Low
		if c2.High > c1.High {
			if c2.Close <= c1.Low || c2.Close <= c2.Open {
				events = append(events, data.StrategyEvent{
					EventTime:  c2TimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "BUY",
					CandleTime: &c2TimeCopy,
					Reason:     fmt.Sprintf("Candle 2 broke Master High but closed RED/DOJI (Shooting Star Rejection: Open ₹%.2f, Close ₹%.2f)", c2.Open, c2.Close),
				})
				return events
			}
			triggerPrice = c2.High
		} else {
			triggerPrice = c1.High
		}
	} else {
		if c2.High > c1.High {
			events = append(events, data.StrategyEvent{
				EventTime:  c2TimeIST,
				Symbol:     symbol,
				Strategy:   "VANDE_BHARAT",
				Stage:      "SETUP_INVALIDATED",
				Direction:  "SELL",
				CandleTime: &c2TimeCopy,
				Reason:     fmt.Sprintf("Candle 2 High ₹%.2f breached Master High ₹%.2f", c2.High, c1.High),
			})
			return events
		}
		slPrice = c2.High
		if c2.Low < c1.Low {
			if c2.Close >= c1.High || c2.Close >= c2.Open {
				events = append(events, data.StrategyEvent{
					EventTime:  c2TimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "SELL",
					CandleTime: &c2TimeCopy,
					Reason:     fmt.Sprintf("Candle 2 broke Master Low but closed GREEN/DOJI (Hammer Rejection: Open ₹%.2f, Close ₹%.2f)", c2.Open, c2.Close),
				})
				return events
			}
			triggerPrice = c2.Low
		} else {
			triggerPrice = c1.Low
		}
	}

	events = append(events, data.StrategyEvent{
		EventTime:    c2TimeIST,
		Symbol:       symbol,
		Strategy:     "VANDE_BHARAT",
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
		Reason:       fmt.Sprintf("Setup Armed @ 09:20 AM: Trigger @ ₹%.2f, SL @ ₹%.2f", triggerPrice, slPrice),
		Details: map[string]interface{}{
			"sl_range_pct": c2RangePct,
			"sl_price":     slPrice,
		},
	})

	// Subsequent candles (09:25 AM onwards)
	for i := 2; i < len(today5m); i++ {
		c := today5m[i]
		cTimeIST := data.NormalizeToIST(c.Time)
		cTimeCopy := cTimeIST
		timeStr := cTimeIST.Format("15:04:05")

		if timeStr >= "11:00:00" {
			events = append(events, data.StrategyEvent{
				EventTime:  cTimeIST,
				Symbol:     symbol,
				Strategy:   "VANDE_BHARAT",
				Stage:      "SETUP_EXPIRED",
				Direction:  dir,
				CandleTime: &cTimeCopy,
				Reason:     "Entry cutoff time 11:00:00 IST reached without trade execution",
			})
			break
		}

		if dir == "BUY" {
			if c.Low < c1.Low {
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "BUY",
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("Candle Low ₹%.2f breached Master Low ₹%.2f before breakout", c.Low, c1.Low),
				})
				break
			}

			if c.High >= triggerPrice {
				runawayPct := 0.0
				if pdl > 0 {
					runawayPct = ((triggerPrice - pdl) / pdl) * 100.0
				}
				if runawayPct > 3.0 {
					events = append(events, data.StrategyEvent{
						EventTime:     cTimeIST,
						Symbol:        symbol,
						Strategy:      "VANDE_BHARAT",
						Stage:         "TRADE_SKIPPED",
						Direction:     "BUY",
						TriggerPrice:  triggerPrice,
						SLPrice:       slPrice,
						CandleTime:    &cTimeCopy,
						Reason:        fmt.Sprintf("Runaway filter blocked entry: Price moved +%.2f%% from PDL exceeding 3.00%% threshold", runawayPct),
						Details: map[string]interface{}{
							"runaway_pct": runawayPct,
						},
					})
				} else {
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
						Reason:        fmt.Sprintf("Breakout trade executed @ ₹%.2f (Target 1: ₹%.2f)", triggerPrice, triggerPrice+(math.Abs(triggerPrice-slPrice)*1.5)),
					})
				}
				break
			}
		} else {
			if c.High > c1.High {
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "VANDE_BHARAT",
					Stage:      "SETUP_INVALIDATED",
					Direction:  "SELL",
					CandleTime: &cTimeCopy,
					Reason:     fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f before breakdown", c.High, c1.High),
				})
				break
			}

			if c.Low <= triggerPrice {
				runawayPct := 0.0
				if pdh > 0 {
					runawayPct = ((pdh - triggerPrice) / pdh) * 100.0
				}
				if runawayPct > 3.0 {
					events = append(events, data.StrategyEvent{
						EventTime:     cTimeIST,
						Symbol:        symbol,
						Strategy:      "VANDE_BHARAT",
						Stage:         "TRADE_SKIPPED",
						Direction:     "SELL",
						TriggerPrice:  triggerPrice,
						SLPrice:       slPrice,
						CandleTime:    &cTimeCopy,
						Reason:        fmt.Sprintf("Runaway filter blocked entry: Price dropped -%.2f%% from PDH exceeding 3.00%% threshold", runawayPct),
						Details: map[string]interface{}{
							"runaway_pct": runawayPct,
						},
					})
				} else {
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
						Reason:        fmt.Sprintf("Breakdown trade executed @ ₹%.2f (Target 1: ₹%.2f)", triggerPrice, triggerPrice-(math.Abs(triggerPrice-slPrice)*1.5)),
					})
				}
				break
			}
		}
	}

	return events
}

// replayVandeBharatTrap simulates the Vande Bharat Trap Strategy across 5m candles
func (a *AuditAnalyzer) replayVandeBharatTrap(symbol string, today5m []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord) []data.StrategyEvent {
	events := make([]data.StrategyEvent, 0)
	if len(today5m) < 3 {
		return events
	}
	pdh := summary.PDH
	pdl := summary.PDL

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

	if c1RangePct > 3.0 {
		events = append(events, data.StrategyEvent{
			EventTime:  c1TimeIST,
			Symbol:     symbol,
			Strategy:   "VANDE_BHARAT_TRAP",
			Stage:      "SETUP_INVALIDATED",
			CandleTime: &c1TimeCopy,
			Reason:     fmt.Sprintf("09:15 Candle failed Fake Master criteria (Range: %.2f%% > 3.00%%)", c1RangePct),
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

		if cTimeIST.Format("15:04:05") >= "11:00:00" {
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
				if rangePct <= 1.8 && wickPct <= 40.0 {
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
						Reason:       fmt.Sprintf("Genuine Master Candle Formed (Breached Fake Master High ₹%.2f, Range: %.2f%% <= 1.8%%)", c1.High, rangePct),
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
						Reason:     fmt.Sprintf("Candle breached Fake Master High but failed Master criteria (Range: %.2f%% > 1.80%% or Wick: %.2f%% > 40.0%%)", rangePct, wickPct),
					})
					return events
				}
			}
		} else {
			if c.Low < c1.Low || c.Close < c1.Low {
				if rangePct <= 1.8 && wickPct <= 40.0 {
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
						Reason:       fmt.Sprintf("Genuine Master Candle Formed (Breached Fake Master Low ₹%.2f, Range: %.2f%% <= 1.8%%)", c1.Low, rangePct),
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
						Reason:     fmt.Sprintf("Candle breached Fake Master Low but failed Master criteria (Range: %.2f%% > 1.80%% or Wick: %.2f%% > 40.0%%)", rangePct, wickPct),
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

	if secondRangePct < 0.5 || secondRangePct > 1.0 {
		events = append(events, data.StrategyEvent{
			EventTime:  secondTimeIST,
			Symbol:     symbol,
			Strategy:   "VANDE_BHARAT_TRAP",
			Stage:      "SETUP_INVALIDATED",
			Direction:  trapDir,
			CandleTime: &secondTimeCopy,
			Reason:     fmt.Sprintf("2nd Candle failed SL range criteria (%.2f%% not between 0.50%% and 1.00%%)", secondRangePct),
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

		if timeStr >= "11:00:00" {
			events = append(events, data.StrategyEvent{
				EventTime:  cTimeIST,
				Symbol:     symbol,
				Strategy:   "VANDE_BHARAT_TRAP",
				Stage:      "SETUP_EXPIRED",
				Direction:  trapDir,
				CandleTime: &cTimeCopy,
				Reason:     "Entry cutoff time 11:00:00 IST reached without trade execution",
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
				runawayPct := 0.0
				if pdh > 0 {
					runawayPct = ((triggerPrice - pdh) / pdh) * 100.0
				}
				if runawayPct > 1.8 {
					events = append(events, data.StrategyEvent{
						EventTime:     cTimeIST,
						Symbol:        symbol,
						Strategy:      "VANDE_BHARAT_TRAP",
						Stage:         "TRADE_SKIPPED",
						Direction:     "BUY",
						TriggerPrice:  triggerPrice,
						SLPrice:       slPrice,
						CandleTime:    &cTimeCopy,
						Reason:        fmt.Sprintf("Runaway filter blocked entry: Price moved +%.2f%% from PDH exceeding 1.80%% threshold", runawayPct),
						Details: map[string]interface{}{
							"runaway_pct": runawayPct,
						},
					})
				} else {
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
				}
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
				runawayPct := 0.0
				if pdl > 0 {
					runawayPct = ((pdl - triggerPrice) / pdl) * 100.0
				}
				if runawayPct > 1.8 {
					events = append(events, data.StrategyEvent{
						EventTime:     cTimeIST,
						Symbol:        symbol,
						Strategy:      "VANDE_BHARAT_TRAP",
						Stage:         "TRADE_SKIPPED",
						Direction:     "SELL",
						TriggerPrice:  triggerPrice,
						SLPrice:       slPrice,
						CandleTime:    &cTimeCopy,
						Reason:        fmt.Sprintf("Runaway filter blocked entry: Price dropped -%.2f%% from PDL exceeding 1.80%% threshold", runawayPct),
						Details: map[string]interface{}{
							"runaway_pct": runawayPct,
						},
					})
				} else {
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
				}
				break
			}
		}
	}

	return events
}


// replayLowVolume simulates Low Volume Scalp strategy
func (a *AuditAnalyzer) replayLowVolume(symbol string, today5m []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord) []data.StrategyEvent {
	events := make([]data.StrategyEvent, 0)
	if len(today5m) < 3 {
		return events
	}
	return events
}

// replayFakeBreakout simulates Fake Breakout strategy
func (a *AuditAnalyzer) replayFakeBreakout(symbol string, today5m []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord) []data.StrategyEvent {
	events := make([]data.StrategyEvent, 0)
	if len(today5m) < 2 {
		return events
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
		if strings.Contains(skipEv.Reason, "Runaway") {
			insights.ImprovementSuggestions = append(insights.ImprovementSuggestions, "Price had already expanded > 3.0% from PDH/PDL before triggering. Consider entering earlier or loosening max runaway threshold for high-beta stocks.")
		}
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
