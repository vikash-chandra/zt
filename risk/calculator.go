package risk

import (
	"math"
)

// RiskRewardProfile holds the calculated risk management properties for a trade setup
type RiskRewardProfile struct {
	StopLoss float64
	Target1  float64
	Quantity int
}

// RiskRewardCalculator defines the interface for calculating SL, targets, and sizing
type RiskRewardCalculator interface {
	Name() string
	CalculateProfile(entryPrice float64, side string, setupHigh float64, setupLow float64, slBufferPct float64, maxCapital float64, marginPerShare float64, rrRatio float64) *RiskRewardProfile
}

// StandardRiskRewardCalculator implements setup-candle breakout based calculations
type StandardRiskRewardCalculator struct{}

// NewStandardRiskRewardCalculator creates a new StandardRiskRewardCalculator instance
func NewStandardRiskRewardCalculator() *StandardRiskRewardCalculator {
	return &StandardRiskRewardCalculator{}
}

// Name returns calculator identity name
func (c *StandardRiskRewardCalculator) Name() string {
	return "STANDARD"
}

// CalculateProfile calculates stop-loss and targets based on Setup candle bounds with volatility buffer
func (c *StandardRiskRewardCalculator) CalculateProfile(entryPrice float64, side string, setupHigh float64, setupLow float64, slBufferPct float64, maxCapital float64, marginPerShare float64, rrRatio float64) *RiskRewardProfile {
	// 1. Calculate Quantity sizing
	qty := 1
	if marginPerShare > 0 {
		qty = int(math.Floor(maxCapital / marginPerShare))
	} else {
		// Fallback to 5x leverage if live margins API is offline
		fallbackMargin := entryPrice / 5.0
		qty = int(math.Floor(maxCapital / fallbackMargin))
	}
	if qty <= 0 {
		qty = 1
	}

	// 2. Calculate initial risk boundaries based on Setup Candle high/low
	var originalRisk float64
	if side == "BUY" {
		if setupLow > 0 {
			originalRisk = math.Abs(entryPrice - setupLow)
		} else {
			originalRisk = entryPrice * 0.01 // Fallback 1% risk
		}
	} else {
		if setupHigh > 0 {
			originalRisk = math.Abs(setupHigh - entryPrice)
		} else {
			originalRisk = entryPrice * 0.01 // Fallback 1% risk
		}
	}

	multiplier := 1.0 + (slBufferPct / 100.0)
	bufferedRisk := multiplier * originalRisk

	var sl, target1 float64
	if side == "BUY" {
		sl = entryPrice - bufferedRisk
		target1 = entryPrice + (rrRatio * bufferedRisk)
	} else {
		sl = entryPrice + bufferedRisk
		target1 = entryPrice - (rrRatio * bufferedRisk)
	}

	return &RiskRewardProfile{
		StopLoss: sl,
		Target1:  target1,
		Quantity: qty,
	}
}

// PercentageRiskRewardCalculator implements a fixed percentage risk setup
type PercentageRiskRewardCalculator struct{}

// NewPercentageRiskRewardCalculator creates a new PercentageRiskRewardCalculator instance
func NewPercentageRiskRewardCalculator() *PercentageRiskRewardCalculator {
	return &PercentageRiskRewardCalculator{}
}

// Name returns calculator identity name
func (c *PercentageRiskRewardCalculator) Name() string {
	return "PERCENTAGE"
}

// CalculateProfile calculates stop-loss and targets based on a fixed 1.5% entry price percentage
func (c *PercentageRiskRewardCalculator) CalculateProfile(entryPrice float64, side string, setupHigh float64, setupLow float64, slBufferPct float64, maxCapital float64, marginPerShare float64, rrRatio float64) *RiskRewardProfile {
	// 1. Calculate Quantity sizing
	qty := 1
	if marginPerShare > 0 {
		qty = int(math.Floor(maxCapital / marginPerShare))
	} else {
		fallbackMargin := entryPrice / 5.0
		qty = int(math.Floor(maxCapital / fallbackMargin))
	}
	if qty <= 0 {
		qty = 1
	}

	// 2. Fixed 1.5% risk profile
	riskAmt := entryPrice * 0.015

	var sl, target1 float64
	if side == "BUY" {
		sl = entryPrice - riskAmt
		target1 = entryPrice + (rrRatio * riskAmt)
	} else {
		sl = entryPrice + riskAmt
		target1 = entryPrice - (rrRatio * riskAmt)
	}

	return &RiskRewardProfile{
		StopLoss: sl,
		Target1:  target1,
		Quantity: qty,
	}
}

// InitializeRiskRewardCalculator initializes the chosen calculator by configuration name
func InitializeRiskRewardCalculator(name string) RiskRewardCalculator {
	switch name {
	case "PERCENTAGE":
		return NewPercentageRiskRewardCalculator()
	case "STANDARD":
		fallthrough
	default:
		return NewStandardRiskRewardCalculator()
	}
}
