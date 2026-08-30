package strategy

import (
	"math"
	"testing"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

func TestLowVolumeEngine(t *testing.T) {
	logger := zap.NewNop()
	engine := NewLowVolumeEngine(logger)

	if engine.Name() != "LOW_VOLUME" {
		t.Errorf("expected strategy name LOW_VOLUME, got %s", engine.Name())
	}

	symbol := "INFY"
	engine.SetPreviousDayHighLow(symbol, 95.0, 85.0)

	baseTime := time.Date(2026, 8, 18, 9, 15, 0, 0, data.ISTLocation)

	// 1. Send first candle (Close 101.0 > PDH 95.0 -> BUY Qualified)
	c1 := &data.Candle{
		Token:  12345,
		Time:   baseTime,
		Open:   100.0,
		High:   105.0,
		Low:    95.0,
		Close:  101.0,
		Volume: 1000,
	}
	engine.OnCandleClose(c1, symbol)

	// 2. Check no signal before setup candle is identified
	sig := engine.CheckBreakout(symbol, 106.0, "BUY_ONLY")
	if sig != nil {
		t.Errorf("expected nil signal before setup candle is active, got %v", sig)
	}

	// 3. Send second candle (lower volume)
	c2 := &data.Candle{
		Token:  12345,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   101.0,
		High:   102.0,
		Low:    98.0,
		Close:  99.0, // RED setup candle
		Volume: 500,  // Lower volume
	}
	engine.OnCandleClose(c2, symbol)

	// In LowVolume strategy, the setup candle is verified at the end of the window.
	// Since we are only feeding 2 candles, the lowest volume is c2 (500 volume).
	// Let's retrieve setup candle.
	setup := engine.GetSetupCandle(symbol)
	if setup == nil {
		t.Fatal("expected setup candle to be established")
	}
	if setup.High != 102.0 || setup.Low != 98.0 {
		t.Errorf("unexpected setup candle bounds: high=%f, low=%f", setup.High, setup.Low)
	}

	// 4. Test Buy breakout on red setup candle
	sig = engine.CheckBreakout(symbol, 103.0, "BUY_ONLY")
	if sig == nil {
		t.Fatal("expected BUY breakout signal, got nil")
	}
	if sig.Action != "BUY" || sig.StrategyName != "LOW_VOLUME" {
		t.Errorf("unexpected signal content: %v", sig)
	}

	// 5. Check duplicate trade is prevented
	sig2 := engine.CheckBreakout(symbol, 103.0, "BUY_ONLY")
	if sig2 != nil {
		t.Errorf("expected duplicate breakout to be blocked, got %v", sig2)
	}

	// Reset strategy
	engine.Reset()
	if engine.GetSetupCandle(symbol) != nil {
		t.Error("expected setup candle to be cleared after reset")
	}
}

func TestCalculateEMA(t *testing.T) {
	logger := zap.NewNop()
	ind := NewIndicators(logger, 20, 14, 10)

	closes := []float64{10.0, 11.0, 12.0, 13.0, 14.0, 15.0, 16.0, 17.0, 18.0, 19.0, 20.0}
	emas := ind.CalculateEMA(closes, 10)

	if len(emas) != len(closes) {
		t.Fatalf("expected EMA length %d, got %d", len(closes), len(emas))
	}

	// Index 9 should match TradingView recursive EMA
	if math.Abs(emas[9]-15.239368) > 0.001 {
		t.Errorf("expected EMA at index 9 to be ~15.239, got %f", emas[9])
	}

	// 11th element (index 10) should be (20 * (2/11)) + (emas[9] * (9/11)) ~ 16.1049
	if emas[10] <= emas[9] || emas[10] >= 20.0 {
		t.Errorf("expected 11th EMA value to trend towards 20.0, got %f", emas[10])
	}
}

func TestStrategyCandleTimeframeConfiguration(t *testing.T) {
	logger := zap.NewNop()

	// 1. Low Volume Engine default timeframe should be "5m"
	lv := NewLowVolumeEngine(logger)
	if lv.CandleTimeFrame() != "5m" {
		t.Errorf("expected LowVolumeEngine default timeframe to be '5m', got '%s'", lv.CandleTimeFrame())
	}
	lv.SetCandleTimeFrame("1m")
	if lv.CandleTimeFrame() != "1m" {
		t.Errorf("expected LowVolumeEngine timeframe to update to '1m', got '%s'", lv.CandleTimeFrame())
	}

	// 2. Vande Bharat Engine default timeframe should be "1m"
	vb := NewVandeBharatEngine(logger, 1.8, 0.5, 1.0, 40.0, 2.0)
	if vb.CandleTimeFrame() != "1m" {
		t.Errorf("expected VandeBharatEngine default timeframe to be '1m', got '%s'", vb.CandleTimeFrame())
	}
	vb.SetCandleTimeFrame("5m")
	if vb.CandleTimeFrame() != "5m" {
		t.Errorf("expected VandeBharatEngine timeframe to update to '5m', got '%s'", vb.CandleTimeFrame())
	}
}
