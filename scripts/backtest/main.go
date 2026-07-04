package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
)

// SectorConstituents maps key F&O sectors to their constituent stock symbols
var SectorConstituents = map[string][]string{
	"BANK":   {"HDFCBANK", "ICICIBANK", "KOTAKBANK", "SBIN", "AXISBANK", "INDUSINDBK", "AUBANK", "FEDERALBNK", "PNB", "BANKBARODA"},
	"IT":     {"TCS", "INFY", "WIPRO", "HCLTECH", "TECHM", "LTIM", "COFORGE", "MPHASIS", "PERSISTENT"},
	"AUTO":   {"MARUTI", "TATAMOTORS", "M&M", "BAJAJ-AUTO", "HEROMOTOCO", "TVSMOTOR", "EICHERMOT", "ASHOKLEY", "BALKRISIND"},
	"PHARMA": {"SUNPHARMA", "CIPLA", "DRREDDY", "DIVISLAB", "LUPIN", "AUROPHARMA", "BIOCON", "TORNTPHARM", "IPCALAB"},
	"METAL":  {"TATASTEEL", "JINDALSTEL", "HINDALCO", "JSWSTEEL", "SAIL", "NATIONALUM", "NMDC", "VEDL"},
	"FMCG":   {"HINDUNILVR", "ITC", "NESTLEIND", "BRITANNIA", "TATACONSUM", "DABUR", "MARICO", "GODREJCP", "COLPAL"},
	"ENERGY": {"RELIANCE", "ONGC", "NTPC", "POWERGRID", "BPCL", "IOC", "GAIL", "ADANIENT", "ADANIPORTS"},
	"REALTY": {"DLF", "GODREJPROP", "OBEROIRLTY"},
	"MEDIA":  {"ZEEL", "SUNTV", "PVRINOX"},
}

type BacktestTrade struct {
	Strategy   string
	Date       string
	Symbol     string
	Side       string
	EntryPrice float64
	ExitPrice  float64
	Quantity   int
	PnL        float64
	EntryTime  string
	ExitTime   string
	ExitReason string
}

type DailyStats struct {
	Date        string
	Bias        string
	Advances    int
	Declines    int
	TradesCount int
	PnL         float64
}

type SimResult struct {
	StrategyName string
	TotalTrades  int
	Winning      int
	Losing       int
	WinRate      float64
	TotalPnL     float64
	ProfitFactor float64
	Trades       []BacktestTrade
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

	var kiteClient *kiteconnect.Client
	if cfg.AccessToken != "" && cfg.AccessToken != "your_access_token_here" {
		kiteClient = kiteconnect.New(cfg.APIKey)
		kiteClient.SetAccessToken(cfg.AccessToken)
	}

	ctx := context.Background()
	securityMaster := data.NewSecurityMaster(db.WithContext(ctx), kiteClient, logger.Logger)

	// Fetch security Master watchlist (Union of F&O underlyings)
	watchlist, err := securityMaster.GetFOStocks(ctx)
	if err != nil {
		// Fallback to Nifty 50 if FO fetch fails
		watchlist, err = securityMaster.GetNifty50Constituents(ctx)
		if err != nil {
			log.Fatalf("Failed to fetch watchlist: %v", err)
		}
	}

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}

	fmt.Println("==================================================================")
	fmt.Printf("Starting Multi-Strategy Backtest Runner (%s)\n", time.Now().Format("2006-01-02"))
	fmt.Println("==================================================================")

	// last 9 calendar days to capture at least 6 trading days
	endDate := time.Now().In(loc)
	startDate := endDate.AddDate(0, 0, -9)

	allCandles5m := make(map[string][]kiteconnect.HistoricalData)
	allCandles1m := make(map[string][]kiteconnect.HistoricalData)

	fmt.Println("Loading historical candles from TimescaleDB cache...")
	for symbol, token := range watchlist {
		c5m, err5m := getHistoricalDataFallback(db, "candles_5m", token, startDate, endDate)
		if err5m == nil && len(c5m) > 0 {
			allCandles5m[symbol] = c5m
		}

		c1m, err1m := getHistoricalDataFallback(db, "candles_1m", token, startDate, endDate)
		if err1m == nil && len(c1m) > 0 {
			allCandles1m[symbol] = c1m
		}
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

	// Filter to exact 6 trading days (today + last 5 days)
	if len(dates) > 6 {
		dates = dates[len(dates)-6:]
	}

	fmt.Printf("Evaluating over %d trading dates: %v\n\n", len(dates), dates)

	// Execute Simulations
	lowVolumeRes := runSim("LOW_VOLUME", db, dates, candles5mByDate, candles1mByDate, allCandles5m, cfg, loc)
	vandeBharatRes := runSim("VANDE_BHARAT", db, dates, candles5mByDate, candles1mByDate, allCandles5m, cfg, loc)
	combinedRes := runSim("COMBINED", db, dates, candles5mByDate, candles1mByDate, allCandles5m, cfg, loc)

	// Print Summary Report
	fmt.Println("==================================================================")
	fmt.Println("                  STRATEGY COMPARISON SUMMARY                     ")
	fmt.Println("==================================================================")
	fmt.Printf("%-15s | %-12s | %-12s | %-12s | %-12s\n", "Strategy", "Total Trades", "Win Rate", "Net PnL (Rs)", "Profit Factor")
	fmt.Println("------------------------------------------------------------------")
	printResultRow(lowVolumeRes)
	printResultRow(vandeBharatRes)
	printResultRow(combinedRes)
	fmt.Println("==================================================================")

	// Write Report to Artifact file
	writeReportToArtifact(dates, lowVolumeRes, vandeBharatRes, combinedRes)
}

func printResultRow(r SimResult) {
	fmt.Printf("%-15s | %-12d | %-11.2f%% | %-12.2f | %-12.2f\n", r.StrategyName, r.TotalTrades, r.WinRate, r.TotalPnL, r.ProfitFactor)
}

func runSim(mode string, db *data.Database, dates []string, candles5mByDate, candles1mByDate map[string]map[string][]kiteconnect.HistoricalData, allCandles5m map[string][]kiteconnect.HistoricalData, cfg *config.Settings, loc *time.Location) SimResult {
	var allTrades []BacktestTrade

	type Position struct {
		Strategy          string
		Symbol            string
		Side              string
		EntryPrice        float64
		SLPrice           float64
		Target1Price      float64
		Quantity          int
		IsPartialExitDone bool
		EntryTime         time.Time
	}

	// Dynamic simulated timeSlots
	var timeSlots []struct{ h, m int }
	for h := 9; h <= 15; h++ {
		startM := 0
		if h == 9 {
			startM = 30
		}
		for m := startM; m < 60; m += 5 {
			if h == 15 && m > 30 {
				break
			}
			timeSlots = append(timeSlots, struct{ h, m int }{h, m})
		}
	}

	for _, dateStr := range dates {
		dayData5m := candles5mByDate[dateStr]
		dayData1m := candles1mByDate[dateStr]

		// 1. Pre-market Breadth
		advances := 0
		declines := 0
		for _, candles := range dayData5m {
			var open0915, ltp0925 float64
			foundOpen, foundLTP := false, false
			for _, c := range candles {
				cTime := c.Date.In(loc)
				if cTime.Format("2006-01-02") == dateStr {
					if cTime.Hour() == 9 && cTime.Minute() == 15 {
						open0915 = c.Open
						foundOpen = true
					}
					if cTime.Hour() == 9 && cTime.Minute() == 25 {
						ltp0925 = c.Close
						foundLTP = true
					}
				}
			}
			if foundOpen && foundLTP && open0915 > 0 {
				if ltp0925 > open0915 {
					advances++
				} else if ltp0925 < open0915 {
					declines++
				}
			}
		}

		bias := "SELL_ONLY"
		var dbBias string
		errBias := db.QueryRow("SELECT bias FROM daily_market_bias WHERE date = $1", dateStr).Scan(&dbBias)
		if errBias == nil && dbBias != "" {
			bias = dbBias
		} else {
			if advances > declines {
				bias = "BUY_ONLY"
			}
		}

		// 2. Watchlist Building
		// Low Volume Watchlist
		lvWatchlist := make(map[string]bool)
		type StockChange struct {
			Symbol    string
			PctChange float64
		}
		var lvChanges []StockChange
		for symbol, candles := range dayData5m {
			var open0915, ltp0925 float64
			foundOpen, foundLTP := false, false
			for _, c := range candles {
				cTime := c.Date.In(loc)
				if cTime.Format("2006-01-02") == dateStr {
					if cTime.Hour() == 9 && cTime.Minute() == 15 {
						open0915 = c.Open
						foundOpen = true
					}
					if cTime.Hour() == 9 && cTime.Minute() == 25 {
						ltp0925 = c.Close
						foundLTP = true
					}
				}
			}
			if foundOpen && foundLTP && open0915 > 0 {
				chg := ((ltp0925 - open0915) / open0915) * 100.0
				if math.Abs(chg) <= cfg.WatchlistMaxPctChange {
					lvChanges = append(lvChanges, StockChange{Symbol: symbol, PctChange: chg})
				}
			}
		}
		if bias == "BUY_ONLY" {
			sort.Slice(lvChanges, func(i, j int) bool { return lvChanges[i].PctChange > lvChanges[j].PctChange })
		} else {
			sort.Slice(lvChanges, func(i, j int) bool { return lvChanges[i].PctChange < lvChanges[j].PctChange })
		}
		for i := 0; i < len(lvChanges) && i < cfg.StrategyWatchlistSize; i++ {
			lvWatchlist[lvChanges[i].Symbol] = true
		}

		// Sectoral Watchlist
		vbWatchlist := selectSectoralWatchlistBacktest(dateStr, dayData5m, bias, cfg, loc)

		// Setup PDH/PDL reference levels for Vande Bharat
		pdHighs := make(map[string]float64)
		pdLows := make(map[string]float64)
		if mode == "VANDE_BHARAT" || mode == "COMBINED" {
			for symbol := range vbWatchlist {
				high, low, err := getPreviousDayHighLowBacktest(symbol, dateStr, allCandles5m[symbol], loc)
				if err == nil {
					pdHighs[symbol] = high
					pdLows[symbol] = low
				}
			}
		}

		// Track Vande Bharat engine state
		vbMaster := make(map[string]kiteconnect.HistoricalData)
		vbConfirm := make(map[string]kiteconnect.HistoricalData)
		vbTriggered := make(map[string]bool)

		openPositions := make(map[string]*Position)
		tradesCount := 0
		dailyPnL := 0.0

		// Helper to scan 1m candles for target/stop exits
		scanExit := func(pos *Position, startTime, endTime time.Time, symbol string) bool {
			symbol1mCandles := dayData1m[symbol]
			sort.Slice(symbol1mCandles, func(i, j int) bool {
				return symbol1mCandles[i].Date.Time.Before(symbol1mCandles[j].Date.Time)
			})

			for _, c1m := range symbol1mCandles {
				c1mTime := c1m.Date.In(loc)
				if (c1mTime.After(startTime) || c1mTime.Equal(startTime)) && c1mTime.Before(endTime) {
					// 1. Target 1 partial exit (50% exit)
					if !pos.IsPartialExitDone {
						if pos.Side == "BUY" && c1m.High >= pos.Target1Price {
							pos.IsPartialExitDone = true
							pos.SLPrice = pos.EntryPrice
							closeQty := pos.Quantity / 2
							pnl := (pos.Target1Price - pos.EntryPrice) * float64(closeQty)
							dailyPnL += pnl
							pos.Quantity -= closeQty

							allTrades = append(allTrades, BacktestTrade{
								Strategy:   pos.Strategy,
								Date:       dateStr,
								Symbol:     symbol,
								Side:       pos.Side,
								EntryPrice: pos.EntryPrice,
								ExitPrice:  pos.Target1Price,
								Quantity:   closeQty,
								PnL:        pnl,
								EntryTime:  pos.EntryTime.Format("2006-01-02 15:04:00"),
								ExitTime:   c1mTime.Format("2006-01-02 15:04:00"),
								ExitReason: "PARTIAL_TARGET1",
							})
						} else if pos.Side == "SELL" && c1m.Low <= pos.Target1Price {
							pos.IsPartialExitDone = true
							pos.SLPrice = pos.EntryPrice
							closeQty := pos.Quantity / 2
							pnl := (pos.EntryPrice - pos.Target1Price) * float64(closeQty)
							dailyPnL += pnl
							pos.Quantity -= closeQty

							allTrades = append(allTrades, BacktestTrade{
								Strategy:   pos.Strategy,
								Date:       dateStr,
								Symbol:     symbol,
								Side:       pos.Side,
								EntryPrice: pos.EntryPrice,
								ExitPrice:  pos.Target1Price,
								Quantity:   closeQty,
								PnL:        pnl,
								EntryTime:  pos.EntryTime.Format("2006-01-02 15:04:00"),
								ExitTime:   c1mTime.Format("2006-01-02 15:04:00"),
								ExitReason: "PARTIAL_TARGET1",
							})
						}
					}

					// 2. Stop-Loss exit
					if pos.Side == "BUY" && c1m.Low <= pos.SLPrice {
						exitQty := pos.Quantity
						pnl := (pos.SLPrice - pos.EntryPrice) * float64(exitQty)
						dailyPnL += pnl

						allTrades = append(allTrades, BacktestTrade{
							Strategy:   pos.Strategy,
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
						return true
					} else if pos.Side == "SELL" && c1m.High >= pos.SLPrice {
						exitQty := pos.Quantity
						pnl := (pos.EntryPrice - pos.SLPrice) * float64(exitQty)
						dailyPnL += pnl

						allTrades = append(allTrades, BacktestTrade{
							Strategy:   pos.Strategy,
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
						return true
					}

					// 3. EOD square-off
					if c1mTime.Hour() == 15 && c1mTime.Minute() == 15 {
						pnl := 0.0
						if pos.Side == "BUY" {
							pnl = (c1m.Close - pos.EntryPrice) * float64(pos.Quantity)
						} else {
							pnl = (pos.EntryPrice - c1m.Close) * float64(pos.Quantity)
						}
						dailyPnL += pnl

						allTrades = append(allTrades, BacktestTrade{
							Strategy:   pos.Strategy,
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
						return true
					}
				}
			}
			return false
		}

		// Run simulation loop
		for _, slot := range timeSlots {
			var vbResets []string

			// Update Vande Bharat candle close setup detections
			if mode == "VANDE_BHARAT" || mode == "COMBINED" {
				for symbol := range vbWatchlist {
					var c5m kiteconnect.HistoricalData
					found := false
					for _, c := range dayData5m[symbol] {
						cTime := c.Date.In(loc)
						if cTime.Hour() == slot.h && cTime.Minute() == slot.m {
							c5m = c
							found = true
							break
						}
					}
					if !found {
						continue
					}

					pdh, okH := pdHighs[symbol]
					pdl, okL := pdLows[symbol]
					if !okH || !okL || pdh <= 0 || pdl <= 0 {
						continue
					}

					// Master Candle detection
					if _, hasMaster := vbMaster[symbol]; !hasMaster {
						isBuy := c5m.Close > pdh && c5m.Close > c5m.Open
						isSell := c5m.Close < pdl && c5m.Close < c5m.Open

						if isBuy || isSell {
							cRange := c5m.High - c5m.Low
							allowed := (cfg.VBMasterMaxPct / 100.0) * c5m.Close
							if cRange <= allowed {
								vbMaster[symbol] = c5m
							}
						}
					} else if _, hasConfirm := vbConfirm[symbol]; !hasConfirm {
						// Confirmation Candle detection
						mCandle := vbMaster[symbol]
						isBuySetup := mCandle.Close > pdh

						var confirmed bool
						if isBuySetup {
							confirmed = c5m.Close > mCandle.High && c5m.Close > c5m.Open
						} else {
							confirmed = c5m.Close < mCandle.Low && c5m.Close < c5m.Open
						}

						if confirmed {
							cRange := c5m.High - c5m.Low
							allowed := (cfg.VBConfirmMaxPct / 100.0) * c5m.Close
							if cRange <= allowed {
								vbConfirm[symbol] = c5m
							} else {
								delete(vbMaster, symbol) // reset
							}
						} else {
							delete(vbMaster, symbol) // reset
						}
					} else {
						// If both Master and Confirmation candles are set,
						// and we are at the end of the next slot (the 3rd candle slot),
						// the trigger window has closed. Reset the setup.
						confirm := vbConfirm[symbol]
						cTime := c5m.Date.In(loc)
						confirmTime := confirm.Date.In(loc)
						if cTime.After(confirmTime) {
							vbResets = append(vbResets, symbol)
						}
					}
				}
			}

			// Process breakouts and exits in the current 5m slot
			for symbol, pos := range openPositions {
				cStartTime := time.Date(pos.EntryTime.Year(), pos.EntryTime.Month(), pos.EntryTime.Day(), slot.h, slot.m, 0, 0, loc)
				cEndTime := cStartTime.Add(5 * time.Minute)
				if scanExit(pos, cStartTime, cEndTime, symbol) {
					delete(openPositions, symbol)
				}
			}

			// Check for new breakouts entries
			if tradesCount < 5 {
				for symbol := range dayData5m {
					if _, open := openPositions[symbol]; open {
						continue
					}

					// Find 5m candle for this slot
					var c5m kiteconnect.HistoricalData
					found := false
					for _, c := range dayData5m[symbol] {
						cTime := c.Date.In(loc)
						if cTime.Hour() == slot.h && cTime.Minute() == slot.m {
							c5m = c
							found = true
							break
						}
					}
					if !found {
						continue
					}

					entryTime := c5m.Date.In(loc)

					// 1. Sim LOW_VOLUME breakouts (09:30 - 10:45)
					if (mode == "LOW_VOLUME" || mode == "COMBINED") && lvWatchlist[symbol] {
						inWindow := (slot.h == 9 && slot.m >= 30) || (slot.h == 10 && slot.m <= 45)
						if inWindow {
							// Find Setup Candle
							var minVol int64 = -1
							var minVolCandle kiteconnect.HistoricalData
							foundSetup := false
							for _, c := range dayData5m[symbol] {
								cTime := c.Date.In(loc)
								if cTime.Format("2006-01-02") == dateStr && cTime.Before(entryTime) {
									if minVol == -1 || int64(c.Volume) < minVol {
										minVol = int64(c.Volume)
										minVolCandle = c
										foundSetup = true
									}
								}
							}
							prevCandleStart := entryTime.Add(-5 * time.Minute)
							if foundSetup && minVolCandle.Date.Time.In(loc).Equal(prevCandleStart) {
								color := "DOJI"
								if minVolCandle.Close < minVolCandle.Open {
									color = "RED"
								} else if minVolCandle.Close > minVolCandle.Open {
									color = "GREEN"
								}

								if bias == "BUY_ONLY" && color == "RED" && c5m.High > minVolCandle.High {
									entryPrice := minVolCandle.High
									risk := math.Abs(entryPrice - minVolCandle.Low)
									bufRisk := (1.0 + (cfg.SLBufferPct / 100.0)) * risk
									qty := int(math.Floor((cfg.MaxCapitalPerTrade * 5.0) / entryPrice))

									if qty > 0 {
										target1 := entryPrice + (cfg.RiskRewardRatio * bufRisk)

										newPos := &Position{
											Strategy:          "LOW_VOLUME",
											Symbol:            symbol,
											Side:              "BUY",
											EntryPrice:        entryPrice,
											SLPrice:           entryPrice - bufRisk,
											Target1Price:      target1,
											Quantity:          qty,
											IsPartialExitDone: false,
											EntryTime:         entryTime,
										}
										openPositions[symbol] = newPos
										tradesCount++

										// Scan the rest of this candle
										if scanExit(newPos, entryTime, entryTime.Add(5*time.Minute), symbol) {
											delete(openPositions, symbol)
										}
										continue
									}
								} else if bias == "SELL_ONLY" && color == "GREEN" && c5m.Low < minVolCandle.Low {
									entryPrice := minVolCandle.Low
									risk := math.Abs(minVolCandle.High - entryPrice)
									bufRisk := (1.0 + (cfg.SLBufferPct / 100.0)) * risk
									qty := int(math.Floor((cfg.MaxCapitalPerTrade * 5.0) / entryPrice))

									if qty > 0 {
										target1 := entryPrice - (cfg.RiskRewardRatio * bufRisk)

										newPos := &Position{
											Strategy:          "LOW_VOLUME",
											Symbol:            symbol,
											Side:              "SELL",
											EntryPrice:        entryPrice,
											SLPrice:           entryPrice + bufRisk,
											Target1Price:      target1,
											Quantity:          qty,
											IsPartialExitDone: false,
											EntryTime:         entryTime,
										}
										openPositions[symbol] = newPos
										tradesCount++

										if scanExit(newPos, entryTime, entryTime.Add(5*time.Minute), symbol) {
											delete(openPositions, symbol)
										}
										continue
									}
								}
							}
						}
					}

					// 2. Sim VANDE_BHARAT breakouts (09:26 - 11:00)
					if (mode == "VANDE_BHARAT" || mode == "COMBINED") && vbWatchlist[symbol] && !vbTriggered[symbol] {
						inWindow := (slot.h == 9 && slot.m >= 26) || (slot.h == 10) || (slot.h == 11 && slot.m == 0)
						if inWindow {
							if confirm, hasConfirm := vbConfirm[symbol]; hasConfirm {
								if bias == "BUY_ONLY" && c5m.High > confirm.High {
									entryPrice := confirm.High
									risk := math.Abs(entryPrice - confirm.Low)
									bufRisk := 1.10 * risk // 10% risk buffer
									qty := int(math.Floor((cfg.MaxCapitalPerTrade * 5.0) / entryPrice))

									if qty > 0 {
										target1 := entryPrice + (cfg.RiskRewardRatio * bufRisk)

										newPos := &Position{
											Strategy:          "VANDE_BHARAT",
											Symbol:            symbol,
											Side:              "BUY",
											EntryPrice:        entryPrice,
											SLPrice:           entryPrice - bufRisk,
											Target1Price:      target1,
											Quantity:          qty,
											IsPartialExitDone: false,
											EntryTime:         entryTime,
										}
										openPositions[symbol] = newPos
										tradesCount++
										vbTriggered[symbol] = true

										if scanExit(newPos, entryTime, entryTime.Add(5*time.Minute), symbol) {
											delete(openPositions, symbol)
										}
									}
								} else if bias == "SELL_ONLY" && c5m.Low < confirm.Low {
									entryPrice := confirm.Low
									risk := math.Abs(confirm.High - entryPrice)
									bufRisk := 1.10 * risk
									qty := int(math.Floor((cfg.MaxCapitalPerTrade * 5.0) / entryPrice))

									if qty > 0 {
										target1 := entryPrice - (cfg.RiskRewardRatio * bufRisk)

										newPos := &Position{
											Strategy:          "VANDE_BHARAT",
											Symbol:            symbol,
											Side:              "SELL",
											EntryPrice:        entryPrice,
											SLPrice:           entryPrice + bufRisk,
											Target1Price:      target1,
											Quantity:          qty,
											IsPartialExitDone: false,
											EntryTime:         entryTime,
										}
										openPositions[symbol] = newPos
										tradesCount++
										vbTriggered[symbol] = true

										if scanExit(newPos, entryTime, entryTime.Add(5*time.Minute), symbol) {
											delete(openPositions, symbol)
										}
									}
								}
							}
						}
					}
				}
			}

			// Apply deferred resets for Vande Bharat setups
			for _, sym := range vbResets {
				delete(vbMaster, sym)
				delete(vbConfirm, sym)
			}
		}

		// Force liquidate remaining positions at 15:15 EOD
		for symbol, pos := range openPositions {
			endTime := time.Date(pos.EntryTime.Year(), pos.EntryTime.Month(), pos.EntryTime.Day(), 15, 16, 0, 0, loc)
			scanExit(pos, pos.EntryTime, endTime, symbol)
		}
	}

	// Performance calculations
	totalTradesCount := len(allTrades)
	winTradesCount := 0
	grossProfit := 0.0
	grossLoss := 0.0
	totalPnL := 0.0

	for _, t := range allTrades {
		totalPnL += t.PnL
		if t.PnL >= 0 {
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

	profitFactor := 0.0
	if grossLoss < 0 {
		profitFactor = grossProfit / math.Abs(grossLoss)
	} else if grossProfit > 0 {
		profitFactor = 999.0
	}

	return SimResult{
		StrategyName: mode,
		TotalTrades:  totalTradesCount,
		Winning:      winTradesCount,
		Losing:       totalTradesCount - winTradesCount,
		WinRate:      winRate,
		TotalPnL:     totalPnL,
		ProfitFactor: profitFactor,
		Trades:       allTrades,
	}
}

func selectSectoralWatchlistBacktest(dateStr string, dayData5m map[string][]kiteconnect.HistoricalData, bias string, cfg *config.Settings, loc *time.Location) map[string]bool {
	stockChanges := make(map[string]float64)
	for symbol, candles := range dayData5m {
		var openVal, closeVal float64
		foundOpen := false
		foundClose := false
		for _, c := range candles {
			cTime := c.Date.In(loc)
			if cTime.Format("2006-01-02") == dateStr {
				if cTime.Hour() == 9 && cTime.Minute() == 15 {
					openVal = c.Open
					foundOpen = true
				}
				if cTime.Hour() == 9 && cTime.Minute() == 25 {
					closeVal = c.Close
					foundClose = true
				}
			}
		}
		if foundOpen && foundClose && openVal > 0 {
			stockChanges[symbol] = ((closeVal - openVal) / openVal) * 100.0
		}
	}

	sectorChanges := make(map[string]float64)
	for sector, constituents := range SectorConstituents {
		var sum float64
		count := 0
		for _, sym := range constituents {
			if change, exists := stockChanges[sym]; exists {
				sum += change
				count++
			}
		}
		if count > 0 {
			sectorChanges[sector] = sum / float64(count)
		}
	}

	type SectorPerf struct {
		Name   string
		Change float64
	}
	var filteredSectors []SectorPerf
	for name, change := range sectorChanges {
		if bias == "BUY_ONLY" {
			if change <= cfg.SectorMaxBuyPct {
				filteredSectors = append(filteredSectors, SectorPerf{Name: name, Change: change})
			}
		} else { // SELL_ONLY
			if change <= cfg.SectorMaxSellPct {
				filteredSectors = append(filteredSectors, SectorPerf{Name: name, Change: change})
			}
		}
	}

	if len(filteredSectors) == 0 {
		return make(map[string]bool)
	}

	if bias == "BUY_ONLY" {
		sort.Slice(filteredSectors, func(i, j int) bool {
			return filteredSectors[i].Change > filteredSectors[j].Change
		})
	} else {
		sort.Slice(filteredSectors, func(i, j int) bool {
			return filteredSectors[i].Change < filteredSectors[j].Change
		})
	}

	topCount := 2
	if len(filteredSectors) < topCount {
		topCount = len(filteredSectors)
	}

	selectedSectors := make(map[string]bool)
	for i := 0; i < topCount; i++ {
		selectedSectors[filteredSectors[i].Name] = true
	}

	type StockPerf struct {
		Symbol string
		Change float64
	}
	var eligibleStocks []StockPerf
	for sector := range selectedSectors {
		for _, sym := range SectorConstituents[sector] {
			change, exists := stockChanges[sym]
			if !exists {
				continue
			}
			if bias == "BUY_ONLY" {
				if change <= cfg.StockMaxBuyPct {
					eligibleStocks = append(eligibleStocks, StockPerf{Symbol: sym, Change: change})
				}
			} else { // SELL_ONLY
				if change >= cfg.StockMaxSellPct {
					eligibleStocks = append(eligibleStocks, StockPerf{Symbol: sym, Change: change})
				}
			}
		}
	}

	if bias == "BUY_ONLY" {
		sort.Slice(eligibleStocks, func(i, j int) bool {
			return eligibleStocks[i].Change > eligibleStocks[j].Change
		})
	} else {
		sort.Slice(eligibleStocks, func(i, j int) bool {
			return eligibleStocks[i].Change < eligibleStocks[j].Change
		})
	}

	finalSize := cfg.StrategyWatchlistSize
	if len(eligibleStocks) < finalSize {
		finalSize = len(eligibleStocks)
	}

	resultMap := make(map[string]bool)
	for i := 0; i < finalSize; i++ {
		resultMap[eligibleStocks[i].Symbol] = true
	}
	return resultMap
}

func getPreviousDayHighLowBacktest(symbol string, dateStr string, allCandles []kiteconnect.HistoricalData, loc *time.Location) (float64, float64, error) {
	var prevDateStr string
	for i := len(allCandles) - 1; i >= 0; i-- {
		cTime := allCandles[i].Date.In(loc)
		cDateStr := cTime.Format("2006-01-02")
		if cDateStr < dateStr {
			prevDateStr = cDateStr
			break
		}
	}
	if prevDateStr == "" {
		return 0, 0, fmt.Errorf("no previous day found")
	}

	var maxHigh float64 = 0
	var minLow float64 = 999999
	found := false
	for _, c := range allCandles {
		cTime := c.Date.In(loc)
		if cTime.Format("2006-01-02") == prevDateStr {
			if c.High > maxHigh {
				maxHigh = c.High
			}
			if c.Low < minLow {
				minLow = c.Low
			}
			found = true
		}
	}
	if !found {
		return 0, 0, fmt.Errorf("no previous day candles found")
	}
	return maxHigh, minLow, nil
}

func getHistoricalDataFallback(db *data.Database, tableName string, token int64, startDate, endDate time.Time) ([]kiteconnect.HistoricalData, error) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}

	query := fmt.Sprintf(`
		SELECT time, open, high, low, close, volume
		FROM %s
		WHERE token = $1 AND time >= $2 AND time <= $3
		ORDER BY time ASC
	`, tableName)

	rows, err := db.Query(query, token, startDate.Format("2006-01-02 15:04:05"), endDate.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candles []kiteconnect.HistoricalData
	for rows.Next() {
		var c kiteconnect.HistoricalData
		var t time.Time
		if err := rows.Scan(&t, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume); err != nil {
			return nil, err
		}
		
		// Normalise time zone shift differences between seeded data and live database data
		if t.Hour() >= 9 && t.Hour() <= 16 {
			c.Date.Time = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc)
		} else {
			c.Date.Time = t.In(loc)
		}
		
		candles = append(candles, c)
	}

	// Normalise volume if it is cumulative (monotonically increasing)
	isCumulative := true
	var lastVol int64 = -1
	for _, c := range candles {
		if lastVol != -1 && int64(c.Volume) < lastVol {
			isCumulative = false
			break
		}
		lastVol = int64(c.Volume)
	}

	if isCumulative && len(candles) > 1 {
		var prevVol int64 = 0
		for i := range candles {
			currentVol := int64(candles[i].Volume)
			diff := currentVol - prevVol
			if diff < 0 {
				diff = 0
			}
			candles[i].Volume = int(diff)
			prevVol = currentVol
		}
	}

	return candles, nil
}

func writeReportToArtifact(dates []string, lv, vb, comb SimResult) {
	artifactDir := os.Getenv("ARTIFACT_DIR")
	if artifactDir == "" {
		artifactDir = "C:\\Users\\Dell\\.gemini\\antigravity-cli\\brain\\03b85694-13f2-4638-8194-90d614327607"
	}
	reportPath := artifactDir + "\\backtest_report.md"

	f, err := os.Create(reportPath)
	if err != nil {
		log.Printf("Warning: failed to create backtest report file: %v", err)
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "# Strategy Performance Backtest Report\n\n")
	fmt.Fprintf(f, "Evaluation Dates: `%v`\n\n", dates)

	fmt.Fprintf(f, "## 📊 Strategy Summary Table\n\n")
	fmt.Fprintf(f, "| Strategy | Total Trades | Win Rate | Net PnL (Rs) | Profit Factor |\n")
	fmt.Fprintf(f, "| :--- | :---: | :---: | :---: | :---: |\n")
	fmt.Fprintf(f, "| **LOW_VOLUME Only** | %d | %.2f%% | %.2f | %.2f |\n", lv.TotalTrades, lv.WinRate, lv.TotalPnL, lv.ProfitFactor)
	fmt.Fprintf(f, "| **VANDE_BHARAT Only** | %d | %.2f%% | %.2f | %.2f |\n", vb.TotalTrades, vb.WinRate, vb.TotalPnL, vb.ProfitFactor)
	fmt.Fprintf(f, "| **COMBINED (With Duplicate Protection)** | %d | %.2f%% | %.2f | %.2f |\n\n", comb.TotalTrades, comb.WinRate, comb.TotalPnL, comb.ProfitFactor)

	writeTradesSection(f, "LOW_VOLUME", lv.Trades)
	writeTradesSection(f, "VANDE_BHARAT", vb.Trades)
	writeTradesSection(f, "COMBINED", comb.Trades)
}

func writeTradesSection(f *os.File, name string, trades []BacktestTrade) {
	fmt.Fprintf(f, "## 📜 All Trades Log - %s\n\n", name)
	fmt.Fprintf(f, "| Date | Symbol | Side | Entry Time | Exit Time | Qty | Entry Price | Exit Price | PnL (Rs) | Reason |\n")
	fmt.Fprintf(f, "| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")
	for _, t := range trades {
		fmt.Fprintf(f, "| %s | %s | %s | %s | %s | %d | %.2f | %.2f | %.2f | %s |\n",
			t.Date, t.Symbol, t.Side, t.EntryTime[11:], t.ExitTime[11:], t.Quantity, t.EntryPrice, t.ExitPrice, t.PnL, t.ExitReason)
	}
	fmt.Fprintf(f, "\n")
}
