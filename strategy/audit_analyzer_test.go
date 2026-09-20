package strategy

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

func TestAuditAnalyzerReplay(t *testing.T) {
	logger := zap.NewNop()
	analyzer := NewAuditAnalyzer(logger, nil, nil)

	// Build synthetic 1m candles for EMA S5
	baseTime := time.Date(2026, 9, 8, 9, 15, 0, 0, data.ISTLocation)
	var all1m []data.Candle
	var today1m []data.Candle

	// Historical 30 candles below EMA
	for i := 0; i < 30; i++ {
		cTime := baseTime.Add(-time.Duration(30-i) * time.Minute)
		c := data.Candle{
			Token:  12345,
			Time:   cTime,
			Open:   680.0,
			High:   681.0,
			Low:    678.0,
			Close:  679.0,
			Volume: 1000,
		}
		all1m = append(all1m, c)
	}

	// 5-candle U-shape
	// Candle 1: Low 676
	all1m = append(all1m, data.Candle{Time: baseTime, Open: 679.0, High: 680.0, Low: 676.0, Close: 677.0, Volume: 2000})
	today1m = append(today1m, all1m[len(all1m)-1])

	// Candle 2: Low 675
	all1m = append(all1m, data.Candle{Time: baseTime.Add(1 * time.Minute), Open: 677.0, High: 678.0, Low: 675.0, Close: 676.0, Volume: 2000})
	today1m = append(today1m, all1m[len(all1m)-1])

	// Candle 3: Low 674.30 (Lowest Low)
	all1m = append(all1m, data.Candle{Time: baseTime.Add(2 * time.Minute), Open: 676.0, High: 677.0, Low: 674.30, Close: 675.50, Volume: 2000})
	today1m = append(today1m, all1m[len(all1m)-1])

	// Candle 4: Rebound starts
	all1m = append(all1m, data.Candle{Time: baseTime.Add(3 * time.Minute), Open: 675.50, High: 678.0, Low: 675.0, Close: 677.50, Volume: 2500})
	today1m = append(today1m, all1m[len(all1m)-1])

	// Candle 5: Master Candle Rebound (+0.85% from lowest low 674.30, closes 680.50 above EMA)
	all1m = append(all1m, data.Candle{Time: baseTime.Add(4 * time.Minute), Open: 677.50, High: 681.0, Low: 677.0, Close: 680.50, Volume: 5000})
	today1m = append(today1m, all1m[len(all1m)-1])

	// Candle 6: Confirmation Candle (Broke Master High 681.0, High 682.0, Low 680.20, Close 681.50)
	all1m = append(all1m, data.Candle{Time: baseTime.Add(5 * time.Minute), Open: 680.50, High: 682.0, Low: 680.20, Close: 681.50, Volume: 4000})
	today1m = append(today1m, all1m[len(all1m)-1])

	// Candle 7: Invalidation (Low breaches Master Low 677.0)
	all1m = append(all1m, data.Candle{Time: baseTime.Add(6 * time.Minute), Open: 681.50, High: 681.80, Low: 676.50, Close: 676.80, Volume: 3000})
	today1m = append(today1m, all1m[len(all1m)-1])

	summary := StockDaySummary{
		Open:     679.0,
		High:     682.0,
		Low:      674.30,
		Close:    676.80,
		RangePct: 1.13,
		PDH:      680.0,
		PDL:      678.0,
		PDClose:  682.0,
	}

	testCfg := AppliedStrategyConfig{
		StrategyName: "EMAS5_BREAKOUT",
		Parameters: map[string]interface{}{
			"rally_candles":       2,
			"min_rebound_pct":     0.5,
			"master_max_pct":      2.0,
			"master_max_wick_pct": 40.0,
			"max_inside_candles":  1,
			"confirm_max_pct":     1.0,
			"trade_end_time":      "11:00:00",
		},
	}

	events, diags := analyzer.replayEMAS5("DLF", all1m, today1m, summary, nil, testCfg)
	if len(diags) != len(today1m) {
		t.Errorf("Expected %d diagnostics items, got %d", len(today1m), len(diags))
	}
	if len(events) < 3 {
		t.Fatalf("Expected at least 3 events (SETUP_FORMED, CONFIRMATION_ARMED, SETUP_INVALIDATED), got %d", len(events))
	}

	if events[0].Stage != "SETUP_FORMED" {
		t.Errorf("Expected event 0 to be SETUP_FORMED, got %s", events[0].Stage)
	}

	if events[1].Stage != "CONFIRMATION_ARMED" {
		t.Errorf("Expected event 1 to be CONFIRMATION_ARMED, got %s", events[1].Stage)
	}

	if events[2].Stage != "SETUP_INVALIDATED" {
		t.Errorf("Expected event 2 to be SETUP_INVALIDATED, got %s", events[2].Stage)
	}

	insights := analyzer.generateInsights("DLF", "EMAS5_BREAKOUT", summary, events, nil)
	if insights.Summary == "" {
		t.Errorf("Expected non-empty insights summary")
	}

	t.Logf("Generated Insights Summary: %s", insights.Summary)
	t.Logf("Improvement suggestions count: %d", len(insights.ImprovementSuggestions))
}

func TestAuditAnalyzerVandeBharatReplay(t *testing.T) {
	logger := zap.NewNop()
	analyzer := NewAuditAnalyzer(logger, nil, nil)

	baseTime := time.Date(2026, 9, 8, 9, 15, 0, 0, data.ISTLocation)
	var today5m []data.Candle

	// Candle 1 (09:15): Master Buy (Closes above PDH 30000, Open 29800, High 30500, Low 29750, Close 30400)
	today5m = append(today5m, data.Candle{
		Time:   baseTime,
		Open:   29800,
		High:   30500,
		Low:    29750,
		Close:  30400,
		Volume: 10000,
	})

	// Candle 2 (09:20): SL Anchor (High 30600 > Master High 30500 => Confirmation Trigger @ 30600, SL @ 30300)
	today5m = append(today5m, data.Candle{
		Time:   baseTime.Add(5 * time.Minute),
		Open:   30400,
		High:   30600,
		Low:    30300,
		Close:  30550,
		Volume: 8000,
	})

	// Candle 3 (09:25): Breakout (High 30900 >= 30600)
	today5m = append(today5m, data.Candle{
		Time:   baseTime.Add(10 * time.Minute),
		Open:   30550,
		High:   30900,
		Low:    30500,
		Close:  30850,
		Volume: 12000,
	})

	summary := StockDaySummary{
		Open:     29800,
		High:     30900,
		Low:      29000,
		Close:    30850,
		RangePct: 6.37,
		PDH:      30000,
		PDL:      29000,
		PDClose:  29900,
	}

	events, diags := analyzer.replayVandeBharat("POWERINDIA", today5m, summary, nil)
	if len(events) < 3 {
		t.Fatalf("Expected 3 events (SETUP_FORMED, CONFIRMATION_ARMED, TRADE_TAKEN), got %d", len(events))
	}
	if len(diags) != 3 {
		t.Fatalf("Expected 3 candle diagnostics, got %d", len(diags))
	}
	if diags[0].Status != "MASTER_ESTABLISHED" || diags[0].Verdict != "PASS" {
		t.Errorf("Expected diags[0] MASTER_ESTABLISHED/PASS, got %s/%s", diags[0].Status, diags[0].Verdict)
	}
	if diags[1].Status != "CONFIRMATION_ARMED" || diags[1].Verdict != "PASS" {
		t.Errorf("Expected diags[1] CONFIRMATION_ARMED/PASS, got %s/%s", diags[1].Status, diags[1].Verdict)
	}
	if diags[2].Status != "BREAKOUT_TRIGGER" || diags[2].Verdict != "ENTRY" {
		t.Errorf("Expected diags[2] BREAKOUT_TRIGGER/ENTRY, got %s/%s", diags[2].Status, diags[2].Verdict)
	}

	if events[0].Stage != "SETUP_FORMED" {
		t.Errorf("Expected event 0 SETUP_FORMED, got %s", events[0].Stage)
	}
	if events[1].Stage != "CONFIRMATION_ARMED" {
		t.Errorf("Expected event 1 CONFIRMATION_ARMED, got %s", events[1].Stage)
	}
	if events[2].Stage != "TRADE_TAKEN" {
		t.Errorf("Expected event 2 TRADE_TAKEN, got %s", events[2].Stage)
	}

	insights := analyzer.generateInsights("POWERINDIA", "VANDE_BHARAT", summary, events, nil)
	t.Logf("POWERINDIA Insights Summary: %s", insights.Summary)
}

func TestAuditAnalyzer_ConcurrentReplay(t *testing.T) {
	logger := zap.NewNop()
	analyzer := NewAuditAnalyzer(logger, nil, nil)

	baseTime := time.Date(2026, 9, 8, 9, 15, 0, 0, data.ISTLocation)
	var all5m []data.Candle
	for i := 0; i < 60; i++ {
		all5m = append(all5m, data.Candle{
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   1000 + float64(i)*2,
			High:   1005 + float64(i)*2,
			Low:    998 + float64(i)*2,
			Close:  1002 + float64(i)*2,
			Volume: 5000,
		})
	}

	appCfg := AppliedStrategyConfig{
		StrategyName:    "EMAS5_BREAKOUT",
		CandleTimeframe: "5m",
		TradeEndTime:    "14:30:30",
		Parameters: map[string]interface{}{
			"rally_candles":   6,
			"min_rebound_pct": 0.40,
			"master_max_pct":  1.00,
		},
	}

	summary := StockDaySummary{
		Open: 1000, High: 1120, Low: 998, Close: 1115,
		RangePct: 12.0, PDH: 1050, PDL: 980, PDClose: 1010,
	}

	var wg sync.WaitGroup
	workers := 25
	errChan := make(chan error, workers)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sym := fmt.Sprintf("STOCK_%d", workerID)
			evs, diags := analyzer.replayEMAS5(sym, all5m, all5m[20:], summary, nil, appCfg)
			if len(diags) != len(all5m[20:]) {
				errChan <- fmt.Errorf("worker %d diagnostics count mismatch: expected %d, got %d", workerID, len(all5m[20:]), len(diags))
				return
			}
			ins := analyzer.generateInsights(sym, "EMAS5_BREAKOUT", summary, evs, nil)
			if ins.GeometricQuality == "" {
				errChan <- fmt.Errorf("worker %d empty geometric quality", workerID)
				return
			}
		}(w)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Fatalf("Concurrent worker failed: %v", err)
	}
}

func TestAuditAnalyzerConfirmationBreachInvalidation(t *testing.T) {
	logger := zap.NewNop()
	analyzer := NewAuditAnalyzer(logger, nil, nil)

	baseTime := time.Date(2026, 9, 18, 9, 15, 0, 0, data.ISTLocation)
	var all5m []data.Candle
	var today5m []data.Candle

	// Warmup 30 candles
	for i := 0; i < 30; i++ {
		c := data.Candle{
			Token:  12345,
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   1400.0,
			High:   1405.0,
			Low:    1395.0,
			Close:  1401.0,
			Volume: 2000,
		}
		all5m = append(all5m, c)
		today5m = append(today5m, c)
	}

	// 11:00 Candle: Master Buy Candle
	cMaster := data.Candle{
		Token:  12345,
		Time:   baseTime.Add(21 * 5 * time.Minute), // 11:00
		Open:   1404.50,
		High:   1411.90,
		Low:    1402.60,
		Close:  1411.60,
		Volume: 5000,
	}
	all5m = append(all5m, cMaster)
	today5m = append(today5m, cMaster)

	// 11:05 Candle: Confirmation Candle (Arms setup: Trigger 1413.60, SL 1402.60, Confirm Low 1410.10)
	cConfirm := data.Candle{
		Token:  12345,
		Time:   baseTime.Add(22 * 5 * time.Minute), // 11:05
		Open:   1411.60,
		High:   1413.60,
		Low:    1410.10,
		Close:  1413.30,
		Volume: 4000,
	}
	all5m = append(all5m, cConfirm)
	today5m = append(today5m, cConfirm)

	// 11:10 Candle: Inside candle awaiting trigger (Low 1411.80 > 1410.10, High 1413.60 <= 1413.60)
	cWait := data.Candle{
		Token:  12345,
		Time:   baseTime.Add(23 * 5 * time.Minute), // 11:10
		Open:   1413.30,
		High:   1413.60,
		Low:    1411.80,
		Close:  1412.40,
		Volume: 2500,
	}
	all5m = append(all5m, cWait)
	today5m = append(today5m, cWait)

	// 11:15 Candle: Master Low Breached (Low 1401.00 < Master Low 1402.60)
	cBreach := data.Candle{
		Token:  12345,
		Time:   baseTime.Add(24 * 5 * time.Minute), // 11:15
		Open:   1412.40,
		High:   1412.40,
		Low:    1401.00,
		Close:  1401.50,
		Volume: 3000,
	}
	all5m = append(all5m, cBreach)
	today5m = append(today5m, cBreach)

	// 11:20 Candle: Subsequent candle (Setup must already be dead/cleared)
	cNext := data.Candle{
		Token:  12345,
		Time:   baseTime.Add(25 * 5 * time.Minute), // 11:20
		Open:   1409.10,
		High:   1410.50,
		Low:    1408.40,
		Close:  1409.10,
		Volume: 1500,
	}
	all5m = append(all5m, cNext)
	today5m = append(today5m, cNext)

	summary := StockDaySummary{
		Open: 1400.0, High: 1413.60, Low: 1395.0, Close: 1409.10,
		RangePct: 1.3, PDH: 1410.0, PDL: 1390.0, PDClose: 1400.0,
	}

	appCfg := AppliedStrategyConfig{
		StrategyName:    "EMAS5_BREAKOUT",
		CandleTimeframe: "5m",
		TradeEndTime:    "14:30:30",
		Parameters: map[string]interface{}{
			"rally_candles":          2,
			"min_rebound_pct":        0.20,
			"master_max_pct":         2.00,
			"master_max_wick_pct":    60.0,
			"max_inside_candles":     3,
			"confirm_max_pct":        1.00,
			"max_setup_wait_candles": 6,
		},
	}

	events, diags := analyzer.replayEMAS5("ADANIENSOL", all5m, today5m, summary, nil, appCfg)

	// Find diagnostics for 11:05, 11:10, 11:15, 11:20
	var diag1105, diag1110, diag1115, diag1120 *CandleDiagnosticItem
	for i := range diags {
		switch diags[i].Time {
		case "11:05":
			diag1105 = &diags[i]
		case "11:10":
			diag1110 = &diags[i]
		case "11:15":
			diag1115 = &diags[i]
		case "11:20":
			diag1120 = &diags[i]
		}
	}

	if diag1105 != nil && diag1105.Status != "CONFIRMATION_ARMED" {
		t.Errorf("Expected 11:05 to be CONFIRMATION_ARMED, got %s", diag1105.Status)
	}

	if diag1110 != nil {
		if diag1110.Status != "AWAITING_TRIGGER" {
			t.Errorf("Expected 11:10 to be AWAITING_TRIGGER, got %s", diag1110.Status)
		}
		if diag1110.Verdict != "ARMED" {
			t.Errorf("Expected 11:10 verdict to be ARMED, got %s", diag1110.Verdict)
		}
	}

	if diag1115 != nil {
		if diag1115.Status != "INVALIDATED" {
			t.Errorf("Expected 11:15 to be INVALIDATED, got %s", diag1115.Status)
		}
		if diag1115.Verdict != "REJECTED" {
			t.Errorf("Expected 11:15 verdict to be REJECTED, got %s", diag1115.Verdict)
		}
		if len(diag1115.RejectionReasons) == 0 || !containsSubstring(diag1115.RejectionReasons[0], "Master Low") {
			t.Errorf("Expected 11:15 rejection reason to mention Master Low breach, got %v", diag1115.RejectionReasons)
		}
	}

	if diag1120 != nil {
		if diag1120.Status == "CONFIRMATION_ARMED" || diag1120.Status == "AWAITING_TRIGGER" {
			t.Errorf("11:20 should NOT be armed after invalidation! Got status %s", diag1120.Status)
		}
	}

	// Verify events contain SETUP_INVALIDATED
	foundInvalidated := false
	for _, ev := range events {
		if ev.Stage == "SETUP_INVALIDATED" {
			foundInvalidated = true
			break
		}
	}
	if !foundInvalidated {
		t.Errorf("Expected SETUP_INVALIDATED event in events list for ADANIENSOL")
	}
}

func TestAuditAnalyzerSONACOMSVandeBharatReplay(t *testing.T) {
	logger := zap.NewNop()
	analyzer := NewAuditAnalyzer(logger, nil, nil)

	baseTime := time.Date(2026, 9, 18, 9, 15, 0, 0, data.ISTLocation)
	var today5m []data.Candle

	// 09:15 - Master BUY candle: Close 793.65 > PDH 791.25, Green, Range 2.03% <= 3%, Wick 16.5% <= 75%
	today5m = append(today5m, data.Candle{
		Time:   baseTime,
		Open:   780.20,
		High:   796.30,
		Low:    780.20,
		Close:  793.65,
		Volume: 71343,
	})

	// 09:20 - Inside Candle 2 / SL Anchor: High 794.05 <= 796.30, Low 790.50 > 780.20
	// Rule 2 Armed: Trigger 796.30, SL 790.50
	today5m = append(today5m, data.Candle{
		Time:   baseTime.Add(5 * time.Minute),
		Open:   793.30,
		High:   794.05,
		Low:    790.50,
		Close:  792.80,
		Volume: 45975,
	})

	// 09:25 - Inside candle
	today5m = append(today5m, data.Candle{
		Time:   baseTime.Add(10 * time.Minute),
		Open:   792.45,
		High:   794.10,
		Low:    790.25,
		Close:  792.40,
		Volume: 24781,
	})

	// 09:30 - Inside candle
	today5m = append(today5m, data.Candle{
		Time:   baseTime.Add(15 * time.Minute),
		Open:   792.60,
		High:   793.50,
		Low:    791.45,
		Close:  792.25,
		Volume: 35506,
	})

	// 09:35 - Inside candle
	today5m = append(today5m, data.Candle{
		Time:   baseTime.Add(20 * time.Minute),
		Open:   792.25,
		High:   794.70,
		Low:    792.00,
		Close:  794.15,
		Volume: 22576,
	})

	// 09:40 - Breakout Candle: High 798.65 breaches Trigger (796.30)!
	today5m = append(today5m, data.Candle{
		Time:   baseTime.Add(25 * time.Minute),
		Open:   795.10,
		High:   798.65,
		Low:    793.35,
		Close:  797.40,
		Volume: 100757,
	})

	summary := StockDaySummary{
		Open:     780.20,
		High:     811.35,
		Low:      780.20,
		Close:    811.35,
		RangePct: 3.99,
		PDH:      791.25,
		PDL:      759.05,
		PDClose:  779.40,
	}

	appCfg := AppliedStrategyConfig{
		StrategyName: "VANDE_BHARAT",
		TradeEndTime: "11:00:00",
		Parameters: map[string]interface{}{
			"master_max_pct":      3.0,
			"master_max_wick_pct": 75.0,
			"sl_min_pct":          0.05,
			"sl_max_pct":          2.0,
		},
	}

	events, diags := analyzer.replayVandeBharat("SONACOMS", today5m, summary, nil, appCfg)

	if len(diags) != 6 {
		t.Fatalf("Expected 6 candle diagnostics, got %d", len(diags))
	}

	// 09:15: MASTER_ESTABLISHED
	if diags[0].Status != "MASTER_ESTABLISHED" || diags[0].Verdict != "PASS" {
		t.Errorf("Expected 09:15 MASTER_ESTABLISHED/PASS, got %s/%s", diags[0].Status, diags[0].Verdict)
	}

	// 09:20: CONFIRMATION_ARMED
	if diags[1].Status != "CONFIRMATION_ARMED" || diags[1].Verdict != "PASS" {
		t.Errorf("Expected 09:20 CONFIRMATION_ARMED/PASS, got %s/%s", diags[1].Status, diags[1].Verdict)
	}

	// 09:25: AWAITING_TRIGGER
	if diags[2].Status != "AWAITING_TRIGGER" || diags[2].Verdict != "ARMED" {
		t.Errorf("Expected 09:25 AWAITING_TRIGGER/ARMED, got %s/%s", diags[2].Status, diags[2].Verdict)
	}

	// 09:40: BREAKOUT_TRIGGER
	if diags[5].Status != "BREAKOUT_TRIGGER" || diags[5].Verdict != "ENTRY" {
		t.Errorf("Expected 09:40 BREAKOUT_TRIGGER/ENTRY, got %s/%s", diags[5].Status, diags[5].Verdict)
	}

	// Verify events
	foundMaster := false
	foundArmed := false
	foundTaken := false
	for _, ev := range events {
		if ev.Stage == "SETUP_FORMED" {
			foundMaster = true
		} else if ev.Stage == "CONFIRMATION_ARMED" {
			foundArmed = true
		} else if ev.Stage == "TRADE_TAKEN" {
			foundTaken = true
			if ev.ExecutedPrice != 796.30 {
				t.Errorf("Expected executed price 796.30, got %.2f", ev.ExecutedPrice)
			}
		}
	}
	if !foundMaster || !foundArmed || !foundTaken {
		t.Errorf("Expected SETUP_FORMED, CONFIRMATION_ARMED, TRADE_TAKEN events, got master=%v, armed=%v, taken=%v", foundMaster, foundArmed, foundTaken)
	}
}

func containsSubstring(s, sub string) bool {
	return strings.Contains(s, sub)
}

func TestAuditAnalyzer_LoadStrategyConfig_DynamicWiring(t *testing.T) {
	logger := zap.NewNop()
	analyzer := NewAuditAnalyzer(logger, nil, nil)

	sysConfigs := map[string]map[string]string{
		"TRADING_STRATEGY": {
			"VANDE_BHARAT": `{"candle_time_frame":"5m","trade_end_time":"11:30:00","master_max_pct":2.5,"master_max_wick_pct":60.0,"sl_min_pct":0.15,"sl_max_pct":1.8}`,
			"EMAS5_BREAKOUT": `{"candle_time_frame":"5m","trade_end_time":"14:00:00","rally_candles":4,"min_rebound_pct":0.35,"master_max_pct":1.20}`,
			"LOW_VOLUME": `{"candle_time_frame":"15m","trade_end_time":"13:00:00"}`,
			"FAKE_BREAKOUT": `{"candle_time_frame":"5m","trade_end_time":"12:00:00","gap_up_min_pct":3.0,"gap_down_min_pct":3.0}`,
			"VANDE_BHARAT_TRAP": `{"candle_time_frame":"5m","trade_end_time":"11:15:00","fake_master_max_pct":2.8,"genuine_master_max_pct":1.5}`,
		},
		"EQUITY_STRATEGY": {
			"vb_master_max_pct": "2.8",
			"vb_candle_timeframe": "5m",
			"es5_rally_candles_count": "5",
			"fb_gap_up_min_pct": "3.5",
		},
	}

	// 1. Verify Vande Bharat dynamic load
	vbCfg := analyzer.loadStrategyConfig(sysConfigs, "VANDE_BHARAT")
	if vbCfg.CandleTimeframe != "5m" {
		t.Errorf("Expected vb CandleTimeframe '5m', got '%s'", vbCfg.CandleTimeframe)
	}
	if vbCfg.TradeEndTime != "11:30:00" {
		t.Errorf("Expected vb TradeEndTime '11:30:00', got '%s'", vbCfg.TradeEndTime)
	}
	// EQUITY_STRATEGY overrides TRADING_STRATEGY if set
	if getFloatParam(vbCfg.Parameters, 0, "master_max_pct") != 2.8 {
		t.Errorf("Expected vb master_max_pct 2.8, got %v", vbCfg.Parameters["master_max_pct"])
	}

	// 2. Verify EMA S5 dynamic load
	es5Cfg := analyzer.loadStrategyConfig(sysConfigs, "EMAS5_BREAKOUT")
	if es5Cfg.TradeEndTime != "14:00:00" {
		t.Errorf("Expected es5 TradeEndTime '14:00:00', got '%s'", es5Cfg.TradeEndTime)
	}
	if getIntParam(es5Cfg.Parameters, 0, "rally_candles_count", "rally_candles") != 5 {
		t.Errorf("Expected es5 rally_candles 5, got %v", es5Cfg.Parameters["rally_candles_count"])
	}

	// 3. Verify getFloatParam & getIntParam with fallback and aliases
	testParams := map[string]interface{}{
		"confirm_min_pct": "0.25",
		"wait_candles":    float64(4),
	}
	val := getFloatParam(testParams, 0.05, "sl_min_pct", "confirm_min_pct")
	if val != 0.25 {
		t.Errorf("Expected alias fallback 0.25, got %f", val)
	}
	intVal := getIntParam(testParams, 2, "max_wait_candles", "wait_candles")
	if intVal != 4 {
		t.Errorf("Expected alias fallback 4, got %d", intVal)
	}
}

func TestAuditAnalyzer_ReplayFaithfulEvaluation(t *testing.T) {
	logger := zap.NewNop()
	analyzer := NewAuditAnalyzer(logger, nil, nil)

	// Construct test scenario where live events show Master and Confirmation but no live order
	baseTime := time.Date(2026, 9, 18, 9, 15, 0, 0, data.ISTLocation)
	var allCandles []data.Candle
	var todayCandles []data.Candle

	// Warmup 25 candles
	for i := 0; i < 25; i++ {
		cTime := baseTime.Add(-time.Duration(25-i) * 5 * time.Minute)
		allCandles = append(allCandles, data.Candle{
			Token:  12345,
			Time:   cTime,
			Open:   2940.0,
			High:   2942.0,
			Low:    2938.0,
			Close:  2940.0,
			Volume: 1000,
		})
	}

	// 5 rally candles
	for i := 0; i < 5; i++ {
		cTime := baseTime.Add(time.Duration(i) * 5 * time.Minute)
		c := data.Candle{Time: cTime, Open: 2930.0, High: 2935.0, Low: 2924.6, Close: 2932.0, Volume: 5000}
		allCandles = append(allCandles, c)
		todayCandles = append(todayCandles, c)
	}

	// Master Candle at 09:40 (idx 5 of today)
	masterTime := baseTime.Add(5 * 5 * time.Minute)
	masterCandle := data.Candle{Time: masterTime, Open: 2944.4, High: 2952.0, Low: 2943.0, Close: 2947.2, Volume: 15000}
	allCandles = append(allCandles, masterCandle)
	todayCandles = append(todayCandles, masterCandle)

	// Confirmation Candle at 09:45 (idx 6 of today)
	confirmTime := baseTime.Add(6 * 5 * time.Minute)
	confirmCandle := data.Candle{Time: confirmTime, Open: 2947.2, High: 2954.4, Low: 2943.2, Close: 2947.4, Volume: 12000}
	allCandles = append(allCandles, confirmCandle)
	todayCandles = append(todayCandles, confirmCandle)

	// Candle at 09:50 (High crosses 2954.4 to 2955.0, but no live trade executed)
	c0950 := data.Candle{Time: baseTime.Add(7 * 5 * time.Minute), Open: 2946.6, High: 2955.0, Low: 2946.0, Close: 2955.0, Volume: 10000}
	allCandles = append(allCandles, c0950)
	todayCandles = append(todayCandles, c0950)

	// Candle at 09:55
	c0955 := data.Candle{Time: baseTime.Add(8 * 5 * time.Minute), Open: 2954.6, High: 2955.0, Low: 2949.6, Close: 2951.3, Volume: 8000}
	allCandles = append(allCandles, c0955)
	todayCandles = append(todayCandles, c0955)

	// Invalidation Candle at 10:00 (Low breaks Master Low 2943.0 to 2941.6)
	c1000 := data.Candle{Time: baseTime.Add(9 * 5 * time.Minute), Open: 2951.3, High: 2951.3, Low: 2941.6, Close: 2943.7, Volume: 10000}
	allCandles = append(allCandles, c1000)
	todayCandles = append(todayCandles, c1000)

	// Intermediate continuous candles from 10:05 to 12:15
	for tCandle := baseTime.Add(10 * 5 * time.Minute); tCandle.Before(time.Date(2026, 9, 18, 12, 20, 0, 0, data.ISTLocation)); tCandle = tCandle.Add(5 * time.Minute) {
		c := data.Candle{Time: tCandle, Open: 2945.0, High: 2948.0, Low: 2942.0, Close: 2946.0, Volume: 8000}
		allCandles = append(allCandles, c)
		todayCandles = append(todayCandles, c)
	}

	// Second Master Candle at 12:20 (rebound from day low)
	c1220Time := time.Date(2026, 9, 18, 12, 20, 0, 0, data.ISTLocation)
	c1220 := data.Candle{Time: c1220Time, Open: 2946.7, High: 2965.0, Low: 2945.0, Close: 2956.9, Volume: 26000}
	allCandles = append(allCandles, c1220)
	todayCandles = append(todayCandles, c1220)

	// Second Confirmation Candle at 12:25
	c1225Time := time.Date(2026, 9, 18, 12, 25, 0, 0, data.ISTLocation)
	c1225 := data.Candle{Time: c1225Time, Open: 2956.9, High: 2966.0, Low: 2956.9, Close: 2964.7, Volume: 20000}
	allCandles = append(allCandles, c1225)
	todayCandles = append(todayCandles, c1225)

	summary := StockDaySummary{
		PDH:     2945.0,
		PDL:     2907.0,
		PDClose: 2929.2,
	}

	testCfg := AppliedStrategyConfig{
		StrategyName: "EMAS5_BREAKOUT",
		Parameters: map[string]interface{}{
			"rally_candles":         5,
			"min_rebound_pct":       0.35,
			"master_max_pct":        1.0,
			"master_max_wick_pct":   80.0,
			"max_inside_candles":    3,
			"confirm_max_pct":       0.75,
			"trade_end_time":        "14:30:30",
			"max_trades_per_stock":  2,
			"max_setup_wait_candles": 7,
		},
	}

	// Live telemetry shows events occurred, but NO live trade was executed (matchingTrades is empty)
	liveEvents := []data.StrategyEvent{
		{Stage: "MASTER_FORMED", Direction: "BUY", EventTime: masterTime},
		{Stage: "CONFIRMATION_ARMED", Direction: "BUY", EventTime: confirmTime},
		{Stage: "SETUP_INVALIDATED", Direction: "BUY", EventTime: c1000.Time},
		{Stage: "MASTER_FORMED", Direction: "BUY", EventTime: c1220Time},
		{Stage: "CONFIRMATION_ARMED", Direction: "BUY", EventTime: c1225Time},
	}

	events, diags := analyzer.replayEMAS5("ADANIENT", allCandles, todayCandles, summary, nil, testCfg, liveEvents)
	if len(diags) != len(todayCandles) {
		t.Fatalf("Expected %d diags, got %d", len(todayCandles), len(diags))
	}

	// Locate 12:20 and 12:25 diagnostics
	var diag1220, diag1225 *CandleDiagnosticItem
	for i := range diags {
		if diags[i].Time == "12:20" {
			diag1220 = &diags[i]
		} else if diags[i].Time == "12:25" {
			diag1225 = &diags[i]
		}
	}

	if diag1220 == nil || diag1220.Status != "MASTER_ESTABLISHED" {
		t.Errorf("Expected 12:20 to be MASTER_ESTABLISHED, got: %+v", diag1220)
	}
	if diag1225 == nil || diag1225.Status != "CONFIRMATION_ARMED" {
		t.Errorf("Expected 12:25 to be CONFIRMATION_ARMED, got: %+v", diag1225)
	}

	// Verify that 09:50 did not falsely trigger a dummy trade and close the setup prematurely
	for _, d := range diags {
		if d.Time == "09:50" && d.Status == "TRADE_TAKEN" {
			t.Errorf("09:50 should not be TRADE_TAKEN when no live trade was executed!")
		}
	}

	// Verify events count has NO dummy trade
	for _, e := range events {
		if e.Stage == "TRADE_TAKEN" {
			t.Errorf("No TRADE_TAKEN should be in events when no real trade was executed!")
		}
	}
}

func TestAuditAnalyzerBreachingCandleImmediatelyReestablishesNewMaster(t *testing.T) {
	logger := zap.NewNop()
	analyzer := NewAuditAnalyzer(logger, nil, nil)

	baseTime := time.Date(2026, 9, 18, 9, 15, 0, 0, data.ISTLocation)

	var all5m []data.Candle
	var today5m []data.Candle

	// Warmup 20 candles
	for i := 0; i < 20; i++ {
		c := data.Candle{
			Token:  12345,
			Time:   baseTime.Add(time.Duration(i*5) * time.Minute),
			Open:   1400.0,
			High:   1405.0,
			Low:    1395.0,
			Close:  1400.0,
			Volume: 5000,
		}
		all5m = append(all5m, c)
		today5m = append(today5m, c)
	}

	// 10:55 c1 Master Candle: Green, Low = 1401.50, High = 1412.00, Close = 1411.00 (Touches EMA10 1402.00)
	c1 := data.Candle{
		Token:  12345,
		Time:   baseTime.Add(20 * 5 * time.Minute), // 10:55
		Open:   1403.00,
		High:   1412.00,
		Low:    1401.50,
		Close:  1411.00,
		Volume: 10000,
	}
	all5m = append(all5m, c1)
	today5m = append(today5m, c1)

	// 11:00 c2 Liquidity sweep: Dips to Low 1398.00 (< c1 Low 1401.50 -> breaches Master Low)
	// But rebounds strongly, High = 1415.00, Close = 1414.00, Open = 1403.00
	c2 := data.Candle{
		Token:  12345,
		Time:   baseTime.Add(21 * 5 * time.Minute), // 11:00
		Open:   1403.00,
		High:   1415.00,
		Low:    1398.00,
		Close:  1414.00,
		Volume: 15000,
	}
	all5m = append(all5m, c2)
	today5m = append(today5m, c2)

	// 11:05 c3 Confirmation Candle: High = 1418.00, Low = 1412.00, Close = 1417.00
	c3 := data.Candle{
		Token:  12345,
		Time:   baseTime.Add(22 * 5 * time.Minute), // 11:05
		Open:   1414.00,
		High:   1418.00,
		Low:    1412.00,
		Close:  1417.00,
		Volume: 12000,
	}
	all5m = append(all5m, c3)
	today5m = append(today5m, c3)

	summary := StockDaySummary{
		Open:     1400.0,
		High:     1418.0,
		Low:      1395.0,
		Close:    1417.0,
		RangePct: 1.6,
		PDH:      1400.0,
		PDL:      1390.0,
		PDClose:  1398.0,
	}

	testCfg := AppliedStrategyConfig{
		StrategyName: "EMAS5_BREAKOUT",
		Parameters: map[string]interface{}{
			"rally_candles":        2,
			"min_rebound_pct":      0.2,
			"master_max_pct":       2.0,
			"master_max_wick_pct":  50.0,
			"max_inside_candles":   1,
			"confirm_max_pct":      1.0,
			"ema_touch_buffer_pct": 0.20,
			"trade_end_time":       "15:00:00",
		},
	}

	events, diags := analyzer.replayEMAS5("INFY", all5m, today5m, summary, nil, testCfg)

	// Verify diagnostics
	var diag1055, diag1100, diag1105 *CandleDiagnosticItem
	for i := range diags {
		if diags[i].Time == "10:55" {
			diag1055 = &diags[i]
		} else if diags[i].Time == "11:00" {
			diag1100 = &diags[i]
		} else if diags[i].Time == "11:05" {
			diag1105 = &diags[i]
		}
	}

	if diag1055 == nil || diag1055.Status != "MASTER_ESTABLISHED" {
		t.Errorf("Expected 10:55 to be MASTER_ESTABLISHED, got: %+v", diag1055)
	}

	if diag1100 == nil || diag1100.Status != "MASTER_ESTABLISHED" {
		t.Errorf("Expected 11:00 (c2) to be MASTER_ESTABLISHED after invalidating c1, got: %+v", diag1100)
	}
	if diag1100.Verdict != "PASS" {
		t.Errorf("Expected 11:00 verdict to be PASS, got: %s", diag1100.Verdict)
	}

	if diag1105 == nil || diag1105.Status != "CONFIRMATION_ARMED" {
		t.Errorf("Expected 11:05 (c3) to be CONFIRMATION_ARMED for c2, got: %+v", diag1105)
	}

	// Verify events contains both SETUP_INVALIDATED for c1 and SETUP_ARMED for c2
	foundInvalidated := false
	armedCount := 0
	for _, ev := range events {
		if ev.Stage == "SETUP_INVALIDATED" {
			foundInvalidated = true
		}
		if ev.Stage == "SETUP_ARMED" || ev.Stage == "SETUP_FORMED" {
			armedCount++
		}
	}

	if !foundInvalidated {
		t.Errorf("Expected SETUP_INVALIDATED event when c2 breached c1 Low")
	}
	if armedCount < 2 {
		t.Errorf("Expected at least 2 master armed events (c1 and c2), got %d", armedCount)
	}
}


