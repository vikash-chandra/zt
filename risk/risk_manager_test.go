package risk

import (
	"fmt"
	"sync"
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
		InitialSLPrice:    90.0,
		InitialRisk:       10.0,
		Target1Price:      118.0, // 1:1.8 R:R gain
		Side:              "BUY",
		IsPartialExitDone: false,
		CreatedAt:         time.Now(),
	}

	// Price at 105.0 (+0.5x risk gain) -> No SL trail yet
	action := rm.CheckTrailingSL("order-buy", 105.0)
	if action != "" {
		t.Errorf("expected empty action at 105.0, got %s", action)
	}

	// Price at 108.0 (+0.8x risk gain) -> Stage 1 SL trail: Cost SL = 100.00
	action = rm.CheckTrailingSL("order-buy", 108.0)
	if action != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED at 108.0, got %s", action)
	}
	if rm.openPositions["order-buy"].SLPrice != 100.00 {
		t.Errorf("expected SL to trail to 100.00, got %f", rm.openPositions["order-buy"].SLPrice)
	}

	// Price hits Target 1 (118.0, 1:1.8 R:R) -> Trigger PARTIAL_EXIT and trail Stop-Loss: lock 2% of peak profit = 100.0 + 0.36 -> 100.35
	action = rm.CheckTrailingSL("order-buy", 118.0)
	if action != "PARTIAL_EXIT" {
		t.Errorf("expected PARTIAL_EXIT at 118.0, got %s", action)
	}

	pos := rm.openPositions["order-buy"]
	if !pos.IsPartialExitDone {
		t.Error("expected IsPartialExitDone to be true")
	}
	if pos.SLPrice != 100.35 {
		t.Errorf("expected Stop-Loss to trail to 100.35, got %f", pos.SLPrice)
	}

	// Record partial exit of 4 lots at 118.0
	rm.RecordPartialExit("order-buy", 118.0, 4)
	if pos.Quantity != 6 {
		t.Errorf("expected remaining quantity to be 6, got %d", pos.Quantity)
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
		InitialSLPrice:    110.0,
		InitialRisk:       10.0,
		Target1Price:      82.0, // 1:1.8 R:R gain for SHORT
		Side:              "SELL",
		IsPartialExitDone: false,
		CreatedAt:         time.Now(),
	}

	// Price at 95.0 (+0.5x risk gain) -> No SL trail yet
	action = rmSell.CheckTrailingSL("order-sell", 95.0)
	if action != "" {
		t.Errorf("expected empty action at 95.0, got %s", action)
	}

	// Price drops to 92.0 (+0.8x risk gain) -> Stage 1 SL trail: Cost SL = 100.00
	action = rmSell.CheckTrailingSL("order-sell", 92.0)
	if action != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED at 92.0 for SELL, got %s", action)
	}
	if rmSell.openPositions["order-sell"].SLPrice != 100.00 {
		t.Errorf("expected SL to trail to 100.00, got %f", rmSell.openPositions["order-sell"].SLPrice)
	}

	// Price drops to Target 1 (82.0, 1:1.8 R:R) -> Trigger PARTIAL_EXIT and trail Stop-Loss: lock 2% of peak profit = 100.0 - 0.36 -> 99.65
	action = rmSell.CheckTrailingSL("order-sell", 82.0)
	if action != "PARTIAL_EXIT" {
		t.Errorf("expected PARTIAL_EXIT at 82.0, got %s", action)
	}

	posSell := rmSell.openPositions["order-sell"]
	if !posSell.IsPartialExitDone {
		t.Error("expected IsPartialExitDone to be true for SELL")
	}
	if posSell.SLPrice != 99.65 {
		t.Errorf("expected Stop-Loss to trail to 99.65 for SELL, got %f", posSell.SLPrice)
	}

	// Record partial exit of 4 lots at 82.0
	rmSell.RecordPartialExit("order-sell", 82.0, 4)
	if posSell.Quantity != 6 {
		t.Errorf("expected remaining quantity to be 6 for SELL, got %d", posSell.Quantity)
	}
	// P&L = (100 - 82) * 4 = +72
	if rmSell.dailyPnL != 72.0 {
		t.Errorf("expected daily P&L to be 72.0 for SELL, got %f", rmSell.dailyPnL)
	}

	// Price goes up to 100.0 (breaching 99.80 SL) -> Should trigger soft SL breach
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
		OrderID:        "test-buy-all",
		Symbol:         "SBIN",
		Quantity:       100,
		EntryPrice:     entryPrice,
		SLPrice:        98.50, // Initial 1.50 INR risk
		InitialSLPrice: 98.50,
		InitialRisk:    1.50,
		HighestPrice:   entryPrice,
		Side:           "BUY",
		CreatedAt:      time.Now(),
	}

	// 1. Gain < 0.8x risk (e.g. 101.00 -> gain = 1.00 < 1.50 * 0.8 = 1.20) -> No Trail
	action := rm.CheckTrailingSL("test-buy-all", 101.00)
	if action != "" {
		t.Fatalf("expected empty action at 101.00, got %s", action)
	}
	if rm.openPositions["test-buy-all"].SLPrice != 98.50 {
		t.Fatalf("expected SL to remain 98.50, got %f", rm.openPositions["test-buy-all"].SLPrice)
	}

	// 2. Stage 1: Gain >= 0.8x risk (101.20 -> gain = 1.20 = 0.8 * 1.5) -> SL trails to Cost SL = 100.00
	action = rm.CheckTrailingSL("test-buy-all", 101.20)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED at 101.20 (1:0.8 R:R), got %s", action)
	}
	if rm.openPositions["test-buy-all"].SLPrice != 100.00 {
		t.Fatalf("expected Cost SL 100.00, got %f", rm.openPositions["test-buy-all"].SLPrice)
	}

	// 3. Stage 2: Gain >= 1.0x risk (101.50 -> gain = 1.50 = 1.0 * 1.5) -> SL trails: lock 2% of peak profit = 100.05
	action = rm.CheckTrailingSL("test-buy-all", 101.50)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED at 101.50 (1:1.0 R:R), got %s", action)
	}
	if rm.openPositions["test-buy-all"].SLPrice != 100.05 {
		t.Fatalf("expected SL to trail to 100.05, got %f", rm.openPositions["test-buy-all"].SLPrice)
	}

	// 4. Stage 3: Gain >= 1.5x risk (102.25 -> gain = 2.25 = 1.5 * 1.5) -> SL trails: lock 2% of peak profit = 100.05
	_ = rm.CheckTrailingSL("test-buy-all", 102.25)
	if rm.openPositions["test-buy-all"].SLPrice != 100.05 {
		t.Fatalf("expected SL to remain 100.05, got %f", rm.openPositions["test-buy-all"].SLPrice)
	}

	// 5. Stage 4: Target 1 (1:1.8 R:R = 102.70) -> PARTIAL_EXIT & trail SL to lock 2% of peak profit = 100.05
	action = rm.CheckTrailingSL("test-buy-all", 102.70)
	if action != "PARTIAL_EXIT" {
		t.Fatalf("expected PARTIAL_EXIT at 102.70 (1:1.8 R:R), got %s", action)
	}
	if rm.openPositions["test-buy-all"].SLPrice != 100.05 {
		t.Fatalf("expected SL 100.05, got %f", rm.openPositions["test-buy-all"].SLPrice)
	}

	// Record partial exit of 35 shares
	rm.RecordPartialExit("test-buy-all", 102.70, 35)
	if rm.openPositions["test-buy-all"].Quantity != 65 {
		t.Fatalf("expected remaining quantity 65, got %d", rm.openPositions["test-buy-all"].Quantity)
	}

	// 6. Stage 5: Runner Gain >= 2.5x risk (103.75 -> gain = 3.75 = 2.5 * 1.5) -> SL trails to 0.5 R reward = 100.00 + 0.5 * 1.5 = 100.75
	action = rm.CheckTrailingSL("test-buy-all", 103.75)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED at 103.75 (1:2.5 R:R), got %s", action)
	}
	if rm.openPositions["test-buy-all"].SLPrice != 100.75 {
		t.Fatalf("expected SL to step-trail to 100.75, got %f", rm.openPositions["test-buy-all"].SLPrice)
	}

	// 7. Stage 5: Runner Gain >= 3.0x risk (104.50 -> gain = 4.50 = 3.0 * 1.5) -> SL trails to 1.0 R reward = 100.00 + 1.0 * 1.5 = 101.50
	action = rm.CheckTrailingSL("test-buy-all", 104.50)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED at 104.50 (1:3.0 R:R), got %s", action)
	}
	if rm.openPositions["test-buy-all"].SLPrice != 101.50 {
		t.Fatalf("expected SL to step-trail to 101.50, got %f", rm.openPositions["test-buy-all"].SLPrice)
	}
}

// TestAllMultiStageTrailingSLSELL verifies all trailing stages for SHORT setups
func TestAllMultiStageTrailingSLSELL(t *testing.T) {
	logger := zap.NewNop()
	limits := RiskLimits{MaxTradesPerDay: 10, MaxDailyLossAmount: 5000.0}
	rm := NewRiskManager(nil, logger, 100000.0, limits)

	entryPrice := 100.0
	rm.openPositions["test-sell-all"] = &Position{
		OrderID:        "test-sell-all",
		Symbol:         "NMDC",
		Quantity:       100,
		EntryPrice:     entryPrice,
		SLPrice:        101.50, // Initial 1.50 INR risk for SHORT
		InitialSLPrice: 101.50,
		InitialRisk:    1.50,
		HighestPrice:   entryPrice,
		Side:           "SELL",
		CreatedAt:      time.Now(),
	}

	// Stage 1: Gain >= 0.8x risk (Price drops to 98.80 -> gain = 1.20 = 0.8 * 1.5) -> SL trails to Cost SL = 100.00
	action := rm.CheckTrailingSL("test-sell-all", 98.80)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED for SELL at 98.80, got %s", action)
	}
	if rm.openPositions["test-sell-all"].SLPrice != 100.00 {
		t.Fatalf("expected Cost SL 100.00, got %f", rm.openPositions["test-sell-all"].SLPrice)
	}

	// Stage 2: Gain >= 1.0x risk (Price drops to 98.50 -> gain = 1.50 = 1.0 * 1.5) -> SL trails: lock 2% of peak profit = 99.95
	action = rm.CheckTrailingSL("test-sell-all", 98.50)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED for SELL at 98.50, got %s", action)
	}
	if rm.openPositions["test-sell-all"].SLPrice != 99.95 {
		t.Fatalf("expected SL to trail to 99.95, got %f", rm.openPositions["test-sell-all"].SLPrice)
	}

	// Stage 5: Runner Gain >= 2.5x risk (Price drops to 96.25 -> gain = 3.75 = 2.5 * 1.5) -> SL trails to 0.5 R reward = 100.00 - 0.5 * 1.5 = 99.25
	action = rm.CheckTrailingSL("test-sell-all", 96.25)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED for SELL at 96.25, got %s", action)
	}
	if rm.openPositions["test-sell-all"].SLPrice != 99.25 {
		t.Fatalf("expected SL to trail to 99.25, got %f", rm.openPositions["test-sell-all"].SLPrice)
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

// TestTimeDecayGuardAfter45Minutes tests that positions held > 45 mins with gain >= 0.2x risk lock Break-Even
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
		InitialSLPrice:  98.50,
		InitialRisk:     1.50,
		HighestPrice:    100.35, // +0.35 gain = 0.233x risk (above min 0.2x threshold)
		Side:            "BUY",
		BrokerSLOrderID: "sl-order-time-decay",
		CreatedAt:       time.Now().Add(-50 * time.Minute), // Held 50 minutes
	}

	action := rm.CheckTrailingSL("time-decay-test", 100.35)
	if action != "SL_TRAILED" {
		t.Fatalf("expected SL_TRAILED for 50-min time decay guard, got %s", action)
	}
	if rm.openPositions["time-decay-test"].SLPrice != 100.10 {
		t.Fatalf("expected 50-min time decay guard to trail SL to 100.10, got %f", rm.openPositions["time-decay-test"].SLPrice)
	}
}

// TestRiskManagerConcurrentRace stress-tests concurrent access to RiskManager state
func TestRiskManagerConcurrentRace(t *testing.T) {
	logger := zap.NewNop()
	limits := RiskLimits{
		MaxTradesPerDay:    100,
		MaxLossStreaks:     5,
		MaxHoldingTimeMin:  60,
		MaxDailyLossAmount: 10000.0,
	}
	rm := NewRiskManager(nil, logger, 100000.0, limits)

	var wg sync.WaitGroup
	numWorkers := 40
	iterations := 100

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		workerID := i
		go func() {
			defer wg.Done()
			orderID := fmt.Sprintf("order-%d", workerID)
			symbol := fmt.Sprintf("SYM%d", workerID%5)

			for j := 0; j < iterations; j++ {
				// 1. Check order placement and symbol check
				_ = rm.CanPlaceOrder(10, 100.0)
				_ = rm.HasOpenPosition(symbol)

				// 2. Add / update open position
				rm.AddOpenPosition(orderID, symbol, int64(workerID), 10, 100.0, "BUY", 98.0, "LOW_VOLUME", 104.0, time.Now())
				rm.UpdatePositionPrice(orderID, 101.0+float64(j%5))
				rm.SetBrokerSLOrderID(orderID, fmt.Sprintf("sl-%d", workerID))
				rm.UpdatePositionQuantity(orderID, 8)

				// 3. Evaluate trailing SL and partial exit
				_ = rm.CheckTrailingSL(orderID, 102.0)
				rm.RecordPartialExit(orderID, 102.0, 4)

				// 4. Close order
				rm.OnOrderClose(orderID, 103.0, 4)

				// 5. Dynamic config mutation and reading
				if j%10 == 0 {
					rm.SetMaxLossStreaks(5 + (j % 3))
					_ = rm.MaxLossStreaks()
					rm.SetMaxTradesPerDay(100 + j)
					rm.SetMaxDailyLossAmount(10000.0 + float64(j*100))
					rm.SetMaxHoldingTimeMin(60 + j)
					_ = rm.GetOpenPositions()
					_ = rm.GetMetrics()
				}
			}
		}()
	}

	wg.Wait()
}

// TestRiskManager_PartialExitEdgeCasesAndConcurrency tests single-share exits,
// quantity decrementing, automatic cleanup on zero quantity, and concurrent thread safety.
func TestRiskManager_PartialExitEdgeCasesAndConcurrency(t *testing.T) {
	logger := zap.NewNop()
	limits := RiskLimits{MaxTradesPerDay: 50, MaxDailyLossAmount: 10000.0}
	rm := NewRiskManager(nil, logger, 100000.0, limits)

	// 1. Edge Case: Single-share position (Quantity = 1)
	order1 := "order-qty-1"
	rm.AddOpenPosition(order1, "INFY", 1001, 1, 1500.0, "BUY", 1480.0, "LOW_VOLUME", 1540.0, time.Now())
	if !rm.HasOpenPosition("INFY") {
		t.Fatal("expected open position for INFY")
	}

	// Partial exit of 1 share
	rm.RecordPartialExit(order1, 1540.0, 1)

	// Assert position is cleaned up from openPositions
	openPos := rm.GetOpenPositions()
	if _, exists := openPos[order1]; exists {
		t.Errorf("expected position %s to be deleted from openPositions when quantity reaches 0", order1)
	}
	if rm.HasOpenPosition("INFY") {
		t.Errorf("expected HasOpenPosition to return false after full exit of 1 share")
	}

	// 2. Edge Case: Multi-share position (Quantity = 10, exit 5)
	order2 := "order-qty-10"
	rm.AddOpenPosition(order2, "TCS", 1002, 10, 3500.0, "BUY", 3450.0, "VANDE_BHARAT", 3600.0, time.Now())
	rm.RecordPartialExit(order2, 3600.0, 5)

	openPos2 := rm.GetOpenPositions()
	p2, exists := openPos2[order2]
	if !exists {
		t.Fatalf("expected position %s to remain in openPositions after partial exit", order2)
	}
	if p2.Quantity != 5 {
		t.Errorf("expected remaining quantity 5, got %d", p2.Quantity)
	}
	if !p2.IsPartialExitDone {
		t.Errorf("expected IsPartialExitDone to be true")
	}

	// 3. Concurrency Test: 20 goroutines reading GetOpenPositions and GetPartialExitPct
	// while other goroutines add, trail SL, and record partial exits
	var wg sync.WaitGroup
	workers := 15
	iterations := 100

	for i := 0; i < workers; i++ {
		wg.Add(1)
		workerID := i
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				sym := fmt.Sprintf("SYM_%d_%d", workerID, j%5)
				ordID := fmt.Sprintf("ord_%d_%d", workerID, j)

				// Concurrent reads of GetPartialExitPct
				pctLV := rm.GetPartialExitPct("LOW_VOLUME")
				pctVB := rm.GetPartialExitPct("VANDE_BHARAT")
				pctES5 := rm.GetPartialExitPct("EMAS5_BREAKOUT")
				if pctLV <= 0 || pctVB <= 0 || pctES5 <= 0 {
					t.Errorf("invalid partial exit pct: %f, %f, %f", pctLV, pctVB, pctES5)
				}

				// Concurrent position addition
				rm.AddOpenPosition(ordID, sym, int64(workerID*1000+j), 4, 100.0, "BUY", 95.0, "LOW_VOLUME", 110.0, time.Now())

				// Concurrent snapshot reads (deep copies)
				positions := rm.GetOpenPositions()
				for _, p := range positions {
					_ = p.Quantity
					_ = p.SLPrice
					_ = p.LatestPrice
				}

				// Concurrent partial exit
				rm.RecordPartialExit(ordID, 110.0, 2)

				// Concurrent close
				rm.OnOrderClose(ordID, 112.0, 2)
			}
		}()
	}

	wg.Wait()
}

// TestRiskManager_PositionSLDetailsAndFillPriceUpdate verifies that fill price update and broker SL details persistence work properly
func TestRiskManager_PositionSLDetailsAndFillPriceUpdate(t *testing.T) {
	logger := zap.NewNop()
	rm := NewRiskManager(nil, logger, 100000.0, RiskLimits{MaxTradesPerDay: 10})

	orderID := "entry-101"
	rm.AddOpenPosition(orderID, "M&M", 12345, 10, 2800.0, "BUY", 2780.0, "LOW_VOLUME", 2820.0, time.Now())

	// 1. Verify initial entry price
	pos := rm.GetPosition(orderID)
	if pos == nil || pos.EntryPrice != 2800.0 {
		t.Fatalf("expected initial entry price 2800.0, got %v", pos)
	}

	// 2. Simulate broker fill price update (e.g. slight slippage from 2800.0 to 2801.50)
	rm.UpdatePositionEntryPrice(orderID, 2801.50)
	pos = rm.GetPosition(orderID)
	if pos.EntryPrice != 2801.50 {
		t.Fatalf("expected updated entry price 2801.50, got %f", pos.EntryPrice)
	}

	// 3. Place initial broker SL order
	rm.SetBrokerSLDetails(orderID, "sl-ord-1", 2780.0)
	pos = rm.GetPosition(orderID)
	if pos.BrokerSLOrderID != "sl-ord-1" || pos.LastPlacedSLPrice != 2780.0 {
		t.Fatalf("expected SL order sl-ord-1 and placed SL 2780.0, got %s / %f", pos.BrokerSLOrderID, pos.LastPlacedSLPrice)
	}

	// 4. Trigger Target 1 partial exit at 2820.0
	action := rm.CheckTrailingSL(orderID, 2820.0)
	if action != "PARTIAL_EXIT" {
		t.Fatalf("expected PARTIAL_EXIT, got %s", action)
	}

	// Verify that fresh position has SL moved to Cost (>= 2801.50)
	freshPos := rm.GetPosition(orderID)
	if freshPos.SLPrice < 2801.50 {
		t.Fatalf("expected SL to be moved to Cost >= 2801.50, got %f", freshPos.SLPrice)
	}

	// 5. Replace broker SL after partial exit
	rm.RecordPartialExit(orderID, 2820.0, 5)
	rm.SetBrokerSLDetails(orderID, "sl-ord-2", freshPos.SLPrice)

	updatedPos := rm.GetPosition(orderID)
	if updatedPos.Quantity != 5 {
		t.Fatalf("expected remaining quantity 5, got %d", updatedPos.Quantity)
	}
	if updatedPos.BrokerSLOrderID != "sl-ord-2" || updatedPos.LastPlacedSLPrice != freshPos.SLPrice {
		t.Fatalf("expected replacement SL order sl-ord-2 and placed SL %f, got %s / %f", freshPos.SLPrice, updatedPos.BrokerSLOrderID, updatedPos.LastPlacedSLPrice)
	}
}


