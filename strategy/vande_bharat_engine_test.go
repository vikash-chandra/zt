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
	// Open: 102.1, Close: 102.0 (> Master High 102.0 but RED: Close < Open) -> Invalidation
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
}
