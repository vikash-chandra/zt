package data

import (
	"testing"
	"time"
)

func TestIndexMasterRegistry(t *testing.T) {
	tests := []struct {
		input           string
		expectedName    string
		expectedPrefix  string
		expectedToken   int64
		expectedExch    string
		expectedLot     int
		expectedWeekday time.Weekday
	}{
		{"NIFTY 50", "NIFTY 50", "NIFTY", 256265, "NFO", 65, time.Thursday},
		{"nifty", "NIFTY 50", "NIFTY", 256265, "NFO", 65, time.Thursday},
		{"BANKNIFTY", "NIFTY BANK", "BANKNIFTY", 260105, "NFO", 15, time.Thursday},
		{"Nifty Bank", "NIFTY BANK", "BANKNIFTY", 260105, "NFO", 15, time.Thursday},
		{"SENSEX", "BSE SENSEX", "SENSEX", 265, "BFO", 20, time.Friday},
		{"BSE SENSEX", "BSE SENSEX", "SENSEX", 265, "BFO", 20, time.Friday},
		{"FINNIFTY", "FINNIFTY", "FINNIFTY", 257801, "NFO", 65, time.Tuesday},
		{"MIDCPNIFTY", "MIDCPNIFTY", "MIDCPNIFTY", 288009, "NFO", 120, time.Monday},
	}

	for _, tt := range tests {
		spec, found := ResolveIndexSpec(tt.input)
		if !found {
			t.Errorf("expected to find spec for %s", tt.input)
			continue
		}
		if spec.Name != tt.expectedName {
			t.Errorf("for input %s: expected Name %s, got %s", tt.input, tt.expectedName, spec.Name)
		}
		if spec.CleanPrefix != tt.expectedPrefix {
			t.Errorf("for input %s: expected CleanPrefix %s, got %s", tt.input, tt.expectedPrefix, spec.CleanPrefix)
		}
		if spec.SpotToken != tt.expectedToken {
			t.Errorf("for input %s: expected SpotToken %d, got %d", tt.input, tt.expectedToken, spec.SpotToken)
		}
		if spec.OptionsExchange != tt.expectedExch {
			t.Errorf("for input %s: expected OptionsExchange %s, got %s", tt.input, tt.expectedExch, spec.OptionsExchange)
		}
		if spec.BaseLotSize != tt.expectedLot {
			t.Errorf("for input %s: expected BaseLotSize %d, got %d", tt.input, tt.expectedLot, spec.BaseLotSize)
		}
		if spec.ExpiryWeekday != tt.expectedWeekday {
			t.Errorf("for input %s: expected ExpiryWeekday %v, got %v", tt.input, tt.expectedWeekday, spec.ExpiryWeekday)
		}
	}
}
