package strategy

import (
	"sync"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// Signal represents a trading signal
type Signal struct {
	Symbol   string
	Action   string  // BUY, SELL, HOLD
	Strength float64 // 0-1, confidence level
	Reason   string
	Candle   *data.Candle
}

// StrategyEngine generates trading signals
type StrategyEngine struct {
	indicators      *Indicators
	logger          *zap.Logger
	signals         chan Signal
	rollingCandles  map[int64][]data.Candle
	mu              sync.RWMutex
	maxCandleBuffer int
}

// NewStrategyEngine creates new strategy engine
func NewStrategyEngine(indicators *Indicators, logger *zap.Logger, bufferSize int) *StrategyEngine {
	return &StrategyEngine{
		indicators:      indicators,
		logger:          logger,
		signals:         make(chan Signal, bufferSize),
		rollingCandles:  make(map[int64][]data.Candle),
		maxCandleBuffer: 100,
	}
}

// OnCandleClose processes a completed candle and generates signals
func (se *StrategyEngine) OnCandleClose(candle *data.Candle) *Signal {
	se.mu.Lock()
	se.rollingCandles[candle.Token] = append(se.rollingCandles[candle.Token], *candle)

	// Keep only last N candles
	if len(se.rollingCandles[candle.Token]) > se.maxCandleBuffer {
		se.rollingCandles[candle.Token] = se.rollingCandles[candle.Token][1:]
	}

	candles := se.rollingCandles[candle.Token]
	se.mu.Unlock()

	if len(candles) < 20 {
		return nil // Not enough data
	}

	// Reverse candles for analysis (newest at end)
	closeCopy := make([]data.Candle, len(candles))
	copy(closeCopy, candles)

	// Calculate indicators
	closes := extractCloses(closeCopy)
	vwaps := se.indicators.CalculateVWAP(closeCopy)
	atrs := se.indicators.CalculateATR(closeCopy)
	rsi := se.indicators.CalculateRSI(closes, 14)
	stdDev := se.indicators.CalculateStdDev(closes)

	currentVWAP := vwaps[len(vwaps)-1]
	currentATR := atrs[len(atrs)-1]
	currentClose := closes[len(closes)-1]

	// Generate signal based on VWAP + RSI
	signal := se.vwapRSISignal(candle.Token, currentClose, currentVWAP, stdDev, rsi, currentATR, *candle)

	if signal.Action != "HOLD" {
		se.logger.Info("Signal Generated",
			zap.String("symbol", signal.Symbol),
			zap.String("action", signal.Action),
			zap.Float64("strength", signal.Strength),
			zap.String("reason", signal.Reason),
		)

		select {
		case se.signals <- *signal:
		default:
			se.logger.Warn("Signals channel full, dropping signal")
		}
	}

	return signal
}

// vwapRSISignal generates signal based on VWAP and RSI
func (se *StrategyEngine) vwapRSISignal(token int64, price, vwap, stdDev, rsi, atr float64, candle data.Candle) *Signal {
	signal := &Signal{
		Symbol: "TOKEN_" + string(rune(token)),
		Action: "HOLD",
		Candle: &candle,
	}

	// BUY signal: Price below VWAP + RSI oversold
	if price < vwap-(1.5*stdDev) && rsi < 30 {
		signal.Action = "BUY"
		signal.Strength = 0.8
		signal.Reason = "Price oversold below VWAP, RSI < 30 (mean reversion)"
		return signal
	}

	// SELL signal: Price above VWAP + RSI overbought
	if price > vwap+(1.5*stdDev) && rsi > 70 {
		signal.Action = "SELL"
		signal.Strength = 0.8
		signal.Reason = "Price overbought above VWAP, RSI > 70"
		return signal
	}

	// Weak BUY: Near VWAP with good RSI
	if price < vwap && rsi < 50 && rsi > 20 {
		signal.Action = "BUY"
		signal.Strength = 0.5
		signal.Reason = "Price near VWAP support, moderate RSI"
		return signal
	}

	// Weak SELL: Near VWAP with high RSI
	if price > vwap && rsi > 50 && rsi < 80 {
		signal.Action = "SELL"
		signal.Strength = 0.5
		signal.Reason = "Price near VWAP resistance, moderate RSI"
		return signal
	}

	return signal
}

// GetSignals returns channel for signals
func (se *StrategyEngine) GetSignals() <-chan Signal {
	return se.signals
}

// GetRollingCandles returns rolling candles for a token
func (se *StrategyEngine) GetRollingCandles(token int64) []data.Candle {
	se.mu.RLock()
	defer se.mu.RUnlock()

	if candles, exists := se.rollingCandles[token]; exists {
		result := make([]data.Candle, len(candles))
		copy(result, candles)
		return result
	}
	return nil
}

// extractCloses extracts close prices from candles
func extractCloses(candles []data.Candle) []float64 {
	closes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
	}
	return closes
}
