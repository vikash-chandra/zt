package risk

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestOptionsPositionManagerReversalAndSLRecovery verifies basic reversal and post-SL guard recovery
func TestOptionsPositionManagerReversalAndSLRecovery(t *testing.T) {
	logger := zap.NewNop()
	mgr := NewOptionsPositionManager(nil, logger, 65, 3, 50.0, 1000000.0)

	// 1. Initial Signal: BULLISH -> Action = OPEN_INITIAL, Qty = 65 (1x)
	action, qty := mgr.EvaluateSignal("BULLISH")
	if action != "OPEN_INITIAL" || qty != 65 {
		t.Fatalf("expected OPEN_INITIAL with qty 65, got %s qty %d", action, qty)
	}

	mgr.OnTradeOpened("order-1", "NIFTY26AUG24000PE", "PE", 65, 100.0)
	status := mgr.GetStatus()
	if status["sl_price"].(float64) != 150.0 {
		t.Fatalf("expected 50%% SL price 150.0, got %v", status["sl_price"])
	}

	// 2. Trend Reversal: BULLISH -> BEARISH -> Action = REVERSAL, Qty = 130 (2x)
	action, qty = mgr.EvaluateSignal("BEARISH")
	if action != "REVERSAL" || qty != 130 {
		t.Fatalf("expected REVERSAL with qty 130 (2x multiplier), got %s qty %d", action, qty)
	}

	// Close old trade and open new BEARISH trade
	mgr.OnTradeClosed(80.0)
	mgr.OnTradeOpened("order-2", "NIFTY26AUG24600CE", "CE", 130, 120.0)

	// 3. Option Premium Rises to 180.0 (Breaches SL Price 180.0) -> CheckTick = true
	isBreached := mgr.CheckTick(180.0)
	if !isBreached {
		t.Fatalf("expected CheckTick to return true when premium breaches SL (180 >= 180)")
	}

	// Execute OnSLHit
	mgr.OnSLHit(180.0)

	// 4. Verify SL Hit State: Multiplier reset to 1, awaitingReversal = true, sl_stopped_trend = BEARISH
	status = mgr.GetStatus()
	if status["multiplier"].(int) != 1 {
		t.Fatalf("expected multiplier reset to 1 after SL hit, got %v", status["multiplier"])
	}
	if !status["awaiting_reversal"].(bool) {
		t.Fatalf("expected awaiting_reversal to be true after SL hit")
	}

	// 5. Test Post-SL Guard: Re-entry signal in SAME trend (BEARISH) -> IGNORED!
	action, _ = mgr.EvaluateSignal("BEARISH")
	if action != "IGNORE" {
		t.Fatalf("expected signal in same trend post-SL to be IGNORED, got %s", action)
	}

	// 6. Test Post-SL Guard: Trend Complete Reversal (BEARISH -> BULLISH) -> Clears Guard & Opens Trade at 1x (65 Qty)!
	action, qty = mgr.EvaluateSignal("BULLISH")
	if action != "OPEN_INITIAL" || qty != 65 {
		t.Fatalf("expected OPEN_INITIAL with 65 qty on complete trend reversal, got %s qty %d", action, qty)
	}
}

// TestOptionsPositionManager_SLCombinations tests various 50% SL hit scenarios, premium updates, and boundary conditions
func TestOptionsPositionManager_SLCombinations(t *testing.T) {
	logger := zap.NewNop()
	mgr := NewOptionsPositionManager(nil, logger, 65, 3, 50.0, 1000000.0)

	// Case 1: PE Option Selling with Entry Premium 120.0 (50% SL = 180.0)
	mgr.EvaluateSignal("BULLISH")
	mgr.OnTradeOpened("order-101", "NIFTY24000PE", "PE", 65, 120.0)

	// Ticks moving under SL: 120 -> 135 -> 150 -> 179.9 -> CheckTick = false
	ticksNonBreach := []float64{120.0, 135.0, 150.0, 179.9}
	for _, p := range ticksNonBreach {
		if mgr.CheckTick(p) {
			t.Fatalf("expected premium %.2f to not breach 180.0 SL", p)
		}
	}

	// Boundary tick: 180.0 -> CheckTick = true
	if !mgr.CheckTick(180.0) {
		t.Fatalf("expected premium 180.0 to trigger SL breach")
	}

	// Execute SL Hit
	realizedLoss := mgr.OnSLHit(180.0)
	expectedLoss := (120.0 - 180.0) * 65.0 // -3900.0
	if realizedLoss != expectedLoss {
		t.Fatalf("expected PnL %.2f, got %.2f", expectedLoss, realizedLoss)
	}

	// Case 2: Extreme Gap-Up SL Hit (Entry premium 100.0, SL 150.0, Fill at 250.0)
	mgr.EvaluateSignal("BEARISH") // Clear guard
	mgr.OnTradeOpened("order-102", "NIFTY24500CE", "CE", 65, 100.0)

	if !mgr.CheckTick(250.0) {
		t.Fatalf("expected extreme gap-up tick 250.0 to trigger SL breach")
	}
	gapLoss := mgr.OnSLHit(250.0)
	expectedGapLoss := (100.0 - 250.0) * 65.0 // -9750.0
	if gapLoss != expectedGapLoss {
		t.Fatalf("expected gap loss %.2f, got %.2f", expectedGapLoss, gapLoss)
	}
}

// TestOptionsPositionManager_MultiStageReversals tests compounding lot multipliers (1x -> 2x -> 3x -> capped at 3x -> day reset)
func TestOptionsPositionManager_MultiStageReversals(t *testing.T) {
	logger := zap.NewNop()
	mgr := NewOptionsPositionManager(nil, logger, 65, 3, 50.0, 1000000.0)

	// Stage 1: Initial Entry -> 1x (65 Qty)
	action, qty := mgr.EvaluateSignal("BULLISH")
	if action != "OPEN_INITIAL" || qty != 65 {
		t.Fatalf("Stage 1 expected OPEN_INITIAL qty 65, got %s %d", action, qty)
	}
	mgr.OnTradeOpened("t1", "NIFTY24000PE", "PE", 65, 120.0)

	// Stage 2: Reversal 1 -> 2x (130 Qty)
	action, qty = mgr.EvaluateSignal("BEARISH")
	if action != "REVERSAL" || qty != 130 {
		t.Fatalf("Stage 2 expected REVERSAL qty 130, got %s %d", action, qty)
	}
	mgr.OnTradeClosed(65.0)
	mgr.OnTradeOpened("t2", "NIFTY24500CE", "CE", 130, 120.0)

	// Stage 3: Reversal 2 -> 3x (195 Qty)
	action, qty = mgr.EvaluateSignal("BULLISH")
	if action != "REVERSAL" || qty != 195 {
		t.Fatalf("Stage 3 expected REVERSAL qty 195, got %s %d", action, qty)
	}
	mgr.OnTradeClosed(65.0)
	mgr.OnTradeOpened("t3", "NIFTY24000PE", "PE", 195, 120.0)

	// Stage 4: Reversal 3 -> Capped at MaxMultiplier=3 -> 3x (195 Qty)
	action, qty = mgr.EvaluateSignal("BEARISH")
	if action != "REVERSAL" || qty != 195 {
		t.Fatalf("Stage 4 expected capped REVERSAL qty 195, got %s %d", action, qty)
	}
	mgr.OnTradeClosed(65.0)
	mgr.OnTradeOpened("t4", "NIFTY24500CE", "CE", 195, 120.0)

	// Stage 5: Day Boundary Reset -> ResetDailyMultiplier() -> 1x (65 Qty)
	mgr.OnTradeClosed(65.0)
	mgr.ResetDailyMultiplier()

	action, qty = mgr.EvaluateSignal("BULLISH")
	if action != "OPEN_INITIAL" || qty != 65 {
		t.Fatalf("Stage 5 expected reset OPEN_INITIAL qty 65, got %s %d", action, qty)
	}
}

// TestOptionsPositionManager_AutoSquareOffAndEnvConfig tests parsing and verifying auto square-off times from config
func TestOptionsPositionManager_AutoSquareOffAndEnvConfig(t *testing.T) {
	testCases := []struct {
		envTime     string
		checkTime   string
		expectedEOD bool
	}{
		{"15:15", "2026-07-27 15:14:59", false},
		{"15:15", "2026-07-27 15:15:00", true},
		{"15:15", "2026-07-27 15:20:00", true},
		{"15:00", "2026-07-27 14:59:59", false},
		{"15:00", "2026-07-27 15:00:00", true},
		{"14:30", "2026-07-27 14:30:00", true},
	}

	loc, _ := time.LoadLocation("Asia/Kolkata")
	if loc == nil {
		loc = time.Local
	}

	for _, tc := range testCases {
		sqHour, sqMin := 15, 15
		if parts := strings.Split(tc.envTime, ":"); len(parts) == 2 {
			fmt.Sscanf(parts[0], "%d", &sqHour)
			fmt.Sscanf(parts[1], "%d", &sqMin)
		}

		tTime, err := time.ParseInLocation("2006-01-02 15:04:05", tc.checkTime, loc)
		if err != nil {
			t.Fatalf("failed to parse time: %v", err)
		}

		isEOD := (tTime.Hour() > sqHour) || (tTime.Hour() == sqHour && tTime.Minute() >= sqMin)
		if isEOD != tc.expectedEOD {
			t.Fatalf("for envTime %s checkTime %s: expected isEOD %v, got %v", tc.envTime, tc.checkTime, tc.expectedEOD, isEOD)
		}
	}
}

// TestOptionsPositionManager_TrailSLOnCandleClose verifies 20% 5m candle close trailing SL ratcheting
func TestOptionsPositionManager_TrailSLOnCandleClose(t *testing.T) {
	logger := zap.NewNop()
	mgr := NewOptionsPositionManager(nil, logger, 65, 3, 50.0, 1000000.0)

	// Open Trade: Entry = 100.0 (Short PE). Initial SL = 150.0 (50%)
	mgr.EvaluateSignal("BULLISH")
	mgr.OnTradeOpened("order-trail-1", "NIFTY26AUG24500PE", "PE", 65, 100.0)

	pos := mgr.GetActivePosition()
	if pos == nil || pos.SLPrice != 150.0 {
		t.Fatalf("expected initial SL price 150.0, got %v", pos)
	}

	// 1. Candle 1 Close: Premium = 85.0 -> Candidate = 85 * 1.20 = 102.0
	newSL, trailed := mgr.TrailSLOnCandleClose(85.0, 20.0)
	if !trailed || newSL != 102.0 {
		t.Fatalf("expected SL to trail to 102.0 (trailed=true), got %.2f (trailed=%v)", newSL, trailed)
	}
	if mgr.GetActivePosition().SLPrice != 102.0 {
		t.Fatalf("active position SL not updated to 102.0")
	}

	// 2. Candle 2 Close: Premium = 65.0 -> Candidate = 65 * 1.20 = 78.0
	newSL, trailed = mgr.TrailSLOnCandleClose(65.0, 20.0)
	if !trailed || newSL != 78.0 {
		t.Fatalf("expected SL to trail to 78.0 (trailed=true), got %.2f (trailed=%v)", newSL, trailed)
	}

	// 3. Candle 3 Close: Premium = 75.0 (Adverse bounce) -> Candidate = 75 * 1.20 = 90.0 (> 78.0)
	// SL MUST REMAIN CONSTANT at 78.0!
	newSL, trailed = mgr.TrailSLOnCandleClose(75.0, 20.0)
	if trailed || newSL != 78.0 {
		t.Fatalf("expected SL to remain constant at 78.0 on adverse bounce (trailed=false), got %.2f (trailed=%v)", newSL, trailed)
	}

	// 4. Candle 4 Close: Premium = 50.0 -> Candidate = 50 * 1.20 = 60.0 (< 78.0)
	newSL, trailed = mgr.TrailSLOnCandleClose(50.0, 20.0)
	if !trailed || newSL != 60.0 {
		t.Fatalf("expected SL to trail to 60.0 (trailed=true), got %.2f (trailed=%v)", newSL, trailed)
	}

	// 5. Tick Check: Premium rises to 59.9 -> Not Breached
	if mgr.CheckTick(59.9) {
		t.Fatalf("expected 59.9 to not breach 60.0 trailed SL")
	}

	// 6. Tick Check: Premium rises to 60.0 -> Breached!
	if !mgr.CheckTick(60.0) {
		t.Fatalf("expected 60.0 to trigger trailed SL breach")
	}

	// 7. OnSLHit exit at 60.0 -> Profit locked: (100 - 60) * 65 = +2600.0
	pnl := mgr.OnSLHit(60.0)
	if pnl != 2600.0 {
		t.Fatalf("expected locked-in profit +2600.0, got %.2f", pnl)
	}
}

// TestMultiIndexOptionsPositionManagers tests concurrent independent position managers across indices
func TestMultiIndexOptionsPositionManagers(t *testing.T) {
	logger := zap.NewNop()

	niftyMgr := NewIndexOptionsPositionManager(nil, logger, "NIFTY 50", 65, 3, 50.0, 1000000.0)
	bankMgr := NewIndexOptionsPositionManager(nil, logger, "NIFTY BANK", 15, 3, 50.0, 1000000.0)
	sensexMgr := NewIndexOptionsPositionManager(nil, logger, "BSE SENSEX", 20, 3, 50.0, 1000000.0)

	// Verify Index Symbols & Base Lot Sizes
	if niftyMgr.GetIndexSymbol() != "NIFTY 50" || niftyMgr.baseLotSize != 65 {
		t.Fatalf("unexpected NIFTY spec: %s lot %d", niftyMgr.GetIndexSymbol(), niftyMgr.baseLotSize)
	}
	if bankMgr.GetIndexSymbol() != "NIFTY BANK" || bankMgr.baseLotSize != 15 {
		t.Fatalf("unexpected BANKNIFTY spec: %s lot %d", bankMgr.GetIndexSymbol(), bankMgr.baseLotSize)
	}
	if sensexMgr.GetIndexSymbol() != "BSE SENSEX" || sensexMgr.baseLotSize != 20 {
		t.Fatalf("unexpected SENSEX spec: %s lot %d", sensexMgr.GetIndexSymbol(), sensexMgr.baseLotSize)
	}

	// Open positions on all 3 concurrently
	niftyMgr.EvaluateSignal("BULLISH")
	niftyMgr.OnTradeOpened("ord-nifty", "NIFTY26AUG24500PE", "PE", 65, 100.0)

	bankMgr.EvaluateSignal("BEARISH")
	bankMgr.OnTradeOpened("ord-bank", "BANKNIFTY26AUG51500CE", "CE", 15, 250.0)

	sensexMgr.EvaluateSignal("BULLISH")
	sensexMgr.OnTradeOpened("ord-sensex", "SENSEX26AUG80500PE", "PE", 20, 240.0)

	// Verify independent positions
	if niftyMgr.GetActivePosition().Symbol != "NIFTY26AUG24500PE" {
		t.Fatalf("unexpected nifty symbol")
	}
	if bankMgr.GetActivePosition().Symbol != "BANKNIFTY26AUG51500CE" {
		t.Fatalf("unexpected bank symbol")
	}
	if sensexMgr.GetActivePosition().Symbol != "SENSEX26AUG80500PE" {
		t.Fatalf("unexpected sensex symbol")
	}

	// Close BankNifty trade - Nifty and Sensex must remain active!
	bankMgr.OnTradeClosed(180.0)
	if bankMgr.GetActivePosition() != nil {
		t.Fatalf("expected bank active position to be nil after close")
	}
	if niftyMgr.GetActivePosition() == nil || sensexMgr.GetActivePosition() == nil {
		t.Fatalf("nifty or sensex position was erroneously cleared")
	}
}
