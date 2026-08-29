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
	logger              *zap.Logger
	mu                  sync.RWMutex
	pdHighs             map[string]float64
	pdLows              map[string]float64
	pdCloses            map[string]float64 // Yesterday's Close Price
	masterCandles       map[string]*data.Candle
	secondCandles       map[string]*data.Candle // 2nd candle of day (09:20 AM IST) for SL anchor (Rule 5)
	confirmationCandles map[string]*data.Candle
	firstCandles        map[string]*data.Candle // 1st candle of day (09:15 AM IST)
	triggeredTrades     map[string]bool
	rollingCandles      map[string][]data.Candle
	masterMaxPct        float64 // Master Candle Max Range (%) - also bounds entry price move from PDH/PDL
	slMinPct            float64 // 2nd Candle (SL) Min Range (%)
	slMaxPct            float64 // 2nd Candle (SL) Max Range (%)
	masterMaxWickPct    float64
	minGapPct           float64 // Min opening gap % from Yesterday's Close (default: 2.0%)
	MinCandlesToIgnore  int
}

// NewVandeBharatEngine creates a new instance of VandeBharatEngine
func NewVandeBharatEngine(logger *zap.Logger, masterMaxPct, slMinPct, slMaxPct, masterMaxWickPct, minGapPct float64) *VandeBharatEngine {
	if minGapPct <= 0 {
		minGapPct = 2.0
	}
	if slMinPct <= 0 {
		slMinPct = 0.5
	}
	if slMaxPct <= 0 {
		slMaxPct = 1.0
	}
	if masterMaxPct <= 0 {
		masterMaxPct = 1.8
	}
	if masterMaxWickPct <= 0 {
		masterMaxWickPct = 40.0
	}
	return &VandeBharatEngine{
		logger:              logger,
		pdHighs:             make(map[string]float64),
		pdLows:              make(map[string]float64),
		pdCloses:            make(map[string]float64),
		masterCandles:       make(map[string]*data.Candle),
		secondCandles:       make(map[string]*data.Candle),
		confirmationCandles: make(map[string]*data.Candle),
		firstCandles:        make(map[string]*data.Candle),
		triggeredTrades:     make(map[string]bool),
		rollingCandles:      make(map[string][]data.Candle),
		masterMaxPct:        masterMaxPct,
		slMinPct:            slMinPct,
		slMaxPct:            slMaxPct,
		masterMaxWickPct:    masterMaxWickPct,
		minGapPct:           minGapPct,
		MinCandlesToIgnore:  0,
	}
}

// Name returns the strategy name
func (e *VandeBharatEngine) Name() string {
	return "VANDE_BHARAT"
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
	if minGapPct > 0 {
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

// OnCandleClose processes incoming 5-minute candles to detect Master & Confirmation candles
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
				maxWickRatio = 0.40
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

	// 2. Record 2nd candle of the day (09:20 AM IST) - SL Anchor & Range Control
	if candleCount == 2 && e.masterCandles[symbol] != nil {
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
			e.logger.Info("Established valid 2nd Candle (SL Anchor, VANDE_BHARAT)",
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
	}

	// 3. Master Candle Invalidation & Confirmation Candle Detection (Any Color)
	if e.masterCandles[symbol] != nil {
		master := e.masterCandles[symbol]
		isBuySetup := master.Close > pdh

		// 3a. Once Confirmation Candle is already established:
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

		// 3b. BEFORE Confirmation Candle is established:
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

			// Breakout Candidate Check (Breaks Day High / Master High for the first time!)
			// Can be ANY COLOR (Green, Red, or Doji)
			if candle.High > master.High || candle.Close > master.High {
				e.confirmationCandles[symbol] = candle
				e.logger.Info("Established Confirmation Candle (VANDE_BHARAT BUY - Broke Day High)",
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

			// Breakdown Candidate Check (Breaks Day Low / Master Low for the first time!)
			// Can be ANY COLOR (Red, Green, or Doji)
			if candle.Low < master.Low || candle.Close < master.Low {
				e.confirmationCandles[symbol] = candle
				e.logger.Info("Established Confirmation Candle (VANDE_BHARAT SELL - Broke Day Low)",
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
func (e *VandeBharatEngine) CheckBreakout(symbol string, ltp float64, bias string) *Signal {
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
	isMasterBuy := master.Close > pdh

	// Price move from PDH (for BUY) or PDL (for SELL) must be <= masterMaxPct
	maxAllowedMove := e.masterMaxPct
	if maxAllowedMove <= 0 {
		maxAllowedMove = 1.8
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

		if ltp > confirm.High {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "BUY",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %f broke above Vande Bharat Confirmation High %f (Move from PDH: %.2f%% <= %.2f%%)", ltp, confirm.High, moveFromPDH, maxAllowedMove),
				Candle:       confirm,
				StrategyName: e.Name(),
			}
		}
	} else {
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

		if ltp < confirm.Low {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "SELL",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %f broke below Vande Bharat Confirmation Low %f (Move from PDL: %.2f%% <= %.2f%%)", ltp, confirm.Low, moveFromPDL, maxAllowedMove),
				Candle:       confirm,
				StrategyName: e.Name(),
			}
		}
	}

	return nil
}

// GetSetupCandle returns the risk anchor (2nd candle 09:20 AM low/high as per Rule 5) to compute Stop-Loss and targets
func (e *VandeBharatEngine) GetSetupCandle(symbol string) *SetupCandle {
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
