package strategy

import (
	"fmt"
	"sync"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// LowVolumeEngine implements the LOW_VOLUME breakout strategy
type LowVolumeEngine struct {
	logger             *zap.Logger
	mu                 sync.RWMutex
	rollingCandles     map[string][]data.Candle // symbol -> 5m candles since 09:15 AM today
	setupCandles       map[string]*SetupCandle  // symbol -> active setup candle
	triggeredTrades    map[string]bool          // symbol -> whether a trade was triggered today
	MinCandlesToIgnore int
}

// NewLowVolumeEngine creates a new instance of LowVolumeEngine
func NewLowVolumeEngine(logger *zap.Logger) *LowVolumeEngine {
	return &LowVolumeEngine{
		logger:             logger,
		rollingCandles:     make(map[string][]data.Candle),
		setupCandles:       make(map[string]*SetupCandle),
		triggeredTrades:    make(map[string]bool),
		MinCandlesToIgnore: 0,
	}
}

// Name returns the strategy name
func (e *LowVolumeEngine) Name() string {
	return "LOW_VOLUME"
}

// OnCandleClose is called every time a 5-minute candle closes for a stock
func (e *LowVolumeEngine) OnCandleClose(candle *data.Candle, symbol string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Append candle to history
	e.rollingCandles[symbol] = append(e.rollingCandles[symbol], *candle)
	candles := e.rollingCandles[symbol]

	if len(candles) == 0 {
		return
	}

	// Identify the Setup Candle: Find the candle with the absolute lowest volume since 09:15 AM
	var lowestVolIdx int = -1
	var lowestVol int64 = -1

	for idx, c := range candles {
		if lowestVol == -1 || c.Volume < lowestVol {
			lowestVol = c.Volume
			lowestVolIdx = idx
		}
	}

	if lowestVolIdx != -1 {
		setupCandle := candles[lowestVolIdx]
		e.setupCandles[symbol] = &SetupCandle{
			Candle: setupCandle,
			High:   setupCandle.High,
			Low:    setupCandle.Low,
			Volume: setupCandle.Volume,
		}
		e.logger.Info("Updated Setup Candle (LOW_VOLUME)",
			zap.String("symbol", symbol),
			zap.Float64("high", setupCandle.High),
			zap.Float64("low", setupCandle.Low),
			zap.Int64("volume", setupCandle.Volume),
			zap.Time("time", setupCandle.Time),
		)
	}
}

// CheckBreakout checks if the live LTP breaks the Setup Candle's bounds
func (e *LowVolumeEngine) CheckBreakout(symbol string, ltp float64, bias string) *Signal {
	e.mu.Lock()
	defer e.mu.Unlock()

	// If already triggered a trade for this symbol today, do not trigger again
	if e.triggeredTrades[symbol] {
		return nil
	}

	candles := e.rollingCandles[symbol]
	if len(candles) < e.MinCandlesToIgnore {
		return nil
	}
	lastCandle := candles[len(candles)-1]

	setup := e.setupCandles[symbol]
	if setup == nil {
		return nil
	}

	// Only consider the setup candle if it is the immediately previous completed candle
	if !setup.Candle.Time.Equal(lastCandle.Time) {
		return nil
	}

	// Long Entry Setup: Global Bias = BUY_ONLY, Setup Candle must be RED (Close < Open)
	if bias == "BUY_ONLY" && setup.Candle.Close < setup.Candle.Open {
		if ltp > setup.High {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "BUY",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %f broke above RED Setup Candle High %f (Volume: %d)", ltp, setup.High, setup.Volume),
				Candle:       &setup.Candle,
				StrategyName: e.Name(),
			}
		}
	}

	// Short Entry Setup: Global Bias = SELL_ONLY, Setup Candle must be GREEN (Close > Open)
	if bias == "SELL_ONLY" && setup.Candle.Close > setup.Candle.Open {
		if ltp < setup.Low {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "SELL",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %f broke below GREEN Setup Candle Low %f (Volume: %d)", ltp, setup.Low, setup.Volume),
				Candle:       &setup.Candle,
				StrategyName: e.Name(),
			}
		}
	}

	return nil
}

// GetSetupCandle returns the active setup candle for a stock
func (e *LowVolumeEngine) GetSetupCandle(symbol string) *SetupCandle {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.setupCandles[symbol]
}

// Reset clears the engine's internal state for a new day
func (e *LowVolumeEngine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.rollingCandles = make(map[string][]data.Candle)
	e.setupCandles = make(map[string]*SetupCandle)
	e.triggeredTrades = make(map[string]bool)
	e.logger.Info("LOW_VOLUME strategy engine state reset successfully")
}
