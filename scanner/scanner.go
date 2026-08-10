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

// RunScan executes full daily quant scan across all NSE Cash & F&O stocks
func (s *QuantScanner) RunScan(ctx context.Context) ([]ScanResult, error) {
	s.logger.Info("Starting Quant Stock Scanner across all NSE Cash & F&O stocks universe...", zap.Int("momentum_days", s.momentumDays))

	// Fetch F&O stock set to categorize segments (F&O vs Cash)
	foStocksMap, _ := s.secMaster.GetFOStocks(ctx)
	if foStocksMap == nil {
		foStocksMap = make(map[string]int64)
	}

	// Fetch all NSE Cash Market & F&O stocks from SecurityMaster
	allStocks, err := s.secMaster.GetAllNSEStocks(ctx)
	if err != nil || len(allStocks) == 0 {
		allStocks = foStocksMap
	}
	if allStocks == nil {
		allStocks = make(map[string]int64)
	}
	// Always include Benchmarks & Commodities (NIFTY 50, GOLD, CRUDEOIL) for Option/Futures trade evaluation
	allStocks["NIFTY 50"] = 256265
	allStocks["GOLD"] = 53491975
	allStocks["CRUDEOIL"] = 53493767

	s.logger.Info("Scanning NSE Cash & F&O stocks universe, Indices & Commodities", zap.Int("total_symbols", len(allStocks)))

	var results []ScanResult
	var mu sync.Mutex
	var wg sync.WaitGroup

	semaphore := make(chan struct{}, 10) // Limit concurrent API calls to 10 workers

	for symbol, token := range allStocks {
		wg.Add(1)
		semaphore <- struct{}{}

		go func(sym string, tok int64) {
			defer wg.Done()
			defer func() { <-semaphore }()

			res, ok := s.analyzeStock(ctx, sym, tok, foStocksMap)
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

func (s *QuantScanner) analyzeStock(ctx context.Context, symbol string, token int64, foStocksMap map[string]int64) (ScanResult, bool) {
	isMacro := (symbol == "NIFTY 50" || symbol == "GOLD" || symbol == "CRUDEOIL" || token == 256265 || token == 53491975 || token == 53493767)

	segment := "CASH"
	if symbol == "NIFTY 50" {
		segment = "INDEX"
	} else if symbol == "GOLD" || symbol == "CRUDEOIL" {
		segment = "COMMODITY"
	} else if _, isFO := foStocksMap[symbol]; isFO {
		segment = "F&O"
	}

	var candles []data.Candle

	// 1. Load stored daily candles from DB (candles_1d table)
	if s.db != nil {
		candles, _ = s.db.GetRecentDailyCandlesByToken(ctx, token, 365)
	}

	// 2. If DB has fewer than 200 daily candles, seed 1-year daily history from Zerodha API
	if len(candles) < 200 && s.brokerClient != nil {
		endTime := time.Now()
		startTime := endTime.AddDate(-1, 0, 0)
		hist, err := s.brokerClient.GetHistoricalData(int(token), "day", startTime, endTime, false, false)
		if err == nil && len(hist) > 0 {
			var fetched []data.Candle
			for _, h := range hist {
				fetched = append(fetched, data.Candle{
					Token:  token,
					Time:   h.Date,
					Open:   h.Open,
					High:   h.High,
					Low:    h.Low,
					Close:  h.Close,
					Volume: int64(h.Volume),
				})
			}
			candles = fetched
			if s.db != nil {
				_ = s.db.UpsertDailyCandles(ctx, candles)
			}
		}
	}

	// 3. Intraday Live Market Hours: Append today's building daily candle up to current time
	nowIST := data.NormalizeToIST(time.Now())
	todayStr := nowIST.Format("2006-01-02")

	hasTodayCandle := false
	if len(candles) > 0 {
		lastCandleDate := data.NormalizeToIST(candles[len(candles)-1].Time).Format("2006-01-02")
		if lastCandleDate == todayStr {
			hasTodayCandle = true
		}
	}

	if !hasTodayCandle && s.db != nil {
		if c5m, err := s.db.GetRecentCandlesByToken(ctx, token, 100); err == nil && len(c5m) > 0 {
			todayDaily := buildTodayLiveDailyCandle(c5m, todayStr)
			if !todayDaily.Time.IsZero() {
				candles = append(candles, todayDaily)
			}
		}
	}

	// 4. Fallback: If daily history is missing (e.g., unseeded candles_1d & expired broker token), aggregate DB 5m candles into daily candles
	if len(candles) < 3 && s.db != nil {
		if c5m, err := s.db.GetRecentCandlesByToken(ctx, token, 1000); err == nil && len(c5m) > 0 {
			candles = aggregate5mToDaily(c5m)
		}
	}

	if len(candles) < 2 {
		return ScanResult{}, false
	}

	// 4. Breakout / Breakdown Identification on Daily Candles
	latest := candles[len(candles)-1]
	prevCandles := candles[:len(candles)-1]

	// All-Time High/Low across available history
	allTimeHigh, allTimeLow := getHighLow(prevCandles, len(prevCandles))
	// 52-Week (Yearly) High/Low (252 trading days)
	yearlyHigh, yearlyLow := getHighLow(prevCandles, 252)
	// Monthly (20 trading days) High/Low
	monthlyHigh, monthlyLow := getHighLow(prevCandles, 20)
	// Weekly (5 trading days) High/Low
	weeklyHigh, weeklyLow := getHighLow(prevCandles, 5)

	breakout := NoBreakout
	direction := "NEUTRAL"

	has1YearData := len(prevCandles) >= 180
	has1MonthData := len(prevCandles) >= 15

	if len(prevCandles) > 0 && (latest.Close > allTimeHigh || latest.High > allTimeHigh) {
		breakout = AllTimeHighBreak
		direction = "BULLISH"
	} else if has1YearData && (latest.Close > yearlyHigh || latest.High > yearlyHigh) {
		breakout = YearlyHighBreak
		direction = "BULLISH"
	} else if has1MonthData && (latest.Close > monthlyHigh || latest.High > monthlyHigh) {
		breakout = MonthlyHighBreak
		direction = "BULLISH"
	} else if latest.Close > weeklyHigh || latest.High > weeklyHigh {
		breakout = WeeklyHighBreak
		direction = "BULLISH"
	} else if len(prevCandles) > 0 && (latest.Close < allTimeLow || latest.Low < allTimeLow) {
		breakout = AllTimeLowBreak
		direction = "BEARISH"
	} else if has1YearData && (latest.Close < yearlyLow || latest.Low < yearlyLow) {
		breakout = YearlyLowBreak
		direction = "BEARISH"
	} else if has1MonthData && (latest.Close < monthlyLow || latest.Low < monthlyLow) {
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

	// 3. Compute Quant Decision & Confidence Score
	quantDir, confScore, recAct := computeQuantDecision(symbol, breakout, direction, pct3D, newsSentiment, volMult, isMacro)

	return ScanResult{
		Symbol:           symbol,
		Segment:          segment,
		Token:            token,
		BreakoutType:     breakout,
		Direction:        direction,
		MomentumDays:     s.momentumDays,
		PctChange1D:      pct1D,
		PctChange3D:      pct3D,
		RangePctChange:   rangePct,
		YearlyHigh:       math.Round(yearlyHigh*100) / 100,
		YearlyLow:        math.Round(yearlyLow*100) / 100,
		AllTimeHigh:      math.Round(allTimeHigh*100) / 100,
		AllTimeLow:       math.Round(allTimeLow*100) / 100,
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
	case AllTimeHighBreak:
		score += 35.0
	case YearlyHighBreak:
		score += 25.0
	case MonthlyHighBreak:
		score += 18.0
	case WeeklyHighBreak:
		score += 10.0
	case AllTimeLowBreak:
		score -= 35.0
	case YearlyLowBreak:
		score -= 25.0
	case MonthlyLowBreak:
		score -= 18.0
	case WeeklyLowBreak:
		score -= 10.0
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

func aggregate5mToDaily(c5m []data.Candle) []data.Candle {
	if len(c5m) == 0 {
		return nil
	}

	type dayAgg struct {
		date   time.Time
		open   float64
		high   float64
		low    float64
		close  float64
		volume int64
		token  int64
	}

	var days []*dayAgg
	dayMap := make(map[string]*dayAgg)

	for _, c := range c5m {
		dStr := data.NormalizeToIST(c.Time).Format("2006-01-02")
		agg, exists := dayMap[dStr]
		if !exists {
			agg = &dayAgg{
				date:   data.NormalizeToIST(c.Time),
				open:   c.Open,
				high:   c.High,
				low:    c.Low,
				close:  c.Close,
				volume: c.Volume,
				token:  c.Token,
			}
			dayMap[dStr] = agg
			days = append(days, agg)
		} else {
			if c.High > agg.high {
				agg.high = c.High
			}
			if c.Low < agg.low {
				agg.low = c.Low
			}
			agg.close = c.Close
			agg.volume += c.Volume
		}
	}

	var res []data.Candle
	for _, d := range days {
		res = append(res, data.Candle{
			Token:  d.token,
			Time:   d.date,
			Open:   d.open,
			High:   d.high,
			Low:    d.low,
			Close:  d.close,
			Volume: d.volume,
		})
	}
	return res
}

func buildTodayLiveDailyCandle(c5m []data.Candle, todayStr string) data.Candle {
	var todayCandles []data.Candle
	for _, c := range c5m {
		if data.NormalizeToIST(c.Time).Format("2006-01-02") == todayStr {
			todayCandles = append(todayCandles, c)
		}
	}
	if len(todayCandles) == 0 {
		return data.Candle{}
	}

	first := todayCandles[0]
	last := todayCandles[len(todayCandles)-1]

	high := first.High
	low := first.Low
	var vol int64

	for _, c := range todayCandles {
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
		Time:   data.NormalizeToIST(first.Time),
		Open:   first.Open,
		High:   high,
		Low:    low,
		Close:  last.Close,
		Volume: vol,
	}
}
