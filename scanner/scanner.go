package scanner

import (
	"context"
	"math"
	"sync"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

type QuantScanner struct {
	db           *data.Database
	secMaster    *data.SecurityMaster
	brokerClient data.BrokerClient
	news         *NewsAggregator
	logger       *zap.Logger
	momentumDays int
	newsEnabled  bool
}

func NewQuantScanner(
	db *data.Database,
	secMaster *data.SecurityMaster,
	brokerClient data.BrokerClient,
	logger *zap.Logger,
	momentumDays int,
	newsEnabled bool,
) *QuantScanner {
	if momentumDays <= 0 {
		momentumDays = 3
	}
	return &QuantScanner{
		db:           db,
		secMaster:    secMaster,
		brokerClient: brokerClient,
		news:         NewNewsAggregator(),
		logger:       logger,
		momentumDays: momentumDays,
		newsEnabled:  newsEnabled,
	}
}

// RunScan executes full daily quant scan across all F&O stocks
func (s *QuantScanner) RunScan(ctx context.Context) ([]ScanResult, error) {
	s.logger.Info("Starting Quant Stock Scanner across F&O universe...", zap.Int("momentum_days", s.momentumDays))

	// 1. Fetch all F&O stocks from SecurityMaster
	foStocks, err := s.secMaster.GetFOStocks(ctx)
	if err != nil {
		foStocks = make(map[string]int64)
	}
	// Always include Benchmarks & Commodities (NIFTY 50, GOLD, CRUDEOIL) for Option/Futures trade evaluation
	foStocks["NIFTY 50"] = 256265
	foStocks["GOLD"] = 53491975
	foStocks["CRUDEOIL"] = 53493767

	s.logger.Info("Scanning F&O stocks universe, Indices & Commodities", zap.Int("total_symbols", len(foStocks)))

	var results []ScanResult
	var mu sync.Mutex
	var wg sync.WaitGroup

	semaphore := make(chan struct{}, 10) // Limit concurrent API calls to 10 workers

	for symbol, token := range foStocks {
		wg.Add(1)
		semaphore <- struct{}{}

		go func(sym string, tok int64) {
			defer wg.Done()
			defer func() { <-semaphore }()

			res, ok := s.analyzeStock(ctx, sym, tok)
			if ok {
				mu.Lock()
				results = append(results, res)
				mu.Unlock()
			}
		}(symbol, token)
	}

	wg.Wait()

	s.logger.Info("Quant Stock Scanner completed", zap.Int("total_opportunities_found", len(results)))
	return results, nil
}

func (s *QuantScanner) analyzeStock(ctx context.Context, symbol string, token int64) (ScanResult, bool) {
	isMacro := (symbol == "NIFTY 50" || symbol == "GOLD" || symbol == "CRUDEOIL" || token == 256265 || token == 53491975 || token == 53493767)

	// Query daily candles for stock (last 365 days / 252 trading days for 52W High/Low)
	endTime := time.Now()
	startTime := endTime.AddDate(-1, 0, 0)

	var candles []data.Candle

	// 1. Fetch 1-year daily candles from broker client first (interval "day")
	if s.brokerClient != nil {
		hist, bErr := s.brokerClient.GetHistoricalData(int(token), "day", startTime, endTime, false, false)
		if bErr == nil && len(hist) > 0 {
			for _, h := range hist {
				candles = append(candles, data.Candle{
					Token:  token,
					Time:   h.Date,
					Open:   h.Open,
					High:   h.High,
					Low:    h.Low,
					Close:  h.Close,
					Volume: int64(h.Volume),
				})
			}
			if s.db != nil {
				_ = s.db.UpsertDailyCandles(ctx, candles)
			}
		}
	}

	// 2. Fallback to DB daily candles (candles_1d table) if broker client unavailable
	if len(candles) < 10 && s.db != nil {
		candles, _ = s.db.GetRecentDailyCandlesByToken(ctx, token, 365)
	}

	if len(candles) < 10 {
		return ScanResult{}, false
	}

	// 1. Breakout / Breakdown Identification
	latest := candles[len(candles)-1]
	prevCandles := candles[:len(candles)-1]

	// 52-Week (Yearly) High/Low (252 trading days)
	yearlyHigh, yearlyLow := getHighLow(prevCandles, 252)
	// Monthly (20 trading days) High/Low
	monthlyHigh, monthlyLow := getHighLow(prevCandles, 20)
	// Weekly (5 trading days) High/Low
	weeklyHigh, weeklyLow := getHighLow(prevCandles, 5)

	breakout := NoBreakout
	direction := "NEUTRAL"

	if latest.Close > yearlyHigh || latest.High > yearlyHigh {
		breakout = YearlyHighBreak
		direction = "BULLISH"
	} else if latest.Close > monthlyHigh || latest.High > monthlyHigh {
		breakout = MonthlyHighBreak
		direction = "BULLISH"
	} else if latest.Close > weeklyHigh || latest.High > weeklyHigh {
		breakout = WeeklyHighBreak
		direction = "BULLISH"
	} else if latest.Close < yearlyLow || latest.Low < yearlyLow {
		breakout = YearlyLowBreak
		direction = "BEARISH"
	} else if latest.Close < monthlyLow || latest.Low < monthlyLow {
		breakout = MonthlyLowBreak
		direction = "BEARISH"
	} else if latest.Close < weeklyLow || latest.Low < weeklyLow {
		breakout = WeeklyLowBreak
		direction = "BEARISH"
	}

	// Filter out stocks without a breakout/breakdown unless they exhibit strong momentum
	pct1D := calculatePctChange(candles, 1)
	pct3D := calculatePctChange(candles, s.momentumDays)
	rangePct := calculateRangePctChange(candles, s.momentumDays)

	// Calculate Volume Metrics
	vol1D := latest.Volume
	volADV := calculateADV(prevCandles, 20)
	volMult := 1.0
	if volADV > 0 {
		volMult = math.Round((float64(vol1D)/float64(volADV))*100.0) / 100.0
	}

	hasStrongMomentum := (direction == "BULLISH" && pct3D >= 1.5) || (direction == "BEARISH" && pct3D <= -1.5) || volMult >= 1.8
	if !isMacro && breakout == NoBreakout && !hasStrongMomentum {
		return ScanResult{}, false
	}

	// 2. Fetch News & Sentiment if enabled
	newsSummary := "No news collected"
	newsSentiment := "NEUTRAL"
	var newsItems []NewsItem

	if s.newsEnabled {
		newsQuery := symbol
		if symbol == "NIFTY 50" {
			newsQuery = "Nifty 50"
		}
		newsItems, newsSummary, newsSentiment = s.news.FetchNewsForStock(newsQuery)
	}

	// 3. Compute Quant Direction & Confidence Score
	quantDir, confScore, recAct := computeQuantDecision(symbol, breakout, direction, pct3D, newsSentiment, volMult, isMacro)

	return ScanResult{
		Symbol:           symbol,
		Token:            token,
		BreakoutType:     breakout,
		Direction:        direction,
		MomentumDays:     s.momentumDays,
		PctChange1D:      pct1D,
		PctChange3D:      pct3D,
		RangePctChange:   rangePct,
		YearlyHigh:       math.Round(yearlyHigh*100) / 100,
		YearlyLow:        math.Round(yearlyLow*100) / 100,
		Volume1D:         vol1D,
		VolumeADV:        volADV,
		VolumeMultiplier: volMult,
		ConfidenceScore:  confScore,
		QuantDirection:   quantDir,
		RecommendedAct:   recAct,
		NewsSummary:      newsSummary,
		NewsSentiment:    newsSentiment,
		NewsItems:        newsItems,
		CreatedAt:        time.Now(),
	}, true
}

func getHighLow(candles []data.Candle, lookbackDays int) (float64, float64) {
	high := -1.0
	low := math.MaxFloat64

	n := len(candles)
	startIdx := n - lookbackDays
	if startIdx < 0 {
		startIdx = 0
	}

	for i := startIdx; i < n; i++ {
		if candles[i].High > high {
			high = candles[i].High
		}
		if candles[i].Low < low {
			low = candles[i].Low
		}
	}
	return high, low
}

func calculateADV(candles []data.Candle, days int) int64 {
	n := len(candles)
	if n == 0 {
		return 0
	}
	if n < days {
		days = n
	}
	var total int64
	for i := n - days; i < n; i++ {
		total += candles[i].Volume
	}
	return total / int64(days)
}

func calculatePctChange(candles []data.Candle, days int) float64 {
	if len(candles) <= days {
		return 0.0
	}
	latest := candles[len(candles)-1].Close
	prev := candles[len(candles)-1-days].Close
	if prev == 0 {
		return 0.0
	}
	return math.Round(((latest-prev)/prev)*100.0*100.0) / 100.0
}

func calculateRangePctChange(candles []data.Candle, days int) float64 {
	if len(candles) <= days {
		return 0.0
	}
	sub := candles[len(candles)-1-days:]
	minLow := math.MaxFloat64
	maxHigh := -1.0
	for _, c := range sub {
		if c.Low < minLow {
			minLow = c.Low
		}
		if c.High > maxHigh {
			maxHigh = c.High
		}
	}
	if minLow == 0 || minLow == math.MaxFloat64 {
		return 0.0
	}
	return math.Round(((maxHigh-minLow)/minLow)*100.0*100.0) / 100.0
}

func computeQuantDecision(
	symbol string,
	breakout BreakoutType,
	direction string,
	pct3D float64,
	newsSentiment string,
	volMultiplier float64,
	isMacro bool,
) (QuantDirection, float64, string) {
	score := 50.0 // Base neutral score

	// Breakout score weighting (45%)
	switch breakout {
	case MonthlyHighBreak:
		score += 25.0
	case WeeklyHighBreak:
		score += 15.0
	case MonthlyLowBreak:
		score -= 25.0
	case WeeklyLowBreak:
		score -= 15.0
	}

	// Momentum score weighting (35%)
	score += pct3D * 3.5

	// News sentiment weighting (20%)
	if newsSentiment == "POSITIVE" {
		score += 10.0
	} else if newsSentiment == "NEGATIVE" {
		score -= 10.0
	}

	// Volume surge participation bonus/penalty
	if volMultiplier >= 1.5 {
		if direction == "BULLISH" {
			score += 5.0
		} else if direction == "BEARISH" {
			score -= 5.0
		}
	}

	if score > 100.0 {
		score = 98.5
	}
	if score < 0.0 {
		score = 5.0
	}

	score = math.Round(score*10.0) / 10.0

	// Decision threshold for Macro Indices & Commodities
	if isMacro {
		if symbol == "GOLD" {
			if score >= 60.0 {
				return Bullish, score, "BUY GOLD FUT / PE SELL"
			} else if score <= 40.0 {
				return Bearish, score, "SELL GOLD FUT / CE SELL"
			}
			return Neutral, score, "NO GOLD TRADE"
		}
		if symbol == "CRUDEOIL" {
			if score >= 60.0 {
				return Bullish, score, "BUY CRUDE FUT / PE SELL"
			} else if score <= 40.0 {
				return Bearish, score, "SELL CRUDE FUT / CE SELL"
			}
			return Neutral, score, "NO CRUDE TRADE"
		}
		// Default NIFTY 50 Index
		if score >= 60.0 {
			return Bullish, score, "SELL PE 300-OTM (BULLISH)"
		} else if score <= 40.0 {
			return Bearish, score, "SELL CE 300-OTM (BEARISH)"
		}
		return Neutral, score, "NO OPTION SELL (NEUTRAL)"
	}

	if score >= 75.0 {
		return StrongBullish, score, "BUY_ON_DIP"
	} else if score >= 60.0 {
		return Bullish, score, "ACCUMULATE"
	} else if score <= 25.0 {
		return StrongBearish, score, "SHORT_ON_RALLY"
	} else if score <= 40.0 {
		return Bearish, score, "REDUCE_LONG"
	}
	return Neutral, score, "WATCHLIST_ONLY"
}
