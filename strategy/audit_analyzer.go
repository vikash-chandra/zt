package strategy

import (
	"context"
	"fmt"
	"math"
	"sort"
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

	var candles1m []data.Candle
	var candles5m []data.Candle
	if token > 0 {
		candles1m, _ = a.db.GetCandlesWithHistory(ctx, token, dateStr, "1m", 150)
		candles5m, _ = a.db.GetCandlesWithHistory(ctx, token, dateStr, "5m", 150)
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
	if len(todayCandles1m) > 0 {
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
	} else if len(todayCandles5m) > 0 {
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
	}

	// Calculate PDH / PDL / PDClose
	if len(prevDayCandles1m) > 0 {
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
		es5Events := a.replayEMAS5(sym, candles1m, todayCandles1m, resp.DaySummary, matchingTrades)
		replayEvents = append(replayEvents, es5Events...)
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

// replayEMAS5 simulates the EMA S5 Breakout Strategy across 1m candles
func (a *AuditAnalyzer) replayEMAS5(symbol string, all1m, today1m []data.Candle, summary StockDaySummary, trades []data.TradeHistoryRecord) []data.StrategyEvent {
	events := make([]data.StrategyEvent, 0)
	if len(all1m) < 10 {
		return events
	}

	// Extract closes and compute EMA 10 and EMA 20
	closes := make([]float64, len(all1m))
	for i, c := range all1m {
		closes[i] = c.Close
	}

	ema10Values := a.indicators.CalculateEMA(closes, 10)
	ema20Values := a.indicators.CalculateEMA(closes, 20)

	// Map times to indices in all1m
	timeToIndex := make(map[string]int)
	for i, c := range all1m {
		timeToIndex[c.Time.Format("2006-01-02 15:04:05")] = i
	}

	var activeMaster *data.Candle
	var masterDir string
	var insideCount int
	var activeConfirm *data.Candle
	tradeTakenToday := false

	for _, c := range today1m {
		cTimeIST := data.NormalizeToIST(c.Time)
		timeStr := cTimeIST.Format("15:04:05")
		key := c.Time.Format("2006-01-02 15:04:05")
		idx, ok := timeToIndex[key]
		if !ok || idx < 20 {
			continue
		}

		e10 := ema10Values[idx]
		e20 := ema20Values[idx]
		cTimeCopy := cTimeIST

		// Check active confirmation awaiting trigger
		if activeMaster != nil && activeConfirm != nil {
			// Check Cutoff Time 11:00:00 IST
			if timeStr >= "11:00:00" {
				events = append(events, data.StrategyEvent{
					EventTime:  cTimeIST,
					Symbol:     symbol,
					Strategy:   "EMAS5_BREAKOUT",
					Stage:      "SETUP_EXPIRED",
					Direction:  masterDir,
					CandleTime: &cTimeCopy,
					Reason:     "Entry cutoff time 11:00:00 IST reached without trigger fill",
					Details: map[string]interface{}{
						"cutoff_time": "11:00:00",
					},
				})
				activeMaster = nil
				activeConfirm = nil
				continue
			}

			// Invalidation check during armed wait
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
					activeMaster = nil
					activeConfirm = nil
					continue
				}

				// Breakout Trigger Check: c.High > activeConfirm.High
				if c.High > activeConfirm.High {
					// Check if runaway filter or sizing would skip
					runawayPct := 0.0
					if summary.PDL > 0 {
						runawayPct = ((activeConfirm.High - summary.PDL) / summary.PDL) * 100.0
					}
					slDist := activeConfirm.High - activeMaster.Low
					if runawayPct > 3.0 {
						events = append(events, data.StrategyEvent{
							EventTime:     cTimeIST,
							Symbol:        symbol,
							Strategy:      "EMAS5_BREAKOUT",
							Stage:         "TRADE_SKIPPED",
							Direction:     "BUY",
							TriggerPrice:  activeConfirm.High,
							SLPrice:       activeMaster.Low,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Runaway filter blocked entry: Price moved +%.2f%% from PDL exceeding 3.00%% threshold", runawayPct),
							Details: map[string]interface{}{
								"runaway_pct": runawayPct,
								"sl_dist":     slDist,
							},
						})
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
					}
					activeMaster = nil
					activeConfirm = nil
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
						Reason:     fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f before breakdown", c.High, activeMaster.High),
						Details: map[string]interface{}{
							"candle_high": c.High,
							"master_high": activeMaster.High,
						},
					})
					activeMaster = nil
					activeConfirm = nil
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
							EventTime:     cTimeIST,
							Symbol:        symbol,
							Strategy:      "EMAS5_BREAKOUT",
							Stage:         "TRADE_SKIPPED",
							Direction:     "SELL",
							TriggerPrice:  activeConfirm.Low,
							SLPrice:       activeMaster.High,
							CandleTime:    &cTimeCopy,
							Reason:        fmt.Sprintf("Runaway filter blocked entry: Price dropped -%.2f%% from PDH exceeding 3.00%% threshold", runawayPct),
							Details: map[string]interface{}{
								"runaway_pct": runawayPct,
								"sl_dist":     slDist,
							},
						})
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
					}
					activeMaster = nil
					activeConfirm = nil
					continue
				}
			}
		}

		// Check master awaiting confirmation
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
					activeMaster = nil
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
						activeMaster = nil
						continue
					}
					cRangePct := (c.High - c.Low) / c.Close * 100.0
					if cRangePct > 1.0 {
						events = append(events, data.StrategyEvent{
							EventTime:  cTimeIST,
							Symbol:     symbol,
							Strategy:   "EMAS5_BREAKOUT",
							Stage:      "SETUP_INVALIDATED",
							Direction:  "BUY",
							CandleTime: &cTimeCopy,
							Reason:     fmt.Sprintf("Confirmation candidate range %.2f%% exceeded max allowed 1.00%%", cRangePct),
						})
						activeMaster = nil
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
							"range_pct": cRangePct,
							"ema10":     e10,
							"ema20":     e20,
						},
					})
					continue
				}

				insideCount++
				if insideCount > 1 {
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "BUY",
						CandleTime: &cTimeCopy,
						Reason:     "Exceeded maximum inside candles consolidation limit (2 inside candles)",
					})
					activeMaster = nil
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
					activeMaster = nil
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
						activeMaster = nil
						continue
					}
					cRangePct := (c.High - c.Low) / c.Close * 100.0
					if cRangePct > 1.0 {
						events = append(events, data.StrategyEvent{
							EventTime:  cTimeIST,
							Symbol:     symbol,
							Strategy:   "EMAS5_BREAKOUT",
							Stage:      "SETUP_INVALIDATED",
							Direction:  "SELL",
							CandleTime: &cTimeCopy,
							Reason:     fmt.Sprintf("Confirmation candidate range %.2f%% exceeded max allowed 1.00%%", cRangePct),
						})
						activeMaster = nil
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
							"range_pct": cRangePct,
							"ema10":     e10,
							"ema20":     e20,
						},
					})
					continue
				}

				insideCount++
				if insideCount > 1 {
					events = append(events, data.StrategyEvent{
						EventTime:  cTimeIST,
						Symbol:     symbol,
						Strategy:   "EMAS5_BREAKOUT",
						Stage:      "SETUP_INVALIDATED",
						Direction:  "SELL",
						CandleTime: &cTimeCopy,
						Reason:     "Exceeded maximum inside candles consolidation limit (2 inside candles)",
					})
					activeMaster = nil
					continue
				}
			}
		}

		// Scan for New Master Candle Formation
		if activeMaster == nil && !tradeTakenToday && timeStr < "11:00:00" {
			// Check BUY Setup (U-shape below EMAs with rebound)
			if c.Close > c.Open && (c.Close >= e10 || c.Close >= e20 || c.High >= e10 || c.High >= e20) {
				lowestLow := c.Low
				validUShape := true
				for k := 1; k <= 5; k++ {
					pastIdx := idx - k
					if pastIdx < 0 {
						validUShape = false
						break
					}
					pastC := all1m[pastIdx]
					pastE10 := ema10Values[pastIdx]
					pastE20 := ema20Values[pastIdx]
					if pastC.Close > pastE10 && pastC.Close > pastE20 {
						validUShape = false
						break
					}
					if pastC.Low < lowestLow {
						lowestLow = pastC.Low
					}
				}
				if validUShape && lowestLow > 0 && c.Close > 0 && (c.High-c.Low) > 0 {
					candleRange := c.High - c.Low
					reboundPct := (c.Close - lowestLow) / lowestLow * 100.0
					rangePct := (candleRange / c.Close) * 100.0
					upperWick := c.High - c.Close
					lowerWick := c.Open - c.Low
					wickPct := ((upperWick + lowerWick) / candleRange) * 100.0
					if reboundPct >= 0.5 && rangePct <= 2.0 && wickPct <= 40.0 {
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
								"u_shape_candles": 5,
								"lowest_low":      lowestLow,
								"rebound_pct":     reboundPct,
								"range_pct":       rangePct,
								"wick_pct":        wickPct,
								"ema10":           e10,
								"ema20":           e20,
							},
						})
					}
				}
			} else if c.Close < c.Open && (c.Close <= e10 || c.Close <= e20 || c.Low <= e10 || c.Low <= e20) {
				// Check SELL Setup (Inverted U-shape above EMAs with drop)
				highestHigh := c.High
				validInverted := true
				for k := 1; k <= 5; k++ {
					pastIdx := idx - k
					if pastIdx < 0 {
						validInverted = false
						break
					}
					pastC := all1m[pastIdx]
					pastE10 := ema10Values[pastIdx]
					pastE20 := ema20Values[pastIdx]
					if pastC.Close < pastE10 && pastC.Close < pastE20 {
						validInverted = false
						break
					}
					if pastC.High > highestHigh {
						highestHigh = pastC.High
					}
				}
				if validInverted && highestHigh > 0 && c.Close > 0 && (c.High-c.Low) > 0 {
					candleRange := c.High - c.Low
					dropPct := (highestHigh - c.Close) / highestHigh * 100.0
					rangePct := (candleRange / c.Close) * 100.0
					upperWick := c.High - c.Open
					lowerWick := c.Close - c.Low
					wickPct := ((upperWick + lowerWick) / candleRange) * 100.0
					if dropPct >= 0.5 && rangePct <= 2.0 && wickPct <= 40.0 {
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
								"inverted_candles": 5,
								"highest_high":     highestHigh,
								"drop_pct":         dropPct,
								"range_pct":        rangePct,
								"wick_pct":         wickPct,
								"ema10":            e10,
								"ema20":            e20,
							},
						})
					}
				}
			}
		}
	}

	return events
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
	if len(today5m) < 2 {
		return events
	}
	pdh := summary.PDH
	pdl := summary.PDL

	c1 := today5m[0]
	c1TimeIST := data.NormalizeToIST(c1.Time)
	c1TimeCopy := c1TimeIST

	// Check if Candle 1 looked like a breakout but got rejected (Fake Master)
	isFakeBuy := c1.High > pdh && c1.Close <= pdh && pdh > 0
	isFakeSell := c1.Low < pdl && c1.Close >= pdl && pdl > 0

	if isFakeBuy {
		events = append(events, data.StrategyEvent{
			EventTime:    c1TimeIST,
			Symbol:       symbol,
			Strategy:     "VANDE_BHARAT_TRAP",
			Stage:        "SETUP_FORMED",
			Direction:    "SELL",
			TriggerPrice: c1.Low,
			SLPrice:      c1.High,
			CandleTime:   &c1TimeCopy,
			Reason:       fmt.Sprintf("Bull Trap detected: Candle 1 breached PDH ₹%.2f but closed inside", pdh),
		})
	} else if isFakeSell {
		events = append(events, data.StrategyEvent{
			EventTime:    c1TimeIST,
			Symbol:       symbol,
			Strategy:     "VANDE_BHARAT_TRAP",
			Stage:        "SETUP_FORMED",
			Direction:    "BUY",
			TriggerPrice: c1.High,
			SLPrice:      c1.Low,
			CandleTime:   &c1TimeCopy,
			Reason:       fmt.Sprintf("Bear Trap detected: Candle 1 breached PDL ₹%.2f but closed inside", pdl),
		})
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
