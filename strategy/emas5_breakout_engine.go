package strategy

import (
	"fmt"
	"math"
	"sync"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// EMAS5BreakoutEngine implements the EMA S5 Breakout Strategy
type EMAS5BreakoutEngine struct {
	logger              *zap.Logger
	mu                  sync.RWMutex
	indicators          *Indicators
	pdHighs             map[string]float64
	pdLows              map[string]float64
	pdCloses            map[string]float64
	rollingCandles      map[string][]data.Candle
	tradeCountsPerStock map[string]int
	masterCandles       map[string]*data.Candle
	masterCandleIndices map[string]int
	masterDirections    map[string]string // "BUY" or "SELL"
	insideCandleCounts  map[string]int
	confirmationCandles map[string]*data.Candle
	lastSetupCandles    map[string]*SetupCandle
	firstCandles        map[string]*data.Candle

	// Configurable Parameters
	maxTradesPerStock  int     // Max trades per stock per day (default: 2)
	rallyCandlesCount  int     // Min consecutive rally candles (default: 5)
	lhBufferPct        float64 // LH / HL tolerance buffer % (default: 0.2%)
	minReboundPct      float64 // Min oval rebound / drop move % (default: 0.5%)
	masterMaxPct       float64 // Master candle max range % (default: 2.0%)
	maxInsideCandles   int     // Max inside candles allowed before confirmation (default: 1)
	confirmMaxPct      float64 // Confirmation candle max range % (default: 1.0%)
	tradeEndTime       string  // Cutoff time (default: "11:00:00")
	slBufferPct        float64 // SL buffer % (default: 0.1%)
	MinCandlesToIgnore int     // Min initial candles to ignore (default: 0)
	candleTimeFrame    string  // Candle interval (default: "1m")
}

// NewEMAS5BreakoutEngine creates a new instance of EMAS5BreakoutEngine
func NewEMAS5BreakoutEngine(
	logger *zap.Logger,
	maxTradesPerStock int,
	rallyCandlesCount int,
	lhBufferPct float64,
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
	if lhBufferPct <= 0 {
		lhBufferPct = 0.2
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
		logger:              logger,
		indicators:          &Indicators{logger: logger},
		pdHighs:             make(map[string]float64),
		pdLows:              make(map[string]float64),
		pdCloses:            make(map[string]float64),
		rollingCandles:      make(map[string][]data.Candle),
		tradeCountsPerStock: make(map[string]int),
		masterCandles:       make(map[string]*data.Candle),
		masterCandleIndices: make(map[string]int),
		masterDirections:    make(map[string]string),
		insideCandleCounts:  make(map[string]int),
		confirmationCandles: make(map[string]*data.Candle),
		lastSetupCandles:    make(map[string]*SetupCandle),
		firstCandles:        make(map[string]*data.Candle),
		maxTradesPerStock:   maxTradesPerStock,
		rallyCandlesCount:   rallyCandlesCount,
		lhBufferPct:         lhBufferPct,
		minReboundPct:       minReboundPct,
		masterMaxPct:        masterMaxPct,
		maxInsideCandles:    maxInsideCandles,
		confirmMaxPct:       confirmMaxPct,
		tradeEndTime:        "11:00:00",
		slBufferPct:         0.1,
		MinCandlesToIgnore:  0,
		candleTimeFrame:     "1m",
	}
}

// Name returns the strategy name
func (e *EMAS5BreakoutEngine) Name() string {
	return "EMAS5_BREAKOUT"
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

// UpdateRules dynamically updates strategy rules from UI settings
func (e *EMAS5BreakoutEngine) UpdateRules(
	maxTradesPerStock int,
	rallyCandlesCount int,
	lhBufferPct float64,
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
	if lhBufferPct > 0 {
		e.lhBufferPct = lhBufferPct
	}
	if minReboundPct > 0 {
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
		zap.Float64("lh_buffer_pct", e.lhBufferPct),
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

	// Append candle to rolling buffer (max 100 historical candles for EMA accuracy)
	candles := append(e.rollingCandles[symbol], candle)
	if len(candles) > 100 {
		candles = candles[len(candles)-100:]
	}
	e.rollingCandles[symbol] = candles
	candleCount := len(candles)

	// Anchor 09:15 AM first candle
	loc, _ := time.LoadLocation("Asia/Kolkata")
	if loc == nil {
		loc = time.Local
	}
	candleIST := candle.Time.In(loc)
	if candleIST.Hour() == 9 && candleIST.Minute() == 15 && e.firstCandles[symbol] == nil {
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
	// State 1: Active Setup Monitoring (Master Candle already formed)
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
				e.resetSymbolSetup(symbol)
				return
			}

			// Rule 2: Breakout of Master High -> Confirmation Candle Formation
			if candle.High > master.High {
				confirmRangePct := (candle.High - candle.Low) / candle.Close * 100.0
				if confirmRangePct > e.confirmMaxPct {
					e.logger.Info("Invalidated EMAS5 BUY confirmation: Range exceeds threshold",
						zap.String("symbol", symbol),
						zap.Float64("range_pct", confirmRangePct),
						zap.Float64("max_range_pct", e.confirmMaxPct),
					)
					e.resetSymbolSetup(symbol)
					return
				}

				cCopy := candle
				e.confirmationCandles[symbol] = &cCopy
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
				return
			}

			// Rule 3: Inside Candle Consolidation Count
			// Candle stayed inside Master range: High <= Master.High && Low >= Master.Low
			e.insideCandleCounts[symbol]++
			if e.insideCandleCounts[symbol] > e.maxInsideCandles {
				e.logger.Info("Invalidated EMAS5 BUY setup: Exceeded max inside candles limit",
					zap.String("symbol", symbol),
					zap.Int("inside_candles", e.insideCandleCounts[symbol]),
					zap.Int("max_allowed", e.maxInsideCandles),
				)
				e.resetSymbolSetup(symbol)
				return
			}
			return

		} else if masterDir == "SELL" {
			// Rule 1: Master High Invalidation Guard
			if candle.High > master.High {
				e.logger.Info("Invalidated EMAS5 SELL setup: Candle broke Master High",
					zap.String("symbol", symbol),
					zap.Float64("candle_high", candle.High),
					zap.Float64("master_high", master.High),
				)
				e.resetSymbolSetup(symbol)
				return
			}

			// Rule 2: Breakdown of Master Low -> Confirmation Candle Formation
			if candle.Low < master.Low {
				confirmRangePct := (candle.High - candle.Low) / candle.Close * 100.0
				if confirmRangePct > e.confirmMaxPct {
					e.logger.Info("Invalidated EMAS5 SELL confirmation: Range exceeds threshold",
						zap.String("symbol", symbol),
						zap.Float64("range_pct", confirmRangePct),
						zap.Float64("max_range_pct", e.confirmMaxPct),
					)
					e.resetSymbolSetup(symbol)
					return
				}

				cCopy := candle
				e.confirmationCandles[symbol] = &cCopy
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
				return
			}

			// Rule 3: Inside Candle Consolidation Count
			e.insideCandleCounts[symbol]++
			if e.insideCandleCounts[symbol] > e.maxInsideCandles {
				e.logger.Info("Invalidated EMAS5 SELL setup: Exceeded max inside candles limit",
					zap.String("symbol", symbol),
					zap.Int("inside_candles", e.insideCandleCounts[symbol]),
					zap.Int("max_allowed", e.maxInsideCandles),
				)
				e.resetSymbolSetup(symbol)
				return
			}
			return
		}
	}

	// -------------------------------------------------------------
	// State 2: Scan for New Master Candle Formation
	// -------------------------------------------------------------
	if master == nil {
		if candleCount < e.rallyCandlesCount+1 {
			return // Need at least N rally candles + current candidate candle
		}

		pdh := e.pdHighs[symbol]
		pdl := e.pdLows[symbol]
		masterRangePct := (candle.High - candle.Low) / candle.Close * 100.0

		// Check candidate Master range cap (<= 2.0%)
		if masterRangePct > e.masterMaxPct {
			return
		}

		// -----------------------------
		// A. Test BUY Master Candidate
		// -----------------------------
		if candle.Close > candle.Open { // Must be GREEN
			// Touch condition: Low <= Level <= High (or touches/dips below Level)
			touchesLevel := false
			levelsToTouch := []float64{currentEMA10, currentEMA20}
			if pdh > 0 {
				levelsToTouch = append(levelsToTouch, pdh)
			}
			if pdl > 0 {
				levelsToTouch = append(levelsToTouch, pdl)
			}

			for _, lvl := range levelsToTouch {
				if candle.Low <= lvl && candle.High >= lvl {
					touchesLevel = true
					break
				}
				if candle.Low <= lvl && candle.Close >= lvl {
					touchesLevel = true
					break
				}
			}

			// Close condition: Must close above ALL 3 key levels (EMA 10, EMA 20, and PDH/PDL reference)
			closesAboveAll := candle.Close > currentEMA10 && candle.Close > currentEMA20
			if pdh > 0 && candle.Close <= pdh && (candle.Low <= pdh || candle.High >= pdh) {
				closesAboveAll = false
			}

			if touchesLevel && closesAboveAll {
				// Validate preceding Rally Sequence (N candles with Higher Lows)
				rallyValid := true
				lowestLow := math.MaxFloat64
				startIdx := candleCount - 1 - e.rallyCandlesCount
				if startIdx < 0 {
					startIdx = 0
				}

				for i := startIdx + 1; i < candleCount-1; i++ {
					prevLow := candles[i-1].Low
					currLow := candles[i].Low
					// Allow 0.2% buffer on Higher Lows
					if currLow < prevLow*(1.0-e.lhBufferPct/100.0) {
						rallyValid = false
						break
					}
				}

				if rallyValid {
					for i := startIdx; i < candleCount; i++ {
						if candles[i].Low < lowestLow {
							lowestLow = candles[i].Low
						}
					}

					// Validate Oval / Upward Curvature Rebound Move (>= 0.5%)
					reboundPct := (candle.Close - lowestLow) / lowestLow * 100.0
					if reboundPct >= e.minReboundPct {
						cCopy := candle
						e.masterCandles[symbol] = &cCopy
						e.masterCandleIndices[symbol] = candleCount - 1
						e.masterDirections[symbol] = "BUY"
						e.insideCandleCounts[symbol] = 0
						e.confirmationCandles[symbol] = nil

						e.logger.Info("Established Master Candle (EMAS5_BREAKOUT BUY)",
							zap.String("symbol", symbol),
							zap.Float64("master_high", candle.High),
							zap.Float64("master_low", candle.Low),
							zap.Float64("rebound_pct", reboundPct),
							zap.Float64("ema10", currentEMA10),
							zap.Float64("ema20", currentEMA20),
						)
						return
					}
				}
			}
		}

		// ------------------------------
		// B. Test SELL Master Candidate
		// ------------------------------
		if candle.Close < candle.Open { // Must be RED
			touchesLevel := false
			levelsToTouch := []float64{currentEMA10, currentEMA20}
			if pdl > 0 {
				levelsToTouch = append(levelsToTouch, pdl)
			}
			if pdh > 0 {
				levelsToTouch = append(levelsToTouch, pdh)
			}

			for _, lvl := range levelsToTouch {
				if candle.Low <= lvl && candle.High >= lvl {
					touchesLevel = true
					break
				}
				if candle.High >= lvl && candle.Close <= lvl {
					touchesLevel = true
					break
				}
			}

			// Close condition: Must close below ALL 3 key levels (EMA 10, EMA 20, and PDL/PDH reference)
			closesBelowAll := candle.Close < currentEMA10 && candle.Close < currentEMA20
			if pdl > 0 && candle.Close >= pdl && (candle.Low <= pdl || candle.High >= pdl) {
				closesBelowAll = false
			}

			if touchesLevel && closesBelowAll {
				// Validate preceding Drop Sequence (N candles with Lower Highs)
				dropValid := true
				highestHigh := 0.0
				startIdx := candleCount - 1 - e.rallyCandlesCount
				if startIdx < 0 {
					startIdx = 0
				}

				for i := startIdx + 1; i < candleCount-1; i++ {
					prevHigh := candles[i-1].High
					currHigh := candles[i].High
					// Allow 0.2% buffer on Lower Highs
					if currHigh > prevHigh*(1.0+e.lhBufferPct/100.0) {
						dropValid = false
						break
					}
				}

				if dropValid {
					for i := startIdx; i < candleCount; i++ {
						if candles[i].High > highestHigh {
							highestHigh = candles[i].High
						}
					}

					// Validate Inverted Oval / Downward Drop Move (>= 0.5%)
					dropPct := (highestHigh - candle.Close) / highestHigh * 100.0
					if dropPct >= e.minReboundPct {
						cCopy := candle
						e.masterCandles[symbol] = &cCopy
						e.masterCandleIndices[symbol] = candleCount - 1
						e.masterDirections[symbol] = "SELL"
						e.insideCandleCounts[symbol] = 0
						e.confirmationCandles[symbol] = nil

						e.logger.Info("Established Master Candle (EMAS5_BREAKOUT SELL)",
							zap.String("symbol", symbol),
							zap.Float64("master_high", candle.High),
							zap.Float64("master_low", candle.Low),
							zap.Float64("drop_pct", dropPct),
							zap.Float64("ema10", currentEMA10),
							zap.Float64("ema20", currentEMA20),
						)
						return
					}
				}
			}
		}
	}
}

// OnCandleClose processes completed candles (Strategy interface)
func (e *EMAS5BreakoutEngine) OnCandleClose(candle *data.Candle, symbol string) {
	if candle != nil {
		e.ProcessCandle(symbol, *candle)
	}
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

	// 1. BUY Breakout Trigger
	if masterDir == "BUY" && ltp >= confirm.High {
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

// EMAS5SymbolState holds snapshot data for UI candidate monitoring
type EMAS5SymbolState struct {
	Symbol             string  `json:"symbol"`
	CandleCount        int     `json:"candle_count"`
	EMA10              float64 `json:"ema10"`
	EMA20              float64 `json:"ema20"`
	PDH                float64 `json:"pdh"`
	PDL                float64 `json:"pdl"`
	MasterStatus       string  `json:"master_status"` // "NONE", "BUY", "SELL"
	MasterHigh         float64 `json:"master_high"`
	MasterLow          float64 `json:"master_low"`
	InsideCount        int     `json:"inside_count"`
	ConfirmationStatus string  `json:"confirmation_status"` // "NONE", "BUY", "SELL"
	ConfirmationHigh   float64 `json:"confirmation_high"`
	ConfirmationLow    float64 `json:"confirmation_low"`
	TradesToday        int     `json:"trades_today"`
	MaxTrades          int     `json:"max_trades"`
}

// GetSymbolState returns the thread-safe monitoring state for a symbol
func (e *EMAS5BreakoutEngine) GetSymbolState(symbol string) EMAS5SymbolState {
	e.mu.RLock()
	defer e.mu.RUnlock()

	state := EMAS5SymbolState{
		Symbol:             symbol,
		TradesToday:        e.tradeCountsPerStock[symbol],
		MaxTrades:          e.maxTradesPerStock,
		PDH:                e.pdHighs[symbol],
		PDL:                e.pdLows[symbol],
		MasterStatus:       "NONE",
		ConfirmationStatus: "NONE",
	}

	candles := e.rollingCandles[symbol]
	state.CandleCount = len(candles)
	if len(candles) > 0 {
		closes := make([]float64, len(candles))
		for i, c := range candles {
			closes[i] = c.Close
		}
		ema10 := e.indicators.CalculateEMA(closes, 10)
		ema20 := e.indicators.CalculateEMA(closes, 20)
		if len(ema10) > 0 {
			state.EMA10 = ema10[len(ema10)-1]
		}
		if len(ema20) > 0 {
			state.EMA20 = ema20[len(ema20)-1]
		}
	}

	if master := e.masterCandles[symbol]; master != nil {
		state.MasterStatus = e.masterDirections[symbol]
		state.MasterHigh = master.High
		state.MasterLow = master.Low
		state.InsideCount = e.insideCandleCounts[symbol]
	}

	if confirm := e.confirmationCandles[symbol]; confirm != nil {
		state.ConfirmationStatus = e.masterDirections[symbol]
		state.ConfirmationHigh = confirm.High
		state.ConfirmationLow = confirm.Low
	}

	return state
}

// GetAllSymbolStates returns states for all symbols currently tracked
func (e *EMAS5BreakoutEngine) GetAllSymbolStates() map[string]EMAS5SymbolState {
	e.mu.RLock()
	defer e.mu.RUnlock()

	results := make(map[string]EMAS5SymbolState)
	for symbol := range e.rollingCandles {
		state := EMAS5SymbolState{
			Symbol:             symbol,
			TradesToday:        e.tradeCountsPerStock[symbol],
			MaxTrades:          e.maxTradesPerStock,
			PDH:                e.pdHighs[symbol],
			PDL:                e.pdLows[symbol],
			MasterStatus:       "NONE",
			ConfirmationStatus: "NONE",
		}

		candles := e.rollingCandles[symbol]
		state.CandleCount = len(candles)
		if len(candles) > 0 {
			closes := make([]float64, len(candles))
			for i, c := range candles {
				closes[i] = c.Close
			}
			ema10 := e.indicators.CalculateEMA(closes, 10)
			ema20 := e.indicators.CalculateEMA(closes, 20)
			if len(ema10) > 0 {
				state.EMA10 = ema10[len(ema10)-1]
			}
			if len(ema20) > 0 {
				state.EMA20 = ema20[len(ema20)-1]
			}
		}

		if master := e.masterCandles[symbol]; master != nil {
			state.MasterStatus = e.masterDirections[symbol]
			state.MasterHigh = master.High
			state.MasterLow = master.Low
			state.InsideCount = e.insideCandleCounts[symbol]
		}

		if confirm := e.confirmationCandles[symbol]; confirm != nil {
			state.ConfirmationStatus = e.masterDirections[symbol]
			state.ConfirmationHigh = confirm.High
			state.ConfirmationLow = confirm.Low
		}

		results[symbol] = state
	}
	return results
}
