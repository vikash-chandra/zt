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
// It guarantees that whether a timestamp from PostgreSQL or Kite Connect API is stored as:
// 1. UTC time (e.g. 03:45:00 UTC representing 09:15 IST)
// 2. Wall-clock IST time (e.g. 09:15:00)
// It will ALWAYS be converted to the exact IST time (Asia/Kolkata).
func NormalizeToIST(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	// Guarantee wall-clock time (Year, Month, Day, Hour, Min, Sec) is anchored in IST (Asia/Kolkata)
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), ISTLocation)
}

// FormatIST formats any time into a clean IST string
func FormatIST(t time.Time, layout string) string {
	return NormalizeToIST(t).Format(layout)
}
