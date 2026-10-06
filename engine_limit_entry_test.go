package main

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"zerodha-trading/config"
	"zerodha-trading/execution"
	"zerodha-trading/monitoring"
	"zerodha-trading/risk"
	"go.uber.org/zap"
)

// TestCalculatePlannedEntryPrice_EdgeCases validates all variations of entry limit calculations
func TestCalculatePlannedEntryPrice_EdgeCases(t *testing.T) {
	testLogger, _ := monitoring.NewLogger("info")

	tests := []struct {
		name              string
		defaultOrderType  string
		anchor            string
		offsetTicks       int
		limitBufPct       float64
		symbol            string
		action            string
		ltp               float64
		setupHigh         float64
		setupLow          float64
		expectedOrderType execution.OrderType
		expectedPrice     float64
	}{
		{
			name:              "Market Order ignores limit settings",
			defaultOrderType:  "MARKET",
			anchor:            "CONFIRMATION_CANDLE",
			offsetTicks:       -2,
			symbol:            "INFY",
			action:            "BUY",
			ltp:               1500.0,
			setupHigh:         1498.0,
			setupLow:          1480.0,
			expectedOrderType: execution.OrderTypeMarket,
			expectedPrice:     1500.0,
		},
		{
			name:              "Limit Order - Confirmation Candle - BUY - 0 offset (exact high)",
			defaultOrderType:  "LIMIT",
			anchor:            "CONFIRMATION_CANDLE",
			offsetTicks:       0,
			symbol:            "INFY",
			action:            "BUY",
			ltp:               1505.0,
			setupHigh:         1500.0,
			setupLow:          1480.0,
			expectedOrderType: execution.OrderTypeLimit,
			expectedPrice:     1500.0,
		},
		{
			name:              "Limit Order - Confirmation Candle - BUY - Negative offset (-2 ticks discount retest)",
			defaultOrderType:  "LIMIT",
			anchor:            "CONFIRMATION_CANDLE",
			offsetTicks:       -2,
			symbol:            "INFY",
			action:            "BUY",
			ltp:               1505.0,
			setupHigh:         1500.0,
			setupLow:          1480.0,
			expectedOrderType: execution.OrderTypeLimit,
			expectedPrice:     1499.90, // 1500.0 - 2 * 0.05
		},
		{
			name:              "Limit Order - Confirmation Candle - BUY - Positive offset (+2 ticks breakout buffer)",
			defaultOrderType:  "LIMIT",
			anchor:            "CONFIRMATION_CANDLE",
			offsetTicks:       2,
			symbol:            "INFY",
			action:            "BUY",
			ltp:               1505.0,
			setupHigh:         1500.0,
			setupLow:          1480.0,
			expectedOrderType: execution.OrderTypeLimit,
			expectedPrice:     1500.10, // 1500.0 + 2 * 0.05
		},
		{
			name:              "Limit Order - Confirmation Candle - SELL - 0 offset (exact low)",
			defaultOrderType:  "LIMIT",
			anchor:            "CONFIRMATION_CANDLE",
			offsetTicks:       0,
			symbol:            "INFY",
			action:            "SELL",
			ltp:               1475.0,
			setupHigh:         1500.0,
			setupLow:          1480.0,
			expectedOrderType: execution.OrderTypeLimit,
			expectedPrice:     1480.0,
		},
		{
			name:              "Limit Order - Confirmation Candle - SELL - Negative offset (-2 ticks bounce discount)",
			defaultOrderType:  "LIMIT",
			anchor:            "CONFIRMATION_CANDLE",
			offsetTicks:       -2,
			symbol:            "INFY",
			action:            "SELL",
			ltp:               1475.0,
			setupHigh:         1500.0,
			setupLow:          1480.0,
			expectedOrderType: execution.OrderTypeLimit,
			expectedPrice:     1480.10, // 1480.0 + 2 * 0.05
		},
		{
			name:              "Limit Order - Confirmation Candle - SELL - Positive offset (+3 ticks deeper breakout)",
			defaultOrderType:  "LIMIT",
			anchor:            "CONFIRMATION_CANDLE",
			offsetTicks:       3,
			symbol:            "INFY",
			action:            "SELL",
			ltp:               1475.0,
			setupHigh:         1500.0,
			setupLow:          1480.0,
			expectedOrderType: execution.OrderTypeLimit,
			expectedPrice:     1479.85, // 1480.0 - 3 * 0.05
		},
		{
			name:              "Limit Order - Confirmation Candle - Nil/Zero Setup Candle (fallback to LTP with buffer)",
			defaultOrderType:  "LIMIT",
			anchor:            "CONFIRMATION_CANDLE",
			offsetTicks:       -2,
			limitBufPct:       0.2,
			symbol:            "INFY",
			action:            "BUY",
			ltp:               1000.0,
			setupHigh:         0.0, // No setup candle high available!
			setupLow:          0.0,
			expectedOrderType: execution.OrderTypeLimit,
			expectedPrice:     1002.0, // 1000.0 * 1.002
		},
		{
			name:              "Limit Order - Confirmation Candle - Out of bounds price (>5% away) triggers safety fallback",
			defaultOrderType:  "LIMIT",
			anchor:            "CONFIRMATION_CANDLE",
			offsetTicks:       0,
			limitBufPct:       0.2,
			symbol:            "INFY",
			action:            "BUY",
			ltp:               1000.0,
			setupHigh:         800.0, // 20% away from LTP!
			setupLow:          780.0,
			expectedOrderType: execution.OrderTypeLimit,
			expectedPrice:     1002.0, // Clamps safely to LTP with buffer
		},
		{
			name:              "Limit Order - LTP Anchor - BUY with -4 ticks discount",
			defaultOrderType:  "LIMIT",
			anchor:            "LTP",
			offsetTicks:       -4,
			symbol:            "INFY",
			action:            "BUY",
			ltp:               2000.0,
			setupHigh:         1990.0,
			setupLow:          1950.0,
			expectedOrderType: execution.OrderTypeLimit,
			expectedPrice:     1999.80, // 2000.0 - 4 * 0.05
		},
		{
			name:              "Limit Order - LTP Anchor - SELL with +4 ticks discount",
			defaultOrderType:  "LIMIT",
			anchor:            "LTP",
			offsetTicks:       4,
			symbol:            "INFY",
			action:            "SELL",
			ltp:               2000.0,
			setupHigh:         2050.0,
			setupLow:          2010.0,
			expectedOrderType: execution.OrderTypeLimit,
			expectedPrice:     1999.80, // 2000.0 - 4 * 0.05
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bot := &TradingBot{
				cfg: &config.Settings{
					DefaultOrderType:      tt.defaultOrderType,
					EntryLimitAnchor:      tt.anchor,
					EntryLimitOffsetTicks: tt.offsetTicks,
					LimitBufferPct:        tt.limitBufPct,
				},
				logger: testLogger,
			}

			orderType, plannedPrice, limitPrice := bot.calculatePlannedEntryPrice(
				tt.symbol, tt.action, tt.ltp, tt.setupHigh, tt.setupLow,
			)

			if orderType != tt.expectedOrderType {
				t.Errorf("expected order type %v, got %v", tt.expectedOrderType, orderType)
			}

			if math.Abs(plannedPrice-tt.expectedPrice) > 0.001 {
				t.Errorf("expected plannedPrice %.2f, got %.2f", tt.expectedPrice, plannedPrice)
			}

			if tt.expectedOrderType == execution.OrderTypeLimit {
				if limitPrice == nil {
					t.Fatalf("expected non-nil limitPrice pointer")
				}
				if math.Abs(*limitPrice-tt.expectedPrice) > 0.001 {
					t.Errorf("expected *limitPrice %.2f, got %.2f", tt.expectedPrice, *limitPrice)
				}
			} else {
				if limitPrice != nil {
					t.Errorf("expected nil limitPrice for MARKET order, got %f", *limitPrice)
				}
			}
		})
	}
}

// TestEntryLimitOrder_TimeoutCancellationAndRestoration ensures that unfilled timed out orders
// do not consume the daily max trades limit.
func TestEntryLimitOrder_TimeoutCancellationAndRestoration(t *testing.T) {
	limits := risk.RiskLimits{
		MaxTradesPerDay: 2,
		MaxLossStreaks:  3,
	}
	rm := risk.NewRiskManager(nil, zap.NewNop(), 100000.0, limits)

	orderID := "test-limit-order-1"
	rm.AddOpenPosition(orderID, "RELIANCE", 12345, 10, 2500.0, "BUY", 2480.0, "EMAS5_BREAKOUT", 2540.0, time.Now())

	if !rm.HasOpenPosition("RELIANCE") {
		t.Fatalf("expected open position to exist for RELIANCE")
	}

	// 1 trade placed so far
	// Now simulate timeout cancellation where 0 shares were filled:
	rm.OnOrderClose(orderID, 0, 0)

	if rm.HasOpenPosition("RELIANCE") {
		t.Fatalf("expected open position to be cleared after cancellation")
	}

	// Because 0 shares were filled, tradestoday must have been decremented back to 0!
	// Now we verify CanPlaceOrder still succeeds for 2 future real trades
	if !rm.CanPlaceOrder(10, 2500.0) {
		t.Fatalf("expected CanPlaceOrder to allow new trade after unfilled order cancelled")
	}
}

// TestEntryLimitOrder_PartialFillTracking verifies that partially filled cancelled orders
// keep their position active with the updated quantity and broker SL.
func TestEntryLimitOrder_PartialFillTracking(t *testing.T) {
	limits := risk.RiskLimits{
		MaxTradesPerDay: 5,
		MaxLossStreaks:  3,
	}
	rm := risk.NewRiskManager(nil, zap.NewNop(), 100000.0, limits)

	orderID := "partial-order-1"
	rm.AddOpenPosition(orderID, "TCS", 99999, 100, 3500.0, "BUY", 3450.0, "VANDE_BHARAT", 3600.0, time.Now())

	// Simulate 35 shares filled before timeout cancellation
	filledQty := 35
	rm.UpdatePositionQuantity(orderID, filledQty)

	pos := rm.GetPosition(orderID)
	if pos == nil {
		t.Fatalf("expected position to exist")
	}
	if pos.Quantity != 35 {
		t.Fatalf("expected position quantity 35, got %d", pos.Quantity)
	}

	// Now position closes at target
	rm.OnOrderClose(orderID, 3600.0, 35)

	if rm.HasOpenPosition("TCS") {
		t.Fatalf("expected position to be closed")
	}

	metrics := rm.GetMetrics()
	if metrics["closed_trades"].(int) != 1 {
		t.Fatalf("expected 1 closed trade, got %v", metrics["closed_trades"])
	}
	expectedPnL := (3600.0 - 3500.0) * 35.0
	dailyPnL := metrics["daily_pnl"].(float64)
	if math.Abs(dailyPnL-expectedPnL) > 0.01 {
		t.Errorf("expected PnL %.2f, got %.2f", expectedPnL, dailyPnL)
	}
}

// TestEntryLimitOrder_RaceConditions verifies thread safety under high concurrency
func TestEntryLimitOrder_RaceConditions(t *testing.T) {
	limits := risk.RiskLimits{
		MaxTradesPerDay: 100,
		MaxLossStreaks:  10,
	}
	rm := risk.NewRiskManager(nil, zap.NewNop(), 500000.0, limits)

	var wg sync.WaitGroup
	numRoutines := 50

	for i := 0; i < numRoutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			oid := fmt.Sprintf("race-order-%d", idx)
			sym := fmt.Sprintf("SYM%d", idx%5)

			rm.AddOpenPosition(oid, sym, int64(idx), 10, 100.0, "BUY", 95.0, "EMAS5_BREAKOUT", 110.0, time.Now())
			rm.UpdatePositionQuantity(oid, 8)
			rm.UpdatePositionEntryPrice(oid, 99.80)
			_ = rm.GetPosition(oid)
			_ = rm.HasOpenPosition(sym)
			_ = rm.CanPlaceOrder(5, 100.0)

			if idx%2 == 0 {
				// Cancelled with 0 fills
				rm.OnOrderClose(oid, 0, 0)
			} else {
				// Filled and exited
				rm.OnOrderClose(oid, 105.0, 8)
			}
		}(i)
	}

	wg.Wait()
}

// TestCalculatePlannedEntryPrice_EntryLimitModes validates that EntryLimitMode
// properly dictates order type and pricing for DIRECT_LIMIT, RETEST_BAND, and MARKET.
func TestCalculatePlannedEntryPrice_EntryLimitModes(t *testing.T) {
	testLogger, _ := monitoring.NewLogger("info")

	tests := []struct {
		name              string
		mode              string
		defaultOrderType  string
		anchor            string
		offsetTicks       int
		action            string
		ltp               float64
		setupHigh         float64
		setupLow          float64
		expectedOrderType execution.OrderType
		expectedPrice     float64
	}{
		{
			name:              "DIRECT_LIMIT mode - BUY discount pullback -2 ticks",
			mode:              "DIRECT_LIMIT",
			defaultOrderType:  "MARKET", // mode takes precedence over defaultOrderType
			anchor:            "CONFIRMATION_CANDLE",
			offsetTicks:       -2,
			action:            "BUY",
			ltp:               1005.0,
			setupHigh:         1000.0,
			setupLow:          980.0,
			expectedOrderType: execution.OrderTypeLimit,
			expectedPrice:     999.90, // 1000.0 - 2 * 0.05
		},
		{
			name:              "RETEST_BAND mode - BUY discount pullback -4 ticks",
			mode:              "RETEST_BAND",
			defaultOrderType:  "MARKET",
			anchor:            "CONFIRMATION_CANDLE",
			offsetTicks:       -4,
			action:            "BUY",
			ltp:               1002.0,
			setupHigh:         1000.0,
			setupLow:          980.0,
			expectedOrderType: execution.OrderTypeLimit,
			expectedPrice:     999.80, // 1000.0 - 4 * 0.05
		},
		{
			name:              "RETEST_BAND mode - SELL bounce pullback -3 ticks",
			mode:              "RETEST_BAND",
			defaultOrderType:  "MARKET",
			anchor:            "CONFIRMATION_CANDLE",
			offsetTicks:       -3,
			action:            "SELL",
			ltp:               978.0,
			setupHigh:         1000.0,
			setupLow:          980.0,
			expectedOrderType: execution.OrderTypeLimit,
			expectedPrice:     980.15, // 980.0 + 3 * 0.05
		},
		{
			name:              "MARKET mode - ignores limit offsets",
			mode:              "MARKET",
			defaultOrderType:  "LIMIT",
			anchor:            "CONFIRMATION_CANDLE",
			offsetTicks:       -4,
			action:            "BUY",
			ltp:               1005.0,
			setupHigh:         1000.0,
			setupLow:          980.0,
			expectedOrderType: execution.OrderTypeMarket,
			expectedPrice:     1005.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bot := &TradingBot{
				cfg: &config.Settings{
					EntryLimitMode:        tt.mode,
					DefaultOrderType:      tt.defaultOrderType,
					EntryLimitAnchor:      tt.anchor,
					EntryLimitOffsetTicks: tt.offsetTicks,
				},
				logger: testLogger,
			}

			orderType, plannedPrice, limitPrice := bot.calculatePlannedEntryPrice("TEST", tt.action, tt.ltp, tt.setupHigh, tt.setupLow)

			if orderType != tt.expectedOrderType {
				t.Fatalf("expected order type %v, got %v", tt.expectedOrderType, orderType)
			}
			if math.Abs(plannedPrice-tt.expectedPrice) > 0.001 {
				t.Errorf("expected plannedPrice %.2f, got %.2f", tt.expectedPrice, plannedPrice)
			}
			if tt.expectedOrderType == execution.OrderTypeLimit {
				if limitPrice == nil {
					t.Fatalf("expected non-nil limitPrice")
				}
				if math.Abs(*limitPrice-tt.expectedPrice) > 0.001 {
					t.Errorf("expected *limitPrice %.2f, got %.2f", tt.expectedPrice, *limitPrice)
				}
			} else {
				if limitPrice != nil {
					t.Errorf("expected nil limitPrice for MARKET mode")
				}
			}
		})
	}
}

// TestRetestBand_MaxChaseCeiling verifies that RETEST_BAND mode enforces the max chase ceiling
// to prevent chasing runaway momentum, while DIRECT_LIMIT and MARKET do not reject trades.
func TestRetestBand_MaxChaseCeiling(t *testing.T) {
	tickSize := 0.05
	setupHigh := 1000.0
	setupLow := 980.0

	// 1. RETEST_BAND with 5 ticks max chase ceiling (1000 + 5*0.05 = 1000.25 max for BUY)
	retestBot := &TradingBot{
		cfg: &config.Settings{
			EntryLimitMode:          "RETEST_BAND",
			EntryLimitMaxChaseTicks: 5,
		},
	}

	// Case 1a: BUY within ceiling (LTP 1000.15 <= 1000.25) -> Allowed
	overextended, reason, _ := retestBot.isBreakoutOverextended("BUY", 1000.15, setupHigh, setupLow, tickSize)
	if overextended {
		t.Errorf("expected LTP 1000.15 NOT to be overextended, but got reason: %s", reason)
	}

	// Case 1b: BUY exceeding ceiling (LTP 1000.35 > 1000.25) -> Overextended!
	overextended, reason, maxAllowed := retestBot.isBreakoutOverextended("BUY", 1000.35, setupHigh, setupLow, tickSize)
	if !overextended {
		t.Errorf("expected LTP 1000.35 to be overextended (> 1000.25)")
	}
	if math.Abs(maxAllowed-1000.25) > 0.001 {
		t.Errorf("expected ceiling price 1000.25, got %.2f", maxAllowed)
	}
	if reason == "" {
		t.Errorf("expected non-empty rejection reason")
	}

	// Case 1c: SELL within ceiling (LTP 979.85 >= 980.0 - 5*0.05 = 979.75) -> Allowed
	overextended, reason, _ = retestBot.isBreakoutOverextended("SELL", 979.85, setupHigh, setupLow, tickSize)
	if overextended {
		t.Errorf("expected LTP 979.85 NOT to be overextended, but got reason: %s", reason)
	}

	// Case 1d: SELL exceeding ceiling (LTP 979.65 < 979.75) -> Overextended!
	overextended, reason, minAllowed := retestBot.isBreakoutOverextended("SELL", 979.65, setupHigh, setupLow, tickSize)
	if !overextended {
		t.Errorf("expected LTP 979.65 to be overextended (< 979.75)")
	}
	if math.Abs(minAllowed-979.75) > 0.001 {
		t.Errorf("expected floor price 979.75, got %.2f", minAllowed)
	}

	// 2. DIRECT_LIMIT mode: Even if LTP is 20 ticks above setupHigh, it is NEVER overextended
	directBot := &TradingBot{
		cfg: &config.Settings{
			EntryLimitMode:          "DIRECT_LIMIT",
			EntryLimitMaxChaseTicks: 5,
		},
	}
	overextended, _, _ = directBot.isBreakoutOverextended("BUY", 1001.00, setupHigh, setupLow, tickSize)
	if overextended {
		t.Errorf("DIRECT_LIMIT mode must never mark breakout as overextended")
	}

	// 3. MARKET mode: Never overextended
	marketBot := &TradingBot{
		cfg: &config.Settings{
			EntryLimitMode:          "MARKET",
			EntryLimitMaxChaseTicks: 5,
		},
	}
	overextended, _, _ = marketBot.isBreakoutOverextended("BUY", 1005.00, setupHigh, setupLow, tickSize)
	if overextended {
		t.Errorf("MARKET mode must never mark breakout as overextended")
	}
}
