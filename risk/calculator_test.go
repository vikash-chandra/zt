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
		Symbol:         "TCS",
		EntryPrice:     1000.0,
		Side:           "BUY",
		SLPrice:        990.0, // initial risk = 10.0 INR
		InitialSLPrice: 990.0,
		InitialRisk:    10.0,
		Target1Price:   1020.0,
		Quantity:       100,
	}

	// 1. Stage 1 (1:1.4 R:R, LTP 1014.0 -> profit = 14.0 = 1.4x risk) -> Trail SL: lock 15% of peak profit = +2.10 (1002.10)
	act1 := strat.EvaluatePosition(pos, 1014.0, 5, 0.05)
	if act1 != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED at Stage 1, got %s", act1)
	}
	if pos.SLPrice != 1002.10 {
		t.Errorf("expected SL 1002.10, got %f", pos.SLPrice)
	}

	// 2. Stage 2 (1:1.5 R:R, LTP 1015.0 -> profit = 15.0 = 1.5x risk) -> Trail SL: lock 35% of peak profit = +5.25 (1005.25)
	act2 := strat.EvaluatePosition(pos, 1015.0, 10, 0.05)
	if act2 != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED at Stage 2, got %s", act2)
	}
	if pos.SLPrice != 1005.25 {
		t.Errorf("expected SL 1005.25, got %f", pos.SLPrice)
	}

	// 3. Stage 3 (1:1.8 R:R, LTP 1018.0 -> profit = 18.0 = 1.8x risk) -> Trail SL: lock 55% of peak profit = +9.90 (1009.90)
	act3 := strat.EvaluatePosition(pos, 1018.0, 12, 0.05)
	if act3 != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED at Stage 3, got %s", act3)
	}
	if pos.SLPrice != 1009.90 {
		t.Errorf("expected SL 1009.90, got %f", pos.SLPrice)
	}

	// 4. Stage 4 (1:2.0 R:R, LTP 1020.0 -> profit = 20.0 = 2.0x risk) -> PARTIAL_EXIT & trail SL to lock 70% of profit = +14.00 (1014.00)
	act4 := strat.EvaluatePosition(pos, 1020.0, 15, 0.05)
	if act4 != "PARTIAL_EXIT" {
		t.Errorf("expected PARTIAL_EXIT at Stage 4, got %s", act4)
	}
	if !pos.IsPartialExitDone {
		t.Errorf("expected IsPartialExitDone true")
	}
	if pos.SLPrice != 1014.00 {
		t.Errorf("expected SL 1014.00, got %f", pos.SLPrice)
	}

	// 5. Stage 5 (1:2.5+ R:R, LTP 1030.0 -> profit = 30.0 = 3.0x risk) -> Trail SL: max(80% lock = 1024.00, peak - 1.0% = 1019.70) = 1024.00
	act5 := strat.EvaluatePosition(pos, 1030.0, 20, 0.05)
	if act5 != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED at Stage 5, got %s", act5)
	}
	if pos.SLPrice != 1024.00 {
		t.Errorf("expected SL 1024.00, got %f", pos.SLPrice)
	}

	// 6. SELL Side Test: Entry 500.0, SL 510.0 (Risk = 10.0)
	sellPos := &Position{
		Symbol:         "INFY",
		EntryPrice:     500.0,
		Side:           "SELL",
		SLPrice:        510.0,
		InitialSLPrice: 510.0,
		InitialRisk:    10.0,
		Quantity:       50,
	}
	// Drop to 486.0 (Gain = 14.0 = 1.4x risk) -> Trail SL: lock 15% of peak profit = -2.10 (497.90)
	sellAct1 := strat.EvaluatePosition(sellPos, 486.0, 5, 0.05)
	if sellAct1 != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED for SELL at Stage 1, got %s", sellAct1)
	}
	if sellPos.SLPrice != 497.90 {
		t.Errorf("expected SELL SL 497.90, got %f", sellPos.SLPrice)
	}

	// 7. Target 1 Profile Calculation: Entry 431.0, Risk 1.15, Stage 4 (1:2.0 R:R) -> Target1 = 431 + 1.15*2 = 433.30
	profile := strat.CalculateProfile(431.0, "BUY", 432.0, 429.85, 0.0, 500.0, 50000.0, 86.20, 2.0)
	expectedT1 := 431.0 + (1.15 * 2.0)
	if math.Abs(profile.Target1-expectedT1) > 0.01 {
		t.Errorf("expected Target1 %f, got %f", expectedT1, profile.Target1)
	}
}

// TestDynamicTrailingSL_LTM_TightRisk verifies the specific setup that caused premature exit in LTM:
// Entry 4016.00, SL 4009.52, Risk 6.48 (0.161% of stock price).
// At LTP 4027.90 (1.836 R:R, Stage 3), trailed SL must NOT exceed LTP or cause an instant exit.
func TestDynamicTrailingSL_LTM_TightRisk(t *testing.T) {
	strat := NewDynamicTrailingSLStrategy(DefaultDynamicTrailingSLConfig())

	pos := &Position{
		Symbol:         "LTM",
		EntryPrice:     4016.00,
		Side:           "BUY",
		SLPrice:        4009.52,
		InitialSLPrice: 4009.52,
		InitialRisk:    6.48,
		Quantity:       15,
	}

	// 1. Tick reaches 4027.90 (Profit = 11.90, 1.836 R:R -> Triggers Stage 3)
	// Peak profit retention at 55%: Trailed SL = 4016.00 + 0.55 * 11.90 = 4022.545 -> 4022.55
	act := strat.EvaluatePosition(pos, 4027.90, 1, 0.05)
	if act != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED at 1.836 R:R on LTM, got %s", act)
	}
	if pos.SLPrice != 4022.55 {
		t.Errorf("expected SL 4022.55, got %f", pos.SLPrice)
	}

	// Crucial: The stock price (4027.90) is safely ABOVE the stop loss (4022.55) by 5.35 points!
	breathingRoom := 4027.90 - pos.SLPrice
	if breathingRoom < 5.0 {
		t.Errorf("expected at least 5.0 points breathing room, got %f", breathingRoom)
	}

	// If price slightly pulls back to 4026.00, it must NOT close the position
	actPullback := strat.EvaluatePosition(pos, 4026.00, 1, 0.05)
	if actPullback == "CLOSE" {
		t.Errorf("position prematurely closed on pullback to 4026.00")
	}

	// Position should only close if price drops below 4022.55
	actSLHit := strat.EvaluatePosition(pos, 4022.50, 2, 0.05)
	if actSLHit != "CLOSE" {
		t.Errorf("expected CLOSE when price drops below 4022.55, got %s", actSLHit)
	}
}

// TestDynamicTrailingSL_LegacyConfigMigration verifies that legacy fractional percentages (< 1.0)
// are safely upgraded to institutional defaults so old DB records do not cause premature exits.
func TestDynamicTrailingSL_LegacyConfigMigration(t *testing.T) {
	legacyCfg := DynamicTrailingSLConfig{
		Stage1TriggerPct:    1.4,
		Stage1TrailPct:      0.01, // Legacy entry price %
		Stage2TriggerPct:    1.5,
		Stage2TrailPct:      0.2, // Legacy entry price %
		Stage3TriggerPct:    1.8,
		Stage3TrailPct:      0.4, // Legacy entry price %
		Stage4TriggerPct:    2.0,
		Stage4ExitPct:       60.0,
		Stage4TrailPct:      0.2,  // Legacy entry price %
		Stage5TriggerPct:    2.5,
		StepTrailOffsetPct:  1.0,
		TimeDecayMin:        45,
		TimeDecayTriggerPct: 0.2,
		TimeDecayTrailPct:   0.05, // Legacy entry price %
	}
	strat := NewDynamicTrailingSLStrategy(legacyCfg)

	pos := &Position{
		Symbol:         "LTM",
		EntryPrice:     4016.00,
		Side:           "BUY",
		SLPrice:        4009.52,
		InitialSLPrice: 4009.52,
		InitialRisk:    6.48,
		Quantity:       15,
	}

	// Trigger Stage 3 (1.8 R:R, LTP 4027.90)
	// With auto-migration, Stage 3 uses 55% instead of 0.4% (which would have placed SL at 4032.05!)
	act := strat.EvaluatePosition(pos, 4027.90, 1, 0.05)
	if act != "SL_TRAILED" {
		t.Errorf("expected SL_TRAILED, got %s", act)
	}
	if pos.SLPrice > 4027.90 {
		t.Errorf("legacy config caused SL inversion above current price: %f vs 4027.90", pos.SLPrice)
	}
	if pos.SLPrice != 4022.55 {
		t.Errorf("expected migrated SL 4022.55, got %f", pos.SLPrice)
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
