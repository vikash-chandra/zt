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

func TestVandeBharatIntermediateCandleConsolidationAndBreakout(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.5, 1.0, 40.0, 3.0)
	symbol := "TCS"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)
	baseTime := time.Date(2026, 8, 17, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1 (09:15 AM): Master Buy Candle (High 102.0, Low 99.5, Close 101.0 > PDH 100.0)
	candle1 := &data.Candle{
		Token: 123, Time: baseTime, Open: 99.8, High: 102.0, Low: 99.5, Close: 101.0, Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	// Candle 2 (09:20 AM): Inside Candle (High 101.5 <= 102.0, Low 100.2 >= 99.5) -> Valid inside consolidation
	candle2 := &data.Candle{
		Token: 123, Time: baseTime.Add(5 * time.Minute), Open: 101.0, High: 101.5, Low: 100.2, Close: 100.8, Volume: 800,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	confirm := engine.confirmationCandles[symbol]
	engine.mu.RUnlock()

	if master == nil {
		t.Fatal("expected Master Candle to remain valid after inside consolidation candle")
	}
	if confirm != nil {
		t.Fatal("expected Confirmation Candle to NOT be set while inside Master range")
	}

	// Candle 3 (09:25 AM): Inside Candle (High 101.8, Low 100.5, Close 101.2) -> Still inside
	candle3 := &data.Candle{
		Token: 123, Time: baseTime.Add(10 * time.Minute), Open: 100.8, High: 101.8, Low: 100.5, Close: 101.2, Volume: 900,
	}
	engine.OnCandleClose(candle3, symbol)

	// Candle 4 (09:30 AM): Valid Confirmation Candle (Closes 102.6 > Master High 102.0, Green, Range (102.8-102.0)/102.6 = 0.78%)
	candle4 := &data.Candle{
		Token: 123, Time: baseTime.Add(15 * time.Minute), Open: 102.0, High: 102.8, Low: 102.0, Close: 102.6, Volume: 1500,
	}
	engine.OnCandleClose(candle4, symbol)

	engine.mu.RLock()
	confirm = engine.confirmationCandles[symbol]
	engine.mu.RUnlock()

	if confirm == nil {
		t.Fatal("expected 4th candle to be set as Confirmation Candle")
	}
}

func TestVandeBharatPrematureInvalidBreakoutInvalidatesMaster(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.5, 1.0, 40.0, 3.0)
	symbol := "INFY"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)
	baseTime := time.Date(2026, 8, 17, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1 (09:15 AM): Master Buy Candle (High 102.0, Low 99.5, Close 101.0 > PDH 100.0)
	candle1 := &data.Candle{
		Token: 123, Time: baseTime, Open: 99.8, High: 102.0, Low: 99.5, Close: 101.0, Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	// Candle 2 (09:20 AM): Breaks Master High (High 102.5 > 102.0) but closes RED (Open 102.4, Close 101.8) -> Fails Confirmation -> MUST INVALIDATE MASTER!
	candle2 := &data.Candle{
		Token: 123, Time: baseTime.Add(5 * time.Minute), Open: 102.4, High: 102.5, Low: 101.7, Close: 101.8, Volume: 1200,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	if master != nil {
		t.Fatal("expected Master Candle to be invalidated when a candle breaches Master High but fails confirmation criteria")
	}
}

func TestVandeBharatSellPrematureInvalidBreakdownInvalidatesMaster(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.5, 1.0, 40.0, 3.0)
	symbol := "GVT&D"

	engine.SetPreviousDayHighLow(symbol, 4200.0, 4112.70)
	baseTime := time.Date(2026, 8, 25, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1 (09:15 AM): Master Sell Candle (Open: 4160, High: 4177, Low: 4095.6, Close: 4104.6 < PDL 4112.7)
	candle1 := &data.Candle{
		Token: 4296449, Time: baseTime, Open: 4160.0, High: 4177.0, Low: 4095.6, Close: 4104.6, Volume: 36000,
	}
	engine.OnCandleClose(candle1, symbol)

	// Candle 2 (09:20 AM): Breaks Master Low (Low 4085 < 4095.6) but is GREEN (Open 4085, Close 4090 > 4085) -> Fails Confirmation -> MUST INVALIDATE MASTER!
	candle2 := &data.Candle{
		Token: 4296449, Time: baseTime.Add(5 * time.Minute), Open: 4085.0, High: 4092.0, Low: 4085.0, Close: 4090.0, Volume: 27000,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	if master != nil {
		t.Fatal("expected Master Candle to be invalidated when a candle breaches Master Low but fails SELL confirmation criteria")
	}
}


