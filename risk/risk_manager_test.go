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
		Target1Price:      120.0,
		Side:              "BUY",
		IsPartialExitDone: false,
		CreatedAt:         time.Now(),
	}

	// Price is below Target 1 -> No exit
	action := rm.CheckTrailingSL("order-buy", 110.0)
	if action != "" {
		t.Errorf("expected empty action at 110.0, got %s", action)
	}

	// Price hits Target 1 -> Trigger PARTIAL_EXIT and trail Stop-Loss to entry price (100.0)
	action = rm.CheckTrailingSL("order-buy", 120.0)
	if action != "PARTIAL_EXIT" {
		t.Errorf("expected PARTIAL_EXIT at 120.0, got %s", action)
	}

	pos := rm.openPositions["order-buy"]
	if !pos.IsPartialExitDone {
		t.Error("expected IsPartialExitDone to be true")
	}
	if pos.SLPrice != 100.0 {
		t.Errorf("expected Stop-Loss to trail to EntryPrice 100.0, got %f", pos.SLPrice)
	}

	// Record partial exit of 5 lots at 120.0
	rm.RecordPartialExit("order-buy", 120.0, 5)
	if pos.Quantity != 5 {
		t.Errorf("expected remaining quantity to be 5, got %d", pos.Quantity)
	}
	// P&L = (120 - 100) * 5 = +100
	if rm.dailyPnL != 100.0 {
		t.Errorf("expected daily P&L to be 100.0, got %f", rm.dailyPnL)
	}

	// Price is above new SL (100.0) -> Should NOT trigger close
	action = rm.CheckTrailingSL("order-buy", 105.0)
	if action != "" {
		t.Errorf("expected no action at 105.0, got %s", action)
	}

	// Price drops to entry price (100.0) -> Should trigger soft SL breach
	action = rm.CheckTrailingSL("order-buy", 100.0)
	if action != "CLOSE" {
		t.Errorf("expected CLOSE action at 100.0, got %s", action)
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
		Target1Price:      80.0,
		Side:              "SELL",
		IsPartialExitDone: false,
		CreatedAt:         time.Now(),
	}

	// Price is above Target 1 -> No exit
	action = rmSell.CheckTrailingSL("order-sell", 90.0)
	if action != "" {
		t.Errorf("expected empty action at 90.0, got %s", action)
	}

	// Price drops to Target 1 -> Trigger PARTIAL_EXIT and trail Stop-Loss to entry price (100.0)
	action = rmSell.CheckTrailingSL("order-sell", 80.0)
	if action != "PARTIAL_EXIT" {
		t.Errorf("expected PARTIAL_EXIT at 80.0, got %s", action)
	}

	posSell := rmSell.openPositions["order-sell"]
	if !posSell.IsPartialExitDone {
		t.Error("expected IsPartialExitDone to be true for SELL")
	}
	if posSell.SLPrice != 100.0 {
		t.Errorf("expected Stop-Loss to trail to EntryPrice 100.0 for SELL, got %f", posSell.SLPrice)
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
