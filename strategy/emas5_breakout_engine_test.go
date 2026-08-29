package strategy

import (
	"testing"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

func TestEMAS5BreakoutEngine_BUY(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.2, 0.5, 2.0, 1, 1.0)
	symbol := "TATASTEEL"
	engine.SetPreviousDayLevels(symbol, 151.5, 145.0, 148.0)

	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, time.UTC)

	// Feed 20 baseline warm-up candles at ~148.0
	for i := 0; i < 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i) * time.Minute),
			Open:   148.0,
			High:   148.5,
			Low:    147.5,
			Close:  148.0,
			Volume: 1000,
		})
	}

	// Feed 5 Rally sequence candles below PDH (151.5):
	// Lows: 148.0 -> 148.4 -> 148.8 -> 149.2 -> 149.6
	rallyPrices := []struct {
		o, h, l, c float64
	}{
		{148.0, 148.8, 148.0, 148.5},
		{148.5, 149.2, 148.4, 149.0},
		{149.0, 149.6, 148.8, 149.4},
		{149.4, 150.0, 149.2, 149.8},
		{149.8, 150.5, 149.6, 150.2},
	}

	for i, p := range rallyPrices {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(20+i) * time.Minute),
			Open:   p.o,
			High:   p.h,
			Low:    p.l,
			Close:  p.c,
			Volume: 1000,
		})
	}

	// Master Candle: Green, touches EMA10/PDH (Low = 150.0), surges above PDH (151.5) to close at 152.0
	// Rebound from 148.0 is (152 - 148)/148 = 2.7% >= 0.5%
	// Range: (152.5 - 150.0) / 152.0 = 1.64% <= 2.0%
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(25 * time.Minute),
		Open:   150.2,
		High:   152.5,
		Low:    150.0,
		Close:  152.0,
		Volume: 2000,
	})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected Master Candle to be established for %s", symbol)
	}
	if engine.masterDirections[symbol] != "BUY" {
		t.Fatalf("Expected Master Direction to be BUY, got %s", engine.masterDirections[symbol])
	}

	// 1 Inside Candle: High = 152.2 <= 152.5, Low = 151.0 >= 149.8
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(26 * time.Minute),
		Open:   152.0,
		High:   152.2,
		Low:    151.0,
		Close:  151.8,
		Volume: 1000,
	})

	if engine.confirmationCandles[symbol] != nil {
		t.Fatalf("Confirmation candle should not be formed on inside candle")
	}

	// Confirmation Candle: Breaks Master High (152.5), closes at 153.0 (High = 153.2, Low = 152.0, Range = 0.78% <= 1.0%)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(27 * time.Minute),
		Open:   151.8,
		High:   153.2,
		Low:    152.0,
		Close:  153.0,
		Volume: 3000,
	})

	if engine.confirmationCandles[symbol] == nil {
		t.Fatalf("Expected Confirmation Candle to be formed for %s", symbol)
	}

	// Test Live Tick Breakout Trigger (LTP >= 153.2)
	sig := engine.CheckBreakout(symbol, 153.30, "")
	if sig == nil {
		t.Fatalf("Expected BUY breakout signal at LTP 153.30")
	}
	if sig.Action != "BUY" {
		t.Fatalf("Expected action BUY, got %s", sig.Action)
	}
	if engine.tradeCountsPerStock[symbol] != 1 {
		t.Fatalf("Expected stock trade count 1, got %d", engine.tradeCountsPerStock[symbol])
	}
}

func TestEMAS5BreakoutEngine_SELL(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.2, 0.5, 2.0, 1, 1.0)
	symbol := "INFY"
	engine.SetPreviousDayLevels(symbol, 1550.0, 1500.0, 1520.0)

	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, time.UTC)

	// Feed 20 baseline warm-up candles at ~1520.0
	for i := 0; i < 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i) * time.Minute),
			Open:   1520.0,
			High:   1525.0,
			Low:    1515.0,
			Close:  1520.0,
			Volume: 1000,
		})
	}

	// Feed 5 Drop sequence candles with Lower Highs:
	// Highs: 1520.0 -> 1515.0 -> 1510.0 -> 1505.0 -> 1500.0
	dropPrices := []struct {
		o, h, l, c float64
	}{
		{1520.0, 1520.0, 1510.0, 1512.0},
		{1512.0, 1515.0, 1505.0, 1508.0},
		{1508.0, 1510.0, 1500.0, 1502.0},
		{1502.0, 1505.0, 1495.0, 1498.0},
		{1498.0, 1500.0, 1490.0, 1492.0},
	}

	for i, p := range dropPrices {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(20+i) * time.Minute),
			Open:   p.o,
			High:   p.h,
			Low:    p.l,
			Close:  p.c,
			Volume: 1000,
		})
	}

	// Master Candle: RED, touches EMA20/PDL (High = 1502.0), closes below all 3 at 1485.0
	// Drop from 1520.0 is (1520 - 1485)/1520 = 2.3% >= 0.5%
	// Range: (1502 - 1480) / 1485 = 1.48% <= 2.0%
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(25 * time.Minute),
		Open:   1492.0,
		High:   1502.0,
		Low:    1480.0,
		Close:  1485.0,
		Volume: 2000,
	})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected Master Candle to be established for %s", symbol)
	}
	if engine.masterDirections[symbol] != "SELL" {
		t.Fatalf("Expected Master Direction to be SELL, got %s", engine.masterDirections[symbol])
	}

	// Confirmation Candle: Breaks Master Low (1480.0), closes at 1475.0 (High = 1482.0, Low = 1472.0, Range = 0.67% <= 1.0%)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(26 * time.Minute),
		Open:   1485.0,
		High:   1482.0,
		Low:    1472.0,
		Close:  1475.0,
		Volume: 3000,
	})

	if engine.confirmationCandles[symbol] == nil {
		t.Fatalf("Expected Confirmation Candle to be formed for %s", symbol)
	}

	// Test Live Tick Breakdown Trigger (LTP <= 1472.0)
	sig := engine.CheckBreakout(symbol, 1471.50, "")
	if sig == nil {
		t.Fatalf("Expected SELL breakout signal at LTP 1471.50")
	}
	if sig.Action != "SELL" {
		t.Fatalf("Expected action SELL, got %s", sig.Action)
	}
}

func TestEMAS5BreakoutEngine_MasterLowInvalidation(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.2, 0.5, 2.0, 1, 1.0)
	symbol := "SBIN"
	engine.SetPreviousDayLevels(symbol, 800.0, 780.0, 790.0)

	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, time.UTC)

	for i := 0; i < 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i) * time.Minute),
			Open:   790.0,
			High:   792.0,
			Low:    788.0,
			Close:  790.0,
			Volume: 1000,
		})
	}

	// 5 Rally candles
	for i := 0; i < 5; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(20+i) * time.Minute),
			Open:   790.0 + float64(i)*1.0,
			High:   792.0 + float64(i)*1.0,
			Low:    789.0 + float64(i)*1.0,
			Close:  791.0 + float64(i)*1.0,
			Volume: 1000,
		})
	}

	// Master candle: Low = 792.0, High = 804.0, Close = 802.0
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(25 * time.Minute),
		Open:   795.0,
		High:   804.0,
		Low:    792.0,
		Close:  802.0,
		Volume: 2000,
	})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Master candle should be established")
	}

	// Next candle drops below Master Low (792.0) -> Low = 790.0
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(26 * time.Minute),
		Open:   802.0,
		High:   803.0,
		Low:    790.0,
		Close:  791.0,
		Volume: 1000,
	})

	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Master candle should be invalidated after breaking Master Low")
	}
}

func TestEMAS5BreakoutEngine_MaxTradesPerStock(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.2, 0.5, 2.0, 1, 1.0)
	symbol := "RELIANCE"

	// Mock confirmation candle
	engine.confirmationCandles[symbol] = &data.Candle{High: 3000.0, Low: 2980.0}
	engine.masterDirections[symbol] = "BUY"

	// Trade 1
	sig1 := engine.CheckBreakout(symbol, 3005.0, "")
	if sig1 == nil {
		t.Fatalf("Expected trade 1 to trigger")
	}
	if engine.tradeCountsPerStock[symbol] != 1 {
		t.Fatalf("Expected trade count 1, got %d", engine.tradeCountsPerStock[symbol])
	}

	// Mock trade 2
	engine.confirmationCandles[symbol] = &data.Candle{High: 3050.0, Low: 3030.0}
	engine.masterDirections[symbol] = "BUY"
	sig2 := engine.CheckBreakout(symbol, 3055.0, "")
	if sig2 == nil {
		t.Fatalf("Expected trade 2 to trigger")
	}
	if engine.tradeCountsPerStock[symbol] != 2 {
		t.Fatalf("Expected trade count 2, got %d", engine.tradeCountsPerStock[symbol])
	}

	// Mock attempt for trade 3 (should be blocked by maxTradesPerStock=2)
	engine.confirmationCandles[symbol] = &data.Candle{High: 3100.0, Low: 3080.0}
	engine.masterDirections[symbol] = "BUY"
	sig3 := engine.CheckBreakout(symbol, 3105.0, "")
	if sig3 != nil {
		t.Fatalf("Expected trade 3 to be BLOCKED, but got signal")
	}
}

func TestEMAS5BreakoutEngine_MasterHighInvalidation(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.2, 0.5, 2.0, 1, 1.0)
	symbol := "WIPRO"

	masterCandle := data.Candle{High: 500.0, Low: 490.0, Close: 492.0, Time: time.Now().Add(-time.Minute)}
	engine.rollingCandles[symbol] = []data.Candle{masterCandle}
	engine.masterCandles[symbol] = &masterCandle
	engine.masterDirections[symbol] = "SELL"
	engine.masterCandleIndices[symbol] = 0

	// Candle rallies and breaks Master High (500.0) -> High = 502.0
	engine.ProcessCandle(symbol, data.Candle{
		Time:   time.Now(),
		Open:   493.0,
		High:   502.0,
		Low:    491.0,
		Close:  501.0,
		Volume: 1000,
	})

	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Expected SELL Master candle to be invalidated on Master High breach")
	}
}

func TestEMAS5BreakoutEngine_InsideCandleOverflow(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.2, 0.5, 2.0, 1, 1.0) // maxInsideCandles = 1
	symbol := "HDFCBANK"

	masterCandle := data.Candle{High: 1650.0, Low: 1630.0, Close: 1648.0, Time: time.Now().Add(-2 * time.Minute)}
	engine.rollingCandles[symbol] = []data.Candle{masterCandle}
	engine.masterCandles[symbol] = &masterCandle
	engine.masterDirections[symbol] = "BUY"
	engine.masterCandleIndices[symbol] = 0

	// Inside candle 1 (allowed)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   time.Now().Add(-time.Minute),
		Open:   1645.0,
		High:   1649.0,
		Low:    1635.0,
		Close:  1642.0,
		Volume: 1000,
	})
	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Master candle should survive 1st inside candle")
	}

	// Inside candle 2 (exceeds maxInsideCandles = 1 -> should invalidate)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   time.Now(),
		Open:   1642.0,
		High:   1648.0,
		Low:    1636.0,
		Close:  1640.0,
		Volume: 1000,
	})
	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Master candle should be invalidated when inside candles > 1")
	}
}
