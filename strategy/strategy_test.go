package strategy

import (
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

	// 1. Send first candle
	c1 := &data.Candle{
		Token:  12345,
		Time:   time.Now(),
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
		Time:   time.Now(),
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

	// 6. Test reset
	engine.Reset()
	if engine.GetSetupCandle(symbol) != nil {
		t.Error("expected setup candle to be cleared after reset")
	}
}
