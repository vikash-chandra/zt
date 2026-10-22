package scanner

import (
	"testing"
	"time"
	"zerodha-trading/data"
)

func TestDetectFractalPivots(t *testing.T) {
	// Create synthetic candles with clear high and low pivots
	baseTime := time.Date(2026, 8, 1, 9, 15, 0, 0, time.UTC)
	var candles []data.Candle

	// 10 candles with a peak at index 3 and a trough at index 7
	prices := []struct{ high, low, close float64 }{
		{100, 95, 98},
		{102, 97, 100},
		{105, 100, 103},
		{115, 104, 110}, // Swing High
		{106, 101, 102},
		{104, 98, 99},
		{99, 92, 94},
		{95, 85, 90}, // Swing Low
		{98, 89, 96},
		{102, 94, 100},
	}

	for i, p := range prices {
		candles = append(candles, data.Candle{
			Time:  baseTime.AddDate(0, 0, i),
			High:  p.high,
			Low:   p.low,
			Close: p.close,
		})
	}

	highs, lows := DetectFractalPivots(candles)
	if len(highs) == 0 {
		t.Fatalf("expected at least 1 swing high, got 0")
	}
	if highs[0].Price != 115 {
		t.Errorf("expected swing high price 115, got %f", highs[0].Price)
	}

	if len(lows) == 0 {
		t.Fatalf("expected at least 1 swing low, got 0")
	}
	if lows[0].Price != 85 {
		t.Errorf("expected swing low price 85, got %f", lows[0].Price)
	}
}

func TestEvaluateDowStructure_BreakoutBuy(t *testing.T) {
	baseTime := time.Date(2026, 8, 1, 9, 15, 0, 0, time.UTC)
	var candles []data.Candle

	// Trending candles with breakout on last candle
	for i := 0; i < 20; i++ {
		candles = append(candles, data.Candle{
			Time:   baseTime.AddDate(0, 0, i),
			Open:   100.0 + float64(i)*2.0,
			High:   102.0 + float64(i)*2.0,
			Low:    99.0 + float64(i)*2.0,
			Close:  101.0 + float64(i)*2.0,
			Volume: 10000,
		})
	}
	// Last candle breaks out strongly with volume
	candles = append(candles, data.Candle{
		Time:   baseTime.AddDate(0, 0, 20),
		Open:   142.0,
		High:   155.0,
		Low:    141.0,
		Close:  153.0,
		Volume: 35000, // 3.5x ADV
	})

	res := EvaluateDowStructure(candles, 35000, 10000)
	if res.PositionalZone != "BREAKOUT_BUY" {
		t.Errorf("expected PositionalZone BREAKOUT_BUY, got %s", res.PositionalZone)
	}
	if res.ActionTiming != "TODAY_ACTIONABLE" {
		t.Errorf("expected ActionTiming TODAY_ACTIONABLE, got %s", res.ActionTiming)
	}
	if res.SelectionReason == "" {
		t.Errorf("expected non-empty SelectionReason")
	}
}

func TestEvaluateDowStructure_PullbackBuy(t *testing.T) {
	baseTime := time.Date(2026, 8, 1, 9, 15, 0, 0, time.UTC)
	var candles []data.Candle

	// Generate uptrend
	for i := 0; i < 25; i++ {
		p := 100.0 + float64(i)*3.0
		candles = append(candles, data.Candle{
			Time:   baseTime.AddDate(0, 0, i),
			Open:   p,
			High:   p + 2.0,
			Low:    p - 1.0,
			Close:  p + 1.5,
			Volume: 10000,
		})
	}

	// Pullback into 20 EMA with green bounce
	ema20 := CalculateEMA(candles, 20)
	candles = append(candles, data.Candle{
		Time:   baseTime.AddDate(0, 0, 25),
		Open:   ema20 + 0.2,
		High:   ema20 + 2.0,
		Low:    ema20 - 0.5,
		Close:  ema20 + 1.8,
		Volume: 15000,
	})

	res := EvaluateDowStructure(candles, 15000, 10000)
	if res.DowTrend != "UPTREND_HH_HL" {
		t.Errorf("expected DowTrend UPTREND_HH_HL, got %s", res.DowTrend)
	}
	if res.PositionalZone != "PULLBACK_BUY" && res.PositionalZone != "BREAKOUT_BUY" {
		t.Errorf("expected PULLBACK_BUY or BREAKOUT_BUY, got %s", res.PositionalZone)
	}
}

func TestDetectSwingDoji(t *testing.T) {
	// 1. Swing High with Gravestone/Shooting Star Doji (High wick rejection at recent high)
	highDojiCandle := data.Candle{
		Open:  199.5,
		High:  210.0, // Long upper wick (10 pts)
		Low:   199.0, // Small lower wick (0.5 pts)
		Close: 200.0, // Body = 0.5 pts, Range = 11.0 pts => Wick = 95.4%
	}
	recentHigh := 210.0
	recentLow := 170.0

	isDoji, dojiType, wickPct, upperPct, _, _ := DetectSwingDoji(highDojiCandle, recentHigh, recentLow)
	if !isDoji {
		t.Errorf("expected isDoji=true for Swing High Doji, got false")
	}
	if dojiType != "SWING_HIGH_DOJI" {
		t.Errorf("expected SWING_HIGH_DOJI, got %s", dojiType)
	}
	if wickPct < 80.0 {
		t.Errorf("expected wickPct > 80%%, got %.2f%%", wickPct)
	}
	if upperPct < 80.0 {
		t.Errorf("expected upperPct > 80%%, got %.2f%%", upperPct)
	}

	// 2. Swing Low with Dragonfly/Hammer Doji (Long lower wick rejection at recent low)
	lowDojiCandle := data.Candle{
		Open:  100.5,
		High:  101.0, // Small upper wick (0.5 pts)
		Low:   90.0,  // Long lower wick (10 pts)
		Close: 100.0, // Body = 0.5 pts, Range = 11.0 pts => Wick = 95.4%
	}
	recentHigh2 := 140.0
	recentLow2 := 90.0

	isDoji2, dojiType2, wickPct2, _, lowerPct2, _ := DetectSwingDoji(lowDojiCandle, recentHigh2, recentLow2)
	if !isDoji2 {
		t.Errorf("expected isDoji=true for Swing Low Doji, got false")
	}
	if dojiType2 != "SWING_LOW_DOJI" {
		t.Errorf("expected SWING_LOW_DOJI, got %s", dojiType2)
	}
	if lowerPct2 < 80.0 {
		t.Errorf("expected lowerPct > 80%%, got %.2f%%", lowerPct2)
	}
	if wickPct2 < 80.0 {
		t.Errorf("expected wickPct > 80%%, got %.2f%%", wickPct2)
	}

	// 3. Normal Trend Candle (Big body, small wicks) - should NOT trigger
	normalCandle := data.Candle{
		Open:  100.0,
		High:  110.0,
		Low:   99.0,
		Close: 109.0, // Body = 9 pts, Range = 11 pts => Wick = 18.2%
	}
	isDoji3, _, _, _, _, _ := DetectSwingDoji(normalCandle, 110.0, 90.0)
	if isDoji3 {
		t.Errorf("expected isDoji=false for normal candle, got true")
	}
}
