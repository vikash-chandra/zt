package data

import (
	"testing"
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
