package selection

import (
	"fmt"
	"math"
	"strings"
	"time"
	"zerodha-trading/data"
)

// OptionStrikeResult holds the selected strike details for an options trade
type OptionStrikeResult struct {
	IndexSymbol  string  `json:"index_symbol"`
	IndexSpot    float64 `json:"index_spot"`
	BaseStrike   float64 `json:"base_strike"`
	StrikeOffset float64 `json:"strike_offset"`
	OptionType   string  `json:"option_type"` // "PE" for Bullish, "CE" for Bearish
	TargetStrike float64 `json:"target_strike"`
	OptionSymbol string  `json:"option_symbol"`
}

// OptionStrikeSelector selects OTM option strikes (Base +/- offset) for NFO Index options
type OptionStrikeSelector struct {
	secMaster *data.SecurityMaster
}

// NewOptionStrikeSelector creates a new OptionStrikeSelector
func NewOptionStrikeSelector(secMaster *data.SecurityMaster) *OptionStrikeSelector {
	return &OptionStrikeSelector{secMaster: secMaster}
}

// SelectOTMStrike calculates target strike and resolves option trading symbol
func (s *OptionStrikeSelector) SelectOTMStrike(indexSymbol string, indexSpot float64, trend string, offsetPoints float64) (*OptionStrikeResult, error) {
	if indexSpot <= 0 {
		return nil, fmt.Errorf("invalid index spot price: %f", indexSpot)
	}

	// 1. Calculate nearest base 100 multiple
	baseStrike := math.Round(indexSpot/100.0) * 100.0

	var optionType string
	var targetStrike float64

	if trend == "BULLISH" {
		// Bullish Trend -> Sell OTM Put (PE) at Base - Offset
		optionType = "PE"
		targetStrike = baseStrike - offsetPoints
	} else if trend == "BEARISH" {
		// Bearish Trend -> Sell OTM Call (CE) at Base + Offset
		optionType = "CE"
		targetStrike = baseStrike + offsetPoints
	} else {
		return nil, fmt.Errorf("cannot select strike for NEUTRAL trend")
	}

	// Format expected symbol pattern (e.g., NIFTY 50 -> NIFTY)
	cleanIndex := "NIFTY"
	if strings.Contains(strings.ToUpper(indexSymbol), "BANK") {
		cleanIndex = "BANKNIFTY"
	}

	// Calculate upcoming weekly expiry date for Zerodha NFO symbol (NIFTY = Tuesday, BANKNIFTY = Wednesday)
	now := time.Now().In(data.ISTLocation)
	targetWeekday := time.Tuesday
	if cleanIndex == "BANKNIFTY" {
		targetWeekday = time.Wednesday
	}

	expiryDate := now
	for expiryDate.Weekday() != targetWeekday {
		expiryDate = expiryDate.AddDate(0, 0, 1)
	}
	if expiryDate.Format("2006-01-02") == now.Format("2006-01-02") && (now.Hour() > 15 || (now.Hour() == 15 && now.Minute() >= 30)) {
		expiryDate = expiryDate.AddDate(0, 0, 7)
	}

	yearStr := expiryDate.Format("06")
	var monthStr string
	m := expiryDate.Month()
	switch m {
	case time.October:
		monthStr = "O"
	case time.November:
		monthStr = "N"
	case time.December:
		monthStr = "D"
	default:
		monthStr = fmt.Sprintf("%d", m)
	}
	dayStr := expiryDate.Format("02")

	// Construct Zerodha NFO weekly option symbol (e.g., NIFTY2681124600CE)
	optionSymbol := fmt.Sprintf("%s%s%s%s%.0f%s", cleanIndex, yearStr, monthStr, dayStr, targetStrike, optionType)

	return &OptionStrikeResult{
		IndexSymbol:  indexSymbol,
		IndexSpot:    indexSpot,
		BaseStrike:   baseStrike,
		StrikeOffset: offsetPoints,
		OptionType:   optionType,
		TargetStrike: targetStrike,
		OptionSymbol: optionSymbol,
	}, nil
}
