package scanner

import (
	"math"
	"sort"
	"strconv"
	"time"

	"zerodha-trading/data"
	"zerodha-trading/strategy"
)

// ClusterConfig holds configuration parameters for the cluster detection algorithm
type ClusterConfig struct {
	EMAFastPeriod        int     `json:"ema_fast_period"`        // Default: 10
	EMAMidPeriod         int     `json:"ema_mid_period"`         // Default: 20
	EMASlowPeriod        int     `json:"ema_slow_period"`        // Default: 89
	ClusterRadiusPoints  float64 `json:"cluster_radius_points"`  // Default: 2.0 points
	ClusterMaxSpreadPct  float64 `json:"cluster_max_spread_pct"` // Default: 1.0%
	ST1Period            int     `json:"st1_period"`             // Default: 10
	ST1Multiplier        float64 `json:"st1_multiplier"`         // Default: 4.0
	ST2Period            int     `json:"st2_period"`             // Default: 7
	ST2Multiplier        float64 `json:"st2_multiplier"`         // Default: 3.0
	ST3Period            int     `json:"st3_period"`             // Default: 7
	ST3Multiplier        float64 `json:"st3_multiplier"`         // Default: 2.0
	DailyClusterEnabled  bool    `json:"daily_cluster_enabled"`  // Default: true
	WeeklyClusterEnabled bool    `json:"weekly_cluster_enabled"` // Default: true
}

// DefaultClusterConfig returns default recommended cluster parameters
func DefaultClusterConfig() ClusterConfig {
	return ClusterConfig{
		EMAFastPeriod:        10,
		EMAMidPeriod:         20,
		EMASlowPeriod:        89,
		ClusterRadiusPoints:  2.0,
		ClusterMaxSpreadPct:  1.0,
		ST1Period:            10,
		ST1Multiplier:        4.0,
		ST2Period:            7,
		ST2Multiplier:        3.0,
		ST3Period:            7,
		ST3Multiplier:        2.0,
		DailyClusterEnabled:  true,
		WeeklyClusterEnabled: true,
	}
}

// ClusterConfigFromMap extracts cluster config from system settings map
func ClusterConfigFromMap(sysMap map[string]string) ClusterConfig {
	cfg := DefaultClusterConfig()
	if sysMap == nil {
		return cfg
	}

	if v, ok := sysMap["cluster_ema_fast"]; ok && v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			cfg.EMAFastPeriod = p
		}
	}
	if v, ok := sysMap["cluster_ema_mid"]; ok && v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			cfg.EMAMidPeriod = p
		}
	}
	if v, ok := sysMap["cluster_ema_slow"]; ok && v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			cfg.EMASlowPeriod = p
		}
	}
	if v, ok := sysMap["cluster_radius_points"]; ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.ClusterRadiusPoints = f
		}
	}
	if v, ok := sysMap["cluster_max_spread_pct"]; ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.ClusterMaxSpreadPct = f
		}
	}
	if v, ok := sysMap["cluster_st1_period"]; ok && v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			cfg.ST1Period = p
		}
	}
	if v, ok := sysMap["cluster_st1_multiplier"]; ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.ST1Multiplier = f
		}
	}
	if v, ok := sysMap["cluster_st2_period"]; ok && v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			cfg.ST2Period = p
		}
	}
	if v, ok := sysMap["cluster_st2_multiplier"]; ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.ST2Multiplier = f
		}
	}
	if v, ok := sysMap["cluster_st3_period"]; ok && v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			cfg.ST3Period = p
		}
	}
	if v, ok := sysMap["cluster_st3_multiplier"]; ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.ST3Multiplier = f
		}
	}
	if v, ok := sysMap["cluster_daily_enabled"]; ok && v == "false" {
		cfg.DailyClusterEnabled = false
	}
	if v, ok := sysMap["cluster_weekly_enabled"]; ok && v == "false" {
		cfg.WeeklyClusterEnabled = false
	}

	return cfg
}

// ClusterMetrics holds computed cluster geometry and indicator levels
type ClusterMetrics struct {
	IsCluster    bool      `json:"is_cluster"`
	CenterPrice  float64   `json:"center_price"`
	Radius       float64   `json:"radius"`
	SpreadPoints float64   `json:"spread_points"`
	SpreadPct    float64   `json:"spread_pct"`
	EMA10        float64   `json:"ema_10"`
	EMA20        float64   `json:"ema_20"`
	EMA89        float64   `json:"ema_89"`
	ST1          float64   `json:"st1"`
	ST2          float64   `json:"st2"`
	ST3          float64   `json:"st3"`
	STTrend      string    `json:"st_trend"`
	Timeframe    string    `json:"timeframe"` // "DAILY" or "WEEKLY"
	CandleTime   time.Time `json:"candle_time"`
}

// AggregateDailyToWeekly aggregates chronologically sorted daily candles into weekly candles (Monday-Friday)
func AggregateDailyToWeekly(dailyCandles []data.Candle) []data.Candle {
	if len(dailyCandles) == 0 {
		return nil
	}

	// Sort chronologically (oldest to newest)
	sorted := make([]data.Candle, len(dailyCandles))
	copy(sorted, dailyCandles)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Time.Before(sorted[j].Time)
	})

	var weekly []data.Candle
	var currentWeek []data.Candle
	var currentYear, currentWeekNum int

	for _, c := range sorted {
		y, w := c.Time.ISOWeek()
		if len(currentWeek) == 0 {
			currentYear = y
			currentWeekNum = w
			currentWeek = append(currentWeek, c)
		} else if y == currentYear && w == currentWeekNum {
			currentWeek = append(currentWeek, c)
		} else {
			if len(currentWeek) > 0 {
				weekly = append(weekly, buildWeeklyCandle(currentWeek))
			}
			currentYear = y
			currentWeekNum = w
			currentWeek = []data.Candle{c}
		}
	}
	if len(currentWeek) > 0 {
		weekly = append(weekly, buildWeeklyCandle(currentWeek))
	}
	return weekly
}

func buildWeeklyCandle(candles []data.Candle) data.Candle {
	first := candles[0]
	last := candles[len(candles)-1]

	high := first.High
	low := first.Low
	var vol int64

	for _, c := range candles {
		if c.High > high {
			high = c.High
		}
		if c.Low < low {
			low = c.Low
		}
		vol += c.Volume
	}

	return data.Candle{
		Token:  first.Token,
		Time:   last.Time,
		Open:   first.Open,
		High:   high,
		Low:    low,
		Close:  last.Close,
		Volume: vol,
	}
}

// EvaluateCluster evaluates whether EMA (10, 20, 89) and Triple SuperTrends fall within a cluster circle radius
func EvaluateCluster(candles []data.Candle, cfg ClusterConfig, timeframe string) (bool, ClusterMetrics) {
	minCandlesRequired := 10
	if len(candles) < minCandlesRequired {
		return false, ClusterMetrics{Timeframe: timeframe}
	}

	// Ensure chronological sort
	sort.Slice(candles, func(i, j int) bool {
		return candles[i].Time.Before(candles[j].Time)
	})

	n := len(candles)
	latest := candles[n-1]

	closes := make([]float64, n)
	for i, c := range candles {
		closes[i] = c.Close
	}

	// 1. Calculate EMAs
	fastPeriod := cfg.EMAFastPeriod
	if fastPeriod <= 0 {
		fastPeriod = 10
	}
	midPeriod := cfg.EMAMidPeriod
	if midPeriod <= 0 {
		midPeriod = 20
	}
	slowPeriod := cfg.EMASlowPeriod
	if slowPeriod <= 0 {
		slowPeriod = 89
	}

	emaFastSeries := calculateEMASeries(closes, fastPeriod)
	emaMidSeries := calculateEMASeries(closes, midPeriod)
	emaSlowSeries := calculateEMASeries(closes, slowPeriod)

	var emaFastVal, emaMidVal, emaSlowVal float64
	if len(emaFastSeries) > 0 {
		emaFastVal = emaFastSeries[len(emaFastSeries)-1]
	}
	if len(emaMidSeries) > 0 {
		emaMidVal = emaMidSeries[len(emaMidSeries)-1]
	}
	if len(emaSlowSeries) > 0 {
		emaSlowVal = emaSlowSeries[len(emaSlowSeries)-1]
	}

	// 2. Calculate Triple SuperTrends
	st1P := cfg.ST1Period
	if st1P <= 0 {
		st1P = 10
	}
	st1M := cfg.ST1Multiplier
	if st1M <= 0 {
		st1M = 4.0
	}
	st2P := cfg.ST2Period
	if st2P <= 0 {
		st2P = 7
	}
	st2M := cfg.ST2Multiplier
	if st2M <= 0 {
		st2M = 3.0
	}
	st3P := cfg.ST3Period
	if st3P <= 0 {
		st3P = 7
	}
	st3M := cfg.ST3Multiplier
	if st3M <= 0 {
		st3M = 2.0
	}

	stEngine := strategy.NewSuperTrendOptionsEngine(st1P, st2P, st3P, st1M, st2M, st3M)
	stRes := stEngine.CalculateTripleSuperTrend(candles)

	st1Val := stRes.ST1.Value
	st2Val := stRes.ST2.Value
	st3Val := stRes.ST3.Value
	stTrend := stRes.Trend

	// Gather indicator points for cluster geometry
	var vals []float64
	if emaFastVal > 0 {
		vals = append(vals, emaFastVal)
	}
	if emaMidVal > 0 {
		vals = append(vals, emaMidVal)
	}
	if emaSlowVal > 0 {
		vals = append(vals, emaSlowVal)
	}
	if st1Val > 0 {
		vals = append(vals, st1Val)
	}
	if st2Val > 0 {
		vals = append(vals, st2Val)
	}
	if st3Val > 0 {
		vals = append(vals, st3Val)
	}

	if len(vals) < 3 {
		return false, ClusterMetrics{
			Timeframe:  timeframe,
			CandleTime: latest.Time,
			EMA10:      emaFastVal,
			EMA20:      emaMidVal,
			EMA89:      emaSlowVal,
			ST1:        st1Val,
			ST2:        st2Val,
			ST3:        st3Val,
			STTrend:    stTrend,
		}
	}

	minVal := vals[0]
	maxVal := vals[0]
	for _, v := range vals {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}

	spread := maxVal - minVal
	center := (maxVal + minVal) / 2.0
	radius := spread / 2.0

	spreadPct := 0.0
	if latest.Close > 0 {
		spreadPct = math.Round((spread/latest.Close)*100.0*100.0) / 100.0
	}

	// Cluster threshold check:
	// A cluster is confirmed if radius <= cfg.ClusterRadiusPoints OR spreadPct <= cfg.ClusterMaxSpreadPct
	radiusTarget := cfg.ClusterRadiusPoints
	if radiusTarget <= 0 {
		radiusTarget = 2.0
	}

	isCluster := false
	if radius <= radiusTarget {
		isCluster = true
	} else if cfg.ClusterMaxSpreadPct > 0 && spreadPct <= cfg.ClusterMaxSpreadPct {
		isCluster = true
	}

	metrics := ClusterMetrics{
		IsCluster:    isCluster,
		CenterPrice:  math.Round(center*100.0) / 100.0,
		Radius:       math.Round(radius*100.0) / 100.0,
		SpreadPoints: math.Round(spread*100.0) / 100.0,
		SpreadPct:    spreadPct,
		EMA10:        math.Round(emaFastVal*100.0) / 100.0,
		EMA20:        math.Round(emaMidVal*100.0) / 100.0,
		EMA89:        math.Round(emaSlowVal*100.0) / 100.0,
		ST1:          math.Round(st1Val*100.0) / 100.0,
		ST2:          math.Round(st2Val*100.0) / 100.0,
		ST3:          math.Round(st3Val*100.0) / 100.0,
		STTrend:      stTrend,
		Timeframe:    timeframe,
		CandleTime:   latest.Time,
	}

	return isCluster, metrics
}

func calculateEMASeries(closes []float64, period int) []float64 {
	if len(closes) == 0 || period <= 0 {
		return nil
	}
	emas := make([]float64, len(closes))
	k := 2.0 / float64(period+1)

	if len(closes) >= period {
		sum := 0.0
		for i := 0; i < period; i++ {
			sum += closes[i]
		}
		emas[period-1] = sum / float64(period)

		for i := 0; i < period-1; i++ {
			emas[i] = closes[i]
		}

		for i := period; i < len(closes); i++ {
			emas[i] = (closes[i] * k) + (emas[i-1] * (1.0 - k))
		}
	} else {
		emas[0] = closes[0]
		for i := 1; i < len(closes); i++ {
			emas[i] = (closes[i] * k) + (emas[i-1] * (1.0 - k))
		}
	}
	return emas
}

