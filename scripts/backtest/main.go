package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
)

type BacktestTrade struct {
	Date        string
	Symbol      string
	Side        string
	EntryPrice  float64
	ExitPrice   float64
	Quantity    int
	PnL         float64
	EntryTime   string
	ExitTime    string
	ExitReason  string
}

type DailyStats struct {
	Date        string
	Bias        string
	Advances    int
	Declines    int
	TradesCount int
	PnL         float64
}

func main() {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create logger
	logger, err := monitoring.NewLogger(cfg.LogLevel)
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}

	// Connect to database
	db, err := data.NewDatabase(
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		logger.Logger,
	)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	// Verify Kite credentials
	if cfg.AccessToken == "" || cfg.AccessToken == "your_access_token_here" {
		log.Fatalf("KITE_ACCESS_TOKEN is not configured. Live historical seeding requires a valid access token.")
	}

	// Create Kite Client
	kiteClient := kiteconnect.New(cfg.APIKey)
	kiteClient.SetAccessToken(cfg.AccessToken)

	ctx := context.Background()
	securityMaster := data.NewSecurityMaster(db.WithContext(ctx), kiteClient, logger.Logger)

	// Fetch Nifty 50 constituents
	watchlist, err := securityMaster.GetNifty50Constituents(ctx)
	if err != nil {
		log.Fatalf("Failed to fetch Nifty 50 constituents: %v", err)
	}

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}

	fmt.Println("==================================================================")
	fmt.Printf("Starting Dual-Resolution (5m Entry / 1m Exit) Backtest (%s)\n", time.Now().Format("2006-01-02"))
	fmt.Println("==================================================================")

	// Set date bounds (last 10 calendar days to capture 7 trading days)
	endDate := time.Now().In(loc)
	startDate := endDate.AddDate(0, 0, -10)

	allCandles5m := make(map[string][]kiteconnect.HistoricalData)
	allCandles1m := make(map[string][]kiteconnect.HistoricalData)

	fmt.Println("Fetching historical 5m and 1m candles from Zerodha API...")
	for symbol, token := range watchlist {
		// Fetch 5m candles for entry triggers
		candles5m, err := kiteClient.GetHistoricalData(int(token), "5minute", startDate, endDate, false, false)
		if err != nil {
			log.Printf("Warning: failed to fetch 5m data for %s: %v", symbol, err)
			continue
		}
		allCandles5m[symbol] = candles5m

		// Fetch 1m candles for position monitoring
		candles1m, err := kiteClient.GetHistoricalData(int(token), "minute", startDate, endDate, false, false)
		if err != nil {
			log.Printf("Warning: failed to fetch 1m data for %s: %v", symbol, err)
			continue
		}
		allCandles1m[symbol] = candles1m

		time.Sleep(100 * time.Millisecond) // Respect rate limits
	}

	// Group 5m candles by date
	candles5mByDate := make(map[string]map[string][]kiteconnect.HistoricalData)
	var dates []string
	seenDates := make(map[string]bool)

	for symbol, candles := range allCandles5m {
		for _, c := range candles {
			cTime := c.Date.In(loc)
			dateStr := cTime.Format("2006-01-02")
			if cTime.Weekday() == time.Saturday || cTime.Weekday() == time.Sunday {
				continue
			}
			if _, ok := candles5mByDate[dateStr]; !ok {
				candles5mByDate[dateStr] = make(map[string][]kiteconnect.HistoricalData)
			}
			candles5mByDate[dateStr][symbol] = append(candles5mByDate[dateStr][symbol], c)

			if !seenDates[dateStr] {
				seenDates[dateStr] = true
				dates = append(dates, dateStr)
			}
		}
	}

	// Group 1m candles by date
	candles1mByDate := make(map[string]map[string][]kiteconnect.HistoricalData)
	for symbol, candles := range allCandles1m {
		for _, c := range candles {
			cTime := c.Date.In(loc)
			dateStr := cTime.Format("2006-01-02")
			if cTime.Weekday() == time.Saturday || cTime.Weekday() == time.Sunday {
				continue
			}
			if _, ok := candles1mByDate[dateStr]; !ok {
				candles1mByDate[dateStr] = make(map[string][]kiteconnect.HistoricalData)
			}
			candles1mByDate[dateStr][symbol] = append(candles1mByDate[dateStr][symbol], c)
		}
	}

	sort.Strings(dates)

	// Filter to the last 7 trading days
	if len(dates) > 7 {
		dates = dates[len(dates)-7:]
	}

	var allTrades []BacktestTrade
	var dailyStatsList []DailyStats

	// Daily Simulation Loop
	for _, dateStr := range dates {
		dayData5m := candles5mByDate[dateStr]
		dayData1m := candles1mByDate[dateStr]

		// 1. Calculate pre-market breadth at 09:29:00 AM using 5m data (09:15 open vs 09:25 close)
		advances := 0
		declines := 0

		type StockChange struct {
			Symbol    string
			PctChange float64
		}
		var changes []StockChange

		for symbol, candles := range dayData5m {
			var open0915 float64
			var ltp0929 float64
			var ltp0930 float64
			foundOpen := false
			foundLTP29 := false
			foundLTP30 := false

			for _, c := range candles {
				cTime := c.Date.In(loc)
				h, m := cTime.Hour(), cTime.Minute()

				if h == 9 && m == 15 {
					open0915 = c.Open
					foundOpen = true
				}
				if h == 9 && m == 25 {
					ltp0929 = c.Close
					foundLTP29 = true
				}
				if h == 9 && m == 25 {
					ltp0930 = c.Close // proxy for 09:30 AM close
					foundLTP30 = true
				}
			}

			if foundOpen && foundLTP29 {
				pctChange := ((ltp0929 - open0915) / open0915) * 100.0
				if pctChange > 0 {
					advances++
				} else if pctChange < 0 {
					declines++
				}
			}

			if foundOpen && foundLTP30 {
				pctChange30 := ((ltp0930 - open0915) / open0915) * 100.0
				if math.Abs(pctChange30) <= cfg.WatchlistMaxPctChange {
					changes = append(changes, StockChange{Symbol: symbol, PctChange: pctChange30})
				}
			}
		}

		bias := "NO_TRADE"
		if advances > declines {
			bias = "BUY_ONLY"
		} else if declines > advances {
			bias = "SELL_ONLY"
		}

		if bias == "NO_TRADE" || len(changes) == 0 {
			dailyStatsList = append(dailyStatsList, DailyStats{Date: dateStr, Bias: bias, Advances: advances, Declines: declines, TradesCount: 0, PnL: 0})
			continue
		}

		// 2. Watchlist Selection at 09:30:00 AM (Top 10 Gainers or Losers)
		if bias == "BUY_ONLY" {
			sort.Slice(changes, func(i, j int) bool {
				return changes[i].PctChange > changes[j].PctChange
			})
		} else {
			sort.Slice(changes, func(i, j int) bool {
				return changes[i].PctChange < changes[j].PctChange
			})
		}

		topCount := 10
		if len(changes) < topCount {
			topCount = len(changes)
		}

		watchlistMap := make(map[string]bool)
		for i := 0; i < topCount; i++ {
			watchlistMap[changes[i].Symbol] = true
		}

		// 3. Resolve Setup Candles (Lowest Volume 5m candle from 09:15 to 09:25)
		type SetupCandle struct {
			High   float64
			Low    float64
			Volume int64
			Color  string
		}
		setupCandles := make(map[string]SetupCandle)

		for symbol := range watchlistMap {
			var minVol int64 = -1
			var minVolCandle kiteconnect.HistoricalData
			found := false

			for _, c := range dayData5m[symbol] {
				cTime := c.Date.In(loc)
				h, m := cTime.Hour(), cTime.Minute()

				if h == 9 && (m == 15 || m == 20 || m == 25) {
					if minVol == -1 || int64(c.Volume) < minVol {
						minVol = int64(c.Volume)
						minVolCandle = c
						found = true
					}
				}
			}

			if found {
				color := "DOJI"
				if minVolCandle.Close < minVolCandle.Open {
					color = "RED"
				} else if minVolCandle.Close > minVolCandle.Open {
					color = "GREEN"
				}
				setupCandles[symbol] = SetupCandle{
					High:   minVolCandle.High,
					Low:    minVolCandle.Low,
					Volume: int64(minVolCandle.Volume),
					Color:  color,
				}
			}
		}

		// Position State
		type Position struct {
			Symbol            string
			Side              string
			EntryPrice        float64
			SLPrice           float64
			Target1Price      float64
			Quantity          int
			IsPartialExitDone bool
			EntryTime         time.Time
		}

		openPositions := make(map[string]*Position)
		tradesToday := 0
		dailyPnL := 0.0

		// Simulating 5-minute candle entry triggers (09:30 to 10:45)
		// and tracking active position state on 1-minute candle logs
		timeSlots5m := []struct{ h, m int }{
			{9, 30}, {9, 35}, {9, 40}, {9, 45}, {9, 50}, {9, 55},
			{10, 0}, {10, 5}, {10, 10}, {10, 15}, {10, 20}, {10, 25}, {10, 30}, {10, 35}, {10, 40}, {10, 45},
		}

		// Helper to scan 1m candles for an active position between two times
		scan1mActivePosition := func(pos *Position, startTime, endTime time.Time, symbol string) (bool, string) {
			symbol1mCandles := dayData1m[symbol]
			
			// Sort 1m candles chronologically to simulate tick updates
			sort.Slice(symbol1mCandles, func(i, j int) bool {
				return symbol1mCandles[i].Date.Time.Before(symbol1mCandles[j].Date.Time)
			})

			for _, c1m := range symbol1mCandles {
				c1mTime := c1m.Date.In(loc)
				if (c1mTime.After(startTime) || c1mTime.Equal(startTime)) && c1mTime.Before(endTime) {
					
					// 1. Check Target 1 Partial Exit (50% Quantity)
					if !pos.IsPartialExitDone {
						if pos.Side == "BUY" && c1m.High >= pos.Target1Price {
							pos.IsPartialExitDone = true
							pos.SLPrice = pos.EntryPrice // trail stop-loss to cost-to-cost entry
							pnl := (pos.Target1Price - pos.EntryPrice) * 50.0
							dailyPnL += pnl
							pos.Quantity = 50

							allTrades = append(allTrades, BacktestTrade{
								Date:       dateStr,
								Symbol:     symbol,
								Side:       pos.Side,
								EntryPrice: pos.EntryPrice,
								ExitPrice:  pos.Target1Price,
								Quantity:   50,
								PnL:        pnl,
								EntryTime:  pos.EntryTime.Format("2006-01-02 15:04:00"),
								ExitTime:   c1mTime.Format("2006-01-02 15:04:00"),
								ExitReason: "PARTIAL_TARGET1",
							})
						} else if pos.Side == "SELL" && c1m.Low <= pos.Target1Price {
							pos.IsPartialExitDone = true
							pos.SLPrice = pos.EntryPrice // trail stop-loss to cost-to-cost entry
							pnl := (pos.EntryPrice - pos.Target1Price) * 50.0
							dailyPnL += pnl
							pos.Quantity = 50

							allTrades = append(allTrades, BacktestTrade{
								Date:       dateStr,
								Symbol:     symbol,
								Side:       pos.Side,
								EntryPrice: pos.EntryPrice,
								ExitPrice:  pos.Target1Price,
								Quantity:   50,
								PnL:        pnl,
								EntryTime:  pos.EntryTime.Format("2006-01-02 15:04:00"),
								ExitTime:   c1mTime.Format("2006-01-02 15:04:00"),
								ExitReason: "PARTIAL_TARGET1",
							})
						}
					}

					// 2. Check Stop-Loss Breaches
					if pos.Side == "BUY" && c1m.Low <= pos.SLPrice {
						exitQty := pos.Quantity
						pnl := (pos.SLPrice - pos.EntryPrice) * float64(exitQty)
						dailyPnL += pnl

						allTrades = append(allTrades, BacktestTrade{
							Date:       dateStr,
							Symbol:     symbol,
							Side:       pos.Side,
							EntryPrice: pos.EntryPrice,
							ExitPrice:  pos.SLPrice,
							Quantity:   exitQty,
							PnL:        pnl,
							EntryTime:  pos.EntryTime.Format("2006-01-02 15:04:00"),
							ExitTime:   c1mTime.Format("2006-01-02 15:04:00"),
							ExitReason: "SL_HIT",
						})
						return true, "SL_HIT" // position liquidated
					} else if pos.Side == "SELL" && c1m.High >= pos.SLPrice {
						exitQty := pos.Quantity
						pnl := (pos.EntryPrice - pos.SLPrice) * float64(exitQty)
						dailyPnL += pnl

						allTrades = append(allTrades, BacktestTrade{
							Date:       dateStr,
							Symbol:     symbol,
							Side:       pos.Side,
							EntryPrice: pos.EntryPrice,
							ExitPrice:  pos.SLPrice,
							Quantity:   exitQty,
							PnL:        pnl,
							EntryTime:  pos.EntryTime.Format("2006-01-02 15:04:00"),
							ExitTime:   c1mTime.Format("2006-01-02 15:04:00"),
							ExitReason: "SL_HIT",
						})
						return true, "SL_HIT" // position liquidated
					}

					// 3. EOD Hard Square-off (at 15:15:00)
					if c1mTime.Hour() == 15 && c1mTime.Minute() == 15 {
						pnl := 0.0
						if pos.Side == "BUY" {
							pnl = (c1m.Close - pos.EntryPrice) * float64(pos.Quantity)
						} else {
							pnl = (pos.EntryPrice - c1m.Close) * float64(pos.Quantity)
						}
						dailyPnL += pnl

						allTrades = append(allTrades, BacktestTrade{
							Date:       dateStr,
							Symbol:     symbol,
							Side:       pos.Side,
							EntryPrice: pos.EntryPrice,
							ExitPrice:  c1m.Close,
							Quantity:   pos.Quantity,
							PnL:        pnl,
							EntryTime:  pos.EntryTime.Format("2006-01-02 15:04:00"),
							ExitTime:   c1mTime.Format("2006-01-02 15:04:00"),
							ExitReason: "EOD_SQUAREOFF",
						})
						return true, "EOD_SQUAREOFF"
					}
				}
			}
			return false, ""
		}

		// Run time increments
		for _, slot := range timeSlots5m {
			for symbol := range watchlistMap {
				// Find 5m candle for entry trigger
				var candle5m kiteconnect.HistoricalData
				found5m := false
				for _, c := range dayData5m[symbol] {
					cTime := c.Date.In(loc)
					if cTime.Hour() == slot.h && cTime.Minute() == slot.m {
						candle5m = c
						found5m = true
						break
					}
				}

				if !found5m {
					continue
				}

				pos := openPositions[symbol]

				// If position is active, scan the 1m candles corresponding to this 5-minute interval
				if pos != nil {
					startTime := candle5m.Date.In(loc)
					endTime := startTime.Add(5 * time.Minute)
					closed, _ := scan1mActivePosition(pos, startTime, endTime, symbol)
					if closed {
						delete(openPositions, symbol)
					}
				} else {
					// Scan for Breakout Entry (on 5m candle data)
					if tradesToday < 5 {
						setup, hasSetup := setupCandles[symbol]
						if !hasSetup {
							continue
						}

						entryTime := candle5m.Date.In(loc)

						// BUY Entry trigger (Setup candle must be RED)
						if bias == "BUY_ONLY" && setup.Color == "RED" {
							if candle5m.High > setup.High {
								entryPrice := setup.High
								originalRisk := math.Abs(entryPrice - setup.Low)
								bufferedRisk := (1.0 + (cfg.SLBufferPct / 100.0)) * originalRisk
								sl := entryPrice - bufferedRisk
								target1 := entryPrice + (2.0 * bufferedRisk)

								newPos := &Position{
									Symbol:            symbol,
									Side:              "BUY",
									EntryPrice:        entryPrice,
									SLPrice:           sl,
									Target1Price:      target1,
									Quantity:          100,
									IsPartialExitDone: false,
									EntryTime:         entryTime,
								}
								openPositions[symbol] = newPos
								tradesToday++

								// Monitor the rest of this 5-minute candle on 1m chart
								closed, _ := scan1mActivePosition(newPos, entryTime, entryTime.Add(5*time.Minute), symbol)
								if closed {
									delete(openPositions, symbol)
								}
							}
						}

						// SELL Entry trigger (Setup candle must be GREEN)
						if bias == "SELL_ONLY" && setup.Color == "GREEN" {
							if candle5m.Low < setup.Low {
								entryPrice := setup.Low
								originalRisk := math.Abs(setup.High - entryPrice)
								bufferedRisk := (1.0 + (cfg.SLBufferPct / 100.0)) * originalRisk
								sl := entryPrice + bufferedRisk
								target1 := entryPrice - (2.0 * bufferedRisk)

								newPos := &Position{
									Symbol:            symbol,
									Side:              "SELL",
									EntryPrice:        entryPrice,
									SLPrice:           sl,
									Target1Price:      target1,
									Quantity:          100,
									IsPartialExitDone: false,
									EntryTime:         entryTime,
								}
								openPositions[symbol] = newPos
								tradesToday++

								// Monitor the rest of this 5-minute candle on 1m chart
								closed, _ := scan1mActivePosition(newPos, entryTime, entryTime.Add(5*time.Minute), symbol)
								if closed {
									delete(openPositions, symbol)
								}
							}
						}
					}
				}
			}
		}

		// For positions held after 10:45 AM, scan 1m chart from 10:45 AM to 15:15 PM EOD
		for symbol, pos := range openPositions {
			startTime := time.Date(pos.EntryTime.Year(), pos.EntryTime.Month(), pos.EntryTime.Day(), 10, 45, 0, 0, loc)
			if pos.EntryTime.After(startTime) {
				startTime = pos.EntryTime
			}
			endTime := time.Date(pos.EntryTime.Year(), pos.EntryTime.Month(), pos.EntryTime.Day(), 15, 16, 0, 0, loc)
			scan1mActivePosition(pos, startTime, endTime, symbol)
		}
		openPositions = make(map[string]*Position) // clear at end of day

		dailyStatsList = append(dailyStatsList, DailyStats{
			Date:        dateStr,
			Bias:        bias,
			Advances:    advances,
			Declines:    declines,
			TradesCount: tradesToday,
			PnL:         dailyPnL,
		})
	}

	// Calculate Performance Metrics
	totalTradesCount := len(allTrades)
	winTradesCount := 0
	grossProfit := 0.0
	grossLoss := 0.0
	totalPnL := 0.0

	for _, t := range allTrades {
		totalPnL += t.PnL
		if t.PnL > 0 {
			winTradesCount++
			grossProfit += t.PnL
		} else {
			grossLoss += t.PnL
		}
	}

	winRate := 0.0
	if totalTradesCount > 0 {
		winRate = (float64(winTradesCount) / float64(totalTradesCount)) * 100.0
	}

	avgWin := 0.0
	if winTradesCount > 0 {
		avgWin = grossProfit / float64(winTradesCount)
	}

	avgLoss := 0.0
	if (totalTradesCount - winTradesCount) > 0 {
		avgLoss = grossLoss / float64(totalTradesCount-winTradesCount)
	}

	profitFactor := 0.0
	if grossLoss < 0 {
		profitFactor = grossProfit / math.Abs(grossLoss)
	} else if grossProfit > 0 {
		profitFactor = 999.0
	}

	// Print Reports
	fmt.Println("\n==================================================================")
	fmt.Println("                       DAILY SUMMARY TABLE                        ")
	fmt.Println("==================================================================")
	fmt.Printf("%-12s | %-10s | %-8s | %-8s | %-8s | %-12s\n", "Date", "Bias", "Advances", "Declines", "Trades", "Daily PnL (Rs)")
	fmt.Println("------------------------------------------------------------------")
	for _, d := range dailyStatsList {
		fmt.Printf("%-12s | %-10s | %-8d | %-8d | %-8d | %-12.2f\n", d.Date, d.Bias, d.Advances, d.Declines, d.TradesCount, d.PnL)
	}
	fmt.Println("==================================================================")

	fmt.Println("\n==================================================================")
	fmt.Println("                        ALL TRADES RECORD                         ")
	fmt.Println("==================================================================")
	fmt.Printf("%-10s | %-10s | %-5s | %-20s | %-20s | %-8s | %-8s | %-12s | %-12s\n", "Date", "Symbol", "Side", "Trade Start Time", "Trade End Time", "Qty", "Entry", "Exit", "PnL (Rs)")
	fmt.Println("------------------------------------------------------------------")
	for _, t := range allTrades {
		fmt.Printf("%-10s | %-10s | %-5s | %-20s | %-20s | %-8d | %-8.2f | %-8.2f | %-12.2f\n", t.Date, t.Symbol, t.Side, t.EntryTime[11:], t.ExitTime[11:], t.Quantity, t.EntryPrice, t.ExitPrice, t.PnL)
	}
	fmt.Println("==================================================================")

	fmt.Println("\n==================================================================")
	fmt.Println("                    FINAL PERFORMANCE METRICS                     ")
	fmt.Println("==================================================================")
	fmt.Printf("Total Trades Executed   : %d (Enforced Max 5/Day)\n", totalTradesCount)
	fmt.Printf("Winning Trades          : %d\n", winTradesCount)
	fmt.Printf("Losing Trades           : %d\n", totalTradesCount-winTradesCount)
	fmt.Printf("Win Rate (%%)            : %.2f%%\n", winRate)
	fmt.Printf("Total Net Profit (Rs)   : %.2f\n", totalPnL)
	fmt.Printf("Average Win (Rs)        : %.2f\n", avgWin)
	fmt.Printf("Average Loss (Rs)       : %.2f\n", avgLoss)
	fmt.Printf("Profit Factor           : %.2f\n", profitFactor)
	fmt.Println("==================================================================")
}
