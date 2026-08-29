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
	pdHighs             map[string]float64
	pdLows              map[string]float64
	pdCloses            map[string]float64      // Yesterday's Close Price
	fakeMasterCandles   map[string]*data.Candle // 1st candle of day (09:15 AM IST) opposite color trap
	masterCandles       map[string]*data.Candle // Established when Fake Master High (BUY) or Low (SELL) is broken
	masterCandleIndices map[string]int          // Index in rollingCandles when master candle formed
	secondCandles       map[string]*data.Candle // Candle immediately following Master Candle (SL Anchor)
	confirmationCandles map[string]*data.Candle // First candle breaking new Day High / Low
	firstCandles        map[string]*data.Candle // 1st candle of day (09:15 AM IST)
	triggeredTrades     map[string]bool
	rollingCandles      map[string][]data.Candle
	fakeMasterMaxPct    float64 // Max candle range % for 1st Fake Master candle (default: 3.0%)
	masterMaxPct        float64 // Master Candle Max Range (%) - also bounds entry price move from PDH/PDL (default: 1.8%)
	slMinPct            float64 // 2nd Candle (SL) Min Range (%) (default: 0.5%)
	slMaxPct            float64 // 2nd Candle (SL) Max Range (%) (default: 1.0%)
	masterMaxWickPct    float64 // Max total upper + lower wick % (default: 40.0%)
	MinCandlesToIgnore  int
	candleTimeFrame     string
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
		logger:              logger,
		pdHighs:             make(map[string]float64),
		pdLows:              make(map[string]float64),
		pdCloses:            make(map[string]float64),
		fakeMasterCandles:   make(map[string]*data.Candle),
		masterCandles:       make(map[string]*data.Candle),
		masterCandleIndices: make(map[string]int),
		secondCandles:       make(map[string]*data.Candle),
		confirmationCandles: make(map[string]*data.Candle),
		firstCandles:        make(map[string]*data.Candle),
		triggeredTrades:     make(map[string]bool),
		rollingCandles:      make(map[string][]data.Candle),
		fakeMasterMaxPct:    fakeMasterMaxPct,
		masterMaxPct:        masterMaxPct,
		slMinPct:            slMinPct,
		slMaxPct:            slMaxPct,
		masterMaxWickPct:    masterMaxWickPct,
		MinCandlesToIgnore:  0,
		candleTimeFrame:     "1m",
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

		if secondRangePct >= minSL && secondRangePct <= maxSL {
			e.secondCandles[symbol] = candle
			e.logger.Info("Established valid 2nd Candle (SL Anchor, VANDE_BHARAT_TRAP)",
				zap.String("symbol", symbol),
				zap.Float64("range_pct", secondRangePct),
				zap.Float64("sl_low", candle.Low),
				zap.Float64("sl_high", candle.High),
			)
		} else {
			e.logger.Warn("2nd Candle failed SL range criteria (too wide or too narrow), setup invalidated",
				zap.String("symbol", symbol),
				zap.Float64("range_pct", secondRangePct),
				zap.Float64("min_sl_pct", minSL),
				zap.Float64("max_sl_pct", maxSL),
			)
			e.masterCandles[symbol] = nil
			return
		}
		return
	}

	// 4. Master Candle Invalidation & Confirmation Candle Detection (Any Color)
	if e.masterCandles[symbol] != nil {
		// 4a. Once Confirmation Candle is already established:
		if e.confirmationCandles[symbol] != nil {
			if isBuySetup && (candle.Close < master.Low || candle.Low < master.Low) {
				e.logger.Warn("Master Candle Low broken after confirmation, BUY setup invalidated",
					zap.String("symbol", symbol),
					zap.Float64("master_low", master.Low),
					zap.Float64("candle_close", candle.Close),
				)
				e.masterCandles[symbol] = nil
				e.confirmationCandles[symbol] = nil
				return
			} else if !isBuySetup && (candle.Close > master.High || candle.High > master.High) {
				e.logger.Warn("Master Candle High broken after confirmation, SELL setup invalidated",
					zap.String("symbol", symbol),
					zap.Float64("master_high", master.High),
					zap.Float64("candle_close", candle.Close),
				)
				e.masterCandles[symbol] = nil
				e.confirmationCandles[symbol] = nil
				return
			}
			return
		}

		// 4b. BEFORE Confirmation Candle is established:
		// All intermediate candles MUST stay strictly inside Master Candle range [master.Low, master.High]!
		if isBuySetup {
			// Invalidation: Opposite side breached (Low broken in Buy setup)
			if candle.Low < master.Low || candle.Close < master.Low {
				e.logger.Warn("Intermediate candle broke Master Low prior to confirmation, BUY setup invalidated",
					zap.String("symbol", symbol),
					zap.Float64("master_low", master.Low),
					zap.Float64("candle_low", candle.Low),
				)
				e.masterCandles[symbol] = nil
				return
			}

			// Breakout Candidate Check (Breaks Day High / Master High)
			if candle.High > master.High || candle.Close > master.High {
				e.confirmationCandles[symbol] = candle
				e.logger.Info("Established Confirmation Candle (VANDE_BHARAT_TRAP BUY - Broke Day High)",
					zap.String("symbol", symbol),
					zap.Float64("candle_high", candle.High),
					zap.Float64("candle_close", candle.Close),
					zap.Float64("master_high", master.High),
				)
				return
			}
		} else {
			// SELL Setup (master.Close < pdl)
			// Invalidation: Opposite side breached (High broken in Sell setup)
			if candle.High > master.High || candle.Close > master.High {
				e.logger.Warn("Intermediate candle broke Master High prior to confirmation, SELL setup invalidated",
					zap.String("symbol", symbol),
					zap.Float64("master_high", master.High),
					zap.Float64("candle_high", candle.High),
				)
				e.masterCandles[symbol] = nil
				return
			}

			// Breakdown Candidate Check (Breaks Day Low / Master Low)
			if candle.Low < master.Low || candle.Close < master.Low {
				e.confirmationCandles[symbol] = candle
				e.logger.Info("Established Confirmation Candle (VANDE_BHARAT_TRAP SELL - Broke Day Low)",
					zap.String("symbol", symbol),
					zap.Float64("candle_low", candle.Low),
					zap.Float64("candle_close", candle.Close),
					zap.Float64("master_low", master.Low),
				)
				return
			}
		}
	}
}

// CheckBreakout checks if the live LTP triggers a breakout entry on the Confirmation Candle
func (e *VandeBharatTrapEngine) CheckBreakout(symbol string, ltp float64, bias string) *Signal {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.triggeredTrades[symbol] {
		return nil
	}

	if len(e.rollingCandles[symbol]) < e.MinCandlesToIgnore {
		return nil
	}

	confirm := e.confirmationCandles[symbol]
	if confirm == nil {
		return nil
	}

	master := e.masterCandles[symbol]
	if master == nil {
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

		if ltp > confirm.High {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "BUY",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %f broke above Vande Bharat Trap Confirmation High %f (Move from PDH: %.2f%% <= %.2f%%)", ltp, confirm.High, moveFromPDH, maxAllowedMove),
				Candle:       confirm,
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

		if ltp < confirm.Low {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "SELL",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %f broke below Vande Bharat Trap Confirmation Low %f (Move from PDL: %.2f%% <= %.2f%%)", ltp, confirm.Low, moveFromPDL, maxAllowedMove),
				Candle:       confirm,
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

	confirm := e.confirmationCandles[symbol]
	if confirm == nil {
		return nil
	}

	second := e.secondCandles[symbol]
	lowVal := confirm.Low
	highVal := confirm.High

	if second != nil {
		lowVal = second.Low
		highVal = second.High
	}

	return &SetupCandle{
		Candle: *confirm,
		High:   highVal,
		Low:    lowVal,
		Volume: confirm.Volume,
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
