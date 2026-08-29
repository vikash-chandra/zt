package strategy

import (
	"testing"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

func TestVandeBharatEngineRules(t *testing.T) {
	logger := zap.NewNop()
	// masterMaxPct=3.0, slMinPct=0.5, slMaxPct=1.0, masterMaxWickPct=40.0, minGapPct=2.0
	engine := NewVandeBharatEngine(logger, 3.0, 0.5, 1.0, 40.0, 2.0)
	symbol := "SBIN"

	// PDH: 100.0, PDL: 90.0
	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	baseTime := time.Date(2026, 8, 17, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1 (09:15 AM): Green Master candle (Open: 102.2 -> 2.2% gap >= 2.0%, High: 102.8, Low: 102.0, Close: 102.7 > PDH 100.0)
	// Range: 0.8, Body: 0.5, Wick: 0.3 (37.5% <= 40.0%)
	candle1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   102.2,
		High:   102.8,
		Low:    102.0,
		Close:  102.7,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	if master == nil {
		t.Fatal("expected 1st candle to be set as Master Candle with 2.2% opening gap")
	}

	// Candle 2 (09:20 AM): 2nd candle of the day (Open: 102.7, High: 102.8, Low: 102.1, Close: 102.5)
	// Range = (102.8 - 102.1)/102.5 = 0.68% -> between 0.5% and 1.0% (Valid 2nd candle SL!)
	// Inside Master range [102.0, 102.8]
	candle2 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   102.7,
		High:   102.8,
		Low:    102.1,
		Close:  102.5,
		Volume: 1200,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	second := engine.secondCandles[symbol]
	engine.mu.RUnlock()

	if second == nil {
		t.Fatal("expected 2nd candle to be locked as valid SL anchor")
	}

	// Candle 3 (09:25 AM): Confirmation Candle breaking Day High (Master High 102.8)
	// Can be ANY COLOR (e.g. Red: Open 102.85, Close 102.6, High 102.85 > 102.8)
	candle3 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(10 * time.Minute),
		Open:   102.85,
		High:   102.85,
		Low:    102.3,
		Close:  102.6,
		Volume: 1500,
	}
	engine.OnCandleClose(candle3, symbol)

	engine.mu.RLock()
	confirm := engine.confirmationCandles[symbol]
	engine.mu.RUnlock()

	if confirm == nil {
		t.Fatal("expected 3rd candle to be set as Confirmation Candle regardless of color")
	}

	// Verify SL Anchor: GetSetupCandle should return 2nd candle Low (102.1) as Low
	setup := engine.GetSetupCandle(symbol)
	if setup == nil || setup.Low != 102.1 {
		t.Fatalf("expected SL anchor low to be 2nd candle low (102.1), got: %+v", setup)
	}

	// Verify Entry Constraint: Move from PDH 100.0 with LTP 102.9 -> (102.9 - 100)/100 = 2.9% <= 3.0% (masterMaxPct)
	sigTrigger := engine.CheckBreakout(symbol, 102.9, "BUY_ONLY")
	if sigTrigger == nil || sigTrigger.Action != "BUY" {
		t.Fatalf("expected BUY trigger when move from PDH is <= masterMaxPct, got: %+v", sigTrigger)
	}
}

func TestVandeBharatEngineGapFilterRejection(t *testing.T) {
	logger := zap.NewNop()
	// minGapPct = 2.0%
	engine := NewVandeBharatEngine(logger, 3.0, 0.5, 1.0, 40.0, 2.0)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	baseTime := time.Date(2026, 8, 17, 9, 15, 0, 0, data.ISTLocation)

	// Opening gap is only 1.0% from PDH (Open 101.0 -> (101.0 - 100.0)/100.0 = 1.0% < 2.0%)
	candle1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   101.0,
		High:   102.0,
		Low:    100.5,
		Close:  101.8,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	if master != nil {
		t.Fatal("expected 1st candle with insufficient opening gap (< 2.0%) to be rejected as Master Candle")
	}
}

func TestVandeBharatEngine2ndCandleSLRangeFilter(t *testing.T) {
	logger := zap.NewNop()
	// slMinPct=0.5, slMaxPct=1.0
	engine := NewVandeBharatEngine(logger, 3.0, 0.5, 1.0, 40.0, 2.0)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)
	baseTime := time.Date(2026, 8, 17, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1: Valid Master Candle (2.2% gap from PDH)
	candle1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   102.2,
		High:   103.2,
		Low:    102.1,
		Close:  103.0,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	// Candle 2: 2nd candle with excessively large range (High 104.5, Low 102.0 -> Range 2.4% > 1.0% max SL)
	candle2 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   103.0,
		High:   104.5,
		Low:    102.0,
		Close:  103.5,
		Volume: 1200,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	if master != nil {
		t.Fatal("expected Master Candle setup to be invalidated when 2nd candle range exceeds slMaxPct (1.0%)")
	}
}

func TestVandeBharatEngineMasterLowInvalidation(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.5, 1.0, 40.0, 2.0)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	baseTime := time.Date(2026, 8, 17, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1: Master Buy Candle (Open 102.2, High 103.2, Low 102.1, Close 103.0 > PDH 100.0)
	candle1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   102.2,
		High:   103.2,
		Low:    102.1,
		Close:  103.0,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	// Candle 2: Price drops below Master Low (Low 101.5 < 102.1) -> MUST INVALIDATE MASTER SETUP!
	candle2 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   102.8,
		High:   102.8,
		Low:    101.5,
		Close:  101.6,
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

func TestVandeBharatEngineDayMoveFromPDHFilter(t *testing.T) {
	logger := zap.NewNop()
	// masterMaxPct = 1.8%
	engine := NewVandeBharatEngine(logger, 1.8, 0.5, 1.0, 40.0, 0.5)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	baseTime := time.Date(2026, 8, 17, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1: Open 100.6, High 101.5, Low 100.5, Close 101.2
	candle1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   100.6,
		High:   101.5,
		Low:    100.5,
		Close:  101.2,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	// Candle 2: 2nd Candle (Open 101.2, High 101.4, Low 100.8, Close 101.0, Range = 0.59% -> Valid SL)
	candle2 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   101.2,
		High:   101.4,
		Low:    100.8,
		Close:  101.0,
		Volume: 1200,
	}
	engine.OnCandleClose(candle2, symbol)

	// Candle 3: Confirmation Candle breaking Master High 101.5 (High 101.7)
	candle3 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(10 * time.Minute),
		Open:   101.1,
		High:   101.7,
		Low:    101.0,
		Close:  101.6,
		Volume: 1500,
	}
	engine.OnCandleClose(candle3, symbol)

	// CheckBreakout with LTP 102.5 -> Move from PDH (100.0) = (102.5 - 100.0)/100.0 = 2.5% > 1.8% (masterMaxPct) -> Should be skipped!
	sigSkipped := engine.CheckBreakout(symbol, 102.5, "BUY_ONLY")
	if sigSkipped != nil {
		t.Fatal("expected trade to be skipped when move from PDH exceeds masterMaxPct (1.8%)")
	}

	// CheckBreakout with LTP 101.75 -> Move from PDH = 1.75% <= 1.8% -> Valid!
	sigValid := engine.CheckBreakout(symbol, 101.75, "BUY_ONLY")
	if sigValid == nil || sigValid.Action != "BUY" {
		t.Fatalf("expected trade to trigger when move from PDH is <= 1.8%%, got: %+v", sigValid)
	}
}


