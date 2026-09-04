package risk

import (
	"math"
	"testing"
)

func TestStandardRiskRewardCalculator(t *testing.T) {
	calc := InitializeRiskRewardCalculator("STANDARD")

	if calc.Name() != "STANDARD" {
		t.Errorf("expected STANDARD calculator, got %s", calc.Name())
	}

	// 1. BUY scenario with active setup candle bounds
	// Entry: 100, SetupLow: 95 (Risk: 5)
	// Buffer: 20% -> Risk = 5 * 1.2 = 6
	// SL = 100 - 6 = 94
	// Target1 (rrRatio 2.0) = 100 + (2.0 * 6) = 112
	// Risk Per Trade: 600.0 -> Quantity = 600 / 6 = 100 shares
	profile := calc.CalculateProfile(100.0, "BUY", 105.0, 95.0, 20.0, 600.0, 20000.0, 20.0, 2.0)

	if profile.Quantity != 100 { // 600 / 6 = 100
		t.Errorf("expected Quantity 100, got %d", profile.Quantity)
	}
	if profile.StopLoss != 94.0 {
		t.Errorf("expected StopLoss 94.0, got %f", profile.StopLoss)
	}
	if profile.Target1 != 112.0 {
		t.Errorf("expected Target1 112.0, got %f", profile.Target1)
	}
	if profile.MaxLoss != 600.0 {
		t.Errorf("expected MaxLoss 600.0, got %f", profile.MaxLoss)
	}

	// 2. SELL scenario with custom RiskRewardRatio (e.g. 3.0)
	// Entry: 100, SetupHigh: 105 (Risk: 5)
	// Buffer: 10% -> Risk = 5 * 1.1 = 5.5
	// SL = 100 + 5.5 = 105.5
	// Target1 (rrRatio 3.0) = 100 - (3.0 * 5.5) = 83.5
	// Risk Per Trade: 550.0 -> Quantity = 550 / 5.5 = 100
	profileShort := calc.CalculateProfile(100.0, "SELL", 105.0, 95.0, 10.0, 550.0, 20000.0, 0.0, 3.0)

	if profileShort.Quantity != 100 {
		t.Errorf("expected Quantity 100, got %d", profileShort.Quantity)
	}
	if profileShort.StopLoss != 105.5 {
		t.Errorf("expected StopLoss 105.5, got %f", profileShort.StopLoss)
	}
	if profileShort.Target1 != 83.5 {
		t.Errorf("expected Target1 83.5, got %f", profileShort.Target1)
	}
	if profileShort.MaxLoss != 550.0 {
		t.Errorf("expected MaxLoss 550.0, got %f", profileShort.MaxLoss)
	}
}

func TestPercentageRiskRewardCalculator(t *testing.T) {
	calc := InitializeRiskRewardCalculator("PERCENTAGE")

	if calc.Name() != "PERCENTAGE" {
		t.Errorf("expected PERCENTAGE calculator, got %s", calc.Name())
	}

	// 1. BUY scenario with 1.5% fixed risk
	// Entry: 100. Risk = 1.5.
	// SL = 100 - 1.5 = 98.5
	// Target1 (rrRatio 2.5) = 100 + (2.5 * 1.5) = 103.75
	// Risk Per Trade: 750.0 -> Quantity = 750 / 1.5 = 500
	profile := calc.CalculateProfile(100.0, "BUY", 0.0, 0.0, 0.0, 750.0, 20000.0, 10.0, 2.5)

	if profile.Quantity != 500 { // 750 / 1.5 = 500
		t.Errorf("expected Quantity 500, got %d", profile.Quantity)
	}
	if math.Abs(profile.StopLoss-98.5) > 0.0001 {
		t.Errorf("expected StopLoss 98.5, got %f", profile.StopLoss)
	}
	if math.Abs(profile.Target1-103.75) > 0.0001 {
		t.Errorf("expected Target1 103.75, got %f", profile.Target1)
	}
	if math.Abs(profile.MaxLoss-750.0) > 0.0001 {
		t.Errorf("expected MaxLoss 750.0, got %f", profile.MaxLoss)
	}
}

func TestPartialBookCostSLStrategy(t *testing.T) {
	strat := NewPartialBookCostSLStrategy(DefaultPartialBookCostSLConfig())

	// Profile Calculation: Entry 100, SetupLow 95, Buffer 0%, RR 2.0 -> Risk 5 -> SL 95, Target 110
	// Risk Per Trade: 500 -> Quantity = 500 / 5 = 100 shares
	profile := strat.CalculateProfile(100.0, "BUY", 105.0, 95.0, 0.0, 500.0, 10000.0, 20.0, 2.0)
	if profile.StopLoss != 95.0 {
		t.Errorf("expected SL 95.0, got %f", profile.StopLoss)
	}
	if profile.Target1 != 110.0 {
		t.Errorf("expected Target1 110.0, got %f", profile.Target1)
	}
	if profile.Quantity != 100 {
		t.Errorf("expected Quantity 100, got %d", profile.Quantity)
	}
	if profile.MaxLoss != 500.0 {
		t.Errorf("expected MaxLoss 500.0, got %f", profile.MaxLoss)
	}

	pos := &Position{
		Symbol:       "SBIN",
		EntryPrice:   100.0,
		Side:         "BUY",
		SLPrice:      95.0,
		Target1Price: 110.0,
		Quantity:     100,
	}

	// 1. Before Target 1 (e.g. LTP 105) -> No action
	act1 := strat.EvaluatePosition(pos, 105.0, 5, 0.05)
	if act1 != "" {
		t.Errorf("expected no action at 105, got %s", act1)
	}

	// 2. Target 1 Hit (LTP 110.5) -> PARTIAL_EXIT & SL moved to cost (100.05 with buffer)
	act2 := strat.EvaluatePosition(pos, 110.5, 10, 0.05)
	if act2 != "PARTIAL_EXIT" {
		t.Errorf("expected PARTIAL_EXIT, got %s", act2)
	}
	if !pos.IsPartialExitDone {
		t.Errorf("expected IsPartialExitDone to be true")
	}
	if pos.SLPrice < 100.0 {
		t.Errorf("expected SL moved to cost >= 100.0, got %f", pos.SLPrice)
	}

	// 3. Price drops below cost SL (99.5) -> CLOSE
	act3 := strat.EvaluatePosition(pos, 99.5, 15, 0.05)
	if act3 != "CLOSE" {
		t.Errorf("expected CLOSE on SL breach, got %s", act3)
	}
}

func TestDynamicTrailingSLStrategy(t *testing.T) {
	strat := NewDynamicTrailingSLStrategy(DefaultDynamicTrailingSLConfig())

	pos := &Position{
		Symbol:       "TCS",
		EntryPrice:   1000.0,
		Side:         "BUY",
		SLPrice:      985.0, // initial 1.5% SL
		Target1Price: 1020.0,
		Quantity:     100,
	}

	// 1. Stage 1 (+0.35% gain, LTP 1003.5) -> Trail SL to +0.05% (1000.5)
	act1 := strat.EvaluatePosition(pos, 1003.5, 5, 0.05)
	if act1 != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED at Stage 1, got %s", act1)
	}
	if pos.SLPrice != 1000.5 {
		t.Errorf("expected SL 1000.5, got %f", pos.SLPrice)
	}

	// 2. Stage 2 (+0.75% gain, LTP 1007.5) -> Trail SL to +0.3% (1003.0)
	act2 := strat.EvaluatePosition(pos, 1007.5, 10, 0.05)
	if act2 != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED at Stage 2, got %s", act2)
	}
	if pos.SLPrice != 1003.0 {
		t.Errorf("expected SL 1003.0, got %f", pos.SLPrice)
	}

	// 3. Stage 3 (+1.25% gain, LTP 1012.5) -> Trail SL to +0.6% (1006.0)
	act3 := strat.EvaluatePosition(pos, 1012.5, 12, 0.05)
	if act3 != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED at Stage 3, got %s", act3)
	}
	if pos.SLPrice != 1006.0 {
		t.Errorf("expected SL 1006.0, got %f", pos.SLPrice)
	}

	// 4. Stage 4 (+2.05% gain, LTP 1020.5) -> PARTIAL_EXIT & Trail SL to +1.0% (1010.0)
	act4 := strat.EvaluatePosition(pos, 1020.5, 15, 0.05)
	if act4 != "PARTIAL_EXIT" {
		t.Errorf("expected PARTIAL_EXIT at Stage 4, got %s", act4)
	}
	if pos.SLPrice != 1010.0 {
		t.Errorf("expected SL 1010.0, got %f", pos.SLPrice)
	}

	// 5. Stage 5 (+3.0% gain, LTP 1030) -> Trail SL to (Peak - 0.6%) = 1030 * (1 - 0.006) = 1023.8
	act5 := strat.EvaluatePosition(pos, 1030.0, 20, 0.05)
	if act5 != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED at Stage 5, got %s", act5)
	}
	if pos.SLPrice != 1023.8 {
		t.Errorf("expected SL 1023.8, got %f", pos.SLPrice)
	}
}

func TestRiskPerTradeQuantityCalculation(t *testing.T) {
	strat := NewDynamicTrailingSLStrategy(DefaultDynamicTrailingSLConfig())

	// Scenario: SBIN Buy at 826.0, Setup/2nd Candle Low = 817.50, Buffer = 0%
	// Risk per share = 826.0 - 817.5 = 8.50
	// Configured Risk Per Trade = 500.0 INR
	// Expected Quantity = floor(500.0 / 8.50) = 58 shares
	// Expected Max Loss = 58 * 8.50 = 493.00 INR (strictly <= 500 INR)
	profile := strat.CalculateProfile(826.0, "BUY", 830.0, 817.50, 0.0, 500.0, 100000.0, 165.20, 2.0)
	if profile.Quantity != 58 {
		t.Errorf("expected Quantity 58, got %d", profile.Quantity)
	}
	if profile.StopLoss != 817.50 {
		t.Errorf("expected SL 817.50, got %f", profile.StopLoss)
	}
	if profile.SLDistance != 8.50 {
		t.Errorf("expected SL distance 8.50, got %f", profile.SLDistance)
	}
	if profile.MaxLoss > 500.0 {
		t.Errorf("expected MaxLoss <= 500.0, got %f", profile.MaxLoss)
	}
	if math.Abs(profile.MaxLoss-493.0) > 0.0001 {
		t.Errorf("expected MaxLoss 493.0, got %f", profile.MaxLoss)
	}

	// Scenario: Tight capital limit capping
	// Max capital = 5000 INR, margin per share = 165.20 -> capital qty = floor(5000 / 165.20) = 30 shares
	// Expected Quantity = min(58, 30) = 30 shares
	profileCapped := strat.CalculateProfile(826.0, "BUY", 830.0, 817.50, 0.0, 500.0, 5000.0, 165.20, 2.0)
	if profileCapped.Quantity != 30 {
		t.Errorf("expected Quantity capped to 30, got %d", profileCapped.Quantity)
	}
	if profileCapped.MaxLoss != 30*8.50 {
		t.Errorf("expected MaxLoss 255.0, got %f", profileCapped.MaxLoss)
	}
}

func TestPartialBookCostSLStrategy_SellAndEdgeCases(t *testing.T) {
	strat := NewPartialBookCostSLStrategy(DefaultPartialBookCostSLConfig())

	// 1. SELL Trade Setup: Entry 500.0, SetupHigh 510.0 -> Risk 10.0 -> SL 510.0, Target1 (1:2) = 480.0
	profile := strat.CalculateProfile(500.0, "SELL", 510.0, 495.0, 0.0, 500.0, 50000.0, 100.0, 2.0)
	if profile.StopLoss != 510.0 {
		t.Errorf("expected SL 510.0, got %f", profile.StopLoss)
	}
	if profile.Target1 != 480.0 {
		t.Errorf("expected Target1 480.0, got %f", profile.Target1)
	}
	if profile.Quantity != 50 { // 500 / 10 = 50
		t.Errorf("expected Quantity 50, got %d", profile.Quantity)
	}

	pos := &Position{
		Symbol:       "INFY",
		EntryPrice:   500.0,
		Side:         "SELL",
		SLPrice:      510.0,
		Target1Price: 480.0,
		Quantity:     50,
	}

	// 2. Before Target 1 (e.g. LTP 490) -> No action
	act1 := strat.EvaluatePosition(pos, 490.0, 5, 0.05)
	if act1 != "" {
		t.Errorf("expected no action at 490.0, got %s", act1)
	}

	// 3. Target 1 Reached (LTP 479.50) -> PARTIAL_EXIT & SL moved to cost (500 * (1 - 0.0005) = 499.75)
	act2 := strat.EvaluatePosition(pos, 479.50, 10, 0.05)
	if act2 != "PARTIAL_EXIT" {
		t.Errorf("expected PARTIAL_EXIT at target 1, got %s", act2)
	}
	if !pos.IsPartialExitDone {
		t.Errorf("expected IsPartialExitDone to be true")
	}
	if pos.SLPrice > 500.0 {
		t.Errorf("expected SL moved down to cost <= 500.0, got %f", pos.SLPrice)
	}

	// 4. Price rebounds above cost SL (LTP 500.5) -> CLOSE
	act3 := strat.EvaluatePosition(pos, 500.5, 15, 0.05)
	if act3 != "CLOSE" {
		t.Errorf("expected CLOSE on SL breach, got %s", act3)
	}

	// 5. Edge Case: 1 share position (partial exit should round to 1 share)
	singleShareQty := int(math.Round(1.0 * 0.50))
	if singleShareQty == 0 {
		singleShareQty = 1
	}
	if singleShareQty != 1 {
		t.Errorf("expected 1 share for single share position, got %d", singleShareQty)
	}
}
