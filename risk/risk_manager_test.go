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

	// Price at 100.5 (+0.5% gain) -> No SL trail yet
	action := rm.CheckTrailingSL("order-buy", 100.5)
	if action != "" {
		t.Errorf("expected empty action at 100.5, got %s", action)
	}

	// Price at 100.8 (+0.8% gain) -> Stage 1 SL trail to 100.2 (+0.2% no-loss buffer)
	action = rm.CheckTrailingSL("order-buy", 100.8)
	if action != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED at 100.8, got %s", action)
	}
	if rm.openPositions["order-buy"].SLPrice != 100.2 {
		t.Errorf("expected SL to trail to 100.2, got %f", rm.openPositions["order-buy"].SLPrice)
	}

	// Price hits Target 1 (102.0) -> Trigger PARTIAL_EXIT and trail Stop-Loss to +1.0% (101.0)
	action = rm.CheckTrailingSL("order-buy", 102.0)
	if action != "PARTIAL_EXIT" {
		t.Errorf("expected PARTIAL_EXIT at 102.0, got %s", action)
	}

	pos := rm.openPositions["order-buy"]
	if !pos.IsPartialExitDone {
		t.Error("expected IsPartialExitDone to be true")
	}
	if pos.SLPrice != 101.0 {
		t.Errorf("expected Stop-Loss to trail to 101.0, got %f", pos.SLPrice)
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

	// Price at 99.5 (+0.5% gain) -> No SL trail yet
	action = rmSell.CheckTrailingSL("order-sell", 99.5)
	if action != "" {
		t.Errorf("expected empty action at 99.5, got %s", action)
	}

	// Price drops to Target 1 (98.0) -> Trigger PARTIAL_EXIT and trail Stop-Loss to 99.0 (+1.0% locked)
	action = rmSell.CheckTrailingSL("order-sell", 98.0)
	if action != "PARTIAL_EXIT" {
		t.Errorf("expected PARTIAL_EXIT at 98.0, got %s", action)
	}

	posSell := rmSell.openPositions["order-sell"]
	if !posSell.IsPartialExitDone {
		t.Error("expected IsPartialExitDone to be true for SELL")
	}
	if posSell.SLPrice != 99.0 {
		t.Errorf("expected Stop-Loss to trail to 99.0 for SELL, got %f", posSell.SLPrice)
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
