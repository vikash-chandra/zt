package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"zerodha-trading/data"
	"zerodha-trading/scanner"
)

var (
	stockChainCacheMu sync.RWMutex
	stockChainCache   = make(map[string]cachedStockChain)
)

type cachedStockChain struct {
	fetchedAt time.Time
	view      scanner.OptionChainAViewResponse
}

// FetchStockOptionChain fetches an on-demand, lot-normalized Option Chain for any F&O stock,
// using its nearest NFO futures price as the spot stand-in (matching IndexOI Stocks tab).
func (tb *TradingBot) FetchStockOptionChain(ctx context.Context, symbol string, sortDesc bool) (scanner.OptionChainAViewResponse, error) {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if sym == "" {
		sym = "RELIANCE"
	}

	stockChainCacheMu.RLock()
	if cached, ok := stockChainCache[sym]; ok && time.Since(cached.fetchedAt) < 30*time.Second {
		view := cached.view
		stockChainCacheMu.RUnlock()
		if sortDesc && len(view.Rows) > 1 && view.Rows[0].StrikePrice < view.Rows[len(view.Rows)-1].StrikePrice {
			for i, j := 0, len(view.Rows)-1; i < j; i, j = i+1, j-1 {
				view.Rows[i], view.Rows[j] = view.Rows[j], view.Rows[i]
			}
		}
		return view, nil
	}
	stockChainCacheMu.RUnlock()

	if tb.kiteClient == nil || tb.securityMaster == nil {
		return scanner.OptionChainAViewResponse{}, fmt.Errorf("broker client or security master not initialized")
	}

	nowIST := data.NowIST()
	todayStr := nowIST.Format("2006-01-02")

	// Future price stands in for spot on single-stock option chains
	futPrice := 0.0
	if futContracts, err := tb.securityMaster.GetNearestFuturesContracts(ctx, "NFO"); err == nil {
		if futInst, ok := futContracts[sym]; ok {
			futKey := "NFO:" + futInst.TradingSymbol
			if qMap, qErr := tb.kiteClient.GetQuote(futKey); qErr == nil {
				if q, ok := qMap[futKey]; ok && q.LastPrice > 0 {
					futPrice = q.LastPrice
				}
			}
		}
	}
	spotPrice := futPrice
	if spotPrice <= 0 {
		spotKey := "NSE:" + sym
		if qMap, qErr := tb.kiteClient.GetQuote(spotKey); qErr == nil {
			if q, ok := qMap[spotKey]; ok && q.LastPrice > 0 {
				spotPrice = q.LastPrice
			}
		}
	}
	if spotPrice <= 0 {
		return scanner.OptionChainAViewResponse{}, fmt.Errorf("no live futures/spot quote available for %s", sym)
	}

	insts, err := tb.securityMaster.GetOptionInstruments(ctx, "NFO")
	if err != nil {
		return scanner.OptionChainAViewResponse{}, err
	}

	todayMid := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 0, 0, 0, 0, data.ISTLocation)
	var nearestExp time.Time
	var matching []data.Instrument
	for _, inst := range insts {
		if strings.ToUpper(inst.Name) != sym {
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
		if expMid.Before(todayMid) {
			continue
		}
		if nearestExp.IsZero() || expMid.Before(nearestExp) {
			nearestExp = expMid
		}
		matching = append(matching, inst)
	}
	if nearestExp.IsZero() || len(matching) == 0 {
		return scanner.OptionChainAViewResponse{}, fmt.Errorf("no active NFO option contracts found for %s", sym)
	}

	type pair struct {
		ce *data.Instrument
		pe *data.Instrument
	}
	strikeMap := make(map[float64]*pair)
	for i := range matching {
		inst := &matching[i]
		expIST := data.NormalizeToIST(inst.Expiry)
		expMid := time.Date(expIST.Year(), expIST.Month(), expIST.Day(), 0, 0, 0, 0, data.ISTLocation)
		if !expMid.Equal(nearestExp) {
			continue
		}
		p, ok := strikeMap[inst.Strike]
		if !ok {
			p = &pair{}
			strikeMap[inst.Strike] = p
		}
		if inst.InstrumentType == "CE" {
			p.ce = inst
		} else {
			p.pe = inst
		}
	}

	var strikes []float64
	for st, p := range strikeMap {
		if p.ce != nil && p.pe != nil {
			strikes = append(strikes, st)
		}
	}
	if len(strikes) == 0 {
		return scanner.OptionChainAViewResponse{}, fmt.Errorf("no matched CE/PE strikes for %s", sym)
	}
	sort.Float64s(strikes)

	atmIdx := 0
	minD := math.Abs(strikes[0] - spotPrice)
	for i, st := range strikes {
		if d := math.Abs(st - spotPrice); d < minD {
			minD = d
			atmIdx = i
		}
	}
	sIdx := atmIdx - 12
	if sIdx < 0 {
		sIdx = 0
	}
	eIdx := atmIdx + 12
	if eIdx >= len(strikes) {
		eIdx = len(strikes) - 1
	}
	sel := strikes[sIdx : eIdx+1]

	quoteKeys := make([]string, 0, len(sel)*2)
	for _, st := range sel {
		p := strikeMap[st]
		quoteKeys = append(quoteKeys, "NFO:"+p.ce.TradingSymbol, "NFO:"+p.pe.TradingSymbol)
	}
	optQuotes, err := tb.batchedGetQuote(quoteKeys, 200)
	if err != nil {
		return scanner.OptionChainAViewResponse{}, err
	}

	snaps := make([]data.FOStrikeOISnapshot, 0, len(sel))
	for i, st := range sel {
		p := strikeMap[st]
		ceQ := optQuotes["NFO:"+p.ce.TradingSymbol]
		peQ := optQuotes["NFO:"+p.pe.TradingSymbol]
		cChg := int64(0)
		if ceQ.OIDayLow > 0 && ceQ.OI >= ceQ.OIDayLow {
			cChg = ceQ.OI - ceQ.OIDayLow
		}
		pChg := int64(0)
		if peQ.OIDayLow > 0 && peQ.OI >= peQ.OIDayLow {
			pChg = peQ.OI - peQ.OIDayLow
		}
		cLTPChg := 0.0
		if ceQ.OHLC.Close > 0 && ceQ.LastPrice > 0 {
			cLTPChg = math.Round((ceQ.LastPrice-ceQ.OHLC.Close)*100) / 100
		}
		pLTPChg := 0.0
		if peQ.OHLC.Close > 0 && peQ.LastPrice > 0 {
			pLTPChg = math.Round((peQ.LastPrice-peQ.OHLC.Close)*100) / 100
		}
		lot := p.ce.LotSize
		if lot <= 0 {
			lot = 1
		}
		snaps = append(snaps, data.FOStrikeOISnapshot{
			Symbol:       sym,
			ExpiryDate:   nearestExp,
			StrikePrice:  st,
			CallOI:       ceQ.OI,
			CallOIChange: cChg,
			PutOI:        peQ.OI,
			PutOIChange:  pChg,
			CallLTP:      ceQ.LastPrice,
			PutLTP:       peQ.LastPrice,
			CallVolume:   ceQ.VolumeTraded,
			PutVolume:    peQ.VolumeTraded,
			CallLTPChg:   cLTPChg,
			PutLTPChg:    pLTPChg,
			SpotPrice:    spotPrice,
			FuturesPrice: futPrice,
			LotSize:      lot,
			ATMOffset:    (sIdx + i) - atmIdx,
			SnapshotTime: nowIST,
		})
	}

	view := scanner.BuildOptionChainAView(sym, todayStr, "", true, false, snaps, []string{todayStr})
	stockChainCacheMu.Lock()
	stockChainCache[sym] = cachedStockChain{fetchedAt: time.Now(), view: view}
	stockChainCacheMu.Unlock()

	if sortDesc {
		return scanner.BuildOptionChainAView(sym, todayStr, "", true, true, snaps, []string{todayStr}), nil
	}
	return view, nil
}

// RefreshIndexStructure fetches live quotes for NIFTY 50 (50 stocks) or NIFTY BANK (14 stocks)
// constituents, computes Up/Down/Flat (+-0.25%) breadth & regime, and persists to DB.
func (tb *TradingBot) RefreshIndexStructure(ctx context.Context, indexName string) (data.IndexStructureSnapshot, error) {
	idx := strings.ToUpper(strings.TrimSpace(indexName))
	var constituents []string
	var spotKey string
	if idx == "BANKNIFTY" || idx == "NIFTY BANK" {
		idx = "NIFTY BANK"
		spotKey = "NSE:NIFTY BANK"
		constituents = scanner.BankNiftyConstituents
	} else {
		idx = "NIFTY 50"
		spotKey = "NSE:NIFTY 50"
		constituents = scanner.Nifty50Constituents
	}

	if tb.kiteClient == nil {
		return data.IndexStructureSnapshot{}, fmt.Errorf("kiteClient not initialized")
	}

	keys := make([]string, 0, len(constituents)+1)
	keys = append(keys, spotKey)
	for _, s := range constituents {
		keys = append(keys, "NSE:"+s)
	}
	quotes, err := tb.batchedGetQuote(keys, 200)
	if err != nil {
		return data.IndexStructureSnapshot{}, err
	}

	spotQ := quotes[spotKey]
	if spotQ.LastPrice <= 0 {
		return data.IndexStructureSnapshot{}, fmt.Errorf("empty spot quote for %s", spotKey)
	}

	nowIST := data.NowIST().Truncate(time.Minute)
	spotChg := 0.0
	spotChgPct := 0.0
	if spotQ.OHLC.Close > 0 {
		spotChg = math.Round((spotQ.LastPrice-spotQ.OHLC.Close)*100) / 100
		spotChgPct = math.Round((spotChg/spotQ.OHLC.Close)*10000) / 100
	}

	var upCnt, downCnt, flatCnt int
	items := make([]scanner.StructureConstituent, 0, len(constituents))
	for _, sym := range constituents {
		q := quotes["NSE:"+sym]
		chgPct := 0.0
		if q.LastPrice > 0 && q.OHLC.Close > 0 {
			chgPct = math.Round(((q.LastPrice-q.OHLC.Close)/q.OHLC.Close)*10000) / 100
		}
		status := "FLAT"
		if chgPct > 0.25 {
			status = "UP"
			upCnt++
		} else if chgPct < -0.25 {
			status = "DOWN"
			downCnt++
		} else {
			flatCnt++
		}
		items = append(items, scanner.StructureConstituent{
			Symbol:    sym,
			LTP:       math.Round(q.LastPrice*100) / 100,
			PrevClose: math.Round(q.OHLC.Close*100) / 100,
			ChangePct: chgPct,
			Status:    status,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ChangePct > items[j].ChangePct })

	regime := "MIXED"
	margin := int(math.Max(2, float64(len(constituents))*0.18))
	if upCnt-downCnt >= margin {
		regime = "BULLISH"
	} else if downCnt-upCnt >= margin {
		regime = "BEARISH"
	}

	heldMins := 1
	if tb.db != nil {
		if prev, pErr := tb.db.GetLatestIndexStructure(ctx, idx, nowIST.Format("2006-01-02")); pErr == nil && prev.IndexName == idx {
			if prev.Regime == regime {
				diffM := int(nowIST.Sub(prev.SnapshotTime).Minutes())
				if diffM < 1 {
					diffM = 0
				}
				heldMins = prev.HeldMinutes + diffM
			}
		}
	}

	rawJSON, _ := json.Marshal(items)
	snap := data.IndexStructureSnapshot{
		IndexName:        idx,
		SnapshotTime:     nowIST,
		SpotPrice:        math.Round(spotQ.LastPrice*100) / 100,
		SpotChange:       spotChg,
		SpotChangePct:    spotChgPct,
		DayHigh:          spotQ.OHLC.High,
		DayLow:           spotQ.OHLC.Low,
		PrevClose:        spotQ.OHLC.Close,
		UpCount:          upCnt,
		DownCount:        downCnt,
		FlatCount:        flatCnt,
		Regime:           regime,
		HeldMinutes:      heldMins,
		ConstituentsJSON: string(rawJSON),
	}
	if tb.db != nil {
		_ = tb.db.SaveIndexStructureSnapshot(ctx, snap)
	}
	return snap, nil
}

// BuildIndexStructureView combines constituent breadth with option chain heatmap & intraday Call vs Put curve.
func (tb *TradingBot) BuildIndexStructureView(ctx context.Context, indexName, metric, dateStr string) (scanner.StructureViewResponse, error) {
	idx := "NIFTY 50"
	if strings.Contains(strings.ToUpper(indexName), "BANK") {
		idx = "NIFTY BANK"
	}
	if metric == "" {
		metric = "oi_chg"
	}

	var snap data.IndexStructureSnapshot
	if tb.db != nil {
		snap, _ = tb.db.GetLatestIndexStructure(ctx, idx, dateStr)
	}
	if snap.IndexName == "" && (dateStr == "" || dateStr == data.NowIST().Format("2006-01-02")) {
		if live, err := tb.RefreshIndexStructure(ctx, idx); err == nil {
			snap = live
		}
	}

	resp := scanner.StructureViewResponse{
		IndexName:     idx,
		Date:          dateStr,
		SpotPrice:     snap.SpotPrice,
		SpotChange:    snap.SpotChange,
		SpotChangePct: snap.SpotChangePct,
		DayHigh:       snap.DayHigh,
		DayLow:        snap.DayLow,
		PrevClose:     snap.PrevClose,
		UpCount:       snap.UpCount,
		DownCount:     snap.DownCount,
		FlatCount:     snap.FlatCount,
		Regime:        snap.Regime,
		HeldMinutes:   snap.HeldMinutes,
		HeatmapMetric: metric,
	}
	if !snap.SnapshotTime.IsZero() {
		resp.LastSyncIST = snap.SnapshotTime.Format("2006-01-02 15:04:05 IST")
	}
	if snap.ConstituentsJSON != "" {
		_ = json.Unmarshal([]byte(snap.ConstituentsJSON), &resp.Constituents)
	}

	if tb.db != nil {
		hist, err := tb.db.GetFOStrikeOIHistoryByDate(ctx, idx, dateStr)
		if err == nil && len(hist) > 0 {
			chainView := scanner.BuildOptionChainAView(idx, dateStr, "", false, false, hist, nil)
			if resp.SpotPrice <= 0 {
				resp.SpotPrice = chainView.SpotPrice
			}
			resp.ATMStrike = chainView.ATMStrike

			// Identify top 2 Call walls (Resistance) & top 2 Put walls (Support)
			type wallCand struct {
				strike float64
				oi     int64
			}
			var calls, puts []wallCand
			for _, r := range chainView.Rows {
				calls = append(calls, wallCand{r.StrikePrice, r.Calls.OI})
				puts = append(puts, wallCand{r.StrikePrice, r.Puts.OI})
			}
			sort.Slice(calls, func(i, j int) bool { return calls[i].oi > calls[j].oi })
			sort.Slice(puts, func(i, j int) bool { return puts[i].oi > puts[j].oi })
			if len(calls) > 0 {
				resp.Resistance = calls[0].strike
				resp.MajorResistance = calls[0].strike
			}
			if len(calls) > 1 {
				if calls[1].strike > calls[0].strike {
					resp.MajorResistance = calls[1].strike
				} else {
					resp.Resistance = calls[1].strike
				}
			}
			if len(puts) > 0 {
				resp.Support = puts[0].strike
				resp.MajorSupport = puts[0].strike
			}
			if len(puts) > 1 {
				if puts[1].strike < puts[0].strike {
					resp.MajorSupport = puts[1].strike
				} else {
					resp.Support = puts[1].strike
				}
			}
			if resp.Resistance > 0 && resp.SpotPrice > 0 {
				resp.PointsToCeiling = math.Round(resp.Resistance - resp.SpotPrice)
			}
			if resp.Support > 0 && resp.SpotPrice > 0 {
				resp.PointsToFloor = math.Round(resp.SpotPrice - resp.Support)
			}

			// Build Heatmap rows (+-8 strikes around ATM)
			lot := float64(chainView.LotSize)
			if lot <= 0 {
				lot = 1
			}
			for _, r := range chainView.Rows {
				if math.Abs(r.StrikePrice-chainView.ATMStrike) > scanner.StrikeStepForSymbol(idx, nil)*8 {
					continue
				}
				var cv, pv float64
				var cl, pl string
				switch metric {
				case "oi":
					cv = float64(r.Calls.OI) * lot
					pv = float64(r.Puts.OI) * lot
					cl, pl = formatLakhsK(cv), formatLakhsK(pv)
				case "volume":
					cv = float64(r.Calls.Volume) * lot
					pv = float64(r.Puts.Volume) * lot
					cl, pl = formatLakhsK(cv), formatLakhsK(pv)
				case "iv":
					cv, pv = r.Calls.IV, r.Puts.IV
					cl, pl = fmt.Sprintf("%.1f%%", cv), fmt.Sprintf("%.1f%%", pv)
				default: // oi_chg
					cv = float64(r.Calls.OIChg) * lot
					pv = float64(r.Puts.OIChg) * lot
					cl, pl = formatLakhsK(cv), formatLakhsK(pv)
				}
				resp.Heatmap = append(resp.Heatmap, scanner.StructureHeatmapRow{
					StrikePrice: r.StrikePrice,
					CallValue:   cv,
					PutValue:    pv,
					CallLabel:   cl,
					PutLabel:    pl,
				})
			}

			// Intraday Call vs Put OI Change curve across all timestamps
			tsMap := make(map[int64][]data.FOStrikeOISnapshot)
			var times []time.Time
			seen := make(map[int64]bool)
			for _, s := range hist {
				ts := data.NormalizeToIST(s.SnapshotTime)
				k := ts.Unix()
				if !seen[k] {
					seen[k] = true
					times = append(times, ts)
				}
				tsMap[k] = append(tsMap[k], s)
			}
			sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
			for _, t := range times {
				var sumC, sumP int64
				for _, s := range tsMap[t.Unix()] {
					sumC += s.CallOIChange
					sumP += s.PutOIChange
				}
				cL := math.Round((float64(sumC)/1e5)*100) / 100
				pL := math.Round((float64(sumP)/1e5)*100) / 100
				leader := "PUTS"
				if cL > pL {
					leader = "CALLS"
				}
				resp.IntradaySeries = append(resp.IntradaySeries, scanner.StructureIntradayPoint{
					Time:       t.Format("15:04"),
					CallOIChgL: cL,
					PutOIChgL:  pL,
					Leader:     leader,
				})
				resp.PutsAheadLakhs = math.Round((pL-cL)*100) / 100
			}
		}
	}

	return resp, nil
}

// CollectIndexOIMinuteSnapshot runs every 1 minute during market hours to capture NIFTY & BANKNIFTY
// option chains (+-16 strikes), update OI Pulse 15m checkpoints, and refresh N/B Structure breadth.
func (tb *TradingBot) CollectIndexOIMinuteSnapshot(ctx context.Context) {
	for _, sym := range []string{"NIFTY 50", "NIFTY BANK"} {
		if ctx.Err() != nil {
			return
		}
		_, _ = tb.RefreshFOSnapshotForSymbol(ctx, sym, true)
		_, _ = tb.RefreshIndexStructure(ctx, sym)
	}

	if tb.db != nil {
		todayStr := data.NowIST().Format("2006-01-02")
		for _, sym := range []string{"NIFTY 50", "NIFTY BANK"} {
			if hist, err := tb.db.GetFOStrikeOIHistoryByDate(ctx, sym, todayStr); err == nil && len(hist) > 0 {
				pulse := scanner.BuildOIPulseView(sym, todayStr, hist, nil)
				if len(pulse.Checkpoints) > 0 {
					_ = tb.db.UpsertOIPulseCheckpoints(ctx, pulse.Checkpoints)
				}
			}
		}
	}
}

// FetchNSEFIIDIIParticipants fetches NSE's daily EOD participant-wise OI CSV for the Straddle tab.
func (tb *TradingBot) FetchNSEFIIDIIParticipants(ctx context.Context, targetDate time.Time) ([]data.FIIDIIParticipantRecord, error) {
	if targetDate.IsZero() {
		targetDate = data.NowIST()
	}
	ddmmyyyy := targetDate.Format("02012006")
	dateISO := targetDate.Format("2006-01-02")
	url := fmt.Sprintf("https://archives.nseindia.com/content/nsccl/fao_participant_oi_%s.csv", ddmmyyyy)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/csv,application/csv,*/*")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("NSE participant OI returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}

	r := csv.NewReader(strings.NewReader(string(body)))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}

	var prevMap map[string]data.FIIDIIParticipantRecord
	if tb.db != nil {
		if prevList, pErr := tb.db.GetLatestFIIDIIParticipants(ctx, ""); pErr == nil {
			prevMap = make(map[string]data.FIIDIIParticipantRecord, len(prevList))
			for _, p := range prevList {
				if p.TradeDate < dateISO {
					prevMap[p.Participant] = p
				}
			}
		}
	}

	parseInt := func(s string) int64 {
		v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		return v
	}

	var out []data.FIIDIIParticipantRecord
	for _, row := range rows {
		if len(row) < 9 {
			continue
		}
		clientType := strings.TrimSpace(row[0])
		if clientType != "FII" && clientType != "DII" && clientType != "Pro" && clientType != "Client" {
			continue
		}
		futLong := parseInt(row[1])
		futShort := parseInt(row[2])
		futNet := futLong - futShort
		longPct := 0.0
		if futLong+futShort > 0 {
			longPct = math.Round((float64(futLong)/float64(futLong+futShort))*1000) / 10
		}
		callsNet := parseInt(row[5]) - parseInt(row[6])
		putsNet := parseInt(row[7]) - parseInt(row[8])
		futChg := int64(0)
		if prev, ok := prevMap[clientType]; ok {
			futChg = futNet - prev.IndexFuturesNet
		}
		out = append(out, data.FIIDIIParticipantRecord{
			TradeDate:       dateISO,
			Participant:     clientType,
			IndexFuturesNet: futNet,
			IndexFuturesChg: futChg,
			LongPct:         longPct,
			IndexCallsNet:   callsNet,
			IndexPutsNet:    putsNet,
		})
	}

	if tb.db != nil && len(out) > 0 {
		_ = tb.db.UpsertFIIDIIParticipants(ctx, out)
	}
	return out, nil
}

func formatLakhsK(v float64) string {
	abs := math.Abs(v)
	sign := ""
	if v < 0 {
		sign = "-"
	}
	if abs >= 100000 {
		return fmt.Sprintf("%s%.1fL", sign, abs/100000)
	}
	if abs >= 1000 {
		return fmt.Sprintf("%s%.1fK", sign, abs/1000)
	}
	return fmt.Sprintf("%s%.0f", sign, abs)
}
