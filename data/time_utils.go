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
	if t.Location() == ISTLocation || t.Location().String() == ISTLocation.String() {
		return t
	}
	// If t.Hour() is > 10 (e.g. 11..23), it cannot be a UTC Indian market hour (03:45-10:00 UTC).
	// It is a wall-clock IST time constructed without IST location.
	if t.Hour() > 10 {
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), ISTLocation)
	}
	// Otherwise, it is a UTC timestamp (e.g. 03:45 UTC = 09:15 IST, 09:30 UTC = 15:00 IST)
	return t.In(ISTLocation)
}

// FormatIST formats any time into a clean IST string
func FormatIST(t time.Time, layout string) string {
	return NormalizeToIST(t).Format(layout)
}
