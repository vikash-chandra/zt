package scanner

import (
	"testing"
	"time"

	"zerodha-trading/data"
)

func TestAnalyzeSentiment(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected string
	}{
		{
			name:     "Positive headline with profit and surge",
			text:     "Tata Motors Q1 profit surges 45% with strong order growth",
			expected: "POSITIVE",
		},
		{
			name:     "Negative headline with loss and downgrade",
			text:     "Company reports heavy net loss following analyst downgrade",
			expected: "NEGATIVE",
		},
		{
			name:     "Neutral headline without financial keywords",
			text:     "Board meeting scheduled for next week",
			expected: "NEUTRAL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyzeSentiment(tt.text)
			if got != tt.expected {
				t.Errorf("analyzeSentiment(%q) = %v; want %v", tt.text, got, tt.expected)
			}
		})
	}
}

func TestComputeQuantDecision(t *testing.T) {
	tests := []struct {
		name          string
		breakout      BreakoutType
		direction     string
		pct3D         float64
		newsSentiment string
		expectedDir   QuantDirection
		minScore      float64
	}{
		{
			name:          "Monthly High Breakout + Strong Momentum + Positive News",
			breakout:      MonthlyHighBreak,
			direction:     "BULLISH",
			pct3D:         3.5,
			newsSentiment: "POSITIVE",
			expectedDir:   StrongBullish,
			minScore:      75.0,
		},
		{
			name:          "Monthly Low Breakdown + Strong Negative Momentum + Negative News",
			breakout:      MonthlyLowBreak,
			direction:     "BEARISH",
			pct3D:         -3.5,
			newsSentiment: "NEGATIVE",
			expectedDir:   StrongBearish,
			minScore:      0.0,
		},
		{
			name:          "No Breakout + Neutral Momentum",
			breakout:      NoBreakout,
			direction:     "NEUTRAL",
			pct3D:         0.0,
			newsSentiment: "NEUTRAL",
			expectedDir:   Neutral,
			minScore:      40.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, score, act := computeQuantDecision(tt.breakout, tt.direction, tt.pct3D, tt.newsSentiment)
			if dir != tt.expectedDir {
				t.Errorf("computeQuantDecision() dir = %v; want %v", dir, tt.expectedDir)
			}
			if score < tt.minScore && tt.expectedDir == StrongBullish {
				t.Errorf("computeQuantDecision() score = %v; expected >= %v", score, tt.minScore)
			}
			if act == "" {
				t.Errorf("computeQuantDecision() recommended action should not be empty")
			}
		})
	}
}

func TestGetHighLowAndPctChange(t *testing.T) {
	now := time.Now()
	candles := []data.Candle{
		{Open: 100, High: 105, Low: 95, Close: 100, Time: now.AddDate(0, 0, -4)},
		{Open: 100, High: 110, Low: 98, Close: 108, Time: now.AddDate(0, 0, -3)},
		{Open: 108, High: 115, Low: 106, Close: 112, Time: now.AddDate(0, 0, -2)},
		{Open: 112, High: 120, Low: 110, Close: 118, Time: now.AddDate(0, 0, -1)},
		{Open: 118, High: 125, Low: 116, Close: 122, Time: now},
	}

	high, low := getHighLow(candles[:4], 3)
	if high != 120 {
		t.Errorf("getHighLow high = %v; want 120", high)
	}
	if low != 98 {
		t.Errorf("getHighLow low = %v; want 98", low)
	}

	pct1D := calculatePctChange(candles, 1)
	if pct1D <= 0 {
		t.Errorf("calculatePctChange 1D = %v; expected positive gain", pct1D)
	}

	pct3D := calculatePctChange(candles, 3)
	if pct3D <= 0 {
		t.Errorf("calculatePctChange 3D = %v; expected positive gain", pct3D)
	}

	rangePct := calculateRangePctChange(candles, 3)
	if rangePct <= 0 {
		t.Errorf("calculateRangePctChange = %v; expected positive range %%", rangePct)
	}
}
