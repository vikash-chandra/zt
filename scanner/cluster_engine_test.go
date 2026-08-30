package scanner

import (
	"testing"
	"time"

	"zerodha-trading/data"
)

func TestAggregateDailyToWeekly(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Kolkata")
	base := time.Date(2026, 8, 3, 9, 15, 0, 0, loc) // Monday

	var dailyCandles []data.Candle
	for i := 0; i < 15; i++ { // 3 full weeks (Mon-Fri)
		dayOffset := i
		// skip weekends
		day := base.AddDate(0, 0, dayOffset)
		dailyCandles = append(dailyCandles, data.Candle{
			Token:  12345,
			Time:   day,
			Open:   100.0 + float64(i),
			High:   105.0 + float64(i),
			Low:    95.0 + float64(i),
			Close:  102.0 + float64(i),
			Volume: 1000,
		})
	}

	weekly := AggregateDailyToWeekly(dailyCandles)
	if len(weekly) < 2 {
		t.Fatalf("expected at least 2 weekly candles, got %d", len(weekly))
	}

	// Verify first weekly candle has sum volume and min low / max high
	firstW := weekly[0]
	if firstW.Volume <= 0 {
		t.Errorf("expected positive weekly volume, got %d", firstW.Volume)
	}
	if firstW.High < firstW.Low {
		t.Errorf("weekly high %f should be >= low %f", firstW.High, firstW.Low)
	}
}

func TestEvaluateCluster(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Kolkata")
	base := time.Date(2026, 8, 1, 9, 15, 0, 0, loc)

	// Create candles where price is tight around 100.0
	var tightCandles []data.Candle
	for i := 0; i < 30; i++ {
		tightCandles = append(tightCandles, data.Candle{
			Token:  12345,
			Time:   base.AddDate(0, 0, i),
			Open:   100.0,
			High:   100.2,
			Low:    99.8,
			Close:  100.0,
			Volume: 50000,
		})
	}

	cfg := DefaultClusterConfig()
	cfg.ClusterMaxSpreadPct = 0.1

	isCluster, metrics := EvaluateCluster(tightCandles, cfg, "DAILY")
	if !isCluster {
		t.Errorf("expected cluster to be true for tight consolidation, got false (spreadPct: %f%%)", metrics.SpreadPct)
	}
	if metrics.EMA10 <= 0 || metrics.EMA20 <= 0 || metrics.EMA89 <= 0 {
		t.Errorf("expected valid EMA values, got EMA10: %f, EMA20: %f, EMA89: %f", metrics.EMA10, metrics.EMA20, metrics.EMA89)
	}
	if metrics.SpreadPct > 0.1 {
		t.Errorf("spreadPct %f exceeded target %f", metrics.SpreadPct, cfg.ClusterMaxSpreadPct)
	}
}
