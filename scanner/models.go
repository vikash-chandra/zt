package scanner

import (
	"time"

	"zerodha-trading/data"
)

// BreakoutType represents the type of breakout or breakdown detected
type BreakoutType string

const (
	AllTimeHighBreak   BreakoutType = "ALL_TIME_HIGH_BREAK"
	YearlyHighBreak    BreakoutType = "YEARLY_HIGH_BREAK"
	MonthlyHighBreak   BreakoutType = "MONTHLY_HIGH_BREAK"
	WeeklyHighBreak    BreakoutType = "WEEKLY_HIGH_BREAK"
	DailyClusterBreak  BreakoutType = "DAILY_CLUSTER"
	WeeklyClusterBreak BreakoutType = "WEEKLY_CLUSTER"
	AllClusterBreak    BreakoutType = "ALL_CLUSTER"
	AllTimeLowBreak    BreakoutType = "ALL_TIME_LOW_BREAK"
	YearlyLowBreak     BreakoutType = "YEARLY_LOW_BREAK"
	MonthlyLowBreak    BreakoutType = "MONTHLY_LOW_BREAK"
	WeeklyLowBreak     BreakoutType = "WEEKLY_LOW_BREAK"
	NoBreakout         BreakoutType = "NONE"
)

// QuantDirection represents predicted next market session direction
type QuantDirection string

const (
	StrongBullish QuantDirection = "STRONG_BULLISH"
	Bullish       QuantDirection = "BULLISH"
	Neutral       QuantDirection = "NEUTRAL"
	Bearish       QuantDirection = "BEARISH"
	StrongBearish QuantDirection = "STRONG_BEARISH"
)

// NewsItem holds financial news headline and sentiment
type NewsItem struct {
	Title     string    `json:"title"`
	Link      string    `json:"link"`
	Source    string    `json:"source"`
	Published time.Time `json:"published"`
	Sentiment string    `json:"sentiment"` // "POSITIVE", "NEGATIVE", "NEUTRAL"
}

// ScanResult holds quant stock scanner output for a single stock
type ScanResult struct {
	ID                int            `json:"id"`
	Symbol            string         `json:"symbol"`
	Segment           string         `json:"segment"` // "F&O", "CASH", "INDEX", "COMMODITY"
	Token             int64          `json:"token"`
	BreakoutType      BreakoutType   `json:"breakout_type"`
	Direction         string         `json:"direction"` // "BULLISH" or "BEARISH"
	MomentumDays      int            `json:"momentum_days"`
	PctChange1D       float64        `json:"pct_change_1d"`
	PctChange3D       float64        `json:"pct_change_3d"`
	RangePctChange    float64        `json:"range_pct_change"`
	CurrentPrice      float64        `json:"current_price"`
	DistanceToHighPct float64        `json:"distance_to_high_pct"`
	YearlyHigh        float64        `json:"yearly_high"`
	YearlyLow         float64        `json:"yearly_low"`
	MonthlyHigh       float64        `json:"monthly_high"`
	MonthlyLow        float64        `json:"monthly_low"`
	WeeklyHigh        float64        `json:"weekly_high"`
	WeeklyLow         float64        `json:"weekly_low"`
	AllTimeHigh       float64        `json:"all_time_high"`
	AllTimeLow        float64        `json:"all_time_low"`
	IsDailyCluster    bool           `json:"is_daily_cluster"`
	IsWeeklyCluster   bool           `json:"is_weekly_cluster"`
	ClusterCenter     float64        `json:"cluster_center"`
	ClusterRadius     float64        `json:"cluster_radius"`
	ClusterSpread     float64        `json:"cluster_spread"`
	ConfidenceScore   float64        `json:"confidence_score"` // 0.0 to 100.0%
	QuantDirection    QuantDirection `json:"quant_direction"`
	RecommendedAct    string         `json:"recommended_action"`
	Volume1D          int64          `json:"volume_1d"`
	VolumeADV         int64          `json:"volume_adv"`
	VolumeMultiplier  float64        `json:"volume_multiplier"`
	DowTrend          string         `json:"dow_trend"`        // "UPTREND_HH_HL", "DOWNTREND_LH_LL", "SIDEWAYS_BASE"
	PositionalZone    string         `json:"positional_zone"`  // "PULLBACK_BUY", "BREAKOUT_BUY", "PULLBACK_SELL", "BREAKDOWN_SELL", "ACCUMULATION_BASE", "NEUTRAL"
	ActionTiming      string         `json:"action_timing"`    // "TODAY_ACTIONABLE", "NEXT_DAY_IMMINENT", "DEVELOPING"
	SelectionReason   string         `json:"selection_reason"` // Descriptive reason for stock selection
	SupportZone       float64        `json:"support_zone"`     // Key Demand / Support price level
	ResistanceZone    float64        `json:"resistance_zone"`  // Key Supply / Resistance price level
	NewsSummary       string         `json:"news_summary"`
	NewsSentiment     string         `json:"news_sentiment"`
	NewsItems         []NewsItem     `json:"news_items,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
}

// ToDBScanResult converts ScanResult to data.DBScanResult with proper date and IST timestamp
func (res ScanResult) ToDBScanResult(scanDate string, createdAt time.Time) data.DBScanResult {
	if scanDate == "" {
		scanDate = data.NowIST().Format("2006-01-02")
	}
	if createdAt.IsZero() {
		createdAt = data.NowIST()
	} else {
		createdAt = createdAt.In(data.ISTLocation)
	}
	return data.DBScanResult{
		ScanDate:          scanDate,
		Symbol:            res.Symbol,
		Segment:           res.Segment,
		BreakoutType:      string(res.BreakoutType),
		Direction:         res.Direction,
		MomentumDays:      res.MomentumDays,
		PctChange1D:       res.PctChange1D,
		PctChange3D:       res.PctChange3D,
		RangePctChange:    res.RangePctChange,
		CurrentPrice:      res.CurrentPrice,
		DistanceToHighPct: res.DistanceToHighPct,
		YearlyHigh:        res.YearlyHigh,
		YearlyLow:         res.YearlyLow,
		MonthlyHigh:       res.MonthlyHigh,
		MonthlyLow:        res.MonthlyLow,
		WeeklyHigh:        res.WeeklyHigh,
		WeeklyLow:         res.WeeklyLow,
		AllTimeHigh:       res.AllTimeHigh,
		AllTimeLow:        res.AllTimeLow,
		IsDailyCluster:    res.IsDailyCluster,
		IsWeeklyCluster:   res.IsWeeklyCluster,
		ClusterSpread:     res.ClusterSpread,
		ClusterCenter:     res.ClusterCenter,
		Volume1D:          res.Volume1D,
		VolumeADV:         res.VolumeADV,
		VolumeMultiplier:  res.VolumeMultiplier,
		DowTrend:          res.DowTrend,
		PositionalZone:    res.PositionalZone,
		ActionTiming:      res.ActionTiming,
		SelectionReason:   res.SelectionReason,
		SupportZone:       res.SupportZone,
		ResistanceZone:    res.ResistanceZone,
		ConfidenceScore:   res.ConfidenceScore,
		QuantDirection:    string(res.QuantDirection),
		RecommendedAction: res.RecommendedAct,
		NewsSummary:       res.NewsSummary,
		NewsSentiment:     res.NewsSentiment,
		CreatedAt:         createdAt,
	}
}
