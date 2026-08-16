package strategy

import (
	"testing"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

func TestVandeBharatEngineRules(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.5, 1.0, 40.0, 3.0)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	baseTime := time.Date(2026, 8, 17, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1 (09:15 AM): Green Master candle (Open: 99.5, High: 101.2, Low: 99.5, Close: 101.0)
	candle1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   99.5,
		High:   101.2,
		Low:    99.5,
		Close:  101.0,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	if master == nil {
		t.Fatal("expected 1st candle to be set as Master Candle")
	}

	// Candle 2 (09:20 AM): 2nd candle of the day (Open: 101.0, High: 101.3, Low: 100.2, Close: 101.2)
	candle2 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   101.0,
		High:   101.3,
		Low:    100.2,
		Close:  101.2,
		Volume: 1200,
	}
	engine.OnCandleClose(candle2, symbol)

	// Candle 3 (09:25 AM): Confirmation Candle breaking Master High 101.2 (Close 101.9 > 101.2, Green)
	candle3 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(10 * time.Minute),
		Open:   101.3,
		High:   102.0,
		Low:    101.3,
		Close:  101.9,
		Volume: 1500,
	}
	engine.OnCandleClose(candle3, symbol)

	engine.mu.RLock()
	confirm := engine.confirmationCandles[symbol]
	engine.mu.RUnlock()

	if confirm == nil {
		t.Fatal("expected 3rd candle to be set as Confirmation Candle")
	}

	// Verify Rule 5 SL Anchor: GetSetupCandle should return 2nd candle Low (100.2) as Low
	setup := engine.GetSetupCandle(symbol)
	if setup == nil || setup.Low != 100.2 {
		t.Fatalf("expected SL anchor low to be 2nd candle low (100.2), got: %+v", setup)
	}

	// Verify Rule 2: Stock day % change < 3.0%
	sigTrigger := engine.CheckBreakout(symbol, 102.2, "BUY_ONLY")
	if sigTrigger == nil || sigTrigger.Action != "BUY" {
		t.Fatalf("expected BUY trigger when day change is < 3.0%%, got: %+v", sigTrigger)
	}
}

func TestVandeBharatEngineMasterLowInvalidation(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.5, 1.0, 40.0, 3.0)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	baseTime := time.Date(2026, 8, 17, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1: Master Buy Candle (High 101.2, Low 99.5, Close 101.0 > PDH 100.0)
	candle1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   99.5,
		High:   101.2,
		Low:    99.5,
		Close:  101.0,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	// Candle 2: Price drops below Master Low (Close 99.0 < 99.5) -> MUST INVALIDATE MASTER SETUP!
	candle2 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   100.5,
		High:   100.5,
		Low:    99.0,
		Close:  99.0,
		Volume: 1200,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	if master != nil {
		t.Fatal("expected Master Candle setup to be invalidated when Master Low is broken")
	}
}

func TestVandeBharatEngineMax40WickRule(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.5, 1.0, 40.0, 3.0)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	baseTime := time.Date(2026, 8, 17, 9, 15, 0, 0, data.ISTLocation)

	// 1st Candle with > 40% wick
	candle1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   100.1,
		High:   103.0,
		Low:    99.0,
		Close:  100.2,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	if master != nil {
		t.Fatal("expected candle with > 40% wick to be rejected as Master Candle")
	}
}

func TestVandeBharatEngineDayPctChangeFilter(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.5, 1.0, 40.0, 3.0)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	baseTime := time.Date(2026, 8, 17, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1: Open 100.0, Close 101.0
	candle1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   100.0,
		High:   101.2,
		Low:    99.8,
		Close:  101.0,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	// Candle 2: Open 101.0, Close 101.8 (Confirmation breaking Master High 101.2, range 0.8%)
	candle2 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   101.1,
		High:   101.9,
		Low:    101.1,
		Close:  101.8,
		Volume: 1200,
	}
	engine.OnCandleClose(candle2, symbol)

	// CheckBreakout with LTP 103.5 -> Change = |103.5 - 100.0|/100.0 = 3.5% >= 3.0% -> Should be skipped!
	sigSkipped := engine.CheckBreakout(symbol, 103.5, "BUY_ONLY")
	if sigSkipped != nil {
		t.Fatal("expected trade to be skipped when day % change >= 3.0%")
	}
}
