package data

import "time"

// ISTLocation is the centralized time location for Indian Standard Time (Asia/Kolkata)
var ISTLocation *time.Location

func init() {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.FixedZone("IST", 5*3600+30*60)
	}
	ISTLocation = loc
}

// NormalizeToIST centralizes time normalization across the entire application.
// It guarantees that any timestamp (UTC from DB/Kite API or wall-clock IST) is
// cleanly converted to exact IST time (Asia/Kolkata).
func NormalizeToIST(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), ISTLocation)
}

// FormatIST formats any time into a clean IST string
func FormatIST(t time.Time, layout string) string {
	return NormalizeToIST(t).Format(layout)
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
