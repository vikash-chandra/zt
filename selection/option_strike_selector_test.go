package selection

import (
	"testing"
	"time"
)

func TestMonthlyExpiryAndRollOver(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Kolkata")

	// Test 1: Mid-month date (e.g. 10 Aug 2026) -> Should select 27 Aug 2026 (last Thursday)
	aug10 := time.Date(2026, 8, 10, 10, 0, 0, 0, loc)
	expDate := GetMonthlyExpiryDate(aug10, 7)
	if expDate.Format("2006-01-02") != "2026-08-27" {
		t.Errorf("expected monthly expiry 2026-08-27, got %s", expDate.Format("2006-01-02"))
	}

	// Test 2: Near end-of-month date (e.g. 23 Aug 2026, <= 7 days before 27 Aug) -> Should roll over to 24 Sep 2026
	aug23 := time.Date(2026, 8, 23, 10, 0, 0, 0, loc)
	rollExpDate := GetMonthlyExpiryDate(aug23, 7)
	if rollExpDate.Format("2006-01-02") != "2026-09-24" {
		t.Errorf("expected rollover monthly expiry 2026-09-24, got %s", rollExpDate.Format("2006-01-02"))
	}
}

func TestTargetPremiumStrikeSelector(t *testing.T) {
	selector := NewOptionStrikeSelector(nil)

	res, err := selector.SelectStrikeByTargetPremium("NIFTY 50", 24340.0, "BULLISH", 100.0, "MONTHLY", 7, nil)
	if err != nil {
		t.Fatalf("unexpected error selecting strike by target premium: %v", err)
	}

	if res.BaseStrike != 24300.0 {
		t.Errorf("expected BaseStrike 24300.0, got %f", res.BaseStrike)
	}
	if res.OptionType != "PE" {
		t.Errorf("expected OptionType PE, got %s", res.OptionType)
	}
}
