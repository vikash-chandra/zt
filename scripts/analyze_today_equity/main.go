package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
	"zerodha-trading/strategy"
)

type TradeResult struct {
	Symbol      string
	Strategy    string
	Side        string
	EntryTime   time.Time
	EntryPrice  float64
	SLPrice     float64
	TargetPrice float64
	ExitTime    time.Time
	ExitPrice   float64
	ExitReason  string
	PnLPct      float64
}

type StockReport struct {
	Symbol          string
	Token           int64
	PDH             float64
	PDL             float64
	PDC             float64
	Open0915        float64
	Close0915       float64
	GapPct          float64
	Is0915Green     bool
	Is0915Red       bool
	Candle0915Range float64
	CandleCount     int
	PossibleTrades  []TradeResult
	DisqualReasons  map[string]string
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logger, err := monitoring.NewLogger(cfg.LogLevel)
	if err != nil {
		log.Fatalf("Failed to init logger: %v", err)
	}

	db, err := data.NewDatabase(cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode, logger.Logger)
	if err != nil {
		log.Fatalf("Failed to connect database: %v", err)
	}

	ctx := context.Background()
	todayStr := "2026-08-31"
	todayDate := time.Date(2026, 8, 31, 0, 0, 0, 0, data.ISTLocation)

	// Fetch active watchlist for today from DB
	dbItems, err := db.GetDailyWatchlist(ctx, todayStr)
	var symbols []string
	symbolTokenMap := make(map[string]int64)

	if err == nil && len(dbItems) > 0 {
		for _, item := range dbItems {
			symbols = append(symbols, item.Symbol)
			symbolTokenMap[item.Symbol] = item.Token
		}
	} else {
		symbols = []string{"KPITTECH", "PERSISTENT", "KAYNES", "VEDL", "TATAELXSI", "ADANIENSOL", "ASTRAL", "DIXON", "BLUESTARCO", "IREDA", "AUROPHARMA", "HDFCBANK", "MCX", "MUTHOOTFIN", "MANAPPURAM"}
	}

	rawClient := data.NewZerodhaBrokerAdapter(nil)
	secMaster := data.NewSecurityMaster(db, rawClient, logger.Logger)
	foMap, _ := secMaster.GetFOStocks(ctx)
	for sym, tok := range foMap {
		if _, exists := symbolTokenMap[sym]; !exists {
			symbolTokenMap[sym] = tok
		}
	}

	fmt.Printf("\n========================================================================================\n")
	fmt.Printf("?? EQUITY STRATEGY SIMULATION REPORT - 31-AUG-2026\n")
	fmt.Printf("Watchlist (%d stocks): %s\n", len(symbols), strings.Join(symbols, ", "))
	fmt.Printf("========================================================================================\n\n")

	var allReports []StockReport
	var totalTrades []TradeResult

	for _, symbol := range symbols {
		token := symbolTokenMap[symbol]
		if token == 0 {
			continue
		}

		rep := StockReport{
			Symbol:         symbol,
			Token:          token,
			DisqualReasons: make(map[string]string),
		}

		// 1. Fetch Previous Day High / Low / Close
		dailyCandles, dErr := db.GetRecentDailyCandlesByToken(ctx, token, 10)
		if dErr == nil && len(dailyCandles) >= 2 {
			prevDay := dailyCandles[len(dailyCandles)-2]
			rep.PDH = prevDay.High
			rep.PDL = prevDay.Low
			rep.PDC = prevDay.Close
		} else {
			prev5m, _ := db.GetLastNCandles("candles_5m", token, 150)
			if len(prev5m) > 0 {
				var pdh, pdl, pdc float64
				pdl = 9999999
				for _, c := range prev5m {
					if c.Time.In(data.ISTLocation).Day() != 31 {
						if c.High > pdh {
							pdh = c.High
						}
						if c.Low < pdl && c.Low > 0 {
							pdl = c.Low
						}
						pdc = c.Close
					}
				}
				if pdh > 0 {
					rep.PDH = pdh
					rep.PDL = pdl
					rep.PDC = pdc
				}
			}
		}

		// 2. Fetch today's 5m candles
		today5m, _ := db.GetCandlesForDayFromTable(ctx, "candles_5m", token, todayDate)
		rep.CandleCount = len(today5m)

		if len(today5m) == 0 {
			rep.DisqualReasons["ALL"] = "No 5m candles in DB"
			allReports = append(allReports, rep)
			continue
		}

		sort.Slice(today5m, func(i, j int) bool {
			return today5m[i].Time.Before(today5m[j].Time)
		})

		firstCandle := today5m[0]
		rep.Open0915 = firstCandle.Open
		rep.Close0915 = firstCandle.Close
		if rep.PDC > 0 {
			rep.GapPct = ((firstCandle.Open - rep.PDC) / rep.PDC) * 100.0
		}
		rep.Is0915Green = firstCandle.Close > firstCandle.Open
		rep.Is0915Red = firstCandle.Close < firstCandle.Open
		rep.Candle0915Range = ((firstCandle.High - firstCandle.Low) / firstCandle.Open) * 100.0

		// Initialize Strategy Engines using standard initialization
		activeStrats := strategy.InitializeActiveStrategies([]string{"LOW_VOLUME", "VANDE_BHARAT", "VANDE_BHARAT_TRAP", "FAKE_BREAKOUT", "EMAS5_BREAKOUT"}, logger.Logger, cfg)

		for _, st := range activeStrats {
			switch e := st.(type) {
			case *strategy.LowVolumeEngine:
				if rep.PDH > 0 && rep.PDL > 0 {
					e.SetPreviousDayHighLow(symbol, rep.PDH, rep.PDL)
				}
			case *strategy.VandeBharatEngine:
				if rep.PDH > 0 && rep.PDL > 0 {
					e.SetPreviousDayLevels(symbol, rep.PDH, rep.PDL, rep.PDC)
				}
			case *strategy.VandeBharatTrapEngine:
				if rep.PDH > 0 && rep.PDL > 0 {
					e.SetPreviousDayLevels(symbol, rep.PDH, rep.PDL, rep.PDC)
				}
			case *strategy.EMAS5BreakoutEngine:
				if rep.PDH > 0 && rep.PDL > 0 {
					e.SetPreviousDayLevels(symbol, rep.PDH, rep.PDL, rep.PDC)
				}
			}
		}

		// Feed candles
		for idx, c := range today5m {
			cTimeIST := data.NormalizeToIST(c.Time)
			color := "DOJI"
			if c.Close > c.Open {
				color = "GREEN"
			} else if c.Close < c.Open {
				color = "RED"
			}

			candle := &data.Candle{
				Token:  token,
				Time:   cTimeIST,
				Open:   c.Open,
				High:   c.High,
				Low:    c.Low,
				Close:  c.Close,
				Volume: c.Volume,
				VWAP:   (c.Open + c.High + c.Low + c.Close) / 4.0,
				Color:  color,
			}

			for _, st := range activeStrats {
				st.OnCandleClose(candle, symbol)

				sigBuy := st.CheckBreakout(symbol, candle.High, "BULLISH")
				if sigBuy != nil && sigBuy.Action == "BUY" {
					entry := candle.High
					sl := candle.Low
					if setup := st.GetSetupCandle(symbol); setup != nil {
						sl = setup.Candle.Low
					}
					target := entry + (entry-sl)*1.5
					tr := simulateTradeOutcome(today5m[idx:], entry, sl, target, "BUY", cTimeIST, symbol, st.Name())
					rep.PossibleTrades = append(rep.PossibleTrades, tr)
					totalTrades = append(totalTrades, tr)
				}

				sigSell := st.CheckBreakout(symbol, candle.Low, "BEARISH")
				if sigSell != nil && sigSell.Action == "SELL" {
					entry := candle.Low
					sl := candle.High
					if setup := st.GetSetupCandle(symbol); setup != nil {
						sl = setup.Candle.High
					}
					target := entry - (sl-entry)*1.5
					tr := simulateTradeOutcome(today5m[idx:], entry, sl, target, "SELL", cTimeIST, symbol, st.Name())
					rep.PossibleTrades = append(rep.PossibleTrades, tr)
					totalTrades = append(totalTrades, tr)
				}
			}
		}

		allReports = append(allReports, rep)
	}

	fmt.Printf("----------------------------------------------------------------------------------------\n")
	fmt.Printf("%-12s | %-7s | %-7s | %-7s | %-7s | %-7s | %-6s | %s\n",
		"SYMBOL", "PDH", "PDL", "PDC", "09:15-O", "09:15-C", "GAP %", "POSSIBLE TRADES")
	fmt.Printf("----------------------------------------------------------------------------------------\n")

	for _, rep := range allReports {
		tradeSummary := "No Trigger"
		if len(rep.PossibleTrades) > 0 {
			var tStr []string
			for _, t := range rep.PossibleTrades {
				tStr = append(tStr, fmt.Sprintf("%s %s @ %.2f (PnL: %+.2f%%)", t.Strategy, t.Side, t.EntryPrice, t.PnLPct))
			}
			tradeSummary = strings.Join(tStr, "; ")
		} else if len(rep.DisqualReasons) > 0 {
			var dStr []string
			for k, v := range rep.DisqualReasons {
				dStr = append(dStr, fmt.Sprintf("%s: %s", k, v))
			}
			tradeSummary = strings.Join(dStr, ", ")
		}

		fmt.Printf("%-12s | %7.2f | %7.2f | %7.2f | %7.2f | %7.2f | %+6.2f%% | %s\n",
			rep.Symbol, rep.PDH, rep.PDL, rep.PDC, rep.Open0915, rep.Close0915, rep.GapPct, tradeSummary)
	}

	fmt.Printf("----------------------------------------------------------------------------------------\n\n")

	if len(totalTrades) > 0 {
		fmt.Printf("?? DETAILED TRIGGERED TRADES BREAKDOWN (%d Trades):\n", len(totalTrades))
		for i, t := range totalTrades {
			fmt.Printf("  #%d [%s] %s %s\n", i+1, t.EntryTime.Format("15:04"), t.Symbol, t.Strategy)
			fmt.Printf("     Direction: %s | Entry: ?%.2f | SL: ?%.2f | Target: ?%.2f\n", t.Side, t.EntryPrice, t.SLPrice, t.TargetPrice)
			fmt.Printf("     Exit Time: %s | Exit Price: ?%.2f | Reason: %s | PnL: %+.2f%%\n\n",
				t.ExitTime.Format("15:04"), t.ExitPrice, t.ExitReason, t.PnLPct)
		}
	} else {
		fmt.Printf("??  No breakout trades met 100%% of rule filters on 31-Aug-2026.\n\n")
	}
}

func simulateTradeOutcome(remainingCandles []data.CandleRecord, entryPrice, slPrice, targetPrice float64, side string, entryTime time.Time, symbol, strategyName string) TradeResult {
	res := TradeResult{
		Symbol:      symbol,
		Strategy:    strategyName,
		Side:        side,
		EntryTime:   entryTime,
		EntryPrice:  entryPrice,
		SLPrice:     slPrice,
		TargetPrice: targetPrice,
	}

	for _, c := range remainingCandles {
		cTime := data.NormalizeToIST(c.Time)
		if side == "BUY" {
			if targetPrice > 0 && c.High >= targetPrice {
				res.ExitTime = cTime
				res.ExitPrice = targetPrice
				res.ExitReason = "TARGET_HIT"
				res.PnLPct = ((targetPrice - entryPrice) / entryPrice) * 100.0
				return res
			}
			if c.Low <= slPrice {
				res.ExitTime = cTime
				res.ExitPrice = slPrice
				res.ExitReason = "SL_HIT"
				res.PnLPct = ((slPrice - entryPrice) / entryPrice) * 100.0
				return res
			}
		} else {
			if targetPrice > 0 && c.Low <= targetPrice {
				res.ExitTime = cTime
				res.ExitPrice = targetPrice
				res.ExitReason = "TARGET_HIT"
				res.PnLPct = ((entryPrice - targetPrice) / entryPrice) * 100.0
				return res
			}
			if c.High >= slPrice {
				res.ExitTime = cTime
				res.ExitPrice = slPrice
				res.ExitReason = "SL_HIT"
				res.PnLPct = ((entryPrice - slPrice) / entryPrice) * 100.0
				return res
			}
		}

		if cTime.Hour() == 15 && cTime.Minute() >= 15 {
			res.ExitTime = cTime
			res.ExitPrice = c.Close
			res.ExitReason = "EOD_SQUARE_OFF"
			if side == "BUY" {
				res.PnLPct = ((c.Close - entryPrice) / entryPrice) * 100.0
			} else {
				res.PnLPct = ((entryPrice - c.Close) / entryPrice) * 100.0
			}
			return res
		}
	}

	last := remainingCandles[len(remainingCandles)-1]
	res.ExitTime = data.NormalizeToIST(last.Time)
	res.ExitPrice = last.Close
	res.ExitReason = "END_OF_DATA"
	if side == "BUY" {
		res.PnLPct = ((last.Close - entryPrice) / entryPrice) * 100.0
	} else {
		res.PnLPct = ((entryPrice - last.Close) / entryPrice) * 100.0
	}
	return res
}
