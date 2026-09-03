package strategy

import (
	"fmt"
	"math"
	"sync"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// VandeBharatTrapEngine implements the Vande Bharat Trap (Fake Master) Strategy
type VandeBharatTrapEngine struct {
	logger              *zap.Logger
	mu                  sync.RWMutex
	pdHighs              map[string]float64
	pdLows               map[string]float64
	pdCloses             map[string]float64      // Yesterday's Close Price
	fakeMasterCandles    map[string]*data.Candle // 1st candle of day (09:15 AM IST) opposite color trap
	masterCandles        map[string]*data.Candle // Established when Fake Master High (BUY) or Low (SELL) is broken
	masterCandleIndices  map[string]int          // Index in rollingCandles when master candle formed
	secondCandles        map[string]*data.Candle // Candle immediately following Master Candle (SL Anchor)
	confirmationCandles  map[string]*data.Candle // Confirmation candle (Candle 2 if broke Master extreme)
	breakoutTriggerLevel map[string]float64      // Active breakout trigger level (Candle 2 High/Low or Master High/Low)
	slAnchorPrices       map[string]float64      // Fixed SL Anchor price (Candle 2 Low for BUY, Candle 2 High for SELL)
	firstCandles         map[string]*data.Candle // 1st candle of day (09:15 AM IST)
	triggeredTrades      map[string]bool
	rollingCandles       map[string][]data.Candle
	fakeMasterMaxPct     float64 // Max candle range % for 1st Fake Master candle (default: 3.0%)
	masterMaxPct         float64 // Master Candle Max Range (%) - also bounds entry price move from PDH/PDL (default: 1.8%)
	slMinPct             float64 // 2nd Candle (SL) Min Range (%) (default: 0.5%)
	slMaxPct             float64 // 2nd Candle (SL) Max Range (%) (default: 1.0%)
	masterMaxWickPct     float64 // Max total upper + lower wick % (default: 40.0%)
	MinCandlesToIgnore   int
	candleTimeFrame      string
}

// NewVandeBharatTrapEngine creates a new instance of VandeBharatTrapEngine
func NewVandeBharatTrapEngine(logger *zap.Logger, fakeMasterMaxPct, masterMaxPct, slMinPct, slMaxPct, masterMaxWickPct float64) *VandeBharatTrapEngine {
	if fakeMasterMaxPct <= 0 {
		fakeMasterMaxPct = 3.0
	}
	if masterMaxPct <= 0 {
		masterMaxPct = 1.8
	}
	if slMinPct <= 0 {
		slMinPct = 0.5
	}
	if slMaxPct <= 0 {
		slMaxPct = 1.0
	}
	if masterMaxWickPct <= 0 {
		masterMaxWickPct = 40.0
	}
	return &VandeBharatTrapEngine{
		logger:               logger,
		pdHighs:              make(map[string]float64),
		pdLows:               make(map[string]float64),
		pdCloses:             make(map[string]float64),
		fakeMasterCandles:    make(map[string]*data.Candle),
		masterCandles:        make(map[string]*data.Candle),
		masterCandleIndices:  make(map[string]int),
		secondCandles:        make(map[string]*data.Candle),
		confirmationCandles:  make(map[string]*data.Candle),
		breakoutTriggerLevel: make(map[string]float64),
		slAnchorPrices:       make(map[string]float64),
		firstCandles:         make(map[string]*data.Candle),
		triggeredTrades:      make(map[string]bool),
		rollingCandles:       make(map[string][]data.Candle),
		fakeMasterMaxPct:     fakeMasterMaxPct,
		masterMaxPct:         masterMaxPct,
		slMinPct:             slMinPct,
		slMaxPct:             slMaxPct,
		masterMaxWickPct:     masterMaxWickPct,
		MinCandlesToIgnore:   0,
		candleTimeFrame:      "1m",
	}
}

// Name returns the strategy name
func (e *VandeBharatTrapEngine) Name() string {
	return "VANDE_BHARAT_TRAP"
}

// CandleTimeFrame returns the configured candle interval (e.g. "1m", "5m")
func (e *VandeBharatTrapEngine) CandleTimeFrame() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.candleTimeFrame == "" {
		return "1m"
	}
	return e.candleTimeFrame
}

// SetCandleTimeFrame sets the strategy candle interval (e.g. "1m", "5m")
func (e *VandeBharatTrapEngine) SetCandleTimeFrame(tf string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if tf == "" {
		tf = "1m"
	}
	e.candleTimeFrame = tf
	e.logger.Info("Updated strategy candle timeframe",
		zap.String("strategy", "VANDE_BHARAT_TRAP"),
		zap.String("timeframe", tf),
	)
}

// UpdateRules dynamically updates the strategy rule thresholds in memory
func (e *VandeBharatTrapEngine) UpdateRules(fakeMasterMaxPct, masterMaxPct, slMinPct, slMaxPct, masterMaxWickPct float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if fakeMasterMaxPct > 0 {
		e.fakeMasterMaxPct = fakeMasterMaxPct
	}
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
	e.logger.Info("Vande Bharat Trap strategy rules dynamically updated",
		zap.Float64("fake_master_max_pct", e.fakeMasterMaxPct),
		zap.Float64("master_max_pct", e.masterMaxPct),
		zap.Float64("sl_min_pct", e.slMinPct),
		zap.Float64("sl_max_pct", e.slMaxPct),
		zap.Float64("master_max_wick_pct", e.masterMaxWickPct),
	)
}

// SetPreviousDayHighLow binds the reference PDH and PDL levels for a symbol
func (e *VandeBharatTrapEngine) SetPreviousDayHighLow(symbol string, high float64, low float64) {
	e.SetPreviousDayLevels(symbol, high, low, (high+low)/2.0)
}

// SetPreviousDayLevels binds the reference PDH, PDL, and Yesterday's Close price for a symbol
func (e *VandeBharatTrapEngine) SetPreviousDayLevels(symbol string, high float64, low float64, closeVal float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pdHighs[symbol] = high
	e.pdLows[symbol] = low
	if closeVal > 0 {
		e.pdCloses[symbol] = closeVal
	} else {
		e.pdCloses[symbol] = (high + low) / 2.0
	}
	e.logger.Info("Vande Bharat Trap reference levels configured",
		zap.String("symbol", symbol),
		zap.Float64("pdh", high),
		zap.Float64("pdl", low),
		zap.Float64("pd_close", e.pdCloses[symbol]),
	)
}

// OnCandleClose processes incoming candles to detect Fake Master, Master, 2nd, and Confirmation candles
func (e *VandeBharatTrapEngine) OnCandleClose(candle *data.Candle, symbol string) {
	candleTimeIST := candle.Time.In(data.ISTLocation)
	marketStart := time.Date(candleTimeIST.Year(), candleTimeIST.Month(), candleTimeIST.Day(), 9, 15, 0, 0, data.ISTLocation)
	if candleTimeIST.Before(marketStart) && candleTimeIST.Hour() < 9 {
		return // Discard pre-market candles before 09:15 AM IST
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.rollingCandles[symbol] = append(e.rollingCandles[symbol], *candle)
	candles := e.rollingCandles[symbol]
	currentIndex := len(candles) - 1

	pdh, okHigh := e.pdHighs[symbol]
	pdl, okLow := e.pdLows[symbol]
	if !okHigh || !okLow || pdh <= 0 || pdl <= 0 {
		return // Reference levels not set for this symbol
	}

	// 1. Record 1st candle of the day (09:15 AM IST only) - Fake Master Candle
	if candleTimeIST.Hour() == 9 && candleTimeIST.Minute() == 15 {
		e.firstCandles[symbol] = candle

		// BUY Fake Master: Closes > PDH, but body is RED (Close < Open)
		isFakeMasterBuy := candle.Close > pdh && candle.Close < candle.Open
		// SELL Fake Master: Closes < PDL, but body is GREEN (Close > Open)
		isFakeMasterSell := candle.Close < pdl && candle.Close > candle.Open

		if isFakeMasterBuy || isFakeMasterSell {
			candleRange := candle.High - candle.Low
			rangePct := (candleRange / candle.Close) * 100.0

			allowedFakeRange := e.fakeMasterMaxPct
			if allowedFakeRange <= 0 {
				allowedFakeRange = 3.0
			}

			if rangePct <= allowedFakeRange {
				e.fakeMasterCandles[symbol] = candle
				direction := "BUY"
				refLevel := pdh
				if isFakeMasterSell {
					direction = "SELL"
					refLevel = pdl
				}
				e.logger.Info("Established Fake Master Candle (1st candle 09:15 AM, VANDE_BHARAT_TRAP)",
					zap.String("symbol", symbol),
					zap.String("direction", direction),
					zap.Float64("open", candle.Open),
					zap.Float64("close", candle.Close),
					zap.Float64("ref_level", refLevel),
					zap.Float64("range_pct", rangePct),
					zap.Float64("max_allowed_pct", allowedFakeRange),
				)
			} else {
				e.logger.Warn("1st Candle (09:15 AM) exceeded Fake Master max range limit, disqualified for day",
					zap.String("symbol", symbol),
					zap.Float64("range_pct", rangePct),
					zap.Float64("max_allowed_pct", allowedFakeRange),
				)
			}
		} else {
			e.logger.Warn("1st Candle (09:15 AM) failed Fake Master criteria (wrong color or inside PDH/PDL)",
				zap.String("symbol", symbol),
				zap.Float64("open", candle.Open),
				zap.Float64("close", candle.Close),
				zap.Float64("pdh", pdh),
				zap.Float64("pdl", pdl),
			)
		}
		return
	}

	fakeMaster := e.fakeMasterCandles[symbol]
	if fakeMaster == nil {
		return // No valid Fake Master candle formed at 09:15 AM
	}

	isBuySetup := fakeMaster.Close > pdh

	// 2. Detect transition to genuine Vande Bharat Master Candle (when Fake Master extreme is broken)
	if e.masterCandles[symbol] == nil {
		if isBuySetup {
			// BUY Setup: Subsequent candle breaks Fake Master High
			if candle.High > fakeMaster.High || candle.Close > fakeMaster.High {
				candleRange := candle.High - candle.Low
				bodySize := math.Abs(candle.Close - candle.Open)
				wickSize := candleRange - bodySize
				allowedRange := (e.masterMaxPct / 100.0) * candle.Close

				maxWickRatio := e.masterMaxWickPct / 100.0
				if maxWickRatio <= 0 {
					maxWickRatio = 0.40
				}
				validWick := candleRange > 0 && (wickSize/candleRange) <= maxWickRatio+1e-5

				if candleRange <= allowedRange && validWick {
					e.masterCandles[symbol] = candle
					e.masterCandleIndices[symbol] = currentIndex
					e.logger.Info("Established Vande Bharat Master Candle from Fake Master High Break (BUY)",
						zap.String("symbol", symbol),
						zap.Float64("fake_master_high", fakeMaster.High),
						zap.Float64("master_high", candle.High),
						zap.Float64("master_close", candle.Close),
						zap.Float64("range_pct", (candleRange/candle.Close)*100.0),
					)
				} else {
					e.logger.Warn("Candle broke Fake Master High but failed Master range/wick criteria",
						zap.String("symbol", symbol),
						zap.Float64("range_pct", (candleRange/candle.Close)*100.0),
						zap.Float64("wick_pct", (wickSize/candleRange)*100.0),
					)
				}
			}
		} else {
			// SELL Setup: Subsequent candle breaks Fake Master Low
			if candle.Low < fakeMaster.Low || candle.Close < fakeMaster.Low {
				candleRange := candle.High - candle.Low
				bodySize := math.Abs(candle.Close - candle.Open)
				wickSize := candleRange - bodySize
				allowedRange := (e.masterMaxPct / 100.0) * candle.Close

				maxWickRatio := e.masterMaxWickPct / 100.0
				if maxWickRatio <= 0 {
					maxWickRatio = 0.40
				}
				validWick := candleRange > 0 && (wickSize/candleRange) <= maxWickRatio+1e-5

				if candleRange <= allowedRange && validWick {
					e.masterCandles[symbol] = candle
					e.masterCandleIndices[symbol] = currentIndex
					e.logger.Info("Established Vande Bharat Master Candle from Fake Master Low Break (SELL)",
						zap.String("symbol", symbol),
						zap.Float64("fake_master_low", fakeMaster.Low),
						zap.Float64("master_low", candle.Low),
						zap.Float64("master_close", candle.Close),
						zap.Float64("range_pct", (candleRange/candle.Close)*100.0),
					)
				} else {
					e.logger.Warn("Candle broke Fake Master Low but failed Master range/wick criteria",
						zap.String("symbol", symbol),
						zap.Float64("range_pct", (candleRange/candle.Close)*100.0),
						zap.Float64("wick_pct", (wickSize/candleRange)*100.0),
					)
				}
			}
		}
		return
	}

	master := e.masterCandles[symbol]
	masterIdx := e.masterCandleIndices[symbol]

	// 3. Record 2nd candle (immediately following Master Candle) - SL Anchor & Range Control
	if currentIndex == masterIdx+1 && e.secondCandles[symbol] == nil {
		// Invalidation check: Breached opposite side of Master
		if isBuySetup && candle.Low < master.Low {
			e.logger.Warn("2nd candle breached Master Low, BUY setup invalidated",
				zap.String("symbol", symbol),
				zap.Float64("master_low", master.Low),
				zap.Float64("candle_low", candle.Low),
			)
			e.masterCandles[symbol] = nil
			return
		} else if !isBuySetup && candle.High > master.High {
			e.logger.Warn("2nd candle breached Master High, SELL setup invalidated",
				zap.String("symbol", symbol),
				zap.Float64("master_high", master.High),
				zap.Float64("candle_high", candle.High),
			)
			e.masterCandles[symbol] = nil
			return
		}

		secondRange := candle.High - candle.Low
		secondRangePct := (secondRange / candle.Close) * 100.0

		minSL := e.slMinPct
		if minSL <= 0 {
			minSL = 0.5
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

		e.secondCandles[symbol] = candle

		if isBuySetup {
			e.slAnchorPrices[symbol] = candle.Low
			if candle.High > master.High {
				e.confirmationCandles[symbol] = candle
				e.breakoutTriggerLevel[symbol] = candle.High
				e.logger.Info("Trap Rule 1: Candle 2 broke Master High -> Confirmation Candle set (Trigger @ Candle 2 High)",
					zap.String("symbol", symbol),
					zap.Float64("confirmation_high", candle.High),
					zap.Float64("sl_anchor_low", candle.Low),
				)
			} else {
				e.confirmationCandles[symbol] = nil
				e.breakoutTriggerLevel[symbol] = master.High
				e.logger.Info("Trap Rule 2: Candle 2 inside Master range -> SL Anchor set (Trigger @ Master High)",
					zap.String("symbol", symbol),
					zap.Float64("master_high", master.High),
					zap.Float64("sl_anchor_low", candle.Low),
				)
			}
		} else {
			e.slAnchorPrices[symbol] = candle.High
			if candle.Low < master.Low {
				e.confirmationCandles[symbol] = candle
				e.breakoutTriggerLevel[symbol] = candle.Low
				e.logger.Info("Trap Rule 1: Candle 2 broke Master Low -> Confirmation Candle set (Trigger @ Candle 2 Low)",
					zap.String("symbol", symbol),
					zap.Float64("confirmation_low", candle.Low),
					zap.Float64("sl_anchor_high", candle.High),
				)
			} else {
				e.confirmationCandles[symbol] = nil
				e.breakoutTriggerLevel[symbol] = master.Low
				e.logger.Info("Trap Rule 2: Candle 2 inside Master range -> SL Anchor set (Trigger @ Master Low)",
					zap.String("symbol", symbol),
					zap.Float64("master_low", master.Low),
					zap.Float64("sl_anchor_high", candle.High),
				)
			}
		}
		return
	}

	// 4. Rule 3: Wait for Breakout Candle & Strict Breakout-Candle Execution Expiration Guard
	if currentIndex > masterIdx+1 && e.masterCandles[symbol] != nil {
		triggerLevel, hasTrigger := e.breakoutTriggerLevel[symbol]

		if hasTrigger {
			if isBuySetup {
				// Opposite breach invalidation
				if candle.Low < master.Low {
					e.logger.Warn("Candle breached Master Low while waiting for trap breakout, BUY setup invalidated",
						zap.String("symbol", symbol),
						zap.Float64("master_low", master.Low),
						zap.Float64("candle_low", candle.Low),
					)
					e.masterCandles[symbol] = nil
					e.secondCandles[symbol] = nil
					e.confirmationCandles[symbol] = nil
					delete(e.breakoutTriggerLevel, symbol)
					delete(e.slAnchorPrices, symbol)
					return
				}

				// Check if this completed candle broke the trigger level (Breakout Candle)
				if candle.High > triggerLevel || candle.Close > triggerLevel {
					if !e.triggeredTrades[symbol] {
						e.logger.Info("Trap Rule 3: Breakout candle broke trigger level but trade was not executed in the same candle -> Setup cancelled",
							zap.String("symbol", symbol),
							zap.Float64("trigger_level", triggerLevel),
							zap.Float64("candle_high", candle.High),
							zap.Time("candle_time", candleTimeIST),
						)
						e.masterCandles[symbol] = nil
						e.secondCandles[symbol] = nil
						e.confirmationCandles[symbol] = nil
						delete(e.breakoutTriggerLevel, symbol)
						delete(e.slAnchorPrices, symbol)
						return
					}
				}
			} else {
				// SELL Setup
				if candle.High > master.High {
					e.logger.Warn("Candle breached Master High while waiting for trap breakdown, SELL setup invalidated",
						zap.String("symbol", symbol),
						zap.Float64("master_high", master.High),
						zap.Float64("candle_high", candle.High),
					)
					e.masterCandles[symbol] = nil
					e.secondCandles[symbol] = nil
					e.confirmationCandles[symbol] = nil
					delete(e.breakoutTriggerLevel, symbol)
					delete(e.slAnchorPrices, symbol)
					return
				}

				// Check if this completed candle broke the trigger level (Breakdown Candle)
				if candle.Low < triggerLevel || candle.Close < triggerLevel {
					if !e.triggeredTrades[symbol] {
						e.logger.Info("Trap Rule 3: Breakdown candle broke trigger level but trade was not executed in the same candle -> Setup cancelled",
							zap.String("symbol", symbol),
							zap.Float64("trigger_level", triggerLevel),
							zap.Float64("candle_low", candle.Low),
							zap.Time("candle_time", candleTimeIST),
						)
						e.masterCandles[symbol] = nil
						e.secondCandles[symbol] = nil
						e.confirmationCandles[symbol] = nil
						delete(e.breakoutTriggerLevel, symbol)
						delete(e.slAnchorPrices, symbol)
						return
					}
				}
			}
		}
	}
}

// CheckBreakout checks if the live LTP triggers a breakout entry on the active trigger level
func (e *VandeBharatTrapEngine) CheckBreakout(symbol string, ltp float64, bias string) *Signal {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.triggeredTrades[symbol] {
		return nil
	}

	if len(e.rollingCandles[symbol]) < e.MinCandlesToIgnore {
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
	fakeMaster := e.fakeMasterCandles[symbol]
	if fakeMaster == nil {
		return nil
	}

	isMasterBuy := fakeMaster.Close > pdh

	// Price move from PDH (for BUY) or PDL (for SELL) must be <= masterMaxPct
	maxAllowedMove := e.masterMaxPct
	if maxAllowedMove <= 0 {
		maxAllowedMove = 1.8
	}

	if isMasterBuy {
		moveFromPDH := ((ltp - pdh) / pdh) * 100.0
		if moveFromPDH > maxAllowedMove {
			e.logger.Warn("Vande Bharat Trap BUY entry skipped: price move from PDH exceeds Master Max Range limit",
				zap.String("symbol", symbol),
				zap.Float64("move_from_pdh_pct", moveFromPDH),
				zap.Float64("max_allowed_move", maxAllowedMove),
				zap.Float64("ltp", ltp),
				zap.Float64("pdh", pdh),
			)
			return nil
		}

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
				Reason:       fmt.Sprintf("Price %.2f broke above Vande Bharat Trap %s %.2f (SL: %.2f, Move from PDH: %.2f%% <= %.2f%%)", ltp, ruleDesc, triggerLevel, slPrice, moveFromPDH, maxAllowedMove),
				Candle:       second,
				StrategyName: e.Name(),
			}
		}
	} else {
		moveFromPDL := ((pdl - ltp) / pdl) * 100.0
		if moveFromPDL > maxAllowedMove {
			e.logger.Warn("Vande Bharat Trap SELL entry skipped: price move from PDL exceeds Master Max Range limit",
				zap.String("symbol", symbol),
				zap.Float64("move_from_pdl_pct", moveFromPDL),
				zap.Float64("max_allowed_move", maxAllowedMove),
				zap.Float64("ltp", ltp),
				zap.Float64("pdl", pdl),
			)
			return nil
		}

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
				Reason:       fmt.Sprintf("Price %.2f broke below Vande Bharat Trap %s %.2f (SL: %.2f, Move from PDL: %.2f%% <= %.2f%%)", ltp, ruleDesc, triggerLevel, slPrice, moveFromPDL, maxAllowedMove),
				Candle:       second,
				StrategyName: e.Name(),
			}
		}
	}

	return nil
}

// GetSetupCandle returns the risk anchor (2nd candle following Master Candle) to compute Stop-Loss and targets
func (e *VandeBharatTrapEngine) GetSetupCandle(symbol string) *SetupCandle {
	e.mu.RLock()
	defer e.mu.RUnlock()

	master := e.masterCandles[symbol]
	second := e.secondCandles[symbol]
	if master == nil || second == nil {
		return nil
	}

	triggerLevel, hasTrigger := e.breakoutTriggerLevel[symbol]
	slPrice, hasSL := e.slAnchorPrices[symbol]
	if !hasTrigger || !hasSL {
		return nil
	}

	pdh := e.pdHighs[symbol]
	fakeMaster := e.fakeMasterCandles[symbol]
	isMasterBuy := true
	if fakeMaster != nil {
		isMasterBuy = fakeMaster.Close > pdh
	}

	highVal := triggerLevel
	lowVal := slPrice
	if !isMasterBuy {
		// For SELL: StopLoss is highVal (Candle 2 High), triggerLevel is lowVal (Candle 2 Low or Master Low)
		highVal = slPrice
		lowVal = triggerLevel
	}

	return &SetupCandle{
		Candle: *second,
		High:   highVal,
		Low:    lowVal,
		Volume: second.Volume,
	}
}

// Reset clears the strategy engine state for a new day
func (e *VandeBharatTrapEngine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.rollingCandles = make(map[string][]data.Candle)
	e.pdHighs = make(map[string]float64)
	e.pdLows = make(map[string]float64)
	e.pdCloses = make(map[string]float64)
	e.fakeMasterCandles = make(map[string]*data.Candle)
	e.masterCandles = make(map[string]*data.Candle)
	e.masterCandleIndices = make(map[string]int)
	e.secondCandles = make(map[string]*data.Candle)
	e.confirmationCandles = make(map[string]*data.Candle)
	e.breakoutTriggerLevel = make(map[string]float64)
	e.slAnchorPrices = make(map[string]float64)
	e.firstCandles = make(map[string]*data.Candle)
	e.triggeredTrades = make(map[string]bool)
	e.logger.Info("VANDE_BHARAT_TRAP strategy engine state reset successfully")
}

// RestoreTriggeredTrade registers an already triggered trade for a symbol
func (e *VandeBharatTrapEngine) RestoreTriggeredTrade(symbol string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.triggeredTrades[symbol] = true
	e.logger.Info("VANDE_BHARAT_TRAP: Restored triggered trade state", zap.String("symbol", symbol))
}
