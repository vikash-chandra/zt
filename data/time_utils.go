package data

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ISTLocation is the centralized time location for Indian Standard Time (Asia/Kolkata)
var ISTLocation *time.Location

func init() {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.FixedZone("IST", 5*3600+30*60)
	}
	ISTLocation = loc
}

// NowIST returns the current wall-clock time in Indian Standard Time (Asia/Kolkata)
func NowIST() time.Time {
	return time.Now().In(ISTLocation)
}

// NormalizeToIST centralizes time normalization across the entire application.
// It guarantees that any timestamp (UTC from DB/Kite API or wall-clock IST) is
// cleanly converted to exact IST time (Asia/Kolkata) with anchored wall-clock components.
func NormalizeToIST(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), ISTLocation)
}

// FormatIST formats any time into a clean IST string according to the given layout
func FormatIST(t time.Time, layout string) string {
	return NormalizeToIST(t).Format(layout)
}

// FormatDate formats any time into a standard YYYY-MM-DD date string in IST
func FormatDate(t time.Time) string {
	return NormalizeToIST(t).Format("2006-01-02")
}

// GetEffectiveTradingDate returns the effective trading date (YYYY-MM-DD) for a given time.
// Before 09:15 AM IST, it rolls back to the previous trading day (skipping weekends).
// At or after 09:15 AM IST, it returns current date (or previous Friday if weekend).
func GetEffectiveTradingDate(t time.Time) string {
	tIST := NormalizeToIST(t)
	cutoff := time.Date(tIST.Year(), tIST.Month(), tIST.Day(), 9, 15, 0, 0, ISTLocation)
	target := tIST
	if tIST.Before(cutoff) {
		target = target.AddDate(0, 0, -1)
	}
	for target.Weekday() == time.Saturday || target.Weekday() == time.Sunday {
		target = target.AddDate(0, 0, -1)
	}
	return target.Format("2006-01-02")
}

// GetPreviousTradingDay returns the previous trading day (skipping weekends) in IST
func GetPreviousTradingDay(t time.Time) time.Time {
	target := NormalizeToIST(t).AddDate(0, 0, -1)
	for target.Weekday() == time.Saturday || target.Weekday() == time.Sunday {
		target = target.AddDate(0, 0, -1)
	}
	return target
}

// GetUpcomingOptionExpiry calculates the next Thursday weekly expiry date in IST format (02-Jan-2006)
func GetUpcomingOptionExpiry(t time.Time) string {
	tIST := NormalizeToIST(t)
	daysUntilThursday := (int(time.Thursday) - int(tIST.Weekday()) + 7) % 7
	if daysUntilThursday == 0 && tIST.Hour() >= 15 {
		daysUntilThursday = 7
	}
	expiryDate := tIST.AddDate(0, 0, daysUntilThursday)
	return expiryDate.Format("02-Jan-2006")
}

var (
	monthlyOptionRegex = regexp.MustCompile(`^[A-Z]+(\d{2})([A-Z]{3})\d+(CE|PE)$`)
	weeklyOptionRegex  = regexp.MustCompile(`^[A-Z]+(\d{2})([1-9OND])(\d{2})\d+(CE|PE)$`)
)

// ParseOptionExpiryFromSymbol extracts and resolves the expiry date (YYYY-MM-DD) from an option symbol
func ParseOptionExpiryFromSymbol(symbol string) string {
	cleanSym := strings.ToUpper(strings.TrimSpace(symbol))
	if cleanSym == "" {
		return ""
	}

	expiryDay := time.Thursday
	if strings.HasPrefix(cleanSym, "BANKNIFTY") {
		expiryDay = time.Wednesday
	} else if strings.HasPrefix(cleanSym, "SENSEX") {
		expiryDay = time.Friday
	} else if strings.HasPrefix(cleanSym, "FINNIFTY") {
		expiryDay = time.Tuesday
	} else if strings.HasPrefix(cleanSym, "MIDCP") {
		expiryDay = time.Monday
	}

	// 1. Monthly Symbol: e.g. NIFTY26AUG24700CE, FINNIFTY26SEP26000PE, SENSEX26AUG80000CE
	if matches := monthlyOptionRegex.FindStringSubmatch(cleanSym); len(matches) == 4 {
		var yr int
		fmt.Sscanf(matches[1], "%d", &yr)
		yr += 2000
		monthMap := map[string]time.Month{
			"JAN": time.January, "FEB": time.February, "MAR": time.March,
			"APR": time.April, "MAY": time.May, "JUN": time.June,
			"JUL": time.July, "AUG": time.August, "SEP": time.September,
			"OCT": time.October, "NOV": time.November, "DEC": time.December,
		}
		if m, ok := monthMap[matches[2]]; ok {
			nextMonthFirst := time.Date(yr, m+1, 1, 0, 0, 0, 0, ISTLocation)
			lastDay := nextMonthFirst.AddDate(0, 0, -1)
			for lastDay.Weekday() != expiryDay {
				lastDay = lastDay.AddDate(0, 0, -1)
			}
			return lastDay.Format("2006-01-02")
		}
	}

	// 2. Weekly Symbol: e.g. NIFTY2690124350PE, NIFTY26O1324700CE (Year 26, Month 9/O/N/D, Day 01-31)
	if matches := weeklyOptionRegex.FindStringSubmatch(cleanSym); len(matches) == 5 {
		var yr, day int
		fmt.Sscanf(matches[1], "%d", &yr)
		yr += 2000
		fmt.Sscanf(matches[3], "%d", &day)
		mStr := matches[2]
		var month int
		switch mStr {
		case "O":
			month = 10
		case "N":
			month = 11
		case "D":
			month = 12
		default:
			fmt.Sscanf(mStr, "%d", &month)
		}
		if month >= 1 && month <= 12 && day >= 1 && day <= 31 {
			return fmt.Sprintf("%04d-%02d-%02d", yr, month, day)
		}
	}

	return ""
}

// ParseTimeHM parses an "HH:MM" (24-hour) string into integer hour and minute
func ParseTimeHM(timeStr string) (int, int, error) {
	h, m, _, err := ParseTimeHMS(timeStr)
	return h, m, err
}

// ParseTimeHMS parses an "HH:MM:SS", "HH:MM", or "HH" (24-hour) string into hour, minute, second
func ParseTimeHMS(timeStr string) (int, int, int, error) {
	var h, m, s int
	parts := strings.Split(strings.TrimSpace(timeStr), ":")
	if len(parts) == 0 || (len(parts) == 1 && parts[0] == "") {
		return 0, 0, 0, fmt.Errorf("empty time string")
	}
	if _, err := fmt.Sscanf(parts[0], "%d", &h); err != nil {
		return 0, 0, 0, err
	}
	if len(parts) >= 2 {
		if _, err := fmt.Sscanf(parts[1], "%d", &m); err != nil {
			return 0, 0, 0, err
		}
	}
	if len(parts) >= 3 {
		fmt.Sscanf(parts[2], "%d", &s)
	}
	return h, m, s, nil
}

// NormalizeTimeHHMMSS formats any "HH:MM" or "HH:MM:SS" into standard "HH:MM:SS" (e.g., "15:20" -> "15:20:00")
func NormalizeTimeHHMMSS(timeStr string) string {
	timeStr = strings.TrimSpace(timeStr)
	if timeStr == "" {
		return ""
	}
	parts := strings.Split(timeStr, ":")
	var h, m, s int
	if len(parts) >= 1 {
		fmt.Sscanf(parts[0], "%d", &h)
	}
	if len(parts) >= 2 {
		fmt.Sscanf(parts[1], "%d", &m)
	}
	if len(parts) >= 3 {
		fmt.Sscanf(parts[2], "%d", &s)
	}
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// NormalizeCandleTimeframe ensures a timeframe string is normalized to standard format (e.g. "1m", "5m", "15m")
// and prevents corruption from HH:MM:SS normalization (e.g. "05:00:00" -> "5m", "01:00:00" -> "1m").
func NormalizeCandleTimeframe(tf string) string {
	tf = strings.TrimSpace(strings.ToLower(tf))
	if tf == "" {
		return "5m"
	}
	if tf == "05:00:00" || tf == "5m" || tf == "5" || tf == "5min" || tf == "5-min" || tf == "00:05:00" {
		return "5m"
	}
	if tf == "01:00:00" || tf == "1m" || tf == "1" || tf == "1min" || tf == "1-min" || tf == "00:01:00" {
		return "1m"
	}
	if tf == "15:00:00" || tf == "15m" || tf == "15" || tf == "15min" || tf == "15-min" || tf == "00:15:00" {
		return "15m"
	}
	return tf
}

// IsClockTimeConfigKey returns true if a config key corresponds to a clock time of day (HH:MM:SS) rather than a duration (in minutes), timeframe, or percentage.
func IsClockTimeConfigKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if strings.Contains(k, "timeframe") || strings.Contains(k, "holding_time") || strings.Contains(k, "time_decay") || strings.Contains(k, "poll_minutes") {
		return false
	}
	return strings.HasSuffix(k, "_time") ||
		strings.Contains(k, "allowed_before") ||
		strings.Contains(k, "allowed_after") ||
		strings.Contains(k, "broad_agg_start") ||
		strings.Contains(k, "broad_agg_end") ||
		strings.Contains(k, "trade_end") ||
		strings.Contains(k, "auto_square") ||
		strings.Contains(k, "stock_select_time") ||
		strings.Contains(k, "execution_time") ||
		strings.Contains(k, "cutoff_time")
}

// ParseTimeToSeconds parses an "HH:MM:SS" or "HH:MM" (24-hour) string into total seconds of day
func ParseTimeToSeconds(timeStr string) int {
	var h, m, s int
	parts := strings.Split(strings.TrimSpace(timeStr), ":")
	if len(parts) >= 2 {
		fmt.Sscanf(parts[0], "%d", &h)
		fmt.Sscanf(parts[1], "%d", &m)
		if len(parts) >= 3 {
			fmt.Sscanf(parts[2], "%d", &s)
		}
	}
	return h*3600 + m*60 + s
}

// IsTradingDay returns true if the given time falls on a weekday (Monday through Friday)
func IsTradingDay(t time.Time) bool {
	w := NormalizeToIST(t).Weekday()
	return w != time.Saturday && w != time.Sunday
}

// IsMarketOpen checks if the given time falls within normal NSE market hours (Mon-Fri 09:15 to 15:30 IST)
func IsMarketOpen(t time.Time) bool {
	tIST := NormalizeToIST(t)
	if !IsTradingDay(tIST) {
		return false
	}
	marketStart := time.Date(tIST.Year(), tIST.Month(), tIST.Day(), 9, 15, 0, 0, ISTLocation)
	marketEnd := time.Date(tIST.Year(), tIST.Month(), tIST.Day(), 15, 30, 0, 0, ISTLocation)
	return !tIST.Before(marketStart) && !tIST.After(marketEnd)
}
