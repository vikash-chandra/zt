package scanner

import (
	"testing"
	"time"

	"zerodha-trading/data"
)

func TestComputeOIPulseDiffPct(t *testing.T) {
	// Match IndexOI /oipulse sample: C = 193776, P = 236561 => D = +42785, D% = +18.09% => NEUTRAL
	diff, pct, sig := ComputeOIPulseDiffPct(193776, 236561)
	if diff != 42785 {
		t.Fatalf("expected diff 42785, got %d", diff)
	}
	if pct < 18.0 || pct > 18.2 {
		t.Fatalf("expected diffPct ~18.09, got %.2f", pct)
	}
	if sig != "NEUTRAL" {
		t.Fatalf("expected NEUTRAL, got %s", sig)
	}

	// Strong Bullish (>= +30%)
	_, pctBull, sigBull := ComputeOIPulseDiffPct(100000, 200000)
	if pctBull != 50.0 || sigBull != "BULLISH" {
		t.Fatalf("expected 50.0 BULLISH, got %.2f %s", pctBull, sigBull)
	}

	// Strong Bearish (<= -30%)
	_, pctBear, sigBear := ComputeOIPulseDiffPct(200000, 100000)
	if pctBear != -50.0 || sigBear != "BEARISH" {
		t.Fatalf("expected -50.0 BEARISH, got %.2f %s", pctBear, sigBear)
	}
}

func TestBuildThreeStrikeView_DiffAndChangeInDiff(t *testing.T) {
	loc := data.ISTLocation
	t1 := time.Date(2026, 11, 26, 15, 27, 0, 0, loc)
	t2 := time.Date(2026, 11, 26, 15, 30, 0, 0, loc)
	exp := time.Date(2026, 12, 1, 0, 0, 0, 0, loc)

	history := []data.FOStrikeOISnapshot{
		{Symbol: "NIFTY 50", ExpiryDate: exp, StrikePrice: 25800, CallOI: 5000000, CallOIChange: -800000, PutOI: 7000000, PutOIChange: 1500000, SpotPrice: 25880, LotSize: 65, SnapshotTime: t1},
		{Symbol: "NIFTY 50", ExpiryDate: exp, StrikePrice: 25850, CallOI: 3000000, CallOIChange: 400000, PutOI: 5000000, PutOIChange: 1100000, SpotPrice: 25880, LotSize: 65, SnapshotTime: t1},
		{Symbol: "NIFTY 50", ExpiryDate: exp, StrikePrice: 25900, CallOI: 6000000, CallOIChange: 2500000, PutOI: 8000000, PutOIChange: 2800000, SpotPrice: 25880, LotSize: 65, SnapshotTime: t1},

		{Symbol: "NIFTY 50", ExpiryDate: exp, StrikePrice: 25800, CallOI: 4900000, CallOIChange: -879970, PutOI: 7100000, PutOIChange: 1544595, SpotPrice: 25884.5, LotSize: 65, SnapshotTime: t2},
		{Symbol: "NIFTY 50", ExpiryDate: exp, StrikePrice: 25850, CallOI: 2990000, CallOIChange: 393705, PutOI: 5010000, PutOIChange: 1100970, SpotPrice: 25884.5, LotSize: 65, SnapshotTime: t2},
		{Symbol: "NIFTY 50", ExpiryDate: exp, StrikePrice: 25900, CallOI: 6050000, CallOIChange: 2531750, PutOI: 8050000, PutOIChange: 2834715, SpotPrice: 25884.5, LotSize: 65, SnapshotTime: t2},
	}

	view := BuildThreeStrikeView("NIFTY 50", "2026-11-26", "", 25800, 3, history, []string{"2026-11-26"})
	if view.TotalCallChg != 2045485 || view.TotalPutChg != 5480280 || view.TotalDiffPC != 3434795 {
		t.Fatalf("unexpected 3-strike totals: call=%d put=%d diff=%d", view.TotalCallChg, view.TotalPutChg, view.TotalDiffPC)
	}
	if len(view.IntradayRows) != 2 {
		t.Fatalf("expected 2 intraday rows, got %d", len(view.IntradayRows))
	}
	// Latest row first (15:30)
	latest := view.IntradayRows[0]
	if latest.Time != "15:30" || latest.Difference != 3434795 {
		t.Fatalf("unexpected latest row: %+v", latest)
	}
	// t1 diff = (1500000+1100000+2800000) - (-800000+400000+2500000) = 5400000 - 2100000 = 3300000
	// change_in_diff = 3434795 - 3300000 = 134795
	if latest.ChangeInDifference != 134795 {
		t.Fatalf("expected change_in_difference 134795, got %d", latest.ChangeInDifference)
	}
}
