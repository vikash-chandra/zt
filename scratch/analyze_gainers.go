package main

import (
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
)

type GainerStats struct {
	Symbol        string
	Token         int64
	ADV           float64
	LastVolume    int64
	VolMultiplier float64
	Compression   float64
	EMASpread     float64
	LastClose     float64
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logger, err := monitoring.NewLogger(cfg.LogLevel)
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}

	db, err := data.NewDatabase(
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		logger.Logger,
	)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	kc := kiteconnect.New(cfg.APIKey)
	kc.SetAccessToken(cfg.AccessToken)

	// Fetch all NSE instruments to resolve tokens
	instruments, err := kc.GetInstrumentsByExchange("NSE")
	if err != nil {
		log.Fatalf("Failed to fetch NSE instruments: %v", err)
	}

	tokenMap := make(map[string]int64)
	for _, inst := range instruments {
		if inst.Segment == "NSE" && inst.InstrumentType == "EQ" {
			tokenMap[inst.Tradingsymbol] = int64(inst.InstrumentToken)
		}
	}

	symbols := []string{
		"ADVENTHTL", "DALMIASUG", "FERMENTA", "TARSONS", "MAZDA", "IVP", "SURAJLTD",
		"SPECTRUM", "SONAL", "MUNJALAU", "DIGITIDE", "PLATIND", "ANDHRSUGAR", "AARTECH",
		"SHAKTIPUMP", "SWANCORP", "GRAVISSHO", "SHBAJRG", "NATIONSTD", "RITCO", "SOTL", "ARIHANTCAP",
	}

	var stats []GainerStats
	loc, _ := time.LoadLocation("Asia/Kolkata")
	toTime := time.Now().In(loc)
	fromTime := toTime.AddDate(0, 0, -60)

	fmt.Println("Analyzing historical daily patterns for listed gainers...")
	fmt.Printf("%-12s %-12s %-12s %-12s %-10s %-10s %-8s\n", "SYMBOL", "CLOSE", "VOLUME", "ADV", "VOL_MULT", "COMPRESS%", "EMA_SPD%")
	fmt.Println(strings.Repeat("-", 80))

	for _, sym := range symbols {
		token, exists := tokenMap[sym]
		if !exists {
			fmt.Printf("%-12s: Token not found in NSE EQ list\n", sym)
			continue
		}

		// Rate limit call to Zerodha
		time.Sleep(350 * time.Millisecond)

		candles, err := kc.GetHistoricalData(int(token), "day", fromTime, toTime, false, false)
		if err != nil || len(candles) < 22 {
			continue
		}

		n := len(candles)
		t1Candle := candles[n-1] // Today (closed) or yesterday depending on time

		// Calculate 20-day ADV excluding last candle
		var totalVol float64
		for i := n - 21; i < n-1; i++ {
			totalVol += float64(candles[i].Volume)
		}
		adv := totalVol / 20.0
		if adv == 0 {
			continue
		}

		volMult := float64(t1Candle.Volume) / adv

		// Calculate price compression
		var priceSum float64
		for i := n - 6; i < n-1; i++ {
			priceSum += candles[i].Close
		}
		meanPrice := priceSum / 5.0
		var varianceSum float64
		for i := n - 6; i < n-1; i++ {
			varianceSum += math.Pow(candles[i].Close-meanPrice, 2)
		}
		stdDev := math.Sqrt(varianceSum / 5.0)
		compression := (stdDev / meanPrice) * 100.0

		// EMA convergence
		ema5 := calculateEMA(candles, 5)
		ema20 := calculateEMA(candles, 20)
		ema50 := calculateEMA(candles, 50)
		emas := []float64{ema5, ema20, ema50}
		sort.Float64s(emas)
		emaSpread := ((emas[2] - emas[0]) / emas[0]) * 100.0

		stat := GainerStats{
			Symbol:        sym,
			Token:         token,
			ADV:           adv,
			LastVolume:    int64(t1Candle.Volume),
			VolMultiplier: volMult,
			Compression:   compression,
			EMASpread:     emaSpread,
			LastClose:     t1Candle.Close,
		}
		stats = append(stats, stat)

		fmt.Printf("%-12s %-12.2f %-12d %-12.0f %-10.2f %-10.2f %-8.2f\n",
			sym, t1Candle.Close, t1Candle.Volume, adv, volMult, compression, emaSpread)
	}
}

func calculateEMA(candles []kiteconnect.HistoricalData, period int) float64 {
	n := len(candles)
	if n == 0 {
		return 0
	}
	if n < period {
		period = n
	}
	alpha := 2.0 / (float64(period) + 1.0)
	ema := candles[n-period].Close
	for i := n - period + 1; i < n; i++ {
		ema = (candles[i].Close * alpha) + (ema * (1.0 - alpha))
	}
	return ema
}
