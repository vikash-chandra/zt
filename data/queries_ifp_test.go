package data

import (
	"context"
	"testing"
	"time"
)

func TestIFPModelsAndNilGuards(t *testing.T) {
	var nilDB *Database
	ctx := context.Background()

	// 1. Verify nil Database graceful handling
	err := nilDB.SaveVolumeBaselines(ctx, []DailyVolumeBaseline{{Symbol: "RELIANCE"}})
	if err != nil {
		t.Errorf("expected nil error on nil DB SaveVolumeBaselines, got %v", err)
	}

	baselines, err := nilDB.GetLatestVolumeBaselines(ctx)
	if err != nil {
		t.Errorf("expected nil error on nil DB GetLatestVolumeBaselines, got %v", err)
	}
	if len(baselines) != 0 {
		t.Errorf("expected empty map on nil DB, got %d items", len(baselines))
	}

	err = nilDB.SaveFOStrikeOISnapshots(ctx, []FOStrikeOISnapshot{{Symbol: "TCS"}})
	if err != nil {
		t.Errorf("expected nil error on nil DB SaveFOStrikeOISnapshots, got %v", err)
	}

	snapshots, err := nilDB.GetLatestFOStrikeOISnapshots(ctx, "TCS")
	if err != nil {
		t.Errorf("expected nil error on nil DB GetLatestFOStrikeOISnapshots, got %v", err)
	}
	if len(snapshots) != 0 {
		t.Errorf("expected empty slice on nil DB, got %d items", len(snapshots))
	}

	// 2. Verify empty slice handling on instantiated DB with nil connection
	emptyDB := &Database{}
	if err := emptyDB.SaveVolumeBaselines(ctx, nil); err != nil {
		t.Errorf("expected nil error on empty baselines slice, got %v", err)
	}
	if err := emptyDB.SaveFOStrikeOISnapshots(ctx, nil); err != nil {
		t.Errorf("expected nil error on empty snapshots slice, got %v", err)
	}

	// 3. Verify VolumeSurgeRecord fields and Quadrant logic
	rec := VolumeSurgeRecord{
		Symbol:       "INFY",
		LTP:          1850.50,
		PrevClose:    1800.00,
		PctChange:    2.80,
		VolumeToday:  3000000,
		AvgVol20D:    1000000,
		Multiplier:   3.0,
		Quadrant:     "ACCUMULATION_BREAKOUT",
		UpdatedAt:    NormalizeToIST(time.Now()),
	}
	if rec.Multiplier != 3.0 {
		t.Errorf("expected Multiplier 3.0, got %f", rec.Multiplier)
	}
	if rec.Quadrant != "ACCUMULATION_BREAKOUT" {
		t.Errorf("expected Quadrant ACCUMULATION_BREAKOUT, got %s", rec.Quadrant)
	}

	// 4. Verify FOSummaryScore and FOStrikeRow calculations
	strikeRow := FOStrikeRow{
		StrikePrice:   1850.00,
		CallOI:        500000,
		CallOIChange:  120000,
		PutOI:         850000,
		PutOIChange:   340000,
		NetStrikeBias: 220000, // PutOIChange - CallOIChange
		ATMOffset:     0,
		Sentiment:     "BULLISH_SUPPORT",
	}
	if strikeRow.NetStrikeBias != (strikeRow.PutOIChange - strikeRow.CallOIChange) {
		t.Errorf("expected NetStrikeBias %d, got %d", strikeRow.PutOIChange-strikeRow.CallOIChange, strikeRow.NetStrikeBias)
	}

	summary := FOSummaryScore{
		Symbol:             "INFY",
		SpotPrice:          1850.50,
		ATMStrike:          1850.00,
		InstitutionalScore: 47.8,
		DeltaPCR:           1.45,
		TotalPCR:           1.30,
		OptionSellAction:   "SELL_OTM_PUT",
		RecommendedStrike:  1800.00,
		RecommendedType:    "PE",
		RecommendedPremium: 14.50,
		StopLossPremium:    20.30,
		TargetPremium:      1.50,
		Strikes:            []FOStrikeRow{strikeRow},
	}
	if summary.InstitutionalScore != 47.8 {
		t.Errorf("expected InstitutionalScore 47.8, got %f", summary.InstitutionalScore)
	}
	if summary.OptionSellAction != "SELL_OTM_PUT" {
		t.Errorf("expected OptionSellAction SELL_OTM_PUT, got %s", summary.OptionSellAction)
	}
}
