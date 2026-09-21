package strategy

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// EMAS5BreakoutEngine implements the EMA S5 Breakout Strategy
type EMAS5BreakoutEngine struct {
	logger                    *zap.Logger
	mu                        sync.RWMutex
	indicators                *Indicators
	pdHighs                   map[string]float64
	pdLows                    map[string]float64
	pdCloses                  map[string]float64
	rollingCandles            map[string][]data.Candle
	tradeCountsPerStock       map[string]int
	masterCandles             map[string]*data.Candle
	masterCandleIndices       map[string]int
	masterDirections          map[string]string // "BUY" or "SELL"
	insideCandleCounts        map[string]int
	confirmationCandles       map[string]*data.Candle
	confirmationCandleIndices map[string]int
	lastSetupCandles          map[string]*SetupCandle
	firstCandles              map[string]*data.Candle

	// Configurable Parameters
	maxTradesPerStock   int     // Max trades per stock per day (default: 2)
	rallyCandlesCount   int     // Min oval sequence candles (default: 5)
	minReboundPct       float64 // Min oval rebound / drop move % (default: 0.5%)
	masterMaxPct        float64 // Master candle max range % (default: 2.0%)
	masterMaxWickPct    float64 // Master candle max total wick % (default: 40.0%)
	maxInsideCandles    int     // Max inside candles allowed before confirmation (default: 1)
	confirmMaxPct       float64 // Confirmation candle max range % (default: 1.0%)
	emaTouchBufferPct   float64 // EMA touch buffer % (default: 0.1%)
	tradeEndTime        string  // Cutoff time (default: "11:00:00")
	slBufferPct         float64 // SL buffer % (default: 0.1%)
	maxEntryDistancePct float64 // Max entry distance % from trigger price (default: 0.35%)
	maxSetupWaitCandles int     // Max candles to wait for breakout after confirmation before expiry (default: 6)
	minPDHPDLRetracePct float64 // Min pre-retrace extension % beyond PDH (BUY) or PDL (SELL) before trace back (default: 0.5%)
	MinCandlesToIgnore  int     // Min initial candles to ignore (default: 0)
	candleTimeFrame     string  // Candle interval (default: "1m")
	tracer              *EventTracer
}

// NewEMAS5BreakoutEngine creates a new instance of EMAS5BreakoutEngine
func NewEMAS5BreakoutEngine(
	logger *zap.Logger,
	maxTradesPerStock int,
	rallyCandlesCount int,
	minReboundPct float64,
	masterMaxPct float64,
	maxInsideCandles int,
	confirmMaxPct float64,
) *EMAS5BreakoutEngine {
	if maxTradesPerStock <= 0 {
		maxTradesPerStock = 2
	}
	if rallyCandlesCount <= 0 {
		rallyCandlesCount = 5
	}
	if minReboundPct <= 0 {
		minReboundPct = 0.5
	}
	if masterMaxPct <= 0 {
		masterMaxPct = 2.0
	}
	if maxInsideCandles < 0 {
		maxInsideCandles = 1
	}
	if confirmMaxPct <= 0 {
		confirmMaxPct = 1.0
	}

	return &EMAS5BreakoutEngine{
		logger:                    logger,
		indicators:                &Indicators{logger: logger},
		pdHighs:                   make(map[string]float64),
		pdLows:                    make(map[string]float64),
		pdCloses:                  make(map[string]float64),
		rollingCandles:            make(map[string][]data.Candle),
		tradeCountsPerStock:       make(map[string]int),
		masterCandles:             make(map[string]*data.Candle),
		masterCandleIndices:       make(map[string]int),
		masterDirections:          make(map[string]string),
		insideCandleCounts:        make(map[string]int),
		confirmationCandles:       make(map[string]*data.Candle),
		confirmationCandleIndices: make(map[string]int),
		lastSetupCandles:          make(map[string]*SetupCandle),
		firstCandles:              make(map[string]*data.Candle),
		maxTradesPerStock:         maxTradesPerStock,
		rallyCandlesCount:         rallyCandlesCount,
		minReboundPct:             minReboundPct,
		masterMaxPct:              masterMaxPct,
		masterMaxWickPct:          40.0,
		maxInsideCandles:          maxInsideCandles,
		confirmMaxPct:             confirmMaxPct,
		emaTouchBufferPct:         0.10,
		tradeEndTime:              "11:00:00",
		slBufferPct:               0.1,
		maxEntryDistancePct:       0.35,
		maxSetupWaitCandles:       6,
		minPDHPDLRetracePct:       0.5,
		MinCandlesToIgnore:        0,
		candleTimeFrame:           "1m",
	}
}

// Name returns the strategy name
func (e *EMAS5BreakoutEngine) Name() string {
	return "EMAS5_BREAKOUT"
}

// TradeEndTime returns the configured trade entry cutoff time (IST)
func (e *EMAS5BreakoutEngine) TradeEndTime() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.tradeEndTime == "" {
		return "11:00:00"
	}
	return e.tradeEndTime
}

// SetTradeEndTime updates the trade entry cutoff time (IST)
func (e *EMAS5BreakoutEngine) SetTradeEndTime(t string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t != "" {
		e.tradeEndTime = data.NormalizeTimeHHMMSS(t)
	}
}

// SetEventTracer attaches the event telemetry tracer
func (e *EMAS5BreakoutEngine) SetEventTracer(tracer *EventTracer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tracer = tracer
}

func (e *EMAS5BreakoutEngine) emitEvent(symbol, stage, severity, direction, title, reason string, candle *data.Candle, trigger, sl, tgt float64, details map[string]interface{}) {
	if e.tracer == nil {
		return
	}
	var candleTime *time.Time
	var cOpen, cHigh, cLow, cClose float64
	var cVol int64
	if candle != nil {
		ct := data.NormalizeToIST(candle.Time)
		candleTime = &ct
		cOpen = candle.Open
		cHigh = candle.High
		cLow = candle.Low
		cClose = candle.Close
		cVol = candle.Volume
	}
	e.tracer.Emit(&data.StrategyEvent{
		EventTime:    time.Now().In(data.ISTLocation),
		Symbol:       symbol,
		Strategy:     e.Name(),
		Stage:        stage,
		Severity:     severity,
		Direction:    direction,
		Title:        title,
		Reason:       reason,
		TriggerPrice: trigger,
		SLPrice:      sl,
		TargetPrice:  tgt,
		CandleTime:   candleTime,
		CandleOpen:   cOpen,
		CandleHigh:   cHigh,
		CandleLow:    cLow,
		CandleClose:  cClose,
		CandleVolume: cVol,
		Details:      details,
	})
}

// MaxEntryDistancePct returns the configured max entry distance percentage beyond confirmation level
func (e *EMAS5BreakoutEngine) MaxEntryDistancePct() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.maxEntryDistancePct <= 0 {
		return 0.35
	}
	return e.maxEntryDistancePct
}

// SetMaxEntryDistancePct updates the max entry distance percentage
func (e *EMAS5BreakoutEngine) SetMaxEntryDistancePct(pct float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if pct > 0 {
		e.maxEntryDistancePct = pct
	}
}

// MaxSetupWaitCandles returns the max candles to wait for breakout after confirmation before expiring
func (e *EMAS5BreakoutEngine) MaxSetupWaitCandles() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.maxSetupWaitCandles <= 0 {
		return 6
	}
	return e.maxSetupWaitCandles
}

// SetMaxSetupWaitCandles updates the max setup wait candles
func (e *EMAS5BreakoutEngine) SetMaxSetupWaitCandles(candles int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if candles > 0 {
		e.maxSetupWaitCandles = candles
	}
}

// MasterMaxWickPct returns the configured Master candle max wick percentage
func (e *EMAS5BreakoutEngine) MasterMaxWickPct() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.masterMaxWickPct <= 0 {
		return 40.0
	}
	return e.masterMaxWickPct
}

// SetMasterMaxWickPct updates the Master candle max wick percentage
func (e *EMAS5BreakoutEngine) SetMasterMaxWickPct(pct float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if pct > 0 {
		e.masterMaxWickPct = pct
	}
}

// EMATouchBufferPct returns the configured EMA touch buffer percentage
func (e *EMAS5BreakoutEngine) EMATouchBufferPct() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.emaTouchBufferPct < 0 {
		return 0.10
	}
	return e.emaTouchBufferPct
}

// SetEMATouchBufferPct updates the EMA touch buffer percentage
func (e *EMAS5BreakoutEngine) SetEMATouchBufferPct(pct float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if pct >= 0 {
		e.emaTouchBufferPct = pct
	}
}

// SLBufferPct returns the configured SL buffer percentage
func (e *EMAS5BreakoutEngine) SLBufferPct() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.slBufferPct < 0 {
		return 0.10
	}
	return e.slBufferPct
}

// SetSLBufferPct updates the SL buffer percentage
func (e *EMAS5BreakoutEngine) SetSLBufferPct(pct float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if pct >= 0 {
		e.slBufferPct = pct
	}
}

// MinPDHPDLRetracePct returns the configured minimum retracement % from PDH or PDL
func (e *EMAS5BreakoutEngine) MinPDHPDLRetracePct() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.minPDHPDLRetracePct
}

// SetMinPDHPDLRetracePct updates the minimum retracement % from PDH or PDL
func (e *EMAS5BreakoutEngine) SetMinPDHPDLRetracePct(pct float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if pct >= 0 {
		e.minPDHPDLRetracePct = pct
	}
}

// CandleTimeFrame returns the configured candle interval (e.g. "1m", "5m")
func (e *EMAS5BreakoutEngine) CandleTimeFrame() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.candleTimeFrame == "" {
		return "1m"
	}
	return e.candleTimeFrame
}

// SetCandleTimeFrame sets the strategy candle interval (e.g. "1m", "5m")
func (e *EMAS5BreakoutEngine) SetCandleTimeFrame(tf string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if tf == "" {
		tf = "1m"
	}
	e.candleTimeFrame = tf
}

// MaxTradesPerStock returns the configured max trades per stock limit
func (e *EMAS5BreakoutEngine) MaxTradesPerStock() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.maxTradesPerStock
}

// RallyCandlesCount returns the configured rally candles count
func (e *EMAS5BreakoutEngine) RallyCandlesCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rallyCandlesCount
}

// MinReboundPct returns the configured min rebound percentage
func (e *EMAS5BreakoutEngine) MinReboundPct() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.minReboundPct
}

// MasterMaxPct returns the configured master candle max percentage
func (e *EMAS5BreakoutEngine) MasterMaxPct() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.masterMaxPct
}

// MaxInsideCandles returns the configured max inside candles allowed
func (e *EMAS5BreakoutEngine) MaxInsideCandles() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.maxInsideCandles
}

// ConfirmMaxPct returns the configured confirmation candle max percentage
func (e *EMAS5BreakoutEngine) ConfirmMaxPct() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.confirmMaxPct
}

// UpdateRules dynamically updates strategy rules from UI settings
func (e *EMAS5BreakoutEngine) UpdateRules(
	maxTradesPerStock int,
	rallyCandlesCount int,
	minReboundPct float64,
	masterMaxPct float64,
	maxInsideCandles int,
	confirmMaxPct float64,
	tradeEndTime string,
) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if maxTradesPerStock > 0 {
		e.maxTradesPerStock = maxTradesPerStock
	}
	if rallyCandlesCount > 0 {
		e.rallyCandlesCount = rallyCandlesCount
	}
	if minReboundPct >= 0 {
		e.minReboundPct = minReboundPct
	}
	if masterMaxPct > 0 {
		e.masterMaxPct = masterMaxPct
	}
	if maxInsideCandles >= 0 {
		e.maxInsideCandles = maxInsideCandles
	}
	if confirmMaxPct > 0 {
		e.confirmMaxPct = confirmMaxPct
	}
	if tradeEndTime != "" {
		e.tradeEndTime = tradeEndTime
	}

	e.logger.Info("EMAS5_BREAKOUT strategy rules dynamically updated",
		zap.Int("max_trades_per_stock", e.maxTradesPerStock),
		zap.Int("rally_candles_count", e.rallyCandlesCount),
		zap.Float64("min_rebound_pct", e.minReboundPct),
		zap.Float64("master_max_pct", e.masterMaxPct),
		zap.Int("max_inside_candles", e.maxInsideCandles),
		zap.Float64("confirm_max_pct", e.confirmMaxPct),
		zap.String("trade_end_time", e.tradeEndTime),
	)
}

// SetPreviousDayLevels sets PDH, PDL, and Yesterday's Close price for a symbol
func (e *EMAS5BreakoutEngine) SetPreviousDayLevels(symbol string, pdh, pdl, pdClose float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pdHighs[symbol] = pdh
	e.pdLows[symbol] = pdl
	e.pdCloses[symbol] = pdClose
}

// ProcessCandle processes completed candles and drives the EMAS5 state machine
func (e *EMAS5BreakoutEngine) ProcessCandle(symbol string, candle data.Candle) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Determine IST timestamp and market session boundaries
	candleTimeIST := data.NormalizeToIST(candle.Time)
	candle.Time = candleTimeIST
	marketStartIST := time.Date(candleTimeIST.Year(), candleTimeIST.Month(), candleTimeIST.Day(), 9, 15, 0, 0, data.ISTLocation)

	// Deduplicate / update candle in rolling buffer
	existingIdx := -1
	for idx, c := range e.rollingCandles[symbol] {
		if data.NormalizeToIST(c.Time).Equal(candleTimeIST) {
			existingIdx = idx
			break
		}
	}

	if existingIdx != -1 {
		// Update existing candle in place with latest OHLCV
		e.rollingCandles[symbol][existingIdx] = candle
		if e.firstCandles[symbol] != nil && data.NormalizeToIST(e.firstCandles[symbol].Time).Equal(candleTimeIST) {
			cCopy := candle
			e.firstCandles[symbol] = &cCopy
		}
		if e.masterCandles[symbol] != nil && data.NormalizeToIST(e.masterCandles[symbol].Time).Equal(candleTimeIST) {
			mCopy := candle
			e.masterCandles[symbol] = &mCopy
		}
		if e.confirmationCandles[symbol] != nil && data.NormalizeToIST(e.confirmationCandles[symbol].Time).Equal(candleTimeIST) {
			confCopy := candle
			e.confirmationCandles[symbol] = &confCopy
		}
		return // Do not re-process already handled candle through EMAS5 state machine
	}

	// Append candle to rolling buffer (max 150 historical candles for full EMA convergence)
	candles := append(e.rollingCandles[symbol], candle)
	sort.SliceStable(candles, func(i, j int) bool {
		return candles[i].Time.Before(candles[j].Time)
	})
	if len(candles) > 150 {
		candles = candles[len(candles)-150:]
	}
	e.rollingCandles[symbol] = candles
	candleCount := len(candles)

	// If candle is a warm-up candle from a previous day (before 09:15 AM today):
	// It is safely stored in rollingCandles for full EMA convergence, but does NOT drive intraday state transitions.
	if candleTimeIST.Before(marketStartIST) {
		return
	}

	// Trade Cutoff Guard: If candle is at or after tradeEndTime, invalidate pending setup and reject new setups
	if e.tradeEndTime != "" {
		endH, endM, endS, errTime := data.ParseTimeHMS(e.tradeEndTime)
		if errTime == nil {
			endBoundary := time.Date(candleTimeIST.Year(), candleTimeIST.Month(), candleTimeIST.Day(), endH, endM, endS, 0, data.ISTLocation)
			if !candleTimeIST.Before(endBoundary) {
				e.resetSymbolSetup(symbol)
				return
			}
		}
	}

	// Anchor 09:15 AM first candle of today
	if candleTimeIST.Hour() == 9 && candleTimeIST.Minute() == 15 && e.firstCandles[symbol] == nil {
		cCopy := candle
		e.firstCandles[symbol] = &cCopy
	}

	// Respect max trades per stock constraint
	if e.tradeCountsPerStock[symbol] >= e.maxTradesPerStock {
		return
	}

	// Compute rolling EMA 10 and EMA 20
	closes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
	}
	ema10Series := e.indicators.CalculateEMA(closes, 10)
	ema20Series := e.indicators.CalculateEMA(closes, 20)

	if len(ema10Series) == 0 || len(ema20Series) == 0 {
		return
	}
	currentEMA10 := ema10Series[len(ema10Series)-1]
	currentEMA20 := ema20Series[len(ema20Series)-1]

	master := e.masterCandles[symbol]
	masterIdx := e.masterCandleIndices[symbol]
	masterDir := e.masterDirections[symbol]
	confirm := e.confirmationCandles[symbol]

	// -------------------------------------------------------------
	// State 1a: Active Setup Monitoring (Master Candle formed, awaiting Confirmation)
	// -------------------------------------------------------------
	if master != nil && confirm == nil {
		currIdx := candleCount - 1
		if currIdx <= masterIdx {
			return
		}

		if masterDir == "BUY" {
			// Rule 1: Master Low Invalidation Guard
			if candle.Low < master.Low {
				e.logger.Info("Invalidated EMAS5 BUY setup: Candle broke Master Low",
					zap.String("symbol", symbol),
					zap.Float64("candle_low", candle.Low),
					zap.Float64("master_low", master.Low),
				)
				e.emitEvent(symbol, "SETUP_INVALIDATED", "DANGER", "BUY",
					"EMAS5 BUY Setup Invalidated: Master Low Pierced",
					fmt.Sprintf("Candle low ₹%.2f breached Master Low ₹%.2f", candle.Low, master.Low),
					&candle, 0, 0, 0,
					map[string]interface{}{"candle_low": candle.Low, "master_low": master.Low},
				)
				e.resetSymbolSetup(symbol)
				master = nil
			} else if candle.High > master.High {
				// Confirmation Candle must close strictly ABOVE Master Low and MUST be GREEN!
				// If it fails to close above Master Low or closes RED/DOJI, it is a failed breakout rejection -> Invalidate setup
				if candle.Close <= master.Low || candle.Close <= candle.Open {
					e.logger.Info("Invalidated EMAS5 BUY setup: Candle broke Master High but failed to close above Master Low or closed RED/DOJI (Rejection)",
						zap.String("symbol", symbol),
						zap.Float64("open", candle.Open),
						zap.Float64("close", candle.Close),
						zap.Float64("master_low", master.Low),
					)
					e.emitEvent(symbol, "CONFIRMATION_FAILED", "WARNING", "BUY",
						"EMAS5 BUY Confirmation Failed: Rejection Close",
						fmt.Sprintf("Candle broke Master High ₹%.2f but closed RED/DOJI (Open ₹%.2f, Close ₹%.2f)", master.High, candle.Open, candle.Close),
						&candle, 0, 0, 0,
						map[string]interface{}{"open": candle.Open, "close": candle.Close, "master_high": master.High, "master_low": master.Low},
					)
					e.resetSymbolSetup(symbol)
					return
				}

				confirmRangePct := (candle.High - candle.Low) / candle.Close * 100.0
				if confirmRangePct > e.confirmMaxPct {
					e.logger.Info("Invalidated EMAS5 BUY confirmation: Range exceeds threshold",
						zap.String("symbol", symbol),
						zap.Float64("range_pct", confirmRangePct),
						zap.Float64("max_range_pct", e.confirmMaxPct),
					)
					e.emitEvent(symbol, "CONFIRMATION_FAILED", "WARNING", "BUY",
						"EMAS5 BUY Confirmation Range Exceeded",
						fmt.Sprintf("Confirmation candle range %.2f%% exceeds max allowed %.2f%%", confirmRangePct, e.confirmMaxPct),
						&candle, 0, 0, 0,
						map[string]interface{}{"range_pct": confirmRangePct, "max_range_pct": e.confirmMaxPct},
					)
					e.resetSymbolSetup(symbol)
					return
				}

				cCopy := candle
				e.confirmationCandles[symbol] = &cCopy
				e.confirmationCandleIndices[symbol] = candleCount - 1
				e.lastSetupCandles[symbol] = &SetupCandle{
					Candle: candle,
					High:   candle.High,
					Low:    candle.Low,
					Volume: candle.Volume,
				}
				e.logger.Info("Established Confirmation Candle (EMAS5_BREAKOUT BUY)",
					zap.String("symbol", symbol),
					zap.Float64("confirmation_high", candle.High),
					zap.Float64("confirmation_low", candle.Low),
					zap.Float64("range_pct", confirmRangePct),
				)
				e.emitEvent(symbol, "CONFIRMATION_ARMED", "SUCCESS", "BUY",
					"EMAS5 BUY Confirmation Armed: Awaiting Breakout",
					fmt.Sprintf("Confirmation candle armed. Trigger High: ₹%.2f, SL Anchor Low: ₹%.2f (Range %.2f%%)", candle.High, candle.Low, confirmRangePct),
					&candle, candle.High, candle.Low, 0,
					map[string]interface{}{"trigger_high": candle.High, "sl_anchor_low": candle.Low, "range_pct": confirmRangePct},
				)
			} else {
				// Rule 3: Inside Candle Consolidation Count
				// Candle stayed inside Master range: High <= Master.High && Low >= Master.Low
				e.insideCandleCounts[symbol]++
				if e.insideCandleCounts[symbol] > e.maxInsideCandles {
					e.logger.Info("Invalidated EMAS5 BUY setup: Exceeded max inside candles limit",
						zap.String("symbol", symbol),
						zap.Int("inside_candles", e.insideCandleCounts[symbol]),
						zap.Int("max_allowed", e.maxInsideCandles),
					)
					e.emitEvent(symbol, "SETUP_INVALIDATED", "WARNING", "BUY",
						"EMAS5 BUY Setup Expired: Max Inside Candles Exceeded",
						fmt.Sprintf("Inside candles count (%d) exceeded max allowed (%d)", e.insideCandleCounts[symbol], e.maxInsideCandles),
						&candle, 0, 0, 0,
						map[string]interface{}{"inside_candles": e.insideCandleCounts[symbol], "max_allowed": e.maxInsideCandles},
					)
					e.resetSymbolSetup(symbol)
					return
				}
				return
			}

		} else if masterDir == "SELL" {
			// Rule 1: Master High Invalidation Guard
			if candle.High > master.High {
				e.logger.Info("Invalidated EMAS5 SELL setup: Candle broke Master High",
					zap.String("symbol", symbol),
					zap.Float64("candle_high", candle.High),
					zap.Float64("master_high", master.High),
				)
				e.emitEvent(symbol, "SETUP_INVALIDATED", "DANGER", "SELL",
					"EMAS5 SELL Setup Invalidated: Master High Pierced",
					fmt.Sprintf("Candle high ₹%.2f breached Master High ₹%.2f", candle.High, master.High),
					&candle, 0, 0, 0,
					map[string]interface{}{"candle_high": candle.High, "master_high": master.High},
				)
				e.resetSymbolSetup(symbol)
				master = nil
			} else if candle.Low < master.Low {
				// Confirmation Candle must close strictly BELOW Master High and MUST be RED!
				// If it fails to close below Master High or closes GREEN/DOJI, it is a failed breakdown rejection -> Invalidate setup
				if candle.Close >= master.High || candle.Close >= candle.Open {
					e.logger.Info("Invalidated EMAS5 SELL setup: Candle broke Master Low but failed to close below Master High or closed GREEN/DOJI (Rejection)",
						zap.String("symbol", symbol),
						zap.Float64("open", candle.Open),
						zap.Float64("close", candle.Close),
						zap.Float64("master_high", master.High),
					)
					e.emitEvent(symbol, "CONFIRMATION_FAILED", "WARNING", "SELL",
						"EMAS5 SELL Confirmation Failed: Rejection Close",
						fmt.Sprintf("Candle broke Master Low ₹%.2f but closed GREEN/DOJI (Open ₹%.2f, Close ₹%.2f)", master.Low, candle.Open, candle.Close),
						&candle, 0, 0, 0,
						map[string]interface{}{"open": candle.Open, "close": candle.Close, "master_high": master.High, "master_low": master.Low},
					)
					e.resetSymbolSetup(symbol)
					return
				}

				confirmRangePct := (candle.High - candle.Low) / candle.Close * 100.0
				if confirmRangePct > e.confirmMaxPct {
					e.logger.Info("Invalidated EMAS5 SELL confirmation: Range exceeds threshold",
						zap.String("symbol", symbol),
						zap.Float64("range_pct", confirmRangePct),
						zap.Float64("max_range_pct", e.confirmMaxPct),
					)
					e.emitEvent(symbol, "CONFIRMATION_FAILED", "WARNING", "SELL",
						"EMAS5 SELL Confirmation Range Exceeded",
						fmt.Sprintf("Confirmation candle range %.2f%% exceeds max allowed %.2f%%", confirmRangePct, e.confirmMaxPct),
						&candle, 0, 0, 0,
						map[string]interface{}{"range_pct": confirmRangePct, "max_range_pct": e.confirmMaxPct},
					)
					e.resetSymbolSetup(symbol)
					return
				}

				cCopy := candle
				e.confirmationCandles[symbol] = &cCopy
				e.confirmationCandleIndices[symbol] = candleCount - 1
				e.lastSetupCandles[symbol] = &SetupCandle{
					Candle: candle,
					High:   candle.High,
					Low:    candle.Low,
					Volume: candle.Volume,
				}
				e.logger.Info("Established Confirmation Candle (EMAS5_BREAKOUT SELL)",
					zap.String("symbol", symbol),
					zap.Float64("confirmation_high", candle.High),
					zap.Float64("confirmation_low", candle.Low),
					zap.Float64("range_pct", confirmRangePct),
				)
				e.emitEvent(symbol, "CONFIRMATION_ARMED", "SUCCESS", "SELL",
					"EMAS5 SELL Confirmation Armed: Awaiting Breakdown",
					fmt.Sprintf("Confirmation candle armed. Trigger Low: ₹%.2f, SL Anchor High: ₹%.2f (Range %.2f%%)", candle.Low, candle.High, confirmRangePct),
					&candle, candle.Low, candle.High, 0,
					map[string]interface{}{"trigger_low": candle.Low, "sl_anchor_high": candle.High, "range_pct": confirmRangePct},
				)
			} else {
				// Rule 3: Inside Candle Consolidation Count
				e.insideCandleCounts[symbol]++
				if e.insideCandleCounts[symbol] > e.maxInsideCandles {
					e.logger.Info("Invalidated EMAS5 SELL setup: Exceeded max inside candles limit",
						zap.String("symbol", symbol),
						zap.Int("inside_candles", e.insideCandleCounts[symbol]),
						zap.Int("max_allowed", e.maxInsideCandles),
					)
					e.emitEvent(symbol, "SETUP_INVALIDATED", "WARNING", "SELL",
						"EMAS5 SELL Setup Expired: Max Inside Candles Exceeded",
						fmt.Sprintf("Inside candles count (%d) exceeded max allowed (%d)", e.insideCandleCounts[symbol], e.maxInsideCandles),
						&candle, 0, 0, 0,
						map[string]interface{}{"inside_candles": e.insideCandleCounts[symbol], "max_allowed": e.maxInsideCandles},
					)
					e.resetSymbolSetup(symbol)
					return
				}
				return
			}
		}
	}

	// -------------------------------------------------------------
	// State 1b: Active Breakout Pending (Confirmation Candle formed, awaiting trigger)
	// -------------------------------------------------------------
	if master != nil && confirm != nil {
		currIdx := candleCount - 1
		confirmIdx, hasConfirmIdx := e.confirmationCandleIndices[symbol]

		// Stale Setup Expiry Guard: Invalidate setup if breakout is not triggered within maxSetupWaitCandles
		if e.maxSetupWaitCandles > 0 && hasConfirmIdx && currIdx > confirmIdx && (currIdx-confirmIdx) >= e.maxSetupWaitCandles {
			e.logger.Info("Invalidated EMAS5 pending breakout: Setup expired (exceeded max wait candles)",
				zap.String("symbol", symbol),
				zap.Int("candles_waited", currIdx-confirmIdx),
				zap.Int("max_wait_candles", e.maxSetupWaitCandles),
			)
			e.emitEvent(symbol, "SETUP_EXPIRED", "WARNING", masterDir,
				"Pending Breakout Expired",
				fmt.Sprintf("Pending breakout expired: not triggered within %d candles after confirmation (%d candles waited)", e.maxSetupWaitCandles, currIdx-confirmIdx),
				&candle, 0, 0, 0,
				map[string]interface{}{"candles_waited": currIdx - confirmIdx, "max_wait_candles": e.maxSetupWaitCandles},
			)
			e.resetSymbolSetup(symbol)
			return
		}

		if masterDir == "BUY" {
			// If a subsequent closed candle breaches Master Low before triggering breakout -> Invalidate
			if candle.Low < master.Low {
				reason := fmt.Sprintf("Candle Low ₹%.2f breached Master Low ₹%.2f before breakout", candle.Low, master.Low)
				e.logger.Info("Invalidated EMAS5 BUY pending breakout: Candle breached Master Low",
					zap.String("symbol", symbol),
					zap.Float64("candle_low", candle.Low),
					zap.Float64("master_low", master.Low),
				)
				e.emitEvent(symbol, "SETUP_INVALIDATED", "DANGER", "BUY",
					"Pending Breakout Invalidated",
					reason,
					&candle, 0, 0, 0,
					map[string]interface{}{"candle_low": candle.Low, "master_low": master.Low},
				)
				e.resetSymbolSetup(symbol)
				master = nil
				confirm = nil
			}
		} else if masterDir == "SELL" {
			// If a subsequent closed candle breaches Master High before triggering breakdown -> Invalidate
			if candle.High > master.High {
				reason := fmt.Sprintf("Candle High ₹%.2f breached Master High ₹%.2f before breakdown", candle.High, master.High)
				e.logger.Info("Invalidated EMAS5 SELL pending breakdown: Candle breached Master High",
					zap.String("symbol", symbol),
					zap.Float64("candle_high", candle.High),
					zap.Float64("master_high", master.High),
				)
				e.emitEvent(symbol, "SETUP_INVALIDATED", "DANGER", "SELL",
					"Pending Breakdown Invalidated",
					reason,
					&candle, 0, 0, 0,
					map[string]interface{}{"candle_high": candle.High, "master_high": master.High},
				)
				e.resetSymbolSetup(symbol)
				master = nil
				confirm = nil
			}
		}
	}

	// -------------------------------------------------------------
	// State 2: Scan for New Master Candle Formation
	// -------------------------------------------------------------
	if master == nil {
		candidateDateStr := candleTimeIST.Format("2006-01-02")
		todayStartIdx := -1
		for i := range candles {
			t := data.NormalizeToIST(candles[i].Time)
			if t.Format("2006-01-02") == candidateDateStr && (t.Equal(marketStartIST) || t.After(marketStartIST)) {
				todayStartIdx = i
				break
			}
		}
		if todayStartIdx < 0 || (candleCount-1-todayStartIdx) < e.rallyCandlesCount {
			return // Need at least N rally candles completed in today's session before candidate candle
		}

		pdh := e.pdHighs[symbol]
		pdl := e.pdLows[symbol]
		masterRangePct := (candle.High - candle.Low) / candle.Close * 100.0

		// Check candidate Master range cap (<= 2.0%)
		if masterRangePct > e.masterMaxPct {
			return
		}

		// Check candidate Master wick cap (<= 40.0% default)
		candleRange := candle.High - candle.Low
		bodySize := math.Abs(candle.Close - candle.Open)
		wickSize := candleRange - bodySize
		if candleRange > 0 {
			wickPct := (wickSize / candleRange) * 100.0
			if wickPct > e.masterMaxWickPct {
				e.logger.Info("[EMAS5_BREAKOUT] Candidate Master candle wicks exceed maximum allowed",
					zap.String("symbol", symbol),
					zap.Float64("total_wick_pct", wickPct),
					zap.Float64("max_wick_pct", e.masterMaxWickPct),
				)
				return
			}
		}

		// -----------------------------
		// A. Test BUY Master Candidate
		// -----------------------------
		if candle.Close > candle.Open { // Must be GREEN
			// Interaction condition: Must touch or come within buffer of EMA 10, EMA 20, or PDH
			ema10Upper := currentEMA10 * (1.0 + e.emaTouchBufferPct/100.0)
			ema10Lower := currentEMA10 * (1.0 - e.emaTouchBufferPct/100.0)
			ema20Upper := currentEMA20 * (1.0 + e.emaTouchBufferPct/100.0)
			ema20Lower := currentEMA20 * (1.0 - e.emaTouchBufferPct/100.0)
			touchesEMA := (candle.Low <= ema10Upper && candle.High >= ema10Lower) ||
				(candle.Low <= ema20Upper && candle.High >= ema20Lower)

			touchesPDH := false
			if pdh > 0 {
				pdhUpper := pdh * (1.0 + e.emaTouchBufferPct/100.0)
				pdhLower := pdh * (1.0 - e.emaTouchBufferPct/100.0)
				touchesPDH = candle.Low <= pdhUpper && candle.High >= pdhLower
			}
			touchesAnyLevel := touchesEMA || touchesPDH

			// Close condition: Must close above ALL active key levels (EMA 10, EMA 20, and PDH if set)
			closesAboveAll := candle.Close > currentEMA10 && candle.Close > currentEMA20
			if pdh > 0 && candle.Close <= pdh {
				closesAboveAll = false
			}

			if touchesAnyLevel && closesAboveAll {
				isValid, lowestLow, candlesSinceLowest, reboundPct, pdhRetracePct := e.validateBuyUShape(candles, candleCount-1, pdh)
				if isValid {
					cCopy := candle
					e.masterCandles[symbol] = &cCopy
					e.masterCandleIndices[symbol] = candleCount - 1
					e.masterDirections[symbol] = "BUY"
					e.insideCandleCounts[symbol] = 0
					e.confirmationCandles[symbol] = nil

					e.logger.Info("Established Master Candle (EMAS5_BREAKOUT BUY Bottom-to-Top Oval)",
						zap.String("symbol", symbol),
						zap.Float64("master_high", candle.High),
						zap.Float64("master_low", candle.Low),
						zap.Float64("lowest_low", lowestLow),
						zap.Int("candles_since_lowest", candlesSinceLowest),
						zap.Float64("rebound_pct", reboundPct),
						zap.Float64("pdh_retrace_pct", pdhRetracePct),
						zap.Float64("ema10", currentEMA10),
						zap.Float64("ema20", currentEMA20),
					)
					e.emitEvent(symbol, "MASTER_FORMED", "SUCCESS", "BUY",
						fmt.Sprintf("EMAS5 BUY Master Formed [%s]", candleTimeIST.Format("15:04")),
						fmt.Sprintf("Master candle formed (U-Shape rebound %.2f%% from low ₹%.2f, PDH retrace %.2f%%). Setup armed, awaiting confirmation.", reboundPct, lowestLow, pdhRetracePct),
						&candle, candle.High, candle.Low, 0,
						map[string]interface{}{
							"master_high":     candle.High,
							"master_low":      candle.Low,
							"range_pct":       masterRangePct,
							"rebound_pct":     reboundPct,
							"pdh_retrace_pct": pdhRetracePct,
							"lowest_low":      lowestLow,
							"ema10":           currentEMA10,
							"ema20":           currentEMA20,
						},
					)
					return
				}
			}
		}

		// ------------------------------
		// B. Test SELL Master Candidate (Top to Bottom Oval Decay)
		// ------------------------------
		if candle.Close < candle.Open { // Must be RED
			// Interaction condition: Must touch or come within buffer of EMA 10, EMA 20, or PDL
			ema10Upper := currentEMA10 * (1.0 + e.emaTouchBufferPct/100.0)
			ema10Lower := currentEMA10 * (1.0 - e.emaTouchBufferPct/100.0)
			ema20Upper := currentEMA20 * (1.0 + e.emaTouchBufferPct/100.0)
			ema20Lower := currentEMA20 * (1.0 - e.emaTouchBufferPct/100.0)
			touchesEMA := (candle.High >= ema10Lower && candle.Low <= ema10Upper) ||
				(candle.High >= ema20Lower && candle.Low <= ema20Upper)

			touchesPDL := false
			if pdl > 0 {
				pdlUpper := pdl * (1.0 + e.emaTouchBufferPct/100.0)
				pdlLower := pdl * (1.0 - e.emaTouchBufferPct/100.0)
				touchesPDL = candle.High >= pdlLower && candle.Low <= pdlUpper
			}
			touchesAnyLevel := touchesEMA || touchesPDL

			// Close condition: Must close below ALL active key levels (EMA 10, EMA 20, and PDL if set)
			closesBelowAll := candle.Close < currentEMA10 && candle.Close < currentEMA20
			if pdl > 0 && candle.Close >= pdl {
				closesBelowAll = false
			}

			if touchesAnyLevel && closesBelowAll {
				isValid, highestHigh, candlesSinceHighest, dropPct, pdlRetracePct := e.validateSellInvertedUShape(candles, candleCount-1, pdl)
				if isValid {
					cCopy := candle
					e.masterCandles[symbol] = &cCopy
					e.masterCandleIndices[symbol] = candleCount - 1
					e.masterDirections[symbol] = "SELL"
					e.insideCandleCounts[symbol] = 0
					e.confirmationCandles[symbol] = nil

					e.logger.Info("Established Master Candle (EMAS5_BREAKOUT SELL Top-to-Bottom Oval)",
						zap.String("symbol", symbol),
						zap.Float64("master_high", candle.High),
						zap.Float64("master_low", candle.Low),
						zap.Float64("highest_high", highestHigh),
						zap.Int("candles_since_highest", candlesSinceHighest),
						zap.Float64("drop_pct", dropPct),
						zap.Float64("pdl_retrace_pct", pdlRetracePct),
						zap.Float64("ema10", currentEMA10),
						zap.Float64("ema20", currentEMA20),
					)
					e.emitEvent(symbol, "MASTER_FORMED", "SUCCESS", "SELL",
						fmt.Sprintf("EMAS5 SELL Master Formed [%s]", candleTimeIST.Format("15:04")),
						fmt.Sprintf("Master candle formed (Inverted U-Shape drop %.2f%% from high ₹%.2f, PDL retrace %.2f%%). Setup armed, awaiting confirmation.", dropPct, highestHigh, pdlRetracePct),
						&candle, candle.Low, candle.High, 0,
						map[string]interface{}{
							"master_high":     candle.High,
							"master_low":      candle.Low,
							"range_pct":       masterRangePct,
							"drop_pct":        dropPct,
							"pdl_retrace_pct": pdlRetracePct,
							"highest_high":    highestHigh,
							"ema10":           currentEMA10,
							"ema20":           currentEMA20,
						},
					)
					return
				}
			}
		}
	}
}

// validateBuyUShape validates that preceding candles form a genuine Bullish 'U'-Shape arc.
// Returns (isValid, lowestLow, candlesSinceLowest, reboundPct, pdhRetracePct).
func (e *EMAS5BreakoutEngine) validateBuyUShape(candles []data.Candle, candidateIdx int, pdh float64) (bool, float64, int, float64, float64) {
	if candidateIdx <= 0 || candidateIdx >= len(candles) {
		return false, 0, 0, 0, 0
	}

	candidateTimeIST := data.NormalizeToIST(candles[candidateIdx].Time)
	candidateDateStr := candidateTimeIST.Format("2006-01-02")
	marketStartIST := time.Date(candidateTimeIST.Year(), candidateTimeIST.Month(), candidateTimeIST.Day(), 9, 15, 0, 0, data.ISTLocation)

	// Identify today's session start index in the rolling buffer
	todayStartIdx := -1
	for i := range candles {
		t := data.NormalizeToIST(candles[i].Time)
		if t.Format("2006-01-02") == candidateDateStr && (t.Equal(marketStartIST) || t.After(marketStartIST)) {
			todayStartIdx = i
			break
		}
	}

	if todayStartIdx < 0 || candidateIdx-todayStartIdx < e.rallyCandlesCount {
		return false, 0, 0, 0, 0
	}

	master := candles[candidateIdx]

	// 1. Scan all preceding candles of the day (since 09:15 AM) to find the Day's Lowest Low.
	// In case of equal low across multiple candles, use the earliest candle that formed the bottom.
	lowestLow := math.MaxFloat64
	lowestIdx := -1
	for k := todayStartIdx; k < candidateIdx; k++ {
		if candles[k].Low < lowestLow {
			lowestLow = candles[k].Low
			lowestIdx = k
		}
	}

	if lowestIdx < todayStartIdx || lowestLow <= 0 {
		return false, 0, 0, 0, 0
	}

	candlesSinceLowest := candidateIdx - lowestIdx
	// Requirement: Day's Lowest Low must have formed at least rallyCandlesCount candles before the Master candle.
	if candlesSinceLowest < e.rallyCandlesCount {
		return false, 0, 0, 0, 0
	}

	// 2. Rebound Condition from Lowest Low to Master Close
	reboundPct := (master.Close - lowestLow) / lowestLow * 100.0
	if reboundPct < e.minReboundPct {
		return false, 0, 0, 0, 0
	}

	// Scan candles from todayStartIdx up to lowestIdx to find peak high reached before the trace back down to lowestLow.
	peakHighBeforeTrough := -math.MaxFloat64
	for k := todayStartIdx; k <= lowestIdx; k++ {
		if candles[k].High > peakHighBeforeTrough {
			peakHighBeforeTrough = candles[k].High
		}
	}

	pdhRetracePct := 0.0
	if pdh > 0 && peakHighBeforeTrough > -math.MaxFloat64 {
		pdhRetracePct = (peakHighBeforeTrough - pdh) / pdh * 100.0
	}

	// 3. Anti-V-Spike Guard: Lowest low cannot be formed right at candidateIdx-1 or candidateIdx-2 without sufficient recovery
	if candlesSinceLowest < 2 {
		return false, lowestLow, candlesSinceLowest, reboundPct, pdhRetracePct
	}

	// 4. Arc Continuity & Broken Cycle Guard:
	// If after lowestIdx, price made an intermediate peak (interHigh) and then fell into a new downward swing
	// that bottomed at candidateIdx-1 or candidateIdx-2, verify that this separate down-swing does not invalidate the original U-shape.
	if lowestIdx < candidateIdx-2 {
		interHigh := -math.MaxFloat64
		interHighIdx := -1
		for k := lowestIdx; k < candidateIdx; k++ {
			if candles[k].High > interHigh {
				interHigh = candles[k].High
				interHighIdx = k
			}
		}

		if interHighIdx > lowestIdx && interHighIdx < candidateIdx-1 {
			// Find lowest low after interHigh
			recentLow := math.MaxFloat64
			recentLowIdx := -1
			for k := interHighIdx; k < candidateIdx; k++ {
				if candles[k].Low < recentLow {
					recentLow = candles[k].Low
					recentLowIdx = k
				}
			}

			// If price dropped significantly (>= 0.30%) from the intermediate high and formed a local trough right before candidate
			if recentLowIdx >= candidateIdx-2 && interHigh > 0 && (interHigh-recentLow)/interHigh*100.0 >= 0.30 {
				// The move from interHigh to candidate is a new separate swing.
				// If the distance from interHigh is less than rallyCandlesCount, it is an incomplete/broken mini-swing
				if candidateIdx-interHighIdx < e.rallyCandlesCount {
					return false, lowestLow, candlesSinceLowest, reboundPct, pdhRetracePct // Disqualified: Broken arc with unconfirmed recent decline
				}
			}
		}
	}

	// 5. Minimum Retracement from PDH Check:
	// Price must have gone above PDH by at least minPDHPDLRetracePct before the trace back down to lowestLow.
	if e.minPDHPDLRetracePct > 0 && pdh > 0 {
		if pdhRetracePct < e.minPDHPDLRetracePct {
			return false, lowestLow, candlesSinceLowest, reboundPct, pdhRetracePct // Disqualified: Insufficient extension above PDH before trace back
		}
	}

	return true, lowestLow, candlesSinceLowest, reboundPct, pdhRetracePct
}

// validateSellInvertedUShape validates that preceding candles form a genuine Bearish Inverted 'U'-Shape arc.
// Returns (isValid, highestHigh, candlesSinceHighest, dropPct, pdlRetracePct).
func (e *EMAS5BreakoutEngine) validateSellInvertedUShape(candles []data.Candle, candidateIdx int, pdl float64) (bool, float64, int, float64, float64) {
	if candidateIdx <= 0 || candidateIdx >= len(candles) {
		return false, 0, 0, 0, 0
	}

	candidateTimeIST := data.NormalizeToIST(candles[candidateIdx].Time)
	candidateDateStr := candidateTimeIST.Format("2006-01-02")
	marketStartIST := time.Date(candidateTimeIST.Year(), candidateTimeIST.Month(), candidateTimeIST.Day(), 9, 15, 0, 0, data.ISTLocation)

	// Identify today's session start index in the rolling buffer
	todayStartIdx := -1
	for i := range candles {
		t := data.NormalizeToIST(candles[i].Time)
		if t.Format("2006-01-02") == candidateDateStr && (t.Equal(marketStartIST) || t.After(marketStartIST)) {
			todayStartIdx = i
			break
		}
	}

	if todayStartIdx < 0 || candidateIdx-todayStartIdx < e.rallyCandlesCount {
		return false, 0, 0, 0, 0
	}

	master := candles[candidateIdx]

	// 1. Scan all preceding candles of the day (since 09:15 AM) to find the Day's Highest High.
	// In case of equal high across multiple candles, use the earliest candle that formed the peak.
	highestHigh := -math.MaxFloat64
	highestIdx := -1
	for k := todayStartIdx; k < candidateIdx; k++ {
		if candles[k].High > highestHigh {
			highestHigh = candles[k].High
			highestIdx = k
		}
	}

	if highestIdx < todayStartIdx || highestHigh <= 0 {
		return false, 0, 0, 0, 0
	}

	candlesSinceHighest := candidateIdx - highestIdx
	// Requirement: Day's Highest High must have formed at least rallyCandlesCount candles before the Master candle.
	if candlesSinceHighest < e.rallyCandlesCount {
		return false, 0, 0, 0, 0
	}

	// 2. Drop Condition from Highest High to Master Close
	dropPct := (highestHigh - master.Close) / highestHigh * 100.0
	if dropPct < e.minReboundPct {
		return false, 0, 0, 0, 0
	}

	// Scan candles from todayStartIdx up to highestIdx to find trough low reached before the trace back up to highestHigh.
	troughLowBeforePeak := math.MaxFloat64
	for k := todayStartIdx; k <= highestIdx; k++ {
		if candles[k].Low < troughLowBeforePeak {
			troughLowBeforePeak = candles[k].Low
		}
	}

	pdlRetracePct := 0.0
	if pdl > 0 && troughLowBeforePeak < math.MaxFloat64 {
		pdlRetracePct = (pdl - troughLowBeforePeak) / pdl * 100.0
	}

	// 3. Anti-V-Spike Guard:
	if candlesSinceHighest < 2 {
		return false, highestHigh, candlesSinceHighest, dropPct, pdlRetracePct
	}

	// 4. Arc Continuity & Broken Cycle Guard:
	if highestIdx < candidateIdx-2 {
		interLow := math.MaxFloat64
		interLowIdx := -1
		for k := highestIdx; k < candidateIdx; k++ {
			if candles[k].Low < interLow {
				interLow = candles[k].Low
				interLowIdx = k
			}
		}

		if interLowIdx > highestIdx && interLowIdx < candidateIdx-1 {
			recentHigh := -math.MaxFloat64
			recentHighIdx := -1
			for k := interLowIdx; k < candidateIdx; k++ {
				if candles[k].High > recentHigh {
					recentHigh = candles[k].High
					recentHighIdx = k
				}
			}

			if recentHighIdx >= candidateIdx-2 && interLow > 0 && (recentHigh-interLow)/interLow*100.0 >= 0.30 {
				if candidateIdx-interLowIdx < e.rallyCandlesCount {
					return false, highestHigh, candlesSinceHighest, dropPct, pdlRetracePct // Disqualified: Broken arc with unconfirmed recent rally
				}
			}
		}
	}

	// 5. Minimum Retracement from PDL Check:
	// Price must have dropped below PDL by at least minPDHPDLRetracePct before the trace back up to highestHigh.
	if e.minPDHPDLRetracePct > 0 && pdl > 0 {
		if pdlRetracePct < e.minPDHPDLRetracePct {
			return false, highestHigh, candlesSinceHighest, dropPct, pdlRetracePct // Disqualified: Insufficient drop below PDL before trace back
		}
	}

	return true, highestHigh, candlesSinceHighest, dropPct, pdlRetracePct
}

// OnCandleClose processes completed candles (Strategy interface)
func (e *EMAS5BreakoutEngine) OnCandleClose(candle *data.Candle, symbol string) {
	if candle != nil {
		e.ProcessCandle(symbol, *candle)
	}
}

// WarmUpCandles loads historical candles into the rolling buffer for indicator convergence
// without driving intraday state transitions (master or confirmation candle formation).
func (e *EMAS5BreakoutEngine) WarmUpCandles(symbol string, newCandles []data.Candle) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Deduplicate existing and new candles by timestamp
	candleMap := make(map[int64]data.Candle)
	for _, c := range e.rollingCandles[symbol] {
		cNorm := c
		cNorm.Time = data.NormalizeToIST(c.Time)
		candleMap[cNorm.Time.Unix()] = cNorm
	}
	for _, c := range newCandles {
		cNorm := c
		cNorm.Time = data.NormalizeToIST(c.Time)
		candleMap[cNorm.Time.Unix()] = cNorm
	}

	merged := make([]data.Candle, 0, len(candleMap))
	for _, c := range candleMap {
		merged = append(merged, c)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Time.Before(merged[j].Time)
	})
	if len(merged) > 150 {
		merged = merged[len(merged)-150:]
	}
	e.rollingCandles[symbol] = merged
	e.logger.Info("Warmed up EMAS5 rolling buffer with historical candles",
		zap.String("symbol", symbol),
		zap.Int("new_candles", len(newCandles)),
		zap.Int("total_buffer_size", len(merged)),
	)
}

// CheckBreakout evaluates live ticks against the Confirmation Candle breakout level
func (e *EMAS5BreakoutEngine) CheckBreakout(symbol string, ltp float64, bias string) *Signal {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.tradeCountsPerStock[symbol] >= e.maxTradesPerStock {
		return nil
	}

	confirm := e.confirmationCandles[symbol]
	masterDir := e.masterDirections[symbol]
	if confirm == nil || masterDir == "" {
		return nil
	}

	if e.MinCandlesToIgnore > 0 {
		confirmTime := data.NormalizeToIST(confirm.Time)
		sessionDateStr := confirmTime.Format("2006-01-02")
		marketStart := time.Date(confirmTime.Year(), confirmTime.Month(), confirmTime.Day(), 9, 15, 0, 0, data.ISTLocation)
		todayCount := 0
		for _, c := range e.rollingCandles[symbol] {
			cTime := data.NormalizeToIST(c.Time)
			if cTime.Format("2006-01-02") == sessionDateStr && !cTime.Before(marketStart) {
				todayCount++
			}
		}
		if todayCount < e.MinCandlesToIgnore {
			return nil
		}
	}

	// 1. BUY Breakout Trigger
	if masterDir == "BUY" && ltp >= confirm.High {
		// Max Entry Distance / Freshness Guard:
		// Discard trigger if price has run up beyond maxEntryDistancePct (default 0.35%) above Confirmation High
		maxAllowedDistPct := e.maxEntryDistancePct
		if maxAllowedDistPct <= 0 {
			maxAllowedDistPct = 0.35
		}
		maxAllowedEntryPrice := confirm.High * (1.0 + maxAllowedDistPct/100.0)
		if ltp > maxAllowedEntryPrice {
			e.logger.Warn("[EMAS5_BREAKOUT] BUY entry skipped: LTP exceeds max entry distance beyond Confirmation High (Late breakout/startup chase guard)",
				zap.String("symbol", symbol),
				zap.Float64("ltp", ltp),
				zap.Float64("confirmation_high", confirm.High),
				zap.Float64("max_allowed_entry_price", maxAllowedEntryPrice),
				zap.Float64("max_entry_distance_pct", maxAllowedDistPct),
			)
			e.emitEvent(symbol, "TRADE_SKIPPED", "WARNING", "BUY",
				"EMAS5 BUY Skipped: Max Entry Distance Exceeded",
				fmt.Sprintf("Live tick ₹%.2f is %.2f%% above Confirmation High ₹%.2f (max allowed %.2f%%). Skipped late breakout.", ltp, (ltp-confirm.High)/confirm.High*100.0, confirm.High, maxAllowedDistPct),
				confirm, confirm.High, 0, 0,
				map[string]interface{}{"ltp": ltp, "confirmation_high": confirm.High, "max_allowed_entry": maxAllowedEntryPrice},
			)
			return nil
		}

		e.tradeCountsPerStock[symbol]++
		reason := fmt.Sprintf("EMAS5_BREAKOUT: Live tick ₹%.2f broke Confirmation High ₹%.2f (Trade %d/%d)",
			ltp, confirm.High, e.tradeCountsPerStock[symbol], e.maxTradesPerStock)

		e.logger.Info("Triggered EMAS5_BREAKOUT BUY Trade",
			zap.String("symbol", symbol),
			zap.Float64("ltp", ltp),
			zap.Float64("confirmation_high", confirm.High),
			zap.Float64("sl_anchor_low", confirm.Low),
			zap.Int("stock_trade_count", e.tradeCountsPerStock[symbol]),
		)
		e.emitEvent(symbol, "BREAKOUT_TRIGGER", "SUCCESS", "BUY",
			fmt.Sprintf("EMAS5 BUY Breakout Triggered at ₹%.2f", ltp),
			fmt.Sprintf("Live tick ₹%.2f crossed Confirmation High ₹%.2f (Trade %d/%d). Firing entry order.", ltp, confirm.High, e.tradeCountsPerStock[symbol], e.maxTradesPerStock),
			confirm, confirm.High, confirm.Low, 0,
			map[string]interface{}{"ltp": ltp, "trigger_high": confirm.High, "sl_anchor_low": confirm.Low, "trade_count": e.tradeCountsPerStock[symbol]},
		)

		// Preserve setup candle for risk management profile sizing & SL calculation
		e.lastSetupCandles[symbol] = &SetupCandle{
			Candle: *confirm,
			High:   confirm.High,
			Low:    confirm.Low,
			Volume: confirm.Volume,
		}

		// Re-arm active setup state for symbol so a subsequent trade can form if within limit
		e.resetSymbolSetup(symbol)

		return &Signal{
			Symbol:       symbol,
			Action:       "BUY",
			Strength:     1.0,
			Reason:       reason,
			Candle:       confirm,
			StrategyName: e.Name(),
		}
	}

	// 2. SELL Breakout Trigger
	if masterDir == "SELL" && ltp <= confirm.Low {
		// Max Entry Distance / Freshness Guard:
		// Discard trigger if price has fallen below maxEntryDistancePct (default 0.35%) under Confirmation Low
		maxAllowedDistPct := e.maxEntryDistancePct
		if maxAllowedDistPct <= 0 {
			maxAllowedDistPct = 0.35
		}
		minAllowedEntryPrice := confirm.Low * (1.0 - maxAllowedDistPct/100.0)
		if ltp < minAllowedEntryPrice {
			e.logger.Warn("[EMAS5_BREAKOUT] SELL entry skipped: LTP falls below max entry distance under Confirmation Low (Late breakdown/startup chase guard)",
				zap.String("symbol", symbol),
				zap.Float64("ltp", ltp),
				zap.Float64("confirmation_low", confirm.Low),
				zap.Float64("min_allowed_entry_price", minAllowedEntryPrice),
				zap.Float64("max_entry_distance_pct", maxAllowedDistPct),
			)
			e.emitEvent(symbol, "TRADE_SKIPPED", "WARNING", "SELL",
				"EMAS5 SELL Skipped: Max Entry Distance Exceeded",
				fmt.Sprintf("Live tick ₹%.2f is %.2f%% below Confirmation Low ₹%.2f (max allowed %.2f%%). Skipped late breakdown.", ltp, (confirm.Low-ltp)/confirm.Low*100.0, confirm.Low, maxAllowedDistPct),
				confirm, confirm.Low, 0, 0,
				map[string]interface{}{"ltp": ltp, "confirmation_low": confirm.Low, "min_allowed_entry": minAllowedEntryPrice},
			)
			return nil
		}

		e.tradeCountsPerStock[symbol]++
		reason := fmt.Sprintf("EMAS5_BREAKOUT: Live tick ₹%.2f broke Confirmation Low ₹%.2f (Trade %d/%d)",
			ltp, confirm.Low, e.tradeCountsPerStock[symbol], e.maxTradesPerStock)

		e.logger.Info("Triggered EMAS5_BREAKOUT SELL Trade",
			zap.String("symbol", symbol),
			zap.Float64("ltp", ltp),
			zap.Float64("confirmation_low", confirm.Low),
			zap.Float64("sl_anchor_high", confirm.High),
			zap.Int("stock_trade_count", e.tradeCountsPerStock[symbol]),
		)
		e.emitEvent(symbol, "BREAKOUT_TRIGGER", "SUCCESS", "SELL",
			fmt.Sprintf("EMAS5 SELL Breakdown Triggered at ₹%.2f", ltp),
			fmt.Sprintf("Live tick ₹%.2f crossed Confirmation Low ₹%.2f (Trade %d/%d). Firing entry order.", ltp, confirm.Low, e.tradeCountsPerStock[symbol], e.maxTradesPerStock),
			confirm, confirm.Low, confirm.High, 0,
			map[string]interface{}{"ltp": ltp, "trigger_low": confirm.Low, "sl_anchor_high": confirm.High, "trade_count": e.tradeCountsPerStock[symbol]},
		)

		// Preserve setup candle for risk management profile sizing & SL calculation
		e.lastSetupCandles[symbol] = &SetupCandle{
			Candle: *confirm,
			High:   confirm.High,
			Low:    confirm.Low,
			Volume: confirm.Volume,
		}

		// Re-arm active setup state for symbol so a subsequent trade can form if within limit
		e.resetSymbolSetup(symbol)

		return &Signal{
			Symbol:       symbol,
			Action:       "SELL",
			Strength:     1.0,
			Reason:       reason,
			Candle:       confirm,
			StrategyName: e.Name(),
		}
	}

	return nil
}

// GetSetupCandle returns the Confirmation Candle (for Stop-Loss anchoring and Risk Per Trade sizing)
func (e *EMAS5BreakoutEngine) GetSetupCandle(symbol string) *SetupCandle {
	e.mu.RLock()
	defer e.mu.RUnlock()

	setup := e.lastSetupCandles[symbol]
	if setup != nil {
		return setup
	}

	confirm := e.confirmationCandles[symbol]
	if confirm == nil {
		return nil
	}

	return &SetupCandle{
		Candle: *confirm,
		High:   confirm.High,
		Low:    confirm.Low,
		Volume: confirm.Volume,
	}
}

// resetSymbolSetup resets active setup state for a symbol without wiping trade count or lastSetupCandles
func (e *EMAS5BreakoutEngine) resetSymbolSetup(symbol string) {
	e.masterCandles[symbol] = nil
	delete(e.masterCandleIndices, symbol)
	delete(e.masterDirections, symbol)
	delete(e.insideCandleCounts, symbol)
	delete(e.confirmationCandles, symbol)
	delete(e.confirmationCandleIndices, symbol)
}

// Reset resets all engine state (called on daily market open)
func (e *EMAS5BreakoutEngine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.rollingCandles = make(map[string][]data.Candle)
	e.tradeCountsPerStock = make(map[string]int)
	e.masterCandles = make(map[string]*data.Candle)
	e.masterCandleIndices = make(map[string]int)
	e.masterDirections = make(map[string]string)
	e.insideCandleCounts = make(map[string]int)
	e.confirmationCandles = make(map[string]*data.Candle)
	e.confirmationCandleIndices = make(map[string]int)
	e.lastSetupCandles = make(map[string]*SetupCandle)
	e.firstCandles = make(map[string]*data.Candle)
	e.pdHighs = make(map[string]float64)
	e.pdLows = make(map[string]float64)
	e.pdCloses = make(map[string]float64)

	e.logger.Info("EMAS5_BREAKOUT strategy engine state reset successfully")
}

// RestoreTriggeredTrade restores completed trade state from database on bot restart
func (e *EMAS5BreakoutEngine) RestoreTriggeredTrade(symbol string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tradeCountsPerStock[symbol]++
	e.logger.Info("EMAS5_BREAKOUT: Restored triggered trade state",
		zap.String("symbol", symbol),
		zap.Int("trade_count", e.tradeCountsPerStock[symbol]),
	)
}
