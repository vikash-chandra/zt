package main

import (
	"fmt"
	"log"
	"math"
	"time"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/strategy"

	"go.uber.org/zap"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Load config err: %v", err)
	}

	db, err := data.NewDatabase(
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		zap.NewNop(),
	)
	if err != nil {
		log.Fatalf("DB err: %v", err)
	}
	defer db.Close()

	token := int64(82945)
	symbol := "ANGELONE"

	// Fetch last 150 5m candles
	dbCandles, err := db.GetLastNCandles("candles_5m", token, 150)
	if err != nil || len(dbCandles) == 0 {
		log.Fatalf("GetLastNCandles err: %v (count=%d)", err, len(dbCandles))
	}

	// Filter today's candles
	todayStr := time.Now().In(data.ISTLocation).Format("2006-01-02")
	fmt.Printf("Analyzing %s (Token %d) on %s. Total 5m candles in DB: %d\n\n", symbol, token, todayStr, len(dbCandles))

	// PDH and PDL
	pdh := 304.35
	pdl := 298.20

	logger := zap.NewExample()
	strat := strategy.NewEMAS5BreakoutEngine(logger, 2, 5, 0.45, 2.0, 1, 1.0)
	strat.SetPreviousDayLevels(symbol, pdh, pdl, 302.90)

	// Step through each candle
	indicators := strategy.NewIndicators()

	var rolling []data.Candle
	for _, c := range dbCandles {
		cIST := data.NormalizeToIST(c.Time)
		c.Time = cIST
		rolling = append(rolling, c)

		if cIST.Format("2006-01-02") != todayStr {
			continue
		}

		closes := make([]float64, len(rolling))
		for i, rc := range rolling {
			closes[i] = rc.Close
		}
		ema10 := indicators.CalculateEMA(closes, 10)
		ema20 := indicators.CalculateEMA(closes, 20)
		e10 := ema10[len(ema10)-1]
		e20 := ema20[len(ema20)-1]

		cRange := c.High - c.Low
		cRangePct := (cRange / c.Close) * 100.0
		bodySize := math.Abs(c.Close - c.Open)
		wickPct := 0.0
		if cRange > 0 {
			wickPct = ((cRange - bodySize) / cRange) * 100.0
		}
		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}

		// Only print between 10:00 and 10:50
		if cIST.Hour() == 10 && cIST.Minute() <= 50 {
			fmt.Printf("--- Candle %02d:%02d IST [%s] ---\n", cIST.Hour(), cIST.Minute(), color)
			fmt.Printf("  OHLC: O=%.2f H=%.2f L=%.2f C=%.2f (Range=%.2f / %.2f%%, Wick=%.1f%%)\n", c.Open, c.High, c.Low, c.Close, cRange, cRangePct, wickPct)
			fmt.Printf("  EMAs: EMA10=%.2f, EMA20=%.2f | Levels: PDH=%.2f, PDL=%.2f\n", e10, e20, pdh, pdl)

			// Detailed checks for BUY Master
			fmt.Printf("  [BUY Master Evaluation]:\n")
			if color != "GREEN" {
				fmt.Printf("    ❌ Color: Candle is %s (Must be GREEN: Close > Open)\n", color)
			} else {
				fmt.Printf("    ✓ Color: GREEN\n")
			}
			if cRangePct > 2.0 {
				fmt.Printf("    ❌ Range: %.2f%% > max 2.0%%\n", cRangePct)
			} else {
				fmt.Printf("    ✓ Range: %.2f%% <= 2.0%%\n", cRangePct)
			}
			if wickPct > 40.0 {
				fmt.Printf("    ❌ Wick: %.1f%% > max 40.0%%\n", wickPct)
			} else {
				fmt.Printf("    ✓ Wick: %.1f%% <= 40.0%%\n", wickPct)
			}
			touchesEMA := (c.Low <= e10*1.001 && c.High >= e10*0.999) || (c.Low <= e20*1.001 && c.High >= e20*0.999)
			touchesPDH := c.Low <= pdh*1.001 && c.High >= pdh*0.999
			if !touchesEMA && !touchesPDH {
				fmt.Printf("    ❌ Level Touch: Does NOT touch EMA10 (%.2f), EMA20 (%.2f), or PDH (%.2f)\n", e10, e20, pdh)
			} else {
				fmt.Printf("    ✓ Level Touch: Touches EMA or PDH\n")
			}
			closesAbove := c.Close > e10 && c.Close > e20 && (pdh <= 0 || c.Close > pdh)
			if !closesAbove {
				fmt.Printf("    ❌ Close Above: Close (%.2f) does NOT close above EMA10 (%.2f), EMA20 (%.2f), and PDH (%.2f)\n", c.Close, e10, e20, pdh)
			} else {
				fmt.Printf("    ✓ Close Above: Closes above all\n")
			}

			// Detailed checks for SELL Master
			fmt.Printf("  [SELL Master Evaluation]:\n")
			if color != "RED" {
				fmt.Printf("    ❌ Color: Candle is %s (Must be RED: Close < Open)\n", color)
			} else {
				fmt.Printf("    ✓ Color: RED\n")
			}
			touchesEMASell := (c.Low <= e10*1.001 && c.High >= e10*0.999) || (c.Low <= e20*1.001 && c.High >= e20*0.999)
			touchesPDL := c.Low <= pdl*1.001 && c.High >= pdl*0.999
			if !touchesEMASell && !touchesPDL {
				fmt.Printf("    ❌ Level Touch: Does NOT touch EMA10 (%.2f), EMA20 (%.2f), or PDL (%.2f)\n", e10, e20, pdl)
			} else {
				fmt.Printf("    ✓ Level Touch: Touches EMA or PDL\n")
			}
			closesBelow := c.Close < e10 && c.Close < e20 && (pdl <= 0 || c.Close < pdl)
			if !closesBelow {
				fmt.Printf("    ❌ Close Below: Close (%.2f) does NOT close below EMA10 (%.2f), EMA20 (%.2f), and PDL (%.2f)\n", c.Close, e10, e20, pdl)
			} else {
				fmt.Printf("    ✓ Close Below: Closes below all\n")
			}

			fmt.Println()
		}

		strat.ProcessCandle(symbol, c)
	}
}
