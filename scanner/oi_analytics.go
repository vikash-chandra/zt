package scanner

import (
	"math"
	"sort"
	"strings"
	"time"

	"zerodha-trading/data"
)

// Nifty50Constituents lists the 50 NSE NIFTY 50 constituent symbols for N Structure breadth.
var Nifty50Constituents = []string{
	"ADANIENT", "ADANIPORTS", "APOLLOHOSP", "ASIANPAINT", "AXISBANK",
	"BAJAJ-AUTO", "BAJFINANCE", "BAJAJFINSV", "BEL", "BHARTIARTL",
	"BPCL", "BRITANNIA", "CIPLA", "COALINDIA", "DRREDDY",
	"EICHERMOT", "ETERNAL", "GRASIM", "HCLTECH", "HDFCBANK",
	"HDFCLIFE", "HEROMOTOCO", "HINDALCO", "HINDUNILVR", "ICICIBANK",
	"INDUSINDBK", "INFY", "ITC", "JSWSTEEL", "KOTAKBANK",
	"LT", "M&M", "MARUTI", "NESTLEIND", "NTPC",
	"ONGC", "POWERGRID", "RELIANCE", "SBILIFE", "SBIN",
	"SHRIRAMFIN", "SUNPHARMA", "TATACONSUM", "TATAMOTORS", "TATASTEEL",
	"TCS", "TECHM", "TITAN", "TRENT", "WIPRO",
}

// BankNiftyConstituents lists the 14 NSE NIFTY BANK constituent symbols for B Structure breadth.
var BankNiftyConstituents = []string{
	"AUBANK", "AXISBANK", "BANDHANBNK", "BANKBARODA", "CANBK",
	"FEDERALBNK", "HDFCBANK", "ICICIBANK", "IDFCFIRSTB", "INDUSINDBK",
	"KOTAKBANK", "PNB", "SBIN", "YESBANK",
}

// ThreeStrikeRow represents one strike inside the 3-Strike Range card.
type ThreeStrikeRow struct {
	StrikePrice float64 `json:"strike_price"`
	CallOIChg   int64   `json:"call_oi_chg"`
	PutOIChg    int64   `json:"put_oi_chg"`
	DiffPC      int64   `json:"diff_pc"`
	Verdict     string  `json:"verdict"`
}

// StrikeLadderRow represents one row in the +-6 Strike Ladder table.
type StrikeLadderRow struct {
	StrikePrice   float64 `json:"strike_price"`
	NetOIChange   int64   `json:"net_oi_change"` // PutOIChg - CallOIChg
	NetOI         int64   `json:"net_oi"`        // PutOI - CallOI
	CallVolume    int64   `json:"call_volume"`
	PutVolume     int64   `json:"put_volume"`
	IsSpotAbove   bool    `json:"is_spot_above"`
	IsHighestPut  bool    `json:"is_highest_put"`
	IsSecondPut   bool    `json:"is_second_put"`
	IsHighestCall bool    `json:"is_highest_call"`
	IsSecondCall  bool    `json:"is_second_call"`
}

// IntradayStrikeSlice holds Call/Put OI change for one of the 3 strikes at a timestamp.
type IntradayStrikeSlice struct {
	StrikePrice float64 `json:"strike_price"`
	CallOIChg   int64   `json:"call_oi_chg"`
	PutOIChg    int64   `json:"put_oi_chg"`
}

// IntradayOIChangeRow represents a single time row in the Change in OI — Intraday table.
type IntradayOIChangeRow struct {
	Time               string                `json:"time"`
	SpotPrice          float64               `json:"spot_price"`
	Strikes            []IntradayStrikeSlice `json:"strikes"`
	CallOIChange       int64                 `json:"call_oi_change"`
	PutOIChange        int64                 `json:"put_oi_change"`
	Difference         int64                 `json:"difference"`           // PutOIChange - CallOIChange
	ChangeInDifference int64                 `json:"change_in_difference"` // diff[t] - diff[t-1]
	RevSignal          string                `json:"rev_signal"`           // BULLISH, BEARISH, or ""
}

// ThreeStrikeViewResponse is the full payload for /api/indexoi/three-strike.
type ThreeStrikeViewResponse struct {
	Symbol           string                `json:"symbol"`
	Date             string                `json:"date"`
	ReplayTime       string                `json:"replay_time"`
	AvailableTimes   []string              `json:"available_times"`
	AvailableDates   []string              `json:"available_dates"`
	SpotPrice        float64               `json:"spot_price"`
	SpotChange       float64               `json:"spot_change"`
	SpotChangePct    float64               `json:"spot_change_pct"`
	PCR              float64               `json:"pcr"`
	SupportStrike    float64               `json:"support_strike"`
	ResistanceStrike float64               `json:"resistance_strike"`
	BaseStrike       float64               `json:"base_strike"`
	StrikeStep       float64               `json:"strike_step"`
	BaseOptions      []float64             `json:"base_options"`
	IntervalMins     int                   `json:"interval_mins"`
	ThreeStrikeRows  []ThreeStrikeRow      `json:"three_strike_rows"`
	TotalCallChg     int64                 `json:"total_call_chg"`
	TotalPutChg      int64                 `json:"total_put_chg"`
	TotalDiffPC      int64                 `json:"total_diff_pc"`
	Ladder           []StrikeLadderRow     `json:"ladder"`
	IntradayRows     []IntradayOIChangeRow `json:"intraday_rows"`
	LastSyncIST      string                `json:"last_sync_ist"`
}

// OptionChainSideMetrics holds Calls or Puts metrics for one strike in A-View / Stocks OI.
type OptionChainSideMetrics struct {
	IV          float64 `json:"iv"`
	Delta       float64 `json:"delta"`
	OIChg       int64   `json:"oi_chg"` // in contracts (lots)
	OIChgMaxPct float64 `json:"oi_chg_max_pct"`
	OI          int64   `json:"oi"` // in contracts (lots)
	OIMaxPct    float64 `json:"oi_max_pct"`
	Volume      int64   `json:"volume"` // in contracts (lots)
	VolMaxPct   float64 `json:"vol_max_pct"`
	LTP         float64 `json:"ltp"`
	LTPChg      float64 `json:"ltp_chg"`
	TimeValue   float64 `json:"time_value"`
	Reversal    float64 `json:"reversal"`
}

// OptionChainARow represents one strike row in A NIFTY / A BANKNIFTY / Stocks OI.
type OptionChainARow struct {
	StrikePrice float64                `json:"strike_price"`
	PCR         float64                `json:"pcr"`
	IsATM       bool                   `json:"is_atm"`
	IsCallWall  bool                   `json:"is_call_wall"`
	IsPutWall   bool                   `json:"is_put_wall"`
	Calls       OptionChainSideMetrics `json:"calls"`
	Puts        OptionChainSideMetrics `json:"puts"`
}

// OptionChainSideSummary holds top summary bar totals for Calls or Puts.
type OptionChainSideSummary struct {
	TvItm      float64 `json:"tv_itm"`
	LTPChg     float64 `json:"ltp_chg"`
	OIChgPct   float64 `json:"oi_chg_pct"`
	TotalDelta float64 `json:"total_delta"`
	WallStrike float64 `json:"wall_strike"`
}

// OptionChainAViewResponse is the full payload for /api/indexoi/chain and /api/indexoi/stocks/chain.
type OptionChainAViewResponse struct {
	Symbol         string                 `json:"symbol"`
	IsStock        bool                   `json:"is_stock"`
	SpotNote       string                 `json:"spot_note"`
	Date           string                 `json:"date"`
	ExpiryDate     string                 `json:"expiry_date"`
	SnapshotTime   string                 `json:"snapshot_time"`
	AvailableTimes []string               `json:"available_times"`
	AvailableDates []string               `json:"available_dates"`
	SpotPrice      float64                `json:"spot_price"`
	FuturesPrice   float64                `json:"futures_price"`
	FMinusS        float64                `json:"f_minus_s"`
	ATMStrike      float64                `json:"atm_strike"`
	LotSize        int                    `json:"lot_size"`
	GEXCrore       float64                `json:"gex_crore"`
	FlipStrike     float64                `json:"flip_strike"`
	WallStatus     string                 `json:"wall_status"`
	ReversalLevels []string               `json:"reversal_levels"`
	CallSummary    OptionChainSideSummary `json:"call_summary"`
	PutSummary     OptionChainSideSummary `json:"put_summary"`
	Rows           []OptionChainARow      `json:"rows"`
}

// OIPulsePoint represents a 1-minute/snapshot point for the OI Pulse Spot & D% charts.
type OIPulsePoint struct {
	Time      string  `json:"time"`
	SpotPrice float64 `json:"spot_price"`
	DiffPct   float64 `json:"diff_pct"`
	DiffPC    int64   `json:"diff_pc"`
}

// OIPulseViewResponse is the full payload for /api/indexoi/pulse.
type OIPulseViewResponse struct {
	Symbol         string                   `json:"symbol"`
	Date           string                   `json:"date"`
	AvailableDates []string                 `json:"available_dates"`
	SpotSeries     []OIPulsePoint           `json:"spot_series"`
	Checkpoints    []data.OIPulseCheckpoint `json:"checkpoints"`
}

// StructureConstituent represents one constituent stock in N/B Structure.
type StructureConstituent struct {
	Symbol    string  `json:"symbol"`
	LTP       float64 `json:"ltp"`
	PrevClose float64 `json:"prev_close"`
	ChangePct float64 `json:"change_pct"`
	Status    string  `json:"status"` // UP, DOWN, FLAT
}

// StructureHeatmapRow represents one strike row in the N/B Structure Heatmap.
type StructureHeatmapRow struct {
	StrikePrice float64 `json:"strike_price"`
	CallValue   float64 `json:"call_value"`
	PutValue    float64 `json:"put_value"`
	CallLabel   string  `json:"call_label"`
	PutLabel    string  `json:"put_label"`
}

// StructureIntradayPoint holds cumulative Call vs Put OI Change for the Structure chart.
type StructureIntradayPoint struct {
	Time       string  `json:"time"`
	CallOIChgL float64 `json:"call_oi_chg_l"`
	PutOIChgL  float64 `json:"put_oi_chg_l"`
	Leader     string  `json:"leader"` // CALLS or PUTS
}

// StructureViewResponse is the full payload for /api/indexoi/structure.
type StructureViewResponse struct {
	IndexName       string                   `json:"index_name"`
	Date            string                   `json:"date"`
	SpotPrice       float64                  `json:"spot_price"`
	SpotChange      float64                  `json:"spot_change"`
	SpotChangePct   float64                  `json:"spot_change_pct"`
	DayHigh         float64                  `json:"day_high"`
	DayLow          float64                  `json:"day_low"`
	PrevClose       float64                  `json:"prev_close"`
	ATMStrike       float64                  `json:"atm_strike"`
	UpCount         int                      `json:"up_count"`
	DownCount       int                      `json:"down_count"`
	FlatCount       int                      `json:"flat_count"`
	Regime          string                   `json:"regime"`
	HeldMinutes     int                      `json:"held_minutes"`
	Constituents    []StructureConstituent   `json:"constituents"`
	MajorResistance float64                  `json:"major_resistance"`
	Resistance      float64                  `json:"resistance"`
	Support         float64                  `json:"support"`
	MajorSupport    float64                  `json:"major_support"`
	PointsToCeiling float64                  `json:"points_to_ceiling"`
	PointsToFloor   float64                  `json:"points_to_floor"`
	HeatmapMetric   string                   `json:"heatmap_metric"`
	Heatmap         []StructureHeatmapRow    `json:"heatmap"`
	PutsAheadLakhs  float64                  `json:"puts_ahead_lakhs"`
	IntradaySeries  []StructureIntradayPoint `json:"intraday_series"`
	LastSyncIST     string                   `json:"last_sync_ist"`
}

// StraddlePoint represents one intraday Straddle vs VWAP point.
type StraddlePoint struct {
	Time          string  `json:"time"`
	StraddlePrice float64 `json:"straddle_price"`
	VWAP          float64 `json:"vwap"`
}

// StraddleViewResponse is the full payload for /api/indexoi/straddle.
type StraddleViewResponse struct {
	Symbol             string                         `json:"symbol"`
	Date               string                         `json:"date"`
	ExpiryDate         string                         `json:"expiry_date"`
	DaysToExpiry       int                            `json:"days_to_expiry"`
	SelectedStrike     float64                        `json:"selected_strike"`
	ATMStrike          float64                        `json:"atm_strike"`
	AvailableStrikes   []float64                      `json:"available_strikes"`
	AvailableDates     []string                       `json:"available_dates"`
	SpotPrice          float64                        `json:"spot_price"`
	StraddlePrice      float64                        `json:"straddle_price"`
	StraddleOpen       float64                        `json:"straddle_open"`
	StraddleHigh       float64                        `json:"straddle_high"`
	StraddleLow        float64                        `json:"straddle_low"`
	StraddleChg        float64                        `json:"straddle_chg"`
	StraddleChgPct     float64                        `json:"straddle_chg_pct"`
	StraddleVWAP       float64                        `json:"straddle_vwap"`
	VWAPStatus         string                         `json:"vwap_status"`
	RangeLow           float64                        `json:"range_low"`
	RangeHigh          float64                        `json:"range_high"`
	MaxPainStrike      float64                        `json:"max_pain_strike"`
	TrackedStrikes     int                            `json:"tracked_strikes"`
	ChainOI15mPct      float64                        `json:"chain_oi_15m_pct"`
	Straddle15mPct     float64                        `json:"straddle_15m_pct"`
	ExpiryWatchStatus  string                         `json:"expiry_watch_status"`
	TotalCallOIChg     int64                          `json:"total_call_oi_chg"`
	TotalPutOIChg      int64                          `json:"total_put_oi_chg"`
	Series             []StraddlePoint                `json:"series"`
	FIIDIIParticipants []data.FIIDIIParticipantRecord `json:"fii_dii_participants"`
}

// DefaultLotSize returns the standard NSE lot size fallback if SecurityMaster lot size is unavailable.
func DefaultLotSize(symbol string) int {
	switch strings.ToUpper(strings.TrimSpace(symbol)) {
	case "NIFTY", "NIFTY 50":
		return 65
	case "BANKNIFTY", "NIFTY BANK":
		return 30
	case "SENSEX", "BSE SENSEX":
		return 20
	case "FINNIFTY", "NIFTY FIN SERVICE":
		return 65
	case "MIDCPNIFTY", "NIFTY MID SELECT":
		return 120
	default:
		return 1
	}
}

// StrikeStepForSymbol returns standard strike intervals for major NSE indices or infers from sorted strikes.
func StrikeStepForSymbol(symbol string, strikes []float64) float64 {
	switch strings.ToUpper(strings.TrimSpace(symbol)) {
	case "NIFTY", "NIFTY 50":
		return 50
	case "BANKNIFTY", "NIFTY BANK", "SENSEX", "BSE SENSEX":
		return 100
	case "FINNIFTY", "NIFTY FIN SERVICE":
		return 50
	case "MIDCPNIFTY", "NIFTY MID SELECT":
		return 25
	}
	if len(strikes) >= 2 {
		minStep := math.MaxFloat64
		for i := 1; i < len(strikes); i++ {
			d := math.Abs(strikes[i] - strikes[i-1])
			if d > 0 && d < minStep {
				minStep = d
			}
		}
		if minStep < math.MaxFloat64 {
			return minStep
		}
	}
	return 50
}

// ClassifyThreeStrikeVerdict maps (PutOIChg - CallOIChg) to Support / Resistance labels.
func ClassifyThreeStrikeVerdict(diffPC int64) string {
	switch {
	case diffPC >= 1000000:
		return "Strong Support"
	case diffPC >= 200000:
		return "Support"
	case diffPC <= -1000000:
		return "Strong Resistance"
	case diffPC <= -200000:
		return "Resistance"
	default:
		return "Neutral"
	}
}

// ComputeOIPulseDiffPct computes IndexOI's exact D% = (P - C) / max(|P|, |C|) * 100 and ±30% signal.
func ComputeOIPulseDiffPct(callChgContracts, putChgContracts int64) (diffPC int64, diffPct float64, signal string) {
	diffPC = putChgContracts - callChgContracts
	denom := math.Max(math.Abs(float64(putChgContracts)), math.Abs(float64(callChgContracts)))
	if denom > 0 {
		diffPct = math.Round((float64(diffPC)/denom)*10000) / 100
	}
	switch {
	case diffPct >= 30.0:
		signal = "BULLISH"
	case diffPct <= -30.0:
		signal = "BEARISH"
	default:
		signal = "NEUTRAL"
	}
	return diffPC, diffPct, signal
}

// ImpliedVolatilityBisect solves Black-Scholes Implied Volatility via bisection from option LTP.
func ImpliedVolatilityBisect(isCall bool, spot, strike, tYears, r, ltp float64) float64 {
	if spot <= 0 || strike <= 0 || ltp <= 0 || tYears <= 0 {
		return 0
	}
	intrinsic := 0.0
	if isCall && spot > strike {
		intrinsic = spot - strike
	} else if !isCall && strike > spot {
		intrinsic = strike - spot
	}
	if ltp <= intrinsic+0.05 {
		return 0.08
	}

	bsPrice := func(sigma float64) float64 {
		sqrtT := math.Sqrt(tYears)
		d1 := (math.Log(spot/strike) + (r+0.5*sigma*sigma)*tYears) / (sigma * sqrtT)
		d2 := d1 - sigma*sqrtT
		disc := math.Exp(-r * tYears)
		if isCall {
			return spot*normCDF(d1) - strike*disc*normCDF(d2)
		}
		return strike*disc*normCDF(-d2) - spot*normCDF(-d1)
	}

	lo, hi := 0.03, 2.50
	for i := 0; i < 24; i++ {
		mid := 0.5 * (lo + hi)
		if bsPrice(mid) > ltp {
			hi = mid
		} else {
			lo = mid
		}
	}
	return math.Round(0.5*(lo+hi)*10000) / 100 // percentage (e.g. 14.52)
}

// filterMarketSessionSnapshots retains only intraday market snapshots from 09:16 IST through 15:45 IST,
// excluding pre-market (<= 09:15) and post-market evening refreshes.
func filterMarketSessionSnapshots(history []data.FOStrikeOISnapshot) []data.FOStrikeOISnapshot {
	if len(history) == 0 {
		return nil
	}
	out := make([]data.FOStrikeOISnapshot, 0, len(history))
	for _, s := range history {
		hhmm := data.NormalizeToIST(s.SnapshotTime).Format("15:04")
		if hhmm >= "09:16" && hhmm <= "15:45" {
			out = append(out, s)
		}
	}
	return out
}

func groupSnapshotsByTimestamp(history []data.FOStrikeOISnapshot) ([]time.Time, map[int64][]data.FOStrikeOISnapshot) {
	tsMap := make(map[int64][]data.FOStrikeOISnapshot)
	var times []time.Time
	seen := make(map[int64]bool)
	for _, s := range history {
		ts := data.NormalizeToIST(s.SnapshotTime)
		key := ts.Unix()
		if !seen[key] {
			seen[key] = true
			times = append(times, ts)
		}
		tsMap[key] = append(tsMap[key], s)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	return times, tsMap
}

// buildOneMinuteReplayTimes generates 1-minute HH:MM slots across [first..last] and resolves the latest snapshot <= replayTime.
func buildOneMinuteReplayTimes(times []time.Time, replayTime string) ([]string, time.Time) {
	if len(times) == 0 {
		return nil, time.Time{}
	}
	selectedTS := times[len(times)-1]
	if replayTime != "" {
		for _, t := range times {
			if t.Format("15:04") <= replayTime {
				selectedTS = t
			}
		}
	}
	startMin := times[0].Truncate(time.Minute)
	endMin := times[len(times)-1].Truncate(time.Minute)
	availTimes := make([]string, 0, 380)
	for cur := startMin; !cur.After(endMin) && len(availTimes) < 420; cur = cur.Add(time.Minute) {
		availTimes = append(availTimes, cur.Format("15:04"))
	}
	return availTimes, selectedTS
}

// BuildThreeStrikeView computes IndexOI's /nifty & /banknifty Three-Strike OI, Ladder, and Intraday Diff table.
func BuildThreeStrikeView(
	symbol, dateStr, replayTime string,
	baseStrike float64,
	intervalMins int,
	history []data.FOStrikeOISnapshot,
	availableDates []string,
) ThreeStrikeViewResponse {
	if intervalMins < 1 || intervalMins > 6 {
		intervalMins = 3
	}
	history = filterMarketSessionSnapshots(history)
	resp := ThreeStrikeViewResponse{
		Symbol:         symbol,
		Date:           dateStr,
		ReplayTime:     replayTime,
		AvailableDates: availableDates,
		IntervalMins:   intervalMins,
	}
	if len(history) == 0 {
		return resp
	}

	times, tsMap := groupSnapshotsByTimestamp(history)
	availTimes, selectedTS := buildOneMinuteReplayTimes(times, replayTime)
	resp.AvailableTimes = availTimes
	resp.ReplayTime = selectedTS.Format("15:04")
	resp.LastSyncIST = selectedTS.Format("2006-01-02 15:04:05 IST")

	curRows := tsMap[selectedTS.Unix()]
	if len(curRows) == 0 {
		return resp
	}
	sort.Slice(curRows, func(i, j int) bool { return curRows[i].StrikePrice < curRows[j].StrikePrice })

	spot := curRows[0].SpotPrice
	if spot <= 0 {
		// Estimate spot from ATM strike (offset == 0)
		for _, r := range curRows {
			if r.ATMOffset == 0 {
				spot = r.StrikePrice
				break
			}
		}
	}
	resp.SpotPrice = math.Round(spot*100) / 100

	// Spot change vs first snapshot of day
	firstRows := tsMap[times[0].Unix()]
	if len(firstRows) > 0 && firstRows[0].SpotPrice > 0 && spot > 0 {
		resp.SpotChange = math.Round((spot-firstRows[0].SpotPrice)*100) / 100
		resp.SpotChangePct = math.Round((resp.SpotChange/firstRows[0].SpotPrice)*10000) / 100
	}

	allStrikes := make([]float64, 0, len(curRows))
	byStrike := make(map[float64]data.FOStrikeOISnapshot, len(curRows))
	var totalCallOI, totalPutOI int64
	var supportStrike, resistanceStrike float64
	maxPosNetOI := int64(-1)
	minNegNetOI := int64(1)
	for _, r := range curRows {
		allStrikes = append(allStrikes, r.StrikePrice)
		byStrike[r.StrikePrice] = r
		totalCallOI += r.CallOI
		totalPutOI += r.PutOI
		netOI := r.PutOI - r.CallOI
		if netOI > maxPosNetOI {
			maxPosNetOI = netOI
			supportStrike = r.StrikePrice
		}
		if netOI < minNegNetOI {
			minNegNetOI = netOI
			resistanceStrike = r.StrikePrice
		}
	}
	if totalCallOI > 0 {
		resp.PCR = math.Round((float64(totalPutOI)/float64(totalCallOI))*100) / 100
	}
	resp.SupportStrike = supportStrike
	resp.ResistanceStrike = resistanceStrike

	step := StrikeStepForSymbol(symbol, allStrikes)
	resp.StrikeStep = step

	// Base options (every 100 pts or 2*step across available strikes with room for 3 consecutive strikes)
	baseStep := step * 2
	if step >= 100 {
		baseStep = step
	}
	var baseOpts []float64
	for i := 0; i+2 < len(allStrikes); i++ {
		st := allStrikes[i]
		if math.Mod(st, baseStep) == 0 {
			baseOpts = append(baseOpts, st)
		}
	}
	if len(baseOpts) == 0 && len(allStrikes) >= 3 {
		baseOpts = allStrikes[:len(allStrikes)-2]
	}
	resp.BaseOptions = baseOpts

	if baseStrike <= 0 || byStrike[baseStrike].StrikePrice == 0 {
		atm := RoundToNearestStrike(spot, step)
		target := atm - step
		best := allStrikes[0]
		bestDist := math.Abs(best - target)
		for _, b := range baseOpts {
			if d := math.Abs(b - target); d < bestDist {
				bestDist = d
				best = b
			}
		}
		baseStrike = best
	}
	resp.BaseStrike = baseStrike

	threeStrikes := []float64{baseStrike, baseStrike + step, baseStrike + 2*step}
	for _, st := range threeStrikes {
		r := byStrike[st]
		diff := r.PutOIChange - r.CallOIChange
		resp.ThreeStrikeRows = append(resp.ThreeStrikeRows, ThreeStrikeRow{
			StrikePrice: st,
			CallOIChg:   r.CallOIChange,
			PutOIChg:    r.PutOIChange,
			DiffPC:      diff,
			Verdict:     ClassifyThreeStrikeVerdict(diff),
		})
		resp.TotalCallChg += r.CallOIChange
		resp.TotalPutChg += r.PutOIChange
	}
	resp.TotalDiffPC = resp.TotalPutChg - resp.TotalCallChg

	// Strike Ladder (+-6 strikes around spot)
	atmIdx := 0
	minD := math.Abs(allStrikes[0] - spot)
	for i, st := range allStrikes {
		if d := math.Abs(st - spot); d < minD {
			minD = d
			atmIdx = i
		}
	}
	lStart := atmIdx - 6
	if lStart < 0 {
		lStart = 0
	}
	lEnd := atmIdx + 6
	if lEnd >= len(allStrikes) {
		lEnd = len(allStrikes) - 1
	}

	ladder := make([]StrikeLadderRow, 0, lEnd-lStart+1)
	var putOIs, callOIs []int64
	for i := lStart; i <= lEnd; i++ {
		r := curRows[i]
		putOIs = append(putOIs, r.PutOI)
		callOIs = append(callOIs, r.CallOI)
		ladder = append(ladder, StrikeLadderRow{
			StrikePrice: r.StrikePrice,
			NetOIChange: r.PutOIChange - r.CallOIChange,
			NetOI:       r.PutOI - r.CallOI,
			CallVolume:  r.CallVolume,
			PutVolume:   r.PutVolume,
			IsSpotAbove: r.StrikePrice > spot,
		})
	}
	sort.Slice(putOIs, func(i, j int) bool { return putOIs[i] > putOIs[j] })
	sort.Slice(callOIs, func(i, j int) bool { return callOIs[i] > callOIs[j] })
	for idx := range ladder {
		r := byStrike[ladder[idx].StrikePrice]
		if len(putOIs) > 0 && r.PutOI == putOIs[0] && r.PutOI > 0 {
			ladder[idx].IsHighestPut = true
		} else if len(putOIs) > 1 && r.PutOI == putOIs[1] && r.PutOI > 0 {
			ladder[idx].IsSecondPut = true
		}
		if len(callOIs) > 0 && r.CallOI == callOIs[0] && r.CallOI > 0 {
			ladder[idx].IsHighestCall = true
		} else if len(callOIs) > 1 && r.CallOI == callOIs[1] && r.CallOI > 0 {
			ladder[idx].IsSecondCall = true
		}
	}
	resp.Ladder = ladder

	// Intraday Change in OI table sampled every intervalMins up to selectedTS
	var lastSampled time.Time
	var prevDiff int64
	hasPrev := false
	for _, t := range times {
		if t.After(selectedTS) {
			break
		}
		if !lastSampled.IsZero() && t.Sub(lastSampled) < time.Duration(intervalMins)*time.Minute-15*time.Second {
			continue
		}
		lastSampled = t
		rowsAtT := tsMap[t.Unix()]
		mAtT := make(map[float64]data.FOStrikeOISnapshot, len(rowsAtT))
		for _, r := range rowsAtT {
			mAtT[r.StrikePrice] = r
		}
		slices := make([]IntradayStrikeSlice, 0, 3)
		var sumCall, sumPut int64
		for _, st := range threeStrikes {
			r := mAtT[st]
			slices = append(slices, IntradayStrikeSlice{
				StrikePrice: st,
				CallOIChg:   r.CallOIChange,
				PutOIChg:    r.PutOIChange,
			})
			sumCall += r.CallOIChange
			sumPut += r.PutOIChange
		}
		diff := sumPut - sumCall
		var diffChg int64
		revSig := ""
		if hasPrev {
			diffChg = diff - prevDiff
			swingThresh := int64(250000)
			if strings.Contains(strings.ToUpper(symbol), "BANK") {
				swingThresh = 120000
			}
			if diffChg >= swingThresh {
				revSig = "BULLISH"
			} else if diffChg <= -swingThresh {
				revSig = "BEARISH"
			}
		}
		prevDiff = diff
		hasPrev = true

		spotT := 0.0
		if len(rowsAtT) > 0 {
			spotT = math.Round(rowsAtT[0].SpotPrice*100) / 100
		}
		resp.IntradayRows = append(resp.IntradayRows, IntradayOIChangeRow{
			Time:               t.Format("15:04"),
			SpotPrice:          spotT,
			Strikes:            slices,
			CallOIChange:       sumCall,
			PutOIChange:        sumPut,
			Difference:         diff,
			ChangeInDifference: diffChg,
			RevSignal:          revSig,
		})
	}
	// Reverse intraday rows so newest timestamp appears first (matching IndexOI)
	for i, j := 0, len(resp.IntradayRows)-1; i < j; i, j = i+1, j-1 {
		resp.IntradayRows[i], resp.IntradayRows[j] = resp.IntradayRows[j], resp.IntradayRows[i]
	}

	return resp
}

// BuildOptionChainAView computes the A NIFTY / A BANKNIFTY / Stocks OI lot-normalized chain, IV, Delta, GEX, Flip, and Walls.
func BuildOptionChainAView(
	symbol, dateStr, replayTime string,
	isStock bool,
	sortDesc bool,
	history []data.FOStrikeOISnapshot,
	availableDates []string,
) OptionChainAViewResponse {
	history = filterMarketSessionSnapshots(history)
	resp := OptionChainAViewResponse{
		Symbol:         symbol,
		IsStock:        isStock,
		Date:           dateStr,
		AvailableDates: availableDates,
	}
	if len(history) == 0 {
		return resp
	}

	times, tsMap := groupSnapshotsByTimestamp(history)
	availTimes, selectedTS := buildOneMinuteReplayTimes(times, replayTime)
	resp.AvailableTimes = availTimes
	resp.SnapshotTime = selectedTS.Format("15:04")

	curRows := tsMap[selectedTS.Unix()]
	if len(curRows) == 0 {
		return resp
	}
	sort.Slice(curRows, func(i, j int) bool { return curRows[i].StrikePrice < curRows[j].StrikePrice })

	lotSize := curRows[0].LotSize
	if lotSize <= 0 {
		lotSize = DefaultLotSize(symbol)
	}
	resp.LotSize = lotSize
	resp.ExpiryDate = curRows[0].ExpiryDate.Format("02 Jan 2006")

	spot := curRows[0].SpotPrice
	fut := curRows[0].FuturesPrice
	if isStock {
		if fut > 0 {
			spot = fut
		}
		resp.SpotNote = "Lot " + itoaSafe(lotSize) + " · future stands in for spot · Reversal not calibrated for single stocks"
	}
	resp.SpotPrice = math.Round(spot*100) / 100
	resp.FuturesPrice = math.Round(fut*100) / 100

	allStrikes := make([]float64, 0, len(curRows))
	for _, r := range curRows {
		allStrikes = append(allStrikes, r.StrikePrice)
	}
	step := StrikeStepForSymbol(symbol, allStrikes)
	atmStrike := RoundToNearestStrike(spot, step)
	resp.ATMStrike = atmStrike

	// Synthetic future F = ATM + CallLTP - PutLTP if FuturesPrice == 0
	for _, r := range curRows {
		if r.StrikePrice == atmStrike && (r.CallLTP > 0 || r.PutLTP > 0) {
			if resp.FuturesPrice <= 0 {
				resp.FuturesPrice = math.Round((atmStrike+r.CallLTP-r.PutLTP)*100) / 100
			}
			break
		}
	}
	if resp.FuturesPrice > 0 && resp.SpotPrice > 0 {
		resp.FMinusS = math.Round((resp.FuturesPrice-resp.SpotPrice)*100) / 100
	}

	dteDays := math.Max(1.0, curRows[0].ExpiryDate.Sub(selectedTS).Hours()/24.0)
	tYears := dteDays / 365.0
	rRate := 0.065

	var maxCOI, maxPOI, maxCChg, maxPChg, maxCVol, maxPVol int64
	var callWall, putWall float64
	var totCOI, totPOI, totCChg, totPChg int64
	var gexTotal float64
	var prevGEX float64
	var prevGEXStrike float64

	type rawComputed struct {
		row           data.FOStrikeOISnapshot
		cOI, pOI      int64
		cChg, pChg    int64
		cVol, pVol    int64
		cIV, pIV      float64
		cDelta, pDelt float64
		cTV, pTV      float64
		pcr           float64
	}
	computed := make([]rawComputed, 0, len(curRows))

	for _, r := range curRows {
		cOI := r.CallOI / int64(lotSize)
		pOI := r.PutOI / int64(lotSize)
		cChg := r.CallOIChange / int64(lotSize)
		pChg := r.PutOIChange / int64(lotSize)
		cVol := r.CallVolume / int64(lotSize)
		pVol := r.PutVolume / int64(lotSize)

		if cOI > maxCOI {
			maxCOI = cOI
			callWall = r.StrikePrice
		}
		if pOI > maxPOI {
			maxPOI = pOI
			putWall = r.StrikePrice
		}
		if abs64(cChg) > maxCChg {
			maxCChg = abs64(cChg)
		}
		if abs64(pChg) > maxPChg {
			maxPChg = abs64(pChg)
		}
		if cVol > maxCVol {
			maxCVol = cVol
		}
		if pVol > maxPVol {
			maxPVol = pVol
		}
		totCOI += cOI
		totPOI += pOI
		totCChg += cChg
		totPChg += pChg

		cIV := ImpliedVolatilityBisect(true, spot, r.StrikePrice, tYears, rRate, r.CallLTP)
		pIV := ImpliedVolatilityBisect(false, spot, r.StrikePrice, tYears, rRate, r.PutLTP)
		sigmaC := math.Max(cIV/100.0, 0.12)
		sigmaP := math.Max(pIV/100.0, 0.12)

		sqrtT := math.Sqrt(tYears)
		d1C := (math.Log(spot/r.StrikePrice) + (rRate+0.5*sigmaC*sigmaC)*tYears) / (sigmaC * sqrtT)
		d1P := (math.Log(spot/r.StrikePrice) + (rRate+0.5*sigmaP*sigmaP)*tYears) / (sigmaP * sqrtT)
		cDelta := math.Round(normCDF(d1C)*100) / 100
		pDelta := math.Round((normCDF(d1P)-1.0)*100) / 100

		gammaC := normPDF(d1C) / (spot * sigmaC * sqrtT)
		gammaP := normPDF(d1P) / (spot * sigmaP * sqrtT)
		strikeGEX := (float64(r.CallOI)*gammaC - float64(r.PutOI)*gammaP) * spot * spot * 0.01 / 1e7
		gexTotal += strikeGEX
		if prevGEXStrike > 0 && ((prevGEX < 0 && strikeGEX >= 0) || (prevGEX > 0 && strikeGEX <= 0)) {
			resp.FlipStrike = r.StrikePrice
		}
		prevGEX = strikeGEX
		prevGEXStrike = r.StrikePrice

		cIntr := math.Max(0, spot-r.StrikePrice)
		pIntr := math.Max(0, r.StrikePrice-spot)
		cTV := math.Round((r.CallLTP-cIntr)*100) / 100
		pTV := math.Round((r.PutLTP-pIntr)*100) / 100

		pcr := 0.0
		if cOI > 0 {
			pcr = math.Round((float64(pOI)/float64(cOI))*100) / 100
		}

		// Top summary aggregations
		if r.StrikePrice < spot {
			resp.CallSummary.TvItm += cTV
		}
		if r.StrikePrice > spot {
			resp.PutSummary.TvItm += pTV
		}
		resp.CallSummary.LTPChg += r.CallLTPChg
		resp.PutSummary.LTPChg += r.PutLTPChg
		resp.CallSummary.TotalDelta += cDelta * float64(cOI) / 100.0
		resp.PutSummary.TotalDelta += pDelta * float64(pOI) / 100.0

		computed = append(computed, rawComputed{
			row: r, cOI: cOI, pOI: pOI, cChg: cChg, pChg: pChg, cVol: cVol, pVol: pVol,
			cIV: cIV, pIV: pIV, cDelta: cDelta, pDelt: pDelta, cTV: cTV, pTV: pTV, pcr: pcr,
		})
	}

	resp.GEXCrore = math.Round(gexTotal*100) / 100
	resp.CallSummary.WallStrike = callWall
	resp.PutSummary.WallStrike = putWall
	resp.CallSummary.TvItm = math.Round(resp.CallSummary.TvItm)
	resp.PutSummary.TvItm = math.Round(resp.PutSummary.TvItm)
	resp.CallSummary.LTPChg = math.Round(resp.CallSummary.LTPChg)
	resp.PutSummary.LTPChg = math.Round(resp.PutSummary.LTPChg)
	resp.CallSummary.TotalDelta = math.Round(resp.CallSummary.TotalDelta*100) / 100
	resp.PutSummary.TotalDelta = math.Round(resp.PutSummary.TotalDelta*100) / 100
	if totCOI > 0 {
		resp.CallSummary.OIChgPct = math.Round((float64(totCChg)/float64(totCOI))*1000) / 10
	}
	if totPOI > 0 {
		resp.PutSummary.OIChgPct = math.Round((float64(totPChg)/float64(totPOI))*1000) / 10
	}

	if callWall == putWall && callWall > 0 {
		resp.WallStatus = "walls on same strike"
	} else {
		resp.WallStatus = "walls holding"
	}

	outRows := make([]OptionChainARow, 0, len(computed))
	for _, c := range computed {
		pct := func(v, mx int64) float64 {
			if mx <= 0 {
				return 0
			}
			return math.Round((float64(abs64(v))/float64(mx))*1000) / 10
		}
		outRows = append(outRows, OptionChainARow{
			StrikePrice: c.row.StrikePrice,
			PCR:         c.pcr,
			IsATM:       c.row.StrikePrice == atmStrike,
			IsCallWall:  c.row.StrikePrice == callWall,
			IsPutWall:   c.row.StrikePrice == putWall,
			Calls: OptionChainSideMetrics{
				IV: c.cIV, Delta: c.cDelta, OIChg: c.cChg, OIChgMaxPct: pct(c.cChg, maxCChg),
				OI: c.cOI, OIMaxPct: pct(c.cOI, maxCOI), Volume: c.cVol, VolMaxPct: pct(c.cVol, maxCVol),
				LTP: c.row.CallLTP, LTPChg: c.row.CallLTPChg, TimeValue: c.cTV,
			},
			Puts: OptionChainSideMetrics{
				IV: c.pIV, Delta: c.pDelt, OIChg: c.pChg, OIChgMaxPct: pct(c.pChg, maxPChg),
				OI: c.pOI, OIMaxPct: pct(c.pOI, maxPOI), Volume: c.pVol, VolMaxPct: pct(c.pVol, maxPVol),
				LTP: c.row.PutLTP, LTPChg: c.row.PutLTPChg, TimeValue: c.pTV,
			},
		})
	}

	if sortDesc {
		sort.Slice(outRows, func(i, j int) bool { return outRows[i].StrikePrice > outRows[j].StrikePrice })
	}
	resp.Rows = outRows
	return resp
}

// BuildOIPulseView computes minute-by-minute Spot + D% series and 20 15-minute checkpoints (09:30–14:15).
func BuildOIPulseView(symbol, dateStr string, history []data.FOStrikeOISnapshot, availableDates []string) OIPulseViewResponse {
	history = filterMarketSessionSnapshots(history)
	resp := OIPulseViewResponse{
		Symbol:         symbol,
		Date:           dateStr,
		AvailableDates: availableDates,
	}
	if len(history) == 0 {
		return resp
	}

	times, tsMap := groupSnapshotsByTimestamp(history)
	lotSize := int64(DefaultLotSize(symbol))
	if history[0].LotSize > 0 {
		lotSize = int64(history[0].LotSize)
	}

	spotByHHMM := make(map[string]float64, len(times))
	pointByHHMM := make(map[string]OIPulsePoint, len(times))

	for _, t := range times {
		rows := tsMap[t.Unix()]
		if len(rows) == 0 {
			continue
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].StrikePrice < rows[j].StrikePrice })
		spot := rows[0].SpotPrice
		atmIdx := 0
		minD := math.Abs(rows[0].StrikePrice - spot)
		for i, r := range rows {
			if d := math.Abs(r.StrikePrice - spot); d < minD {
				minD = d
				atmIdx = i
			}
		}
		sIdx := atmIdx - 5
		if sIdx < 0 {
			sIdx = 0
		}
		eIdx := atmIdx + 5
		if eIdx >= len(rows) {
			eIdx = len(rows) - 1
		}
		var sumC, sumP int64
		for i := sIdx; i <= eIdx; i++ {
			sumC += rows[i].CallOIChange / lotSize
			sumP += rows[i].PutOIChange / lotSize
		}
		diffPC, diffPct, _ := ComputeOIPulseDiffPct(sumC, sumP)
		hhmm := t.Format("15:04")
		pt := OIPulsePoint{
			Time:      hhmm,
			SpotPrice: math.Round(spot*100) / 100,
			DiffPct:   diffPct,
			DiffPC:    diffPC,
		}
		resp.SpotSeries = append(resp.SpotSeries, pt)
		spotByHHMM[hhmm] = pt.SpotPrice
		pointByHHMM[hhmm] = pt
	}

	// Build 20 checkpoints: 09:30, 09:45, ..., 14:15
	startCP, _ := time.Parse("15:04", "09:30")
	for i := 0; i < 20; i++ {
		cpT := startCP.Add(time.Duration(i*15) * time.Minute)
		cpStr := cpT.Format("15:04")
		laterStr := cpT.Add(60 * time.Minute).Format("15:04")

		// Find nearest snapshot <= cpStr
		var matchedTS time.Time
		for _, t := range times {
			if t.Format("15:04") <= cpStr {
				matchedTS = t
			}
		}
		if matchedTS.IsZero() {
			continue
		}
		rows := tsMap[matchedTS.Unix()]
		sort.Slice(rows, func(i, j int) bool { return rows[i].StrikePrice < rows[j].StrikePrice })
		spot := rows[0].SpotPrice
		atmIdx := 0
		minD := math.Abs(rows[0].StrikePrice - spot)
		for idx, r := range rows {
			if d := math.Abs(r.StrikePrice - spot); d < minD {
				minD = d
				atmIdx = idx
			}
		}
		sIdx := atmIdx - 5
		if sIdx < 0 {
			sIdx = 0
		}
		eIdx := atmIdx + 5
		if eIdx >= len(rows) {
			eIdx = len(rows) - 1
		}
		var sumC, sumP int64
		for idx := sIdx; idx <= eIdx; idx++ {
			sumC += rows[idx].CallOIChange / lotSize
			sumP += rows[idx].PutOIChange / lotSize
		}
		diffPC, diffPct, sig := ComputeOIPulseDiffPct(sumC, sumP)

		var spot1H, chg1H float64
		for _, t := range times {
			if t.Format("15:04") >= laterStr {
				if sVal, ok := spotByHHMM[t.Format("15:04")]; ok && sVal > 0 {
					spot1H = sVal
					chg1H = math.Round((sVal-spot)*100) / 100
				}
				break
			}
		}

		resp.Checkpoints = append(resp.Checkpoints, data.OIPulseCheckpoint{
			Symbol:             symbol,
			TradeDate:          dateStr,
			CheckpointTime:     cpStr,
			SpotPrice:          math.Round(spot*100) / 100,
			ATMStrike:          rows[atmIdx].StrikePrice,
			CallOIChgContracts: sumC,
			PutOIChgContracts:  sumP,
			DiffPC:             diffPC,
			DiffPct:            diffPct,
			Signal:             sig,
			Spot1HLater:        spot1H,
			Spot1HChg:          chg1H,
		})
	}

	return resp
}

// BuildStraddleView computes ATM/strike Straddle, VWAP, Range to Expiry, Max Pain, and 15m velocities.
func BuildStraddleView(
	symbol, dateStr string,
	selectedStrike float64,
	history []data.FOStrikeOISnapshot,
	availableDates []string,
	fiiDII []data.FIIDIIParticipantRecord,
) StraddleViewResponse {
	history = filterMarketSessionSnapshots(history)
	resp := StraddleViewResponse{
		Symbol:             symbol,
		Date:               dateStr,
		AvailableDates:     availableDates,
		FIIDIIParticipants: fiiDII,
		ExpiryWatchStatus:  "Normal",
	}
	if len(history) == 0 {
		return resp
	}

	times, tsMap := groupSnapshotsByTimestamp(history)
	latestTS := times[len(times)-1]
	curRows := tsMap[latestTS.Unix()]
	sort.Slice(curRows, func(i, j int) bool { return curRows[i].StrikePrice < curRows[j].StrikePrice })

	spot := curRows[0].SpotPrice
	resp.SpotPrice = math.Round(spot*100) / 100
	resp.ExpiryDate = curRows[0].ExpiryDate.Format("02 Jan")
	resp.DaysToExpiry = int(math.Max(0, math.Ceil(curRows[0].ExpiryDate.Sub(latestTS).Hours()/24.0)))
	resp.TrackedStrikes = len(curRows)

	strikes := make([]float64, 0, len(curRows))
	for _, r := range curRows {
		strikes = append(strikes, r.StrikePrice)
	}
	resp.AvailableStrikes = strikes
	step := StrikeStepForSymbol(symbol, strikes)
	atmStrike := RoundToNearestStrike(spot, step)
	resp.ATMStrike = atmStrike
	if selectedStrike <= 0 {
		selectedStrike = atmStrike
	}
	resp.SelectedStrike = selectedStrike

	// Max Pain across tracked strikes
	bestPain := math.MaxFloat64
	for _, cand := range curRows {
		pain := 0.0
		for _, r := range curRows {
			if cand.StrikePrice > r.StrikePrice {
				pain += (cand.StrikePrice - r.StrikePrice) * float64(r.CallOI)
			} else if r.StrikePrice > cand.StrikePrice {
				pain += (r.StrikePrice - cand.StrikePrice) * float64(r.PutOI)
			}
		}
		if pain < bestPain {
			bestPain = pain
			resp.MaxPainStrike = cand.StrikePrice
		}
	}

	var totCChg, totPChg int64
	for _, r := range curRows {
		totCChg += r.CallOIChange
		totPChg += r.PutOIChange
	}
	resp.TotalCallOIChg = totCChg
	resp.TotalPutOIChg = totPChg

	// Intraday Straddle vs VWAP curve
	var cumPV, cumV float64
	var openStrad, highStrad, lowStrad float64
	var strad15mAgo, oi15mAgo float64
	var curTotalOI float64
	target15m := latestTS.Add(-15 * time.Minute)

	for _, t := range times {
		rows := tsMap[t.Unix()]
		var strad, volDelta float64
		var totOI float64
		for _, r := range rows {
			totOI += float64(r.CallOI + r.PutOI)
			if r.StrikePrice == selectedStrike {
				strad = r.CallLTP + r.PutLTP
				volDelta = math.Max(1, float64(r.CallVolume+r.PutVolume))
			}
		}
		if strad <= 0 {
			continue
		}
		if openStrad == 0 {
			openStrad = strad
			highStrad = strad
			lowStrad = strad
		}
		if strad > highStrad {
			highStrad = strad
		}
		if strad < lowStrad {
			lowStrad = strad
		}
		cumPV += strad * volDelta
		cumV += volDelta
		vwap := strad
		if cumV > 0 {
			vwap = cumPV / cumV
		}
		resp.Series = append(resp.Series, StraddlePoint{
			Time:          t.Format("15:04"),
			StraddlePrice: math.Round(strad*100) / 100,
			VWAP:          math.Round(vwap*100) / 100,
		})
		if !t.After(target15m) {
			strad15mAgo = strad
			oi15mAgo = totOI
		}
		curTotalOI = totOI
	}

	if n := len(resp.Series); n > 0 {
		last := resp.Series[n-1]
		resp.StraddlePrice = last.StraddlePrice
		resp.StraddleVWAP = last.VWAP
		resp.StraddleOpen = math.Round(openStrad*100) / 100
		resp.StraddleHigh = math.Round(highStrad*100) / 100
		resp.StraddleLow = math.Round(lowStrad*100) / 100
		if openStrad > 0 {
			resp.StraddleChg = math.Round((last.StraddlePrice-openStrad)*100) / 100
			resp.StraddleChgPct = math.Round((resp.StraddleChg/openStrad)*10000) / 100
		}
		if last.StraddlePrice < last.VWAP {
			resp.VWAPStatus = "Below VWAP — premium being sold down"
		} else {
			resp.VWAPStatus = "Above VWAP — option buyers active"
		}
		resp.RangeLow = math.Round(spot - last.StraddlePrice)
		resp.RangeHigh = math.Round(spot + last.StraddlePrice)
		if strad15mAgo > 0 {
			resp.Straddle15mPct = math.Round(((last.StraddlePrice-strad15mAgo)/strad15mAgo)*10000) / 100
		}
		if oi15mAgo > 0 {
			resp.ChainOI15mPct = math.Round(((curTotalOI-oi15mAgo)/oi15mAgo)*10000) / 100
		}
	}

	return resp
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func itoaSafe(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
