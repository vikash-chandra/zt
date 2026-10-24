package scanner

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// Classification constants for futures open interest buildup
const (
	BuildupLongBuildup   = "LONG_BUILDUP"
	BuildupShortBuildup  = "SHORT_BUILDUP"
	BuildupShortCovering = "SHORT_COVERING"
	BuildupLongUnwinding = "LONG_UNWINDING"
	BuildupNeutral       = "NEUTRAL"
)

// Option selling action recommendations
const (
	ActionSellOTMPut     = "SELL_OTM_PUT"
	ActionSellOTMCall    = "SELL_OTM_CALL"
	ActionShortStrangle  = "SHORT_STRANGLE"
	ActionNoTrade        = "NO_TRADE"
)

// FOAnalyzer manages real-time +/- ATM strike open interest aggregation and option selling predictions
type FOAnalyzer struct {
	db     *data.Database
	logger *zap.Logger

	mu              sync.RWMutex
	summaries       map[string]data.FOSummaryScore
	futuresBuildups []data.FuturesBuildupRecord
	lastUpdateTime  time.Time
}

// NewFOAnalyzer creates a new FOAnalyzer instance
func NewFOAnalyzer(db *data.Database, logger *zap.Logger) *FOAnalyzer {
	return &FOAnalyzer{
		db:        db,
		logger:    logger,
		summaries: make(map[string]data.FOSummaryScore),
	}
}

// RoundToNearestStrike calculates the nearest ATM strike price based on the stock's strike step interval
func RoundToNearestStrike(spotPrice float64, step float64) float64 {
	if step <= 0 {
		return spotPrice
	}
	return math.Round(spotPrice/step) * step
}

// ClassifyFuturesBuildup evaluates price change vs OI change for institutional futures direction
func ClassifyFuturesBuildup(
	spotPrice float64,
	futuresPrice float64,
	prevFuturesPrice float64,
	currentOI int64,
	prevOI int64,
) (buildupType string, basisPct, oiChgPct, priceChgPct float64) {
	if spotPrice > 0 {
		basisPct = math.Round(((futuresPrice-spotPrice)/spotPrice)*10000) / 100
	}

	if prevFuturesPrice > 0 {
		priceChgPct = math.Round(((futuresPrice-prevFuturesPrice)/prevFuturesPrice)*10000) / 100
	}

	if prevOI > 0 {
		oiChgPct = math.Round((float64(currentOI-prevOI)/float64(prevOI))*10000) / 100
	}

	if priceChgPct > 0.05 && oiChgPct > 0.5 {
		buildupType = BuildupLongBuildup
	} else if priceChgPct < -0.05 && oiChgPct > 0.5 {
		buildupType = BuildupShortBuildup
	} else if priceChgPct > 0.05 && oiChgPct < -0.5 {
		buildupType = BuildupShortCovering
	} else if priceChgPct < -0.05 && oiChgPct < -0.5 {
		buildupType = BuildupLongUnwinding
	} else {
		buildupType = BuildupNeutral
	}

	return buildupType, basisPct, oiChgPct, priceChgPct
}

// CalculateStrikeMetrics computes NetStrikeBias, sentiment, and strike-level aggregates
func CalculateStrikeMetrics(
	strikePrice float64,
	atmOffset int,
	callOI int64,
	callOIChange int64,
	putOI int64,
	putOIChange int64,
	callLTP float64,
	putLTP float64,
) data.FOStrikeRow {
	netBias := putOIChange - callOIChange

	sentiment := "NEUTRAL"
	if netBias > 25000 {
		sentiment = "BULLISH_SUPPORT"
	} else if netBias < -25000 {
		sentiment = "BEARISH_RESISTANCE"
	}

	return data.FOStrikeRow{
		StrikePrice:   strikePrice,
		CallOI:        callOI,
		CallOIChange:  callOIChange,
		PutOI:         putOI,
		PutOIChange:   putOIChange,
		CallLTP:       callLTP,
		PutLTP:        putLTP,
		NetStrikeBias: netBias,
		ATMOffset:     atmOffset,
		Sentiment:     sentiment,
	}
}

// CalculateInstitutionalScore evaluates the normalized -100 to +100 institutional bias score across ATM cluster
func CalculateInstitutionalScore(strikes []data.FOStrikeRow) (score float64, deltaPCR float64, totalPCR float64) {
	if len(strikes) == 0 {
		return 0.0, 1.0, 1.0
	}

	var sumPutOI, sumCallOI int64
	var sumPutOIChg, sumCallOIChg int64

	for _, s := range strikes {
		sumPutOI += s.PutOI
		sumCallOI += s.CallOI
		sumPutOIChg += s.PutOIChange
		sumCallOIChg += s.CallOIChange
	}

	// 1. Total Put-Call Ratio
	if sumCallOI > 0 {
		totalPCR = math.Round((float64(sumPutOI)/float64(sumCallOI))*100) / 100
	} else {
		totalPCR = 1.0
	}

	// 2. Delta Put-Call Ratio (Intraday writing aggression)
	absPutChg := math.Max(0, float64(sumPutOIChg))
	absCallChg := math.Max(0, float64(sumCallOIChg))
	if absCallChg > 0 {
		deltaPCR = math.Round((absPutChg/absCallChg)*100) / 100
	} else if absPutChg > 0 {
		deltaPCR = 3.0
	} else {
		deltaPCR = 1.0
	}

	// 3. Net Institutional Bias Score (-100 to +100)
	netDelta := float64(sumPutOIChg - sumCallOIChg)
	totalDelta := float64(sumPutOIChg + sumCallOIChg)
	if totalDelta != 0 {
		rawScore := (netDelta / math.Abs(totalDelta)) * 100.0
		if rawScore > 100.0 {
			rawScore = 100.0
		} else if rawScore < -100.0 {
			rawScore = -100.0
		}
		score = math.Round(rawScore*10) / 10
	}

	return score, deltaPCR, totalPCR
}

// FindWallsAndMaxPain detects the Call Wall (highest Call OI), Put Wall (highest Put OI), and Max Pain Strike
func FindWallsAndMaxPain(strikes []data.FOStrikeRow) (callWall float64, putWall float64, maxPain float64) {
	if len(strikes) == 0 {
		return 0, 0, 0
	}

	var maxCallOI int64 = -1
	var maxPutOI int64 = -1

	for _, s := range strikes {
		if s.CallOI > maxCallOI {
			maxCallOI = s.CallOI
			callWall = s.StrikePrice
		}
		if s.PutOI > maxPutOI {
			maxPutOI = s.PutOI
			putWall = s.StrikePrice
		}
	}

	// Calculate Max Pain: strike where option buyers lose maximum money (sellers lose least)
	minTotalLoss := math.MaxFloat64
	for _, trial := range strikes {
		totalLoss := 0.0
		for _, s := range strikes {
			// Call writers payout if trial price > strike
			if trial.StrikePrice > s.StrikePrice {
				totalLoss += float64(s.CallOI) * (trial.StrikePrice - s.StrikePrice)
			}
			// Put writers payout if trial price < strike
			if trial.StrikePrice < s.StrikePrice {
				totalLoss += float64(s.PutOI) * (s.StrikePrice - trial.StrikePrice)
			}
		}
		if totalLoss < minTotalLoss {
			minTotalLoss = totalLoss
			maxPain = trial.StrikePrice
		}
	}

	return callWall, putWall, maxPain
}

// GenerateOptionSellingTrade generates concrete actionable trade parameters for option sellers
func GenerateOptionSellingTrade(
	score float64,
	deltaPCR float64,
	strikes []data.FOStrikeRow,
	spotPrice float64,
	callWall float64,
	putWall float64,
) (action string, recStrike float64, recType string, recPremium float64, sl float64, target float64, reason string) {
	if len(strikes) == 0 {
		return ActionNoTrade, 0, "", 0, 0, 0, "No strike data available"
	}

	// Case 1: Strong Bullish Put Writing
	if score >= 35.0 && deltaPCR >= 1.25 {
		// Identify strike with highest Put OI Change or Put Wall below spot
		targetStrike := putWall
		var bestPutRow *data.FOStrikeRow
		for i := range strikes {
			if strikes[i].StrikePrice <= spotPrice {
				if bestPutRow == nil || strikes[i].PutOIChange > bestPutRow.PutOIChange {
					bestPutRow = &strikes[i]
					targetStrike = strikes[i].StrikePrice
				}
			}
		}

		premium := 0.0
		if bestPutRow != nil && bestPutRow.PutLTP > 0 {
			premium = bestPutRow.PutLTP
		}

		slPrem := math.Round(premium*1.40*100) / 100   // 40% premium increase
		tgtPrem := math.Round(premium*0.10*100) / 100  // 90% decay

		reason = fmt.Sprintf("Institutional Put Writing cluster (+%.1f score, PCR %.2f) guarantees strong floor at %.0f", score, deltaPCR, targetStrike)
		return ActionSellOTMPut, targetStrike, "PE", premium, slPrem, tgtPrem, reason
	}

	// Case 2: Strong Bearish Call Writing
	if score <= -35.0 && deltaPCR <= 0.75 {
		// Identify strike with highest Call OI Change or Call Wall above spot
		targetStrike := callWall
		var bestCallRow *data.FOStrikeRow
		for i := range strikes {
			if strikes[i].StrikePrice >= spotPrice {
				if bestCallRow == nil || strikes[i].CallOIChange > bestCallRow.CallOIChange {
					bestCallRow = &strikes[i]
					targetStrike = strikes[i].StrikePrice
				}
			}
		}

		premium := 0.0
		if bestCallRow != nil && bestCallRow.CallLTP > 0 {
			premium = bestCallRow.CallLTP
		}

		slPrem := math.Round(premium*1.40*100) / 100
		tgtPrem := math.Round(premium*0.10*100) / 100

		reason = fmt.Sprintf("Institutional Call Writing ceiling (-%.1f score, PCR %.2f) capping upside at %.0f", math.Abs(score), deltaPCR, targetStrike)
		return ActionSellOTMCall, targetStrike, "CE", premium, slPrem, tgtPrem, reason
	}

	// Case 3: Neutral Rangebound -> Short Strangle Setup
	if math.Abs(score) <= 15.0 && callWall > spotPrice && putWall < spotPrice {
		reason = fmt.Sprintf("Balanced institutional straddle pegging between Put Wall %.0f and Call Wall %.0f", putWall, callWall)
		return ActionShortStrangle, putWall, "STRANGLE", 0.0, 0.0, 0.0, reason
	}

	return ActionNoTrade, 0, "", 0, 0, 0, "No strong asymmetric writing dominance detected"
}

// BuildFOSummary creates a full FOSummaryScore object
func (fa *FOAnalyzer) BuildFOSummary(
	symbol string,
	spotPrice float64,
	atmStrike float64,
	expiryDate string,
	futuresTrend string,
	strikes []data.FOStrikeRow,
	now time.Time,
) data.FOSummaryScore {
	score, deltaPCR, totalPCR := CalculateInstitutionalScore(strikes)
	callWall, putWall, maxPain := FindWallsAndMaxPain(strikes)
	action, recStrike, recType, recPrem, slPrem, tgtPrem, reason := GenerateOptionSellingTrade(score, deltaPCR, strikes, spotPrice, callWall, putWall)

	summary := data.FOSummaryScore{
		Symbol:             symbol,
		SpotPrice:          spotPrice,
		ATMStrike:          atmStrike,
		ExpiryDate:         expiryDate,
		InstitutionalScore: score,
		DeltaPCR:           deltaPCR,
		TotalPCR:           totalPCR,
		CallWallStrike:     callWall,
		PutWallStrike:      putWall,
		MaxPainStrike:      maxPain,
		FuturesTrend:       futuresTrend,
		OptionSellAction:   action,
		RecommendedStrike:  recStrike,
		RecommendedType:    recType,
		RecommendedPremium: recPrem,
		StopLossPremium:    slPrem,
		TargetPremium:      tgtPrem,
		Reason:             reason,
		Strikes:            strikes,
		UpdatedAt:          data.NormalizeToIST(now),
	}

	fa.mu.Lock()
	fa.summaries[symbol] = summary
	fa.lastUpdateTime = now
	fa.mu.Unlock()

	return summary
}

// GetSummary retrieves the cached summary for a stock
func (fa *FOAnalyzer) GetSummary(symbol string) (data.FOSummaryScore, bool) {
	fa.mu.RLock()
	defer fa.mu.RUnlock()
	res, exists := fa.summaries[symbol]
	return res, exists
}

// GetAllSummaries returns all cached summaries sorted by InstitutionalScore descending
func (fa *FOAnalyzer) GetAllSummaries() []data.FOSummaryScore {
	fa.mu.RLock()
	defer fa.mu.RUnlock()

	list := make([]data.FOSummaryScore, 0, len(fa.summaries))
	for _, s := range fa.summaries {
		list = append(list, s)
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].InstitutionalScore > list[j].InstitutionalScore
	})

	return list
}

// SetFuturesBuildups updates the cached futures buildup list
func (fa *FOAnalyzer) SetFuturesBuildups(records []data.FuturesBuildupRecord) {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	fa.futuresBuildups = records
}

// GetFuturesBuildups returns the cached futures buildup list
func (fa *FOAnalyzer) GetFuturesBuildups() []data.FuturesBuildupRecord {
	fa.mu.RLock()
	defer fa.mu.RUnlock()
	return fa.futuresBuildups
}
