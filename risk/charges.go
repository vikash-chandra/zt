package risk

import (
	"math"
)

// Segment represents the market segment for trading
type Segment string

const (
	SegmentEquityIntraday Segment = "EQUITY_INTRADAY"
	SegmentEquityDelivery Segment = "EQUITY_DELIVERY"
	SegmentOptions        Segment = "OPTIONS"
	SegmentFutures        Segment = "FUTURES"
)

// ChargesConfig holds the fee and tax schedule for brokerage calculations matching Zerodha
type ChargesConfig struct {
	// Equity Intraday
	EquityIntradayBrokeragePct float64 `json:"equity_intraday_brokerage_pct"` // 0.03%
	EquityIntradayBrokerageMax float64 `json:"equity_intraday_brokerage_max"` // ₹20.0
	EquityIntradaySTTSellPct   float64 `json:"equity_intraday_stt_sell_pct"`   // 0.025%
	EquityIntradayExchangePct  float64 `json:"equity_intraday_exchange_pct"`  // 0.00307% (NSE)
	EquityIntradayStampBuyPct  float64 `json:"equity_intraday_stamp_buy_pct"`  // 0.003%

	// Options (NFO)
	OptionsBrokerageFlatPerOrder float64 `json:"options_brokerage_flat_per_order"` // ₹20.0
	OptionsSTTSellPct            float64 `json:"options_stt_sell_pct"`             // 0.10% (Oct 2024 revision)
	OptionsExchangePct           float64 `json:"options_exchange_pct"`            // 0.03553% (NSE)
	OptionsStampBuyPct           float64 `json:"options_stamp_buy_pct"`            // 0.003%

	// Common Levies
	SEBITurnoverPerCrore float64 `json:"sebi_turnover_per_crore"` // ₹10.0 per crore (0.0001%)
	GSTRatePct           float64 `json:"gst_rate_pct"`            // 18.0%
}

// DefaultZerodhaChargesConfig returns official Zerodha charges and statutory tax rates
func DefaultZerodhaChargesConfig() ChargesConfig {
	return ChargesConfig{
		// Equity Intraday (MIS)
		EquityIntradayBrokeragePct: 0.03,
		EquityIntradayBrokerageMax: 20.0,
		EquityIntradaySTTSellPct:   0.025,
		EquityIntradayExchangePct:  0.00307,
		EquityIntradayStampBuyPct:  0.003,

		// Equity Options (NFO)
		OptionsBrokerageFlatPerOrder: 20.0,
		OptionsSTTSellPct:            0.10,
		OptionsExchangePct:           0.03553,
		OptionsStampBuyPct:           0.003,

		// Common Levies
		SEBITurnoverPerCrore: 10.0, // ₹10 per crore (0.0001%)
		GSTRatePct:           18.0,
	}
}

// TradeChargesBreakdown holds the granular itemized fee breakdown for an executed round-trip trade
type TradeChargesBreakdown struct {
	Segment          Segment `json:"segment"`
	BuyTurnover      float64 `json:"buy_turnover"`
	SellTurnover     float64 `json:"sell_turnover"`
	TotalTurnover    float64 `json:"total_turnover"`
	BuyBrokerage     float64 `json:"buy_brokerage"`
	SellBrokerage    float64 `json:"sell_brokerage"`
	TotalBrokerage   float64 `json:"total_brokerage"`
	STT              float64 `json:"stt"`
	ExchangeTxn      float64 `json:"exchange_txn"`
	SEBICharges      float64 `json:"sebi_charges"`
	StampDuty        float64 `json:"stamp_duty"`
	GST              float64 `json:"gst"`
	TotalCharges     float64 `json:"total_charges"`
	GrossPnL         float64 `json:"gross_pnl"`
	NetPnL           float64 `json:"net_pnl"`
	BreakevenPoints  float64 `json:"breakeven_points"`   // Per-share price movement needed to breakeven
	BreakevenPct     float64 `json:"breakeven_pct"`      // Breakeven percentage of entry price
}

// round2 rounds a float to 2 decimal places (standard currency/tax precision) with IEEE 754 epsilon guard
func round2(val float64) float64 {
	if val >= 0 {
		return math.Round((val+1e-9)*100.0) / 100.0
	}
	return math.Round((val-1e-9)*100.0) / 100.0
}

// CalculateEquityIntradayCharges computes exact Zerodha brokerage, taxes, and P&L for an intraday equity trade
func CalculateEquityIntradayCharges(cfg ChargesConfig, buyPrice, sellPrice float64, qty int) TradeChargesBreakdown {
	if qty <= 0 || buyPrice <= 0 || sellPrice <= 0 {
		return TradeChargesBreakdown{Segment: SegmentEquityIntraday}
	}

	buyTurnover := buyPrice * float64(qty)
	sellTurnover := sellPrice * float64(qty)
	totalTurnover := buyTurnover + sellTurnover

	// 1. Brokerage: min(20, turnover * 0.03%) on both buy and sell legs
	buyBrok := math.Min(cfg.EquityIntradayBrokerageMax, buyTurnover*(cfg.EquityIntradayBrokeragePct/100.0))
	sellBrok := math.Min(cfg.EquityIntradayBrokerageMax, sellTurnover*(cfg.EquityIntradayBrokeragePct/100.0))
	buyBrok = round2(buyBrok)
	sellBrok = round2(sellBrok)
	totalBrok := buyBrok + sellBrok

	// 2. STT: 0.025% on sell side only (standard Zerodha rule: rounded to nearest rupee on total note, or 2 decimals)
	stt := round2(sellTurnover * (cfg.EquityIntradaySTTSellPct / 100.0))

	// 3. Exchange Transaction Charges: 0.00307% on total turnover (NSE)
	exchangeTxn := round2(totalTurnover * (cfg.EquityIntradayExchangePct / 100.0))

	// 4. SEBI Turnover Charges: ₹10 per crore (0.0001% of total turnover)
	sebiCharges := round2(totalTurnover * (cfg.SEBITurnoverPerCrore / 10000000.0))

	// 5. Stamp Duty: 0.003% on buy side only
	stampDuty := round2(buyTurnover * (cfg.EquityIntradayStampBuyPct / 100.0))

	// 6. GST: 18% of (Brokerage + Exchange Txn + SEBI)
	gstBase := totalBrok + exchangeTxn + sebiCharges
	gst := round2(gstBase * (cfg.GSTRatePct / 100.0))

	// 7. Total Taxes and Charges
	totalCharges := round2(totalBrok + stt + exchangeTxn + sebiCharges + stampDuty + gst)

	// 8. Gross and Net P&L
	grossPnL := round2((sellPrice - buyPrice) * float64(qty))
	netPnL := round2(grossPnL - totalCharges)

	// 9. Breakeven metrics
	breakevenPoints := 0.0
	breakevenPct := 0.0
	if qty > 0 {
		breakevenPoints = round2((totalCharges / float64(qty)) * 100.0) / 100.0
		if buyPrice > 0 {
			breakevenPct = round2((breakevenPoints / buyPrice) * 10000.0) / 100.0
		}
	}

	return TradeChargesBreakdown{
		Segment:         SegmentEquityIntraday,
		BuyTurnover:     round2(buyTurnover),
		SellTurnover:    round2(sellTurnover),
		TotalTurnover:   round2(totalTurnover),
		BuyBrokerage:    buyBrok,
		SellBrokerage:   sellBrok,
		TotalBrokerage:  totalBrok,
		STT:             stt,
		ExchangeTxn:     exchangeTxn,
		SEBICharges:     sebiCharges,
		StampDuty:       stampDuty,
		GST:             gst,
		TotalCharges:    totalCharges,
		GrossPnL:        grossPnL,
		NetPnL:          netPnL,
		BreakevenPoints: breakevenPoints,
		BreakevenPct:    breakevenPct,
	}
}

// CalculateOptionsCharges computes exact Zerodha brokerage, taxes, and P&L for an options trade
func CalculateOptionsCharges(cfg ChargesConfig, buyPremium, sellPremium float64, qty int) TradeChargesBreakdown {
	if qty <= 0 || buyPremium <= 0 || sellPremium <= 0 {
		return TradeChargesBreakdown{Segment: SegmentOptions}
	}

	buyTurnover := buyPremium * float64(qty)
	sellTurnover := sellPremium * float64(qty)
	totalTurnover := buyTurnover + sellTurnover

	// 1. Brokerage: Flat ₹20 per executed order (buy & sell)
	buyBrok := cfg.OptionsBrokerageFlatPerOrder
	sellBrok := cfg.OptionsBrokerageFlatPerOrder
	totalBrok := buyBrok + sellBrok

	// 2. STT: 0.10% on sell premium only
	stt := round2(sellTurnover * (cfg.OptionsSTTSellPct / 100.0))

	// 3. Exchange Transaction Charges: 0.03553% on premium turnover (NSE)
	exchangeTxn := round2(totalTurnover * (cfg.OptionsExchangePct / 100.0))

	// 4. SEBI Turnover Charges: ₹10 per crore (0.0001% of total premium turnover)
	sebiCharges := round2(totalTurnover * (cfg.SEBITurnoverPerCrore / 10000000.0))

	// 5. Stamp Duty: 0.003% on buy premium turnover only
	stampDuty := round2(buyTurnover * (cfg.OptionsStampBuyPct / 100.0))

	// 6. GST: 18% of (Brokerage + Exchange Txn + SEBI)
	gstBase := totalBrok + exchangeTxn + sebiCharges
	gst := round2(gstBase * (cfg.GSTRatePct / 100.0))

	// 7. Total Charges
	totalCharges := round2(totalBrok + stt + exchangeTxn + sebiCharges + stampDuty + gst)

	// 8. Gross and Net P&L
	grossPnL := round2((sellPremium - buyPremium) * float64(qty))
	netPnL := round2(grossPnL - totalCharges)

	// 9. Breakeven metrics
	breakevenPoints := 0.0
	breakevenPct := 0.0
	if qty > 0 {
		breakevenPoints = round2((totalCharges / float64(qty)) * 100.0) / 100.0
		if buyPremium > 0 {
			breakevenPct = round2((breakevenPoints / buyPremium) * 10000.0) / 100.0
		}
	}

	return TradeChargesBreakdown{
		Segment:         SegmentOptions,
		BuyTurnover:     round2(buyTurnover),
		SellTurnover:    round2(sellTurnover),
		TotalTurnover:   round2(totalTurnover),
		BuyBrokerage:    buyBrok,
		SellBrokerage:   sellBrok,
		TotalBrokerage:  totalBrok,
		STT:             stt,
		ExchangeTxn:     exchangeTxn,
		SEBICharges:     sebiCharges,
		StampDuty:       stampDuty,
		GST:             gst,
		TotalCharges:    totalCharges,
		GrossPnL:        grossPnL,
		NetPnL:          netPnL,
		BreakevenPoints: breakevenPoints,
		BreakevenPct:    breakevenPct,
	}
}

// CalculateTradeCharges is a polymorphic dispatcher calculating charges based on Segment
func CalculateTradeCharges(cfg ChargesConfig, segment Segment, buyPrice, sellPrice float64, qty int) TradeChargesBreakdown {
	if segment == SegmentOptions {
		return CalculateOptionsCharges(cfg, buyPrice, sellPrice, qty)
	}
	return CalculateEquityIntradayCharges(cfg, buyPrice, sellPrice, qty)
}

// CalculateDynamicBreakevenBuffer calculates the exact price offset needed to cover 100% of Zerodha charges
// This is the ideal plug-in hook for RiskRewardCalculator cost SL trailing.
func CalculateDynamicBreakevenBuffer(cfg ChargesConfig, entryPrice float64, qty int, segment Segment) (breakevenPoints, breakevenPct float64) {
	if entryPrice <= 0 || qty <= 0 {
		return 0.0, 0.05 // Safe fallback to 0.05%
	}

	// 1st estimate assuming trade closes at entryPrice
	breakdown := CalculateTradeCharges(cfg, segment, entryPrice, entryPrice, qty)
	if breakdown.TotalCharges <= 0 {
		return 0.0, 0.05
	}

	pts := breakdown.TotalCharges / float64(qty)

	// 2nd pass refinement: calculate charges at entryPrice + pts to fully cover sell-side levies (STT, GST, etc.)
	refined := CalculateTradeCharges(cfg, segment, entryPrice, entryPrice+pts, qty)
	pts = refined.TotalCharges / float64(qty)

	pct := (pts / entryPrice) * 100.0

	// Use ceil to guarantee pnl at breakeven SL is >= 100% of charges
	ptsCeil := math.Ceil(pts*100.0) / 100.0

	return ptsCeil, round2(pct)
}
