package risk

import (
	"math"
	"sync"
	"testing"
)

func TestEquityIntradayZerodhaCharges(t *testing.T) {
	cfg := DefaultZerodhaChargesConfig()

	// Test Case A: 400 shares, Buy @ 1000, Sell @ 1010
	breakdown := CalculateEquityIntradayCharges(cfg, 1000.0, 1010.0, 400)

	if breakdown.BuyTurnover != 400000.0 {
		t.Errorf("expected buy turnover 400000.0, got %v", breakdown.BuyTurnover)
	}
	if breakdown.SellTurnover != 404000.0 {
		t.Errorf("expected sell turnover 404000.0, got %v", breakdown.SellTurnover)
	}
	if breakdown.TotalTurnover != 804000.0 {
		t.Errorf("expected total turnover 804000.0, got %v", breakdown.TotalTurnover)
	}
	// Brokerage is capped at ₹20 per leg
	if breakdown.BuyBrokerage != 20.0 || breakdown.SellBrokerage != 20.0 || breakdown.TotalBrokerage != 40.0 {
		t.Errorf("expected total brokerage 40.0, got %v (buy: %v, sell: %v)", breakdown.TotalBrokerage, breakdown.BuyBrokerage, breakdown.SellBrokerage)
	}
	// STT = 0.025% of 404,000 = 101.0
	if breakdown.STT != 101.0 {
		t.Errorf("expected STT 101.0, got %v", breakdown.STT)
	}
	// Exchange Txn = 0.00307% of 804,000 = 24.68
	if math.Abs(breakdown.ExchangeTxn-24.68) > 0.02 {
		t.Errorf("expected exchange txn 24.68, got %v", breakdown.ExchangeTxn)
	}
	// SEBI Charges = ₹10 / cr on 804,000 = 0.80
	if math.Abs(breakdown.SEBICharges-0.80) > 0.01 {
		t.Errorf("expected SEBI charges 0.80, got %v", breakdown.SEBICharges)
	}
	// Stamp Duty = 0.003% of 400,000 = 12.0
	if breakdown.StampDuty != 12.0 {
		t.Errorf("expected stamp duty 12.0, got %v", breakdown.StampDuty)
	}
	// GST = 18% of (40.0 + 24.68 + 0.80) = 18% of 65.48 = 11.79
	if math.Abs(breakdown.GST-11.79) > 0.02 {
		t.Errorf("expected GST 11.79, got %v", breakdown.GST)
	}
	// Total Charges ~ 190.27
	if math.Abs(breakdown.TotalCharges-190.27) > 0.05 {
		t.Errorf("expected total charges ~190.27, got %v", breakdown.TotalCharges)
	}
	// Gross PnL = (1010 - 1000) * 400 = 4000
	if breakdown.GrossPnL != 4000.0 {
		t.Errorf("expected gross pnl 4000.0, got %v", breakdown.GrossPnL)
	}
	// Net PnL = 4000 - 190.27 = 3809.73
	if math.Abs(breakdown.NetPnL-3809.73) > 0.05 {
		t.Errorf("expected net pnl ~3809.73, got %v", breakdown.NetPnL)
	}
	// Breakeven Points ~ 0.48
	if math.Abs(breakdown.BreakevenPoints-0.48) > 0.02 {
		t.Errorf("expected breakeven points ~0.48, got %v", breakdown.BreakevenPoints)
	}
}

func TestEquityIntradaySmallTradePercentageBrokerage(t *testing.T) {
	cfg := DefaultZerodhaChargesConfig()

	// Small trade: 10 shares @ 100 buy, 105 sell
	// Buy turnover = 1000. 0.03% = 0.30 (much less than 20)
	// Sell turnover = 1050. 0.03% = 0.315 ~ 0.32
	breakdown := CalculateEquityIntradayCharges(cfg, 100.0, 105.0, 10)

	if breakdown.BuyBrokerage != 0.30 {
		t.Errorf("expected buy brokerage 0.30, got %v", breakdown.BuyBrokerage)
	}
	if breakdown.SellBrokerage != 0.32 {
		t.Errorf("expected sell brokerage 0.32, got %v", breakdown.SellBrokerage)
	}
	if breakdown.TotalBrokerage != 0.62 {
		t.Errorf("expected total brokerage 0.62, got %v", breakdown.TotalBrokerage)
	}
}

func TestOptionsZerodhaCharges(t *testing.T) {
	cfg := DefaultZerodhaChargesConfig()

	// Test Case B: 1 lot (75 qty) @ 100 buy, 150 sell
	breakdown := CalculateOptionsCharges(cfg, 100.0, 150.0, 75)

	if breakdown.BuyTurnover != 7500.0 {
		t.Errorf("expected buy turnover 7500.0, got %v", breakdown.BuyTurnover)
	}
	if breakdown.SellTurnover != 11250.0 {
		t.Errorf("expected sell turnover 11250.0, got %v", breakdown.SellTurnover)
	}
	if breakdown.TotalTurnover != 18750.0 {
		t.Errorf("expected total turnover 18750.0, got %v", breakdown.TotalTurnover)
	}
	// Flat brokerage ₹20 per leg = ₹40 total
	if breakdown.TotalBrokerage != 40.0 {
		t.Errorf("expected total brokerage 40.0, got %v", breakdown.TotalBrokerage)
	}
	// STT = 0.10% of 11,250 = 11.25
	if breakdown.STT != 11.25 {
		t.Errorf("expected STT 11.25, got %v", breakdown.STT)
	}
	// Exchange Txn = 0.03553% of 18,750 = 6.66
	if math.Abs(breakdown.ExchangeTxn-6.66) > 0.02 {
		t.Errorf("expected exchange txn 6.66, got %v", breakdown.ExchangeTxn)
	}
	// SEBI Charges = ₹10 / cr of 18,750 = 0.02
	if math.Abs(breakdown.SEBICharges-0.02) > 0.01 {
		t.Errorf("expected SEBI charges 0.02, got %v", breakdown.SEBICharges)
	}
	// Stamp Duty = 0.003% of 7,500 = 0.23
	if math.Abs(breakdown.StampDuty-0.23) > 0.01 {
		t.Errorf("expected stamp duty 0.23, got %v", breakdown.StampDuty)
	}
	// GST = 18% of (40 + 6.66 + 0.02) = 18% of 46.68 = 8.40
	if math.Abs(breakdown.GST-8.40) > 0.02 {
		t.Errorf("expected GST 8.40, got %v", breakdown.GST)
	}
	// Total Charges ~ 66.56
	if math.Abs(breakdown.TotalCharges-66.56) > 0.05 {
		t.Errorf("expected total charges ~66.56, got %v", breakdown.TotalCharges)
	}
	// Gross PnL = (150 - 100) * 75 = 3750.0
	if breakdown.GrossPnL != 3750.0 {
		t.Errorf("expected gross pnl 3750.0, got %v", breakdown.GrossPnL)
	}
	// Net PnL = 3750 - 66.56 = 3683.44
	if math.Abs(breakdown.NetPnL-3683.44) > 0.05 {
		t.Errorf("expected net pnl ~3683.44, got %v", breakdown.NetPnL)
	}
	// Breakeven Points ~ 0.89 premium points
	if math.Abs(breakdown.BreakevenPoints-0.89) > 0.02 {
		t.Errorf("expected breakeven points ~0.89, got %v", breakdown.BreakevenPoints)
	}
}

func TestDynamicBreakevenBuffer(t *testing.T) {
	cfg := DefaultZerodhaChargesConfig()

	// For equity 500 shares @ 2000
	points, pct := CalculateDynamicBreakevenBuffer(cfg, 2000.0, 500, SegmentEquityIntraday)
	if points <= 0 || pct <= 0 {
		t.Errorf("expected positive breakeven points and percentage, got points=%v, pct=%v", points, pct)
	}

	// For options 75 qty @ 120
	optPoints, optPct := CalculateDynamicBreakevenBuffer(cfg, 120.0, 75, SegmentOptions)
	if optPoints <= 0 || optPct <= 0 {
		t.Errorf("expected positive breakeven points and percentage for options, got points=%v, pct=%v", optPoints, optPct)
	}
}

func TestChargesConcurrencySafety(t *testing.T) {
	cfg := DefaultZerodhaChargesConfig()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			price := float64(500 + idx*10)
			res1 := CalculateEquityIntradayCharges(cfg, price, price+5, 100)
			res2 := CalculateOptionsCharges(cfg, 50.0, 60.0, 75)
			if res1.TotalCharges <= 0 || res2.TotalCharges <= 0 {
				t.Errorf("concurrency calculation error on iteration %d", idx)
			}
		}(i)
	}
	wg.Wait()
}
