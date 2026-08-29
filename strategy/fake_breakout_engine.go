package strategy

import (
	"fmt"
	"sync"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// FakeBreakoutEngine implements the high opening gap exhaustion Fake Breakout strategy
type FakeBreakoutEngine struct {
	logger              *zap.Logger
	mu                  sync.RWMutex
	pdHighs             map[string]float64
	pdLows              map[string]float64
	pdCloses            map[string]float64 // Yesterday's Close Price
	masterCandles       map[string]*data.Candle
	confirmationCandles map[string]*data.Candle // 2nd candle of day
	firstCandles        map[string]*data.Candle // 1st candle of day (09:15 AM IST)
	triggeredTrades     map[string]bool
	rollingCandles      map[string][]data.Candle
	gapUpMinPct         float64 // Min opening gap up % from Yesterday's Close for SELL (default: 4.0%)
	gapUpMaxPct         float64 // Max opening gap up % from Yesterday's Close for SELL (default: 8.0%)
	gapDownMinPct       float64 // Min opening gap down % from Yesterday's Close for BUY (default: 4.0%)
	gapDownMaxPct       float64 // Max opening gap down % from Yesterday's Close for BUY (default: 8.0%)
	maxConfirmationPct  float64 // Max range % of Confirmation candle (default: 1.0%)
	masterMaxWickPct    float64 // Max wick % of 1st Master candle (default: 40.0%)
	TradeEndTime        string  // Entry cutoff time (default: "11:00:00")
	MinCandlesToIgnore  int
	candleTimeFrame     string
}

// NewFakeBreakoutEngine creates a new instance of FakeBreakoutEngine
func NewFakeBreakoutEngine(logger *zap.Logger, gapUpMinPct, gapUpMaxPct, gapDownMinPct, gapDownMaxPct, maxConfirmationPct, masterMaxWickPct float64) *FakeBreakoutEngine {
	if gapUpMinPct <= 0 {
		gapUpMinPct = 4.0
	}
	if gapUpMaxPct <= 0 {
		gapUpMaxPct = 8.0
	}
	if gapDownMinPct <= 0 {
		gapDownMinPct = 4.0
	}
	if gapDownMaxPct <= 0 {
		gapDownMaxPct = 8.0
	}
	if maxConfirmationPct <= 0 {
		maxConfirmationPct = 1.0
	}
	if masterMaxWickPct <= 0 {
		masterMaxWickPct = 40.0
	}
	return &FakeBreakoutEngine{
		logger:              logger,
		pdHighs:             make(map[string]float64),
		pdLows:              make(map[string]float64),
		pdCloses:            make(map[string]float64),
		masterCandles:       make(map[string]*data.Candle),
		confirmationCandles: make(map[string]*data.Candle),
		firstCandles:        make(map[string]*data.Candle),
		triggeredTrades:     make(map[string]bool),
		rollingCandles:      make(map[string][]data.Candle),
		gapUpMinPct:         gapUpMinPct,
		gapUpMaxPct:         gapUpMaxPct,
		gapDownMinPct:       gapDownMinPct,
		gapDownMaxPct:       gapDownMaxPct,
		maxConfirmationPct:  maxConfirmationPct,
		masterMaxWickPct:    masterMaxWickPct,
		TradeEndTime:        "11:00:00",
		MinCandlesToIgnore:  0,
		candleTimeFrame:     "1m",
	}
}

// Name returns the strategy name
func (e *FakeBreakoutEngine) Name() string {
	return "FAKE_BREAKOUT"
}

// CandleTimeFrame returns the configured candle interval (e.g. "1m", "5m")
func (e *FakeBreakoutEngine) CandleTimeFrame() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.candleTimeFrame == "" {
		return "1m"
	}
	return e.candleTimeFrame
}

// SetCandleTimeFrame sets the strategy candle interval (e.g. "1m", "5m")
func (e *FakeBreakoutEngine) SetCandleTimeFrame(tf string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if tf == "" {
		tf = "1m"
	}
	e.candleTimeFrame = tf
	e.logger.Info("Updated strategy candle timeframe",
		zap.String("strategy", "FAKE_BREAKOUT"),
		zap.String("timeframe", tf),
	)
}

// UpdateRules dynamically updates the strategy rule thresholds in memory
func (e *FakeBreakoutEngine) UpdateRules(gapUpMin, gapUpMax, gapDownMin, gapDownMax, maxConfirm, masterMaxWick float64, tradeEndTime string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if gapUpMin > 0 {
		e.gapUpMinPct = gapUpMin
	}
	if gapUpMax > 0 {
		e.gapUpMaxPct = gapUpMax
	}
	if gapDownMin > 0 {
		e.gapDownMinPct = gapDownMin
	}
	if gapDownMax > 0 {
		e.gapDownMaxPct = gapDownMax
	}
	if maxConfirm > 0 {
		e.maxConfirmationPct = maxConfirm
	}
	if masterMaxWick > 0 {
		e.masterMaxWickPct = masterMaxWick
	}
	if tradeEndTime != "" {
		e.TradeEndTime = tradeEndTime
	}
}

// SetPDHPDL sets the previous day high, low, and close for a symbol
func (e *FakeBreakoutEngine) SetPDHPDL(symbol string, pdh, pdl, pdClose float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pdHighs[symbol] = pdh
	e.pdLows[symbol] = pdl
	e.pdCloses[symbol] = pdClose
}

// GetSetupCandle returns the setup bounds for risk management (SL and Trigger bounds)
func (e *FakeBreakoutEngine) GetSetupCandle(symbol string) *SetupCandle {
	e.mu.RLock()
	defer e.mu.RUnlock()

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

// RestoreTriggeredTrade marks a symbol as already traded today
func (e *FakeBreakoutEngine) RestoreTriggeredTrade(symbol string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.triggeredTrades[symbol] = true
}

// Reset clears the in-memory state
func (e *FakeBreakoutEngine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pdHighs = make(map[string]float64)
	e.pdLows = make(map[string]float64)
	e.pdCloses = make(map[string]float64)
	e.masterCandles = make(map[string]*data.Candle)
	e.confirmationCandles = make(map[string]*data.Candle)
	e.firstCandles = make(map[string]*data.Candle)
	e.triggeredTrades = make(map[string]bool)
	e.rollingCandles = make(map[string][]data.Candle)
}

// OnCandleClose processes finalized candles and tracks Master & Confirmation candles
func (e *FakeBreakoutEngine) OnCandleClose(candle *data.Candle, symbol string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Append to rolling candles
	e.rollingCandles[symbol] = append(e.rollingCandles[symbol], *candle)
	candleCount := len(e.rollingCandles[symbol])

	istTime := data.NormalizeToIST(candle.Time)

	// Step 1: Detect and lock 09:15 AM Master Candle
	if candleCount == 1 {
		if istTime.Hour() != 9 || istTime.Minute() != 15 {
			e.logger.Warn("[FAKE_BREAKOUT] Incomplete session history: 1st candle is not 09:15 AM IST. Strategy disqualified for symbol",
				zap.String("symbol", symbol),
				zap.Time("candle_time", istTime),
			)
			return
		}

		cCopy := *candle
		e.firstCandles[symbol] = &cCopy

		pdClose := e.pdCloses[symbol]
		if pdClose <= 0 {
			pdClose = candle.Open
		}

		gapUpPct := ((candle.Open - pdClose) / pdClose) * 100.0
		gapDownPct := ((pdClose - candle.Open) / pdClose) * 100.0

		// Check color and wick constraints
		candleRange := candle.High - candle.Low
		if candleRange <= 0 {
			e.logger.Info("[FAKE_BREAKOUT] 1st Master candle zero range, disqualified", zap.String("symbol", symbol))
			return
		}

		var upperWick, lowerWick float64
		isRed := candle.Close < candle.Open
		isGreen := candle.Close > candle.Open

		if isRed {
			upperWick = candle.High - candle.Open
			lowerWick = candle.Close - candle.Low
		} else if isGreen {
			upperWick = candle.High - candle.Close
			lowerWick = candle.Open - candle.Low
		}

		totalWickPct := ((upperWick + lowerWick) / candleRange) * 100.0
		if totalWickPct > e.masterMaxWickPct {
			e.logger.Info("[FAKE_BREAKOUT] Master candle wicks exceed maximum allowed",
				zap.String("symbol", symbol),
				zap.Float64("total_wick_pct", totalWickPct),
				zap.Float64("max_wick_pct", e.masterMaxWickPct),
			)
			return
		}

		// SELL candidate check: Gap Up in [gapUpMinPct, gapUpMaxPct] and 1st candle RED
		if gapUpPct >= e.gapUpMinPct && gapUpPct <= e.gapUpMaxPct && isRed {
			e.masterCandles[symbol] = &cCopy
			e.logger.Info("[FAKE_BREAKOUT] Master Candle qualified for SELL setup",
				zap.String("symbol", symbol),
				zap.Float64("gap_up_pct", gapUpPct),
				zap.Float64("open", candle.Open),
				zap.Float64("close", candle.Close),
				zap.Float64("high", candle.High),
				zap.Float64("low", candle.Low),
			)
			return
		}

		// BUY candidate check: Gap Down in [gapDownMinPct, gapDownMaxPct] and 1st candle GREEN
		if gapDownPct >= e.gapDownMinPct && gapDownPct <= e.gapDownMaxPct && isGreen {
			e.masterCandles[symbol] = &cCopy
			e.logger.Info("[FAKE_BREAKOUT] Master Candle qualified for BUY setup",
				zap.String("symbol", symbol),
				zap.Float64("gap_down_pct", gapDownPct),
				zap.Float64("open", candle.Open),
				zap.Float64("close", candle.Close),
				zap.Float64("high", candle.High),
				zap.Float64("low", candle.Low),
			)
			return
		}

		e.logger.Info("[FAKE_BREAKOUT] 1st candle did not qualify as Master",
			zap.String("symbol", symbol),
			zap.Float64("gap_up_pct", gapUpPct),
			zap.Float64("gap_down_pct", gapDownPct),
			zap.Bool("is_red", isRed),
			zap.Bool("is_green", isGreen),
		)
		return
	}

	// Step 2: 2nd Candle Confirmation Check
	if candleCount == 2 {
		master := e.masterCandles[symbol]
		if master == nil {
			return
		}

		pdClose := e.pdCloses[symbol]
		if pdClose <= 0 {
			pdClose = master.Open
		}
		gapUpPct := ((master.Open - pdClose) / pdClose) * 100.0

		// Range % of 2nd candle
		confirmRange := candle.High - candle.Low
		if candle.Close <= 0 || confirmRange <= 0 {
			return
		}
		confirmRangePct := (confirmRange / candle.Close) * 100.0

		if confirmRangePct > e.maxConfirmationPct {
			e.logger.Info("[FAKE_BREAKOUT] 2nd candle range exceeds max confirmation threshold",
				zap.String("symbol", symbol),
				zap.Float64("range_pct", confirmRangePct),
				zap.Float64("max_confirm_pct", e.maxConfirmationPct),
			)
			return
		}

		// SELL Confirmation: Master was Red (Gap Up) -> 2nd candle must be RED & break Master Low
		if master.Close < master.Open && gapUpPct >= e.gapUpMinPct {
			isRed := candle.Close < candle.Open
			breaksMasterLow := candle.Low < master.Low

			if isRed && breaksMasterLow {
				cCopy := *candle
				e.confirmationCandles[symbol] = &cCopy
				e.logger.Info("[FAKE_BREAKOUT] ✅ SELL Confirmation Candle confirmed",
					zap.String("symbol", symbol),
					zap.Float64("master_low", master.Low),
					zap.Float64("confirm_low", candle.Low),
					zap.Float64("confirm_high", candle.High),
					zap.Float64("range_pct", confirmRangePct),
				)
				return
			}
		}

		// BUY Confirmation: Master was Green (Gap Down) -> 2nd candle must be GREEN & break Master High
		if master.Close > master.Open {
			isGreen := candle.Close > candle.Open
			breaksMasterHigh := candle.High > master.High

			if isGreen && breaksMasterHigh {
				cCopy := *candle
				e.confirmationCandles[symbol] = &cCopy
				e.logger.Info("[FAKE_BREAKOUT] ✅ BUY Confirmation Candle confirmed",
					zap.String("symbol", symbol),
					zap.Float64("master_high", master.High),
					zap.Float64("confirm_high", candle.High),
					zap.Float64("confirm_low", candle.Low),
					zap.Float64("range_pct", confirmRangePct),
				)
				return
			}
		}

		e.logger.Info("[FAKE_BREAKOUT] 2nd candle failed confirmation criteria",
			zap.String("symbol", symbol),
		)
		return
	}
}

// CheckBreakout checks if live tick triggers a trade entry from 3rd candle onward
func (e *FakeBreakoutEngine) CheckBreakout(symbol string, ltp float64, bias string) *Signal {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.triggeredTrades[symbol] {
		return nil
	}

	candles := e.rollingCandles[symbol]
	if len(candles) < 2 {
		// Cannot trade on 1st or 2nd candle (trade from 3rd candle onward)
		return nil
	}

	master := e.masterCandles[symbol]
	confirm := e.confirmationCandles[symbol]
	if master == nil || confirm == nil {
		return nil
	}

	firstCandle := e.firstCandles[symbol]
	if firstCandle == nil {
		return nil
	}
	fTime := data.NormalizeToIST(firstCandle.Time)
	if fTime.Hour() != 9 || fTime.Minute() != 15 {
		return nil
	}

	// SELL Breakout Trigger
	if master.Close < master.Open {
		if ltp <= confirm.Low {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "SELL",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Fake Breakout SELL trigger: LTP (%.2f) broke Confirmation Low (%.2f). SL: %.2f", ltp, confirm.Low, confirm.High),
				Candle:       confirm,
				StrategyName: "FAKE_BREAKOUT",
			}
		}
	}

	// BUY Breakout Trigger
	if master.Close > master.Open {
		if ltp >= confirm.High {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "BUY",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Fake Breakout BUY trigger: LTP (%.2f) broke Confirmation High (%.2f). SL: %.2f", ltp, confirm.High, confirm.Low),
				Candle:       confirm,
				StrategyName: "FAKE_BREAKOUT",
			}
		}
	}

	return nil
}
