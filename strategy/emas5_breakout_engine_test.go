package strategy

import (
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
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0)
	symbol := "INFY"
	engine.SetPreviousDayLevels(symbol, 1550.0, 1520.0, 1530.0)

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
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0) // maxInsideCandles = 1
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

func TestEMAS5BreakoutEngine_ConfirmationRangeOverflow(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEMAS5BreakoutEngine(logger, 2, 5, 0.5, 2.0, 1, 1.0) // confirmMaxPct = 1.0%
	symbol := "AXISBANK"

	masterCandle := data.Candle{High: 1200.0, Low: 1190.0, Close: 1198.0, Time: time.Now().Add(-time.Minute)}
	engine.rollingCandles[symbol] = []data.Candle{masterCandle}
	engine.masterCandles[symbol] = &masterCandle
	engine.masterDirections[symbol] = "BUY"
	engine.masterCandleIndices[symbol] = 0

	// Confirmation candle breaks Master High (1200.0) -> High = 1220.0, Low = 1195.0, Close = 1215.0
	// Range: (1220 - 1195) / 1215 = 2.05% > 1.0% max -> should invalidate setup!
	engine.ProcessCandle(symbol, data.Candle{
		Time:   time.Now(),
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
	symbol := "TCS"
	engine.SetPreviousDayLevels(symbol, 2320.0, 2290.0, 2310.0)

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
