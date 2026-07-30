package risk

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestRiskManagerDailyLossLimit(t *testing.T) {
	logger := zap.NewNop()
	
	limits := RiskLimits{
		MaxTradesPerDay:    10,
		MaxLossStreaks:     3,
		MaxHoldingTimeMin:  30,
		MaxDailyLossAmount: 100.0,
	}

	rm := NewRiskManager(nil, logger, 10000.0, limits)

	// 1. CanPlaceOrder should return true initially
	if !rm.CanPlaceOrder(1, 100.0) {
		t.Fatal("expected CanPlaceOrder to return true initially")
	}

	// 2. Add an open position
	rm.openPositions["order-1"] = &Position{
		OrderID:    "order-1",
		Symbol:     "SBIN",
		Quantity:   10,
		EntryPrice: 100.0,
		Side:       "BUY",
	}

	// 3. Close the position with a P&L of -120.0 (exceeds daily loss limit of 100.0)
	rm.OnOrderClose("order-1", 88.0, 10)

	// Verify daily P&L and circuit breaker state
	if rm.dailyPnL != -120.0 {
		t.Fatalf("expected daily PnL to be -120.0, got %f", rm.dailyPnL)
	}

	if !rm.circuitBreakerHit {
		t.Fatal("expected circuit breaker to be hit after exceeding daily loss limit")
	}

	// 4. CanPlaceOrder should now return false since circuit breaker is active
	if rm.CanPlaceOrder(1, 100.0) {
		t.Fatal("expected CanPlaceOrder to return false when circuit breaker is active")
	}
}

func TestRiskManagerDailyLossLimitBypassedIfZero(t *testing.T) {
	logger := zap.NewNop()
	
	limits := RiskLimits{
		MaxTradesPerDay:    10,
		MaxLossStreaks:     3,
		MaxHoldingTimeMin:  30,
		MaxDailyLossAmount: 0.0, // Disabled
	}

	rm := NewRiskManager(nil, logger, 10000.0, limits)

	rm.openPositions["order-1"] = &Position{
		OrderID:    "order-1",
		Symbol:     "SBIN",
		Quantity:   10,
		EntryPrice: 100.0,
		Side:       "BUY",
	}

	// Close with -500.0 P&L
	rm.OnOrderClose("order-1", 50.0, 10)

	if rm.dailyPnL != -500.0 {
		t.Fatalf("expected daily PnL to be -500.0, got %f", rm.dailyPnL)
	}

	if rm.circuitBreakerHit {
		t.Fatal("expected circuit breaker NOT to be hit when MaxDailyLossAmount is 0")
	}

	if !rm.CanPlaceOrder(1, 100.0) {
		t.Fatal("expected CanPlaceOrder to return true since circuit breaker is not active")
	}
}

func TestRiskManagerPartialExitAndSLTrailing(t *testing.T) {
	logger := zap.NewNop()
	limits := RiskLimits{
		MaxTradesPerDay:    10,
		MaxLossStreaks:     3,
		MaxHoldingTimeMin:  360,
		MaxDailyLossAmount: 5000.0,
	}

	// ==========================================
	// 1. BUY Position Test
	// ==========================================
	rm := NewRiskManager(nil, logger, 100000.0, limits)
	rm.openPositions["order-buy"] = &Position{
		OrderID:           "order-buy",
		Symbol:            "SBIN",
		Quantity:          10,
		EntryPrice:        100.0,
		SLPrice:           90.0,
		Target1Price:      102.0, // +2.0% gain
		Side:              "BUY",
		IsPartialExitDone: false,
		CreatedAt:         time.Now(),
	}

	// Price at 100.2 (+0.2% gain) -> No SL trail yet
	action := rm.CheckTrailingSL("order-buy", 100.2)
	if action != "" {
		t.Errorf("expected empty action at 100.2, got %s", action)
	}

	// Price at 100.3 (+0.3% gain) -> Stage 1 SL trail to 100.05 (+0.05% break-even buffer)
	action = rm.CheckTrailingSL("order-buy", 100.3)
	if action != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED at 100.3, got %s", action)
	}
	if rm.openPositions["order-buy"].SLPrice != 100.05 {
		t.Errorf("expected SL to trail to 100.05, got %f", rm.openPositions["order-buy"].SLPrice)
	}

	// Price hits Target 1 (102.0) -> Trigger PARTIAL_EXIT and trail Stop-Loss to +1.0% (101.00)
	action = rm.CheckTrailingSL("order-buy", 102.0)
	if action != "PARTIAL_EXIT" {
		t.Errorf("expected PARTIAL_EXIT at 102.0, got %s", action)
	}

	pos := rm.openPositions["order-buy"]
	if !pos.IsPartialExitDone {
		t.Error("expected IsPartialExitDone to be true")
	}
	if pos.SLPrice != 101.00 {
		t.Errorf("expected Stop-Loss to trail to 101.00, got %f", pos.SLPrice)
	}

	// Record partial exit of 6 lots at 102.0
	rm.RecordPartialExit("order-buy", 102.0, 6)
	if pos.Quantity != 4 {
		t.Errorf("expected remaining quantity to be 4, got %d", pos.Quantity)
	}

	// ==========================================
	// 2. SELL Position Test
	// ==========================================
	rmSell := NewRiskManager(nil, logger, 100000.0, limits)
	rmSell.openPositions["order-sell"] = &Position{
		OrderID:           "order-sell",
		Symbol:            "TATASTEEL",
		Quantity:          10,
		EntryPrice:        100.0,
		SLPrice:           110.0,
		Target1Price:      98.0, // +2.0% gain for SHORT
		Side:              "SELL",
		IsPartialExitDone: false,
		CreatedAt:         time.Now(),
	}

	// Price at 99.8 (+0.2% gain) -> No SL trail yet
	action = rmSell.CheckTrailingSL("order-sell", 99.8)
	if action != "" {
		t.Errorf("expected empty action at 99.8, got %s", action)
	}

	// Price drops to Target 1 (98.0) -> Trigger PARTIAL_EXIT and trail Stop-Loss to 99.00 (+1.0% locked)
	action = rmSell.CheckTrailingSL("order-sell", 98.0)
	if action != "PARTIAL_EXIT" {
		t.Errorf("expected PARTIAL_EXIT at 98.0, got %s", action)
	}

	posSell := rmSell.openPositions["order-sell"]
	if !posSell.IsPartialExitDone {
		t.Error("expected IsPartialExitDone to be true for SELL")
	}
	if posSell.SLPrice != 99.00 {
		t.Errorf("expected Stop-Loss to trail to 99.00 for SELL, got %f", posSell.SLPrice)
	}

	// Record partial exit of 5 lots at 80.0
	rmSell.RecordPartialExit("order-sell", 80.0, 5)
	if posSell.Quantity != 5 {
		t.Errorf("expected remaining quantity to be 5 for SELL, got %d", posSell.Quantity)
	}
	// P&L = (100 - 80) * 5 = +100
	if rmSell.dailyPnL != 100.0 {
		t.Errorf("expected daily P&L to be 100.0 for SELL, got %f", rmSell.dailyPnL)
	}

	// Price goes up to 100.0 -> Should trigger soft SL breach
	action = rmSell.CheckTrailingSL("order-sell", 100.0)
	if action != "CLOSE" {
		t.Errorf("expected CLOSE action at 100.0 for SELL, got %s", action)
	}
}

func TestRiskManagerOnOrderCloseDoesNotDeleteForSLID(t *testing.T) {
	logger := zap.NewNop()
	limits := RiskLimits{
		MaxTradesPerDay:    10,
		MaxLossStreaks:     3,
		MaxHoldingTimeMin:  360,
		MaxDailyLossAmount: 5000.0,
	}

	rm := NewRiskManager(nil, logger, 100000.0, limits)
	entryOrderID := "entry-order-1"
	slOrderID := "sl-order-1"

	rm.openPositions[entryOrderID] = &Position{
		OrderID:         entryOrderID,
		Symbol:          "SBIN",
		Quantity:        10,
		EntryPrice:      100.0,
		SLPrice:         90.0,
		Side:            "BUY",
		BrokerSLOrderID: slOrderID,
		CreatedAt:       time.Now(),
	}

	// 1. Call OnOrderClose with the BrokerSLOrderID
	rm.OnOrderClose(slOrderID, 0, 0)

	// Verify that the position is STILL in memory (not deleted!)
	if _, exists := rm.openPositions[entryOrderID]; !exists {
		t.Fatal("expected position to NOT be deleted when OnOrderClose is called with BrokerSLOrderID")
	}

	// 2. Call OnOrderClose with the actual EntryOrderID
	rm.OnOrderClose(entryOrderID, 105.0, 10)

	// Verify that the position is now successfully deleted from memory
	if _, exists := rm.openPositions[entryOrderID]; exists {
		t.Fatal("expected position to be deleted when OnOrderClose is called with EntryOrderID")
	}
}

func TestRiskManagerOnOrderCloseIgnoresZeroQuantity(t *testing.T) {
	logger := zap.NewNop()
	limits := RiskLimits{
		MaxTradesPerDay: 10,
	}
	rm := NewRiskManager(nil, logger, 100000.0, limits)

	entryOrderID := "entry-order-2"
	rm.openPositions[entryOrderID] = &Position{
		OrderID:    entryOrderID,
		Symbol:     "TCS",
		Quantity:   10,
		EntryPrice: 3000.0,
		Side:       "BUY",
		CreatedAt:  time.Now(),
	}

	// Call OnOrderClose with 0 quantity
	rm.OnOrderClose(entryOrderID, 0.0, 0)

	// Position should be deleted from memory
	if _, exists := rm.openPositions[entryOrderID]; exists {
		t.Fatal("expected position to be deleted from memory even with 0 quantity")
	}

	// No closed trade should be recorded
	if len(rm.closedTrades) != 0 {
		t.Errorf("expected 0 closed trades to be recorded, got %d", len(rm.closedTrades))
	}
}

// TestAllMultiStageTrailingSLBUY verifies all 5 trailing stages for BUY setups step-by-step
func TestAllMultiStageTrailingSLBUY(t *testing.T) {
	logger := zap.NewNop()
	limits := RiskLimits{MaxTradesPerDay: 10, MaxDailyLossAmount: 5000.0}
	rm := NewRiskManager(nil, logger, 100000.0, limits)

	entryPrice := 100.0
	rm.openPositions["test-buy-all"] = &Position{
		OrderID:      "test-buy-all",
		Symbol:       "SBIN",
		Quantity:     100,
		EntryPrice:   entryPrice,
		SLPrice:      98.50, // Initial 1.5% SL
		HighestPrice: entryPrice,
		Side:         "BUY",
		CreatedAt:    time.Now(),
	}

	// 1. Gain < +0.3% (e.g. 100.20) -> No Trail
	action := rm.CheckTrailingSL("test-buy-all", 100.20)
	if action != "" {
		t.Fatalf("expected empty action at +0.2%%, got %s", action)
	}
	if rm.openPositions["test-buy-all"].SLPrice != 98.50 {
		t.Fatalf("expected SL to remain 98.50, got %f", rm.openPositions["test-buy-all"].SLPrice)
	}

	// 2. Stage 1: Gain >= +0.3% (e.g. 100.35) -> SL trails to Break-Even (+0.05% = 100.05)
	action = rm.CheckTrailingSL("test-buy-all", 100.35)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED at +0.35%%, got %s", action)
	}
	if rm.openPositions["test-buy-all"].SLPrice != 100.05 {
		t.Fatalf("expected SL to trail to 100.05, got %f", rm.openPositions["test-buy-all"].SLPrice)
	}

	// 3. Stage 2: Gain >= +0.7% (e.g. 100.75) -> SL trails to +0.3% (100.30)
	action = rm.CheckTrailingSL("test-buy-all", 100.75)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED at +0.75%%, got %s", action)
	}
	if rm.openPositions["test-buy-all"].SLPrice != 100.30 {
		t.Fatalf("expected SL to trail to 100.30, got %f", rm.openPositions["test-buy-all"].SLPrice)
	}

	// 4. Stage 3: Gain >= +1.2% (e.g. 101.25) -> SL trails to +0.6% (100.60)
	action = rm.CheckTrailingSL("test-buy-all", 101.25)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED at +1.25%%, got %s", action)
	}
	if rm.openPositions["test-buy-all"].SLPrice != 100.60 {
		t.Fatalf("expected SL to trail to 100.60, got %f", rm.openPositions["test-buy-all"].SLPrice)
	}

	// 5. Stage 4: Target 1 (+2.0% = 102.00) -> PARTIAL_EXIT & SL trails to +1.0% (101.00)
	action = rm.CheckTrailingSL("test-buy-all", 102.05)
	if action != "PARTIAL_EXIT" {
		t.Fatalf("expected PARTIAL_EXIT at +2.05%%, got %s", action)
	}
	if rm.openPositions["test-buy-all"].SLPrice != 101.00 {
		t.Fatalf("expected SL to trail to 101.00, got %f", rm.openPositions["test-buy-all"].SLPrice)
	}

	// Record partial exit of 60 shares
	rm.RecordPartialExit("test-buy-all", 102.00, 60)
	if rm.openPositions["test-buy-all"].Quantity != 40 {
		t.Fatalf("expected remaining quantity 40, got %d", rm.openPositions["test-buy-all"].Quantity)
	}

	// 6. Stage 5: High Gain >= +2.5% (e.g. 103.00) -> SL trails to Peak - 0.6% (103.00 * 0.994 = 102.38 -> 102.40)
	action = rm.CheckTrailingSL("test-buy-all", 103.00)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED at +3.0%%, got %s", action)
	}
	if rm.openPositions["test-buy-all"].SLPrice < 102.35 {
		t.Fatalf("expected SL to step-trail above 102.35, got %f", rm.openPositions["test-buy-all"].SLPrice)
	}
}

// TestAllMultiStageTrailingSLSELL verifies all trailing stages for SHORT setups
func TestAllMultiStageTrailingSLSELL(t *testing.T) {
	logger := zap.NewNop()
	limits := RiskLimits{MaxTradesPerDay: 10, MaxDailyLossAmount: 5000.0}
	rm := NewRiskManager(nil, logger, 100000.0, limits)

	entryPrice := 100.0
	rm.openPositions["test-sell-all"] = &Position{
		OrderID:      "test-sell-all",
		Symbol:       "NMDC",
		Quantity:     100,
		EntryPrice:   entryPrice,
		SLPrice:      101.50, // Initial 1.5% SL for SHORT
		HighestPrice: entryPrice,
		Side:         "SELL",
		CreatedAt:    time.Now(),
	}

	// Stage 1: Gain >= +0.3% (Price drops to 99.65) -> SL trails to 99.95 (Break-Even)
	action := rm.CheckTrailingSL("test-sell-all", 99.65)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED for SELL at 99.65, got %s", action)
	}
	if rm.openPositions["test-sell-all"].SLPrice != 99.95 {
		t.Fatalf("expected SL to trail to 99.95, got %f", rm.openPositions["test-sell-all"].SLPrice)
	}

	// Stage 2: Gain >= +0.7% (Price drops to 99.25) -> SL trails to 99.70 (+0.3% locked)
	action = rm.CheckTrailingSL("test-sell-all", 99.25)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED for SELL at 99.25, got %s", action)
	}
	if rm.openPositions["test-sell-all"].SLPrice != 99.70 {
		t.Fatalf("expected SL to trail to 99.70, got %f", rm.openPositions["test-sell-all"].SLPrice)
	}
}

// TestRoundTickIEEE754FloatTrimming tests float rounding to exact 0.05 exchange ticks
func TestRoundTickIEEE754FloatTrimming(t *testing.T) {
	tests := []struct {
		input    float64
		tickSize float64
		expected float64
	}{
		{85.52451, 0.05, 85.50},
		{85.53999, 0.05, 85.55},
		{2099.6000000000004, 0.05, 2099.60},
		{100.05000000000001, 0.05, 100.05},
		{1497.62, 0.05, 1497.60},
	}

	for _, tt := range tests {
		got := RoundTick(tt.input, tt.tickSize)
		if got != tt.expected {
			t.Errorf("RoundTick(%f, %f) = %f; want %f", tt.input, tt.tickSize, got, tt.expected)
		}
	}
}

// TestTimeDecayGuardAfter45Minutes tests that positions held > 45 mins with gain >= 0.2% lock Break-Even
func TestTimeDecayGuardAfter45Minutes(t *testing.T) {
	logger := zap.NewNop()
	limits := RiskLimits{MaxTradesPerDay: 10, MaxHoldingTimeMin: 360}
	rm := NewRiskManager(nil, logger, 100000.0, limits)

	rm.openPositions["time-decay-test"] = &Position{
		OrderID:         "time-decay-test",
		Symbol:          "TCS",
		Quantity:        10,
		EntryPrice:      100.0,
		SLPrice:         98.50,
		HighestPrice:    100.25, // +0.25% peak gain (below Stage 1 +0.3%)
		Side:            "BUY",
		BrokerSLOrderID: "sl-order-time-decay",
		CreatedAt:       time.Now().Add(-50 * time.Minute), // Held 50 minutes
	}

	action := rm.CheckTrailingSL("time-decay-test", 100.25)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED for 50-min time decay guard, got %s", action)
	}
	if rm.openPositions["time-decay-test"].SLPrice != 100.05 {
		t.Fatalf("expected 50-min time decay guard to trail SL to 100.05, got %f", rm.openPositions["time-decay-test"].SLPrice)
	}
}
