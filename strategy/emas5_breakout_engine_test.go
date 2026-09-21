package strategy

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

func TestEMAS5BreakoutEngine_BUY(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0)
	symbol := "TATASTEEL"
	engine.SetPreviousDayLevels(symbol, 151.5, 145.0, 148.0)

	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, time.UTC)

	// Feed Day Peak candle at start (High = 154.0)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime,
		Open:   152.0,
		High:   154.0, // Day Peak High
		Low:    150.0,
		Close:  151.0,
		Volume: 1000,
	})

	// Feed 20 baseline candles descending to trough ~147.5 (Bottom of 'U')
	for i := 1; i <= 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i) * time.Minute),
			Open:   148.0,
			High:   148.5,
			Low:    147.5,
			Close:  148.0,
			Volume: 1000,
		})
	}

	// Feed 4 Rally sequence candles curving upward:
	// Lows: 148.0 -> 148.4 -> 148.8 -> 149.2
	rallyPrices := []struct {
		o, h, l, c float64
	}{
		{148.0, 148.8, 148.0, 148.5},
		{148.5, 149.2, 148.4, 149.0},
		{149.0, 149.6, 148.8, 149.4},
		{149.4, 150.0, 149.2, 149.8},
	}

	for i, p := range rallyPrices {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(21+i) * time.Minute),
			Open:   p.o,
			High:   p.h,
			Low:    p.l,
			Close:  p.c,
			Volume: 1000,
		})
	}

	// Master Candle: Green, touches EMA10/PDH (Low = 150.0), surges above PDH (151.5) to close at 152.0
	// Rebound from 147.5 is (152 - 147.5)/147.5 = 3.05% >= 0.5%
	// Range: (152.5 - 150.0) / 152.0 = 1.64% <= 2.0%
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(25 * time.Minute),
		Open:   149.2,
		High:   151.8,
		Low:    149.0, // Touches EMA10 (~149.2)
		Close:  151.8,
		Volume: 2000,
	})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected Master Candle to be established for %s", symbol)
	}
	if engine.masterDirections[symbol] != "BUY" {
		t.Fatalf("Expected Master Direction to be BUY, got %s", engine.masterDirections[symbol])
	}

	// 1 Inside Candle: High = 151.5 <= 151.8, Low = 149.5 >= 149.0
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(26 * time.Minute),
		Open:   151.0,
		High:   151.5,
		Low:    149.5,
		Close:  151.0,
		Volume: 1000,
	})

	if engine.confirmationCandles[symbol] != nil {
		t.Fatalf("Confirmation candle should not be formed on inside candle")
	}

	// Confirmation Candle: Breaks Master High (151.8), closes at 152.2 (High = 152.5, Low = 151.5, Range = 0.65% <= 1.0%)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(27 * time.Minute),
		Open:   151.0,
		High:   152.5,
		Low:    151.5,
		Close:  152.2,
		Volume: 3000,
	})

	if engine.confirmationCandles[symbol] == nil {
		t.Fatalf("Expected Confirmation Candle to be formed for %s", symbol)
	}

	// Test Live Tick Breakout Trigger (LTP >= 152.5)
	sig := engine.CheckBreakout(symbol, 152.60, "")
	if sig == nil {
		t.Fatalf("Expected BUY breakout signal at LTP 152.60")
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
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0)
	symbol := "INFY"
	engine.SetPreviousDayLevels(symbol, 1550.0, 1516.0, 1530.0)

	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, time.UTC)

	// Feed Day Trough candle at start (Low = 1460.0)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime,
		Open:   1480.0,
		High:   1490.0,
		Low:    1460.0, // Day Trough Low
		Close:  1485.0,
		Volume: 1000,
	})

	// Feed 20 baseline candles ascending to Peak at 1525.0 (Top of Inverted 'U')
	for i := 1; i <= 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i) * time.Minute),
			Open:   1520.0,
			High:   1525.0,
			Low:    1515.0,
			Close:  1520.0,
			Volume: 1000,
		})
	}

	// Master Candle: RED, touches EMA10/20 & PDL (1520.0) (High = 1522.0), drops and closes below all 3 at 1512.0 (< PDL 1520.0)
	// Drop from 1525.0 is (1525 - 1512)/1525 = 0.85% >= 0.5%
	// Range: (1522 - 1510) / 1512 = 0.79% <= 2.0%
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(21 * time.Minute),
		Open:   1520.0,
		High:   1522.0,
		Low:    1510.0,
		Close:  1512.0,
		Volume: 2000,
	})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected Master Candle to be established for %s", symbol)
	}
	if engine.masterDirections[symbol] != "SELL" {
		t.Fatalf("Expected Master Direction to be SELL, got %s", engine.masterDirections[symbol])
	}

	// 1 Inside Candle: High = 1515.0 <= 1522.0, Low = 1511.0 >= 1510.0
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(22 * time.Minute),
		Open:   1512.0,
		High:   1515.0,
		Low:    1511.0,
		Close:  1513.0,
		Volume: 1000,
	})

	if engine.confirmationCandles[symbol] != nil {
		t.Fatalf("Confirmation candle should not be formed on inside candle")
	}

	// Confirmation Candle: Breaks Master Low (1510.0), closes RED at 1502.0 (High = 1511.0, Low = 1500.0, Range = 0.73% <= 1.0%)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(23 * time.Minute),
		Open:   1511.0,
		High:   1511.0,
		Low:    1500.0,
		Close:  1502.0,
		Volume: 3000,
	})

	if engine.confirmationCandles[symbol] == nil {
		t.Fatalf("Expected Confirmation Candle to be formed for %s", symbol)
	}

	// Test Live Tick Breakdown Trigger (LTP <= 1500.0)
	sig := engine.CheckBreakout(symbol, 1499.50, "")
	if sig == nil {
		t.Fatalf("Expected SELL breakout signal at LTP 1499.50")
	}
	if sig.Action != "SELL" {
		t.Fatalf("Expected action SELL, got %s", sig.Action)
	}
}

func TestEMAS5BreakoutEngine_MasterLowInvalidation(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0)
	symbol := "SBIN"
	engine.SetPreviousDayLevels(symbol, 800.0, 780.0, 790.0)

	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, time.UTC)

	// Feed Day Peak candle
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime,
		Open:   810.0,
		High:   815.0, // Day Peak High
		Low:    805.0,
		Close:  808.0,
		Volume: 1000,
	})

	for i := 1; i <= 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i) * time.Minute),
			Open:   790.0,
			High:   792.0,
			Low:    788.0,
			Close:  790.0,
			Volume: 1000,
		})
	}

	// 4 Rally candles
	for i := 0; i < 4; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(21+i) * time.Minute),
			Open:   790.0 + float64(i)*1.0,
			High:   792.0 + float64(i)*1.0,
			Low:    789.0 + float64(i)*1.0,
			Close:  791.0 + float64(i)*1.0,
			Volume: 1000,
		})
	}

	// Master candle: Low = 794.0, High = 804.0, Close = 802.0 (Wick % = 30% <= 40%)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(25 * time.Minute),
		Open:   795.0,
		High:   804.0,
		Low:    794.0,
		Close:  802.0,
		Volume: 2000,
	})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Master candle should be established")
	}

	// Next candle drops below Master Low (794.0) -> Low = 790.0
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
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0)
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
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0)
	symbol := "WIPRO"

	baseTime := time.Date(2026, 8, 29, 9, 30, 0, 0, data.ISTLocation)
	masterCandle := data.Candle{High: 500.0, Low: 490.0, Close: 492.0, Time: baseTime}
	engine.rollingCandles[symbol] = []data.Candle{masterCandle}
	engine.masterCandles[symbol] = &masterCandle
	engine.masterDirections[symbol] = "SELL"
	engine.masterCandleIndices[symbol] = 0

	// Candle rallies and breaks Master High (500.0) -> High = 502.0
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(time.Minute),
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
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0) // maxInsideCandles = 1
	symbol := "HDFCBANK"

	baseTime := time.Date(2026, 8, 29, 9, 30, 0, 0, data.ISTLocation)
	masterCandle := data.Candle{High: 1650.0, Low: 1630.0, Close: 1648.0, Time: baseTime}
	engine.rollingCandles[symbol] = []data.Candle{masterCandle}
	engine.masterCandles[symbol] = &masterCandle
	engine.masterDirections[symbol] = "BUY"
	engine.masterCandleIndices[symbol] = 0

	// Inside candle 1 (allowed)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(time.Minute),
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
		Time:   baseTime.Add(2 * time.Minute),
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

func TestEMAS5BreakoutEngine_ConfirmationRangeOverflow(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0) // confirmMaxPct = 1.0%
	symbol := "AXISBANK"

	baseTime := time.Date(2026, 8, 29, 9, 30, 0, 0, data.ISTLocation)
	masterCandle := data.Candle{High: 1200.0, Low: 1190.0, Close: 1198.0, Time: baseTime}
	engine.rollingCandles[symbol] = []data.Candle{masterCandle}
	engine.masterCandles[symbol] = &masterCandle
	engine.masterDirections[symbol] = "BUY"
	engine.masterCandleIndices[symbol] = 0

	// Confirmation candle breaks Master High (1200.0) -> High = 1220.0, Low = 1195.0, Close = 1215.0
	// Range: (1220 - 1195) / 1215 = 2.05% > 1.0% max -> should invalidate setup!
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(time.Minute),
		Open:   1198.0,
		High:   1220.0,
		Low:    1195.0,
		Close:  1215.0,
		Volume: 2000,
	})

	if engine.masterCandles[symbol] != nil || engine.confirmationCandles[symbol] != nil {
		t.Fatalf("Confirmation candle with range > 1.0%% should invalidate the setup")
	}
}

func TestEMAS5BreakoutEngine_MasterRangeOverflow(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0) // masterMaxPct = 2.0%
	symbol := "ICICIBANK"
	engine.SetPreviousDayLevels(symbol, 1100.0, 1050.0, 1080.0)

	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, time.UTC)

	// Feed 20 baseline + 5 rally candles
	for i := 0; i < 25; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i) * time.Minute),
			Open:   1080.0 + float64(i)*0.5,
			High:   1082.0 + float64(i)*0.5,
			Low:    1079.0 + float64(i)*0.5,
			Close:  1081.0 + float64(i)*0.5,
			Volume: 1000,
		})
	}

	// Candidate Master: Green, touches EMA10/20, but range is 2.8% > 2.0%
	// High = 1130.0, Low = 1100.0, Close = 1125.0 -> Range = (1130-1100)/1125 = 2.66% > 2.0%
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(25 * time.Minute),
		Open:   1101.0,
		High:   1130.0,
		Low:    1100.0,
		Close:  1125.0,
		Volume: 2000,
	})

	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Candidate Master candle with range > 2.0%% should be rejected")
	}
}

func TestEMAS5BreakoutEngine_InsufficientRebound(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 1.5, 2.0, 1, 1.0) // minReboundPct = 1.5%
	symbol := "KOTAKBANK"
	engine.SetPreviousDayLevels(symbol, 1800.0, 1750.0, 1780.0)

	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, time.UTC)

	// 25 baseline candles around 1780.0
	for i := 0; i < 25; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i) * time.Minute),
			Open:   1780.0,
			High:   1782.0,
			Low:    1778.0,
			Close:  1780.0,
			Volume: 1000,
		})
	}

	// Candidate Master: Closes at 1785.0 (Rebound from 1778.0 is only (1785-1778)/1778 = 0.39% < 1.5% min)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(25 * time.Minute),
		Open:   1779.0,
		High:   1786.0,
		Low:    1778.0,
		Close:  1785.0,
		Volume: 2000,
	})

	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Candidate Master candle with insufficient curve rebound should be rejected")
	}
}

func TestEMAS5BreakoutEngine_GetSetupCandleRetention(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0)
	symbol := "LT"

	// Mock confirmation candle
	engine.confirmationCandles[symbol] = &data.Candle{High: 3500.0, Low: 3480.0, Volume: 5000}
	engine.lastSetupCandles[symbol] = &SetupCandle{
		Candle: data.Candle{High: 3500.0, Low: 3480.0, Volume: 5000},
		High:   3500.0,
		Low:    3480.0,
		Volume: 5000,
	}
	engine.masterDirections[symbol] = "BUY"

	// Trigger trade
	sig := engine.CheckBreakout(symbol, 3505.0, "")
	if sig == nil {
		t.Fatalf("Expected trade signal")
	}

	// Verify GetSetupCandle retains the exact confirmation candle for Stop-Loss anchoring in execution engine
	setup := engine.GetSetupCandle(symbol)
	if setup == nil {
		t.Fatalf("Expected GetSetupCandle to return setup candle after breakout trigger")
	}
	if setup.High != 3500.0 || setup.Low != 3480.0 {
		t.Fatalf("Expected setup High=3500.0, Low=3480.0, got High=%.2f, Low=%.2f", setup.High, setup.Low)
	}
}

func TestEMAS5BreakoutEngine_ConcurrencyRace(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0)
	symbol := "TCS"
	engine.SetPreviousDayLevels(symbol, 3500.0, 3400.0, 3450.0)

	done := make(chan bool)
	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, time.UTC)

	// Concurrently run 30 goroutines processing candles, ticks, level updates, and rule updates
	for g := 0; g < 30; g++ {
		go func(id int) {
			for i := 0; i < 50; i++ {
				engine.ProcessCandle(symbol, data.Candle{
					Time:   baseTime.Add(time.Duration(i) * time.Minute),
					Open:   3450.0 + float64(i%10),
					High:   3460.0 + float64(i%10),
					Low:    3445.0 + float64(i%10),
					Close:  3455.0 + float64(i%10),
					Volume: 1000,
				})
				_ = engine.CheckBreakout(symbol, 3465.0, "")
				_ = engine.GetSetupCandle(symbol)
				_ = engine.CandleTimeFrame()
				if i%10 == 0 {
					engine.UpdateRules(2, 5, 0.5, 2.0, 1, 1.0, "11:00:00")
					engine.SetPreviousDayLevels(symbol, 3500.0, 3400.0, 3450.0)
				}
			}
			done <- true
		}(g)
	}

	for g := 0; g < 30; g++ {
		<-done
	}
}

func TestEMAS5BreakoutEngine_BottomToTopOvalShape(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0)
	engine.SetMinPDHPDLRetracePct(0.0)
	symbol := "TATAMOTORS"
	engine.SetPreviousDayLevels(symbol, 1000.0, 950.0, 980.0)

	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, time.UTC)

	// 20 baseline candles
	for i := 0; i < 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i) * time.Minute),
			Open:   980.0,
			High:   985.0,
			Low:    975.0,
			Close:  980.0,
			Volume: 1000,
		})
	}

	// 6 Oval curve candles:
	// Drops down to swing bottom at 970.0 (Index 20), then curves upward over 6 candles (Index 21 -> 26)
	ovalCandles := []struct {
		o, h, l, c float64
	}{
		{978.0, 980.0, 970.0, 972.0}, // Lowest Low = 970.0 (Index 20)
		{972.0, 976.0, 971.0, 975.0},
		{975.0, 980.0, 974.0, 978.0},
		{978.0, 986.0, 977.0, 985.0},
		{985.0, 990.0, 982.0, 988.0},
		{988.0, 992.0, 984.0, 990.0},
	}

	for i, c := range ovalCandles {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(20+i) * time.Minute),
			Open:   c.o,
			High:   c.h,
			Low:    c.l,
			Close:  c.c,
			Volume: 1000,
		})
	}

	// Master Candle (Index 26): Green, touches EMA10/20 (Low = 986.0), closes above all at 1002.0 (surges above PDH 1000.0)
	// Rebound from 970.0 is (1002 - 970)/970 = +3.3% >= 0.5%
	// Distance from lowest low (Index 20) is 26 - 20 = 6 candles >= 5
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(26 * time.Minute),
		Open:   988.0,
		High:   1004.0,
		Low:    986.0, // Touches EMA10/20 ~980.0-986.0
		Close:  1002.0,
		Volume: 2000,
	})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected BUY Master Candle to form on bottom-to-top oval curve rebound")
	}
	if engine.masterDirections[symbol] != "BUY" {
		t.Fatalf("Expected Master Direction to be BUY, got %s", engine.masterDirections[symbol])
	}
}

func TestEMAS5BreakoutEngine_MasterIsLowestLowRejected(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0)
	symbol := "INFY"
	engine.SetPreviousDayLevels(symbol, 1500.0, 1400.0, 1450.0)

	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, time.UTC)

	// 20 baseline candles
	for i := 0; i < 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i) * time.Minute),
			Open:   1450.0,
			High:   1455.0,
			Low:    1445.0,
			Close:  1450.0,
			Volume: 1000,
		})
	}

	// 5 Candles at 1460.0 (Lows: 1455.0)
	for i := 0; i < 5; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(20+i) * time.Minute),
			Open:   1460.0,
			High:   1465.0,
			Low:    1455.0,
			Close:  1460.0,
			Volume: 1000,
		})
	}

	// Candidate Master dips severely so Master.Low = 1430.0 (which is lowest low across window)
	// Because lowestIdx is on Master itself, it is NOT a bottom-to-top curve rebound and must be rejected
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(25 * time.Minute),
		Open:   1450.0,
		High:   1470.0,
		Low:    1430.0, // Lowest point is on Master candle
		Close:  1468.0,
		Volume: 1000,
	})

	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Expected candidate to be rejected when Master candle itself is the lowest low")
	}
}

// Edge Case 1: Insufficient Distance from Day Extreme (e.g. 2 candles < 5 candles requirement)
func TestEMAS5BreakoutEngine_EdgeCase_InsufficientDistance(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	symbol := "TCS"
	engine.SetPreviousDayLevels(symbol, 2300.0, 2250.0, 2280.0)

	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, time.UTC)

	// Candles 0 to 19 at 2290
	for i := 0; i < 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 2290.0, High: 2295.0, Low: 2285.0, Close: 2290.0, Volume: 1000})
	}

	// Day's Lowest Low occurs at Index 20 (only 2 candles before candidate master)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(20 * time.Minute), Open: 2285.0, High: 2286.0, Low: 2260.0, Close: 2275.0, Volume: 1000})
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(21 * time.Minute), Open: 2275.0, High: 2285.0, Low: 2275.0, Close: 2284.0, Volume: 1000})

	// Candidate Master at Index 22 (Distance from 2260.0 bottom is only 2 candles < 5)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(22 * time.Minute), Open: 2284.0, High: 2305.0, Low: 2284.0, Close: 2302.0, Volume: 2000})

	// Must be REJECTED because distance from Lowest Low (2 candles) is less than rallyCandlesCount (5)
	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Expected Master Candle to be rejected due to insufficient distance from Day Lowest Low (2 < 5)")
	}
}

// Edge Case 2: Failed Confirmation Candle Color Guard (BUY candidate breaks Master High but closes RED)
func TestEMAS5BreakoutEngine_EdgeCase_FailedConfirmationColor_BUY(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	engine.SetMinPDHPDLRetracePct(0.0)
	symbol := "TCS"
	engine.SetPreviousDayLevels(symbol, 2300.0, 2250.0, 2280.0)

	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, time.UTC)

	// Feed Day Peak
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime, Open: 2330.0, High: 2340.0, Low: 2328.0, Close: 2335.0, Volume: 1000})

	// Feed 20 baseline candles establishing Trough at 2321.0
	for i := 1; i <= 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 2326.0, High: 2328.0, Low: 2321.0, Close: 2324.0, Volume: 1000})
	}
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(21 * time.Minute), Open: 2323.0, High: 2328.0, Low: 2323.0, Close: 2327.0, Volume: 1000})

	// Master Candle (GREEN, High: 2333.0, Low: 2324.0, Close: 2332.2, Open: 2325.0 -> Wick % = 20% <= 40%)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(22 * time.Minute), Open: 2325.0, High: 2333.0, Low: 2324.0, Close: 2332.2, Volume: 1000})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected Master Candle to be established")
	}

	// Next Candle: Breaks Master High (2333.9 > 2333.0) but closes RED (Open 2332.4, Close 2329.6)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(23 * time.Minute), Open: 2332.4, High: 2333.9, Low: 2328.9, Close: 2329.6, Volume: 1000})

	// Confirmation MUST be rejected and Master setup INVALIDATED due to RED rejection candle!
	if engine.confirmationCandles[symbol] != nil {
		t.Fatalf("Confirmation Candle should NOT form on a RED rejection candle")
	}
	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Master setup should be invalidated when breakout candle closes RED")
	}
}

// Edge Case 3: Failed Confirmation Candle Color Guard (SELL candidate breaks Master Low but closes GREEN)
func TestEMAS5BreakoutEngine_EdgeCase_FailedConfirmationColor_SELL(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	symbol := "NBCC"
	engine.SetPreviousDayLevels(symbol, 90.0, 88.50, 89.0)

	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, time.UTC)

	// Feed Day Trough candle
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime, Open: 86.0, High: 86.5, Low: 85.5, Close: 86.0, Volume: 1000})

	// Feed 20 baseline candles establishing Peak at 89.0
	for i := 1; i <= 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 88.5, High: 89.0, Low: 88.0, Close: 88.5, Volume: 1000})
	}

	// Master Candle (RED, High: 88.60, Low: 87.80, Close: 88.00, Open: 88.55 -> Wick % = 31.25% <= 40%)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(21 * time.Minute), Open: 88.55, High: 88.60, Low: 87.80, Close: 88.00, Volume: 1000})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected SELL Master Candle to be established")
	}

	// Next Candle: Breaks Master Low (87.70 < 87.80) but closes GREEN (Open 87.70, Close 88.30)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(22 * time.Minute), Open: 87.70, High: 88.40, Low: 87.70, Close: 88.30, Volume: 1000})

	// Confirmation MUST be rejected and Master setup INVALIDATED due to GREEN rejection candle!
	if engine.confirmationCandles[symbol] != nil {
		t.Fatalf("Confirmation Candle should NOT form on a GREEN rejection candle for SELL")
	}
	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Master setup should be invalidated when breakdown candle closes GREEN")
	}
}

// Edge Case 4: Confirmation Range Filter (> 1.0% Invalidates)
func TestEMAS5BreakoutEngine_EdgeCase_ConfirmationRangeExceeded(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	symbol := "TCS"
	engine.SetPreviousDayLevels(symbol, 2300.0, 2250.0, 2280.0)

	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, time.UTC)

	for i := 0; i < 7; i++ {
		engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 2330.0, High: 2335.0, Low: 2321.0, Close: 2327.0, Volume: 1000})
	}
	// Master Candle
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(7 * time.Minute), Open: 2327.0, High: 2332.0, Low: 2326.0, Close: 2331.0, Volume: 1000})

	// Next Candle: Breaks High (2335.0 > 2332.0) and is GREEN, but range is 1.5% > 1.0% (High 2355, Low 2320, Close 2345)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(8 * time.Minute), Open: 2328.0, High: 2355.0, Low: 2320.0, Close: 2345.0, Volume: 1000})

	// Must be INVALIDATED due to range > 1.0%
	if engine.confirmationCandles[symbol] != nil {
		t.Fatalf("Confirmation should be rejected when range exceeds 1.0%%")
	}
	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Master setup should be invalidated when confirmation range exceeds 1.0%%")
	}
}

// Edge Case 5: Master Range Filter (> 2.0% Rejected)
func TestEMAS5BreakoutEngine_EdgeCase_MasterRangeExceeded(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	symbol := "TCS"
	engine.SetPreviousDayLevels(symbol, 2300.0, 2250.0, 2280.0)

	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, time.UTC)

	for i := 0; i < 7; i++ {
		engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 2330.0, High: 2335.0, Low: 2321.0, Close: 2327.0, Volume: 1000})
	}
	// Candidate Master with range 3.0% (High 2370, Low 2300, Close 2360) > 2.0%
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(7 * time.Minute), Open: 2310.0, High: 2370.0, Low: 2300.0, Close: 2360.0, Volume: 1000})

	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Master candidate should be rejected when range exceeds 2.0%%")
	}
}

// Edge Case 6: Master Low Breach Invalidation
func TestEMAS5BreakoutEngine_EdgeCase_MasterLowBreach(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	engine.SetMinPDHPDLRetracePct(0.0)
	symbol := "TCS"
	engine.SetPreviousDayLevels(symbol, 2300.0, 2250.0, 2280.0)

	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, time.UTC)

	for i := 0; i < 7; i++ {
		engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 2330.0, High: 2335.0, Low: 2321.0, Close: 2327.0, Volume: 1000})
	}
	// Master Candle (High: 2332.0, Low: 2326.0)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(7 * time.Minute), Open: 2327.0, High: 2332.0, Low: 2326.0, Close: 2331.0, Volume: 1000})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected Master Candle to be established")
	}

	// Subsequent candle crashes below Master Low (Low: 2320.0 < 2326.0)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(8 * time.Minute), Open: 2328.0, High: 2330.0, Low: 2320.0, Close: 2322.0, Volume: 1000})

	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Master setup should be invalidated when subsequent candle breaches Master Low")
	}
}

// TestEMAS5BreakoutEngine_MasterMaxWickInvalidation verifies that Master candidate with wicks > maxWickPct is rejected
func TestEMAS5BreakoutEngine_MasterMaxWickInvalidation(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	symbol := "RELIANCE"
	engine.SetPreviousDayLevels(symbol, 2300.0, 2280.0, 2290.0)

	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, time.UTC)

	// Feed 20 baseline candles establishing Trough at 2321.0
	for i := 0; i < 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 2326.0, High: 2328.0, Low: 2321.0, Close: 2324.0, Volume: 1000})
	}

	// Master Candidate with High Wick:
	// Range = 2334.0 - 2324.0 = 10.0
	// Open = 2328.0, Close = 2330.0 (Body = 2.0)
	// Wick = 8.0 -> Wick % = 80% > 40.0%
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(21 * time.Minute),
		Open:   2328.0,
		High:   2334.0,
		Low:    2324.0,
		Close:  2330.0,
		Volume: 1000,
	})

	// Master candle should NOT be established due to excess wick %
	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Master candle with 80%% wick should be rejected when max wick is 40%%")
	}
}

// TestEMAS5BreakoutEngine_RejectCOLPALBrokenArc verifies that a broken arc with a recent unconfirmed down-leg (like COLPAL 04-Sep) is rejected
func TestEMAS5BreakoutEngine_RejectCOLPALBrokenArc(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	symbol := "COLPAL"
	engine.SetPreviousDayLevels(symbol, 1860.0, 1820.0, 1840.0)

	baseTime := time.Date(2026, 9, 4, 9, 15, 0, 0, time.UTC)

	// Feed 09:15 to 09:40 baseline
	for i := 0; i < 6; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   1860.0 - float64(i)*5.0,
			High:   1863.0 - float64(i)*5.0,
			Low:    1850.0 - float64(i)*5.0,
			Close:  1852.0 - float64(i)*5.0,
			Volume: 1000,
		})
	}

	// 09:45 (Index 6): Absolute Day Lowest Low = 1829.30
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(30 * time.Minute),
		Open:   1832.0,
		High:   1834.4,
		Low:    1829.3,
		Close:  1830.0,
		Volume: 1000,
	})

	// 09:50 to 10:30 recovery up to 1839
	for i := 1; i <= 8; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(30+i*5) * time.Minute),
			Open:   1830.0 + float64(i)*1.0,
			High:   1832.0 + float64(i)*1.0,
			Low:    1829.5 + float64(i)*1.0,
			Close:  1831.0 + float64(i)*1.0,
			Volume: 1000,
		})
	}

	// 10:35 (Index 15): Intermediate Peak = 1840.0
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(80 * time.Minute),
		Open:   1838.0,
		High:   1840.0,
		Low:    1837.5,
		Close:  1839.0,
		Volume: 1000,
	})

	// 10:40 (Index 16): Down leg starts
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(85 * time.Minute),
		Open:   1839.3,
		High:   1839.3,
		Low:    1836.0,
		Close:  1836.0,
		Volume: 1000,
	})

	// 10:45 (Index 17): Lower low
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(90 * time.Minute),
		Open:   1836.0,
		High:   1836.9,
		Low:    1835.0,
		Close:  1835.0,
		Volume: 1000,
	})

	// 10:50 (Index 18): Fresh local trough = 1834.10 (only 1 candle prior!)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(95 * time.Minute),
		Open:   1835.0,
		High:   1837.0,
		Low:    1834.1,
		Close:  1836.9,
		Volume: 1000,
	})

	// 10:55 (Index 19): 1-Candle V-Spike jumping to 1841.30
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(100 * time.Minute),
		Open:   1836.9,
		High:   1841.5,
		Low:    1836.9,
		Close:  1841.3,
		Volume: 2000,
	})

	// MUST BE REJECTED because the U-curve arc was broken by 10:35 peak & 10:50 local decline!
	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Expected COLPAL 10:55 V-Spike to be REJECTED, but Master Candle was established!")
	}
}

// TestEMAS5BreakoutEngine_RejectOneCandleVSpike tests that sharp 1-candle drops/spikes without rounded base are rejected
func TestEMAS5BreakoutEngine_RejectOneCandleVSpike(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	symbol := "V_SPIKE_STOCK"
	engine.SetPreviousDayLevels(symbol, 1000.0, 950.0, 980.0)

	baseTime := time.Date(2026, 9, 4, 9, 15, 0, 0, time.UTC)

	// Feed 10 flat candles around 980
	for i := 0; i < 10; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   980.0,
			High:   982.0,
			Low:    978.0,
			Close:  980.0,
			Volume: 1000,
		})
	}

	// Candle 10: Sharp plunge (Open: 980, Low: 960, Close: 962)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(50 * time.Minute),
		Open:   980.0,
		High:   980.0,
		Low:    960.0,
		Close:  962.0,
		Volume: 1000,
	})

	// Candle 11: Immediate 1-candle jump back up (Open: 962, High: 984, Low: 962, Close: 982)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(55 * time.Minute),
		Open:   962.0,
		High:   984.0,
		Low:    962.0,
		Close:  982.0,
		Volume: 2000,
	})

	// MUST be rejected as an anti-V-spike violation (trough at candle 10 was 1 candle prior)
	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Expected 1-candle V-spike to be REJECTED, but Master candle was established!")
	}
}

// TestEMAS5BreakoutEngine_TCS_ValidUShape verifies the real-world 28-Aug-2026 TCS 5m setup
func TestEMAS5BreakoutEngine_TCS_ValidUShape(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	engine.SetMinPDHPDLRetracePct(0.0)
	symbol := "TCS"
	engine.SetPreviousDayLevels(symbol, 2320.0, 2290.0, 2310.0)
	engine.SetTradeEndTime("14:30:00")

	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, time.UTC)

	// 09:15 to 09:35 (Peak High 2335.0 at Index 4, Open dip at 09:15)
	for i := 0; i <= 4; i++ {
		lowVal := 2323.0 + float64(i)*2.0
		if i == 0 {
			lowVal = 2319.0 // Day Lowest Low at morning open
		}
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   2325.0 + float64(i)*2.0,
			High:   2327.0 + float64(i)*2.0,
			Low:    lowVal,
			Close:  2326.0 + float64(i)*2.0,
			Volume: 1000,
		})
	}

	// 09:40 to 11:40 (Index 5 to 29): Gradual descent to Trough Low 2321.0 at Index 29
	for i := 5; i <= 29; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   2335.0 - float64(i-4)*0.55,
			High:   2336.0 - float64(i-4)*0.55,
			Low:    2333.0 - float64(i-4)*0.55,
			Close:  2334.0 - float64(i-4)*0.55,
			Volume: 1000,
		})
	}

	// 11:45 to 11:50: Curving upward (Index 30, 31)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(150 * time.Minute),
		Open:   2322.0,
		High:   2326.0,
		Low:    2322.0,
		Close:  2325.0,
		Volume: 1000,
	})
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(155 * time.Minute),
		Open:   2325.0,
		High:   2329.0,
		Low:    2325.0,
		Close:  2328.0,
		Volume: 1000,
	})

	// 11:55: Master Candle (Index 32)
	// Open: 2327.0, High: 2331.5, Low: 2326.0, Close: 2331.0 GREEN (Touches EMA10 ~2326.5, Wick % = 27.2% <= 40%)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(160 * time.Minute),
		Open:   2327.0,
		High:   2331.5,
		Low:    2326.0,
		Close:  2331.0,
		Volume: 2000,
	})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected valid TCS 5m Bullish U-Shape Master Candle to be established")
	}
	if engine.masterDirections[symbol] != "BUY" {
		t.Fatalf("Expected BUY direction for TCS, got %s", engine.masterDirections[symbol])
	}
}

// TestEMAS5BreakoutEngine_NBCC_ValidInvertedUShape verifies the real-world 28-Aug-2026 NBCC 5m setup
func TestEMAS5BreakoutEngine_NBCC_ValidInvertedUShape(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	engine.SetMinPDHPDLRetracePct(0.0)
	symbol := "NBCC"
	engine.SetPreviousDayLevels(symbol, 90.0, 88.42, 89.0)

	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, time.UTC)

	// 09:15: Peak High 89.28 (Index 0)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime,
		Open:   88.50,
		High:   89.28,
		Low:    88.40,
		Close:  88.80,
		Volume: 1000,
	})

	// 09:20 to 10:25: Highs hovering 89.20 -> 88.60 (Index 1 to 14)
	for i := 1; i <= 14; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   88.80 - float64(i)*0.03,
			High:   89.00 - float64(i)*0.03,
			Low:    88.50 - float64(i)*0.03,
			Close:  88.70 - float64(i)*0.03,
			Volume: 1000,
		})
	}

	// 10:30: Master Candle RED (Index 15)
	// Open: 88.42, High: 88.44, Low: 88.29, Close: 88.31 RED
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(75 * time.Minute),
		Open:   88.42,
		High:   88.44,
		Low:    88.29,
		Close:  88.31,
		Volume: 2000,
	})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected valid NBCC 5m Bearish Inverted U-Shape Master Candle to be established")
	}
	if engine.masterDirections[symbol] != "SELL" {
		t.Fatalf("Expected SELL direction for NBCC, got %s", engine.masterDirections[symbol])
	}
}

func TestEMAS5BreakoutEngine_MaxEntryDistanceGuard(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	engine.SetMaxEntryDistancePct(0.35)

	if engine.MaxEntryDistancePct() != 0.35 {
		t.Fatalf("Expected MaxEntryDistancePct to be 0.35, got %f", engine.MaxEntryDistancePct())
	}

	// 1. BUY Test
	buySymbol := "TESTBUY"
	engine.masterDirections[buySymbol] = "BUY"
	engine.confirmationCandles[buySymbol] = &data.Candle{
		High: 1000.0,
		Low:  990.0,
	}

	// Within tolerance (1000.0 <= LTP <= 1003.50): Should trigger BUY
	sig := engine.CheckBreakout(buySymbol, 1002.0, "")
	if sig == nil {
		t.Fatalf("Expected BUY breakout to trigger when LTP (1002.0) is within 0.35%% of Confirmation High (1000.0)")
	}
	if sig.Action != "BUY" {
		t.Fatalf("Expected action BUY, got %s", sig.Action)
	}

	// Reset confirmation for late entry test
	engine.tradeCountsPerStock[buySymbol] = 0
	engine.confirmationCandles[buySymbol] = &data.Candle{
		High: 1000.0,
		Low:  990.0,
	}
	// Beyond tolerance (LTP 1005.0 > 1003.50): Should be rejected as late trigger
	sigLate := engine.CheckBreakout(buySymbol, 1005.0, "")
	if sigLate != nil {
		t.Fatalf("Expected BUY breakout to be rejected when LTP (1005.0) exceeds 0.35%% threshold (1003.50)")
	}

	// 2. SELL Test
	sellSymbol := "POLICYBZR"
	engine.masterDirections[sellSymbol] = "SELL"
	engine.confirmationCandles[sellSymbol] = &data.Candle{
		High: 1813.70,
		Low:  1808.40,
	}

	// Confirmation Low = 1808.40. Threshold (0.35% below) = 1808.40 * (1 - 0.0035) = 1802.07
	// If LTP = 1805.00 (within tolerance), should trigger SELL
	sigSell := engine.CheckBreakout(sellSymbol, 1805.00, "")
	if sigSell == nil {
		t.Fatalf("Expected SELL breakout to trigger when LTP (1805.00) is within 0.35%% of Confirmation Low (1808.40)")
	}
	if sigSell.Action != "SELL" {
		t.Fatalf("Expected action SELL, got %s", sigSell.Action)
	}

	// Reset confirmation for late entry test (e.g. after mid-day reboot when price is already at 1780.00)
	engine.tradeCountsPerStock[sellSymbol] = 0
	engine.confirmationCandles[sellSymbol] = &data.Candle{
		High: 1813.70,
		Low:  1808.40,
	}
	// Beyond tolerance (LTP 1780.00 < 1802.07): Should be rejected as late trigger
	sigSellLate := engine.CheckBreakout(sellSymbol, 1780.00, "")
	if sigSellLate != nil {
		t.Fatalf("Expected SELL breakout to be rejected when LTP (1780.00) exceeds 0.35%% threshold (1802.07)")
	}
}

// Test confirmation candle for BUY that breaks Master High, closes below Master High but strictly above Master Low with GREEN body
func TestEMAS5BreakoutEngine_ConfirmationClose_AboveMasterLow_Accepted_BUY(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	engine.SetMinPDHPDLRetracePct(0.0)
	symbol := "TCS"
	engine.SetPreviousDayLevels(symbol, 2300.0, 2250.0, 2280.0)

	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, time.UTC)

	// Feed Day Peak
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime, Open: 2330.0, High: 2340.0, Low: 2328.0, Close: 2335.0, Volume: 1000})

	// Feed 20 baseline candles establishing Trough at 2321.0
	for i := 1; i <= 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 2326.0, High: 2328.0, Low: 2321.0, Close: 2324.0, Volume: 1000})
	}
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(21 * time.Minute), Open: 2323.0, High: 2328.0, Low: 2323.0, Close: 2327.0, Volume: 1000})

	// Master Candle (GREEN, High: 2333.0, Low: 2324.0, Close: 2332.2, Open: 2325.0)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(22 * time.Minute), Open: 2325.0, High: 2333.0, Low: 2324.0, Close: 2332.2, Volume: 1000})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected Master Candle to be established")
	}

	// Confirmation Candle: Breaks Master High (2334.0 > 2333.0), closes at 2331.0 (<= Master High 2333.0, but > Master Low 2324.0) with GREEN body (Open: 2328.0, Close: 2331.0)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(23 * time.Minute), Open: 2328.0, High: 2334.0, Low: 2328.0, Close: 2331.0, Volume: 1000})

	if engine.confirmationCandles[symbol] == nil {
		t.Fatalf("Expected Confirmation Candle to form when candle breaks Master High and closes above Master Low with GREEN body")
	}
	if engine.confirmationCandles[symbol].High != 2334.0 {
		t.Fatalf("Expected Confirmation High to be 2334.0, got %f", engine.confirmationCandles[symbol].High)
	}
}

// Test confirmation candle for SELL that breaks Master Low, closes above Master Low but strictly below Master High with RED body
func TestEMAS5BreakoutEngine_ConfirmationClose_BelowMasterHigh_Accepted_SELL(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	symbol := "NBCC"
	engine.SetPreviousDayLevels(symbol, 90.0, 88.50, 89.0)

	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, time.UTC)

	// Feed Day Trough candle
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime, Open: 86.0, High: 86.5, Low: 85.5, Close: 86.0, Volume: 1000})

	// Feed 20 baseline candles establishing Peak at 89.0
	for i := 1; i <= 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 88.5, High: 89.0, Low: 88.0, Close: 88.5, Volume: 1000})
	}

	// Master Candle (RED, High: 88.60, Low: 87.80, Close: 88.00, Open: 88.55)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(21 * time.Minute), Open: 88.55, High: 88.60, Low: 87.80, Close: 88.00, Volume: 1000})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected SELL Master Candle to be established")
	}

	// Confirmation Candle: Breaks Master Low (87.60 < 87.80), closes at 88.10 (>= Master Low 87.80, but < Master High 88.60) with RED body (Open: 88.40, Close: 88.10)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(22 * time.Minute), Open: 88.40, High: 88.40, Low: 87.60, Close: 88.10, Volume: 1000})

	if engine.confirmationCandles[symbol] == nil {
		t.Fatalf("Expected Confirmation Candle to form when candle breaks Master Low and closes below Master High with RED body")
	}
	if engine.confirmationCandles[symbol].Low != 87.60 {
		t.Fatalf("Expected Confirmation Low to be 87.60, got %f", engine.confirmationCandles[symbol].Low)
	}
}

// Test Master candle that touches PDH (within buffer) and closes above EMA 10, EMA 20, and PDH
func TestEMAS5BreakoutEngine_MasterTouchesPDH_Accepted_BUY(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	symbol := "INFY"
	// Set PDH = 150.0
	engine.SetPreviousDayLevels(symbol, 150.0, 140.0, 145.0)

	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, time.UTC)

	// Feed Day Peak
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime, Open: 151.0, High: 153.0, Low: 148.0, Close: 150.0, Volume: 1000})

	// Feed baseline candles establishing Trough at 146.0
	for i := 1; i <= 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 147.0, High: 147.5, Low: 146.0, Close: 147.0, Volume: 1000})
	}
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(21 * time.Minute), Open: 147.0, High: 148.5, Low: 147.0, Close: 148.0, Volume: 1000})

	// Master Candle: Low touches PDH (150.0), closes at 151.5 (> PDH 150.0 and > EMAs ~147.5) with GREEN body (Open: 149.5)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(22 * time.Minute), Open: 149.5, High: 152.0, Low: 149.9, Close: 151.5, Volume: 1000})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected Master Candle to form when touching PDH and closing above all 3 levels (EMA 10, EMA 20, PDH)")
	}
	if engine.masterDirections[symbol] != "BUY" {
		t.Fatalf("Expected Master Direction to be BUY, got %s", engine.masterDirections[symbol])
	}
}

// Test Master candle that touches PDL (within buffer) and closes below EMA 10, EMA 20, and PDL
func TestEMAS5BreakoutEngine_MasterTouchesPDL_Accepted_SELL(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	symbol := "WIPRO"
	// Set PDL = 100.0
	engine.SetPreviousDayLevels(symbol, 110.0, 100.0, 105.0)

	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, time.UTC)

	// Feed Day Trough
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime, Open: 98.0, High: 101.0, Low: 97.0, Close: 99.0, Volume: 1000})

	// Feed Day Peak candle (High = 104.0) at index 1
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(1 * time.Minute), Open: 101.0, High: 104.0, Low: 101.0, Close: 103.0, Volume: 1000})

	// Feed baseline descending candles
	for i := 2; i <= 20; i++ {
		engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 102.0, High: 102.5, Low: 101.0, Close: 101.5, Volume: 1000})
	}
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(21 * time.Minute), Open: 101.5, High: 101.8, Low: 100.2, Close: 100.5, Volume: 1000})

	// Master Candle: High touches PDL (100.0), closes at 98.5 (< PDL 100.0 and < EMAs ~101.5) with RED body (Open: 100.0, High: 100.1, Low: 98.3, Close: 98.5)
	engine.ProcessCandle(symbol, data.Candle{Time: baseTime.Add(22 * time.Minute), Open: 100.0, High: 100.1, Low: 98.3, Close: 98.5, Volume: 1000})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected Master Candle to form when touching PDL and closing below all 3 levels (EMA 10, EMA 20, PDL)")
	}
	if engine.masterDirections[symbol] != "SELL" {
		t.Fatalf("Expected Master Direction to be SELL, got %s", engine.masterDirections[symbol])
	}
}

// Test that WarmUpCandles loads historical candles into the rolling buffer without triggering intraday states
func TestEMAS5BreakoutEngine_WarmUpCandles_DoesNotTriggerIntradayState(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
	symbol := "INFY"
	engine.SetPreviousDayLevels(symbol, 1850.0, 1820.0, 1835.0)

	// Create 50 historical candles from yesterday
	yesterdayBase := time.Date(2026, 9, 14, 10, 0, 0, 0, data.ISTLocation)
	priorCandles := make([]data.Candle, 50)
	for i := 0; i < 50; i++ {
		priorCandles[i] = data.Candle{
			Time:   yesterdayBase.Add(time.Duration(i) * time.Minute),
			Open:   1830.0,
			High:   1835.0,
			Low:    1825.0,
			Close:  1830.0,
			Volume: 10000,
		}
	}

	// Warm up buffer
	engine.WarmUpCandles(symbol, priorCandles)

	engine.mu.RLock()
	bufLen := len(engine.rollingCandles[symbol])
	master := engine.masterCandles[symbol]
	confirm := engine.confirmationCandles[symbol]
	first := engine.firstCandles[symbol]
	engine.mu.RUnlock()

	if bufLen != 50 {
		t.Fatalf("expected 50 candles in rolling buffer, got %d", bufLen)
	}
	if master != nil {
		t.Fatalf("expected nil master candle during warm-up, got %+v", master)
	}
	if confirm != nil {
		t.Fatalf("expected nil confirmation candle during warm-up, got %+v", confirm)
	}
	if first != nil {
		t.Fatalf("expected nil first candle during warm-up, got %+v", first)
	}
}

// Test that 100 historical warm-up candles from yesterday cannot be counted as the intraday U-shape base
// for premature Master/Confirmation formation on the first two morning candles of today.
func TestEMAS5BreakoutEngine_WarmUpCandles_CrossDayLeakRejected(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.35, 2.0, 1, 1.0)
	engine.SetCandleTimeFrame("5m")
	symbol := "GODREJPROP"
	engine.SetPreviousDayLevels(symbol, 1695.30, 1657.70, 1690.00)

	// 1. Create 100 historical warm-up candles from yesterday (2026-09-17)
	yesterdayBase := time.Date(2026, 9, 17, 9, 15, 0, 0, data.ISTLocation)
	priorCandles := make([]data.Candle, 100)
	for i := 0; i < 100; i++ {
		cTime := yesterdayBase.Add(time.Duration(i*5) * time.Minute)
		low := 1680.0
		if i == 0 {
			low = 1657.70 // Trough low from 17-Sep morning
		}
		priorCandles[i] = data.Candle{
			Time:   cTime,
			Open:   1685.0,
			High:   1690.0,
			Low:    low,
			Close:  1688.0,
			Volume: 10000,
		}
	}
	engine.WarmUpCandles(symbol, priorCandles)

	// 2. Feed today's 1st 5m candle (09:15-09:20 IST on 2026-09-18)
	c1Time := time.Date(2026, 9, 18, 9, 15, 0, 0, data.ISTLocation)
	c1 := data.Candle{
		Time:   c1Time,
		Open:   1686.90,
		High:   1703.40,
		Low:    1686.90,
		Close:  1698.20,
		Volume: 22166,
	}
	engine.OnCandleClose(&c1, symbol)

	engine.mu.RLock()
	masterC1 := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	// Master candle MUST NOT form on the 1st candle of today despite 100 historical warm-up candles
	if masterC1 != nil {
		t.Fatalf("expected nil Master candle on 1st candle of today (09:15 AM), got %+v", masterC1)
	}

	// 3. Feed today's 2nd 5m candle (09:20-09:25 IST on 2026-09-18)
	c2Time := time.Date(2026, 9, 18, 9, 20, 0, 0, data.ISTLocation)
	c2 := data.Candle{
		Time:   c2Time,
		Open:   1697.70,
		High:   1706.00,
		Low:    1697.70,
		Close:  1706.00,
		Volume: 11203,
	}
	engine.OnCandleClose(&c2, symbol)

	engine.mu.RLock()
	masterC2 := engine.masterCandles[symbol]
	confirmC2 := engine.confirmationCandles[symbol]
	engine.mu.RUnlock()

	if masterC2 != nil {
		t.Fatalf("expected nil Master candle on 2nd candle of today (09:20 AM), got %+v", masterC2)
	}
	if confirmC2 != nil {
		t.Fatalf("expected nil Confirmation candle on 2nd candle of today, got %+v", confirmC2)
	}

	// 4. Live tick breakout check at 09:25:08 IST must return nil
	sig := engine.CheckBreakout(symbol, 1706.40, "BUY")
	if sig != nil {
		t.Fatalf("expected nil breakout signal at 09:25:08 IST after only 2 candles, got %+v", sig)
	}
}

func TestEMAS5BreakoutEngine_RetestBelowConfirmationLow_PreservesArmedSetup(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.20, 2.0, 1, 1.0)
	engine.SetMinPDHPDLRetracePct(0.0)
	symbol := "HAL"
	engine.SetPreviousDayLevels(symbol, 4815.0, 4750.0, 4780.0)

	baseTime := time.Date(2026, 9, 18, 9, 15, 0, 0, data.ISTLocation)

	// Morning start peak -> trough
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime,
		Open:   4800.0, High: 4820.0, Low: 4790.0, Close: 4810.0, Volume: 5000,
	})
	// Pullback candles to trough at 4780.0
	for i := 1; i <= 10; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   4790.0, High: 4795.0, Low: 4780.0, Close: 4785.0, Volume: 2000,
		})
	}
	// Rally candles
	for i := 11; i <= 15; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   4785.0 + float64(i-11)*3.0,
			High:   4792.0 + float64(i-11)*3.0,
			Low:    4784.0 + float64(i-11)*3.0,
			Close:  4790.0 + float64(i-11)*3.0,
			Volume: 3000,
		})
	}

	// 10:40 Master Candle: Green, Low = 4808.10, High = 4830.00
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(17 * 5 * time.Minute), // 10:40
		Open:   4809.0, High: 4830.0, Low: 4808.10, Close: 4828.0, Volume: 15000,
	})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected Master Candle to be formed")
	}
	masterLow := engine.masterCandles[symbol].Low

	// 10:45 Confirmation Candle: High = 4837.90, Low = 4823.00, Close = 4835.0 (Green, breaks Master High)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(18 * 5 * time.Minute), // 10:45
		Open:   4828.0, High: 4837.90, Low: 4823.00, Close: 4835.0, Volume: 12000,
	})

	if engine.confirmationCandles[symbol] == nil {
		t.Fatalf("Expected Confirmation Candle to be formed and armed")
	}
	confirmHigh := engine.confirmationCandles[symbol].High
	confirmLow := engine.confirmationCandles[symbol].Low

	// 10:50 Retest Candle: High = 4836.30, Low = 4821.90 (Breaches Confirm Low 4823.00, but > Master Low 4808.10)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(19 * 5 * time.Minute), // 10:50
		Open:   4835.0, High: 4836.30, Low: 4821.90, Close: 4824.0, Volume: 8000,
	})

	// Assert that setup is NOT invalidated!
	if engine.confirmationCandles[symbol] == nil {
		t.Fatalf("Setup should NOT be invalidated when candle low (4821.90) is above Master Low (%.2f), even if below Confirm Low (%.2f)", masterLow, confirmLow)
	}

	// 10:55 Breakout tick: LTP crosses Confirmation High 4837.90 -> 4838.50
	sig := engine.CheckBreakout(symbol, 4838.50, "BUY")
	if sig == nil {
		t.Fatalf("Expected BUY breakout signal when LTP (4838.50) crosses Confirmation High (%.2f)", confirmHigh)
	}
	if sig.Action != "BUY" {
		t.Errorf("Expected BUY signal action, got %s", sig.Action)
	}
}

func TestEMAS5BreakoutEngine_BreachingCandleImmediatelyReestablishesNewMaster(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.20, 2.0, 1, 1.0)
	engine.SetMinPDHPDLRetracePct(0.0)
	symbol := "RELIANCE"
	engine.SetPreviousDayLevels(symbol, 4815.0, 4750.0, 4780.0)

	baseTime := time.Date(2026, 9, 18, 9, 15, 0, 0, data.ISTLocation)

	// Morning start peak -> trough
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime,
		Open:   4800.0, High: 4820.0, Low: 4790.0, Close: 4810.0, Volume: 5000,
	})
	// Pullback candles to trough at 4780.0
	for i := 1; i <= 10; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   4790.0, High: 4795.0, Low: 4780.0, Close: 4785.0, Volume: 2000,
		})
	}
	// Rally candles
	for i := 11; i <= 15; i++ {
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   4785.0 + float64(i-11)*3.0,
			High:   4792.0 + float64(i-11)*3.0,
			Low:    4784.0 + float64(i-11)*3.0,
			Close:  4790.0 + float64(i-11)*3.0,
			Volume: 3000,
		})
	}

	// 10:40 c1 Master Candle: Green, Low = 4808.00, High = 4825.00
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(17 * 5 * time.Minute), // 10:40
		Open:   4809.0, High: 4825.0, Low: 4808.00, Close: 4824.0, Volume: 15000,
	})

	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected c1 to establish as Master Candle")
	}
	if engine.masterCandles[symbol].Low != 4808.00 {
		t.Errorf("Expected c1 Master Low to be 4808.00, got %.2f", engine.masterCandles[symbol].Low)
	}

	// 10:45 c2 Liquidity Sweep Candle:
	// Dips to Low = 4805.00 (breaching c1 Master Low 4808.00 -> invalidates c1)
	// But rebounds and closes Green at 4828.00, High = 4830.00 (meets all Master criteria!)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(18 * 5 * time.Minute), // 10:45
		Open:   4810.0, High: 4830.0, Low: 4805.00, Close: 4828.0, Volume: 18000,
	})

	// Assert that c2 was NOT discarded, but immediately established as the NEW Master Candle!
	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected c2 to immediately establish as NEW Master Candle after invalidating c1")
	}
	if engine.masterCandles[symbol].Low != 4805.00 {
		t.Errorf("Expected NEW Master Low to be 4805.00 (c2 Low), got %.2f", engine.masterCandles[symbol].Low)
	}
	if engine.masterCandles[symbol].High != 4830.00 {
		t.Errorf("Expected NEW Master High to be 4830.00 (c2 High), got %.2f", engine.masterCandles[symbol].High)
	}
	if engine.confirmationCandles[symbol] != nil {
		t.Errorf("Confirmation candle should be nil until next candle")
	}

	// 10:50 c3 Confirmation Candle: High = 4835.00 (breaks c2 High 4830.00), Low = 4826.00 (> c2 Low 4805.00), Close = 4833.00 (Green)
	engine.ProcessCandle(symbol, data.Candle{
		Time:   baseTime.Add(19 * 5 * time.Minute), // 10:50
		Open:   4828.0, High: 4835.0, Low: 4826.00, Close: 4833.0, Volume: 10000,
	})

	if engine.confirmationCandles[symbol] == nil {
		t.Fatalf("Expected c3 to establish as Confirmation Candle for c2")
	}
	if engine.confirmationCandles[symbol].High != 4835.00 {
		t.Errorf("Expected Confirmation High to be 4835.00, got %.2f", engine.confirmationCandles[symbol].High)
	}

	// 10:55 Breakout tick above Confirmation High 4835.00 -> 4836.00
	sig := engine.CheckBreakout(symbol, 4836.00, "BUY")
	if sig == nil {
		t.Fatalf("Expected BUY breakout signal when LTP (4836.00) crosses Confirmation High 4835.00")
	}
	if sig.Action != "BUY" {
		t.Errorf("Expected BUY signal action, got %s", sig.Action)
	}
	if sig.Candle.High != 4835.00 {
		t.Errorf("Expected Signal Candle High to be 4835.00, got %.2f", sig.Candle.High)
	}
}

func TestEMAS5BreakoutEngine_DeduplicationAndMinCandlesToIgnore(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 4, 0.30, 2.0, 6, 1.0)
	symbol := "SBIN"

	baseTime := time.Date(2026, 9, 21, 9, 15, 0, 0, data.ISTLocation)

	// 1. Test WarmUpCandles deduplication
	priorCandles := []data.Candle{
		{Time: baseTime.Add(-15 * time.Minute), Open: 500, High: 502, Low: 498, Close: 501, Volume: 1000},
		{Time: baseTime.Add(-10 * time.Minute), Open: 501, High: 503, Low: 499, Close: 502, Volume: 1000},
		{Time: baseTime.Add(-5 * time.Minute), Open: 502, High: 504, Low: 500, Close: 503, Volume: 1000},
	}
	engine.WarmUpCandles(symbol, priorCandles)
	if len(engine.rollingCandles[symbol]) != 3 {
		t.Fatalf("expected 3 candles after first warm-up, got %d", len(engine.rollingCandles[symbol]))
	}

	// Warm-up again with overlapping candles
	engine.WarmUpCandles(symbol, priorCandles)
	if len(engine.rollingCandles[symbol]) != 3 {
		t.Fatalf("expected 3 candles after duplicate warm-up, got %d", len(engine.rollingCandles[symbol]))
	}

	// 2. Test ProcessCandle deduplication
	c1 := data.Candle{
		Time:   baseTime,
		Open:   503,
		High:   505,
		Low:    501,
		Close:  504,
		Volume: 2000,
	}
	engine.ProcessCandle(symbol, c1)
	if len(engine.rollingCandles[symbol]) != 4 {
		t.Fatalf("expected 4 candles after c1, got %d", len(engine.rollingCandles[symbol]))
	}

	// Ingest c1 again (duplicate)
	engine.ProcessCandle(symbol, c1)
	if len(engine.rollingCandles[symbol]) != 4 {
		t.Fatalf("expected 4 candles after duplicate c1, got %d", len(engine.rollingCandles[symbol]))
	}
}

func TestEMAS5BreakoutEngine_MinRetracementFromPDH_BUY(t *testing.T) {
	logger := zap.NewNop()
	symbol := "TATASTEEL"
	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, data.ISTLocation)

	createEngineWithCandles := func(peakHigh float64, minRetracePct float64) *EMAS5BreakoutEngine {
		engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
		engine.SetMinPDHPDLRetracePct(minRetracePct)
		// PDH = 150.0
		engine.SetPreviousDayLevels(symbol, 150.0, 140.0, 148.0)

		// 1. Day peak high candle at 09:15 reaching peakHigh before trough
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime,
			Open:   149.8,
			High:   peakHigh,
			Low:    149.5,
			Close:  150.0,
			Volume: 1000,
		})

		// 2. Feed baseline pullback candles down to lowestLow = 148.5
		lowestLow := 148.5
		for i := 1; i <= 15; i++ {
			engine.ProcessCandle(symbol, data.Candle{
				Time:   baseTime.Add(time.Duration(i) * time.Minute),
				Open:   149.7,
				High:   149.8,
				Low:    lowestLow,
				Close:  lowestLow + 0.1,
				Volume: 1000,
			})
		}

		// 3. Feed 4 rally sequence candles
		rally := []struct{ o, h, l, c float64 }{
			{lowestLow + 0.1, lowestLow + 0.3, lowestLow + 0.1, lowestLow + 0.25},
			{lowestLow + 0.25, lowestLow + 0.4, lowestLow + 0.2, lowestLow + 0.35},
			{lowestLow + 0.35, lowestLow + 0.5, lowestLow + 0.3, lowestLow + 0.45},
			{lowestLow + 0.45, lowestLow + 0.6, lowestLow + 0.4, lowestLow + 0.55},
		}
		for i, p := range rally {
			engine.ProcessCandle(symbol, data.Candle{
				Time:   baseTime.Add(time.Duration(16+i) * time.Minute),
				Open:   p.o,
				High:   p.h,
				Low:    p.l,
				Close:  p.c,
				Volume: 1000,
			})
		}

		// 4. Candidate Master candle: Close above EMA10, EMA20 and PDH (150.0)
		masterOpen := lowestLow + 0.55
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(20 * time.Minute),
			Open:   masterOpen,
			High:   150.8,
			Low:    masterOpen - 0.1,
			Close:  150.6, // Closes above PDH 150.0, Green body
			Volume: 2000,
		})

		return engine
	}

	t.Run("Insufficient Retracement Rejected", func(t *testing.T) {
		// peakHigh = 150.4 => Extension above PDH (150.0) = (150.4 - 150)/150 = 0.27% < 0.50%
		engine := createEngineWithCandles(150.4, 0.50)
		if engine.masterCandles[symbol] != nil {
			t.Fatalf("Expected Master Candle to be REJECTED due to insufficient extension above PDH (0.27%% < 0.50%%)")
		}
	})

	t.Run("Sufficient Retracement Accepted", func(t *testing.T) {
		// peakHigh = 151.0 => Extension above PDH (150.0) = (151.0 - 150)/150 = 0.67% >= 0.50%
		engine := createEngineWithCandles(151.0, 0.50)
		if engine.masterCandles[symbol] == nil {
			t.Fatalf("Expected Master Candle to be ESTABLISHED with sufficient extension above PDH (0.67%% >= 0.50%%)")
		}
		if engine.masterDirections[symbol] != "BUY" {
			t.Fatalf("Expected Master Direction BUY, got %s", engine.masterDirections[symbol])
		}
	})

	t.Run("Disabled Filter Allows Shallow Retracement", func(t *testing.T) {
		// peakHigh = 150.4 => Extension 0.27%, but minPDHPDLRetracePct = 0.0 (disabled)
		engine := createEngineWithCandles(150.4, 0.0)
		if engine.masterCandles[symbol] == nil {
			t.Fatalf("Expected Master Candle to form when filter is disabled (0.0%%)")
		}
	})
}

func TestEMAS5BreakoutEngine_MinRetracementFromPDL_SELL(t *testing.T) {
	logger := zap.NewNop()
	symbol := "TATAMOTORS"
	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, data.ISTLocation)

	createEngineWithCandles := func(troughLow float64, minRetracePct float64) *EMAS5BreakoutEngine {
		engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
		engine.SetMinPDHPDLRetracePct(minRetracePct)
		// PDL = 100.0
		engine.SetPreviousDayLevels(symbol, 110.0, 100.0, 105.0)

		// 1. Day starting low candle at 09:15 reaching troughLow before peak
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime,
			Open:   100.2,
			High:   100.4,
			Low:    troughLow,
			Close:  100.1,
			Volume: 1000,
		})

		// 2. Feed baseline rally candles with highestHigh = 101.5
		highestHigh := 101.5
		for i := 1; i <= 15; i++ {
			engine.ProcessCandle(symbol, data.Candle{
				Time:   baseTime.Add(time.Duration(i) * time.Minute),
				Open:   highestHigh - 0.1,
				High:   highestHigh,
				Low:    highestHigh - 0.2,
				Close:  highestHigh - 0.05,
				Volume: 1000,
			})
		}

		// 3. Feed 4 decay sequence candles curving downward
		decay := []struct{ o, h, l, c float64 }{
			{highestHigh - 0.05, highestHigh - 0.05, highestHigh - 0.2, highestHigh - 0.15},
			{highestHigh - 0.15, highestHigh - 0.1, highestHigh - 0.3, highestHigh - 0.25},
			{highestHigh - 0.25, highestHigh - 0.2, highestHigh - 0.4, highestHigh - 0.35},
			{highestHigh - 0.35, highestHigh - 0.3, highestHigh - 0.5, highestHigh - 0.45},
		}
		for i, p := range decay {
			engine.ProcessCandle(symbol, data.Candle{
				Time:   baseTime.Add(time.Duration(16+i) * time.Minute),
				Open:   p.o,
				High:   p.h,
				Low:    p.l,
				Close:  p.c,
				Volume: 1000,
			})
		}

		// 4. Candidate Master candle: Red, closes below PDL (100.0)
		masterOpen := highestHigh - 0.45
		engine.ProcessCandle(symbol, data.Candle{
			Time:   baseTime.Add(20 * time.Minute),
			Open:   masterOpen,
			High:   masterOpen + 0.1,
			Low:    99.3,
			Close:  99.4, // Closes below PDL 100.0, Red body
			Volume: 2000,
		})

		return engine
	}

	t.Run("Insufficient Retracement Rejected", func(t *testing.T) {
		// troughLow = 99.8 => Extension below PDL (100.0) = (100 - 99.8)/100 = 0.20% < 0.50%
		engine := createEngineWithCandles(99.8, 0.50)
		if engine.masterCandles[symbol] != nil {
			t.Fatalf("Expected Master Candle to be REJECTED due to insufficient extension below PDL (0.20%% < 0.50%%)")
		}
	})

	t.Run("Sufficient Retracement Accepted", func(t *testing.T) {
		// troughLow = 99.2 => Extension below PDL (100.0) = (100 - 99.2)/100 = 0.80% >= 0.50%
		engine := createEngineWithCandles(99.2, 0.50)
		if engine.masterCandles[symbol] == nil {
			t.Fatalf("Expected Master Candle to be ESTABLISHED with sufficient extension below PDL (0.80%% >= 0.50%%)")
		}
		if engine.masterDirections[symbol] != "SELL" {
			t.Fatalf("Expected Master Direction SELL, got %s", engine.masterDirections[symbol])
		}
	})

	t.Run("Disabled Filter Allows Shallow Retracement", func(t *testing.T) {
		// troughLow = 99.8 => Extension 0.20%, but minPDHPDLRetracePct = 0.0 (disabled)
		engine := createEngineWithCandles(99.8, 0.0)
		if engine.masterCandles[symbol] == nil {
			t.Fatalf("Expected Master Candle to form when filter is disabled (0.0%%)")
		}
	})
}

// TestEMAS5BreakoutEngine_EdgeCases_Retracement_And_Race validates all boundary and concurrency edge scenarios.
func TestEMAS5BreakoutEngine_EdgeCases_Retracement_And_Race(t *testing.T) {
	logger := zap.NewNop()
	symbol := "RELIANCE"
	baseTime := time.Date(2026, 8, 29, 9, 15, 0, 0, data.ISTLocation)

	t.Run("ZeroOrNegativePDH_BypassesFilterSafely", func(t *testing.T) {
		engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
		engine.SetMinPDHPDLRetracePct(0.50)
		// PDH = 0.0 (Missing level)
		engine.SetPreviousDayLevels(symbol, 0.0, 0.0, 0.0)

		// Feed candles where lowestLow = 148.5, peakHigh = 149.0
		engine.ProcessCandle(symbol, data.Candle{
			Time: baseTime, Open: 149.0, High: 149.0, Low: 148.5, Close: 148.8, Volume: 1000,
		})
		for i := 1; i <= 15; i++ {
			engine.ProcessCandle(symbol, data.Candle{
				Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 148.8, High: 148.9, Low: 148.5, Close: 148.6, Volume: 1000,
			})
		}
		for i := 0; i < 4; i++ {
			engine.ProcessCandle(symbol, data.Candle{
				Time: baseTime.Add(time.Duration(16+i) * time.Minute), Open: 148.6 + float64(i)*0.2, High: 148.8 + float64(i)*0.2, Low: 148.6, Close: 148.8 + float64(i)*0.2, Volume: 1000,
			})
		}
		// Master candle (touches EMA10 around 149.1-149.2)
		engine.ProcessCandle(symbol, data.Candle{
			Time: baseTime.Add(20 * time.Minute), Open: 149.2, High: 150.2, Low: 149.1, Close: 150.0, Volume: 2000,
		})
		if engine.masterCandles[symbol] == nil {
			t.Fatalf("Expected Master Candle to form safely when PDH is 0 (bypasses retracement check)")
		}
	})

	t.Run("ZeroOrNegativePDL_BypassesFilterSafely", func(t *testing.T) {
		engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
		engine.SetMinPDHPDLRetracePct(0.50)
		// PDL = 0.0
		engine.SetPreviousDayLevels(symbol, 0.0, 0.0, 0.0)

		engine.ProcessCandle(symbol, data.Candle{
			Time: baseTime, Open: 100.0, High: 100.5, Low: 99.8, Close: 100.2, Volume: 1000,
		})
		for i := 1; i <= 15; i++ {
			engine.ProcessCandle(symbol, data.Candle{
				Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 100.4, High: 100.5, Low: 100.3, Close: 100.45, Volume: 1000,
			})
		}
		for i := 0; i < 4; i++ {
			engine.ProcessCandle(symbol, data.Candle{
				Time: baseTime.Add(time.Duration(16+i) * time.Minute), Open: 100.4 - float64(i)*0.2, High: 100.4, Low: 100.2 - float64(i)*0.2, Close: 100.2 - float64(i)*0.2, Volume: 1000,
			})
		}
		// Red Master candle (touches EMA10 around 99.8-99.9)
		engine.ProcessCandle(symbol, data.Candle{
			Time: baseTime.Add(20 * time.Minute), Open: 99.7, High: 99.9, Low: 98.8, Close: 99.0, Volume: 2000,
		})
		if engine.masterCandles[symbol] == nil {
			t.Fatalf("Expected SELL Master Candle to form safely when PDL is 0 (bypasses retracement check)")
		}
	})

	t.Run("PeakHighExactlyEqualsPDH_RejectedWhenThresholdPositive", func(t *testing.T) {
		engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
		engine.SetMinPDHPDLRetracePct(0.50)
		// PDH = 150.0. PeakHigh = 150.0 (0.00% extension)
		engine.SetPreviousDayLevels(symbol, 150.0, 140.0, 148.0)

		engine.ProcessCandle(symbol, data.Candle{
			Time: baseTime, Open: 149.8, High: 150.0, Low: 148.5, Close: 149.0, Volume: 1000,
		})
		for i := 1; i <= 15; i++ {
			engine.ProcessCandle(symbol, data.Candle{
				Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 148.8, High: 149.0, Low: 148.5, Close: 148.6, Volume: 1000,
			})
		}
		for i := 0; i < 4; i++ {
			engine.ProcessCandle(symbol, data.Candle{
				Time: baseTime.Add(time.Duration(16+i) * time.Minute), Open: 148.6 + float64(i)*0.2, High: 148.8 + float64(i)*0.2, Low: 148.6, Close: 148.8 + float64(i)*0.2, Volume: 1000,
			})
		}
		engine.ProcessCandle(symbol, data.Candle{
			Time: baseTime.Add(20 * time.Minute), Open: 149.4, High: 150.8, Low: 149.3, Close: 150.6, Volume: 2000,
		})
		if engine.masterCandles[symbol] != nil {
			t.Fatalf("Expected Master Candle to be REJECTED when peak high exactly equals PDH (0.00%% < 0.50%% threshold)")
		}
	})

	t.Run("PeakHighOccursBetweenCandle0AndTrough_CorrectlyDetected", func(t *testing.T) {
		engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
		engine.SetMinPDHPDLRetracePct(0.50)
		engine.SetPreviousDayLevels(symbol, 150.0, 140.0, 148.0)

		// Candle 0: 09:15 - flat open
		engine.ProcessCandle(symbol, data.Candle{
			Time: baseTime, Open: 150.0, High: 150.2, Low: 149.8, Close: 150.1, Volume: 1000,
		})
		// Candle 1: 09:16 - Rallies above PDH to 151.2 (+0.80% above PDH 150.0)
		engine.ProcessCandle(symbol, data.Candle{
			Time: baseTime.Add(1 * time.Minute), Open: 150.1, High: 151.2, Low: 150.0, Close: 151.0, Volume: 1500,
		})
		// Candles 2-15: Retraces down to trough low 148.5 at candle 10
		for i := 2; i <= 15; i++ {
			engine.ProcessCandle(symbol, data.Candle{
				Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 149.0, High: 149.5, Low: 148.5, Close: 148.8, Volume: 1000,
			})
		}
		// 4 rally curve candles
		for i := 0; i < 4; i++ {
			engine.ProcessCandle(symbol, data.Candle{
				Time: baseTime.Add(time.Duration(16+i) * time.Minute), Open: 148.8 + float64(i)*0.2, High: 149.0 + float64(i)*0.2, Low: 148.8, Close: 149.0 + float64(i)*0.2, Volume: 1000,
			})
		}
		// Master Candle closing above EMAs and PDH
		engine.ProcessCandle(symbol, data.Candle{
			Time: baseTime.Add(20 * time.Minute), Open: 149.6, High: 150.8, Low: 149.5, Close: 150.6, Volume: 2000,
		})
		if engine.masterCandles[symbol] == nil {
			t.Fatalf("Expected Master Candle to be ESTABLISHED because candle 1 reached +0.80%% above PDH before the retrace")
		}
		if engine.masterDirections[symbol] != "BUY" {
			t.Fatalf("Expected BUY direction, got %s", engine.masterDirections[symbol])
		}
	})

	t.Run("CrossDayWarmupCandleHighIgnored", func(t *testing.T) {
		engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
		engine.SetMinPDHPDLRetracePct(0.50)
		engine.SetPreviousDayLevels(symbol, 150.0, 140.0, 148.0)

		// Yesterday's historical warm-up candle reached 155.0 (+3.33% above PDH)
		yesterdayTime := baseTime.Add(-24 * time.Hour)
		engine.WarmUpCandles(symbol, []data.Candle{
			{Time: yesterdayTime, Open: 150.0, High: 155.0, Low: 149.0, Close: 154.0, Volume: 5000},
		})

		// Today's candles never exceed 150.2 (+0.13% < 0.50%)
		engine.ProcessCandle(symbol, data.Candle{
			Time: baseTime, Open: 149.8, High: 150.2, Low: 148.5, Close: 149.0, Volume: 1000,
		})
		for i := 1; i <= 15; i++ {
			engine.ProcessCandle(symbol, data.Candle{
				Time: baseTime.Add(time.Duration(i) * time.Minute), Open: 148.8, High: 149.0, Low: 148.5, Close: 148.6, Volume: 1000,
			})
		}
		for i := 0; i < 4; i++ {
			engine.ProcessCandle(symbol, data.Candle{
				Time: baseTime.Add(time.Duration(16+i) * time.Minute), Open: 148.6 + float64(i)*0.2, High: 148.8 + float64(i)*0.2, Low: 148.6, Close: 148.8 + float64(i)*0.2, Volume: 1000,
			})
		}
		engine.ProcessCandle(symbol, data.Candle{
			Time: baseTime.Add(20 * time.Minute), Open: 149.4, High: 150.8, Low: 149.3, Close: 150.6, Volume: 2000,
		})
		// Must be REJECTED because yesterday's high of 155.0 must not leak into today's pre-retrace check!
		if engine.masterCandles[symbol] != nil {
			t.Fatalf("Expected Master Candle to be REJECTED because yesterday's warm-up candle high must not satisfy today's PDH retracement")
		}
	})

	t.Run("ConcurrentRaceConditions", func(t *testing.T) {
		engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
		engine.SetMinPDHPDLRetracePct(0.50)
		engine.SetPreviousDayLevels(symbol, 150.0, 140.0, 148.0)

		var wg sync.WaitGroup
		concurrency := 20

		for g := 0; g < concurrency; g++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sym := fmt.Sprintf("SYM_%d", id%3)
				engine.SetPreviousDayLevels(sym, 150.0+float64(id), 140.0, 145.0)
				engine.SetMinPDHPDLRetracePct(0.50)
				_ = engine.MinPDHPDLRetracePct()

				for m := 0; m < 25; m++ {
					c := data.Candle{
						Time:   baseTime.Add(time.Duration(m) * time.Minute),
						Open:   149.0 + float64(m)*0.1,
						High:   151.5 + float64(m)*0.1,
						Low:    148.5,
						Close:  150.5 + float64(m)*0.1,
						Volume: 1000,
					}
					engine.ProcessCandle(sym, c)
					_ = engine.CheckBreakout(sym, 152.0, "BUY")
				}
			}(g)
		}

		wg.Wait()
	})

	t.Run("LowestLowAtOpen_DetectsPeakFromSubsequentMorningRally", func(t *testing.T) {
		logger, _ := zap.NewDevelopment()
		engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.4, 2.0, 1, 1.0)
		engine.SetMinPDHPDLRetracePct(0.50)

		// Case A: SUNPHARMA scenario: Day opens at lowest low 1832.2 (09:15), rallies to 1883.0 at 09:40 (+0.31% above PDH 1877.2), then retraces.
		// Threshold is 0.50%. Since +0.31% < 0.50%, it should be rejected.
		pdh := 1877.2
		candles := make([]data.Candle, 10)
		baseTime := time.Date(2026, 9, 21, 9, 15, 0, 0, data.ISTLocation)

		// 09:15 (Index 0): Open = Lowest Low of day
		candles[0] = data.Candle{Time: baseTime, Open: 1832.2, High: 1864.3, Low: 1832.2, Close: 1862.5}
		candles[1] = data.Candle{Time: baseTime.Add(5 * time.Minute), Open: 1862.5, High: 1870.0, Low: 1858.0, Close: 1868.5}
		candles[2] = data.Candle{Time: baseTime.Add(10 * time.Minute), Open: 1868.5, High: 1872.0, Low: 1865.7, Close: 1869.5}
		candles[3] = data.Candle{Time: baseTime.Add(15 * time.Minute), Open: 1869.5, High: 1874.8, Low: 1869.5, Close: 1874.8}
		candles[4] = data.Candle{Time: baseTime.Add(20 * time.Minute), Open: 1874.7, High: 1874.9, Low: 1872.1, Close: 1873.7}
		candles[5] = data.Candle{Time: baseTime.Add(25 * time.Minute), Open: 1874.3, High: 1883.0, Low: 1873.7, Close: 1880.4} // Peak: 1883.0 (+0.31% above PDH)
		candles[6] = data.Candle{Time: baseTime.Add(30 * time.Minute), Open: 1880.4, High: 1882.9, Low: 1879.0, Close: 1882.7}
		candles[7] = data.Candle{Time: baseTime.Add(35 * time.Minute), Open: 1881.9, High: 1882.7, Low: 1877.5, Close: 1878.0}
		candles[8] = data.Candle{Time: baseTime.Add(40 * time.Minute), Open: 1878.5, High: 1879.4, Low: 1875.1, Close: 1875.6} // Trough: 1875.1
		candles[9] = data.Candle{Time: baseTime.Add(45 * time.Minute), Open: 1875.6, High: 1878.0, Low: 1873.6, Close: 1877.6} // Candidate (10:00)

		isValid, lowestLow, _, reboundPct, pdhRetracePct := engine.validateBuyUShape(candles, 9, pdh)
		if isValid {
			t.Fatalf("Expected SUNPHARMA setup to be rejected because peak +0.31%% is below 0.50%% threshold")
		}
		if lowestLow != 1832.2 {
			t.Errorf("Expected lowestLow to be 1832.2, got %.2f", lowestLow)
		}
		if math.Abs(pdhRetracePct-0.30896) > 0.01 {
			t.Errorf("Expected pdhRetracePct to be ~+0.31%% (from 1883.0 peak), got %.4f%%", pdhRetracePct)
		}
		if reboundPct <= 0 {
			t.Errorf("Expected positive reboundPct, got %.2f%%", reboundPct)
		}

		// Case B: If Peak reached 1888.0 (+0.575% >= 0.50% threshold)
		candles[5].High = 1888.0
		isValidB, _, _, _, pdhRetracePctB := engine.validateBuyUShape(candles, 9, pdh)
		if !isValidB {
			t.Fatalf("Expected setup with +0.575%% peak to be accepted under PDH retracement check")
		}
		if math.Abs(pdhRetracePctB-0.5753) > 0.01 {
			t.Errorf("Expected pdhRetracePct to be ~+0.58%%, got %.4f%%", pdhRetracePctB)
		}
	})
}
