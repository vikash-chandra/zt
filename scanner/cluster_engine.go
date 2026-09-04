package scanner

import (
	"math"
	"sort"
	"strconv"
	"time"

	"zerodha-trading/data"
)

// ClusterConfig holds configuration parameters for the EMA cluster detection algorithm
type ClusterConfig struct {
	EMAFastPeriod        int     `json:"ema_fast_period"`        // Default: 10
	EMAMidPeriod         int     `json:"ema_mid_period"`         // Default: 20
	EMASlowPeriod        int     `json:"ema_slow_period"`        // Default: 89
	ClusterMaxSpreadPct  float64 `json:"cluster_max_spread_pct"` // Default: 0.1%
	DailyClusterEnabled  bool    `json:"daily_cluster_enabled"`  // Default: true
	WeeklyClusterEnabled bool    `json:"weekly_cluster_enabled"` // Default: true
}

// DefaultClusterConfig returns default recommended cluster parameters
func DefaultClusterConfig() ClusterConfig {
	return ClusterConfig{
		EMAFastPeriod:        10,
		EMAMidPeriod:         20,
		EMASlowPeriod:        89,
		ClusterMaxSpreadPct:  0.1,
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
	if v, ok := sysMap["cluster_max_spread_pct"]; ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.ClusterMaxSpreadPct = f
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

// ClusterMetrics holds computed EMA cluster geometry and indicator levels
type ClusterMetrics struct {
	IsCluster    bool      `json:"is_cluster"`
	CenterPrice  float64   `json:"center_price"`
	Radius       float64   `json:"radius"`
	SpreadPoints float64   `json:"spread_points"`
	SpreadPct    float64   `json:"spread_pct"`
	EMA10        float64   `json:"ema_10"`
	EMA20        float64   `json:"ema_20"`
	EMA89        float64   `json:"ema_89"`
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
	if len(candles) == 0 {
		return data.Candle{}
	}
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

// EvaluateCluster evaluates whether the 3 EMAs (10, 20, 89) fall within a cluster spread percentage
func EvaluateCluster(candles []data.Candle, cfg ClusterConfig, timeframe string) (bool, ClusterMetrics) {
	minCandlesRequired := 10
	if len(candles) < minCandlesRequired {
		return false, ClusterMetrics{Timeframe: timeframe}
	}

	// Defensive copy to prevent in-place mutation or race conditions across concurrent callers
	c := make([]data.Candle, len(candles))
	copy(c, candles)

	// Ensure chronological sort
	sort.Slice(c, func(i, j int) bool {
		return c[i].Time.Before(c[j].Time)
	})

	n := len(c)
	latest := c[n-1]

	closes := make([]float64, n)
	for i, candle := range c {
		closes[i] = candle.Close
	}

	// 1. Calculate EMAs (EMA 10, EMA 20, EMA 89)
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

	if emaFastVal <= 0 || emaMidVal <= 0 || emaSlowVal <= 0 {
		return false, ClusterMetrics{
			Timeframe:  timeframe,
			CandleTime: latest.Time,
			EMA10:      emaFastVal,
			EMA20:      emaMidVal,
			EMA89:      emaSlowVal,
		}
	}

	vals := []float64{emaFastVal, emaMidVal, emaSlowVal}
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
	// A cluster is confirmed strictly if spreadPct <= cfg.ClusterMaxSpreadPct (default: 0.1%)
	maxSpreadTarget := cfg.ClusterMaxSpreadPct
	if maxSpreadTarget <= 0 {
		maxSpreadTarget = 0.1
	}

	isCluster := spreadPct <= maxSpreadTarget

	metrics := ClusterMetrics{
		IsCluster:    isCluster,
		CenterPrice:  math.Round(center*100.0) / 100.0,
		Radius:       math.Round(radius*100.0) / 100.0,
		SpreadPoints: math.Round(spread*100.0) / 100.0,
		SpreadPct:    spreadPct,
		EMA10:        math.Round(emaFastVal*100.0) / 100.0,
		EMA20:        math.Round(emaMidVal*100.0) / 100.0,
		EMA89:        math.Round(emaSlowVal*100.0) / 100.0,
		Timeframe:    timeframe,
		CandleTime:   latest.Time,
	}

	return isCluster, metrics
}

// calculateEMASeries calculates Exponential Moving Average series matching Zerodha Kite / TradingView ta.ema
func calculateEMASeries(closes []float64, period int) []float64 {
	if len(closes) == 0 || period <= 0 {
		return nil
	}
	emas := make([]float64, len(closes))
	k := 2.0 / float64(period+1)

	if len(closes) < period {
		emas[0] = closes[0]
		for i := 1; i < len(closes); i++ {
			emas[i] = (closes[i] * k) + (emas[i-1] * (1.0 - k))
		}
		return emas
	}

	// First period bars: calculate cumulative SMA
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += closes[i]
		emas[i] = sum / float64(i+1)
	}

	// From index period onward, apply recursive EMA formula
	for i := period; i < len(closes); i++ {
		emas[i] = (closes[i] * k) + (emas[i-1] * (1.0 - k))
	}
	return emas
}
