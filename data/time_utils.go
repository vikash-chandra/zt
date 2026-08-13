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
