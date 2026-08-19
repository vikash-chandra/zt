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
	Exchange     string  `json:"exchange"` // "NFO" or "BFO"
	ExpiryDate   string  `json:"expiry_date"`
	SelectedLTP  float64 `json:"selected_ltp"`
}

// OptionStrikeSelector selects OTM option strikes (Base +/- offset) for Index options across NFO and BFO
type OptionStrikeSelector struct {
	secMaster *data.SecurityMaster
}

// NewOptionStrikeSelector creates a new OptionStrikeSelector
func NewOptionStrikeSelector(secMaster *data.SecurityMaster) *OptionStrikeSelector {
	return &OptionStrikeSelector{secMaster: secMaster}
}

// GetMonthlyExpiryDate returns the last Thursday of current month, or next month if <= rolloverDays remain
func GetMonthlyExpiryDate(t time.Time, rolloverDays int) time.Time {
	return GetMonthlyExpiryDateForIndex(t, rolloverDays, time.Thursday)
}

// GetMonthlyExpiryDateForIndex returns the last specific weekday of current month (or next month if <= rolloverDays remain)
func GetMonthlyExpiryDateForIndex(t time.Time, rolloverDays int, weekday time.Weekday) time.Time {
	t = t.In(data.ISTLocation)
	if rolloverDays <= 0 {
		rolloverDays = 7
	}

	calcLastWeekday := func(year int, month time.Month) time.Time {
		// Last day of target month
		lastDay := time.Date(year, month+1, 0, 15, 30, 0, 0, data.ISTLocation)
		for lastDay.Weekday() != weekday {
			lastDay = lastDay.AddDate(0, 0, -1)
		}
		return lastDay
	}

	currExpiry := calcLastWeekday(t.Year(), t.Month())
	daysRemaining := int(currExpiry.Sub(t).Hours() / 24)

	// If 7 days or fewer remain before monthly expiry, roll over to next month's expiry!
	if daysRemaining <= rolloverDays {
		nextMonth := t.AddDate(0, 1, 0)
		return calcLastWeekday(nextMonth.Year(), nextMonth.Month())
	}

	return currExpiry
}

// FormatMonthlyOptionSymbol formats Zerodha monthly option symbol (e.g. NIFTY26AUG24800CE, SENSEX26AUG80000CE)
func FormatMonthlyOptionSymbol(cleanIndex string, expiryDate time.Time, strike float64, optType string) string {
	yearStr := expiryDate.Format("06")
	monthStr := strings.ToUpper(expiryDate.Format("Jan"))
	return fmt.Sprintf("%s%s%s%.0f%s", cleanIndex, yearStr, monthStr, strike, optType)
}

// SelectStrikeByTargetPremium scans candidate strikes and selects the contract nearest to targetPremium
func (s *OptionStrikeSelector) SelectStrikeByTargetPremium(
	indexSymbol string, indexSpot float64, trend string, targetPremium float64,
	expiryType string, rolloverDays int, broker data.BrokerClient,
) (*OptionStrikeResult, error) {
	if indexSpot <= 0 {
		return nil, fmt.Errorf("invalid index spot price: %f", indexSpot)
	}

	spec, _ := data.ResolveIndexSpec(indexSymbol)
	cleanIndex := spec.CleanPrefix
	optsExchange := spec.OptionsExchange
	strikeStep := spec.StrikeStep
	if strikeStep <= 0 {
		strikeStep = 100.0
	}

	if targetPremium <= 0 {
		targetPremium = spec.DefaultTargetPremium
		if targetPremium <= 0 {
			targetPremium = 100.0
		}
	}

	baseStrike := math.Round(indexSpot/strikeStep) * strikeStep
	var optionType string
	if trend == "BULLISH" {
		optionType = "PE"
	} else if trend == "BEARISH" {
		optionType = "CE"
	} else {
		return nil, fmt.Errorf("cannot select strike for NEUTRAL trend")
	}

	now := time.Now().In(data.ISTLocation)
	var expiryDate time.Time
	if strings.ToUpper(expiryType) == "MONTHLY" {
		expiryDate = GetMonthlyExpiryDateForIndex(now, rolloverDays, spec.ExpiryWeekday)
	} else {
		// Fallback to weekly expiry
		expiryDate = now
		targetWeekday := spec.ExpiryWeekday
		for expiryDate.Weekday() != targetWeekday {
			expiryDate = expiryDate.AddDate(0, 0, 1)
		}
		if expiryDate.Format("2006-01-02") == now.Format("2006-01-02") && (now.Hour() > 15 || (now.Hour() == 15 && now.Minute() >= 30)) {
			expiryDate = expiryDate.AddDate(0, 0, 7)
		}
	}

	// Generate candidate OTM strikes (Base +/- 1x..10x strikeStep)
	var candidateSymbols []string
	candidateStrikes := make(map[string]float64)

	for multiplier := 1.0; multiplier <= 10.0; multiplier += 1.0 {
		offset := multiplier * strikeStep
		var strike float64
		if optionType == "PE" {
			strike = baseStrike - offset
		} else {
			strike = baseStrike + offset
		}

		var sym string
		if strings.ToUpper(expiryType) == "MONTHLY" {
			sym = FormatMonthlyOptionSymbol(cleanIndex, expiryDate, strike, optionType)
		} else {
			yearStr := expiryDate.Format("06")
			m := expiryDate.Month()
			monthStr := fmt.Sprintf("%d", m)
			if m == time.October {
				monthStr = "O"
			} else if m == time.November {
				monthStr = "N"
			} else if m == time.December {
				monthStr = "D"
			}
			dayStr := expiryDate.Format("02")
			sym = fmt.Sprintf("%s%s%s%s%.0f%s", cleanIndex, yearStr, monthStr, dayStr, strike, optionType)
		}

		prefixedSym := optsExchange + ":" + sym
		candidateSymbols = append(candidateSymbols, prefixedSym)
		candidateStrikes[prefixedSym] = strike
	}

	// Default fallback: 3rd candidate
	defaultIdx := 2
	if defaultIdx >= len(candidateSymbols) {
		defaultIdx = 0
	}
	bestSymbol := candidateSymbols[defaultIdx]
	bestStrike := candidateStrikes[bestSymbol]
	bestLTP := targetPremium
	minDiff := 999999.0

	// Query live quotes for candidate contracts if broker is provided
	if broker != nil && len(candidateSymbols) > 0 {
		quotes, err := broker.GetQuote(candidateSymbols...)
		if err == nil && len(quotes) > 0 {
			for sym, q := range quotes {
				if q.LastPrice > 0 {
					diff := math.Abs(q.LastPrice - targetPremium)
					if diff < minDiff {
						minDiff = diff
						bestSymbol = sym
						bestStrike = candidateStrikes[sym]
						bestLTP = q.LastPrice
					}
				}
			}
		}
	}

	cleanSymbol := strings.TrimPrefix(strings.TrimPrefix(bestSymbol, "NFO:"), "BFO:")
	return &OptionStrikeResult{
		IndexSymbol:  spec.Name,
		IndexSpot:    indexSpot,
		BaseStrike:   baseStrike,
		StrikeOffset: math.Abs(bestStrike - baseStrike),
		OptionType:   optionType,
		TargetStrike: bestStrike,
		OptionSymbol: cleanSymbol,
		Exchange:     optsExchange,
		ExpiryDate:   expiryDate.Format("2006-01-02"),
		SelectedLTP:  bestLTP,
	}, nil
}

// SelectOTMStrike calculates target strike and resolves option trading symbol using fixed offset or target premium
func (s *OptionStrikeSelector) SelectOTMStrike(indexSymbol string, indexSpot float64, trend string, offsetPoints float64) (*OptionStrikeResult, error) {
	return s.SelectStrikeByTargetPremium(indexSymbol, indexSpot, trend, 100.0, "MONTHLY", 7, nil)
}
