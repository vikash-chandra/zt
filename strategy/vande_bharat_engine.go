package strategy

import (
	"fmt"
	"sync"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// VandeBharatEngine implements the refined Previous Day High/Low Breakout strategy
type VandeBharatEngine struct {
	logger              *zap.Logger
	mu                  sync.RWMutex
	pdHighs             map[string]float64
	pdLows              map[string]float64
	masterCandles       map[string]*data.Candle
	confirmationCandles map[string]*data.Candle
	triggeredTrades     map[string]bool
	rollingCandles      map[string][]data.Candle
	masterMaxPct        float64
	confirmMaxPct       float64
	MinCandlesToIgnore  int
}

// NewVandeBharatEngine creates a new instance of VandeBharatEngine
func NewVandeBharatEngine(logger *zap.Logger, masterMaxPct, confirmMaxPct float64) *VandeBharatEngine {
	return &VandeBharatEngine{
		logger:              logger,
		pdHighs:             make(map[string]float64),
		pdLows:              make(map[string]float64),
		masterCandles:       make(map[string]*data.Candle),
		confirmationCandles: make(map[string]*data.Candle),
		triggeredTrades:     make(map[string]bool),
		rollingCandles:      make(map[string][]data.Candle),
		masterMaxPct:        masterMaxPct,
		confirmMaxPct:       confirmMaxPct,
		MinCandlesToIgnore:  0,
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
	e.mu.Lock()
	defer e.mu.Unlock()

	e.rollingCandles[symbol] = append(e.rollingCandles[symbol], *candle)

	if len(e.rollingCandles[symbol]) == 0 {
		return
	}

	pdh, okHigh := e.pdHighs[symbol]
	pdl, okLow := e.pdLows[symbol]
	if !okHigh || !okLow || pdh <= 0 || pdl <= 0 {
		return // Reference levels not set for this symbol
	}

	// 1. Detect Master Candle
	if e.masterCandles[symbol] == nil {
		// BUY bias: candle close > PDH (must be GREEN: close > open)
		// SELL bias: candle close < PDL (must be RED: close < open)
		isMasterBuy := candle.Close > pdh && candle.Close > candle.Open
		isMasterSell := candle.Close < pdl && candle.Close < candle.Open

		if isMasterBuy || isMasterSell {
			// Validate candle size range percentage limit (High - Low <= masterMaxPct % of Close)
			candleRange := candle.High - candle.Low
			allowedRange := (e.masterMaxPct / 100.0) * candle.Close

			if candleRange <= allowedRange {
				e.masterCandles[symbol] = candle
				direction := "BUY"
				refLevel := pdh
				if isMasterSell {
					direction = "SELL"
					refLevel = pdl
				}
				e.logger.Info("Established Master Candle (VANDE_BHARAT)",
					zap.String("symbol", symbol),
					zap.String("direction", direction),
					zap.Float64("close", candle.Close),
					zap.Float64("ref_level", refLevel),
					zap.Float64("range_pct", (candleRange/candle.Close)*100.0),
				)
			} else {
				e.logger.Warn("Master Candle candidate range too large, ignored",
					zap.String("symbol", symbol),
					zap.Float64("range_pct", (candleRange/candle.Close)*100.0),
				)
			}
		}
		return
	}

	// 2. Detect Confirmation Candle (must be the immediately following candle)
	if e.confirmationCandles[symbol] == nil {
		master := e.masterCandles[symbol]
		isBuySetup := master.Close > pdh

		var confirmed bool
		if isBuySetup {
			// Buy Confirmation: Close > Master High && must be GREEN (Close > Open)
			confirmed = candle.Close > master.High && candle.Close > candle.Open
		} else {
			// Sell Confirmation: Close < Master Low && must be RED (Close < Open)
			confirmed = candle.Close < master.Low && candle.Close < candle.Open
		}

		if confirmed {
			// Validate Confirmation candle range percentage limit (High - Low <= confirmMaxPct % of Close)
			candleRange := candle.High - candle.Low
			allowedRange := (e.confirmMaxPct / 100.0) * candle.Close

			if candleRange <= allowedRange {
				e.confirmationCandles[symbol] = candle
				e.logger.Info("Established Confirmation Candle (VANDE_BHARAT)",
					zap.String("symbol", symbol),
					zap.Float64("close", candle.Close),
					zap.Float64("range_pct", (candleRange/candle.Close)*100.0),
				)
			} else {
				e.logger.Warn("Confirmation Candle range too large, resetting Master",
					zap.String("symbol", symbol),
					zap.Float64("range_pct", (candleRange/candle.Close)*100.0),
				)
				e.masterCandles[symbol] = nil // Reset setup
			}
		} else {
			e.logger.Info("Next candle failed confirmation check, resetting Master",
				zap.String("symbol", symbol),
				zap.Float64("close", candle.Close),
			)
			e.masterCandles[symbol] = nil // Reset setup
		}
		return
	}

	// 3. Trigger window (the 3rd candle) close check.
	// If the Confirmation Candle is established but we didn't trigger a trade during the next candle close,
	// the trigger window has expired. Reset the setup.
	if e.confirmationCandles[symbol] != nil {
		e.logger.Info("Trigger window (3rd candle) completed without breakout, resetting setup",
			zap.String("symbol", symbol),
		)
		e.masterCandles[symbol] = nil
		e.confirmationCandles[symbol] = nil
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

// GetSetupCandle returns the Confirmation Candle as the risk anchor to compute Stop-Loss and targets
func (e *VandeBharatEngine) GetSetupCandle(symbol string) *SetupCandle {
	e.mu.RLock()
	defer e.mu.RUnlock()

	confirm := e.confirmationCandles[symbol]
	if confirm == nil {
		return nil
	}

	// Maps Confirmation Candle properties into SetupCandle for risk management
	return &SetupCandle{
		Candle: *confirm,
		High:   confirm.High,
		Low:    confirm.Low,
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
	e.confirmationCandles = make(map[string]*data.Candle)
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
