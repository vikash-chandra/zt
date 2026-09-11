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
