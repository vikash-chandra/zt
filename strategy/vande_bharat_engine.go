package strategy

import (
	"fmt"
	"sync"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// VandeBharatEngine implements the 15-minute Opening Range Breakout (ORB) strategy
type VandeBharatEngine struct {
	logger          *zap.Logger
	mu              sync.RWMutex
	rollingCandles  map[string][]data.Candle // symbol -> 5m candles since 09:15 AM
	setupRanges     map[string]*SetupCandle  // symbol -> 15m range (SetupCandle.High/SetupCandle.Low)
	triggeredTrades map[string]bool          // symbol -> whether trade triggered today
}

// NewVandeBharatEngine creates a new instance of VandeBharatEngine
func NewVandeBharatEngine(logger *zap.Logger) *VandeBharatEngine {
	return &VandeBharatEngine{
		logger:          logger,
		rollingCandles:  make(map[string][]data.Candle),
		setupRanges:     make(map[string]*SetupCandle),
		triggeredTrades: make(map[string]bool),
	}
}

// Name returns the strategy name
func (e *VandeBharatEngine) Name() string {
	return "VANDE_BHARAT"
}

// OnCandleClose processes incoming 5-minute candles to establish the 15m opening range
func (e *VandeBharatEngine) OnCandleClose(candle *data.Candle, symbol string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Append candle
	e.rollingCandles[symbol] = append(e.rollingCandles[symbol], *candle)
	candles := e.rollingCandles[symbol]

	// The first 15 minutes of the session (09:15 to 09:30) comprise exactly the first 3 candles
	if len(candles) == 3 && e.setupRanges[symbol] == nil {
		var maxHigh float64 = -1
		var minLow float64 = -1
		var totalVolume int64 = 0

		for _, c := range candles {
			if maxHigh == -1 || c.High > maxHigh {
				maxHigh = c.High
			}
			if minLow == -1 || c.Low < minLow {
				minLow = c.Low
			}
			totalVolume += c.Volume
		}

		// Save the 15m range parameters inside a SetupCandle structure for risk manager compatibility
		e.setupRanges[symbol] = &SetupCandle{
			Candle: candles[2], // Anchor on the 09:25-09:30 close candle
			High:   maxHigh,
			Low:    minLow,
			Volume: totalVolume,
		}

		e.logger.Info("Established 15-Minute Opening Range (VANDE_BHARAT)",
			zap.String("symbol", symbol),
			zap.Float64("high", maxHigh),
			zap.Float64("low", minLow),
			zap.Int64("total_volume", totalVolume),
		)
	}
}

// CheckBreakout checks if the live LTP breaks out of the 15-minute range
func (e *VandeBharatEngine) CheckBreakout(symbol string, ltp float64, bias string) *Signal {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Prevent duplicate trades on same stock today
	if e.triggeredTrades[symbol] {
		return nil
	}

	setup := e.setupRanges[symbol]
	if setup == nil {
		return nil // 15m range not yet established
	}

	// Long Breakout: LTP breaks above 15m high under BUY bias
	if bias == "BUY_ONLY" {
		if ltp > setup.High {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "BUY",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %f broke above 15m ORB High %f (VANDE_BHARAT)", ltp, setup.High),
				Candle:       &setup.Candle,
				StrategyName: e.Name(),
			}
		}
	}

	// Short Breakout: LTP breaks below 15m low under SELL bias
	if bias == "SELL_ONLY" {
		if ltp < setup.Low {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "SELL",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %f broke below 15m ORB Low %f (VANDE_BHARAT)", ltp, setup.Low),
				Candle:       &setup.Candle,
				StrategyName: e.Name(),
			}
		}
	}

	return nil
}

// GetSetupCandle returns the active setup bounds (opening range)
func (e *VandeBharatEngine) GetSetupCandle(symbol string) *SetupCandle {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.setupRanges[symbol]
}

// Reset clears the strategy engine state for a new day
func (e *VandeBharatEngine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.rollingCandles = make(map[string][]data.Candle)
	e.setupRanges = make(map[string]*SetupCandle)
	e.triggeredTrades = make(map[string]bool)
	e.logger.Info("VANDE_BHARAT strategy engine state reset successfully")
}
