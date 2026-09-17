package data

import (
	"testing"
	"time"
)

func TestNormalizeCandleTimeframe(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"5m", "5m"},
		{"5min", "5m"},
		{"5", "5m"},
		{"05:00:00", "5m"},
		{"00:05:00", "5m"},
		{"5-min", "5m"},
		{"1m", "1m"},
		{"1min", "1m"},
		{"1", "1m"},
		{"01:00:00", "1m"},
		{"00:01:00", "1m"},
		{"15m", "15m"},
		{"15:00:00", "15m"},
		{"", "5m"},
	}

	for _, tt := range tests {
		result := NormalizeCandleTimeframe(tt.input)
		if result != tt.expected {
			t.Errorf("NormalizeCandleTimeframe(%q) = %q; want %q", tt.input, result, tt.expected)
		}
	}
}

func TestIsClockTimeConfigKey(t *testing.T) {
	tests := []struct {
		key      string
		expected bool
	}{
		// Non-clock keys (should NOT be converted by NormalizeTimeHHMMSS)
		{"lv_candle_timeframe", false},
		{"vb_candle_timeframe", false},
		{"fb_candle_timeframe", false},
		{"vbt_candle_timeframe", false},
		{"es5_candle_timeframe", false},
		{"max_holding_time_min", false},
		{"time_decay_min", false},
		{"manual_trade_poll_minutes", false},
		{"risk_per_trade_inr", false},
		{"capital_inr", false},

		// Clock-time keys (SHOULD be normalized to HH:MM:SS)
		{"lv_trade_end_time", true},
		{"vb_trade_end_time", true},
		{"fb_trade_end_time", true},
		{"vbt_trade_end_time", true},
		{"es5_trade_end_time", true},
		{"auto_square_off_time", true},
		{"restart_allowed_before", true},
		{"restart_allowed_after", true},
		{"morning_broad_agg_start", true},
		{"morning_broad_agg_end", true},
		{"stock_select_time", true},
		{"evg_stock_select_time", true},
		{"execution_time", true},
		{"supertrend_cutoff_time", true},
	}

	for _, tt := range tests {
		result := IsClockTimeConfigKey(tt.key)
		if result != tt.expected {
			t.Errorf("IsClockTimeConfigKey(%q) = %v; want %v", tt.key, result, tt.expected)
		}
	}
}

func TestParseTimeHMS(t *testing.T) {
	tests := []struct {
		input string
		h     int
		m     int
		s     int
		valid bool
	}{
		{"09:15:00", 9, 15, 0, true},
		{"09:15", 9, 15, 0, true},
		{"9:15", 9, 15, 0, true},
		{"11:00:00", 11, 0, 0, true},
		{"11:00", 11, 0, 0, true},
		{"11", 11, 0, 0, true},
		{"15:30:45", 15, 30, 45, true},
		{"", 0, 0, 0, false},
	}

	for _, tt := range tests {
		h, m, s, err := ParseTimeHMS(tt.input)
		if tt.valid && err != nil {
			t.Errorf("ParseTimeHMS(%q) unexpected error: %v", tt.input, err)
		} else if !tt.valid && err == nil {
			t.Errorf("ParseTimeHMS(%q) expected error but got none", tt.input)
		} else if tt.valid {
			if h != tt.h || m != tt.m || s != tt.s {
				t.Errorf("ParseTimeHMS(%q) = (%d, %d, %d); want (%d, %d, %d)", tt.input, h, m, s, tt.h, tt.m, tt.s)
			}
		}
	}
}

func TestNormalizeToIST(t *testing.T) {
	// 1. Live 11:25 AM IST tick in UTC (05:55:00 UTC)
	t1 := time.Date(2026, 9, 17, 5, 55, 0, 0, time.UTC)
	n1 := NormalizeToIST(t1)
	if n1.Hour() != 11 || n1.Minute() != 25 {
		t.Errorf("Scenario 1 (05:55 UTC) failed: got %02d:%02d, want 11:25", n1.Hour(), n1.Minute())
	}

	// 2. Live 09:15 AM IST tick in UTC (03:45:00 UTC)
	t2 := time.Date(2026, 9, 17, 3, 45, 0, 0, time.UTC)
	n2 := NormalizeToIST(t2)
	if n2.Hour() != 9 || n2.Minute() != 15 {
		t.Errorf("Scenario 2 (03:45 UTC) failed: got %02d:%02d, want 09:15", n2.Hour(), n2.Minute())
	}

	// 3. Candle generated in ISTLocation
	t3 := time.Date(2026, 9, 17, 11, 25, 0, 0, ISTLocation)
	n3 := NormalizeToIST(t3)
	if n3.Hour() != 11 || n3.Minute() != 25 {
		t.Errorf("Scenario 3 (ISTLocation) failed: got %02d:%02d, want 11:25", n3.Hour(), n3.Minute())
	}

	// 4. Daily candle date 00:00:00 UTC
	t4 := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	n4 := NormalizeToIST(t4)
	if n4.Hour() != 0 || n4.Minute() != 0 {
		t.Errorf("Scenario 4 (Daily 00:00:00 UTC) failed: got %02d:%02d, want 00:00", n4.Hour(), n4.Minute())
	}

	// 5. Seeded historical 09:15:00 UTC
	t5 := time.Date(2026, 9, 17, 9, 15, 0, 0, time.UTC)
	n5 := NormalizeToIST(t5)
	if n5.Hour() != 9 || n5.Minute() != 15 {
		t.Errorf("Scenario 5 (Seeded 09:15 UTC) failed: got %02d:%02d, want 09:15", n5.Hour(), n5.Minute())
	}

	// 6. PostgreSQL TIMESTAMPTZ with +05:30
	t6 := time.Date(2026, 9, 17, 11, 25, 0, 0, time.FixedZone("IST", 19800))
	n6 := NormalizeToIST(t6)
	if n6.Hour() != 11 || n6.Minute() != 25 {
		t.Errorf("Scenario 6 (TIMESTAMPTZ +05:30) failed: got %02d:%02d, want 11:25", n6.Hour(), n6.Minute())
	}
}

