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
	logger                 *zap.Logger
	mu                     sync.RWMutex
	pdHighs                map[string]float64
	pdLows                 map[string]float64
	masterCandles          map[string]*data.Candle
	secondCandles          map[string]*data.Candle // 2nd candle of day (09:20 AM IST) for SL anchor (Rule 5)
	confirmationCandles    map[string]*data.Candle
	firstCandles           map[string]*data.Candle // 1st candle of day (09:15 AM IST) for day % change (Rule 2)
	triggeredTrades        map[string]bool
	rollingCandles         map[string][]data.Candle
	masterMaxPct           float64
	confirmMinPct          float64
	confirmMaxPct          float64
	masterMaxWickPct       float64
	stockMaxDayChangePct   float64
	MinCandlesToIgnore     int
}

// NewVandeBharatEngine creates a new instance of VandeBharatEngine
func NewVandeBharatEngine(logger *zap.Logger, masterMaxPct, confirmMinPct, confirmMaxPct, masterMaxWickPct, stockMaxDayChangePct float64) *VandeBharatEngine {
	return &VandeBharatEngine{
		logger:                 logger,
		pdHighs:                make(map[string]float64),
		pdLows:                 make(map[string]float64),
		masterCandles:          make(map[string]*data.Candle),
		secondCandles:          make(map[string]*data.Candle),
		confirmationCandles:    make(map[string]*data.Candle),
		firstCandles:           make(map[string]*data.Candle),
		triggeredTrades:        make(map[string]bool),
		rollingCandles:         make(map[string][]data.Candle),
		masterMaxPct:           masterMaxPct,
		confirmMinPct:          confirmMinPct,
		confirmMaxPct:          confirmMaxPct,
		masterMaxWickPct:       masterMaxWickPct,
		stockMaxDayChangePct:   stockMaxDayChangePct,
		MinCandlesToIgnore:     0,
	}
}

// Name returns the strategy name
func (e *VandeBharatEngine) Name() string {
	return "VANDE_BHARAT"
}

// SetPreviousDayHighLow binds the reference PDH and PDL levels for a symbol
func (e *VandeBharatEngine) SetPreviousDayHighLow(symbol string, high float64, low float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pdHighs[symbol] = high
	e.pdLows[symbol] = low
	e.logger.Info("Vande Bharat reference levels configured",
		zap.String("symbol", symbol),
		zap.Float64("pdh", high),
		zap.Float64("pdl", low),
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

	// Record 1st candle of the day (09:15 AM IST)
	if candleCount == 1 {
		e.firstCandles[symbol] = candle

		// Rule 1: Master Candle MUST be the 1st candle of the day (09:15 AM) ONLY
		isMasterBuy := candle.Close > pdh && candle.Close > candle.Open
		isMasterSell := candle.Close < pdl && candle.Close < candle.Open

		if isMasterBuy || isMasterSell {
			candleRange := candle.High - candle.Low
			bodySize := math.Abs(candle.Close - candle.Open)
			wickSize := candleRange - bodySize

			allowedRange := (e.masterMaxPct / 100.0) * candle.Close

			// Rule 4: Master candle body must be overall max wick % (wickSize <= masterMaxWickPct/100 * candleRange)
			maxWickRatio := e.masterMaxWickPct / 100.0
			if maxWickRatio <= 0 {
				maxWickRatio = 0.40
			}
			validWick := candleRange > 0 && (wickSize/candleRange) <= maxWickRatio

			if candleRange <= allowedRange && validWick {
				e.masterCandles[symbol] = candle
				direction := "BUY"
				refLevel := pdh
				if isMasterSell {
					direction = "SELL"
					refLevel = pdl
				}
				e.logger.Info("Established Master Candle (1st candle 09:15 AM, VANDE_BHARAT)",
					zap.String("symbol", symbol),
					zap.String("direction", direction),
					zap.Float64("close", candle.Close),
					zap.Float64("ref_level", refLevel),
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
		}
		return
	}

	// Record 2nd candle of the day (09:20 AM IST) for Rule 5 SL Anchor
	if candleCount == 2 {
		e.secondCandles[symbol] = candle
	}

	// Master Candle Invalidation Check & Confirmation Candle Detection
	if e.masterCandles[symbol] != nil {
		master := e.masterCandles[symbol]
		isBuySetup := master.Close > pdh

		// Invalidation Rule: If price breaks Master Low (for Buy) or Master High (for Sell), setup becomes INVALID!
		if isBuySetup && (candle.Close < master.Low || candle.Low < master.Low) {
			e.logger.Warn("Master Candle Low broken by subsequent candle, BUY setup invalidated",
				zap.String("symbol", symbol),
				zap.Float64("master_low", master.Low),
				zap.Float64("candle_close", candle.Close),
			)
			e.masterCandles[symbol] = nil
			e.confirmationCandles[symbol] = nil
			return
		} else if !isBuySetup && (candle.Close > master.High || candle.High > master.High) {
			e.logger.Warn("Master Candle High broken by subsequent candle, SELL setup invalidated",
				zap.String("symbol", symbol),
				zap.Float64("master_high", master.High),
				zap.Float64("candle_close", candle.Close),
			)
			e.masterCandles[symbol] = nil
			e.confirmationCandles[symbol] = nil
			return
		}

		// Detect Confirmation Candle (Any subsequent candle breaking Master High/Low within range bounds)
		if e.confirmationCandles[symbol] == nil {
			var confirmed bool
			if isBuySetup {
				// Buy Confirmation: Candle breaks Master High & is GREEN (Close > Open)
				confirmed = candle.Close > master.High && candle.Close > candle.Open
			} else {
				// Sell Confirmation: Candle breaks Master Low & is RED (Close < Open)
				confirmed = candle.Close < master.Low && candle.Close < candle.Open
			}

			if confirmed {
				candleRange := candle.High - candle.Low
				rangePct := (candleRange / candle.Close) * 100.0

				minConfirmPct := e.confirmMinPct
				if minConfirmPct <= 0 {
					minConfirmPct = 0.5
				}
				maxConfirmPct := e.confirmMaxPct
				if maxConfirmPct <= 0 {
					maxConfirmPct = 1.0
				}

				// Rule 3: Confirmation candle range MUST be strictly between confirmMinPct and confirmMaxPct of stock price
				if rangePct >= minConfirmPct && rangePct <= maxConfirmPct {
					e.confirmationCandles[symbol] = candle
					e.logger.Info("Established Confirmation Candle (VANDE_BHARAT)",
						zap.String("symbol", symbol),
						zap.Float64("close", candle.Close),
						zap.Float64("range_pct", rangePct),
					)
				} else {
					e.logger.Warn("Confirmation Candle candidate range percentage outside configured bounds, ignored",
						zap.String("symbol", symbol),
						zap.Float64("range_pct", rangePct),
						zap.Float64("min_pct", minConfirmPct),
						zap.Float64("max_pct", maxConfirmPct),
					)
				}
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

	// Rule 2: Overall day % change in stock when trade starts must be < stockMaxDayChangePct
	firstCandle := e.firstCandles[symbol]
	if firstCandle != nil && firstCandle.Open > 0 {
		dayPctChange := math.Abs((ltp-firstCandle.Open)/firstCandle.Open) * 100.0
		maxDayChange := e.stockMaxDayChangePct
		if maxDayChange <= 0 {
			maxDayChange = 3.0
		}
		if dayPctChange >= maxDayChange {
			e.logger.Warn("Vande Bharat trade entry skipped: stock day % change exceeds limit",
				zap.String("symbol", symbol),
				zap.Float64("day_pct_change", dayPctChange),
				zap.Float64("max_day_change", maxDayChange),
				zap.Float64("ltp", ltp),
				zap.Float64("open", firstCandle.Open),
			)
			return nil
		}
	}

	if bias == "BUY_ONLY" {
		if ltp > confirm.High {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "BUY",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %f broke above Vande Bharat Confirmation High %f", ltp, confirm.High),
				Candle:       confirm,
				StrategyName: e.Name(),
			}
		}
	} else if bias == "SELL_ONLY" {
		if ltp < confirm.Low {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "SELL",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %f broke below Vande Bharat Confirmation Low %f", ltp, confirm.Low),
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

	// Rule 5: SL will be the 2nd candle low (09:20 AM candle Low for BUY) or high (09:20 AM candle High for SELL)
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
