package scanner

import (
	"time"
)

// BreakoutType represents the type of breakout or breakdown detected
type BreakoutType string

const (
	AllTimeHighBreak BreakoutType = "ALL_TIME_HIGH_BREAK"
	YearlyHighBreak  BreakoutType = "YEARLY_HIGH_BREAK"
	MonthlyHighBreak BreakoutType = "MONTHLY_HIGH_BREAK"
	WeeklyHighBreak  BreakoutType = "WEEKLY_HIGH_BREAK"
	AllTimeLowBreak  BreakoutType = "ALL_TIME_LOW_BREAK"
	YearlyLowBreak   BreakoutType = "YEARLY_LOW_BREAK"
	MonthlyLowBreak  BreakoutType = "MONTHLY_LOW_BREAK"
	WeeklyLowBreak   BreakoutType = "WEEKLY_LOW_BREAK"
	NoBreakout       BreakoutType = "NONE"
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
	ID               int            `json:"id"`
	Symbol           string         `json:"symbol"`
	Segment          string         `json:"segment"` // "F&O", "CASH", "INDEX", "COMMODITY"
	Token            int64          `json:"token"`
	BreakoutType     BreakoutType   `json:"breakout_type"`
	Direction        string         `json:"direction"` // "BULLISH" or "BEARISH"
	MomentumDays     int            `json:"momentum_days"`
	PctChange1D      float64        `json:"pct_change_1d"`
	PctChange3D      float64        `json:"pct_change_3d"`
	RangePctChange   float64        `json:"range_pct_change"`
	YearlyHigh       float64        `json:"yearly_high"`
	YearlyLow        float64        `json:"yearly_low"`
	MonthlyHigh      float64        `json:"monthly_high"`
	MonthlyLow       float64        `json:"monthly_low"`
	WeeklyHigh       float64        `json:"weekly_high"`
	WeeklyLow        float64        `json:"weekly_low"`
	AllTimeHigh      float64        `json:"all_time_high"`
	AllTimeLow       float64        `json:"all_time_low"`
	ConfidenceScore  float64        `json:"confidence_score"` // 0.0 to 100.0%
	QuantDirection   QuantDirection `json:"quant_direction"`
	RecommendedAct   string         `json:"recommended_action"`
	Volume1D         int64          `json:"volume_1d"`
	VolumeADV        int64          `json:"volume_adv"`
	VolumeMultiplier float64        `json:"volume_multiplier"`
	NewsSummary      string         `json:"news_summary"`
	NewsSentiment    string         `json:"news_sentiment"`
	NewsItems        []NewsItem     `json:"news_items,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
}
