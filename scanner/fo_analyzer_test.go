package scanner

import (
	"testing"
	"time"

	"zerodha-trading/data"
	"zerodha-trading/monitoring"
)

func TestRoundToNearestStrike(t *testing.T) {
	if s := RoundToNearestStrike(1418.50, 20.0); s != 1420.0 {
		t.Errorf("expected 1420.0 for 1418.50 step 20, got %f", s)
	}
	if s := RoundToNearestStrike(1408.00, 20.0); s != 1400.0 {
		t.Errorf("expected 1400.0 for 1408.00 step 20, got %f", s)
	}
	if s := RoundToNearestStrike(24560.00, 50.0); s != 24550.0 {
		t.Errorf("expected 24550.0 for 24560 step 50, got %f", s)
	}
}

func TestClassifyFuturesBuildup(t *testing.T) {
	// 1. Long Buildup: Price UP (+1.5%), OI UP (+8.0%)
	b1, _, oiChg1, priceChg1 := ClassifyFuturesBuildup(100.0, 101.5, 100.0, 108000, 100000)
	if b1 != BuildupLongBuildup {
		t.Errorf("expected LONG_BUILDUP, got %s (price %.2f%%, oi %.2f%%)", b1, priceChg1, oiChg1)
	}

	// 2. Short Buildup: Price DOWN (-2.0%), OI UP (+12.0%)
	b2, _, oiChg2, priceChg2 := ClassifyFuturesBuildup(100.0, 98.0, 100.0, 112000, 100000)
	if b2 != BuildupShortBuildup {
		t.Errorf("expected SHORT_BUILDUP, got %s (price %.2f%%, oi %.2f%%)", b2, priceChg2, oiChg2)
	}

	// 3. Short Covering: Price UP (+1.8%), OI DOWN (-10.0%)
	b3, _, oiChg3, priceChg3 := ClassifyFuturesBuildup(100.0, 101.8, 100.0, 90000, 100000)
	if b3 != BuildupShortCovering {
		t.Errorf("expected SHORT_COVERING, got %s (price %.2f%%, oi %.2f%%)", b3, priceChg3, oiChg3)
	}

	// 4. Long Unwinding: Price DOWN (-1.5%), OI DOWN (-8.0%)
	b4, _, oiChg4, priceChg4 := ClassifyFuturesBuildup(100.0, 98.5, 100.0, 92000, 100000)
	if b4 != BuildupLongUnwinding {
		t.Errorf("expected LONG_UNWINDING, got %s (price %.2f%%, oi %.2f%%)", b4, priceChg4, oiChg4)
	}
}

func TestCalculateInstitutionalScoreAndOptionSelling(t *testing.T) {
	// Scenario A: Strong Bullish Put Writing Cluster around ATM 1420
	bullishStrikes := []data.FOStrikeRow{
		{StrikePrice: 1380, CallOI: 100000, CallOIChange: 5000, PutOI: 800000, PutOIChange: 250000, PutLTP: 4.50},
		{StrikePrice: 1400, CallOI: 250000, CallOIChange: 15000, PutOI: 1200000, PutOIChange: 450000, PutLTP: 12.00},
		{StrikePrice: 1420, CallOI: 500000, CallOIChange: 40000, PutOI: 900000, PutOIChange: 320000, PutLTP: 24.50},
		{StrikePrice: 1440, CallOI: 850000, CallOIChange: 80000, PutOI: 300000, PutOIChange: 50000, CallLTP: 15.00},
		{StrikePrice: 1460, CallOI: 1100000, CallOIChange: 110000, PutOI: 100000, PutOIChange: 10000, CallLTP: 6.00},
	}

	score, deltaPCR, totalPCR := CalculateInstitutionalScore(bullishStrikes)
	if score < 35.0 {
		t.Errorf("expected strong positive institutional score >= +35, got %.1f", score)
	}
	if deltaPCR < 1.25 {
		t.Errorf("expected delta PCR >= 1.25, got %.2f", deltaPCR)
	}
	if totalPCR <= 1.0 {
		t.Errorf("expected total PCR > 1.0, got %.2f", totalPCR)
	}

	callWall, putWall, _ := FindWallsAndMaxPain(bullishStrikes)
	if callWall != 1460.0 {
		t.Errorf("expected Call Wall at 1460 (1.1M Call OI), got %.0f", callWall)
	}
	if putWall != 1400.0 {
		t.Errorf("expected Put Wall at 1400 (1.2M Put OI), got %.0f", putWall)
	}

	action, recStrike, recType, recPrem, slPrem, tgtPrem, _ := GenerateOptionSellingTrade(
		score, deltaPCR, bullishStrikes, 1422.0, callWall, putWall,
	)
	if action != ActionSellOTMPut {
		t.Errorf("expected action SELL_OTM_PUT, got %s", action)
	}
	if recType != "PE" {
		t.Errorf("expected recommended type PE, got %s", recType)
	}
	if recStrike != 1400.0 {
		t.Errorf("expected recommended strike 1400 (Put Wall), got %.0f", recStrike)
	}
	if recPrem != 12.00 {
		t.Errorf("expected recommended premium 12.00, got %.2f", recPrem)
	}
	if slPrem <= recPrem || tgtPrem >= recPrem {
		t.Errorf("expected SL > entry and Target < entry, got SL %.2f, Target %.2f", slPrem, tgtPrem)
	}
}

func TestFOAnalyzerBuildSummaryAndCache(t *testing.T) {
	logger, _ := monitoring.NewLogger("info")
	fa := NewFOAnalyzer(nil, logger.Logger)

	strikes := []data.FOStrikeRow{
		{StrikePrice: 1780, CallOI: 400000, CallOIChange: 200000, PutOI: 200000, PutOIChange: 20000, CallLTP: 35.0, PutLTP: 15.0},
		{StrikePrice: 1800, CallOI: 950000, CallOIChange: 450000, PutOI: 300000, PutOIChange: 15000, CallLTP: 18.0, PutLTP: 38.0},
		{StrikePrice: 1820, CallOI: 1200000, CallOIChange: 550000, PutOI: 150000, PutOIChange: 5000, CallLTP: 8.5, PutLTP: 65.0},
	}

	summary := fa.BuildFOSummary("TECHM", 1794.40, 1800.0, "2026-10-29", BuildupShortBuildup, strikes, time.Now())
	if summary.InstitutionalScore > -35.0 {
		t.Errorf("expected bearish score <= -35, got %.1f", summary.InstitutionalScore)
	}
	if summary.OptionSellAction != ActionSellOTMCall {
		t.Errorf("expected action SELL_OTM_CALL, got %s", summary.OptionSellAction)
	}

	cached, found := fa.GetSummary("TECHM")
	if !found || cached.Symbol != "TECHM" {
		t.Fatalf("expected to retrieve cached summary for TECHM")
	}

	all := fa.GetAllSummaries()
	if len(all) != 1 || all[0].Symbol != "TECHM" {
		t.Errorf("expected 1 summary in GetAllSummaries")
	}

	// Test Futures buildup cache
	buildups := []data.FuturesBuildupRecord{
		{Symbol: "TECHM", SpotPrice: 1794.40, FuturesPrice: 1799.00, BuildupType: BuildupShortBuildup},
	}
	fa.SetFuturesBuildups(buildups)
	resBuildups := fa.GetFuturesBuildups()
	if len(resBuildups) != 1 || resBuildups[0].Symbol != "TECHM" {
		t.Errorf("expected 1 cached futures buildup record")
	}
}
