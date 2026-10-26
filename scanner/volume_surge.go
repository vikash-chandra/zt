package scanner

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// Quadrant classification constants for institutional volume vs price action
const (
	QuadrantAccumulationBreakout = "ACCUMULATION_BREAKOUT"
	QuadrantDistributionBreakdown = "DISTRIBUTION_BREAKDOWN"
	QuadrantChurnAbsorption      = "CHURN_ABSORPTION"
	QuadrantExhaustionTrap       = "EXHAUSTION_TRAP"
	QuadrantNormal               = "NORMAL"
)

// VolumeSurgeScanner analyzes multi-day volume anomalies and 4-quadrant institutional patterns
type VolumeSurgeScanner struct {
	db     *data.Database
	logger *zap.Logger

	mu             sync.RWMutex
	baselines      map[string]data.DailyVolumeBaseline
	latestRecords  []data.VolumeSurgeRecord
	lastLoadedDate string
}

// NewVolumeSurgeScanner creates a new scanner instance
func NewVolumeSurgeScanner(db *data.Database, logger *zap.Logger) *VolumeSurgeScanner {
	return &VolumeSurgeScanner{
		db:        db,
		logger:    logger,
		baselines: make(map[string]data.DailyVolumeBaseline),
	}
}

// LoadBaselines fetches the latest daily volume baselines from DB into in-memory cache
func (v *VolumeSurgeScanner) LoadBaselines(ctx context.Context) error {
	if v.db == nil {
		return nil
	}
	baselines, err := v.db.GetLatestVolumeBaselines(ctx)
	if err != nil {
		v.logger.Error("Failed to load volume baselines", zap.Error(err))
		return err
	}

	if len(baselines) == 0 {
		if inserted, autoErr := v.db.EnsureVolumeBaselinesPopulated(ctx); autoErr == nil && inserted > 0 {
			v.logger.Info("Auto-populated daily volume baselines from candles_1d", zap.Int64("inserted", inserted))
			baselines, _ = v.db.GetLatestVolumeBaselines(ctx)
		}
	}

	v.mu.Lock()
	v.baselines = baselines
	v.lastLoadedDate = time.Now().In(data.ISTLocation).Format("2006-01-02")
	v.mu.Unlock()

	v.logger.Info("Loaded daily volume baselines into cache", zap.Int("count", len(baselines)))
	return nil
}

// DetermineQuadrant classifies the volume surge vs price % move into one of the 4 institutional quadrants
func DetermineQuadrant(pctChange float64, multiplier float64, threshold float64) string {
	if threshold <= 0 {
		threshold = 2.0
	}

	// High Volume Surge (>= threshold)
	if multiplier >= threshold {
		if pctChange >= 1.5 {
			return QuadrantAccumulationBreakout
		} else if pctChange <= -1.5 {
			return QuadrantDistributionBreakdown
		} else if math.Abs(pctChange) <= 0.6 {
			return QuadrantChurnAbsorption
		}
		return QuadrantAccumulationBreakout
	}

	// Low Volume Trap: Big price move on light volume
	if multiplier < 0.8 && math.Abs(pctChange) >= 2.0 {
		return QuadrantExhaustionTrap
	}

	return QuadrantNormal
}

// CalculateProjectedRunRate computes the estimated end-of-day volume based on minutes elapsed since 09:15 IST
func CalculateProjectedRunRate(currentVol int64, now time.Time) (int64, float64) {
	nowIST := data.NormalizeToIST(now)
	marketOpen := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 9, 15, 0, 0, data.ISTLocation)
	marketClose := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 15, 30, 0, 0, data.ISTLocation)

	if nowIST.Before(marketOpen) {
		return currentVol, 0.0
	}
	if nowIST.After(marketClose) {
		return currentVol, 1.0
	}

	elapsedMinutes := nowIST.Sub(marketOpen).Minutes()
	if elapsedMinutes < 1 {
		elapsedMinutes = 1
	}
	totalSessionMinutes := 375.0 // 09:15 to 15:30 IST

	fraction := elapsedMinutes / totalSessionMinutes
	if fraction > 1.0 {
		fraction = 1.0
	}

	projectedVol := int64(float64(currentVol) / fraction)
	return projectedVol, fraction
}

// EvaluateSymbol evaluates a single stock's volume metrics against its historical baselines
func (v *VolumeSurgeScanner) EvaluateSymbol(
	symbol string,
	ltp float64,
	prevClose float64,
	volumeToday int64,
	dayHigh float64,
	dayLow float64,
	vwap float64,
	lookbackDays int,
	threshold float64,
	now time.Time,
) data.VolumeSurgeRecord {
	v.mu.RLock()
	baseline, hasBaseline := v.baselines[symbol]
	v.mu.RUnlock()

	pctChange := 0.0
	if prevClose > 0 {
		pctChange = math.Round(((ltp-prevClose)/prevClose)*10000) / 100
	} else if hasBaseline && baseline.ClosePrice > 0 {
		pctChange = math.Round(((ltp-baseline.ClosePrice)/baseline.ClosePrice)*10000) / 100
		prevClose = baseline.ClosePrice
	}

	var targetBaseline int64
	if hasBaseline {
		switch lookbackDays {
		case 10:
			targetBaseline = baseline.AvgVol10D
		case 30:
			targetBaseline = baseline.AvgVol30D
		case 50:
			targetBaseline = baseline.AvgVol50D
		default: // 20-day default
			targetBaseline = baseline.AvgVol20D
		}
	}

	multiplier := 0.0
	if targetBaseline > 0 {
		multiplier = math.Round((float64(volumeToday)/float64(targetBaseline))*100) / 100
	}

	projectedEODVol, _ := CalculateProjectedRunRate(volumeToday, now)
	projectedMultiplier := 0.0
	if targetBaseline > 0 {
		projectedMultiplier = math.Round((float64(projectedEODVol)/float64(targetBaseline))*100) / 100
	}

	dayRangePct := 50.0
	if dayHigh > dayLow && dayLow > 0 {
		dayRangePct = math.Round(((ltp-dayLow)/(dayHigh-dayLow))*10000) / 100
		if dayRangePct < 0 {
			dayRangePct = 0
		} else if dayRangePct > 100 {
			dayRangePct = 100
		}
	}

	quadrant := DetermineQuadrant(pctChange, multiplier, threshold)

	return data.VolumeSurgeRecord{
		Symbol:              symbol,
		LTP:                 ltp,
		PrevClose:           prevClose,
		PctChange:           pctChange,
		VolumeToday:         volumeToday,
		AvgVol10D:           baseline.AvgVol10D,
		AvgVol20D:           baseline.AvgVol20D,
		AvgVol30D:           baseline.AvgVol30D,
		AvgVol50D:           baseline.AvgVol50D,
		Multiplier:          multiplier,
		ProjectedEODVol:     projectedEODVol,
		ProjectedMultiplier: projectedMultiplier,
		DayHigh:             dayHigh,
		DayLow:              dayLow,
		DayRangePct:         dayRangePct,
		VWAP:                vwap,
		Quadrant:            quadrant,
		UpdatedAt:           data.NormalizeToIST(now),
	}
}

// ScanBatch processes a batch of symbol quotes and sorts them by volume multiplier descending
func (v *VolumeSurgeScanner) ScanBatch(
	quotes map[string]data.Quote,
	lookbackDays int,
	threshold float64,
	now time.Time,
) []data.VolumeSurgeRecord {
	if len(quotes) == 0 {
		return nil
	}

	var records []data.VolumeSurgeRecord
	for symbol, q := range quotes {
		if q.LastPrice <= 0 || q.VolumeTraded <= 0 {
			continue
		}
		rec := v.EvaluateSymbol(
			symbol,
			q.LastPrice,
			q.OHLC.Close,
			q.VolumeTraded,
			q.OHLC.High,
			q.OHLC.Low,
			0.0, // VWAP if available in full tick
			lookbackDays,
			threshold,
			now,
		)
		records = append(records, rec)
	}

	// Sort by Multiplier descending (highest volume surge at top)
	sort.Slice(records, func(i, j int) bool {
		return records[i].Multiplier > records[j].Multiplier
	})

	v.mu.Lock()
	v.latestRecords = records
	v.mu.Unlock()

	return records
}

// GetCachedRecords returns the latest evaluated records filtered by optional min multiplier
func (v *VolumeSurgeScanner) GetCachedRecords(minMultiplier float64, quadrantFilter string) []data.VolumeSurgeRecord {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if len(v.latestRecords) == 0 {
		return nil
	}

	var filtered []data.VolumeSurgeRecord
	for _, r := range v.latestRecords {
		if minMultiplier > 0 && r.Multiplier < minMultiplier {
			continue
		}
		if quadrantFilter != "" && r.Quadrant != quadrantFilter {
			continue
		}
		filtered = append(filtered, r)
	}
	return filtered
}
