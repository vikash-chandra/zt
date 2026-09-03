package strategy

import (
	"sync"
	"testing"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

func TestVandeBharatTrapEngine_BuySetup(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatTrapEngine(logger, 3.0, 1.8, 0.5, 1.0, 40.0)

	symbol := "TCS"
	pdh := 3500.0
	pdl := 3400.0
	pdClose := 3450.0
	engine.SetPreviousDayLevels(symbol, pdh, pdl, pdClose)

	today := time.Now().In(data.ISTLocation)

	// 1. Day 1st candle (09:15 AM IST) - RED candle closing above PDH (Fake Master)
	// Open=3520, Close=3508 (Red, Close > PDH), High=3525, Low=3505 (Range = 20 pts, ~0.57% <= 3.0%)
	c1Time := time.Date(today.Year(), today.Month(), today.Day(), 9, 15, 0, 0, data.ISTLocation)
	c1 := &data.Candle{
		Time:   c1Time,
		Open:   3520.0,
		High:   3525.0,
		Low:    3505.0,
		Close:  3508.0,
		Volume: 10000,
	}
	engine.OnCandleClose(c1, symbol)

	if engine.fakeMasterCandles[symbol] == nil {
		t.Fatalf("Expected Fake Master Candle to be established for BUY, got nil")
	}

	// 2. Candle 2: Inside Fake Master range, does not break Fake Master High
	c2Time := time.Date(today.Year(), today.Month(), today.Day(), 9, 16, 0, 0, data.ISTLocation)
	c2 := &data.Candle{
		Time:   c2Time,
		Open:   3508.0,
		High:   3520.0,
		Low:    3506.0,
		Close:  3515.0,
		Volume: 5000,
	}
	engine.OnCandleClose(c2, symbol)
	if engine.masterCandles[symbol] != nil {
		t.Fatalf("Expected masterCandles to be nil before Fake Master High breach, got %+v", engine.masterCandles[symbol])
	}

	// 3. Candle 3: Breaks Fake Master High (High 3525) -> Forms Vande Bharat Master Candle!
	// Open=3515, High=3535, Low=3510, Close=3532 (Range = 25 pts, ~0.70% <= 1.8%)
	c3Time := time.Date(today.Year(), today.Month(), today.Day(), 9, 17, 0, 0, data.ISTLocation)
	c3 := &data.Candle{
		Time:   c3Time,
		Open:   3515.0,
		High:   3535.0,
		Low:    3510.0,
		Close:  3532.0,
		Volume: 15000,
	}
	engine.OnCandleClose(c3, symbol)
	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected Master Candle to be established upon Fake Master High break, got nil")
	}

	// 4. Candle 4: 2nd Candle following Master (SL Anchor)
	// Range: High=3540, Low=3515 (Range = 25 pts, 25/3530 = ~0.70% in [0.5%, 1.0%])
	c4Time := time.Date(today.Year(), today.Month(), today.Day(), 9, 18, 0, 0, data.ISTLocation)
	c4 := &data.Candle{
		Time:   c4Time,
		Open:   3532.0,
		High:   3540.0,
		Low:    3515.0,
		Close:  3525.0,
		Volume: 8000,
	}
	engine.OnCandleClose(c4, symbol)
	if engine.secondCandles[symbol] == nil {
		t.Fatalf("Expected 2nd Candle (SL Anchor) to be established, got nil")
	}
	if engine.breakoutTriggerLevel[symbol] != 3540.0 {
		t.Fatalf("Expected breakout trigger level to be Candle 2 High (3540.0), got: %.2f", engine.breakoutTriggerLevel[symbol])
	}
	if engine.slAnchorPrices[symbol] != 3515.0 {
		t.Fatalf("Expected SL anchor price to be Candle 2 Low (3515.0), got: %.2f", engine.slAnchorPrices[symbol])
	}

	// 5. Live Tick check: LTP breaks Confirmation High (3540.0) -> 3546.0
	// Move from PDH: (3546 - 3500) / 3500 = 1.31% <= 1.8%
	sig := engine.CheckBreakout(symbol, 3546.0, "")
	if sig == nil {
		t.Fatalf("Expected BUY signal on breakout above 3540.0, got nil")
	}
	if sig.Action != "BUY" {
		t.Fatalf("Expected BUY signal action, got %s", sig.Action)
	}

	// 6. Verify SetupCandle returns 2nd candle Low (3515.0) as SL anchor
	setup := engine.GetSetupCandle(symbol)
	if setup == nil {
		t.Fatalf("Expected setup candle, got nil")
	}
	if setup.Low != 3515.0 {
		t.Fatalf("Expected setup candle Low to be 3515.0, got %f", setup.Low)
	}
	if setup.High != 3540.0 {
		t.Fatalf("Expected setup candle High to be 3540.0, got %f", setup.High)
	}
}

func TestVandeBharatTrapEngine_SellSetup(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatTrapEngine(logger, 3.0, 1.8, 0.5, 1.0, 40.0)

	symbol := "INFY"
	pdh := 1550.0
	pdl := 1475.0
	pdClose := 1500.0
	engine.SetPreviousDayLevels(symbol, pdh, pdl, pdClose)

	today := time.Now().In(data.ISTLocation)

	// 1. Day 1st candle (09:15 AM IST) - GREEN candle closing below PDL (Fake Master)
	// Open=1465, Close=1472 (Green, Close < PDL), High=1474, Low=1462 (Range = 12 pts, ~0.81% <= 3.0%)
	c1Time := time.Date(today.Year(), today.Month(), today.Day(), 9, 15, 0, 0, data.ISTLocation)
	c1 := &data.Candle{
		Time:   c1Time,
		Open:   1465.0,
		High:   1474.0,
		Low:    1462.0,
		Close:  1472.0,
		Volume: 10000,
	}
	engine.OnCandleClose(c1, symbol)

	if engine.fakeMasterCandles[symbol] == nil {
		t.Fatalf("Expected Fake Master Candle to be established for SELL, got nil")
	}

	// 2. Candle 2: Breaks Fake Master Low (Low 1462) -> Forms Vande Bharat Master Candle!
	c2Time := time.Date(today.Year(), today.Month(), today.Day(), 9, 16, 0, 0, data.ISTLocation)
	c2 := &data.Candle{
		Time:   c2Time,
		Open:   1472.0,
		High:   1473.0,
		Low:    1458.0,
		Close:  1460.0, // Range = 15 pts, 15/1460 = ~1.02% <= 1.8%
		Volume: 15000,
	}
	engine.OnCandleClose(c2, symbol)
	if engine.masterCandles[symbol] == nil {
		t.Fatalf("Expected Master Candle to be established upon Fake Master Low break, got nil")
	}

	// 3. Candle 3: 2nd Candle following Master (SL Anchor)
	// Breaks Master Low (1458) -> Low 1456, High 1468 (Range = 12 pts, 12/1460 = ~0.82% in [0.5%, 1.0%])
	c3Time := time.Date(today.Year(), today.Month(), today.Day(), 9, 17, 0, 0, data.ISTLocation)
	c3 := &data.Candle{
		Time:   c3Time,
		Open:   1460.0,
		High:   1468.0,
		Low:    1456.0,
		Close:  1462.0,
		Volume: 8000,
	}
	engine.OnCandleClose(c3, symbol)
	if engine.secondCandles[symbol] == nil {
		t.Fatalf("Expected 2nd Candle (SL Anchor) to be established, got nil")
	}
	if engine.breakoutTriggerLevel[symbol] != 1456.0 {
		t.Fatalf("Expected breakdown trigger level to be Candle 2 Low (1456.0), got: %.2f", engine.breakoutTriggerLevel[symbol])
	}
	if engine.slAnchorPrices[symbol] != 1468.0 {
		t.Fatalf("Expected SL anchor price to be Candle 2 High (1468.0), got: %.2f", engine.slAnchorPrices[symbol])
	}

	// 4. Live Tick check: LTP breaks Confirmation Low (1456.0) -> 1451.0
	// Move from PDL: (1475 - 1451) / 1475 = 1.62% <= 1.8%
	sig := engine.CheckBreakout(symbol, 1451.0, "")
	if sig == nil {
		t.Fatalf("Expected SELL signal on breakdown below 1456.0, got nil")
	}
	if sig.Action != "SELL" {
		t.Fatalf("Expected SELL signal action, got %s", sig.Action)
	}

	// 5. Verify SetupCandle returns 2nd candle High (1468.0) as SL anchor
	setup := engine.GetSetupCandle(symbol)
	if setup == nil {
		t.Fatalf("Expected setup candle, got nil")
	}
	if setup.High != 1468.0 {
		t.Fatalf("Expected setup candle High to be 1468.0, got %f", setup.High)
	}
	if setup.Low != 1456.0 {
		t.Fatalf("Expected setup candle Low to be 1456.0, got %f", setup.Low)
	}
}

func TestVandeBharatTrapEngine_InvalidColor(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatTrapEngine(logger, 3.0, 1.8, 0.5, 1.0, 40.0)

	symbol := "RELIANCE"
	pdh := 2500.0
	pdl := 2400.0
	engine.SetPreviousDayLevels(symbol, pdh, pdl, 2450.0)

	today := time.Now().In(data.ISTLocation)

	// 1st candle is GREEN above PDH (not a Fake Master trap, this is standard VB)
	c1Time := time.Date(today.Year(), today.Month(), today.Day(), 9, 15, 0, 0, data.ISTLocation)
	c1 := &data.Candle{
		Time:   c1Time,
		Open:   2510.0,
		High:   2540.0,
		Low:    2505.0,
		Close:  2530.0, // Green above PDH -> Rejected by Trap strategy
		Volume: 10000,
	}
	engine.OnCandleClose(c1, symbol)

	if engine.fakeMasterCandles[symbol] != nil {
		t.Fatalf("Expected Fake Master Candle to be nil for green body above PDH, got %+v", engine.fakeMasterCandles[symbol])
	}
}

func TestVandeBharatTrapEngine_Rule3_WaitAndBreakoutCandleExpiration(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatTrapEngine(logger, 3.0, 1.8, 0.5, 1.0, 40.0)

	symbol := "TCS"
	pdh := 3500.0
	pdl := 3400.0
	pdClose := 3450.0
	engine.SetPreviousDayLevels(symbol, pdh, pdl, pdClose)

	today := time.Now().In(data.ISTLocation)

	// 1. Fake Master (09:15 AM IST)
	c1Time := time.Date(today.Year(), today.Month(), today.Day(), 9, 15, 0, 0, data.ISTLocation)
	c1 := &data.Candle{
		Time:   c1Time,
		Open:   3520.0,
		High:   3525.0,
		Low:    3505.0,
		Close:  3508.0,
		Volume: 10000,
	}
	engine.OnCandleClose(c1, symbol)

	// 2. Candle 2: Breaks Fake Master High (3525) -> Genuine Master Formed!
	c2Time := time.Date(today.Year(), today.Month(), today.Day(), 9, 16, 0, 0, data.ISTLocation)
	c2 := &data.Candle{
		Time:   c2Time,
		Open:   3515.0,
		High:   3535.0,
		Low:    3510.0,
		Close:  3532.0,
		Volume: 15000,
	}
	engine.OnCandleClose(c2, symbol)

	// 3. Candle 3: 2nd Candle (Inside Master Range, High 3535 <= 3535, Low 3515, Range 0.57% in [0.5%, 1.0%]) -> Trigger @ Master High (3535.0), SL @ 3515.0
	c3Time := time.Date(today.Year(), today.Month(), today.Day(), 9, 17, 0, 0, data.ISTLocation)
	c3 := &data.Candle{
		Time:   c3Time,
		Open:   3530.0,
		High:   3535.0,
		Low:    3515.0,
		Close:  3525.0,
		Volume: 8000,
	}
	engine.OnCandleClose(c3, symbol)

	// 4. Candle 4: Consolidates inside range (High 3532 <= 3535) -> Waiting Phase
	c4Time := time.Date(today.Year(), today.Month(), today.Day(), 9, 18, 0, 0, data.ISTLocation)
	c4 := &data.Candle{
		Time:   c4Time,
		Open:   3525.0,
		High:   3532.0,
		Low:    3520.0,
		Close:  3528.0,
		Volume: 5000,
	}
	engine.OnCandleClose(c4, symbol)

	// Verify setup is still waiting
	engine.mu.RLock()
	masterStillAlive := engine.masterCandles[symbol]
	engine.mu.RUnlock()
	if masterStillAlive == nil {
		t.Fatal("Expected setup to remain active and waiting during consolidation")
	}

	// 5. Candle 5: Breakout Candle (High touches 3545 > 3535) but trade is NOT executed in Candle 5
	c5Time := time.Date(today.Year(), today.Month(), today.Day(), 9, 19, 0, 0, data.ISTLocation)
	c5 := &data.Candle{
		Time:   c5Time,
		Open:   3528.0,
		High:   3545.0,
		Low:    3526.0,
		Close:  3540.0,
		Volume: 12000,
	}
	engine.OnCandleClose(c5, symbol)

	// Verify setup is cancelled immediately at close of Breakout Candle
	engine.mu.RLock()
	masterAfterBreakout := engine.masterCandles[symbol]
	engine.mu.RUnlock()
	if masterAfterBreakout != nil {
		t.Fatal("Expected setup to be CANCELLED after breakout candle closed without trade execution")
	}

	// Late entry attempt on Candle 6 MUST return nil
	sigLate := engine.CheckBreakout(symbol, 3548.0, "")
	if sigLate != nil {
		t.Fatalf("Expected NO signal for late entry on subsequent candle, got: %+v", sigLate)
	}
}

func TestVandeBharatTrapEngineConcurrency(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatTrapEngine(logger, 3.0, 1.8, 0.5, 1.0, 40.0)

	symbol := "TCS"
	engine.SetPreviousDayLevels(symbol, 3500.0, 3400.0, 3450.0)

	now := time.Now().In(data.ISTLocation)
	var wg sync.WaitGroup
	workers := 40

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cTime := now.Add(time.Duration(id) * time.Minute)
			c := &data.Candle{
				Time:   cTime,
				Open:   3520.0 + float64(id%5),
				High:   3535.0 + float64(id%5),
				Low:    3505.0 - float64(id%3),
				Close:  3515.0 + float64(id%4),
				Volume: 10000,
			}
			engine.OnCandleClose(c, symbol)
			_ = engine.CheckBreakout(symbol, 3530.0+float64(id), "BUY_ONLY")
			_ = engine.GetSetupCandle(symbol)
			engine.UpdateRules(3.0, 1.8, 0.5, 1.0, 40.0)
		}(i)
	}

	wg.Wait()
}
