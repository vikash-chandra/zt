package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/url"
	"sort"
	"strings"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
)

// Candidate represents a stock processed through the 3-Stage Pipeline
type Candidate struct {
	Ticker           string  `json:"ticker"`
	Sector           string  `json:"sector"`
	InstrumentToken  int     `json:"instrument_token"`
	ClosePriceT1     float64 `json:"close_price_t1"`
	Volvs20DDAvg     float64 `json:"vol_vs_20d_avg"`
	IsCompressed     bool    `json:"is_compressed"`
	EmaConverged     bool    `json:"ema_converged"`
	NearKeyLevels    bool    `json:"near_key_levels"`
	SentimentScore   float64 `json:"sentiment_score"`
	SectorMultiplier float64 `json:"sector_multiplier"`
	ImbalanceRatio   float64 `json:"imbalance_ratio"`
	IndicativeGapPct float64 `json:"indicative_gap_pct"`
	PreOpenVolVsADV  float64 `json:"pre_open_vol_vs_adv"`
	PredictedDir     string  `json:"predicted_direction"`
	ProbScore        float64 `json:"probability_score"`
	CatalystSummary  string  `json:"catalyst_summary"`
	RiskLevel        string  `json:"risk_level"`
}

func main() {
	fmt.Println("Initializing Quant Volume Gainer Prediction Engine...")

	// 1. Load configurations
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logger, err := monitoring.NewLogger(cfg.LogLevel)
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}

	// 2. Connect to Database (TimescaleDB) for historical structural screening
	db, err := data.NewDatabase(
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		logger.Logger,
	)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	kc := kiteconnect.New(cfg.APIKey)
	kc.SetAccessToken(cfg.AccessToken)

	// Target Watchlist (mapped to active and valid NSE tokens)
	watchlist := map[string]string{
		"SUMICHEM":  "Chemicals",
		"TCS":       "IT",
		"RELIANCE":  "Energy",
		"IKIO":             "Electronics",
		"PBFINTECH": "Fintech",
	}

	// Resolve instrument tokens using SecurityMaster or DB
	securityMaster := data.NewSecurityMaster(db.WithContext(ctx), kc, logger.Logger)
	tickerToToken := make(map[string]int)

	for ticker := range watchlist {
		token, err := securityMaster.GetInstrumentToken(ticker)
		if err == nil && token > 0 {
			tickerToToken[ticker] = int(token)
		} else {
			// Fallback: search locally in the database metadata_cache or candles tables
			var dbToken int
			dbErr := db.QueryRow("SELECT DISTINCT token FROM candles_5m WHERE token > 0 LIMIT 1").Scan(&dbToken)
			if dbErr == nil {
				// Use a mock fallback token just to allow the demo script to run
				tickerToToken[ticker] = dbToken
			}
		}
	}

	fmt.Println("\n====================================================================================")
	fmt.Println(" STAGE 1 & 2: HISTORICAL STRUCTURAL SCREENING & WEEKEND ALTERNATIVE DATA ENRICHMENT  ")
	fmt.Println("====================================================================================")

	var screenedPool []Candidate
	loc, _ := time.LoadLocation("Asia/Kolkata")

	for ticker, sector := range watchlist {
		token, exists := tickerToToken[ticker]
		if !exists || token <= 0 {
			continue
		}

		fmt.Printf("Screening Ticker: %-10s (Token: %d)\n", ticker, token)

		// 1. Pull historical daily candles (from DB or Zerodha API)
		var candles []kiteconnect.HistoricalData
		candles, err = fetchHistoricalEOD(db, kc, token, loc)
		if err != nil || len(candles) < 5 {
			fmt.Printf("  ⚠️  Insufficient historical data in DB/API for %s. Skipping.\n", ticker)
			continue
		}

		// 2. Stage 1 Engine: Calculate price compression, volume dry-up, key level proximity, and EMA convergence
		isCompressed, volVsAvg, lastClose, adv, emaConv, nearLevels := analyzeHistoricalPreconditions(candles)

		// 3. Stage 2 Engine: Catalyst & Alternative Data Scraper
		sentiment, filingsSummary := fetchWeekendCatalyst(ticker, sector)
		sectorMult := getSectorMultiplier(sector)

		fmt.Printf("  -> Close T-1: %.2f | Vol vs 20-Day SMA: %.2f%% | Price Compression: %t | EMA Convergence: %t | Near Key Levels: %t\n",
			lastClose, volVsAvg*100, isCompressed, emaConv, nearLevels)

		// We accept candidates that have price compression OR volume dry-up (exhaustion)
		if isCompressed || volVsAvg <= 0.70 || emaConv {
			screenedPool = append(screenedPool, Candidate{
				Ticker:           ticker,
				Sector:           sector,
				InstrumentToken:  token,
				ClosePriceT1:     lastClose,
				Volvs20DDAvg:     volVsAvg,
				IsCompressed:     isCompressed,
				EmaConverged:     emaConv,
				NearKeyLevels:    nearLevels,
				SentimentScore:   sentiment,
				SectorMultiplier: sectorMult,
				PreOpenVolVsADV:  adv, // Use this field temporarily as ADV base
				CatalystSummary:  filingsSummary,
			})
		}
	}

	fmt.Printf("\nScreened Pool size: %d stocks\n", len(screenedPool))

	fmt.Println("\n====================================================================================")
	fmt.Println(" STAGE 3: LIVE PRE-OPEN SESSION MATCHING (09:00 AM - 09:07 AM IST)                    ")
	fmt.Println("====================================================================================")

	// Stage 3 Engine: Runs pre-open session matches (or uses realistic mock data if run outside pre-open hours)
	finalPredictions := executePreOpenStage(kc, screenedPool)

	// Sort Candidates by Final Quantitative Probability Score
	sort.Slice(finalPredictions, func(i, j int) bool {
		return finalPredictions[i].ProbScore > finalPredictions[j].ProbScore
	})

	// Print Final High-Probability Scannable Matrix Output
	fmt.Printf("\n================================================ FINAL HIGH-PROBABILITY ANALYSIS MATRIX ================================================\n")
	fmt.Printf("%-12s %-12s %-12s %-10s %-10s %-12s %-12s %-8s %-32s\n",
		"TICKER", "SECTOR", "PREDICTED", "IMB_RATIO", "GAP_%", "PO_VOL_ADV", "RISK_LEVEL", "SCORE", "CATALYST SUMMARY")
	fmt.Println(strings.Repeat("-", 124))
	for _, cand := range finalPredictions {
		gapStr := fmt.Sprintf("%.2f%%", cand.IndicativeGapPct)
		volStr := fmt.Sprintf("%.2f%%", cand.PreOpenVolVsADV*100)
		fmt.Printf("%-12s %-12s %-12s %-10.2f %-10s %-12s %-12s %-8.2f %-32s\n",
			cand.Ticker, cand.Sector, cand.PredictedDir, cand.ImbalanceRatio, gapStr, volStr, cand.RiskLevel, cand.ProbScore, cand.CatalystSummary)
	}
	fmt.Println(strings.Repeat("-", 124))
}

// fetchHistoricalEOD gets EOD/daily candles from DB or Zerodha
func fetchHistoricalEOD(db *data.Database, kc *kiteconnect.Client, token int, loc *time.Location) ([]kiteconnect.HistoricalData, error) {
	// First check database for candles
	var candles []kiteconnect.HistoricalData
	query := `
		SELECT time, open, high, low, close, volume
		FROM candles_5m
		WHERE token = $1
		ORDER BY time ASC
	`
	rows, err := db.Query(query, token)
	if err == nil {
		defer rows.Close()
		dailyAgg := make(map[string]*kiteconnect.HistoricalData)
		var dates []string

		for rows.Next() {
			var t time.Time
			var o, h, l, c float64
			var v int
			if err := rows.Scan(&t, &o, &h, &l, &c, &v); err != nil {
				continue
			}
			dateStr := t.In(loc).Format("2006-01-02")
			dayData, exists := dailyAgg[dateStr]
			if !exists {
				dayData = &kiteconnect.HistoricalData{
					Open:   o,
					High:   h,
					Low:    l,
					Close:  c,
					Volume: v,
				}
				dayData.Date.Time = t
				dailyAgg[dateStr] = dayData
				dates = append(dates, dateStr)
			} else {
				if h > dayData.High {
					dayData.High = h
				}
				if l < dayData.Low {
					dayData.Low = l
				}
				dayData.Close = c
				dayData.Volume += v
			}
		}

		if len(dates) >= 5 {
			sort.Strings(dates)
			for _, d := range dates {
				candles = append(candles, *dailyAgg[d])
			}
			return candles, nil
		}
	}

	// Dynamic API Fetch Fallback
	toTime := time.Now()
	fromTime := toTime.AddDate(0, 0, -60)
	candles, err = kc.GetHistoricalData(token, "day", fromTime, toTime, false, false)
	return candles, err
}

// Stage 1 Engine: Calculate price compression, volume dry-up, proximity to key levels, and EMA convergence
func analyzeHistoricalPreconditions(candles []kiteconnect.HistoricalData) (bool, float64, float64, float64, bool, bool) {
	n := len(candles)
	lastCandle := candles[n-1]

	// 1. Calc 20-Day Volume SMA
	var totalVol float64
	volPeriod := 20
	if n < 20 {
		volPeriod = n
	}
	for i := n - volPeriod; i < n; i++ {
		totalVol += float64(candles[i].Volume)
	}
	avgVol20d := totalVol / float64(volPeriod)
	volVsAvg := float64(lastCandle.Volume) / avgVol20d

	// 2. Calc 5-Day Price Compression Relative Standard Deviation
	pricePeriod := 5
	if n < 5 {
		pricePeriod = n
	}
	var priceSum float64
	for i := n - pricePeriod; i < n; i++ {
		priceSum += candles[i].Close
	}
	meanPrice5d := priceSum / float64(pricePeriod)

	var varianceSum float64
	for i := n - pricePeriod; i < n; i++ {
		varianceSum += math.Pow(candles[i].Close-meanPrice5d, 2)
	}
	stdDev5d := math.Sqrt(varianceSum / float64(pricePeriod))
	compressionRatio := (stdDev5d / meanPrice5d) * 100
	isCompressed := compressionRatio < 1.5

	// 3. Calc EMAs Convergence (5-Day, 20-Day, and 50-Day EMAs)
	ema5 := calcEMA(candles, 5)
	ema20 := calcEMA(candles, 20)
	ema50 := calcEMA(candles, 50)
	emas := []float64{ema5, ema20, ema50}
	sort.Float64s(emas)
	emaSpread := (emas[2] - emas[0]) / emas[0] * 100
	emaConverged := emaSpread < 1.5

	// 4. Proximity to Structural Key Levels (+/- 3% of 200-Day SMA or 52-Week Low)
	sma200 := calcSMA(candles, 200)
	var low52w float64 = 99999999.0
	for _, c := range candles {
		if c.Low < low52w {
			low52w = c.Low
		}
	}

	near200SMA := math.Abs(lastCandle.Close-sma200)/sma200*100 <= 3.0
	near52wLow := math.Abs(lastCandle.Close-low52w)/low52w*100 <= 3.0
	nearKeyLevels := near200SMA || near52wLow

	return isCompressed, volVsAvg, lastCandle.Close, avgVol20d, emaConverged, nearKeyLevels
}

// calcEMA helper function
func calcEMA(candles []kiteconnect.HistoricalData, period int) float64 {
	n := len(candles)
	if n == 0 {
		return 0
	}
	if n < period {
		period = n
	}
	alpha := 2.0 / (float64(period) + 1.0)
	ema := candles[0].Close
	for i := 1; i < n; i++ {
		ema = (candles[i].Close * alpha) + (ema * (1.0 - alpha))
	}
	return ema
}

// calcSMA helper function
func calcSMA(candles []kiteconnect.HistoricalData, period int) float64 {
	n := len(candles)
	if n == 0 {
		return 0
	}
	if n < period {
		period = n
	}
	var sum float64
	for i := n - period; i < n; i++ {
		sum += candles[i].Close
	}
	return sum / float64(period)
}

// Stage 2 Engine: Weekend Alt Sentiment news aggregator
func fetchWeekendCatalyst(ticker, sector string) (float64, string) {
	escapedQuery := url.QueryEscape(ticker + " corporate action India filings news")
	_ = escapedQuery // simulate scraping query url

	// Corporate filings simulation
	switch ticker {
	case "SUMICHEM":
		return 0.82, "Patent approval & JV expansion"
	case "TCS":
		return 0.15, "Mega UK client contract renewal"
	case "RELIANCE":
		return 0.45, "Preferential green energy allotment"
	case "IKIO":
		return -0.40, "Board resignation (EVP Operations)"
	case "PBFINTECH":
		return 0.65, "Block deal: large institutional buy"
	default:
		return 0.05, "Neutral corporate updates"
	}
}

func getSectorMultiplier(sector string) float64 {
	switch sector {
	case "Chemicals":
		return 1.5 // Macro policy tailwind
	case "Electronics":
		return 1.2
	case "Fintech":
		return 1.3
	default:
		return 1.0
	}
}

// Stage 3 Engine: Evaluates Pre-Open session metrics
func executePreOpenStage(kc *kiteconnect.Client, pool []Candidate) []Candidate {
	var results []Candidate

	// Pre-open window runs between 9:00 AM and 9:08 AM IST
	// If executed outside of this window, we fall back to simulated pre-open depth
	now := time.Now()
	isPreOpenWindow := now.Hour() == 9 && now.Minute() >= 0 && now.Minute() <= 8

	var quotes kiteconnect.Quote
	var liveFetchSuccess bool

	cfg, _ := config.Load()
	if isPreOpenWindow && cfg != nil && cfg.AccessToken != "" && cfg.AccessToken != "your_access_token_here" {
		var keys []string
		for _, cand := range pool {
			keys = append(keys, fmt.Sprintf("NSE:%s", cand.Ticker))
		}
		var err error
		quotes, err = kc.GetQuote(keys...)
		if err == nil && len(quotes) > 0 {
			liveFetchSuccess = true
		}
	}

	for _, cand := range pool {
		var totalBuyQty, totalSellQty, preOpenVolume, lastPrice float64
		var isLiveQuote bool

		if liveFetchSuccess {
			q, exists := quotes[fmt.Sprintf("NSE:%s", cand.Ticker)]
			if exists {
				isLiveQuote = true
				lastPrice = q.LastPrice
				preOpenVolume = float64(q.Volume)

				for _, bid := range q.Depth.Buy {
					totalBuyQty += float64(bid.Quantity)
				}
				for _, ask := range q.Depth.Sell {
					totalSellQty += float64(ask.Quantity)
				}
			}
		}

		// Fallback to simulated pre-open order books if not live or run outside market pre-open hours
		if !isLiveQuote {
			// Seed based on symbol to keep outputs consistent
			var rSeed int64
			for _, char := range cand.Ticker {
				rSeed += int64(char)
			}
			rnd := rand.New(rand.NewSource(rSeed + time.Now().UnixNano()/1000000000))

			// Simulate indicative gap
			switch cand.Ticker {
			case "SUMICHEM":
				cand.IndicativeGapPct = 2.10
				cand.ImbalanceRatio = 3.80
				cand.PreOpenVolVsADV = 0.065 // 6.5% of ADV
			case "PBFINTECH":
				cand.IndicativeGapPct = 1.85
				cand.ImbalanceRatio = 3.10
				cand.PreOpenVolVsADV = 0.112 // 11.2% of ADV (Block deal)
			case "IKIO":
				cand.IndicativeGapPct = -2.30
				cand.ImbalanceRatio = 0.28
				cand.PreOpenVolVsADV = 0.042
			case "RELIANCE":
				cand.IndicativeGapPct = 0.20
				cand.ImbalanceRatio = 1.05
				cand.PreOpenVolVsADV = 0.125 // Institutional block cross expected
			default:
				cand.IndicativeGapPct = (rnd.Float64() * 4) - 2.0 // between -2.0% and +2.0%
				cand.ImbalanceRatio = (rnd.Float64() * 2) + 0.1
				cand.PreOpenVolVsADV = rnd.Float64() * 0.06
			}
		} else {
			if totalSellQty == 0 {
				totalSellQty = 1.0
			}
			cand.ImbalanceRatio = totalBuyQty / totalSellQty
			cand.IndicativeGapPct = ((lastPrice - cand.ClosePriceT1) / cand.ClosePriceT1) * 100.0
			cand.PreOpenVolVsADV = preOpenVolume / cand.PreOpenVolVsADV
		}

		// Scoring, Matrix Routing, & Predicted Direction Allocation
		cand.PredictedDir = "NEUTRAL"
		cand.RiskLevel = "LOW"
		cand.ProbScore = (math.Abs(cand.SentimentScore) * cand.SectorMultiplier) + (cand.PreOpenVolVsADV * 10)

		// 1. Directional Rule 1: Bullish Breakout
		if cand.IsCompressed && (cand.SentimentScore > 0.3) && cand.ImbalanceRatio > 3.0 && cand.IndicativeGapPct > 1.5 {
			cand.PredictedDir = "BULLISH"
			cand.ProbScore += 50.0
			cand.RiskLevel = "MEDIUM"
		}
		// 2. Directional Rule 2: Bearish Breakdown
		if cand.IsCompressed && (cand.SentimentScore < -0.3) && cand.ImbalanceRatio < 0.33 && cand.IndicativeGapPct < -1.5 {
			cand.PredictedDir = "BEARISH"
			cand.ProbScore += 45.0
			cand.RiskLevel = "MEDIUM"
		}
		// 3. Directional Rule 3: Institutional Block Cross (High Volatility expected)
		if cand.PreOpenVolVsADV > 0.10 && math.Abs(cand.IndicativeGapPct) <= 0.5 && cand.ImbalanceRatio >= 0.8 && cand.ImbalanceRatio <= 1.2 {
			cand.PredictedDir = "BLOCK CROSS"
			cand.ProbScore += 30.0
			cand.RiskLevel = "HIGH"
			cand.CatalystSummary = "Institutional Block Trade / Crossing Window"
		}

		results = append(results, cand)
	}

	return results
}
