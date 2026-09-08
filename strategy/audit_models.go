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

// StockAuditResponse is the structured payload returned by /api/strategy/stock-audit
type StockAuditResponse struct {
	Symbol           string                `json:"symbol"`
	Date             string                `json:"date"`
	SelectedStrategy string                `json:"selected_strategy"`
	DaySummary       StockDaySummary       `json:"day_summary"`
	Events           []data.StrategyEvent  `json:"events"`
	AnalysisInsights StockAnalysisInsights `json:"analysis_insights"`
}
