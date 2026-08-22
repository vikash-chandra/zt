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
	profile := calc.CalculateProfile(100.0, "BUY", 105.0, 95.0, 20.0, 20000.0, 20.0, 2.0)

	if profile.Quantity != 1000 { // 20000 / 20 = 1000
		t.Errorf("expected Quantity 1000, got %d", profile.Quantity)
	}
	if profile.StopLoss != 94.0 {
		t.Errorf("expected StopLoss 94.0, got %f", profile.StopLoss)
	}
	if profile.Target1 != 112.0 {
		t.Errorf("expected Target1 112.0, got %f", profile.Target1)
	}

	// 2. SELL scenario with custom RiskRewardRatio (e.g. 3.0)
	// Entry: 100, SetupHigh: 105 (Risk: 5)
	// Buffer: 10% -> Risk = 5 * 1.1 = 5.5
	// SL = 100 + 5.5 = 105.5
	// Target1 (rrRatio 3.0) = 100 - (3.0 * 5.5) = 83.5
	profileShort := calc.CalculateProfile(100.0, "SELL", 105.0, 95.0, 10.0, 20000.0, 0.0, 3.0) // 0.0 marginPerShare fallback to 5x leverage -> margin = 20 -> Qty = 1000

	if profileShort.Quantity != 1000 {
		t.Errorf("expected Quantity 1000 on fallback, got %d", profileShort.Quantity)
	}
	if profileShort.StopLoss != 105.5 {
		t.Errorf("expected StopLoss 105.5, got %f", profileShort.StopLoss)
	}
	if profileShort.Target1 != 83.5 {
		t.Errorf("expected Target1 83.5, got %f", profileShort.Target1)
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
	profile := calc.CalculateProfile(100.0, "BUY", 0.0, 0.0, 0.0, 20000.0, 10.0, 2.5)

	if profile.Quantity != 2000 { // 20000 / 10 = 2000
		t.Errorf("expected Quantity 2000, got %d", profile.Quantity)
	}
	if math.Abs(profile.StopLoss-98.5) > 0.0001 {
		t.Errorf("expected StopLoss 98.5, got %f", profile.StopLoss)
	}
	if math.Abs(profile.Target1-103.75) > 0.0001 {
		t.Errorf("expected Target1 103.75, got %f", profile.Target1)
	}
}

func TestPartialBookCostSLStrategy(t *testing.T) {
	strat := NewPartialBookCostSLStrategy(DefaultPartialBookCostSLConfig())

	// Profile Calculation: Entry 100, SetupLow 95, Buffer 0%, RR 2.0 -> Risk 5 -> SL 95, Target 110
	profile := strat.CalculateProfile(100.0, "BUY", 105.0, 95.0, 0.0, 10000.0, 20.0, 2.0)
	if profile.StopLoss != 95.0 {
		t.Errorf("expected SL 95.0, got %f", profile.StopLoss)
	}
	if profile.Target1 != 110.0 {
		t.Errorf("expected Target1 110.0, got %f", profile.Target1)
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
