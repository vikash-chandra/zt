package strategy

import (
	"testing"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

func TestFakeBreakoutEngine_SELL(t *testing.T) {
	logger := zap.NewNop()
	// Gap Up 4% to 8%, max confirmation 1.0%, master max wick 40%
	engine := NewFakeBreakoutEngine(logger, 4.0, 8.0, 4.0, 8.0, 1.0, 40.0)

	symbol := "TATAMOTORS"
	pdClose := 1000.0
	engine.SetPDHPDL(symbol, 1010.0, 990.0, pdClose)

	loc := data.ISTLocation

	// 1. Master Candle (09:15 AM IST)
	// Gap Up 5.0% (Open: 1050.0). Red candle: High: 1052.0, Low: 1038.0, Close: 1040.0
	// Range = 14.0. Upper wick = 1052 - 1050 = 2.0. Lower wick = 1040 - 1038 = 2.0. Total wick % = 4 / 14 = 28.5% <= 40%
	c1Time := time.Date(2026, 8, 29, 9, 15, 0, 0, loc)
	c1 := &data.Candle{
		Time:   c1Time,
		Open:   1050.0,
		High:   1052.0,
		Low:    1038.0,
		Close:  1040.0,
		Volume: 50000,
	}
	engine.OnCandleClose(c1, symbol)

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("expected master candle to be qualified for SELL")
	}

	// 2. Confirmation Candle (09:16 AM IST)
	// Must be RED, break Master Low (1038.0), range <= 1.0% (<= 10.3)
	// Open: 1039.0, High: 1040.0, Low: 1032.0, Close: 1033.0
	// Range = 8.0 / 1033.0 = 0.77% <= 1.0%. Low 1032.0 < 1038.0
	c2Time := time.Date(2026, 8, 29, 9, 16, 0, 0, loc)
	c2 := &data.Candle{
		Time:   c2Time,
		Open:   1039.0,
		High:   1040.0,
		Low:    1032.0,
		Close:  1033.0,
		Volume: 40000,
	}
	engine.OnCandleClose(c2, symbol)

	if engine.confirmationCandles[symbol] == nil {
		t.Fatalf("expected confirmation candle to be qualified for SELL")
	}

	setup := engine.GetSetupCandle(symbol)
	if setup == nil {
		t.Fatalf("expected setup candle to be non-nil")
	}
	if setup.High != 1040.0 || setup.Low != 1032.0 {
		t.Errorf("expected setup bounds High 1040.0, Low 1032.0, got High %f, Low %f", setup.High, setup.Low)
	}

	// 3. Trade Entry from 3rd candle onward
	// Live tick at 1035.0 (above confirmation low 1032.0) -> No trigger
	sig1 := engine.CheckBreakout(symbol, 1035.0, "SELL")
	if sig1 != nil {
		t.Errorf("expected no signal at 1035.0, got %v", sig1)
	}

	// Live tick breaks confirmation low at 1031.50 -> SELL Trigger!
	sig2 := engine.CheckBreakout(symbol, 1031.50, "SELL")
	if sig2 == nil {
		t.Fatalf("expected SELL trigger at 1031.50")
	}
	if sig2.Action != "SELL" {
		t.Errorf("expected action SELL, got %s", sig2.Action)
	}
	if sig2.StrategyName != "FAKE_BREAKOUT" {
		t.Errorf("expected strategy FAKE_BREAKOUT, got %s", sig2.StrategyName)
	}

	// Re-trigger should be blocked
	sig3 := engine.CheckBreakout(symbol, 1030.0, "SELL")
	if sig3 != nil {
		t.Errorf("expected no duplicate trigger, got %v", sig3)
	}
}

func TestFakeBreakoutEngine_BUY(t *testing.T) {
	logger := zap.NewNop()
	engine := NewFakeBreakoutEngine(logger, 4.0, 8.0, 4.0, 8.0, 1.0, 40.0)

	symbol := "INFY"
	pdClose := 2000.0
	engine.SetPDHPDL(symbol, 2020.0, 1980.0, pdClose)

	loc := data.ISTLocation

	// 1. Master Candle (09:15 AM IST)
	// Gap Down 5.0% (Open: 1900.0). Green candle: High: 1920.0, Low: 1898.0, Close: 1918.0
	// Range = 22.0. Upper wick = 1920 - 1918 = 2.0. Lower wick = 1900 - 1898 = 2.0. Total wick % = 4 / 22 = 18.1% <= 40%
	c1Time := time.Date(2026, 8, 29, 9, 15, 0, 0, loc)
	c1 := &data.Candle{
		Time:   c1Time,
		Open:   1900.0,
		High:   1920.0,
		Low:    1898.0,
		Close:  1918.0,
		Volume: 60000,
	}
	engine.OnCandleClose(c1, symbol)

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("expected master candle to be qualified for BUY")
	}

	// 2. Confirmation Candle (09:16 AM IST)
	// Must be GREEN, break Master High (1920.0), range <= 1.0% (<= 19.2)
	// Open: 1919.0, High: 1928.0, Low: 1917.0, Close: 1926.0
	// Range = 11.0 / 1926.0 = 0.57% <= 1.0%. High 1928.0 > 1920.0
	c2Time := time.Date(2026, 8, 29, 9, 16, 0, 0, loc)
	c2 := &data.Candle{
		Time:   c2Time,
		Open:   1919.0,
		High:   1928.0,
		Low:    1917.0,
		Close:  1926.0,
		Volume: 45000,
	}
	engine.OnCandleClose(c2, symbol)

	if engine.confirmationCandles[symbol] == nil {
		t.Fatalf("expected confirmation candle to be qualified for BUY")
	}

	// 3. Trade Entry from 3rd candle onward
	// Live tick breaks confirmation high at 1928.50 -> BUY Trigger!
	sig := engine.CheckBreakout(symbol, 1928.50, "BUY")
	if sig == nil {
		t.Fatalf("expected BUY trigger at 1928.50")
	}
	if sig.Action != "BUY" {
		t.Errorf("expected action BUY, got %s", sig.Action)
	}
}

func TestFakeBreakoutEngine_InvalidationRules(t *testing.T) {
	logger := zap.NewNop()
	loc := data.ISTLocation

	// Case 1: Gap too small (< 4%)
	{
		engine := NewFakeBreakoutEngine(logger, 4.0, 8.0, 4.0, 8.0, 1.0, 40.0)
		engine.SetPDHPDL("SBIN", 505.0, 495.0, 500.0)
		c1 := &data.Candle{
			Time:  time.Date(2026, 8, 29, 9, 15, 0, 0, loc),
			Open:  510.0, // only 2% gap up
			High:  512.0,
			Low:   506.0,
			Close: 507.0,
		}
		engine.OnCandleClose(c1, "SBIN")
		if engine.masterCandles["SBIN"] != nil {
			t.Errorf("expected master candle to be nil due to low gap")
		}
	}

	// Case 2: 1st candle color mismatch (Green on Gap Up)
	{
		engine := NewFakeBreakoutEngine(logger, 4.0, 8.0, 4.0, 8.0, 1.0, 40.0)
		engine.SetPDHPDL("RELIANCE", 2500.0, 2400.0, 2400.0)
		c1 := &data.Candle{
			Time:  time.Date(2026, 8, 29, 9, 15, 0, 0, loc),
			Open:  2520.0, // 5% gap up
			High:  2550.0,
			Low:   2515.0,
			Close: 2540.0, // Green close > open
		}
		engine.OnCandleClose(c1, "RELIANCE")
		if engine.masterCandles["RELIANCE"] != nil {
			t.Errorf("expected master candle to be nil due to green candle on gap up")
		}
	}

	// Case 3: 2nd candle range too large (> 1.0%)
	{
		engine := NewFakeBreakoutEngine(logger, 4.0, 8.0, 4.0, 8.0, 1.0, 40.0)
		engine.SetPDHPDL("HDFCBANK", 1500.0, 1400.0, 1400.0)
		c1 := &data.Candle{
			Time:  time.Date(2026, 8, 29, 9, 15, 0, 0, loc),
			Open:  1480.0, // 5.7% gap up
			High:  1485.0,
			Low:   1460.0,
			Close: 1465.0, // Red
		}
		engine.OnCandleClose(c1, "HDFCBANK")

		c2 := &data.Candle{
			Time:  time.Date(2026, 8, 29, 9, 16, 0, 0, loc),
			Open:  1465.0,
			High:  1470.0,
			Low:   1440.0, // Breaks master low
			Close: 1445.0, // Range = 30 / 1445 = 2.07% > 1.0%
		}
		engine.OnCandleClose(c2, "HDFCBANK")
		if engine.confirmationCandles["HDFCBANK"] != nil {
			t.Errorf("expected confirmation candle to be nil due to large range")
		}
	}
}
