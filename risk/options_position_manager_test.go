package risk

import (
	"testing"
	"go.uber.org/zap"
)

func TestOptionsPositionManagerReversalAndSLRecovery(t *testing.T) {
	logger := zap.NewNop()
	mgr := NewOptionsPositionManager(nil, logger, 25, 3, 50.0, 1000000.0)

	// 1. Initial Signal: BULLISH -> Action = OPEN_INITIAL, Qty = 25 (1x)
	action, qty := mgr.EvaluateSignal("BULLISH")
	if action != "OPEN_INITIAL" || qty != 25 {
		t.Fatalf("expected OPEN_INITIAL with qty 25, got %s qty %d", action, qty)
	}

	mgr.OnTradeOpened("order-1", "NIFTY26AUG24000PE", "PE", 25, 100.0)
	status := mgr.GetStatus()
	if status["sl_price"].(float64) != 150.0 {
		t.Fatalf("expected 50%% SL price 150.0, got %v", status["sl_price"])
	}

	// 2. Trend Reversal: BULLISH -> BEARISH -> Action = REVERSAL, Qty = 50 (2x)
	action, qty = mgr.EvaluateSignal("BEARISH")
	if action != "REVERSAL" || qty != 50 {
		t.Fatalf("expected REVERSAL with qty 50 (2x multiplier), got %s qty %d", action, qty)
	}

	// Close old trade and open new BEARISH trade
	mgr.OnTradeClosed(80.0)
	mgr.OnTradeOpened("order-2", "NIFTY26AUG24600CE", "CE", 50, 120.0)

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

	// 6. Test Post-SL Guard: Trend Complete Reversal (BEARISH -> BULLISH) -> Clears Guard & Opens Trade at 1x (25 Qty)!
	action, qty = mgr.EvaluateSignal("BULLISH")
	if action != "OPEN_INITIAL" || qty != 25 {
		t.Fatalf("expected OPEN_INITIAL with 25 qty on complete trend reversal, got %s qty %d", action, qty)
	}
}
