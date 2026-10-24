package scanner

import (
	"context"
	"testing"
	"time"

	"zerodha-trading/data"
	"zerodha-trading/monitoring"
)

func TestDetermineQuadrant(t *testing.T) {
	tests := []struct {
		name       string
		pctChange  float64
		multiplier float64
		threshold  float64
		expected   string
	}{
		{
			name:       "Strong Accumulation Breakout (3.5x vol, +3.2%)",
			pctChange:  3.2,
			multiplier: 3.5,
			threshold:  2.0,
			expected:   QuadrantAccumulationBreakout,
		},
		{
			name:       "Strong Distribution Breakdown (2.8x vol, -2.5%)",
			pctChange:  -2.5,
			multiplier: 2.8,
			threshold:  2.0,
			expected:   QuadrantDistributionBreakdown,
		},
		{
			name:       "Churn Absorption (4.0x vol, +0.2% flat move)",
			pctChange:  0.2,
			multiplier: 4.0,
			threshold:  2.0,
			expected:   QuadrantChurnAbsorption,
		},
		{
			name:       "Exhaustion Trap (0.5x low vol, +2.8% price move)",
			pctChange:  2.8,
			multiplier: 0.5,
			threshold:  2.0,
			expected:   QuadrantExhaustionTrap,
		},
		{
			name:       "Normal Trading (1.2x vol, +0.8% move)",
			pctChange:  0.8,
			multiplier: 1.2,
			threshold:  2.0,
			expected:   QuadrantNormal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := DetermineQuadrant(tt.pctChange, tt.multiplier, tt.threshold)
			if res != tt.expected {
				t.Errorf("expected quadrant %s, got %s", tt.expected, res)
			}
		})
	}
}

func TestCalculateProjectedRunRate(t *testing.T) {
	// Simulate 10:15 AM IST (60 minutes into market session)
	now1015 := time.Date(2026, 10, 24, 10, 15, 0, 0, data.ISTLocation)
	currentVol := int64(600000)

	projected, fraction := CalculateProjectedRunRate(currentVol, now1015)
	if fraction <= 0.15 || fraction >= 0.17 {
		t.Errorf("expected session fraction around 0.16 (60/375), got %f", fraction)
	}
	if projected < 3500000 || projected > 4000000 {
		t.Errorf("expected projected volume ~3.75M, got %d", projected)
	}

	// Simulate post-market (16:00 IST)
	now1600 := time.Date(2026, 10, 24, 16, 0, 0, 0, data.ISTLocation)
	projEOD, fracEOD := CalculateProjectedRunRate(currentVol, now1600)
	if fracEOD != 1.0 {
		t.Errorf("expected post-market fraction 1.0, got %f", fracEOD)
	}
	if projEOD != currentVol {
		t.Errorf("expected projected volume to equal current volume post-market, got %d", projEOD)
	}
}

func TestVolumeSurgeScannerEvaluateAndBatch(t *testing.T) {
	logger, _ := monitoring.NewLogger("info")
	vss := NewVolumeSurgeScanner(nil, logger.Logger)

	// Manually inject baselines
	vss.mu.Lock()
	vss.baselines["RELIANCE"] = data.DailyVolumeBaseline{
		Symbol:     "RELIANCE",
		AvgVol10D:  6000000,
		AvgVol20D:  5000000,
		AvgVol30D:  4800000,
		AvgVol50D:  4500000,
		ClosePrice: 1400.00,
	}
	vss.baselines["TCS"] = data.DailyVolumeBaseline{
		Symbol:     "TCS",
		AvgVol10D:  1500000,
		AvgVol20D:  1200000,
		AvgVol30D:  1100000,
		AvgVol50D:  1000000,
		ClosePrice: 4200.00,
	}
	vss.mu.Unlock()

	now := time.Date(2026, 10, 24, 12, 30, 0, 0, data.ISTLocation)

	quotes := map[string]data.Quote{
		"RELIANCE": {
			LastPrice:    1435.00, // +2.5%
			VolumeTraded: 12500000, // 2.5x of 5M 20D baseline
			OHLC: data.OHLC{
				Open:  1405.00,
				High:  1440.00,
				Low:   1402.00,
				Close: 1400.00,
			},
		},
		"TCS": {
			LastPrice:    4120.00, // -1.9%
			VolumeTraded: 3600000,  // 3.0x of 1.2M 20D baseline
			OHLC: data.OHLC{
				Open:  4190.00,
				High:  4205.00,
				Low:   4110.00,
				Close: 4200.00,
			},
		},
	}

	results := vss.ScanBatch(quotes, 20, 2.0, now)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// TCS has 3.0x multiplier, should be first
	if results[0].Symbol != "TCS" || results[0].Multiplier != 3.0 {
		t.Errorf("expected TCS first with 3.0x multiplier, got %s (%.2f)", results[0].Symbol, results[0].Multiplier)
	}
	if results[0].Quadrant != QuadrantDistributionBreakdown {
		t.Errorf("expected TCS to be DISTRIBUTION_BREAKDOWN, got %s", results[0].Quadrant)
	}

	// RELIANCE has 2.5x multiplier, should be second
	if results[1].Symbol != "RELIANCE" || results[1].Multiplier != 2.5 {
		t.Errorf("expected RELIANCE second with 2.5x multiplier, got %s (%.2f)", results[1].Symbol, results[1].Multiplier)
	}
	if results[1].Quadrant != QuadrantAccumulationBreakout {
		t.Errorf("expected RELIANCE to be ACCUMULATION_BREAKOUT, got %s", results[1].Quadrant)
	}

	// Test cache retrieval
	filtered := vss.GetCachedRecords(2.6, "")
	if len(filtered) != 1 || filtered[0].Symbol != "TCS" {
		t.Errorf("expected 1 filtered result (TCS >= 2.6x), got %d", len(filtered))
	}

	// Test nil DB graceful exit
	if err := vss.LoadBaselines(context.Background()); err != nil {
		t.Errorf("expected nil error on nil DB, got %v", err)
	}
}
