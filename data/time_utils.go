package data

import (
	"fmt"
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

// ParseTimeHM parses an "HH:MM" (24-hour) string into integer hour and minute
func ParseTimeHM(timeStr string) (int, int, error) {
	var h, m int
	_, err := fmt.Sscanf(timeStr, "%d:%d", &h, &m)
	if err != nil {
		return 0, 0, err
	}
	return h, m, nil
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
