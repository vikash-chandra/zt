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

	// 3. SELL: Third Candle must be RED (Close < Open) and Close < Confirmation Low (88.0)
	// Open: 87.8, Close: 87.5, High: 87.8, Low: 87.4 (Range: 0.4 <= 1.0% of Close)
	candle3 := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   87.8,
		High:   87.8,
		Low:    87.4,
		Close:  87.5,
		Volume: 1000,
	}

	engine.OnCandleClose(candle3, symbol)

	engine.mu.RLock()
	third := engine.thirdCandles[symbol]
	engine.mu.RUnlock()

	if third == nil {
		t.Fatal("expected RED Third Candle to be set")
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

	// 3. Third Candle (Green, Close > Confirmation High 102.9)
	candle3 := &data.Candle{
		Token:  123,
		Time:   time.Now(),
		Open:   103.0,
		High:   103.8,
		Low:    102.9,
		Close:  103.7,
		Volume: 1000,
	}
	engine.OnCandleClose(candle3, symbol)

	// Verify setup candle anchor is the third candle
	setup := engine.GetSetupCandle(symbol)
	if setup == nil || setup.High != 103.8 || setup.Low != 102.9 {
		t.Fatalf("expected setup candle to be third candle, got: %+v", setup)
	}

	// Test CheckBreakout
	// Price below third candle high -> no trigger
	sigNoTrigger := engine.CheckBreakout(symbol, 103.5, "BUY_ONLY")
	if sigNoTrigger != nil {
		t.Fatal("expected no trigger since price is below third candle high")
	}

	// Price breaks third candle high -> BUY trigger
	sigTrigger := engine.CheckBreakout(symbol, 103.9, "BUY_ONLY")
	if sigTrigger == nil || sigTrigger.Action != "BUY" {
		t.Fatalf("expected BUY trigger, got: %+v", sigTrigger)
	}
}
