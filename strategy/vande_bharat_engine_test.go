package strategy

import (
	"testing"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

func TestVandeBharatEngineBuyColorConstraints(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 1.0)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	// 1. BUY: Master Candle must be GREEN (Close > Open)
	// Open: 99, Close: 101 (> 100), High: 102, Low: 99 -> Green Master (Valid)
	candle1 := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   99.0,
		High:   102.0,
		Low:    99.0,
		Close:  101.0,
		Volume: 1000,
	}

	engine.OnCandleClose(candle1, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	if master == nil {
		t.Fatal("expected GREEN Master Candle to be set")
	}

	// Reset for invalid color test
	engine.Reset()
	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	// Open: 102, Close: 101 (> 100), High: 103, Low: 99 -> Red Master (Invalid for Buy setup)
	candle1Red := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   102.0,
		High:   103.0,
		Low:    99.0,
		Close:  101.0,
		Volume: 1000,
	}

	engine.OnCandleClose(candle1Red, symbol)

	engine.mu.RLock()
	masterInvalid := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	if masterInvalid != nil {
		t.Fatal("expected RED Master Candle to be ignored in BUY setup")
	}
}

func TestVandeBharatEngineConfirmationColorConstraints(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 1.0)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	// 1. Establish valid Green Master
	candle1 := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   99.0,
		High:   102.0,
		Low:    99.0,
		Close:  101.0,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	// 2. Buy Confirmation Candle must be GREEN (Close > Open)
	// Open: 102.2, Close: 102.1 (> Master High 102.0 but RED: Close < Open) -> Invalidation
	candle2Red := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   102.2,
		High:   102.3,
		Low:    102.0,
		Close:  102.1,
		Volume: 1200,
	}

	engine.OnCandleClose(candle2Red, symbol)

	engine.mu.RLock()
	confirm := engine.confirmationCandles[symbol]
	masterCleared := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	if confirm != nil {
		t.Fatal("expected RED Confirmation Candle to be ignored in BUY setup")
	}
	if masterCleared != nil {
		t.Fatal("expected Master Candle to be cleared on failed confirmation candle color check")
	}
}

func TestVandeBharatEngineSellColorConstraints(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 1.0)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	// 1. SELL: Master Candle must be RED (Close < Open) and Close < PDL
	// Open: 91, Close: 89 (< 90), High: 91.0, Low: 89.0 (Range: 2.0 <= 3.0% of Close)
	candle1 := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   91.0,
		High:   91.0,
		Low:    89.0,
		Close:  89.0,
		Volume: 1000,
	}

	engine.OnCandleClose(candle1, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	if master == nil {
		t.Fatal("expected RED Master Candle to be set")
	}

	// 2. SELL: Confirmation Candle must be RED (Close < Open)
	// Open: 88.5, Close: 88.0 (< Master Low 89.0), High: 88.5, Low: 88.0 (Range: 0.5 <= 1.0% of Close)
	candle2 := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   88.5,
		High:   88.5,
		Low:    88.0,
		Close:  88.0,
		Volume: 1000,
	}

	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	confirm := engine.confirmationCandles[symbol]
	engine.mu.RUnlock()

	if confirm == nil {
		t.Fatal("expected RED Confirmation Candle to be set")
	}

	// 3. Trigger window close test:
	// A new candle close (representing the 3rd candle closing without trigger)
	// should reset Master and Confirmation candles to nil.
	candle3 := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   96.0,
		High:   96.0,
		Low:    95.0,
		Close:  95.0,
		Volume: 1000,
	}

	engine.OnCandleClose(candle3, symbol)

	engine.mu.RLock()
	masterCleared := engine.masterCandles[symbol]
	confirmCleared := engine.confirmationCandles[symbol]
	engine.mu.RUnlock()

	if masterCleared != nil || confirmCleared != nil {
		t.Fatal("expected setup to be reset on 3rd candle close if no breakout triggered")
	}
}

func TestVandeBharatEngineBuySetupCompleteAndTrigger(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 1.0)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	// 1. Master Candle (Green, Close > PDH 100.0)
	candle1 := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   99.0,
		High:   102.0,
		Low:    99.0,
		Close:  101.0,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	// 2. Confirmation Candle (Green, Close > Master High 102.0)
	candle2 := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   102.1,
		High:   102.9,
		Low:    102.0,
		Close:  102.8,
		Volume: 1000,
	}
	engine.OnCandleClose(candle2, symbol)

	// Verify setup candle anchor is the Confirmation Candle
	setup := engine.GetSetupCandle(symbol)
	if setup == nil || setup.High != 102.9 || setup.Low != 102.0 {
		t.Fatalf("expected setup candle to be confirmation candle, got: %+v", setup)
	}

	// Test CheckBreakout
	// Price below confirmation candle high -> no trigger
	sigNoTrigger := engine.CheckBreakout(symbol, 102.5, "BUY_ONLY")
	if sigNoTrigger != nil {
		t.Fatal("expected no trigger since price is below confirmation candle high")
	}

	// Price breaks confirmation candle high -> BUY trigger
	sigTrigger := engine.CheckBreakout(symbol, 103.0, "BUY_ONLY")
	if sigTrigger == nil || sigTrigger.Action != "BUY" {
		t.Fatalf("expected BUY trigger, got: %+v", sigTrigger)
	}
}

func TestVandeBharatEngineConfirmationPromotion(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 1.0)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 1000.0, 900.0)

	// 1. 09:20 Candle 1: Master (Green, Close > PDH 1000)
	candle1 := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   999.0,
		High:   1005.0,
		Low:    999.0,
		Close:  1005.0,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	engine.mu.RLock()
	master1 := engine.masterCandles[symbol]
	engine.mu.RUnlock()
	if master1 == nil || master1.Close != 1005.0 {
		t.Fatal("expected candle 1 to be Master Candle")
	}

	// 2. 09:25 Candle 2: Confirmation candidate.
	// Open: 1001, Close: 1004.
	// This fails confirmation because Close (1004) <= Master High (1005).
	// However, it is GREEN and Close (1004) > PDH (1000). Range: 3.0 (0.3% of Close) <= 3.0%.
	// It should be promoted to the new Master Candle.
	candle2 := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   1001.0,
		High:   1004.0,
		Low:    1001.0,
		Close:  1004.0,
		Volume: 1000,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	master2 := engine.masterCandles[symbol]
	confirm2 := engine.confirmationCandles[symbol]
	engine.mu.RUnlock()

	if master2 == nil || master2.Close != 1004.0 {
		t.Fatal("expected candle 2 to be promoted to new Master Candle")
	}
	if confirm2 != nil {
		t.Fatal("expected confirmation candle to be nil after failed confirmation/promotion")
	}

	// 3. 09:30 Candle 3: Confirmation candle for the new Master Candle.
	// Open: 1003, Close: 1006.
	// This confirms the promoted Master (Close 1006 > Master High 1004, Green, range <= 1.0%).
	candle3 := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   1003.0,
		High:   1006.0,
		Low:    1003.0,
		Close:  1006.0,
		Volume: 1000,
	}
	engine.OnCandleClose(candle3, symbol)

	engine.mu.RLock()
	master3 := engine.masterCandles[symbol]
	confirm3 := engine.confirmationCandles[symbol]
	engine.mu.RUnlock()

	if master3 == nil || master3.Close != 1004.0 {
		t.Fatal("expected master candle to remain candle 2")
	}
	if confirm3 == nil || confirm3.Close != 1006.0 {
		t.Fatal("expected candle 3 to establish confirmation candle")
	}

	// 4. Test CheckBreakout triggers on breaking confirmation high (1006.0)
	sigTrigger := engine.CheckBreakout(symbol, 1007.0, "BUY_ONLY")
	if sigTrigger == nil || sigTrigger.Action != "BUY" {
		t.Fatalf("expected BUY breakout trigger above 1006.0, got: %+v", sigTrigger)
	}
}
