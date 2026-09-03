package strategy

import (
	"sync"
	"testing"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// Test Rule 1: Candle 2 breaks Master High -> Candle 2 is Confirmation Candle, Trigger @ Candle 2 High in 3rd Candle
func TestVandeBharatEngine_Rule1_Candle2BreaksMasterHigh(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.05, 1.0, 60.0, 0.0)
	symbol := "SBIN"

	// PDH: 100.0, PDL: 90.0, Yesterday's Close: 99.0
	engine.SetPreviousDayLevels(symbol, 100.0, 90.0, 99.0)
	baseTime := time.Date(2026, 9, 3, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1 (09:15 AM): Master Candle (Open: 101.0, High: 102.5, Low: 100.5, Close: 102.0 > PDH 100.0)
	candle1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   101.0,
		High:   102.5,
		Low:    100.5,
		Close:  102.0,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()
	if master == nil {
		t.Fatal("expected Candle 1 to be set as Master Candle")
	}

	// Candle 2 (09:20 AM): Breaks Master High (High 102.9 > 102.5), Low 102.1 (SL Anchor, Range 0.78%)
	candle2 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   102.0,
		High:   102.9,
		Low:    102.1,
		Close:  102.8,
		Volume: 1200,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	confirm := engine.confirmationCandles[symbol]
	triggerLvl := engine.breakoutTriggerLevel[symbol]
	slPrice := engine.slAnchorPrices[symbol]
	engine.mu.RUnlock()

	if confirm == nil {
		t.Fatal("expected Candle 2 to be set as Confirmation Candle (Rule 1)")
	}
	if triggerLvl != 102.9 {
		t.Fatalf("expected breakout trigger level to be Candle 2 High (102.9), got: %.2f", triggerLvl)
	}
	if slPrice != 102.1 {
		t.Fatalf("expected SL anchor price to be Candle 2 Low (102.1), got: %.2f", slPrice)
	}

	// 3rd Candle (09:25 AM Window): Live tick breaks Candle 2 High (102.9) -> Trigger BUY!
	sig := engine.CheckBreakout(symbol, 103.00, "BUY_ONLY")
	if sig == nil || sig.Action != "BUY" {
		t.Fatalf("expected BUY signal when breaking Confirmation High 102.9 in 3rd Candle, got: %+v", sig)
	}
	setup := engine.GetSetupCandle(symbol)
	if setup == nil || setup.Low != 102.1 {
		t.Fatalf("expected StopLoss anchor to be Candle 2 Low (102.1), got: %+v", setup)
	}
}

// Test Rule 2: Candle 2 does NOT break Master High -> Trigger @ Master High in 3rd Candle
func TestVandeBharatEngine_Rule2_Candle2InsideMasterRange(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.05, 1.0, 60.0, 0.0)
	symbol := "TATASTEEL"

	// PDH: 184.05, PDL: 181.45, Yesterday's Close: 183.25
	engine.SetPreviousDayLevels(symbol, 184.05, 181.45, 183.25)
	baseTime := time.Date(2026, 9, 3, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1 (09:15 AM): Master Candle (Open: 184.11, High: 184.89, Low: 184.10, Close: 184.65 > PDH 184.05)
	candle1 := &data.Candle{
		Token:  895745,
		Time:   baseTime,
		Open:   184.11,
		High:   184.89,
		Low:    184.10,
		Close:  184.65,
		Volume: 500000,
	}
	engine.OnCandleClose(candle1, symbol)

	// Candle 2 (09:20 AM): Inside Master range (High 184.75 <= Master High 184.89, Low 184.30)
	candle2 := &data.Candle{
		Token:  895745,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   184.65,
		High:   184.75,
		Low:    184.30,
		Close:  184.60,
		Volume: 300000,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	confirm := engine.confirmationCandles[symbol]
	triggerLvl := engine.breakoutTriggerLevel[symbol]
	slPrice := engine.slAnchorPrices[symbol]
	engine.mu.RUnlock()

	if confirm != nil {
		t.Fatal("expected confirmation candle to be nil when Candle 2 did NOT break Master High")
	}
	if triggerLvl != 184.89 {
		t.Fatalf("expected breakout trigger level to be Master High (184.89), got: %.2f", triggerLvl)
	}
	if slPrice != 184.30 {
		t.Fatalf("expected SL anchor price to be Candle 2 Low (184.30), got: %.2f", slPrice)
	}

	// 3rd Candle (09:25 AM Window): Live tick breaks Master High (184.89) -> Immediately Trigger BUY!
	sig := engine.CheckBreakout(symbol, 185.00, "BUY_ONLY")
	if sig == nil || sig.Action != "BUY" {
		t.Fatalf("expected immediate BUY signal when breaking Master High (184.89) in 3rd Candle, got: %+v", sig)
	}
	setup := engine.GetSetupCandle(symbol)
	if setup == nil || setup.Low != 184.30 {
		t.Fatalf("expected StopLoss anchor to be Candle 2 Low (184.30), got: %+v", setup)
	}
}

// Test Rule 3: Wait for Breakout Candle & Strict Breakout Candle Expiration Guard
func TestVandeBharatEngine_Rule3_WaitAndBreakoutCandleExpiration(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.05, 1.0, 60.0, 0.0)
	symbol := "MAHABANK"

	engine.SetPreviousDayLevels(symbol, 84.30, 81.30, 82.00)
	baseTime := time.Date(2026, 9, 3, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1 (09:15 AM): Master High = 84.85, Low = 82.82, Close = 84.61
	candle1 := &data.Candle{
		Token:  2912513,
		Time:   baseTime,
		Open:   83.15,
		High:   84.85,
		Low:    82.82,
		Close:  84.61,
		Volume: 2000000,
	}
	engine.OnCandleClose(candle1, symbol)

	// Candle 2 (09:20 AM): Breaks Master High -> Confirmation @ 85.39, SL @ 84.63
	candle2 := &data.Candle{
		Token:  2912513,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   84.64,
		High:   85.39,
		Low:    84.63,
		Close:  85.09,
		Volume: 2000000,
	}
	engine.OnCandleClose(candle2, symbol)

	// Candle 3 (09:25–09:30 AM): Consolidates inside range (High 85.15 <= 85.39, Low 84.54 >= 82.82)
	candle3 := &data.Candle{
		Token:  2912513,
		Time:   baseTime.Add(10 * time.Minute),
		Open:   85.15,
		High:   85.15,
		Low:    84.54,
		Close:  84.56,
		Volume: 1000000,
	}
	engine.OnCandleClose(candle3, symbol)

	// Rule 3 Waiting Phase: Setup MUST still be active and waiting (not expired yet)!
	engine.mu.RLock()
	masterWaiting := engine.masterCandles[symbol]
	triggerLvl := engine.breakoutTriggerLevel[symbol]
	engine.mu.RUnlock()
	if masterWaiting == nil {
		t.Fatal("expected setup to remain ACTIVE and waiting while price is inside range")
	}
	if triggerLvl != 85.39 {
		t.Fatalf("expected trigger level to remain 85.39, got: %.2f", triggerLvl)
	}

	// Candle 4 (09:30–09:35 AM): Still consolidates (High 85.00 <= 85.39)
	candle4 := &data.Candle{
		Token:  2912513,
		Time:   baseTime.Add(15 * time.Minute),
		Open:   84.62,
		High:   85.00,
		Low:    84.59,
		Close:  84.94,
		Volume: 500000,
	}
	engine.OnCandleClose(candle4, symbol)

	// Candle 5 (09:35–09:40 AM): This is the BREAKOUT CANDLE! (High touches 85.73 > 85.39)
	// If trade was NOT executed during Candle 5:
	candle5 := &data.Candle{
		Token:  2912513,
		Time:   baseTime.Add(20 * time.Minute),
		Open:   84.99,
		High:   85.73,
		Low:    84.95,
		Close:  85.22,
		Volume: 1600000,
	}
	engine.OnCandleClose(candle5, symbol)

	// Rule 3 Expiration: Because Candle 5 broke out but trade was NOT executed in Candle 5,
	// the setup MUST be cancelled/expired immediately at the close of Candle 5!
	engine.mu.RLock()
	masterAfterBreakout := engine.masterCandles[symbol]
	engine.mu.RUnlock()
	if masterAfterBreakout != nil {
		t.Fatal("expected setup to be CANCELLED after breakout candle closed without trade execution")
	}

	// Attempting late entry on Candle 7 (e.g. 09:49 AM @ 85.91) MUST return nil
	sigLate := engine.CheckBreakout(symbol, 85.91, "BUY_ONLY")
	if sigLate != nil {
		t.Fatalf("expected NO signal for late entry on subsequent candle, got: %+v", sigLate)
	}
}

// Test Rule 4: SELL Setup Vice Versa for Rule 1 & Rule 2
func TestVandeBharatEngine_Rule4_SELLSetups(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.05, 1.0, 60.0, 0.0)
	symbol := "INFY"

	// PDH: 1500.0, PDL: 1450.0, Yesterday's Close: 1460.0
	engine.SetPreviousDayLevels(symbol, 1500.0, 1450.0, 1460.0)
	baseTime := time.Date(2026, 9, 3, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1 (09:15 AM): Red Master Candle (Open: 1445.0, High: 1448.0, Low: 1430.0, Close: 1435.0 < PDL 1450.0)
	candle1 := &data.Candle{
		Token:  456,
		Time:   baseTime,
		Open:   1445.0,
		High:   1448.0,
		Low:    1430.0,
		Close:  1435.0,
		Volume: 5000,
	}
	engine.OnCandleClose(candle1, symbol)

	// Case A: Candle 2 breaks Master Low (Low 1425.0 < 1430.0), High 1438.0 (SL Anchor)
	candle2 := &data.Candle{
		Token:  456,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   1435.0,
		High:   1438.0,
		Low:    1425.0,
		Close:  1428.0,
		Volume: 6000,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	triggerLvl := engine.breakoutTriggerLevel[symbol]
	slPrice := engine.slAnchorPrices[symbol]
	engine.mu.RUnlock()

	if triggerLvl != 1425.0 {
		t.Fatalf("expected SELL trigger level to be Candle 2 Low (1425.0), got: %.2f", triggerLvl)
	}
	if slPrice != 1438.0 {
		t.Fatalf("expected SELL SL anchor to be Candle 2 High (1438.0), got: %.2f", slPrice)
	}

	// 3rd Candle: Live tick breaks below 1425.0 -> Trigger SELL!
	sig := engine.CheckBreakout(symbol, 1424.0, "SELL_ONLY")
	if sig == nil || sig.Action != "SELL" {
		t.Fatalf("expected SELL signal when breaking below Confirmation Low 1425.0, got: %+v", sig)
	}
	setup := engine.GetSetupCandle(symbol)
	if setup == nil || setup.High != 1438.0 {
		t.Fatalf("expected StopLoss anchor to be Candle 2 High (1438.0), got: %+v", setup)
	}
}

func TestVandeBharatEngineMasterOppositeBreachInvalidation(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.05, 1.0, 60.0, 0.0)
	symbol := "SBIN"

	engine.SetPreviousDayLevels(symbol, 100.0, 90.0, 99.0)
	baseTime := time.Date(2026, 9, 3, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1: Master Buy Candle (Open 101.0, High 103.0, Low 100.5, Close 102.5 > PDH 100.0)
	candle1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   101.0,
		High:   103.0,
		Low:    100.5,
		Close:  102.5,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	// Candle 2: Breaches Master Low (Low 100.0 < 100.5) -> MUST INVALIDATE SETUP!
	candle2 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   102.5,
		High:   102.6,
		Low:    100.0,
		Close:  100.2,
		Volume: 1200,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	if master != nil {
		t.Fatal("expected Master Candle setup to be invalidated when Master Low is breached on Candle 2")
	}
}

func TestVandeBharatEngineConcurrency(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.05, 1.0, 60.0, 0.0)
	symbols := []string{"RELIANCE", "TCS", "INFY", "HDFCBANK", "SBIN", "ICICIBANK", "AXISBANK"}

	for _, sym := range symbols {
		engine.SetPreviousDayLevels(sym, 1000.0, 950.0, 990.0)
	}

	var wg sync.WaitGroup
	numWorkers := 50
	iterations := 30

	baseTime := time.Date(2026, 9, 3, 9, 15, 0, 0, data.ISTLocation)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		workerID := i
		go func() {
			defer wg.Done()
			sym := symbols[workerID%len(symbols)]

			for j := 0; j < iterations; j++ {
				c := &data.Candle{
					Token:  int64(workerID),
					Time:   baseTime.Add(time.Duration(j*5) * time.Minute),
					Open:   1020.0 + float64(j%5),
					High:   1025.0 + float64(j%5),
					Low:    1015.0 - float64(j%5),
					Close:  1022.0 + float64(j%5),
					Volume: 5000,
				}
				engine.OnCandleClose(c, sym)
				_ = engine.CheckBreakout(sym, 1026.0+float64(j%10), "BUY_ONLY")
				_ = engine.GetSetupCandle(sym)
			}
		}()
	}

	wg.Wait()
}
