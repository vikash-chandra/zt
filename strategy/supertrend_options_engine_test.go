package strategy

import (
	"testing"
	"time"
	"zerodha-trading/data"
)

func TestSuperTrendOptionsEngine(t *testing.T) {
	engine := NewSuperTrendOptionsEngine(10, 7, 7, 4.0, 3.0, 2.0)

	// Create synthetic bullish trending candles
	candles := make([]data.Candle, 20)
	baseTime := time.Now().Add(-100 * time.Minute)

	for i := 0; i < 20; i++ {
		price := 100.0 + float64(i)*2.0 // Strong uptrend
		candles[i] = data.Candle{
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   price - 0.5,
			High:   price + 1.5,
			Low:    price - 1.0,
			Close:  price + 1.0,
			Volume: 1000,
		}
	}

	res := engine.CalculateTripleSuperTrend(candles)
	if res.Trend != "BULLISH" {
		t.Fatalf("expected BULLISH trend for strong uptrend candles, got %s", res.Trend)
	}

	// Create synthetic bearish trending candles
	bearishCandles := make([]data.Candle, 20)
	for i := 0; i < 20; i++ {
		price := 200.0 - float64(i)*3.0 // Strong downtrend
		bearishCandles[i] = data.Candle{
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   price + 0.5,
			High:   price + 1.0,
			Low:    price - 1.5,
			Close:  price - 1.0,
			Volume: 1000,
		}
	}

	bearRes := engine.CalculateTripleSuperTrend(bearishCandles)
	if bearRes.Trend != "BEARISH" {
		t.Fatalf("expected BEARISH trend for strong downtrend candles, got %s", bearRes.Trend)
	}
}
