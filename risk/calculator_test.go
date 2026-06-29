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
