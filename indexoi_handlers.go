package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"zerodha-trading/data"
	"zerodha-trading/scanner"
)

func writeIndexOIJSON(w http.ResponseWriter, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func resolveIndexOISymbol(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	switch s {
	case "", "NIFTY", "NIFTY 50", "ANIFTY":
		return "NIFTY 50"
	case "BANKNIFTY", "NIFTY BANK", "ABANKNIFTY":
		return "NIFTY BANK"
	case "SENSEX", "BSE SENSEX":
		return "BSE SENSEX"
	case "FINNIFTY", "NIFTY FIN SERVICE":
		return "FINNIFTY"
	case "MIDCPNIFTY", "NIFTY MID SELECT":
		return "MIDCPNIFTY"
	default:
		return s
	}
}

func (tb *TradingBot) loadIndexOIHistory(ctx context.Context, sym, dateStr string) ([]data.FOStrikeOISnapshot, []string) {
	var availDates []string
	if tb.db != nil {
		availDates, _ = tb.db.GetAvailableFODates(ctx, sym)
	}
	var hist []data.FOStrikeOISnapshot
	if tb.db != nil {
		hist, _ = tb.db.GetFOStrikeOIHistoryByDate(ctx, sym, dateStr)
	}
	todayStr := data.NowIST().Format("2006-01-02")
	if len(hist) == 0 && (dateStr == "" || dateStr == todayStr) {
		if _, err := tb.RefreshFOSnapshotForSymbol(ctx, sym, false); err == nil && tb.db != nil {
			hist, _ = tb.db.GetFOStrikeOIHistoryByDate(ctx, sym, dateStr)
			availDates, _ = tb.db.GetAvailableFODates(ctx, sym)
		}
	}
	return hist, availDates
}

// handleIndexOIThreeStrike serves /api/indexoi/three-strike for 3-Strike OI, Ladder, and Intraday Diff.
func (tb *TradingBot) handleIndexOIThreeStrike(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sym := resolveIndexOISymbol(q.Get("symbol"))
	dateStr := strings.TrimSpace(q.Get("date"))
	replayTime := strings.TrimSpace(q.Get("time"))
	baseStrike, _ := strconv.ParseFloat(q.Get("base"), 64)
	intervalMins, _ := strconv.Atoi(q.Get("interval"))
	if intervalMins < 1 || intervalMins > 6 {
		intervalMins = 3
	}

	hist, availDates := tb.loadIndexOIHistory(r.Context(), sym, dateStr)
	if dateStr == "" && len(availDates) > 0 {
		dateStr = availDates[0]
	}
	view := scanner.BuildThreeStrikeView(sym, dateStr, replayTime, baseStrike, intervalMins, hist, availDates)
	writeIndexOIJSON(w, view)
}

// handleIndexOIChain serves /api/indexoi/chain for A NIFTY / A BANKNIFTY Option Chain.
func (tb *TradingBot) handleIndexOIChain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sym := resolveIndexOISymbol(q.Get("symbol"))
	dateStr := strings.TrimSpace(q.Get("date"))
	replayTime := strings.TrimSpace(q.Get("time"))
	sortDesc := strings.ToLower(q.Get("sort")) == "desc"

	hist, availDates := tb.loadIndexOIHistory(r.Context(), sym, dateStr)
	if dateStr == "" && len(availDates) > 0 {
		dateStr = availDates[0]
	}
	view := scanner.BuildOptionChainAView(sym, dateStr, replayTime, false, sortDesc, hist, availDates)
	writeIndexOIJSON(w, view)
}

// handleIndexOIPulse serves /api/indexoi/pulse for Nifty/BankNifty 15m OI Pulse Checkpoints.
func (tb *TradingBot) handleIndexOIPulse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sym := resolveIndexOISymbol(q.Get("symbol"))
	dateStr := strings.TrimSpace(q.Get("date"))

	hist, availDates := tb.loadIndexOIHistory(r.Context(), sym, dateStr)
	if dateStr == "" && len(availDates) > 0 {
		dateStr = availDates[0]
	}
	view := scanner.BuildOIPulseView(sym, dateStr, hist, availDates)
	if len(view.Checkpoints) == 0 && tb.db != nil {
		if cps, err := tb.db.GetOIPulseByDate(r.Context(), sym, dateStr); err == nil && len(cps) > 0 {
			view.Checkpoints = cps
		}
	}
	writeIndexOIJSON(w, view)
}

// handleIndexOIStructure serves /api/indexoi/structure for N Structure (50) & B Structure (14).
func (tb *TradingBot) handleIndexOIStructure(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sym := resolveIndexOISymbol(q.Get("symbol"))
	metric := strings.TrimSpace(q.Get("metric"))
	dateStr := strings.TrimSpace(q.Get("date"))
	if strings.EqualFold(q.Get("refresh"), "true") {
		_, _ = tb.RefreshIndexStructure(r.Context(), sym)
	}
	view, err := tb.BuildIndexStructureView(r.Context(), sym, metric, dateStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeIndexOIJSON(w, view)
}

// handleIndexOIStocksList serves /api/indexoi/stocks/list returning all tracked F&O stocks.
func (tb *TradingBot) handleIndexOIStocksList(w http.ResponseWriter, r *http.Request) {
	var stocks []string
	if tb.securityMaster != nil {
		if foMap, err := tb.securityMaster.GetFOStocks(r.Context()); err == nil {
			stocks = make([]string, 0, len(foMap))
			for sym := range foMap {
				stocks = append(stocks, sym)
			}
		}
	}
	if len(stocks) == 0 {
		stocks = []string{"RELIANCE", "HDFCBANK", "ICICIBANK", "INFY", "TCS", "SBIN", "AXISBANK", "AUBANK"}
	}
	sort.Strings(stocks)
	writeIndexOIJSON(w, map[string]interface{}{
		"stocks": stocks,
		"count":  len(stocks),
	})
}

// handleIndexOIStocksChain serves /api/indexoi/stocks/chain for single-stock Option Chain.
func (tb *TradingBot) handleIndexOIStocksChain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sym := strings.ToUpper(strings.TrimSpace(q.Get("symbol")))
	if sym == "" {
		sym = "RELIANCE"
	}
	sortDesc := strings.ToLower(q.Get("sort")) == "desc"
	view, err := tb.FetchStockOptionChain(r.Context(), sym, sortDesc)
	if err != nil {
		writeIndexOIJSON(w, map[string]interface{}{
			"symbol": sym,
			"error":  err.Error(),
			"rows":   []interface{}{},
		})
		return
	}
	writeIndexOIJSON(w, view)
}

// handleIndexOIStraddle serves /api/indexoi/straddle for Straddle, VWAP, Max Pain, and FII/DII OI.
func (tb *TradingBot) handleIndexOIStraddle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sym := resolveIndexOISymbol(q.Get("symbol"))
	dateStr := strings.TrimSpace(q.Get("date"))
	strikeVal, _ := strconv.ParseFloat(q.Get("strike"), 64)

	hist, availDates := tb.loadIndexOIHistory(r.Context(), sym, dateStr)
	if dateStr == "" && len(availDates) > 0 {
		dateStr = availDates[0]
	}

	var fiiDII []data.FIIDIIParticipantRecord
	if tb.db != nil {
		fiiDII, _ = tb.db.GetLatestFIIDIIParticipants(r.Context(), dateStr)
	}
	if len(fiiDII) == 0 {
		// Trigger non-blocking background fetch so the Straddle UI never stalls waiting on NSE archives
		go func() {
			bgCtx, cancel := context.WithTimeout(tb.ctx, 10*time.Second)
			defer cancel()
			_, _ = tb.FetchNSEFIIDIIParticipants(bgCtx, data.NowIST())
		}()
	}

	view := scanner.BuildStraddleView(sym, dateStr, strikeVal, hist, availDates, fiiDII)
	writeIndexOIJSON(w, view)
}

// handleGannLevels serves /api/gann/levels?symbol=NIFTY 50&ref=prev_close|open&price=<optional>
// computing W.D. Gann Square of 9 intraday breakout, support, resistance, and target levels.
func (tb *TradingBot) handleGannLevels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rawSym := strings.TrimSpace(q.Get("symbol"))
	if rawSym == "" {
		rawSym = "NIFTY 50"
	}
	refType := strings.ToLower(strings.TrimSpace(q.Get("ref")))
	if refType == "" {
		refType = "prev_close"
	}
	customPrice, _ := strconv.ParseFloat(q.Get("price"), 64)
	customLTP, _ := strconv.ParseFloat(q.Get("ltp"), 64)
	lineCount, _ := strconv.Atoi(q.Get("count"))
	if lineCount <= 0 {
		lineCount = 5
	}

	spotKey, _, _ := resolveSpotKeyAndOptionExchange(rawSym)
	var refPrice, ltp float64

	if tb.kiteClient != nil {
		if qMap, err := tb.kiteClient.GetQuote(spotKey); err == nil {
			if qt, ok := qMap[spotKey]; ok {
				ltp = qt.LastPrice
				if refType == "open" && qt.OHLC.Open > 0 {
					refPrice = qt.OHLC.Open
				} else if qt.OHLC.Close > 0 {
					refPrice = qt.OHLC.Close
				}
			}
		}
	}

	if customPrice > 0 {
		refPrice = customPrice
		if q.Get("ref") == "" {
			refType = "custom"
		}
	}
	if customLTP > 0 {
		ltp = customLTP
	}
	if refPrice <= 0 && tb.db != nil && tb.securityMaster != nil {
		if tok, err := tb.securityMaster.GetInstrumentToken(strings.ToUpper(rawSym)); err == nil && tok > 0 {
			if c1d, err := tb.db.GetLastNCandles("candles_1d", tok, 2); err == nil && len(c1d) > 0 {
				last := c1d[len(c1d)-1]
				if ltp <= 0 {
					ltp = last.Close
				}
				if refType == "open" && last.Open > 0 {
					refPrice = last.Open
				} else if len(c1d) >= 2 {
					refPrice = c1d[len(c1d)-2].Close
				} else {
					refPrice = last.Close
				}
			}
		}
	}

	writeIndexOIJSON(w, scanner.CalculateGannLevelsWithCount(rawSym, refType, refPrice, ltp, lineCount))
}
