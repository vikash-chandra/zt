package strategy

import (
	"fmt"
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

	events := analyzer.replayVandeBharat("POWERINDIA", today5m, summary, nil)
	if len(events) < 3 {
		t.Fatalf("Expected 3 events (SETUP_FORMED, CONFIRMATION_ARMED, TRADE_SKIPPED), got %d", len(events))
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

	// 11:15 Candle: Confirmation Low Breached (Low 1408.60 < Confirm Low 1410.10, though > Master Low 1402.60)
	cBreach := data.Candle{
		Token:  12345,
		Time:   baseTime.Add(24 * 5 * time.Minute), // 11:15
		Open:   1412.40,
		High:   1412.40,
		Low:    1408.60,
		Close:  1409.10,
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
		if len(diag1115.RejectionReasons) == 0 || !containsSubstring(diag1115.RejectionReasons[0], "Confirmation Low") {
			t.Errorf("Expected 11:15 rejection reason to mention Confirmation Low breach, got %v", diag1115.RejectionReasons)
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

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (len(s) > 0 && len(sub) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

