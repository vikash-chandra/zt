package strategy

import (
	"math"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// Indicators calculates technical indicators
type Indicators struct {
	logger     *zap.Logger
	vwapWindow int
	atrPeriod  int
	obiWindow  int
}

// NewIndicators creates new indicators calculator
func NewIndicators(logger *zap.Logger, vwapWindow, atrPeriod, obiWindow int) *Indicators {
	return &Indicators{
		logger:     logger,
		vwapWindow: vwapWindow,
		atrPeriod:  atrPeriod,
		obiWindow:  obiWindow,
	}
}

// CalculateVWAP calculates Volume Weighted Average Price
func (ind *Indicators) CalculateVWAP(candles []data.Candle) []float64 {
	vwaps := make([]float64, len(candles))
	cumPV := 0.0
	cumVol := 0.0

	for i, candle := range candles {
		tp := (candle.High + candle.Low + candle.Close) / 3.0
		pv := tp * float64(candle.Volume)
		cumPV += pv
		cumVol += float64(candle.Volume)

		if cumVol > 0 {
			vwaps[i] = cumPV / cumVol
		} else {
			vwaps[i] = tp
		}
	}

	return vwaps
}

// CalculateATR calculates Average True Range
func (ind *Indicators) CalculateATR(candles []data.Candle) []float64 {
	atrs := make([]float64, len(candles))
	trs := make([]float64, len(candles))

	for i, candle := range candles {
		var tr float64
		if i == 0 {
			tr = candle.High - candle.Low
		} else {
			prevClose := candles[i-1].Close
			tr = math.Max(
				candle.High-candle.Low,
				math.Max(
					math.Abs(candle.High-prevClose),
					math.Abs(candle.Low-prevClose),
				),
			)
		}
		trs[i] = tr

		if i < ind.atrPeriod-1 {
			sum := 0.0
			for j := 0; j <= i; j++ {
				sum += trs[j]
			}
			atrs[i] = sum / float64(i+1)
		} else {
			sum := 0.0
			for j := i - ind.atrPeriod + 1; j <= i; j++ {
				sum += trs[j]
			}
			atrs[i] = sum / float64(ind.atrPeriod)
		}
	}

	return atrs
}

// CalculateStdDev calculates standard deviation of closes
func (ind *Indicators) CalculateStdDev(closes []float64) float64 {
	if len(closes) == 0 {
		return 0
	}

	// Calculate mean
	mean := 0.0
	for _, c := range closes {
		mean += c
	}
	mean /= float64(len(closes))

	// Calculate variance
	variance := 0.0
	for _, c := range closes {
		diff := c - mean
		variance += diff * diff
	}
	variance /= float64(len(closes))

	return math.Sqrt(variance)
}

// CalculateOBI calculates Order Book Imbalance
func (ind *Indicators) CalculateOBI(bidVolume, askVolume float64) float64 {
	total := bidVolume + askVolume
	if total == 0 {
		return 0
	}
	return (bidVolume - askVolume) / total
}

// CalculateRSI calculates Relative Strength Index using Wilder's Smoothing
func (ind *Indicators) CalculateRSI(closes []float64, period int) float64 {
	if len(closes) < period+1 {
		return 50.0 // Default neutral
	}

	var avgGain, avgLoss float64

	// First calculation: Simple average of first 'period' changes
	for i := 1; i <= period; i++ {
		change := closes[i] - closes[i-1]
		if change > 0 {
			avgGain += change
		} else {
			avgLoss += -change
		}
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)

	// Subsequent calculations: Wilder's Smoothing
	for i := period + 1; i < len(closes); i++ {
		change := closes[i] - closes[i-1]
		var gain, loss float64
		if change > 0 {
			gain = change
		} else {
			loss = -change
		}
		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
	}

	if avgLoss == 0 {
		return 100.0
	}

	rs := avgGain / avgLoss
	return 100.0 - (100.0 / (1.0 + rs))
}

// CalculateBollingerBands calculates Bollinger Bands
func (ind *Indicators) CalculateBollingerBands(closes []float64, period int, numStdDev float64) (sma, upper, lower float64) {
	if len(closes) < period {
		return 0, 0, 0
	}

	// Calculate SMA
	sum := 0.0
	for i := len(closes) - period; i < len(closes); i++ {
		sum += closes[i]
	}
	sma = sum / float64(period)

	// Calculate std dev
	variance := 0.0
	for i := len(closes) - period; i < len(closes); i++ {
		diff := closes[i] - sma
		variance += diff * diff
	}
	variance /= float64(period)
	stdDev := math.Sqrt(variance)

	upper = sma + (numStdDev * stdDev)
	lower = sma - (numStdDev * stdDev)

	return sma, upper, lower
}
