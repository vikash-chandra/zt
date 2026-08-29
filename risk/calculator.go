package risk

import (
	"math"
)

// RiskRewardProfile holds the calculated risk management properties for a trade setup
type RiskRewardProfile struct {
	StopLoss   float64
	Target1    float64
	Quantity   int
	MaxLoss    float64 // Maximum loss in INR if StopLoss is hit (Quantity * SL Distance)
	SLDistance float64 // Per-share risk distance |entryPrice - StopLoss|
}

// RiskRewardCalculator defines the interface for calculating SL, targets, and risk-based sizing
type RiskRewardCalculator interface {
	Name() string
	CalculateProfile(entryPrice float64, side string, setupHigh float64, setupLow float64, slBufferPct float64, riskPerTrade float64, maxCapital float64, marginPerShare float64, rrRatio float64) *RiskRewardProfile
}

// RiskRewardStrategy defines the modular risk-reward strategy interface
type RiskRewardStrategy interface {
	RiskRewardCalculator
	EvaluatePosition(pos *Position, currentPrice float64, holdTimeMin int, tickSize float64) string
}

// PartialBookCostSLConfig contains parameters for Strategy 1: Book 50% at 1:X & Move SL to Cost
type PartialBookCostSLConfig struct {
	RiskRewardRatio float64 `json:"risk_reward_ratio"`    // e.g. 2.0 (1:2)
	PartialExitPct  float64 `json:"partial_exit_qty_pct"` // e.g. 50.0%
	MoveSLToCost    bool    `json:"move_sl_to_cost"`      // e.g. true
	CostBufferPct   float64 `json:"cost_sl_buffer_pct"`   // e.g. 0.05% (covers transaction charges)
	InitialSLMode   string  `json:"initial_sl_mode"`      // "SETUP_BREAKOUT" or "PERCENTAGE"
	InitialSLPct    float64 `json:"fixed_sl_pct"`         // e.g. 1.5%
	SLBufferPct     float64 `json:"sl_buffer_pct"`        // e.g. 0.1%
}

// DefaultPartialBookCostSLConfig returns default parameters
func DefaultPartialBookCostSLConfig() PartialBookCostSLConfig {
	return PartialBookCostSLConfig{
		RiskRewardRatio: 2.0,
		PartialExitPct:  50.0,
		MoveSLToCost:    true,
		CostBufferPct:   0.05,
		InitialSLMode:   "SETUP_BREAKOUT",
		InitialSLPct:    1.5,
		SLBufferPct:     0.1,
	}
}

// PartialBookCostSLStrategy implements Strategy 1
type PartialBookCostSLStrategy struct {
	Cfg PartialBookCostSLConfig
}

// NewPartialBookCostSLStrategy creates an instance
func NewPartialBookCostSLStrategy(cfg PartialBookCostSLConfig) *PartialBookCostSLStrategy {
	return &PartialBookCostSLStrategy{Cfg: cfg}
}

func (s *PartialBookCostSLStrategy) Name() string {
	return "PARTIAL_BOOK_COST_SL"
}

func (s *PartialBookCostSLStrategy) CalculateProfile(entryPrice float64, side string, setupHigh float64, setupLow float64, slBufferPct float64, riskPerTrade float64, maxCapital float64, marginPerShare float64, rrRatio float64) *RiskRewardProfile {
	effectiveRatio := s.Cfg.RiskRewardRatio
	if rrRatio > 0 {
		effectiveRatio = rrRatio
	}
	if effectiveRatio <= 0 {
		effectiveRatio = 2.0
	}

	effectiveBuffer := slBufferPct
	if slBufferPct < 0 {
		effectiveBuffer = s.Cfg.SLBufferPct
	}

	var originalRisk float64
	if s.Cfg.InitialSLMode == "PERCENTAGE" {
		slPct := s.Cfg.InitialSLPct
		if slPct <= 0 {
			slPct = 1.5
		}
		originalRisk = entryPrice * (slPct / 100.0)
	} else {
		if side == "BUY" {
			if setupLow > 0 {
				originalRisk = math.Abs(entryPrice - setupLow)
			} else {
				originalRisk = entryPrice * 0.01
			}
		} else {
			if setupHigh > 0 {
				originalRisk = math.Abs(setupHigh - entryPrice)
			} else {
				originalRisk = entryPrice * 0.01
			}
		}
		multiplier := 1.0 + (effectiveBuffer / 100.0)
		originalRisk *= multiplier
	}

	if originalRisk <= 0 {
		originalRisk = entryPrice * 0.01
	}

	var sl, target1 float64
	if side == "BUY" {
		sl = entryPrice - originalRisk
		target1 = entryPrice + (effectiveRatio * originalRisk)
	} else {
		sl = entryPrice + originalRisk
		target1 = entryPrice - (effectiveRatio * originalRisk)
	}

	// Sizing based on Risk Per Trade:
	// Quantity = floor(RiskPerTrade / SL_Distance)
	// Capped by Max Capital / MarginPerShare if specified
	qty := 1
	if riskPerTrade > 0 && originalRisk > 0 {
		riskQty := int(math.Floor(riskPerTrade / originalRisk))
		if maxCapital > 0 && marginPerShare > 0 {
			capitalQty := int(math.Floor(maxCapital / marginPerShare))
			if capitalQty > 0 && capitalQty < riskQty {
				qty = capitalQty
			} else {
				qty = riskQty
			}
		} else {
			qty = riskQty
		}
	} else if maxCapital > 0 && marginPerShare > 0 {
		qty = int(math.Floor(maxCapital / marginPerShare))
	} else if marginPerShare > 0 {
		fallbackMargin := entryPrice / 5.0
		qty = int(math.Floor(maxCapital / fallbackMargin))
	}

	if qty <= 0 {
		qty = 1
	}

	slDistance := math.Abs(entryPrice - sl)
	maxLoss := float64(qty) * slDistance

	return &RiskRewardProfile{
		StopLoss:   sl,
		Target1:    target1,
		Quantity:   qty,
		MaxLoss:    maxLoss,
		SLDistance: slDistance,
	}
}

func (s *PartialBookCostSLStrategy) EvaluatePosition(pos *Position, currentPrice float64, holdTimeMin int, tickSize float64) string {
	oldSL := pos.SLPrice

	// Check Target 1 partial exit (50% at 1:X)
	if !pos.IsPartialExitDone && pos.Target1Price > 0 {
		if pos.Side == "BUY" && currentPrice >= pos.Target1Price {
			pos.IsPartialExitDone = true
			if s.Cfg.MoveSLToCost {
				costSL := RoundTick(pos.EntryPrice*(1.0+(s.Cfg.CostBufferPct/100.0)), tickSize)
				if costSL > pos.SLPrice {
					pos.SLPrice = costSL
				}
			}
			return "PARTIAL_EXIT"
		} else if pos.Side == "SELL" && currentPrice <= pos.Target1Price {
			pos.IsPartialExitDone = true
			if s.Cfg.MoveSLToCost {
				costSL := RoundTick(pos.EntryPrice*(1.0-(s.Cfg.CostBufferPct/100.0)), tickSize)
				if pos.SLPrice == 0 || costSL < pos.SLPrice {
					pos.SLPrice = costSL
				}
			}
			return "PARTIAL_EXIT"
		}
	}

	// Check Stop-Loss Breach (if no broker order)
	if pos.BrokerSLOrderID == "" {
		if pos.Side == "BUY" && currentPrice <= pos.SLPrice {
			return "CLOSE"
		}
		if pos.Side == "SELL" && currentPrice >= pos.SLPrice {
			return "CLOSE"
		}
	}

	if math.Abs(pos.SLPrice-oldSL) >= 0.04 {
		return "SL_TRAILED"
	}

	return ""
}

// DynamicTrailingSLConfig contains parameters for Strategy 2: Multi-Stage Trailing SL
type DynamicTrailingSLConfig struct {
	Stage1TriggerPct    float64 `json:"stage1_trigger_gain_pct"` // default 0.3%
	Stage1TrailPct      float64 `json:"stage1_trail_sl_pct"`     // default 0.05%
	Stage2TriggerPct    float64 `json:"stage2_trigger_gain_pct"` // default 0.7%
	Stage2TrailPct      float64 `json:"stage2_trail_sl_pct"`     // default 0.3%
	Stage3TriggerPct    float64 `json:"stage3_trigger_gain_pct"` // default 1.2%
	Stage3TrailPct      float64 `json:"stage3_trail_sl_pct"`     // default 0.6%
	Stage4TriggerPct    float64 `json:"stage4_trigger_gain_pct"` // default 2.0%
	Stage4ExitPct       float64 `json:"stage4_exit_pct"`         // default 60.0%
	Stage4TrailPct      float64 `json:"stage4_trail_sl_pct"`     // default 1.0%
	Stage5TriggerPct    float64 `json:"stage5_trigger_gain_pct"` // default 2.5%
	StepTrailOffsetPct  float64 `json:"stage5_step_offset_pct"`  // default 0.6%
	TimeDecayMin        int     `json:"time_decay_min"`          // default 45 min
	TimeDecayTriggerPct float64 `json:"time_decay_trigger_pct"`  // default 0.2%
	TimeDecayTrailPct   float64 `json:"time_decay_trail_sl_pct"` // default 0.05%
	InitialSLMode       string  `json:"initial_sl_mode"`         // "SETUP_BREAKOUT" or "PERCENTAGE"
	InitialSLPct        float64 `json:"fixed_sl_pct"`            // default 1.5%
	SLBufferPct         float64 `json:"sl_buffer_pct"`           // default 0.1%
}

// DefaultDynamicTrailingSLConfig returns default parameters
func DefaultDynamicTrailingSLConfig() DynamicTrailingSLConfig {
	return DynamicTrailingSLConfig{
		Stage1TriggerPct:    0.3,
		Stage1TrailPct:      0.05,
		Stage2TriggerPct:    0.7,
		Stage2TrailPct:      0.3,
		Stage3TriggerPct:    1.2,
		Stage3TrailPct:      0.6,
		Stage4TriggerPct:    2.0,
		Stage4ExitPct:       60.0,
		Stage4TrailPct:      1.0,
		Stage5TriggerPct:    2.5,
		StepTrailOffsetPct:  0.6,
		TimeDecayMin:        45,
		TimeDecayTriggerPct: 0.2,
		TimeDecayTrailPct:   0.05,
		InitialSLMode:       "SETUP_BREAKOUT",
		InitialSLPct:        1.5,
		SLBufferPct:         0.1,
	}
}

// DynamicTrailingSLStrategy implements Strategy 2
type DynamicTrailingSLStrategy struct {
	Cfg DynamicTrailingSLConfig
}

// NewDynamicTrailingSLStrategy creates an instance
func NewDynamicTrailingSLStrategy(cfg DynamicTrailingSLConfig) *DynamicTrailingSLStrategy {
	return &DynamicTrailingSLStrategy{Cfg: cfg}
}

func (s *DynamicTrailingSLStrategy) Name() string {
	return "DYNAMIC_TRAILING_SL"
}

func (s *DynamicTrailingSLStrategy) CalculateProfile(entryPrice float64, side string, setupHigh float64, setupLow float64, slBufferPct float64, riskPerTrade float64, maxCapital float64, marginPerShare float64, rrRatio float64) *RiskRewardProfile {
	effectiveBuffer := slBufferPct
	if slBufferPct < 0 {
		effectiveBuffer = s.Cfg.SLBufferPct
	}

	var originalRisk float64
	if s.Cfg.InitialSLMode == "PERCENTAGE" {
		slPct := s.Cfg.InitialSLPct
		if slPct <= 0 {
			slPct = 1.5
		}
		originalRisk = entryPrice * (slPct / 100.0)
	} else {
		if side == "BUY" {
			if setupLow > 0 {
				originalRisk = math.Abs(entryPrice - setupLow)
			} else {
				originalRisk = entryPrice * 0.01
			}
		} else {
			if setupHigh > 0 {
				originalRisk = math.Abs(setupHigh - entryPrice)
			} else {
				originalRisk = entryPrice * 0.01
			}
		}
		multiplier := 1.0 + (effectiveBuffer / 100.0)
		originalRisk *= multiplier
	}

	if originalRisk <= 0 {
		originalRisk = entryPrice * 0.01
	}

	target1GainPct := s.Cfg.Stage4TriggerPct
	if target1GainPct <= 0 {
		target1GainPct = 2.0
	}

	var sl, target1 float64
	if side == "BUY" {
		sl = entryPrice - originalRisk
		target1 = entryPrice * (1.0 + target1GainPct/100.0)
	} else {
		sl = entryPrice + originalRisk
		target1 = entryPrice * (1.0 - target1GainPct/100.0)
	}

	// Sizing based on Risk Per Trade:
	// Quantity = floor(RiskPerTrade / SL_Distance)
	// Capped by Max Capital / MarginPerShare if specified
	qty := 1
	if riskPerTrade > 0 && originalRisk > 0 {
		riskQty := int(math.Floor(riskPerTrade / originalRisk))
		if maxCapital > 0 && marginPerShare > 0 {
			capitalQty := int(math.Floor(maxCapital / marginPerShare))
			if capitalQty > 0 && capitalQty < riskQty {
				qty = capitalQty
			} else {
				qty = riskQty
			}
		} else {
			qty = riskQty
		}
	} else if maxCapital > 0 && marginPerShare > 0 {
		qty = int(math.Floor(maxCapital / marginPerShare))
	} else if marginPerShare > 0 {
		fallbackMargin := entryPrice / 5.0
		qty = int(math.Floor(maxCapital / fallbackMargin))
	}

	if qty <= 0 {
		qty = 1
	}

	slDistance := math.Abs(entryPrice - sl)
	maxLoss := float64(qty) * slDistance

	return &RiskRewardProfile{
		StopLoss:   sl,
		Target1:    target1,
		Quantity:   qty,
		MaxLoss:    maxLoss,
		SLDistance: slDistance,
	}
}

func (s *DynamicTrailingSLStrategy) EvaluatePosition(pos *Position, currentPrice float64, holdTimeMin int, tickSize float64) string {
	if pos.Side == "BUY" {
		if currentPrice > pos.HighestPrice || pos.HighestPrice == 0 {
			pos.HighestPrice = currentPrice
		}
	} else {
		if currentPrice < pos.HighestPrice || pos.HighestPrice == 0 {
			pos.HighestPrice = currentPrice
		}
	}

	oldSL := pos.SLPrice

	if pos.Side == "BUY" {
		gainPct := math.Round(((pos.HighestPrice-pos.EntryPrice)/pos.EntryPrice)*100000) / 1000.0 // as percentage e.g. 0.35%

		if gainPct >= s.Cfg.Stage5TriggerPct {
			trailedSL := RoundTick(pos.HighestPrice*(1.0-(s.Cfg.StepTrailOffsetPct/100.0)), tickSize)
			if trailedSL > pos.SLPrice+0.01 {
				pos.SLPrice = trailedSL
			}
		} else if gainPct >= s.Cfg.Stage4TriggerPct && !pos.IsPartialExitDone {
			pos.IsPartialExitDone = true
			pos.SLPrice = RoundTick(pos.EntryPrice*(1.0+(s.Cfg.Stage4TrailPct/100.0)), tickSize)
			return "PARTIAL_EXIT"
		} else if gainPct >= s.Cfg.Stage3TriggerPct {
			trailedSL := RoundTick(pos.EntryPrice*(1.0+(s.Cfg.Stage3TrailPct/100.0)), tickSize)
			if trailedSL > pos.SLPrice+0.01 {
				pos.SLPrice = trailedSL
			}
		} else if gainPct >= s.Cfg.Stage2TriggerPct {
			trailedSL := RoundTick(pos.EntryPrice*(1.0+(s.Cfg.Stage2TrailPct/100.0)), tickSize)
			if trailedSL > pos.SLPrice+0.01 {
				pos.SLPrice = trailedSL
			}
		} else if gainPct >= s.Cfg.Stage1TriggerPct {
			trailedSL := RoundTick(pos.EntryPrice*(1.0+(s.Cfg.Stage1TrailPct/100.0)), tickSize)
			if trailedSL > pos.SLPrice+0.01 {
				pos.SLPrice = trailedSL
			}
		}

		if holdTimeMin > s.Cfg.TimeDecayMin && gainPct >= s.Cfg.TimeDecayTriggerPct {
			trailedSL := RoundTick(pos.EntryPrice*(1.0+(s.Cfg.TimeDecayTrailPct/100.0)), tickSize)
			if trailedSL > pos.SLPrice+0.01 {
				pos.SLPrice = trailedSL
			}
		}
	} else {
		gainPct := math.Round(((pos.EntryPrice-pos.HighestPrice)/pos.EntryPrice)*100000) / 1000.0

		if gainPct >= s.Cfg.Stage5TriggerPct {
			trailedSL := RoundTick(pos.HighestPrice*(1.0+(s.Cfg.StepTrailOffsetPct/100.0)), tickSize)
			if pos.SLPrice == 0 || trailedSL < pos.SLPrice-0.01 {
				pos.SLPrice = trailedSL
			}
		} else if gainPct >= s.Cfg.Stage4TriggerPct && !pos.IsPartialExitDone {
			pos.IsPartialExitDone = true
			pos.SLPrice = RoundTick(pos.EntryPrice*(1.0-(s.Cfg.Stage4TrailPct/100.0)), tickSize)
			return "PARTIAL_EXIT"
		} else if gainPct >= s.Cfg.Stage3TriggerPct {
			trailedSL := RoundTick(pos.EntryPrice*(1.0-(s.Cfg.Stage3TrailPct/100.0)), tickSize)
			if pos.SLPrice == 0 || trailedSL < pos.SLPrice-0.01 {
				pos.SLPrice = trailedSL
			}
		} else if gainPct >= s.Cfg.Stage2TriggerPct {
			trailedSL := RoundTick(pos.EntryPrice*(1.0-(s.Cfg.Stage2TrailPct/100.0)), tickSize)
			if pos.SLPrice == 0 || trailedSL < pos.SLPrice-0.01 {
				pos.SLPrice = trailedSL
			}
		} else if gainPct >= s.Cfg.Stage1TriggerPct {
			trailedSL := RoundTick(pos.EntryPrice*(1.0-(s.Cfg.Stage1TrailPct/100.0)), tickSize)
			if pos.SLPrice == 0 || trailedSL < pos.SLPrice-0.01 {
				pos.SLPrice = trailedSL
			}
		}

		if holdTimeMin > s.Cfg.TimeDecayMin && gainPct >= s.Cfg.TimeDecayTriggerPct {
			trailedSL := RoundTick(pos.EntryPrice*(1.0-(s.Cfg.TimeDecayTrailPct/100.0)), tickSize)
			if pos.SLPrice == 0 || trailedSL < pos.SLPrice-0.01 {
				pos.SLPrice = trailedSL
			}
		}
	}

	if pos.BrokerSLOrderID == "" {
		if pos.Side == "BUY" && currentPrice <= pos.SLPrice {
			return "CLOSE"
		}
		if pos.Side == "SELL" && currentPrice >= pos.SLPrice {
			return "CLOSE"
		}
	}

	if math.Abs(pos.SLPrice-oldSL) >= 0.04 {
		return "SL_TRAILED"
	}

	return ""
}

// StandardRiskRewardCalculator provides backward-compatibility for legacy standard setups
type StandardRiskRewardCalculator struct {
	strategy *PartialBookCostSLStrategy
}

func NewStandardRiskRewardCalculator() *StandardRiskRewardCalculator {
	return &StandardRiskRewardCalculator{
		strategy: NewPartialBookCostSLStrategy(DefaultPartialBookCostSLConfig()),
	}
}

func (c *StandardRiskRewardCalculator) Name() string {
	return "STANDARD"
}

func (c *StandardRiskRewardCalculator) CalculateProfile(entryPrice float64, side string, setupHigh float64, setupLow float64, slBufferPct float64, riskPerTrade float64, maxCapital float64, marginPerShare float64, rrRatio float64) *RiskRewardProfile {
	return c.strategy.CalculateProfile(entryPrice, side, setupHigh, setupLow, slBufferPct, riskPerTrade, maxCapital, marginPerShare, rrRatio)
}

func (c *StandardRiskRewardCalculator) EvaluatePosition(pos *Position, currentPrice float64, holdTimeMin int, tickSize float64) string {
	return c.strategy.EvaluatePosition(pos, currentPrice, holdTimeMin, tickSize)
}

// PercentageRiskRewardCalculator provides backward-compatibility for percentage-based setups
type PercentageRiskRewardCalculator struct {
	strategy *PartialBookCostSLStrategy
}

func NewPercentageRiskRewardCalculator() *PercentageRiskRewardCalculator {
	cfg := DefaultPartialBookCostSLConfig()
	cfg.InitialSLMode = "PERCENTAGE"
	cfg.InitialSLPct = 1.5
	return &PercentageRiskRewardCalculator{
		strategy: NewPartialBookCostSLStrategy(cfg),
	}
}

func (c *PercentageRiskRewardCalculator) Name() string {
	return "PERCENTAGE"
}

func (c *PercentageRiskRewardCalculator) CalculateProfile(entryPrice float64, side string, setupHigh float64, setupLow float64, slBufferPct float64, riskPerTrade float64, maxCapital float64, marginPerShare float64, rrRatio float64) *RiskRewardProfile {
	return c.strategy.CalculateProfile(entryPrice, side, setupHigh, setupLow, slBufferPct, riskPerTrade, maxCapital, marginPerShare, rrRatio)
}

func (c *PercentageRiskRewardCalculator) EvaluatePosition(pos *Position, currentPrice float64, holdTimeMin int, tickSize float64) string {
	return c.strategy.EvaluatePosition(pos, currentPrice, holdTimeMin, tickSize)
}

// InitializeRiskRewardCalculator instantiates the appropriate calculator/strategy
func InitializeRiskRewardCalculator(name string) RiskRewardCalculator {
	switch name {
	case "PARTIAL_BOOK_COST_SL":
		return NewPartialBookCostSLStrategy(DefaultPartialBookCostSLConfig())
	case "DYNAMIC_TRAILING_SL":
		return NewDynamicTrailingSLStrategy(DefaultDynamicTrailingSLConfig())
	case "PERCENTAGE":
		return NewPercentageRiskRewardCalculator()
	case "STANDARD":
		fallthrough
	default:
		return NewStandardRiskRewardCalculator()
	}
}

// InitializeRiskRewardStrategy instantiates the RiskRewardStrategy
func InitializeRiskRewardStrategy(name string) RiskRewardStrategy {
	switch name {
	case "PARTIAL_BOOK_COST_SL":
		return NewPartialBookCostSLStrategy(DefaultPartialBookCostSLConfig())
	case "DYNAMIC_TRAILING_SL":
		return NewDynamicTrailingSLStrategy(DefaultDynamicTrailingSLConfig())
	case "PERCENTAGE":
		return NewPercentageRiskRewardCalculator()
	case "STANDARD":
		fallthrough
	default:
		return NewStandardRiskRewardCalculator()
	}
}
