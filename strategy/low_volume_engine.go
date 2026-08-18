package strategy

import (
	"fmt"
	"sync"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// LowVolumeEngine implements the LOW_VOLUME breakout strategy
type LowVolumeEngine struct {
	logger             *zap.Logger
	mu                 sync.RWMutex
	rollingCandles     map[string][]data.Candle // symbol -> 5m candles since 09:15 AM today
	setupCandles       map[string]*SetupCandle  // symbol -> active setup candle
	firstCandles       map[string]*data.Candle  // symbol -> 1st 5m candle of day (09:15 AM IST)
	pdHighs            map[string]float64       // symbol -> PDH reference level
	pdLows             map[string]float64       // symbol -> PDL reference level
	triggeredTrades    map[string]bool          // symbol -> whether a trade was triggered today
	MinCandlesToIgnore int
}

// NewLowVolumeEngine creates a new instance of LowVolumeEngine
func NewLowVolumeEngine(logger *zap.Logger) *LowVolumeEngine {
	return &LowVolumeEngine{
		logger:             logger,
		rollingCandles:     make(map[string][]data.Candle),
		setupCandles:       make(map[string]*SetupCandle),
		firstCandles:       make(map[string]*data.Candle),
		pdHighs:            make(map[string]float64),
		pdLows:             make(map[string]float64),
		triggeredTrades:    make(map[string]bool),
		MinCandlesToIgnore: 0,
	}
}

// Name returns the strategy name
func (e *LowVolumeEngine) Name() string {
	return "LOW_VOLUME"
}

// SetPreviousDayHighLow binds the reference PDH and PDL levels for a symbol
func (e *LowVolumeEngine) SetPreviousDayHighLow(symbol string, high float64, low float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pdHighs[symbol] = high
	e.pdLows[symbol] = low
	e.logger.Info("Low Volume reference levels configured",
		zap.String("symbol", symbol),
		zap.Float64("pdh", high),
		zap.Float64("pdl", low),
	)
}

func (e *LowVolumeEngine) OnCandleClose(candle *data.Candle, symbol string) {
	candleTimeIST := candle.Time.In(data.ISTLocation)
	marketStart := time.Date(candleTimeIST.Year(), candleTimeIST.Month(), candleTimeIST.Day(), 9, 15, 0, 0, data.ISTLocation)
	if candleTimeIST.Before(marketStart) && candleTimeIST.Hour() < 9 {
		return // Discard pre-market candles before 09:15 AM IST
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Append candle to history
	e.rollingCandles[symbol] = append(e.rollingCandles[symbol], *candle)
	candles := e.rollingCandles[symbol]

	if len(candles) == 0 {
		return
	}

	// Record 1st candle of the day (09:15 AM IST)
	if len(candles) == 1 {
		e.firstCandles[symbol] = candle
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

// CheckBreakout checks if the live LTP breaks the Setup Candle's bounds based on 1st candle PDH/PDL qualification
func (e *LowVolumeEngine) CheckBreakout(symbol string, ltp float64, bias string) *Signal {
	e.mu.Lock()
	defer e.mu.Unlock()

	// If already triggered a trade for this symbol today, do not trigger again
	if e.triggeredTrades[symbol] {
		return nil
	}

	candles := e.rollingCandles[symbol]
	if len(candles) == 0 || len(candles) < e.MinCandlesToIgnore {
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

	pdh, okHigh := e.pdHighs[symbol]
	pdl, okLow := e.pdLows[symbol]
	if !okHigh || !okLow || pdh <= 0 || pdl <= 0 {
		return nil // Reference levels not set for this symbol
	}

	firstCandle := e.firstCandles[symbol]
	if firstCandle == nil {
		return nil
	}

	// 1st Candle Qualification Rules:
	// BUY allowed ONLY if 1st candle closed > PDH
	// SELL allowed ONLY if 1st candle closed < PDL
	isBuyQualified := firstCandle.Close > pdh
	isSellQualified := firstCandle.Close < pdl

	if !isBuyQualified && !isSellQualified {
		return nil // 1st candle closed between PDH and PDL, no trade allowed today
	}

	// Long Entry Setup: 1st candle > PDH, Setup Candle must be RED (Close < Open)
	if isBuyQualified && setup.Candle.Close < setup.Candle.Open {
		if ltp > setup.High {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "BUY",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %f broke above RED Setup Candle High %f (1st candle close %f > PDH %f)", ltp, setup.High, firstCandle.Close, pdh),
				Candle:       &setup.Candle,
				StrategyName: e.Name(),
			}
		}
	}

	// Short Entry Setup: 1st candle < PDL, Setup Candle must be GREEN (Close > Open)
	if isSellQualified && setup.Candle.Close > setup.Candle.Open {
		if ltp < setup.Low {
			e.triggeredTrades[symbol] = true
			return &Signal{
				Symbol:       symbol,
				Action:       "SELL",
				Strength:     1.0,
				Reason:       fmt.Sprintf("Price %f broke below GREEN Setup Candle Low %f (1st candle close %f < PDL %f)", ltp, setup.Low, firstCandle.Close, pdl),
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

// Reset clears the strategy engine state for a new day
func (e *LowVolumeEngine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.rollingCandles = make(map[string][]data.Candle)
	e.setupCandles = make(map[string]*SetupCandle)
	e.firstCandles = make(map[string]*data.Candle)
	e.pdHighs = make(map[string]float64)
	e.pdLows = make(map[string]float64)
	e.triggeredTrades = make(map[string]bool)
	e.logger.Info("LOW_VOLUME strategy engine state reset successfully")
}

// RestoreTriggeredTrade registers an already triggered trade for a symbol
func (e *LowVolumeEngine) RestoreTriggeredTrade(symbol string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.triggeredTrades[symbol] = true
	e.logger.Info("LOW_VOLUME: Restored triggered trade state", zap.String("symbol", symbol))
}
