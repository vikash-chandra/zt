package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
	"zerodha-trading/risk"
	"zerodha-trading/selection"
	"zerodha-trading/strategy"
)

type TradeLog struct {
	ID              int
	Day             string
	EntryTimeStr    string
	ExitTimeStr     string
	Symbol          string
	OptionType      string
	Side            string
	Quantity        int
	Multiplier      int
	EntryPrice      float64
	ExitPrice       float64
	PnL             float64
	TimeHeldMinutes int
	ExitReason      string
	EntryMarker     string
	ExitMarker      string
	EntryCandleUnix int64
	ExitCandleUnix  int64
}

func estimateOptionPremium(spotPrice float64, strike float64, optionType string, tIST time.Time) float64 {
	dist := math.Abs(spotPrice - strike)
	base := 120.0 + (300.0-dist)*0.15
	if base < 45.0 {
		base = 45.0
	}
	if base > 160.0 {
		base = 160.0
	}

	minsFromOpen := float64(tIST.Hour()*60 + tIST.Minute() - (9*60 + 15))
	if minsFromOpen < 0 {
		minsFromOpen = 0
	}
	decayFraction := (minsFromOpen / 375.0)
	if decayFraction > 1.0 {
		decayFraction = 1.0
	}

	premium := base * (1.0 - 0.55*decayFraction)
	return math.Round(premium*20.0) / 20.0 // Round to nearest 0.05 tick
}

func main() {
	fmt.Println("==========================================================================")
	fmt.Println("  5-DAY TRIPLE SUPERTREND OPTIONS STRATEGY SIMULATION & UI ARROW REPORT   ")
	fmt.Println("==========================================================================")

	// 1. Load Config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logger, err := monitoring.NewLogger(cfg.LogLevel)
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}

	var allCandles []data.Candle

	// Try reading from scratch/candles.json first
	jsonPath := filepath.Join("scratch", "candles.json")
	if bytesData, err := os.ReadFile(jsonPath); err == nil && len(bytesData) > 0 {
		bytesData = bytes.TrimPrefix(bytesData, []byte("\xef\xbb\xbf"))
		type rawCandle struct {
			Time   string  `json:"time"`
			Open   float64 `json:"open"`
			High   float64 `json:"high"`
			Low    float64 `json:"low"`
			Close  float64 `json:"close"`
			Volume int64   `json:"volume"`
		}
		var raws []rawCandle
		errUn := json.Unmarshal(bytesData, &raws)
		if errUn != nil {
			log.Printf("JSON unmarshal error: %v", errUn)
		}
		if errUn == nil && len(raws) > 0 {
			for _, r := range raws {
				parsedTime, _ := time.Parse("2006-01-02T15:04:05", r.Time)
				if parsedTime.IsZero() {
					parsedTime, _ = time.Parse(time.RFC3339, r.Time)
				}
				if parsedTime.IsZero() {
					parsedTime, _ = time.Parse("2006-01-02T15:04:05Z", r.Time)
				}
				allCandles = append(allCandles, data.Candle{
					Token:  256265,
					Time:   data.NormalizeToIST(parsedTime),
					Open:   r.Open,
					High:   r.High,
					Low:    r.Low,
					Close:  r.Close,
					Volume: r.Volume,
				})
			}
			fmt.Printf("✓ Successfully loaded %d candles from AWS DB dump (%s)\n", len(allCandles), jsonPath)
		}
	}

	var db *data.Database
	if len(allCandles) == 0 {
		var err error
		db, err = data.NewDatabase(
			cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
			logger.Logger,
		)
		if err == nil {
			defer db.Close()
			niftyToken := int64(256265)
			allCandles, _ = db.GetLastNCandles("candles_5m", niftyToken, 1000)
		}
	}

	if len(allCandles) == 0 {
		log.Fatalf("No candles found for NIFTY 50 in DB or dump file.")
	}

	// Group candles by IST date and filter EOD candles > 15:10
	cutoffH, cutoffM, cutoffS := 15, 15, 0
	if parts := strings.Split(cfg.Options.SuperTrendCutoffTime, ":"); len(parts) >= 2 {
		fmt.Sscanf(parts[0], "%d", &cutoffH)
		fmt.Sscanf(parts[1], "%d", &cutoffM)
		if len(parts) >= 3 {
			fmt.Sscanf(parts[2], "%d", &cutoffS)
		}
	}
	cutoffSecOfDay := cutoffH*3600 + cutoffM*60 + cutoffS

	dailyMap := make(map[string][]data.Candle)
	var dateList []string

	for _, c := range allCandles {
		tIST := data.NormalizeToIST(c.Time)
		cSecOfDay := tIST.Hour()*3600 + tIST.Minute()*60 + tIST.Second()
		if cSecOfDay > cutoffSecOfDay {
			continue // Discard EOD 15:15, 15:20, 15:25
		}
		dStr := tIST.Format("2006-01-02")
		if _, exists := dailyMap[dStr]; !exists {
			dateList = append(dateList, dStr)
		}
		c.Time = tIST
		dailyMap[dStr] = append(dailyMap[dStr], c)
	}

	sort.Strings(dateList)
	if len(dateList) > 5 {
		dateList = dateList[len(dateList)-5:] // Keep last 5 trading days
	}

	fmt.Printf("Simulating Strategy for Last %d Trading Days: %v\n\n", len(dateList), dateList)

	// Initialize Strategy & Position Manager
	stEngine := strategy.NewSuperTrendOptionsEngine(
		cfg.Options.SuperTrendST1Period, cfg.Options.SuperTrendST2Period, cfg.Options.SuperTrendST3Period,
		cfg.Options.SuperTrendST1Factor, cfg.Options.SuperTrendST2Factor, cfg.Options.SuperTrendST3Factor,
	)
	strikeSelector := selection.NewOptionStrikeSelector(nil)

	var simulatedTrades []TradeLog
	tradeCounter := 0
	totalPnL := 0.0
	wins := 0
	losses := 0

	for _, dStr := range dateList {
		dayCandles := dailyMap[dStr]
		if len(dayCandles) == 0 {
			continue
		}

		posMgr := risk.NewOptionsPositionManager(
			db, logger.Logger, cfg.Options.BaseLotSize, cfg.Options.MaxQuantityMultiplier,
			cfg.Options.OptionsSLPct, cfg.Options.PaperBalance,
		)
		posMgr.ResetDailyMultiplier()

		var runningCandles []data.Candle

		for i, candle := range dayCandles {
			runningCandles = append(runningCandles, candle)
			if len(runningCandles) < 10 {
				continue
			}

			stRes := stEngine.CalculateTripleSuperTrend(runningCandles)
			tIST := candle.Time

			// Check Stop-Loss on Active Position
			if active := posMgr.GetActivePosition(); active != nil {
				// Estimate option high premium for SL check
				highEst := estimateOptionPremium(candle.High, 24000, active.Symbol[len(active.Symbol)-2:], tIST)
				lowEst := estimateOptionPremium(candle.Low, 24000, active.Symbol[len(active.Symbol)-2:], tIST)
				currentPrem := estimateOptionPremium(candle.Close, 24000, active.Symbol[len(active.Symbol)-2:], tIST)
				active.LatestPrice = currentPrem

				if highEst >= active.SLPrice {
					// SL Hit!
					exitPrice := active.SLPrice
					pnl := posMgr.OnTradeClosed(exitPrice, "EXIT_SL")

					tradeCounter++
					heldMins := int(tIST.Sub(active.CreatedAt).Minutes())
					if heldMins < 1 {
						heldMins = 1
					}

					simulatedTrades = append(simulatedTrades, TradeLog{
						ID:              tradeCounter,
						Day:             dStr,
						EntryTimeStr:    active.CreatedAt.Format("15:04:05"),
						ExitTimeStr:     tIST.Format("15:04:05"),
						Symbol:          active.Symbol,
						OptionType:      active.Symbol[len(active.Symbol)-2:],
						Side:            active.Side,
						Quantity:        active.Quantity,
						Multiplier:      active.Quantity / cfg.Options.BaseLotSize,
						EntryPrice:      active.EntryPremium,
						ExitPrice:       exitPrice,
						PnL:             pnl,
						TimeHeldMinutes: heldMins,
						ExitReason:      "SL HIT (-50%)",
						EntryMarker:     fmt.Sprintf("SELL_%s", active.Symbol[len(active.Symbol)-2:]),
						ExitMarker:      "EXIT_SL",
						EntryCandleUnix: (active.CreatedAt.Unix() / 300) * 300,
						ExitCandleUnix:  (tIST.Unix() / 300) * 300,
					})

					totalPnL += pnl
					losses++
					continue
				}
				_ = lowEst
			}

			// EOD Square-Off at 15:14
			if (tIST.Hour() == 15 && tIST.Minute() >= 14) || i == len(dayCandles)-1 {
				if active := posMgr.GetActivePosition(); active != nil {
					exitPrice := estimateOptionPremium(candle.Close, 24000, active.Symbol[len(active.Symbol)-2:], tIST)
					pnl := posMgr.OnTradeClosed(exitPrice, "EXIT_EOD")

					tradeCounter++
					heldMins := int(tIST.Sub(active.CreatedAt).Minutes())
					if heldMins < 1 {
						heldMins = 1
					}

					if pnl >= 0 {
						wins++
					} else {
						losses++
					}
					totalPnL += pnl

					simulatedTrades = append(simulatedTrades, TradeLog{
						ID:              tradeCounter,
						Day:             dStr,
						EntryTimeStr:    active.CreatedAt.Format("15:04:05"),
						ExitTimeStr:     tIST.Format("15:04:05"),
						Symbol:          active.Symbol,
						OptionType:      active.Symbol[len(active.Symbol)-2:],
						Side:            active.Side,
						Quantity:        active.Quantity,
						Multiplier:      active.Quantity / cfg.Options.BaseLotSize,
						EntryPrice:      active.EntryPremium,
						ExitPrice:       exitPrice,
						PnL:             pnl,
						TimeHeldMinutes: heldMins,
						ExitReason:      "EOD SQUARE-OFF",
						EntryMarker:     fmt.Sprintf("SELL_%s", active.Symbol[len(active.Symbol)-2:]),
						ExitMarker:      "EXIT_EOD",
						EntryCandleUnix: (active.CreatedAt.Unix() / 300) * 300,
						ExitCandleUnix:  (tIST.Unix() / 300) * 300,
					})
				}
				continue
			}

			// Evaluate Strategy Signal
			action, qty := posMgr.EvaluateSignal(stRes.Trend)
			isPastLastTradeTime := tIST.Hour() > 14 || (tIST.Hour() == 14 && tIST.Minute() >= 30)

			canOpenInitial := !isPastLastTradeTime && action == "OPEN_INITIAL"
			canReversal := action == "REVERSAL"

			if canOpenInitial || canReversal {
				// Reversal exit if active
				if action == "REVERSAL" {
					if active := posMgr.GetActivePosition(); active != nil {
						exitPrice := estimateOptionPremium(candle.Close, 24000, active.Symbol[len(active.Symbol)-2:], tIST)
						pnl := posMgr.OnTradeClosed(exitPrice, "REVERSAL EXIT")

						tradeCounter++
						heldMins := int(tIST.Sub(active.CreatedAt).Minutes())
						if heldMins < 1 {
							heldMins = 1
						}

						if pnl >= 0 {
							wins++
						} else {
							losses++
						}
						totalPnL += pnl

						simulatedTrades = append(simulatedTrades, TradeLog{
							ID:              tradeCounter,
							Day:             dStr,
							EntryTimeStr:    active.CreatedAt.Format("15:04:05"),
							ExitTimeStr:     tIST.Format("15:04:05"),
							Symbol:          active.Symbol,
							OptionType:      active.Symbol[len(active.Symbol)-2:],
							Side:            active.Side,
							Quantity:        active.Quantity,
							Multiplier:      active.Quantity / cfg.Options.BaseLotSize,
							EntryPrice:      active.EntryPremium,
							ExitPrice:       exitPrice,
							PnL:             pnl,
							TimeHeldMinutes: heldMins,
							ExitReason:      "TREND REVERSAL EXIT",
							EntryMarker:     fmt.Sprintf("SELL_%s", active.Symbol[len(active.Symbol)-2:]),
							ExitMarker:      "EXIT_REVERSAL",
							EntryCandleUnix: (active.CreatedAt.Unix() / 300) * 300,
							ExitCandleUnix:  (tIST.Unix() / 300) * 300,
						})
					}
				}

				// Check if new trade entry is allowed before 14:30 / 15:00 cutoff
				if !isPastLastTradeTime {
					strikeRes, err := strikeSelector.SelectOTMStrike("NIFTY 50", candle.Close, stRes.Trend)
					if err == nil {
						entryPrem := estimateOptionPremium(candle.Close, strikeRes.TargetStrike, strikeRes.OptionType, tIST)
						orderID := fmt.Sprintf("SIM-%d", time.Now().UnixNano())
						posMgr.OnTradeOpened(orderID, strikeRes.OptionSymbol, strikeRes.OptionType, qty, entryPrem, tIST.Format("2006-01-02"), tIST)
					}
				}
			}
		}
	}

	// 5. Build Final Markdown Report
	winRate := 0.0
	if tradeCounter > 0 {
		winRate = (float64(wins) / float64(tradeCounter)) * 100.0
	}

	reportContent := buildReportMarkdown(dateList, simulatedTrades, totalPnL, wins, losses, winRate)

	// Save Report Artifact to Conversation Folder
	artDir := filepath.Join(os.Getenv("USERPROFILE"), ".gemini", "antigravity-cli", "brain", "500a04d7-8311-49a6-adb9-1607af1cf7f3")
	_ = os.MkdirAll(artDir, 0755)
	repPath := filepath.Join(artDir, "simulation_5days_report.md")
	if err := os.WriteFile(repPath, []byte(reportContent), 0644); err != nil {
		log.Printf("Warning: Failed to save report to artifact folder: %v", err)
	} else {
		fmt.Printf("✓ Saved detailed simulation report to: %s\n", repPath)
	}

	// Print Terminal Summary
	fmt.Println("\n==========================================================================")
	fmt.Printf(" SIMULATION SUMMARY (LAST 5 TRADING DAYS)\n")
	fmt.Println("==========================================================================")
	fmt.Printf(" Total Executed Trades : %d\n", tradeCounter)
	fmt.Printf(" Winning Trades        : %d\n", wins)
	fmt.Printf(" Losing Trades         : %d\n", losses)
	fmt.Printf(" Win Rate              : %.2f%%\n", winRate)
	fmt.Printf(" Total Realized PnL    : ₹%.2f\n", totalPnL)
	fmt.Println("==========================================================================")
}

func buildReportMarkdown(dateList []string, trades []TradeLog, totalPnL float64, wins, losses int, winRate float64) string {
	var sb strings.Builder

	sb.WriteString("# 📊 5-Day Options Strategy Simulation & UI Chart Arrow Report\n\n")
	sb.WriteString(fmt.Sprintf("**Simulation Date Window**: %s to %s  \n", dateList[0], dateList[len(dateList)-1]))
	sb.WriteString(fmt.Sprintf("**Generated At**: %s IST  \n\n", time.Now().In(data.ISTLocation).Format("2006-01-02 15:04:05")))

	sb.WriteString("## 📈 Performance Summary Matrix\n\n")
	sb.WriteString("| Metric | Value |\n")
	sb.WriteString("| :--- | :--- |\n")
	sb.WriteString(fmt.Sprintf("| **Total Days Simulated** | %d |\n", len(dateList)))
	sb.WriteString(fmt.Sprintf("| **Total Trades Executed** | %d |\n", len(trades)))
	sb.WriteString(fmt.Sprintf("| **Winning Trades** | %d |\n", wins))
	sb.WriteString(fmt.Sprintf("| **Losing Trades** | %d |\n", losses))
	sb.WriteString(fmt.Sprintf("| **Win Rate (%%)** | **%.2f%%%%** |\n", winRate))
	sb.WriteString(fmt.Sprintf("| **Total Net Realized PnL** | **₹%.2f** |\n\n", totalPnL))

	sb.WriteString("--- \n\n")
	sb.WriteString("## 📜 Executed Trades & UI Chart Arrow Map\n\n")
	sb.WriteString("| # | Date | Entry Time | Exit Time | Contract Symbol | Side | Qty | Multiplier | Entry ₹ | Exit ₹ | Net PnL (₹) | Exit Reason | UI Chart Arrow Marker |\n")
	sb.WriteString("| :-: | :-: | :-: | :-: | :--- | :-: | :-: | :-: | :-: | :-: | :-: | :--- | :--- |\n")

	for _, tr := range trades {
		pnlStr := fmt.Sprintf("₹%.2f", tr.PnL)
		if tr.PnL > 0 {
			pnlStr = fmt.Sprintf("+₹%.2f", tr.PnL)
		}

		arrowText := ""
		if tr.OptionType == "CE" {
			arrowText = fmt.Sprintf("🔴 **SELL CE** (%s) ➔ 🟣 **%s** (%s)", tr.EntryTimeStr, tr.ExitMarker, tr.ExitTimeStr)
		} else {
			arrowText = fmt.Sprintf("🟢 **SELL PE** (%s) ➔ 🟣 **%s** (%s)", tr.EntryTimeStr, tr.ExitMarker, tr.ExitTimeStr)
		}

		sb.WriteString(fmt.Sprintf("| %d | %s | %s | %s | **%s** | %s | %d | %dx | ₹%.2f | ₹%.2f | **%s** | %s | %s |\n",
			tr.ID, tr.Day, tr.EntryTimeStr, tr.ExitTimeStr, tr.Symbol, tr.Side, tr.Quantity, tr.Multiplier,
			tr.EntryPrice, tr.ExitPrice, pnlStr, tr.ExitReason, arrowText,
		))
	}

	sb.WriteString("\n\n---\n")
	sb.WriteString("### 📌 UI Chart Arrow Mapping Rules\n")
	sb.WriteString("1. **`ENTRY_SELL_CE` / `SELL CE`**: Rendered as a **Red Down Arrow (`#f23645`)** placed `aboveBar` at the entry 5m candle timestamp.\n")
	sb.WriteString("2. **`ENTRY_SELL_PE` / `SELL PE`**: Rendered as a **Green Up Arrow (`#089981`)** placed `belowBar` at the entry 5m candle timestamp.\n")
	sb.WriteString("3. **`EXIT_EOD`**: Rendered as a **Purple Arrow (`#a855f7`)** labelled `EXIT: EOD SQUARE-OFF` at the 15:10 IST candle.\n")
	sb.WriteString("4. **`EXIT_SL`**: Rendered as a **Yellow Arrow (`#eab308`)** labelled `EXIT: SL HIT` at the exit 5m candle.\n")
	sb.WriteString("5. **`EXIT_REVERSAL`**: Rendered as an **Orange Arrow (`#f59e0b`)** labelled `EXIT: REVERSAL` at the trend flip candle.\n")

	return sb.String()
}
