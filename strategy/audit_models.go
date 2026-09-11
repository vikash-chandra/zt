package strategy

import (
	"zerodha-trading/data"
)

// StockDaySummary provides high-level summary metrics for a stock's trading session
type StockDaySummary struct {
	Open              float64 `json:"open"`
	High              float64 `json:"high"`
	Low               float64 `json:"low"`
	Close             float64 `json:"close"`
	RangePct          float64 `json:"range_pct"`
	PDH               float64 `json:"pdh"`
	PDL               float64 `json:"pdl"`
	PDClose           float64 `json:"pd_close"`
	YearlyHigh        float64 `json:"yearly_high"`
	YearlyLow         float64 `json:"yearly_low"`
	TradesTakenCount  int     `json:"trades_taken_count"`
	SetupsFormedCount int     `json:"setups_formed_count"`
	FinalStatus       string  `json:"final_status"` // "TRADE_TAKEN", "SETUP_INVALIDATED", "SETUP_EXPIRED", "NO_SETUP_FORMED", "ARMED_WAITING"
}

// StockAnalysisInsights provides actionable diagnostic and parameter recommendations
type StockAnalysisInsights struct {
	Summary                string   `json:"summary"`
	SLDistancePct          float64  `json:"sl_distance_pct"`
	GeometricQuality       string   `json:"geometric_quality"` // "EXCELLENT", "MODERATE", "POOR", "N/A"
	ImprovementSuggestions []string `json:"improvement_suggestions"`
}

// CandleDiagnosticItem represents diagnostic inspection of a single candle during a trading session
type CandleDiagnosticItem struct {
	Time             string                 `json:"time"`
	Open             float64                `json:"open"`
	High             float64                `json:"high"`
	Low              float64                `json:"low"`
	Close            float64                `json:"close"`
	Volume           int64                  `json:"volume"`
	Color            string                 `json:"color"`
	EMA10            float64                `json:"ema10"`
	EMA20            float64                `json:"ema20"`
	RangePct         float64                `json:"range_pct"`
	WickPct          float64                `json:"wick_pct"`
	Status           string                 `json:"status"` // "WARMUP", "REJECTED", "MASTER_CANDIDATE", "MASTER_ESTABLISHED", "CONFIRMATION_ARMED", "INSIDE_CANDLE", "BREAKOUT_TRIGGERED"
	Verdict          string                 `json:"verdict"`
	RejectionReasons []string               `json:"rejection_reasons"`
	Details          map[string]interface{} `json:"details,omitempty"`
}

// AppliedStrategyConfig captures the exact parameters applied to evaluate this stock
type AppliedStrategyConfig struct {
	StrategyName    string                 `json:"strategy_name"`
	CandleTimeframe string                 `json:"candle_timeframe"`
	TradeEndTime    string                 `json:"trade_end_time"`
	UseBrokerSL     bool                   `json:"use_broker_sl"`
	AttachedRR      string                 `json:"attached_rr"`
	Parameters      map[string]interface{} `json:"parameters"`
}

// StockAuditResponse is the structured payload returned by /api/strategy/stock-audit
type StockAuditResponse struct {
	Symbol              string                 `json:"symbol"`
	Date                string                 `json:"date"`
	SelectedStrategy    string                 `json:"selected_strategy"`
	ConfiguredTimeframe string                 `json:"configured_timeframe"`
	AppliedConfig       AppliedStrategyConfig  `json:"applied_config"`
	DaySummary          StockDaySummary        `json:"day_summary"`
	Events              []data.StrategyEvent   `json:"events"`
	CandleDiagnostics   []CandleDiagnosticItem `json:"candle_diagnostics"`
	AnalysisInsights    StockAnalysisInsights  `json:"analysis_insights"`
}
