package strategy

import (
	"math"
	"sync"
	"zerodha-trading/data"
)

// SuperTrendParams holds period and multiplier for a SuperTrend indicator
type SuperTrendParams struct {
	Period     int
	Multiplier float64
}

// SuperTrendValue holds calculated upper/lower bands and trend direction (+1 Bullish, -1 Bearish)
type SuperTrendValue struct {
	Upper     float64
	Lower     float64
	Value     float64
	Direction int // +1 = Bullish, -1 = Bearish
}

// TripleSuperTrendResult holds values for ST1, ST2, ST3 and combined Trend
type TripleSuperTrendResult struct {
	ST1   SuperTrendValue
	ST2   SuperTrendValue
	ST3   SuperTrendValue
	Trend string // "BULLISH", "BEARISH", "NEUTRAL"
}

// SuperTrendOptionsEngine calculates 3 SuperTrends on 5m candles and classifies trends
type SuperTrendOptionsEngine struct {
	mu  sync.RWMutex
	st1 SuperTrendParams
	st2 SuperTrendParams
	st3 SuperTrendParams
}

// NewSuperTrendOptionsEngine creates a new Triple SuperTrend Engine
func NewSuperTrendOptionsEngine(st1P, st2P, st3P int, st1M, st2M, st3M float64) *SuperTrendOptionsEngine {
	return &SuperTrendOptionsEngine{
		st1: SuperTrendParams{Period: st1P, Multiplier: st1M},
		st2: SuperTrendParams{Period: st2P, Multiplier: st2M},
		st3: SuperTrendParams{Period: st3P, Multiplier: st3M},
	}
}

// CalculateTripleSuperTrend calculates ST1, ST2, ST3 on a slice of 5-minute candles
func (e *SuperTrendOptionsEngine) CalculateTripleSuperTrend(candles []data.Candle) *TripleSuperTrendResult {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(candles) == 0 {
		return &TripleSuperTrendResult{Trend: "NEUTRAL"}
	}

	st1Res := calculateSingleSuperTrend(candles, e.st1.Period, e.st1.Multiplier)
	st2Res := calculateSingleSuperTrend(candles, e.st2.Period, e.st2.Multiplier)
	st3Res := calculateSingleSuperTrend(candles, e.st3.Period, e.st3.Multiplier)

	lastCandle := candles[len(candles)-1]

	var trend string
	if lastCandle.Close > st1Res.Value && lastCandle.Close > st2Res.Value && lastCandle.Close > st3Res.Value {
		trend = "BULLISH"
	} else if lastCandle.Close < st1Res.Value && lastCandle.Close < st2Res.Value && lastCandle.Close < st3Res.Value {
		trend = "BEARISH"
	} else {
		trend = "NEUTRAL"
	}

	return &TripleSuperTrendResult{
		ST1:   st1Res,
		ST2:   st2Res,
		ST3:   st3Res,
		Trend: trend,
	}
}

// calculateSingleSuperTrend computes SuperTrend for a single (Period, Multiplier) parameter set
func calculateSingleSuperTrend(candles []data.Candle, period int, multiplier float64) SuperTrendValue {
	n := len(candles)
	if n < period {
		last := candles[n-1]
		return SuperTrendValue{
			Upper:     last.High,
			Lower:     last.Low,
			Value:     last.Close,
			Direction: 0,
		}
	}

	// 1. Calculate True Range (TR)
	tr := make([]float64, n)
	tr[0] = candles[0].High - candles[0].Low
	for i := 1; i < n; i++ {
		h := candles[i].High
		l := candles[i].Low
		pc := candles[i-1].Close
		tr[i] = math.Max(h-l, math.Max(math.Abs(h-pc), math.Abs(l-pc)))
	}

	// 2. Calculate ATR using Wilder's Smoothing
	atr := make([]float64, n)
	sumTR := 0.0
	for i := 0; i < period; i++ {
		sumTR += tr[i]
	}
	atr[period-1] = sumTR / float64(period)
	for i := period; i < n; i++ {
		atr[i] = (atr[i-1]*float64(period-1) + tr[i]) / float64(period)
	}

	// 3. Compute Basic Bands and Final Clamped Bands
	upper := make([]float64, n)
	lower := make([]float64, n)
	stValue := make([]float64, n)
	dir := make([]int, n)

	for i := period - 1; i < n; i++ {
		hl2 := (candles[i].High + candles[i].Low) / 2.0
		basicUpper := hl2 + (multiplier * atr[i])
		basicLower := hl2 - (multiplier * atr[i])

		if i == period-1 {
			upper[i] = basicUpper
			lower[i] = basicLower
			if candles[i].Close > upper[i] {
				dir[i] = 1
				stValue[i] = lower[i]
			} else {
				dir[i] = -1
				stValue[i] = upper[i]
			}
			continue
		}

		// Clamp Upper Band
		if basicUpper < upper[i-1] || candles[i-1].Close > upper[i-1] {
			upper[i] = basicUpper
		} else {
			upper[i] = upper[i-1]
		}

		// Clamp Lower Band
		if basicLower > lower[i-1] || candles[i-1].Close < lower[i-1] {
			lower[i] = basicLower
		} else {
			lower[i] = lower[i-1]
		}

		// Determine Direction & ST Value
		if dir[i-1] == 1 {
			if candles[i].Close < lower[i] {
				dir[i] = -1
				stValue[i] = upper[i]
			} else {
				dir[i] = 1
				stValue[i] = lower[i]
			}
		} else {
			if candles[i].Close > upper[i] {
				dir[i] = 1
				stValue[i] = lower[i]
			} else {
				dir[i] = -1
				stValue[i] = upper[i]
			}
		}
	}

	lastIdx := n - 1
	return SuperTrendValue{
		Upper:     upper[lastIdx],
		Lower:     lower[lastIdx],
		Value:     stValue[lastIdx],
		Direction: dir[lastIdx],
	}
}
