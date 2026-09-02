package scanner

import (
	"fmt"
	"math"
	"sort"
	"zerodha-trading/data"
)

// Pivot represents a swing pivot high or low on daily candles
type Pivot struct {
	Index  int
	Price  float64
	Time   string
	IsHigh bool
}

// DowStructureResult holds the complete Dow Theory and Positional Setup analysis for a stock
type DowStructureResult struct {
	DowTrend        string  // "UPTREND_HH_HL", "DOWNTREND_LH_LL", "SIDEWAYS_BASE"
	PositionalZone  string  // "PULLBACK_BUY", "BREAKOUT_BUY", "PULLBACK_SELL", "BREAKDOWN_SELL", "ACCUMULATION_BASE", "NEUTRAL"
	ActionTiming    string  // "TODAY_ACTIONABLE", "NEXT_DAY_IMMINENT", "DEVELOPING"
	SelectionReason string  // Human-readable rationale explaining setup, zone, and catalyst
	SupportZone     float64 // Key Demand / Support level
	ResistanceZone  float64 // Key Supply / Resistance level
	EMA20           float64 // 20-Day Exponential Moving Average
	EMA50           float64 // 50-Day Exponential Moving Average
	RecentSwingHigh float64 // Most recent swing high pivot
	RecentSwingLow  float64 // Most recent swing low pivot
	IsNR7           bool    // Narrow Range 7 (volatility compression)
	IsInsideDay     bool    // Inside Day candle
}

// DetectFractalPivots detects Swing Highs and Swing Lows using 2-bar left, 2-bar right local extrema
func DetectFractalPivots(candles []data.Candle) (highs []Pivot, lows []Pivot) {
	if len(candles) < 5 {
		return nil, nil
	}

	for i := 2; i < len(candles)-2; i++ {
		curr := candles[i]
		// Swing High: higher than 2 bars before and 2 bars after
		if curr.High > candles[i-1].High && curr.High > candles[i-2].High &&
			curr.High > candles[i+1].High && curr.High > candles[i+2].High {
			highs = append(highs, Pivot{
				Index:  i,
				Price:  curr.High,
				Time:   curr.Time.Format("2006-01-02"),
				IsHigh: true,
			})
		}

		// Swing Low: lower than 2 bars before and 2 bars after
		if curr.Low < candles[i-1].Low && curr.Low < candles[i-2].Low &&
			curr.Low < candles[i+1].Low && curr.Low < candles[i+2].Low {
			lows = append(lows, Pivot{
				Index:  i,
				Price:  curr.Low,
				Time:   curr.Time.Format("2006-01-02"),
				IsHigh: false,
			})
		}
	}

	return highs, lows
}

// CalculateEMA computes exponential moving average on closing prices
func CalculateEMA(candles []data.Candle, period int) float64 {
	if len(candles) == 0 {
		return 0
	}
	if len(candles) < period {
		// Fallback to simple average
		sum := 0.0
		for _, c := range candles {
			sum += c.Close
		}
		return sum / float64(len(candles))
	}

	// Calculate initial SMA for first period
	sma := 0.0
	for i := 0; i < period; i++ {
		sma += candles[i].Close
	}
	ema := sma / float64(period)
	multiplier := 2.0 / float64(period+1)

	for i := period; i < len(candles); i++ {
		ema = (candles[i].Close-ema)*multiplier + ema
	}

	return ema
}

// EvaluateDowStructure evaluates the complete Dow Theory trend, support/resistance zones, positional strategy, and action timing
func EvaluateDowStructure(candles []data.Candle, vol1D int64, volADV int64) DowStructureResult {
	res := DowStructureResult{
		DowTrend:       "SIDEWAYS_BASE",
		PositionalZone: "NEUTRAL",
		ActionTiming:   "DEVELOPING",
	}

	if len(candles) < 5 {
		return res
	}

	// Make a defensive copy to prevent any in-place mutation or race conditions across concurrent callers
	c := make([]data.Candle, len(candles))
	copy(c, candles)

	// Ensure chronological sort
	sort.Slice(c, func(i, j int) bool {
		return c[i].Time.Before(c[j].Time)
	})

	latest := c[len(c)-1]
	currentPrice := latest.Close
	if currentPrice <= 0 {
		currentPrice = latest.Open
	}

	prev := c[len(c)-2]

	// 1. Calculate EMAs
	res.EMA20 = math.Round(CalculateEMA(c, 20)*100) / 100
	res.EMA50 = math.Round(CalculateEMA(c, 50)*100) / 100

	// 2. Detect Fractal Pivots
	swingHighs, swingLows := DetectFractalPivots(c)

	var lastSH, prevSH, lastSL, prevSL float64
	if len(swingHighs) >= 1 {
		lastSH = swingHighs[len(swingHighs)-1].Price
		res.RecentSwingHigh = lastSH
	}
	if len(swingHighs) >= 2 {
		prevSH = swingHighs[len(swingHighs)-2].Price
	}
	if len(swingLows) >= 1 {
		lastSL = swingLows[len(swingLows)-1].Price
		res.RecentSwingLow = lastSL
	}
	if len(swingLows) >= 2 {
		prevSL = swingLows[len(swingLows)-2].Price
	}

	// If pivots are insufficient, derive from recent lookbacks
	if lastSH == 0 {
		h, _ := getHighLow(c[:len(c)-1], 20)
		lastSH = h
		res.RecentSwingHigh = lastSH
	}
	if lastSL == 0 {
		_, l := getHighLow(c[:len(c)-1], 20)
		lastSL = l
		res.RecentSwingLow = lastSL
	}

	// 3. Classify Dow Theory Trend Structure
	isHigherHighs := (lastSH > prevSH) || (prevSH == 0 && currentPrice > res.EMA20)
	isHigherLows := (lastSL > prevSL) || (prevSL == 0 && lastSL > res.EMA50)

	isLowerHighs := (lastSH < prevSH && lastSH > 0 && prevSH > 0)
	isLowerLows := (lastSL < prevSL && lastSL > 0 && prevSL > 0)

	if isHigherHighs && isHigherLows && currentPrice >= res.EMA50 {
		res.DowTrend = "UPTREND_HH_HL"
	} else if isLowerHighs && isLowerLows && currentPrice <= res.EMA50 {
		res.DowTrend = "DOWNTREND_LH_LL"
	} else if currentPrice > res.EMA20 && res.EMA20 > res.EMA50 {
		res.DowTrend = "UPTREND_HH_HL"
	} else if currentPrice < res.EMA20 && res.EMA20 < res.EMA50 {
		res.DowTrend = "DOWNTREND_LH_LL"
	} else {
		res.DowTrend = "SIDEWAYS_BASE"
	}

	// 4. Define Support & Resistance Zones
	// Support Zone: Nearest key level below current price (Swing Low, 20 EMA, or Prior Resistance flipped to Support)
	if res.DowTrend == "UPTREND_HH_HL" {
		res.SupportZone = math.Max(lastSL, res.EMA20)
		if res.SupportZone > currentPrice && lastSL > 0 {
			res.SupportZone = lastSL
		}
		res.ResistanceZone = math.Max(lastSH, currentPrice*1.02)
	} else if res.DowTrend == "DOWNTREND_LH_LL" {
		res.ResistanceZone = math.Min(lastSH, res.EMA20)
		if res.ResistanceZone < currentPrice && lastSH > 0 {
			res.ResistanceZone = lastSH
		}
		res.SupportZone = math.Min(lastSL, currentPrice*0.98)
	} else {
		res.SupportZone = lastSL
		res.ResistanceZone = lastSH
	}

	res.SupportZone = math.Round(res.SupportZone*100) / 100
	res.ResistanceZone = math.Round(res.ResistanceZone*100) / 100

	// 5. Detect Volatility Compression: Inside Day & NR7
	res.IsInsideDay = (latest.High <= prev.High && latest.Low >= prev.Low)

	// NR7: Today's range (High - Low) is narrower than each of the prior 6 days (total 7 days)
	todayRange := latest.High - latest.Low
	if len(c) >= 7 && todayRange > 0 {
		isSmallest := true
		for k := len(c) - 7; k < len(c)-1; k++ {
			cRange := c[k].High - c[k].Low
			if todayRange >= cRange {
				isSmallest = false
				break
			}
		}
		res.IsNR7 = isSmallest
	}

	// Volume Surge Multiplier
	volMult := 1.0
	if volADV > 0 && vol1D > 0 {
		volMult = math.Round((float64(vol1D)/float64(volADV))*100.0) / 100.0
	}

	// Proximity calculations
	distToSupportPct := 999.0
	if res.SupportZone > 0 {
		distToSupportPct = math.Abs((currentPrice - res.SupportZone) / res.SupportZone * 100.0)
	}

	distToResistancePct := 999.0
	if res.ResistanceZone > 0 {
		distToResistancePct = math.Abs((res.ResistanceZone - currentPrice) / res.ResistanceZone * 100.0)
	}

	// 6. Identify Positional Strategy Zone & Action Timing

	// Condition A: BREAKOUT BUY (Resistance expansion)
	if (lastSH > 0 && latest.High >= lastSH) || (distToResistancePct <= 0.8 && currentPrice >= lastSH*0.992) {
		res.PositionalZone = "BREAKOUT_BUY"
		if latest.Close >= lastSH || volMult >= 1.3 {
			res.ActionTiming = "TODAY_ACTIONABLE"
			res.SelectionReason = fmt.Sprintf("Resistance Breakout: Surged above Swing High ₹%.2f (Vol %.1fx ADV); active momentum breakout today.", lastSH, volMult)
		} else if res.IsNR7 || res.IsInsideDay || distToResistancePct <= 0.5 {
			res.ActionTiming = "NEXT_DAY_IMMINENT"
			res.SelectionReason = fmt.Sprintf("Pre-Breakout Coil: Resting within 0.5%% of Swing High ₹%.2f with volatility compression; primed for Next-Day (T+1) breakout.", lastSH)
		} else {
			res.ActionTiming = "TODAY_ACTIONABLE"
			res.SelectionReason = fmt.Sprintf("Resistance Test: Approaching Swing High ₹%.2f with bullish momentum; watch for immediate breakout.", lastSH)
		}
		return res
	}

	// Condition B: PULLBACK BUY (Demand Zone / 20 EMA dip in HH+HL Uptrend)
	if res.DowTrend == "UPTREND_HH_HL" && (distToSupportPct <= 2.5 || (res.EMA20 > 0 && math.Abs(currentPrice-res.EMA20)/res.EMA20*100.0 <= 2.0)) {
		res.PositionalZone = "PULLBACK_BUY"
		// Check candle shape: bounce candle (green or lower wick rejection)
		lowerWick := math.Min(latest.Open, latest.Close) - latest.Low
		totalRange := latest.High - latest.Low
		isBounce := (latest.Close >= latest.Open) || (totalRange > 0 && lowerWick/totalRange >= 0.35)

		if isBounce && (volMult >= 1.2 || latest.Close > prev.Close) {
			res.ActionTiming = "TODAY_ACTIONABLE"
			res.SelectionReason = fmt.Sprintf("Bullish Dow Uptrend (HH+HL): Pullback to Demand Zone / 20 EMA (₹%.2f) confirmed with bullish bounce candle today.", res.SupportZone)
		} else if res.IsNR7 || res.IsInsideDay || distToSupportPct <= 1.0 {
			res.ActionTiming = "NEXT_DAY_IMMINENT"
			res.SelectionReason = fmt.Sprintf("Bullish Dow Uptrend (HH+HL): Resting at Demand Support (₹%.2f) with NR7 volatility compression; primed for Next-Day (T+1) reversal.", res.SupportZone)
		} else {
			res.ActionTiming = "DEVELOPING"
			res.SelectionReason = fmt.Sprintf("Bullish Dow Uptrend (HH+HL): Retracing towards 20 EMA Support (₹%.2f); developing pullback buy zone.", res.SupportZone)
		}
		return res
	}

	// Condition C: BREAKDOWN SELL (Support failure in Downtrend)
	if (lastSL > 0 && latest.Low <= lastSL) || (distToSupportPct <= 0.8 && currentPrice <= lastSL*1.008 && res.DowTrend == "DOWNTREND_LH_LL") {
		res.PositionalZone = "BREAKDOWN_SELL"
		if latest.Close <= lastSL || volMult >= 1.3 {
			res.ActionTiming = "TODAY_ACTIONABLE"
			res.SelectionReason = fmt.Sprintf("Support Breakdown: Slipped below Swing Low ₹%.2f (Vol %.1fx ADV); active breakdown trigger today.", lastSL, volMult)
		} else if res.IsNR7 || res.IsInsideDay || distToSupportPct <= 0.5 {
			res.ActionTiming = "NEXT_DAY_IMMINENT"
			res.SelectionReason = fmt.Sprintf("Pre-Breakdown Coil: Pressuring Swing Low ₹%.2f with volatility compression; primed for Next-Day (T+1) breakdown.", lastSL)
		} else {
			res.ActionTiming = "TODAY_ACTIONABLE"
			res.SelectionReason = fmt.Sprintf("Support Test: Testing Swing Low ₹%.2f under selling pressure; watch for breakdown.", lastSL)
		}
		return res
	}

	// Condition D: PULLBACK SELL (Supply Zone / 20 EMA rally in LH+LL Downtrend)
	if res.DowTrend == "DOWNTREND_LH_LL" && (distToResistancePct <= 2.5 || (res.EMA20 > 0 && math.Abs(currentPrice-res.EMA20)/res.EMA20*100.0 <= 2.0)) {
		res.PositionalZone = "PULLBACK_SELL"
		upperWick := latest.High - math.Max(latest.Open, latest.Close)
		totalRange := latest.High - latest.Low
		isRejection := (latest.Close <= latest.Open) || (totalRange > 0 && upperWick/totalRange >= 0.35)

		if isRejection && (volMult >= 1.2 || latest.Close < prev.Close) {
			res.ActionTiming = "TODAY_ACTIONABLE"
			res.SelectionReason = fmt.Sprintf("Bearish Dow Downtrend (LH+LL): Retraced to 20 EMA Supply Zone (₹%.2f) with upper wick rejection today; prime positional sell.", res.ResistanceZone)
		} else if res.IsNR7 || res.IsInsideDay || distToResistancePct <= 1.0 {
			res.ActionTiming = "NEXT_DAY_IMMINENT"
			res.SelectionReason = fmt.Sprintf("Bearish Dow Downtrend (LH+LL): Retracing into 20 EMA Supply (₹%.2f) with NR7 compression; primed for Next-Day (T+1) downward continuation.", res.ResistanceZone)
		} else {
			res.ActionTiming = "DEVELOPING"
			res.SelectionReason = fmt.Sprintf("Bearish Dow Downtrend (LH+LL): Retracing towards 20 EMA Supply (₹%.2f); developing pullback sell zone.", res.ResistanceZone)
		}
		return res
	}

	// Condition E: ACCUMULATION BASE / CONSOLIDATION
	if res.DowTrend == "SIDEWAYS_BASE" && distToSupportPct <= 2.0 && lastSL > 0 {
		res.PositionalZone = "ACCUMULATION_BASE"
		if res.IsNR7 || res.IsInsideDay {
			res.ActionTiming = "NEXT_DAY_IMMINENT"
			res.SelectionReason = fmt.Sprintf("Accumulation Base: Testing multi-touch Support ₹%.2f with NR7 volatility squeeze; primed for Next-Day (T+1) expansion.", res.SupportZone)
		} else {
			res.ActionTiming = "DEVELOPING"
			res.SelectionReason = fmt.Sprintf("Accumulation Base: Consolidating near base support ₹%.2f; building positional demand.", res.SupportZone)
		}
		return res
	}

	// Default Fallback
	if res.DowTrend == "UPTREND_HH_HL" {
		res.SelectionReason = fmt.Sprintf("Bullish Dow Uptrend (HH+HL): Trending above 20 EMA (₹%.2f); Support ₹%.2f, Resistance ₹%.2f.", res.EMA20, res.SupportZone, res.ResistanceZone)
	} else if res.DowTrend == "DOWNTREND_LH_LL" {
		res.SelectionReason = fmt.Sprintf("Bearish Dow Downtrend (LH+LL): Trending below 20 EMA (₹%.2f); Resistance ₹%.2f, Support ₹%.2f.", res.EMA20, res.ResistanceZone, res.SupportZone)
	} else {
		res.SelectionReason = fmt.Sprintf("Sideways Consolidation: Rangebound between Support ₹%.2f and Resistance ₹%.2f.", res.SupportZone, res.ResistanceZone)
	}

	return res
}
