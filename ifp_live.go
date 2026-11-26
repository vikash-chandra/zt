package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"zerodha-trading/data"
	"zerodha-trading/scanner"
)

var ifpDefaultIndices = []string{"NIFTY 50", "NIFTY BANK", "BSE SENSEX", "FINNIFTY", "MIDCPNIFTY"}

func resolveSpotKeyAndOptionExchange(symbol string) (spotKey, underlying, optExchange string) {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	switch sym {
	case "NIFTY 50", "NIFTY":
		return "NSE:NIFTY 50", "NIFTY", "NFO"
	case "NIFTY BANK", "BANKNIFTY":
		return "NSE:NIFTY BANK", "BANKNIFTY", "NFO"
	case "BSE SENSEX", "SENSEX":
		return "BSE:SENSEX", "SENSEX", "BFO"
	case "FINNIFTY", "NIFTY FIN SERVICE":
		return "NSE:NIFTY FIN SERVICE", "FINNIFTY", "NFO"
	case "MIDCPNIFTY", "NIFTY MID SELECT":
		return "NSE:NIFTY MID SELECT", "MIDCPNIFTY", "NFO"
	default:
		return "NSE:" + sym, sym, "NFO"
	}
}

func (tb *TradingBot) batchedGetQuote(keys []string, chunkSize int) (map[string]data.Quote, error) {
	combined := make(map[string]data.Quote, len(keys))
	if tb.kiteClient == nil || len(keys) == 0 {
		return combined, fmt.Errorf("kiteClient unavailable or empty keys")
	}
	if chunkSize <= 0 {
		chunkSize = 200
	}
	var lastErr error
	for i := 0; i < len(keys); i += chunkSize {
		end := i + chunkSize
		if end > len(keys) {
			end = len(keys)
		}
		batchQuotes, err := tb.kiteClient.GetQuote(keys[i:end]...)
		if err != nil {
			lastErr = err
			continue
		}
		for k, q := range batchQuotes {
			combined[k] = q
		}
	}
	if len(combined) == 0 && lastErr != nil {
		return combined, lastErr
	}
	return combined, nil
}

// RefreshFOSnapshotForSymbol fetches live spot, nearest futures trend, and +-10 ATM strike OI quotes
// from Zerodha NFO/BFO for the given underlying symbol, updates FOAnalyzer, and persists snapshots to DB.
func (tb *TradingBot) RefreshFOSnapshotForSymbol(ctx context.Context, symbol string, force bool) (data.FOSummaryScore, error) {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if sym == "" {
		sym = "NIFTY 50"
	}

	if !force && tb.foAnalyzer != nil {
		tb.ifpMu.RLock()
		lastFetch := time.Time{}
		if tb.foLastFetch != nil {
			lastFetch = tb.foLastFetch[sym]
		}
		tb.ifpMu.RUnlock()
		if !lastFetch.IsZero() && time.Since(lastFetch) < 60*time.Second {
			if cached, ok := tb.foAnalyzer.GetSummary(sym); ok && len(cached.Strikes) > 0 {
				return cached, nil
			}
		}
	}

	if tb.kiteClient == nil || tb.securityMaster == nil || tb.foAnalyzer == nil {
		return data.FOSummaryScore{}, fmt.Errorf("broker client, security master, or foAnalyzer not initialized")
	}

	spotKey, underlying, optExchange := resolveSpotKeyAndOptionExchange(sym)
	nowIST := data.NowIST()

	spotQuotes, err := tb.kiteClient.GetQuote(spotKey)
	if err != nil {
		return data.FOSummaryScore{}, fmt.Errorf("failed to fetch spot quote for %s: %w", spotKey, err)
	}
	spotQ, hasSpot := spotQuotes[spotKey]
	spotPrice := spotQ.LastPrice
	if !hasSpot || spotPrice <= 0 {
		return data.FOSummaryScore{}, fmt.Errorf("empty spot price returned by Zerodha for %s", spotKey)
	}

	// Optional futures trend from nearest active NFO/BFO FUT contract
	futuresTrend := "NEUTRAL"
	futuresPrice := 0.0
	if futContracts, futErr := tb.securityMaster.GetNearestFuturesContracts(ctx, optExchange); futErr == nil {
		if futInst, ok := futContracts[underlying]; ok {
			futKey := optExchange + ":" + futInst.TradingSymbol
			if futQuotes, qErr := tb.kiteClient.GetQuote(futKey); qErr == nil {
				if fq, ok := futQuotes[futKey]; ok && fq.LastPrice > 0 {
					futuresPrice = fq.LastPrice
					if fq.OHLC.Close > 0 {
						chgPct := (fq.LastPrice - fq.OHLC.Close) / fq.OHLC.Close * 100
						if chgPct >= 0.25 {
							futuresTrend = "BULLISH"
						} else if chgPct <= -0.25 {
							futuresTrend = "BEARISH"
						}
					}
				}
			}
		}
	}

	insts, err := tb.securityMaster.GetOptionInstruments(ctx, optExchange)
	if err != nil {
		return data.FOSummaryScore{}, fmt.Errorf("failed to load %s option instruments for %s: %w", optExchange, sym, err)
	}

	todayMidnight := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 0, 0, 0, 0, data.ISTLocation)
	var nearestExpiry time.Time
	var matching []data.Instrument
	for _, inst := range insts {
		if strings.ToUpper(inst.Name) != underlying {
			continue
		}
		if inst.InstrumentType != "CE" && inst.InstrumentType != "PE" {
			continue
		}
		if inst.Expiry.IsZero() {
			continue
		}
		expIST := data.NormalizeToIST(inst.Expiry)
		expMid := time.Date(expIST.Year(), expIST.Month(), expIST.Day(), 0, 0, 0, 0, data.ISTLocation)
		if expMid.Before(todayMidnight) {
			continue
		}
		if nearestExpiry.IsZero() || expMid.Before(nearestExpiry) {
			nearestExpiry = expMid
		}
		matching = append(matching, inst)
	}

	if nearestExpiry.IsZero() || len(matching) == 0 {
		return data.FOSummaryScore{}, fmt.Errorf("no active %s expiry contracts found for %s", optExchange, underlying)
	}

	type strikePair struct {
		ce *data.Instrument
		pe *data.Instrument
	}
	strikeMap := make(map[float64]*strikePair)
	for i := range matching {
		inst := &matching[i]
		expIST := data.NormalizeToIST(inst.Expiry)
		expMid := time.Date(expIST.Year(), expIST.Month(), expIST.Day(), 0, 0, 0, 0, data.ISTLocation)
		if !expMid.Equal(nearestExpiry) {
			continue
		}
		pair, ok := strikeMap[inst.Strike]
		if !ok {
			pair = &strikePair{}
			strikeMap[inst.Strike] = pair
		}
		if inst.InstrumentType == "CE" {
			pair.ce = inst
		} else {
			pair.pe = inst
		}
	}

	var strikes []float64
	for st, pair := range strikeMap {
		if pair.ce != nil && pair.pe != nil {
			strikes = append(strikes, st)
		}
	}
	if len(strikes) == 0 {
		return data.FOSummaryScore{}, fmt.Errorf("no matched CE/PE strikes for %s expiry %s", underlying, nearestExpiry.Format("2006-01-02"))
	}
	sort.Float64s(strikes)

	// Locate ATM index closest to spotPrice and slice +-16 strikes for NIFTY/BANKNIFTY (+-10 otherwise)
	window := 10
	if sym == "NIFTY 50" || sym == "NIFTY BANK" {
		window = 16
	}
	atmIdx := 0
	minDiff := math.Abs(strikes[0] - spotPrice)
	for i, st := range strikes {
		diff := math.Abs(st - spotPrice)
		if diff < minDiff {
			minDiff = diff
			atmIdx = i
		}
	}
	startIdx := atmIdx - window
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := atmIdx + window
	if endIdx >= len(strikes) {
		endIdx = len(strikes) - 1
	}

	selectedStrikes := strikes[startIdx : endIdx+1]
	atmStrike := strikes[atmIdx]

	quoteKeys := make([]string, 0, len(selectedStrikes)*2)
	for _, st := range selectedStrikes {
		pair := strikeMap[st]
		quoteKeys = append(quoteKeys, optExchange+":"+pair.ce.TradingSymbol, optExchange+":"+pair.pe.TradingSymbol)
	}

	optQuotes, err := tb.batchedGetQuote(quoteKeys, 200)
	if err != nil {
		return data.FOSummaryScore{}, fmt.Errorf("failed to fetch option chain quotes for %s: %w", sym, err)
	}

	todayStr := nowIST.Format("2006-01-02")
	snapTime := nowIST.Truncate(time.Minute)
	expiryStr := nearestExpiry.Format("2006-01-02")

	// Load prior-day closing OI baseline per strike so CallOIChange/PutOIChange reflect cumulative intraday change from open
	priorCloseOI := make(map[float64][2]int64)
	if tb.db != nil {
		if prevMap, pErr := tb.db.GetPreviousDayClosingStrikeOI(ctx, sym, expiryStr, todayStr); pErr == nil {
			priorCloseOI = prevMap
		}
	}

	strikeRows := make([]data.FOStrikeRow, 0, len(selectedStrikes))
	dbSnapshots := make([]data.FOStrikeOISnapshot, 0, len(selectedStrikes))

	for i, st := range selectedStrikes {
		pair := strikeMap[st]
		ceQ := optQuotes[optExchange+":"+pair.ce.TradingSymbol]
		peQ := optQuotes[optExchange+":"+pair.pe.TradingSymbol]

		cOI := int64(ceQ.OI)
		pOI := int64(peQ.OI)
		var cOIChg, pOIChg int64
		if prevPair, ok := priorCloseOI[st]; ok && prevPair[0] > 0 {
			cOIChg = cOI - prevPair[0]
		} else if ceQ.OIDayLow > 0 && cOI >= ceQ.OIDayLow {
			cOIChg = cOI - ceQ.OIDayLow
		}
		if prevPair, ok := priorCloseOI[st]; ok && prevPair[1] > 0 {
			pOIChg = pOI - prevPair[1]
		} else if peQ.OIDayLow > 0 && pOI >= peQ.OIDayLow {
			pOIChg = pOI - peQ.OIDayLow
		}

		cLTPChg := 0.0
		if ceQ.OHLC.Close > 0 && ceQ.LastPrice > 0 {
			cLTPChg = math.Round((ceQ.LastPrice-ceQ.OHLC.Close)*100) / 100
		}
		pLTPChg := 0.0
		if peQ.OHLC.Close > 0 && peQ.LastPrice > 0 {
			pLTPChg = math.Round((peQ.LastPrice-peQ.OHLC.Close)*100) / 100
		}
		lotSz := pair.ce.LotSize
		if lotSz <= 0 {
			lotSz = scanner.DefaultLotSize(sym)
		}

		offset := (startIdx + i) - atmIdx
		row := scanner.CalculateStrikeMetrics(st, offset, cOI, cOIChg, pOI, pOIChg, ceQ.LastPrice, peQ.LastPrice)
		strikeRows = append(strikeRows, row)

		dbSnapshots = append(dbSnapshots, data.FOStrikeOISnapshot{
			Symbol:       sym,
			ExpiryDate:   nearestExpiry,
			StrikePrice:  st,
			CallOI:       cOI,
			CallOIChange: cOIChg,
			PutOI:        pOI,
			PutOIChange:  pOIChg,
			CallLTP:      ceQ.LastPrice,
			PutLTP:       peQ.LastPrice,
			CallVolume:   ceQ.VolumeTraded,
			PutVolume:    peQ.VolumeTraded,
			CallLTPChg:   cLTPChg,
			PutLTPChg:    pLTPChg,
			SpotPrice:    spotPrice,
			FuturesPrice: futuresPrice,
			LotSize:      lotSz,
			ATMOffset:    offset,
			SnapshotTime: snapTime,
		})
	}

	summary := tb.foAnalyzer.BuildFOSummary(sym, spotPrice, atmStrike, expiryStr, futuresTrend, strikeRows, nowIST)
	summary.SnapshotDate = nowIST.Format("2006-01-02")
	summary.LastZerodhaSync = nowIST.Format("2006-01-02 15:04:05 IST")

	if tb.db != nil && len(dbSnapshots) > 0 {
		_ = tb.db.SaveFOStrikeOISnapshots(ctx, dbSnapshots)
	}

	tb.ifpMu.Lock()
	if tb.foLastFetch == nil {
		tb.foLastFetch = make(map[string]time.Time)
	}
	tb.foLastFetch[sym] = time.Now()
	tb.ifpMu.Unlock()

	return summary, nil
}

// RefreshAllIFPFOIndices refreshes live F&O strike OI matrices for all tracked major indices.
func (tb *TradingBot) RefreshAllIFPFOIndices(ctx context.Context, force bool) []data.FOSummaryScore {
	summaries := make([]data.FOSummaryScore, 0, len(ifpDefaultIndices))
	for _, idxSym := range ifpDefaultIndices {
		if ctx.Err() != nil {
			break
		}
		summary, err := tb.RefreshFOSnapshotForSymbol(ctx, idxSym, force)
		if err == nil && len(summary.Strikes) > 0 {
			summaries = append(summaries, summary)
		}
	}
	return summaries
}

// FetchLiveFuturesBuildups fetches live NFO nearest-month futures & spot quotes across F&O underlyings,
// classifies Long Buildup / Short Buildup / Short Covering / Long Unwinding, and updates FOAnalyzer cache.
func (tb *TradingBot) FetchLiveFuturesBuildups(ctx context.Context, force bool) ([]data.FuturesBuildupRecord, time.Time, error) {
	if !force && tb.foAnalyzer != nil {
		tb.ifpMu.RLock()
		lastFetch := tb.futLastFetch
		tb.ifpMu.RUnlock()
		if !lastFetch.IsZero() && time.Since(lastFetch) < 60*time.Second {
			cached := tb.foAnalyzer.GetFuturesBuildups()
			if len(cached) > 0 {
				return cached, lastFetch, nil
			}
		}
	}

	if tb.kiteClient == nil || tb.securityMaster == nil || tb.foAnalyzer == nil {
		return nil, time.Time{}, fmt.Errorf("broker client, security master, or foAnalyzer not initialized")
	}

	futContracts, err := tb.securityMaster.GetNearestFuturesContracts(ctx, "NFO")
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to resolve nearest NFO futures contracts: %w", err)
	}
	if len(futContracts) == 0 {
		return nil, time.Time{}, fmt.Errorf("no active NFO futures contracts found")
	}

	var foStocks map[string]int64
	if tb.db != nil {
		foStocks, _ = tb.securityMaster.GetFOStocks(ctx)
	}
	nowIST := data.NowIST()
	todayStr := nowIST.Format("2006-01-02")

	// Load real previous trading day closing futures OI from futures_buildup_snapshots on date rollout
	tb.ifpMu.Lock()
	if tb.futBaselineDate != todayStr || tb.futBaselineOI == nil {
		tb.futBaselineOI = make(map[string]int64)
		tb.futBaselineDate = todayStr
		if tb.db != nil {
			if prevOI, dbErr := tb.db.GetPreviousTradingDayFuturesOI(ctx, todayStr); dbErr == nil && len(prevOI) > 0 {
				tb.futBaselineOI = prevOI
			}
		}
	}
	tb.ifpMu.Unlock()

	bfoFutContracts, _ := tb.securityMaster.GetNearestFuturesContracts(ctx, "BFO")

	type pairTarget struct {
		symbol   string
		contract string
		segment  string
		futKey   string
		spotKey  string
	}
	targets := make([]pairTarget, 0, len(futContracts)+len(bfoFutContracts))
	quoteKeys := make([]string, 0, (len(futContracts)+len(bfoFutContracts))*2)

	for sym := range foStocks {
		uSym := strings.ToUpper(strings.TrimSpace(sym))
		if futInst, ok := futContracts[uSym]; ok {
			fKey := "NFO:" + futInst.TradingSymbol
			sKey := "NSE:" + uSym
			targets = append(targets, pairTarget{
				symbol: uSym, contract: futInst.TradingSymbol, segment: "STOCK", futKey: fKey, spotKey: sKey,
			})
			quoteKeys = append(quoteKeys, fKey, sKey)
		}
	}
	// Include major index futures (NFO + BFO SENSEX)
	for _, idx := range []struct{ disp, exch, under, spot string }{
		{"NIFTY 50", "NFO", "NIFTY", "NSE:NIFTY 50"},
		{"NIFTY BANK", "NFO", "BANKNIFTY", "NSE:NIFTY BANK"},
		{"FINNIFTY", "NFO", "FINNIFTY", "NSE:NIFTY FIN SERVICE"},
		{"MIDCPNIFTY", "NFO", "MIDCPNIFTY", "NSE:NIFTY MID SELECT"},
		{"BSE SENSEX", "BFO", "SENSEX", "BSE:SENSEX"},
	} {
		srcMap := futContracts
		if idx.exch == "BFO" {
			srcMap = bfoFutContracts
		}
		if futInst, ok := srcMap[idx.under]; ok {
			fKey := idx.exch + ":" + futInst.TradingSymbol
			targets = append(targets, pairTarget{
				symbol: idx.disp, contract: futInst.TradingSymbol, segment: "INDEX", futKey: fKey, spotKey: idx.spot,
			})
			quoteKeys = append(quoteKeys, fKey, idx.spot)
		}
	}

	quotes, err := tb.batchedGetQuote(quoteKeys, 200)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to fetch NFO futures quotes: %w", err)
	}

	records := make([]data.FuturesBuildupRecord, 0, len(targets))
	tb.ifpMu.Lock()
	for _, t := range targets {
		fq, hasFut := quotes[t.futKey]
		if !hasFut || fq.LastPrice <= 0 {
			continue
		}
		sq, hasSpot := quotes[t.spotKey]
		spotPrice := fq.LastPrice
		if hasSpot && sq.LastPrice > 0 {
			spotPrice = sq.LastPrice
		}
		prevClose := fq.OHLC.Close
		if prevClose <= 0 && hasSpot && sq.OHLC.Close > 0 {
			prevClose = sq.OHLC.Close
		}
		if prevClose <= 0 {
			prevClose = fq.OHLC.Open
		}

		curOI := int64(fq.OI)
		prevOI := tb.futBaselineOI[t.symbol]
		if prevOI <= 0 {
			if fq.OIDayLow > 0 {
				prevOI = int64(fq.OIDayLow)
			} else {
				prevOI = curOI
			}
			if prevOI > 0 {
				tb.futBaselineOI[t.symbol] = prevOI
			}
		}

		bType, basisPct, oiChgPct, priceChgPct := scanner.ClassifyFuturesBuildup(
			spotPrice, fq.LastPrice, prevClose, curOI, prevOI,
		)

		records = append(records, data.FuturesBuildupRecord{
			Symbol:         t.symbol,
			ContractSymbol: t.contract,
			Segment:        t.segment,
			SpotPrice:      math.Round(spotPrice*100) / 100,
			FuturesPrice:   math.Round(fq.LastPrice*100) / 100,
			BasisPct:       basisPct,
			TotalOI:        curOI,
			OIChangePct:    oiChgPct,
			PriceChangePct: priceChgPct,
			BuildupType:    bType,
			ScanDate:       todayStr,
			UpdatedAt:      nowIST,
		})
	}
	tb.futLastFetch = nowIST
	tb.ifpMu.Unlock()

	sort.Slice(records, func(i, j int) bool {
		return math.Abs(records[i].OIChangePct) > math.Abs(records[j].OIChangePct)
	})

	if tb.db != nil && len(records) > 0 {
		_ = tb.db.SaveFuturesBuildupSnapshots(ctx, todayStr, records)
	}

	tb.foAnalyzer.SetFuturesBuildups(records)
	return records, nowIST, nil
}

// FetchLiveVolumeSurges computes volume surges across all F&O stocks using live WebSocket ticks
// (cumulative day VolumeTraded) and batched Zerodha GetQuote snapshots against updated 20D/10D baselines.
func (tb *TradingBot) FetchLiveVolumeSurges(ctx context.Context, lookback int, minMultiplier float64, quadrantFilter string) ([]data.VolumeSurgeRecord, time.Time, string, error) {
	if tb.volumeSurgeScanner == nil {
		return nil, time.Time{}, "", fmt.Errorf("volumeSurgeScanner not initialized")
	}

	nowIST := data.NowIST()
	todayStr := nowIST.Format("2006-01-02")

	// Reload baselines if not yet loaded today
	tb.ifpMu.RLock()
	needBaselineReload := tb.lastVolBaselineDate != todayStr
	tb.ifpMu.RUnlock()
	if needBaselineReload && tb.db != nil {
		_, _ = tb.db.EnsureVolumeBaselinesPopulated(ctx)
		if err := tb.volumeSurgeScanner.LoadBaselines(ctx); err == nil {
			tb.ifpMu.Lock()
			tb.lastVolBaselineDate = todayStr
			tb.ifpMu.Unlock()
		}
	}

	var foStocks map[string]int64
	if tb.securityMaster != nil {
		foStocks, _ = tb.securityMaster.GetFOStocks(ctx)
	}
	if len(foStocks) == 0 {
		tb.watchlistMutex.RLock()
		foStocks = make(map[string]int64, len(tb.watchlist))
		for s, tok := range tb.watchlist {
			foStocks[s] = tok
		}
		tb.watchlistMutex.RUnlock()
	}

	// Refresh batched REST quotes every 45s (gives OHLC prevClose + full-day VolumeTraded even post-market)
	tb.ifpMu.RLock()
	cachedQuotes := tb.volQuoteCache
	lastFetch := tb.volQuoteFetch
	tb.ifpMu.RUnlock()

	if len(cachedQuotes) == 0 || lastFetch.IsZero() || time.Since(lastFetch) >= 45*time.Second {
		if tb.kiteClient != nil && len(foStocks) > 0 {
			keys := make([]string, 0, len(foStocks))
			for sym := range foStocks {
				keys = append(keys, "NSE:"+strings.ToUpper(strings.TrimSpace(sym)))
			}
			fetched, err := tb.batchedGetQuote(keys, 250)
			if err == nil && len(fetched) > 0 {
				normalized := make(map[string]data.Quote, len(fetched))
				for sym, tok := range foStocks {
					uSym := strings.ToUpper(strings.TrimSpace(sym))
					if q, ok := fetched["NSE:"+uSym]; ok {
						q.InstrumentToken = uint32(tok)
						normalized[uSym] = q
					}
				}
				tb.ifpMu.Lock()
				tb.volQuoteCache = normalized
				tb.volQuoteFetch = nowIST
				cachedQuotes = normalized
				lastFetch = nowIST
				tb.ifpMu.Unlock()
			}
		}
	}

	quotes := make(map[string]data.Quote, len(foStocks))
	for sym, q := range cachedQuotes {
		quotes[sym] = q
	}

	// Overlay fresher live WebSocket tick cumulative Volume & LTP when streaming
	if tb.ticker != nil {
		for sym, tok := range foStocks {
			uSym := strings.ToUpper(strings.TrimSpace(sym))
			if tick := tb.ticker.GetLatestTick(tok); tick != nil && tick.LTP > 0 && tick.Volume > 0 {
				q := quotes[uSym]
				q.InstrumentToken = uint32(tok)
				q.LastPrice = tick.LTP
				if tick.Volume > q.VolumeTraded {
					q.VolumeTraded = tick.Volume
				}
				quotes[uSym] = q
			}
		}
	}

	if len(quotes) == 0 {
		return nil, lastFetch, "EMPTY", nil
	}

	records := tb.volumeSurgeScanner.ScanBatch(quotes, lookback, minMultiplier, nowIST)
	if quadrantFilter != "" {
		filtered := make([]data.VolumeSurgeRecord, 0, len(records))
		for _, r := range records {
			if r.Quadrant == quadrantFilter {
				filtered = append(filtered, r)
			}
		}
		records = filtered
	}
	if lastFetch.IsZero() {
		lastFetch = nowIST
	}
	return records, lastFetch, "LIVE_ZERODHA", nil
}

// GetLastZerodhaTimestamps returns DB + in-memory last Zerodha sync timestamps across all tabs.
func (tb *TradingBot) GetLastZerodhaTimestamps(ctx context.Context) map[string]string {
	out := make(map[string]string)
	if tb.db != nil {
		if dbTS, err := tb.db.GetLastZerodhaDataTimestamps(ctx); err == nil {
			for k, v := range dbTS {
				out[k] = v
			}
		}
	}

	if tb.ticker != nil {
		if tb.ticker.IsConnected() {
			out["ws_connected"] = "true"
		} else {
			out["ws_connected"] = "false"
		}
	}

	tb.ifpMu.RLock()
	if !tb.futLastFetch.IsZero() {
		out["ifp_futures_sync"] = tb.futLastFetch.Format("2006-01-02 15:04:05 IST")
	}
	if !tb.volQuoteFetch.IsZero() {
		out["ifp_volume_sync"] = tb.volQuoteFetch.Format("2006-01-02 15:04:05 IST")
	}
	if !tb.lastQuantSync.IsZero() {
		out["quant_scanner_sync"] = tb.lastQuantSync.Format("2006-01-02 15:04:05 IST")
	}
	var maxFO time.Time
	for _, ts := range tb.foLastFetch {
		if ts.After(maxFO) {
			maxFO = ts
		}
	}
	if !maxFO.IsZero() {
		out["ifp_fo_sync"] = maxFO.Format("2006-01-02 15:04:05 IST")
	}
	tb.ifpMu.RUnlock()

	return out
}
