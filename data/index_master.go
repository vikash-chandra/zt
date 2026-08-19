package data

import (
	"strings"
	"time"
)

// IndexSpec defines the structural specification for an index and its derivative contracts
type IndexSpec struct {
	Name                 string       `json:"name"`                   // e.g. "NIFTY 50", "NIFTY BANK", "BSE SENSEX"
	CleanPrefix          string       `json:"clean_prefix"`           // e.g. "NIFTY", "BANKNIFTY", "SENSEX"
	SpotToken            int64        `json:"spot_token"`             // Zerodha instrument token for underlying index
	SpotExchange         string       `json:"spot_exchange"`          // "NSE" or "BSE"
	OptionsExchange      string       `json:"options_exchange"`       // "NFO" or "BFO"
	BaseLotSize          int          `json:"base_lot_size"`          // Standard lot size (e.g. 65 for NIFTY, 15 for BANKNIFTY, 20 for SENSEX)
	StrikeStep           float64      `json:"strike_step"`            // Strike interval (e.g. 50, 100)
	ExpiryWeekday        time.Weekday `json:"expiry_weekday"`         // Standard monthly expiry weekday
	DefaultTargetPremium float64      `json:"default_target_premium"` // Default target premium for strike selection (e.g. ₹100, ₹250)
	Aliases              []string     `json:"aliases"`                // Common synonyms/aliases
}

// Pre-defined Index Specifications for all major Indian Indices
var supportedIndices = []*IndexSpec{
	{
		Name:                 "NIFTY 50",
		CleanPrefix:          "NIFTY",
		SpotToken:            256265,
		SpotExchange:         "NSE",
		OptionsExchange:      "NFO",
		BaseLotSize:          65,
		StrikeStep:           50.0,
		ExpiryWeekday:        time.Thursday,
		DefaultTargetPremium: 100.0,
		Aliases:              []string{"NIFTY", "NIFTY 50", "NIFTY50", "NIFTY-50"},
	},
	{
		Name:                 "NIFTY BANK",
		CleanPrefix:          "BANKNIFTY",
		SpotToken:            260105,
		SpotExchange:         "NSE",
		OptionsExchange:      "NFO",
		BaseLotSize:          15,
		StrikeStep:           100.0,
		ExpiryWeekday:        time.Thursday,
		DefaultTargetPremium: 250.0,
		Aliases:              []string{"BANKNIFTY", "NIFTY BANK", "NIFTYBANK", "BANK NIFTY", "BANK-NIFTY"},
	},
	{
		Name:                 "BSE SENSEX",
		CleanPrefix:          "SENSEX",
		SpotToken:            265,
		SpotExchange:         "BSE",
		OptionsExchange:      "BFO",
		BaseLotSize:          20,
		StrikeStep:           100.0,
		ExpiryWeekday:        time.Friday,
		DefaultTargetPremium: 250.0,
		Aliases:              []string{"SENSEX", "BSE SENSEX", "BSESEN", "BSE-SENSEX"},
	},
	{
		Name:                 "FINNIFTY",
		CleanPrefix:          "FINNIFTY",
		SpotToken:            257801,
		SpotExchange:         "NSE",
		OptionsExchange:      "NFO",
		BaseLotSize:          65,
		StrikeStep:           50.0,
		ExpiryWeekday:        time.Tuesday,
		DefaultTargetPremium: 100.0,
		Aliases:              []string{"FINNIFTY", "NIFTY FIN SERVICE", "NIFTY FINANCIAL SERVICES", "NIFTYFIN"},
	},
	{
		Name:                 "MIDCPNIFTY",
		CleanPrefix:          "MIDCPNIFTY",
		SpotToken:            288009,
		SpotExchange:         "NSE",
		OptionsExchange:      "NFO",
		BaseLotSize:          120,
		StrikeStep:           25.0,
		ExpiryWeekday:        time.Monday,
		DefaultTargetPremium: 80.0,
		Aliases:              []string{"MIDCPNIFTY", "NIFTY MID SELECT", "NIFTY MIDCAP SELECT", "MIDCAPNIFTY"},
	},
}

// GetAllSupportedIndices returns a copy of all supported index specifications
func GetAllSupportedIndices() []*IndexSpec {
	res := make([]*IndexSpec, len(supportedIndices))
	copy(res, supportedIndices)
	return res
}

// ResolveIndexSpec matches an input name, symbol, or alias to its IndexSpec
func ResolveIndexSpec(input string) (*IndexSpec, bool) {
	trimmed := strings.TrimSpace(strings.ToUpper(input))
	if trimmed == "" {
		// Default to NIFTY 50
		return supportedIndices[0], true
	}

	for _, spec := range supportedIndices {
		if strings.ToUpper(spec.Name) == trimmed || strings.ToUpper(spec.CleanPrefix) == trimmed {
			return spec, true
		}
		for _, alias := range spec.Aliases {
			if strings.ToUpper(alias) == trimmed {
				return spec, true
			}
		}
	}

	// Substring / partial match fallback
	for _, spec := range supportedIndices {
		if strings.Contains(trimmed, strings.ToUpper(spec.CleanPrefix)) {
			return spec, true
		}
	}

	// Default fallback: NIFTY 50
	return supportedIndices[0], false
}
