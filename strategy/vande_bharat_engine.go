package strategy

import (
	"fmt"
	"math"
	"sync"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// VandeBharatEngine implements the refined Previous Day High/Low Breakout strategy
type VandeBharatEngine struct {
	logger               *zap.Logger
	mu                   sync.RWMutex
	pdHighs              map[string]float64
	pdLows               map[string]float64
	pdCloses             map[string]float64 // Yesterday's Close Price
	masterCandles        map[string]*data.Candle
	secondCandles        map[string]*data.Candle // 2nd candle of day (09:20 AM IST) for SL anchor (Rule 1 & 2)
	confirmationCandles  map[string]*data.Candle // Confirmation candle if Candle 2 broke Master High/Low (Rule 1)
	breakoutTriggerLevel map[string]float64      // Level to break: Candle 2 High (Rule 1) or Master High (Rule 2)
	slAnchorPrices       map[string]float64      // SL Anchor price: Candle 2 Low for BUY, Candle 2 High for SELL
	firstCandles         map[string]*data.Candle // 1st candle of day (09:15 AM IST)
	triggeredTrades      map[string]bool
	rollingCandles       map[string][]data.Candle
	masterMaxPct         float64 // Master Candle Max Range (%) - also bounds entry price move from PDH/PDL
	slMinPct             float64 // 2nd Candle (SL) Min Range (%)
	slMaxPct             float64 // 2nd Candle (SL) Max Range (%)
	masterMaxWickPct     float64
	minGapPct            float64 // Min opening gap % from Yesterday's Close (default: 0% or configured)
	MinCandlesToIgnore   int
	candleTimeFrame      string
}

// NewVandeBharatEngine creates a new instance of VandeBharatEngine
func NewVandeBharatEngine(logger *zap.Logger, masterMaxPct, slMinPct, slMaxPct, masterMaxWickPct, minGapPct float64) *VandeBharatEngine {
	if minGapPct < 0 {
		minGapPct = 0.0
	}
	if slMinPct <= 0 {
		slMinPct = 0.05
	}
	if slMaxPct <= 0 {
		slMaxPct = 1.0
	}
	if masterMaxPct <= 0 {
		masterMaxPct = 3.0
	}
	if masterMaxWickPct <= 0 {
		masterMaxWickPct = 60.0
	}
	return &VandeBharatEngine{
		logger:               logger,
		pdHighs:              make(map[string]float64),
		pdLows:               make(map[string]float64),
		pdCloses:             make(map[string]float64),
		masterCandles:        make(map[string]*data.Candle),
		secondCandles:        make(map[string]*data.Candle),
		confirmationCandles:  make(map[string]*data.Candle),
		breakoutTriggerLevel: make(map[string]float64),
		slAnchorPrices:       make(map[string]float64),
		firstCandles:         make(map[string]*data.Candle),
		triggeredTrades:      make(map[string]bool),
		rollingCandles:       make(map[string][]data.Candle),
		masterMaxPct:         masterMaxPct,
		slMinPct:             slMinPct,
		slMaxPct:             slMaxPct,
		masterMaxWickPct:     masterMaxWickPct,
		minGapPct:            minGapPct,
		MinCandlesToIgnore:   0,
		candleTimeFrame:      "1m",
	}
}

// Name returns the strategy name
func (e *VandeBharatEngine) Name() string {
	return "VANDE_BHARAT"
}

// CandleTimeFrame returns the configured candle interval (e.g. "1m", "5m")
func (e *VandeBharatEngine) CandleTimeFrame() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.candleTimeFrame == "" {
		return "5m"
	}
	return e.candleTimeFrame
}

// SetCandleTimeFrame sets the strategy candle interval (e.g. "1m", "5m")
func (e *VandeBharatEngine) SetCandleTimeFrame(tf string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if tf == "" {
		tf = "5m"
	}
	e.candleTimeFrame = tf
	e.logger.Info("Updated strategy candle timeframe",
		zap.String("strategy", "VANDE_BHARAT"),
		zap.String("timeframe", tf),
	)
}

// UpdateRules dynamically updates the strategy rule thresholds in memory
func (e *VandeBharatEngine) UpdateRules(masterMaxPct, slMinPct, slMaxPct, masterMaxWickPct, minGapPct float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if masterMaxPct > 0 {
		e.masterMaxPct = masterMaxPct
	}
	if slMinPct > 0 {
		e.slMinPct = slMinPct
	}
	if slMaxPct > 0 {
		e.slMaxPct = slMaxPct
	}
	if masterMaxWickPct > 0 {
		e.masterMaxWickPct = masterMaxWickPct
	}
	if minGapPct >= 0 {
		e.minGapPct = minGapPct
	}
	e.logger.Info("Vande Bharat strategy rules dynamically updated",
		zap.Float64("master_max_pct", e.masterMaxPct),
		zap.Float64("sl_min_pct", e.slMinPct),
		zap.Float64("sl_max_pct", e.slMaxPct),
		zap.Float64("master_max_wick_pct", e.masterMaxWickPct),
		zap.Float64("min_gap_pct", e.minGapPct),
	)
}

// SetPreviousDayHighLow binds the reference PDH and PDL levels for a symbol
func (e *VandeBharatEngine) SetPreviousDayHighLow(symbol string, high float64, low float64) {
	e.SetPreviousDayLevels(symbol, high, low, (high+low)/2.0)
}

// SetPreviousDayLevels binds the reference PDH, PDL, and Yesterday's Close price for a symbol
func (e *VandeBharatEngine) SetPreviousDayLevels(symbol string, high float64, low float64, closeVal float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pdHighs[symbol] = high
	e.pdLows[symbol] = low
	if closeVal > 0 {
		e.pdCloses[symbol] = closeVal
	} else {
		e.pdCloses[symbol] = (high + low) / 2.0
	}
	e.logger.Info("Vande Bharat reference levels configured",
		zap.String("symbol", symbol),
		zap.Float64("pdh", high),
		zap.Float64("pdl", low),
		zap.Float64("pd_close", e.pdCloses[symbol]),
	)
}

// OnCandleClose processes incoming completed candles to detect Master & SL Anchor/Confirmation candles
func (e *VandeBharatEngine) OnCandleClose(candle *data.Candle, symbol string) {
	candleTimeIST := candle.Time.In(data.ISTLocation)
	marketStart := time.Date(candleTimeIST.Year(), candleTimeIST.Month(), candleTimeIST.Day(), 9, 15, 0, 0, data.ISTLocation)
	if candleTimeIST.Before(marketStart) && candleTimeIST.Hour() < 9 {
		return // Discard pre-market candles before 09:15 AM IST
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.rollingCandles[symbol] = append(e.rollingCandles[symbol], *candle)
	candles := e.rollingCandles[symbol]
	candleCount := len(candles)

	pdh, okHigh := e.pdHighs[symbol]
	pdl, okLow := e.pdLows[symbol]
	if !okHigh || !okLow || pdh <= 0 || pdl <= 0 {
		return // Reference levels not set for this symbol
	}

	// 1. Record 1st candle of the day (09:15 AM IST only) - Master Candle
	if candleTimeIST.Hour() == 9 && candleTimeIST.Minute() == 15 {
		e.firstCandles[symbol] = candle

		pdClose := e.pdCloses[symbol]
		if pdClose <= 0 {
			pdClose = (pdh + pdl) / 2.0
		}

		// Opening gap calculation relative to Yesterday's Close Price (pdClose)
		gapBuyPct := ((candle.Open - pdClose) / pdClose) * 100.0
		gapSellPct := ((pdClose - candle.Open) / pdClose) * 100.0

		isMasterBuy := candle.Close > pdh && candle.Close > candle.Open && gapBuyPct >= e.minGapPct
		isMasterSell := candle.Close < pdl && candle.Close < candle.Open && gapSellPct >= e.minGapPct

		if isMasterBuy || isMasterSell {
			candleRange := candle.High - candle.Low
			bodySize := math.Abs(candle.Close - candle.Open)
			wickSize := candleRange - bodySize

			allowedRange := (e.masterMaxPct / 100.0) * candle.Close

			maxWickRatio := e.masterMaxWickPct / 100.0
			if maxWickRatio <= 0 {
				maxWickRatio = 0.60
			}
			validWick := candleRange > 0 && (wickSize/candleRange) <= maxWickRatio+1e-5

			if candleRange <= allowedRange && validWick {
				e.masterCandles[symbol] = candle
				direction := "BUY"
				refLevel := pdh
				gapUsed := gapBuyPct
				if isMasterSell {
					direction = "SELL"
					refLevel = pdl
					gapUsed = gapSellPct
				}
				e.logger.Info("Established Master Candle (1st candle 09:15 AM, VANDE_BHARAT)",
					zap.String("symbol", symbol),
					zap.String("direction", direction),
					zap.Float64("open", candle.Open),
					zap.Float64("close", candle.Close),
					zap.Float64("ref_level", refLevel),
					zap.Float64("pd_close", pdClose),
					zap.Float64("gap_pct", gapUsed),
					zap.Float64("range_pct", (candleRange/candle.Close)*100.0),
					zap.Float64("wick_pct", (wickSize/candleRange)*100.0),
				)
			} else {
				e.logger.Warn("1st Candle (09:15 AM) failed Master criteria (range or max wick rule), no Master set today",
					zap.String("symbol", symbol),
					zap.Float64("range_pct", (candleRange/candle.Close)*100.0),
					zap.Float64("wick_pct", (wickSize/candleRange)*100.0),
				)
			}
		} else {
			if candle.Close > pdh && gapBuyPct < e.minGapPct {
				e.logger.Warn("1st Candle (09:15 AM) failed BUY gap-up criteria from Yesterday's Close",
					zap.String("symbol", symbol),
					zap.Float64("open", candle.Open),
					zap.Float64("pd_close", pdClose),
					zap.Float64("pdh", pdh),
					zap.Float64("gap_pct", gapBuyPct),
					zap.Float64("min_gap_pct", e.minGapPct),
				)
			} else if candle.Close < pdl && gapSellPct < e.minGapPct {
				e.logger.Warn("1st Candle (09:15 AM) failed SELL gap-down criteria from Yesterday's Close",
					zap.String("symbol", symbol),
					zap.Float64("open", candle.Open),
					zap.Float64("pd_close", pdClose),
					zap.Float64("pdl", pdl),
					zap.Float64("gap_pct", gapSellPct),
					zap.Float64("min_gap_pct", e.minGapPct),
				)
			}
		}
		return
	}

	// 2. Record 2nd candle of the day (09:20 AM IST) - SL Anchor & Confirmation Setup
	if candleCount == 2 && e.masterCandles[symbol] != nil {
		master := e.masterCandles[symbol]
		isBuySetup := master.Close > pdh

		secondRange := candle.High - candle.Low
		secondRangePct := (secondRange / candle.Close) * 100.0

		minSL := e.slMinPct
		if minSL <= 0 {
			minSL = 0.05
		}
		maxSL := e.slMaxPct
		if maxSL <= 0 {
			maxSL = 1.0
		}

		if secondRangePct < minSL || secondRangePct > maxSL {
			e.logger.Warn("2nd Candle failed SL range criteria (too wide or too narrow), setup invalidated",
				zap.String("symbol", symbol),
				zap.Float64("range_pct", secondRangePct),
				zap.Float64("min_sl_pct", minSL),
				zap.Float64("max_sl_pct", maxSL),
			)
			e.masterCandles[symbol] = nil
			return
		}

		// Store 2nd candle as SL Anchor
		e.secondCandles[symbol] = candle

		if isBuySetup {
			// Invalidation: 2nd candle breached Master Low
			if candle.Low < master.Low {
				e.logger.Warn("2nd candle breached Master Low, BUY setup invalidated",
					zap.String("symbol", symbol),
					zap.Float64("master_low", master.Low),
					zap.Float64("candle_low", candle.Low),
				)
				e.masterCandles[symbol] = nil
				e.secondCandles[symbol] = nil
				return
			}

			// SL Anchor is locked to Candle 2 Low
			e.slAnchorPrices[symbol] = candle.Low

			// Rule 1: Candle 2 breaks Master High -> Candle 2 is Confirmation Candle
			if candle.High > master.High {
				e.confirmationCandles[symbol] = candle
				e.breakoutTriggerLevel[symbol] = candle.High
				e.logger.Info("Rule 1: Candle 2 broke Master High -> Confirmation Candle set (Trigger @ Candle 2 High)",
					zap.String("symbol", symbol),
					zap.Float64("confirmation_high", candle.High),
					zap.Float64("sl_anchor_low", candle.Low),
					zap.Float64("master_high", master.High),
				)
			} else {
				// Rule 2: Candle 2 did NOT break Master High -> Trigger @ Master High directly
				e.confirmationCandles[symbol] = nil
				e.breakoutTriggerLevel[symbol] = master.High
				e.logger.Info("Rule 2: Candle 2 inside Master range -> SL Anchor set (Trigger @ Master High)",
					zap.String("symbol", symbol),
					zap.Float64("master_high", master.High),
					zap.Float64("sl_anchor_low", candle.Low),
				)
			}
		} else {
			// SELL Setup (master.Close < pdl)
			// Invalidation: 2nd candle breached Master High
			if candle.High > master.High {
				e.logger.Warn("2nd candle breached Master High, SELL setup invalidated",
					zap.String("symbol", symbol),
					zap.Float64("master_high", master.High),
					zap.Float64("candle_high", candle.High),
				)
				e.masterCandles[symbol] = nil
				e.secondCandles[symbol] = nil
				return
			}

			// SL Anchor is locked to Candle 2 High
			e.slAnchorPrices[symbol] = candle.High

			// Rule 1: Candle 2 breaks Master Low -> Candle 2 is Confirmation Candle
			if candle.Low < master.Low {
				e.confirmationCandles[symbol] = candle
				e.breakoutTriggerLevel[symbol] = candle.Low
				e.logger.Info("Rule 1: Candle 2 broke Master Low -> Confirmation Candle set (Trigger @ Candle 2 Low)",
					zap.String("symbol", symbol),
					zap.Float64("confirmation_low", candle.Low),
					zap.Float64("sl_anchor_high", candle.High),
					zap.Float64("master_low", master.Low),
				)
			} else {
				// Rule 2: Candle 2 did NOT break Master Low -> Trigger @ Master Low directly
				e.confirmationCandles[symbol] = nil
				e.breakoutTriggerLevel[symbol] = master.Low
				e.logger.Info("Rule 2: Candle 2 inside Master range -> SL Anchor set (Trigger @ Master Low)",
					zap.String("symbol", symbol),
					zap.Float64("master_low", master.Low),
					zap.Float64("sl_anchor_high", candle.High),
				)
			}
		}
		return
	}

	// 3. Rule 3: Single-Candle Execution Window & Expiration Guard
	// The trade MUST be initiated in the execution candle (Candle 3 / 09:25–09:30).
	// When Candle 3 closes (candleCount >= 3), if no trade was taken, setup expires immediately!
	if candleCount >= 3 && e.masterCandles[symbol] != nil {
		if !e.triggeredTrades[symbol] {
			e.logger.Info("Rule 3: Breakout candle closed without trade execution -> Vande Bharat setup expired",
				zap.String("symbol", symbol),
				zap.Int("candle_count", candleCount),
				zap.Time("candle_time", candleTimeIST),
			)
		}
		// Clear setup state to prevent late entries on subsequent candles (Candle 4, 5, 6, 7+)
		e.masterCandles[symbol] = nil
		e.secondCandles[symbol] = nil
		e.confirmationCandles[symbol] = nil
		delete(e.breakoutTriggerLevel, symbol)
		delete(e.slAnchorPrices, symbol)
	}
}

// CheckBreakout checks if live LTP triggers breakout entry during the active 3rd candle window
func (e *VandeBharatEngine) CheckBreakout(symbol string, ltp float64, bias string) *Signal {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.triggeredTrades[symbol] {
		return nil
	}

	candles := e.rollingCandles[symbol]
	// Breakout execution is strictly valid during the 3rd candle (when 2 candles are completed: 09:15 and 09:20)
	if len(candles) != 2 {
		return nil
	}

	master := e.masterCandles[symbol]
	second := e.secondCandles[symbol]
	if master == nil || second == nil {
		return nil
	}

	triggerLevel, hasTrigger := e.breakoutTriggerLevel[symbol]
	slPrice, hasSL := e.slAnchorPrices[symbol]
	if !hasTrigger || !hasSL || triggerLevel <= 0 || slPrice <= 0 {
		return nil
	}

	pdh := e.pdHighs[symbol]
	pdl := e.pdLows[symbol]
	isMasterBuy := master.Close > pdh

	// Price move from PDH (for BUY) or PDL (for SELL) must be <= masterMaxPct
	maxAllowedMove := e.masterMaxPct
	if maxAllowedMove <= 0 {
		maxAllowedMove = 3.0
	}

	if isMasterBuy {
		moveFromPDH := ((ltp - pdh) / pdh) * 100.0
		if moveFromPDH > maxAllowedMove {
			e.logger.Warn("Vande Bharat BUY entry skipped: price move from PDH exceeds Master Max Range limit",
				zap.String("symbol", symbol),
				zap.Float64("move_from_pdh_pct", moveFromPDH),
				zap.Float64("max_allowed_move", maxAllowedMove),
				zap.Float64("ltp", ltp),
				zap.Float64("pdh", pdh),
			)
			return nil
		}

		// Check if LTP breaks the trigger level (Candle 2 High for Rule 1, or Master High for Rule 2)
		if ltp > triggerLevel {
			e.triggeredTrades[symbol] = true
			ruleDesc := "Master High"
			if e.confirmationCandles[symbol] != nil {
				ruleDesc = "Confirmation High (Candle 2)"
			}

			return &Signal{
				Symbol:       symbol,
				Action:       "BUY",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %.2f broke above Vande Bharat %s %.2f in 3rd Candle (SL: %.2f, Move: %.2f%% <= %.2f%%)", ltp, ruleDesc, triggerLevel, slPrice, moveFromPDH, maxAllowedMove),
				Candle:       second,
				StrategyName: e.Name(),
			}
		}
	} else {
		// SELL Setup (master.Close < pdl)
		moveFromPDL := ((pdl - ltp) / pdl) * 100.0
		if moveFromPDL > maxAllowedMove {
			e.logger.Warn("Vande Bharat SELL entry skipped: price move from PDL exceeds Master Max Range limit",
				zap.String("symbol", symbol),
				zap.Float64("move_from_pdl_pct", moveFromPDL),
				zap.Float64("max_allowed_move", maxAllowedMove),
				zap.Float64("ltp", ltp),
				zap.Float64("pdl", pdl),
			)
			return nil
		}

		// Check if LTP breaks the trigger level (Candle 2 Low for Rule 1, or Master Low for Rule 2)
		if ltp < triggerLevel {
			e.triggeredTrades[symbol] = true
			ruleDesc := "Master Low"
			if e.confirmationCandles[symbol] != nil {
				ruleDesc = "Confirmation Low (Candle 2)"
			}

			return &Signal{
				Symbol:       symbol,
				Action:       "SELL",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %.2f broke below Vande Bharat %s %.2f in 3rd Candle (SL: %.2f, Move: %.2f%% <= %.2f%%)", ltp, ruleDesc, triggerLevel, slPrice, moveFromPDL, maxAllowedMove),
				Candle:       second,
				StrategyName: e.Name(),
			}
		}
	}

	return nil
}

// GetSetupCandle returns the risk anchor (trigger price and SL anchor price) to compute Stop-Loss and targets
func (e *VandeBharatEngine) GetSetupCandle(symbol string) *SetupCandle {
	e.mu.RLock()
	defer e.mu.RUnlock()

	second := e.secondCandles[symbol]
	master := e.masterCandles[symbol]
	if second == nil && master == nil {
		return nil
	}

	triggerLevel, hasTrigger := e.breakoutTriggerLevel[symbol]
	slPrice, hasSL := e.slAnchorPrices[symbol]

	refCandle := second
	if refCandle == nil {
		refCandle = master
	}

	if hasTrigger && hasSL {
		pdh := e.pdHighs[symbol]
		isBuy := (master != nil && master.Close > pdh)
		highVal := triggerLevel
		lowVal := slPrice
		if !isBuy {
			highVal = slPrice
			lowVal = triggerLevel
		}
		return &SetupCandle{
			Candle: *refCandle,
			High:   highVal,
			Low:    lowVal,
			Volume: refCandle.Volume,
		}
	}

	if second != nil {
		return &SetupCandle{
			Candle: *second,
			High:   second.High,
			Low:    second.Low,
			Volume: second.Volume,
		}
	}

	return &SetupCandle{
		Candle: *master,
		High:   master.High,
		Low:    master.Low,
		Volume: master.Volume,
	}
}

// Reset clears the strategy engine state for a new day
func (e *VandeBharatEngine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.rollingCandles = make(map[string][]data.Candle)
	e.pdHighs = make(map[string]float64)
	e.pdLows = make(map[string]float64)
	e.pdCloses = make(map[string]float64)
	e.masterCandles = make(map[string]*data.Candle)
	e.secondCandles = make(map[string]*data.Candle)
	e.confirmationCandles = make(map[string]*data.Candle)
	e.breakoutTriggerLevel = make(map[string]float64)
	e.slAnchorPrices = make(map[string]float64)
	e.firstCandles = make(map[string]*data.Candle)
	e.triggeredTrades = make(map[string]bool)
	e.logger.Info("VANDE_BHARAT strategy engine state reset successfully")
}

// RestoreTriggeredTrade registers an already triggered trade for a symbol
func (e *VandeBharatEngine) RestoreTriggeredTrade(symbol string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.triggeredTrades[symbol] = true
	e.logger.Info("VANDE_BHARAT: Restored triggered trade state", zap.String("symbol", symbol))
}
