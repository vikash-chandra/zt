package selection

import (
	"math"
	"testing"
)

// TestEquityStrategiesLevelShiftCalculations tests all edge cases for reference boundary shift calculations
func TestEquityStrategiesLevelShiftCalculations(t *testing.T) {
	tests := []struct {
		name         string
		rawHigh      float64
		rawLow       float64
		shiftPct     float64
		tickSize     float64
		expectedHigh float64
		expectedLow  float64
	}{
		{
			name:         "PGEL Real Market Case (1.5% Buffer)",
			rawHigh:      513.85,
			rawLow:       476.00,
			shiftPct:     1.5,
			tickSize:     0.05,
			expectedHigh: 506.15, // 513.85 * (1 - 0.015) = 506.14225 -> rounded to 506.15
			expectedLow:  483.15, // 476.00 * (1 + 0.015) = 483.14 -> rounded to 483.15
		},
		{
			name:         "Negative Buffer Entry (-1.5%)",
			rawHigh:      513.85,
			rawLow:       476.00,
			shiftPct:     -1.5,
			tickSize:     0.05,
			expectedHigh: 506.15,
			expectedLow:  483.15,
		},
		{
			name:         "Zero Buffer (0.0% No Shift)",
			rawHigh:      513.85,
			rawLow:       476.00,
			shiftPct:     0.0,
			tickSize:     0.05,
			expectedHigh: 513.85,
			expectedLow:  476.00,
		},
		{
			name:         "Large Cap High Momentum Stock (TCS 3500.00 / 3400.00, 2.0% Buffer)",
			rawHigh:      3500.00,
			rawLow:       3400.00,
			shiftPct:     2.0,
			tickSize:     0.05,
			expectedHigh: 3430.00, // 3500 * 0.98 = 3430.00
			expectedLow:  3468.00, // 3400 * 1.02 = 3468.00
		},
		{
			name:         "Penny Stock (₹10.50 / ₹9.20, 1.0% Buffer)",
			rawHigh:      10.50,
			rawLow:       9.20,
			shiftPct:     1.0,
			tickSize:     0.05,
			expectedHigh: 10.40, // 10.50 * 0.99 = 10.395 -> 10.40
			expectedLow:  9.30,  // 9.20 * 1.01 = 9.292 -> 9.30
		},
		{
			name:         "Ultra-High Priced Stock (MRF 140000.00 / 138000.00, 0.5% Buffer)",
			rawHigh:      140000.00,
			rawLow:       138000.00,
			shiftPct:     0.5,
			tickSize:     0.05,
			expectedHigh: 139300.00, // 140000 * 0.995 = 139300.00
			expectedLow:  138690.00, // 138000 * 1.005 = 138690.00
		},
		{
			name:         "Zero High or Low Price Edge Cases",
			rawHigh:      0.0,
			rawLow:       0.0,
			shiftPct:     1.5,
			tickSize:     0.05,
			expectedHigh: 0.0,
			expectedLow:  0.0,
		},
		{
			name:         "Fallback Tick Size (Zero or Negative Tick)",
			rawHigh:      200.00,
			rawLow:       100.00,
			shiftPct:     2.0,
			tickSize:     0.0, // Should fallback to 0.05
			expectedHigh: 196.00,
			expectedLow:  102.00,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotHigh := CalculateLevelShiftedHigh(tt.rawHigh, tt.shiftPct, tt.tickSize)
			if math.Abs(gotHigh-tt.expectedHigh) > 0.001 {
				t.Errorf("CalculateLevelShiftedHigh() = %v, expected %v", gotHigh, tt.expectedHigh)
			}

			gotLow := CalculateLevelShiftedLow(tt.rawLow, tt.shiftPct, tt.tickSize)
			if math.Abs(gotLow-tt.expectedLow) > 0.001 {
				t.Errorf("CalculateLevelShiftedLow() = %v, expected %v", gotLow, tt.expectedLow)
			}
		})
	}
}

// TestMultiSelectorPriorityRankingWithLevelShift validates winner resolution and correct shift %
func TestMultiSelectorPriorityRankingWithLevelShift(t *testing.T) {
	configs := map[string]StockSelectionStrategyConfig{
		"RESULT": {
			Name:          "RESULT",
			DisplayName:   "Results",
			Enabled:       true,
			PriorityRank:  2,
			LevelShiftPct: 1.5,
			WatchlistSize: 10,
		},
		"FO": {
			Name:          "FO",
			DisplayName:   "F&O Momentum",
			Enabled:       true,
			PriorityRank:  7,
			LevelShiftPct: 0.0,
			WatchlistSize: 10,
		},
		"PDH_PDL": {
			Name:          "PDH_PDL",
			DisplayName:   "PDH-PDL Breakout",
			Enabled:       true,
			PriorityRank:  6,
			LevelShiftPct: 0.0,
			WatchlistSize: 10,
		},
	}

	// Case 1: Comma-separated selectors "MANUAL:RESULT,FO" -> RESULT must win with 1.5% shift
	candidates := []string{"MANUAL:RESULT", "FO"}
	winner, shift := ResolveWinningSelector("PGEL", candidates, configs)
	if winner != "RESULT" {
		t.Errorf("expected winner RESULT, got %s", winner)
	}
	if shift != 1.5 {
		t.Errorf("expected shift 1.5%%, got %f", shift)
	}

	// Case 2: Verify that applying shift to PGEL gives early breakout qualification
	rawPDH := 513.85
	rawPDL := 476.00
	shiftedHigh := CalculateLevelShiftedHigh(rawPDH, shift, 0.05)
	shiftedLow := CalculateLevelShiftedLow(rawPDL, shift, 0.05)

	if shiftedHigh >= rawPDH {
		t.Errorf("shiftedHigh (%f) should be lower than rawPDH (%f) for earlier BUY qualification", shiftedHigh, rawPDH)
	}
	if shiftedLow <= rawPDL {
		t.Errorf("shiftedLow (%f) should be higher than rawPDL (%f) for earlier SELL qualification", shiftedLow, rawPDL)
	}
}
