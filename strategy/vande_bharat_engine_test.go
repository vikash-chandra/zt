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

// Test Color Guard: Candle 2 breaks Master High but closes RED -> Rejected (Shooting Star)
func TestVandeBharatEngine_ColorGuard_ShootingStarRejected(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.05, 1.0, 60.0, 0.0)
	symbol := "TCS"

	engine.SetPreviousDayLevels(symbol, 3500.0, 3400.0, 3450.0)
	baseTime := time.Date(2026, 9, 3, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1 (09:15 AM): Green Master (Open: 3510, High: 3530, Low: 3505, Close: 3525 > PDH 3500)
	candle1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   3510.0,
		High:   3530.0,
		Low:    3505.0,
		Close:  3525.0,
		Volume: 10000,
	}
	engine.OnCandleClose(candle1, symbol)

	// Candle 2 (09:20 AM): Breaks Master High (High 3535 > 3530), but closes RED (Open 3532, Close 3515 < Open)
	candle2 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   3532.0,
		High:   3535.0,
		Low:    3512.0,
		Close:  3515.0, // RED body shooting star
		Volume: 12000,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	confirm := engine.confirmationCandles[symbol]
	triggerLvl := engine.breakoutTriggerLevel[symbol]
	engine.mu.RUnlock()

	if master != nil {
		t.Fatal("expected Master Candle setup to be invalidated by Shooting Star Color Guard rejection on Candle 2")
	}
	if confirm != nil {
		t.Fatal("expected Confirmation Candle to be nil after Color Guard rejection")
	}
	if triggerLvl != 0 {
		t.Fatalf("expected trigger level to be 0, got %.2f", triggerLvl)
	}
}

// Test Color Guard: Candle 2 breaks Master Low but closes GREEN -> Rejected (Hammer)
func TestVandeBharatEngine_ColorGuard_HammerRejected(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.05, 1.0, 60.0, 0.0)
	symbol := "INFY"

	engine.SetPreviousDayLevels(symbol, 1500.0, 1450.0, 1460.0)
	baseTime := time.Date(2026, 9, 3, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1 (09:15 AM): Red Master (Open: 1445, High: 1448, Low: 1430, Close: 1435 < PDL 1450)
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

	// Candle 2 (09:20 AM): Breaks Master Low (Low 1425 < 1430), but closes GREEN (Open 1428, Close 1440 > Open)
	candle2 := &data.Candle{
		Token:  456,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   1428.0,
		High:   1442.0,
		Low:    1425.0,
		Close:  1440.0, // GREEN body hammer
		Volume: 6000,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	confirm := engine.confirmationCandles[symbol]
	triggerLvl := engine.breakoutTriggerLevel[symbol]
	engine.mu.RUnlock()

	if master != nil {
		t.Fatal("expected Master Candle setup to be invalidated by Hammer Color Guard rejection on Candle 2")
	}
	if confirm != nil {
		t.Fatal("expected Confirmation Candle to be nil after Color Guard rejection")
	}
	if triggerLvl != 0 {
		t.Fatalf("expected trigger level to be 0, got %.2f", triggerLvl)
	}
}

// Test Zero Min Gap allows flat or slightly opposite open that closes beyond PDH/PDL
func TestVandeBharatEngine_ZeroMinGapAllowsFlatOpen(t *testing.T) {
	logger := zap.NewNop()
	// minGapPct = 0.0 (no gap required)
	engine := NewVandeBharatEngine(logger, 3.0, 0.05, 1.0, 60.0, 0.0)
	symbol := "NTPC"

	// PDH: 334.0, PDL: 330.40, Yesterday's Close: 331.60
	engine.SetPreviousDayLevels(symbol, 334.0, 330.40, 331.60)
	baseTime := time.Date(2026, 9, 15, 9, 15, 0, 0, data.ISTLocation)

	// Candle 1 (09:15 AM): Opens at 333.30 (higher than pdClose 331.60), but closes at 329.90 (BELOW PDL 330.40)
	// Range: (333.5 - 329.1) = 4.4 / 329.9 = 1.33% (< 3.00%)
	candle1 := &data.Candle{
		Token:  12345,
		Time:   baseTime,
		Open:   333.30,
		High:   333.50,
		Low:    329.10,
		Close:  329.90,
		Volume: 100000,
	}
	engine.OnCandleClose(candle1, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	pdl := engine.pdLows[symbol]
	engine.mu.RUnlock()

	if master == nil {
		t.Fatal("expected SELL Master Candle to be formed when minGapPct is 0 and candle closes below PDL, even if opened above pdClose")
	}
	if master.Close >= pdl {
		t.Fatalf("expected master close < pdl (SELL direction), got close=%.2f, pdl=%.2f", master.Close, pdl)
	}
}

// Test 1-Minute Timeframe Execution for VandeBharatEngine:
// 1m Candle 1 (09:15-09:16) establishes Master Candle.
// 1m Candle 2 (09:16-09:17) with realistic 1m range (0.15% < 0.50%) is accepted due to dynamic 1m SL adaptation.
// Live tick at 09:17:05 fires BUY breakout trade.
func TestVandeBharatEngine_1MinuteTimeframe_Execution(t *testing.T) {
	logger := zap.NewNop()
	// Configured with 5m default slMinPct = 0.50%
	engine := NewVandeBharatEngine(logger, 3.0, 0.50, 1.0, 60.0, 0.0)
	engine.SetCandleTimeFrame("1m")
	symbol := "COFORGE"

	// PDH: 1000.0, PDL: 950.0, Yesterday's Close: 990.0
	engine.SetPreviousDayLevels(symbol, 1000.0, 950.0, 990.0)
	baseTime := time.Date(2026, 9, 15, 9, 15, 0, 0, data.ISTLocation)

	// 1m Candle 1 (09:15 AM IST): Closes at 09:16:00, Close: 1010.0 > PDH 1000.0, Green
	candle1 := &data.Candle{
		Token:  999,
		Time:   baseTime,
		Open:   1002.0,
		High:   1012.0,
		Low:    1001.0,
		Close:  1010.0,
		Volume: 5000,
	}
	engine.OnCandleClose(candle1, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()
	if master == nil {
		t.Fatal("expected 1m Candle 1 to establish Master Candle")
	}

	// 1m Candle 2 (09:16 AM IST): Closes at 09:17:00. Breaks Master High (1014 > 1012).
	// Range: 1014 - 1012.5 = 1.5 -> (1.5 / 1013.5) * 100 = 0.148% (< 0.50% 5m default)
	candle2 := &data.Candle{
		Token:  999,
		Time:   baseTime.Add(1 * time.Minute),
		Open:   1010.0,
		High:   1014.0,
		Low:    1012.5,
		Close:  1013.5,
		Volume: 6000,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	confirm := engine.confirmationCandles[symbol]
	triggerLvl := engine.breakoutTriggerLevel[symbol]
	slPrice := engine.slAnchorPrices[symbol]
	engine.mu.RUnlock()

	if confirm == nil {
		t.Fatal("expected 1m Candle 2 to be accepted as Confirmation Candle with dynamic 1m minSL adaptation")
	}
	if triggerLvl != 1014.0 {
		t.Fatalf("expected breakout trigger level to be 1014.0, got: %.2f", triggerLvl)
	}
	if slPrice != 1012.5 {
		t.Fatalf("expected SL anchor price to be 1012.5, got: %.2f", slPrice)
	}

	// Live tick at 09:17:05 IST (Candle 3 Window): LTP breaks 1014.0 -> BUY Breakout Triggered!
	sig := engine.CheckBreakout(symbol, 1014.50, "BOTH")
	if sig == nil || sig.Action != "BUY" {
		t.Fatalf("expected BUY signal on 1m breakout, got: %+v", sig)
	}
}

// Test 5-Minute Timeframe Preserves Existing Behavior:
// With slMinPct = 0.50%, a 5m confirmation candle with range 0.15% MUST be disqualified,
// exactly as in the existing implementation.
func TestVandeBharatEngine_5MinuteTimeframe_PreservesDefaultSLMin(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.50, 1.0, 60.0, 0.0)
	engine.SetCandleTimeFrame("5m")
	symbol := "RELIANCE"

	engine.SetPreviousDayLevels(symbol, 2500.0, 2400.0, 2480.0)
	baseTime := time.Date(2026, 9, 15, 9, 15, 0, 0, data.ISTLocation)

	// 5m Candle 1 (09:15-09:20): Close > PDH
	candle1 := &data.Candle{
		Token:  888,
		Time:   baseTime,
		Open:   2510.0,
		High:   2525.0,
		Low:    2505.0,
		Close:  2520.0,
		Volume: 10000,
	}
	engine.OnCandleClose(candle1, symbol)

	engine.mu.RLock()
	master := engine.masterCandles[symbol]
	engine.mu.RUnlock()
	if master == nil {
		t.Fatal("expected 5m Candle 1 to establish Master Candle")
	}

	// 5m Candle 2 (09:20-09:25): Range is 0.15% (< 0.50% default)
	candle2 := &data.Candle{
		Token:  888,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   2520.0,
		High:   2523.0,
		Low:    2519.2,
		Close:  2522.0,
		Volume: 8000,
	}
	engine.OnCandleClose(candle2, symbol)

	engine.mu.RLock()
	masterAfter := engine.masterCandles[symbol]
	engine.mu.RUnlock()

	// In 5m mode, range 0.15% < 0.50% MUST invalidate the setup
	if masterAfter != nil {
		t.Fatal("expected 5m setup to be invalidated when Candle 2 range < 0.50% (preserved existing 5m rule)")
	}
}

func TestVandeBharatEngine_DeduplicationAndMinCandlesToIgnore(t *testing.T) {
	logger := zap.NewNop()
	engine := NewVandeBharatEngine(logger, 3.0, 0.05, 1.0, 60.0, 0.0)
	engine.MinCandlesToIgnore = 3
	symbol := "SBIN"
	engine.SetPreviousDayLevels(symbol, 500.0, 480.0, 498.0)

	baseTime := time.Date(2026, 9, 21, 9, 15, 0, 0, data.ISTLocation)

	// 1. Candle 1 (09:15)
	c1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   501.0,
		High:   505.0,
		Low:    501.0,
		Close:  504.0,
		Volume: 10000,
	}
	engine.OnCandleClose(c1, symbol)
	// Duplicate Candle 1
	engine.OnCandleClose(c1, symbol)

	if len(engine.rollingCandles[symbol]) != 1 {
		t.Fatalf("expected 1 rolling candle after duplicate c1, got %d", len(engine.rollingCandles[symbol]))
	}
	if engine.masterCandles[symbol] == nil {
		t.Fatal("expected Master Candle to be formed")
	}
	if engine.secondCandles[symbol] != nil {
		t.Fatal("second candle should not be formed from duplicate c1")
	}

	// 2. Candle 2 (09:20)
	c2 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   504.0,
		High:   506.0,
		Low:    503.0,
		Close:  505.0,
		Volume: 5000,
	}
	engine.OnCandleClose(c2, symbol)
	// Duplicate Candle 2
	engine.OnCandleClose(c2, symbol)

	if len(engine.rollingCandles[symbol]) != 2 {
		t.Fatalf("expected 2 rolling candles after duplicate c2, got %d", len(engine.rollingCandles[symbol]))
	}
	if engine.secondCandles[symbol] == nil {
		t.Fatal("expected Second Candle to be formed")
	}

	// 3. During Candle 3 (09:25–09:30): only 2 candles closed.
	// With MinCandlesToIgnore = 3, breakout must be IGNORED!
	sig := engine.CheckBreakout(symbol, 507.0, "BUY_ONLY")
	if sig != nil {
		t.Fatalf("expected nil signal during candle 3 when MinCandlesToIgnore=3, got %+v", sig)
	}

	// 4. Candle 3 (09:25) closes at 09:30
	c3 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(10 * time.Minute),
		Open:   505.0,
		High:   505.8,
		Low:    504.0,
		Close:  505.2,
		Volume: 4000,
	}
	engine.OnCandleClose(c3, symbol)

	if len(engine.rollingCandles[symbol]) != 3 {
		t.Fatalf("expected 3 rolling candles after c3, got %d", len(engine.rollingCandles[symbol]))
	}

	// 5. Now in Candle 4 (09:30+): 3 candles completed, MinCandlesToIgnore=3 is satisfied!
	sig2 := engine.CheckBreakout(symbol, 507.0, "BUY_ONLY")
	if sig2 == nil || sig2.Action != "BUY" {
		t.Fatalf("expected BUY breakout signal after 3 candles completed, got %+v", sig2)
	}
}
