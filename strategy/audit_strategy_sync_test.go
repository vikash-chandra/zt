package strategy

import (
	"testing"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// TestAuditAndStrategies_AllFiveStrategiesSync rigorously verifies that AuditAnalyzer
// and the live strategy engines produce identical lifecycle states and signals.
func TestAuditAndStrategies_AllFiveStrategiesSync(t *testing.T) {
	logger := zap.NewNop()
	analyzer := NewAuditAnalyzer(logger, nil, nil)
	baseTime := time.Date(2026, 9, 22, 9, 15, 0, 0, data.ISTLocation)

	// =========================================================================
	// 1. VANDE_BHARAT SYNCHRONIZATION
	// =========================================================================
	t.Run("VANDE_BHARAT_Sync", func(t *testing.T) {
		vbEngine := NewVandeBharatEngine(logger, 2.0, 0.5, 1.0, 40.0, 0.0)
		sym := "TATAMOTORS"
		vbEngine.SetPreviousDayLevels(sym, 1000.0, 950.0, 990.0)

		c1 := data.Candle{Time: baseTime, Open: 1002.0, High: 1010.0, Low: 1001.0, Close: 1008.0, Volume: 50000}
		c2 := data.Candle{Time: baseTime.Add(5 * time.Minute), Open: 1008.0, High: 1015.0, Low: 1006.0, Close: 1014.0, Volume: 30000}
		c3 := data.Candle{Time: baseTime.Add(10 * time.Minute), Open: 1014.0, High: 1020.0, Low: 1013.0, Close: 1018.0, Volume: 40000}
		candles := []data.Candle{c1, c2, c3}

		// Run engine
		vbEngine.OnCandleClose(&c1, sym)
		vbEngine.mu.RLock()
		engineMaster := vbEngine.masterCandles[sym]
		vbEngine.mu.RUnlock()
		if engineMaster == nil {
			t.Fatal("Engine: expected Master candle to be established")
		}

		vbEngine.OnCandleClose(&c2, sym)
		vbEngine.mu.RLock()
		engineConfirm := vbEngine.confirmationCandles[sym]
		vbEngine.mu.RUnlock()
		if engineConfirm == nil {
			t.Fatal("Engine: expected Confirmation candle to be established")
		}

		signal := vbEngine.CheckBreakout(sym, 1016.0, "BUY")
		if signal == nil || signal.Action != "BUY" {
			t.Fatalf("Engine: expected BUY signal, got %+v", signal)
		}

		// Run AuditAnalyzer replay
		summary := StockDaySummary{Open: 1002.0, High: 1020.0, Low: 1001.0, Close: 1018.0, PDH: 1000.0, PDL: 950.0, PDClose: 990.0}
		appCfg := AppliedStrategyConfig{
			StrategyName: "VANDE_BHARAT",
			TradeEndTime: "11:00:00",
			Parameters: map[string]interface{}{
				"master_max_pct":      2.0,
				"master_max_wick_pct": 40.0,
				"sl_min_pct":          0.5,
				"sl_max_pct":          1.0,
				"min_gap_pct":         0.0,
			},
		}
		events, diags := analyzer.replayVandeBharat(sym, candles, summary, nil, appCfg)
		if len(events) < 3 {
			t.Fatalf("Audit: expected at least 3 events, got %d", len(events))
		}
		if diags[0].Status != "MASTER_ESTABLISHED" || diags[0].Verdict != "PASS" {
			t.Errorf("Audit diags[0] mismatch: got %s/%s", diags[0].Status, diags[0].Verdict)
		}
		if diags[1].Status != "CONFIRMATION_ARMED" || diags[1].Verdict != "PASS" {
			t.Errorf("Audit diags[1] mismatch: got %s/%s", diags[1].Status, diags[1].Verdict)
		}
		if diags[2].Status != "BREAKOUT_TRIGGER" || (diags[2].Verdict != "PASS" && diags[2].Verdict != "ENTRY") {
			t.Errorf("Audit diags[2] mismatch: got %s/%s", diags[2].Status, diags[2].Verdict)
		}
	})

	// =========================================================================
	// 2. LOW_VOLUME SYNCHRONIZATION
	// =========================================================================
	t.Run("LOW_VOLUME_Sync", func(t *testing.T) {
		lvEngine := NewLowVolumeEngine(logger)
		sym := "RELIANCE"
		lvEngine.SetPreviousDayHighLow(sym, 2500.0, 2400.0)
		lvEngine.MinCandlesToIgnore = 1

		c1 := data.Candle{Time: baseTime, Open: 2505.0, High: 2520.0, Low: 2502.0, Close: 2515.0, Volume: 100000}
		// c2 has lowest volume so far and is RED (Close < Open) -> valid Setup Candle for BUY
		c2 := data.Candle{Time: baseTime.Add(5 * time.Minute), Open: 2515.0, High: 2518.0, Low: 2508.0, Close: 2510.0, Volume: 5000}
		c3 := data.Candle{Time: baseTime.Add(10 * time.Minute), Open: 2510.0, High: 2522.0, Low: 2509.0, Close: 2520.0, Volume: 80000}
		candles := []data.Candle{c1, c2, c3}

		lvEngine.OnCandleClose(&c1, sym)
		lvEngine.OnCandleClose(&c2, sym)
		setup := lvEngine.GetSetupCandle(sym)
		if setup == nil || setup.High != 2518.0 {
			t.Fatalf("Engine: expected setup candle with High 2518.0, got %+v", setup)
		}

		signal := lvEngine.CheckBreakout(sym, 2519.0, "BUY")
		if signal == nil || signal.Action != "BUY" {
			t.Fatalf("Engine: expected BUY signal on breakout above setup High, got %+v", signal)
		}

		summary := StockDaySummary{Open: 2505.0, High: 2522.0, Low: 2502.0, Close: 2520.0, PDH: 2500.0, PDL: 2400.0, PDClose: 2490.0}
		appCfg := AppliedStrategyConfig{
			StrategyName: "LOW_VOLUME",
			TradeEndTime: "10:45:00",
			Parameters: map[string]interface{}{
				"min_candles_to_ignore": 1,
			},
		}
		events, diags := analyzer.replayLowVolume(sym, candles, summary, nil, appCfg)
		if len(events) < 3 {
			t.Fatalf("Audit: expected at least 3 events, got %d", len(events))
		}
		if diags[0].Status != "MASTER_ESTABLISHED" || diags[0].Verdict != "PASS" {
			t.Errorf("Audit diags[0] mismatch: got %s/%s", diags[0].Status, diags[0].Verdict)
		}
		if diags[1].Status != "CONFIRMATION_ARMED" || diags[1].Verdict != "PASS" {
			t.Errorf("Audit diags[1] mismatch: got %s/%s", diags[1].Status, diags[1].Verdict)
		}
		if diags[2].Status != "BREAKOUT_TRIGGERED" || diags[2].Verdict != "PASS" {
			t.Errorf("Audit diags[2] mismatch: got %s/%s", diags[2].Status, diags[2].Verdict)
		}
	})

	// =========================================================================
	// 3. FAKE_BREAKOUT SYNCHRONIZATION
	// =========================================================================
	t.Run("FAKE_BREAKOUT_Sync", func(t *testing.T) {
		fbEngine := NewFakeBreakoutEngine(logger, 4.0, 8.0, 4.0, 8.0, 1.0, 40.0)
		sym := "SBIN"
		fbEngine.SetPDHPDL(sym, 800.0, 780.0, 785.0)

		// Gap up 5.0% (Open 824.25 from PDClose 785.0), RED candle (Close < Open)
		c1 := data.Candle{Time: baseTime, Open: 824.25, High: 826.0, Low: 818.0, Close: 819.0, Volume: 80000}
		// Candle 2: RED & breaks Master Low (817.0 < 818.0), range < 1.0%
		c2 := data.Candle{Time: baseTime.Add(5 * time.Minute), Open: 819.0, High: 820.0, Low: 816.0, Close: 817.0, Volume: 50000}
		c3 := data.Candle{Time: baseTime.Add(10 * time.Minute), Open: 817.0, High: 818.0, Low: 814.0, Close: 815.0, Volume: 60000}
		candles := []data.Candle{c1, c2, c3}

		fbEngine.OnCandleClose(&c1, sym)
		fbEngine.mu.RLock()
		fbMaster := fbEngine.masterCandles[sym]
		fbEngine.mu.RUnlock()
		if fbMaster == nil {
			t.Fatal("Engine: expected Master candle to qualify for SELL setup")
		}

		fbEngine.OnCandleClose(&c2, sym)
		fbEngine.mu.RLock()
		fbConfirm := fbEngine.confirmationCandles[sym]
		fbEngine.mu.RUnlock()
		if fbConfirm == nil {
			t.Fatal("Engine: expected Confirmation candle to confirm SELL")
		}

		signal := fbEngine.CheckBreakout(sym, 815.50, "SELL")
		if signal == nil || signal.Action != "SELL" {
			t.Fatalf("Engine: expected SELL signal on breakdown below Confirmation Low, got %+v", signal)
		}

		summary := StockDaySummary{Open: 824.25, High: 826.0, Low: 814.0, Close: 815.0, PDH: 800.0, PDL: 780.0, PDClose: 785.0}
		appCfg := AppliedStrategyConfig{
			StrategyName: "FAKE_BREAKOUT",
			TradeEndTime: "11:00:00",
			Parameters: map[string]interface{}{
				"gap_up_min_pct":      4.0,
				"gap_up_max_pct":      8.0,
				"gap_down_min_pct":    4.0,
				"gap_down_max_pct":    8.0,
				"confirm_max_pct":     1.0,
				"master_max_wick_pct": 40.0,
			},
		}
		events := analyzer.replayFakeBreakout(sym, candles, summary, nil, appCfg)
		if len(events) < 3 {
			t.Fatalf("Audit: expected 3 events (SETUP_FORMED, CONFIRMATION_ARMED, TRADE_TAKEN), got %d", len(events))
		}
		if events[0].Stage != "SETUP_FORMED" || events[0].Direction != "SELL" {
			t.Errorf("Audit event[0] mismatch: got stage=%s dir=%s", events[0].Stage, events[0].Direction)
		}
		if events[1].Stage != "CONFIRMATION_ARMED" || events[1].Direction != "SELL" {
			t.Errorf("Audit event[1] mismatch: got stage=%s dir=%s", events[1].Stage, events[1].Direction)
		}
		if events[2].Stage != "TRADE_TAKEN" || events[2].Direction != "SELL" {
			t.Errorf("Audit event[2] mismatch: got stage=%s dir=%s", events[2].Stage, events[2].Direction)
		}
	})

	// =========================================================================
	// 4. VANDE_BHARAT_TRAP SYNCHRONIZATION
	// =========================================================================
	t.Run("VANDE_BHARAT_TRAP_Sync", func(t *testing.T) {
		vbtEngine := NewVandeBharatTrapEngine(logger, 3.0, 1.8, 0.5, 1.0, 40.0)
		sym := "TCS"
		vbtEngine.SetPreviousDayLevels(sym, 3500.0, 3400.0, 3450.0)

		// 1. Day 1st candle (09:15 AM IST) - RED candle closing above PDH (Fake Master)
		// Open=3520, Close=3508 (Red, Close > PDH), High=3525, Low=3505
		c1 := data.Candle{Time: baseTime, Open: 3520.0, High: 3525.0, Low: 3505.0, Close: 3508.0, Volume: 10000}
		// 2. Candle 2: Breaks Fake Master High (High 3535 > 3525) -> Genuine Master Candle!
		c2 := data.Candle{Time: baseTime.Add(5 * time.Minute), Open: 3515.0, High: 3535.0, Low: 3510.0, Close: 3532.0, Volume: 15000}
		// 3. Candle 3: 2nd Candle following Master (SL Anchor), range ~0.70%
		c3 := data.Candle{Time: baseTime.Add(10 * time.Minute), Open: 3520.0, High: 3540.0, Low: 3515.0, Close: 3538.0, Volume: 8000}
		// 4. Candle 4: Breakout trigger above c3 High (3542.0 > 3540.0)
		c4 := data.Candle{Time: baseTime.Add(15 * time.Minute), Open: 3538.0, High: 3545.0, Low: 3535.0, Close: 3543.0, Volume: 25000}
		candles := []data.Candle{c1, c2, c3, c4}

		vbtEngine.OnCandleClose(&c1, sym)
		vbtEngine.mu.RLock()
		fakeMaster := vbtEngine.fakeMasterCandles[sym]
		vbtEngine.mu.RUnlock()
		if fakeMaster == nil {
			t.Fatal("Engine: expected Fake Master candle to form")
		}

		vbtEngine.OnCandleClose(&c2, sym)
		vbtEngine.mu.RLock()
		genuineMaster := vbtEngine.masterCandles[sym]
		vbtEngine.mu.RUnlock()
		if genuineMaster == nil {
			t.Fatal("Engine: expected Genuine Master candle to form")
		}

		vbtEngine.OnCandleClose(&c3, sym)
		vbtEngine.mu.RLock()
		secondCandle := vbtEngine.secondCandles[sym]
		vbtEngine.mu.RUnlock()
		if secondCandle == nil {
			t.Fatal("Engine: expected 2nd candle (SL Anchor) to arm")
		}

		signal := vbtEngine.CheckBreakout(sym, 3542.0, "BUY")
		if signal == nil || signal.Action != "BUY" {
			t.Fatalf("Engine: expected BUY signal on trap breakout, got %+v", signal)
		}

		summary := StockDaySummary{Open: 3520.0, High: 3545.0, Low: 3505.0, Close: 3543.0, PDH: 3500.0, PDL: 3400.0, PDClose: 3450.0}
		appCfg := AppliedStrategyConfig{
			StrategyName: "VANDE_BHARAT_TRAP",
			TradeEndTime: "11:00:00",
			Parameters: map[string]interface{}{
				"fake_master_max_pct":     3.0,
				"genuine_master_max_pct":  1.8,
				"genuine_master_max_wick": 40.0,
				"sl_min_pct":              0.5,
				"sl_max_pct":              1.0,
			},
		}
		events := analyzer.replayVandeBharatTrap(sym, candles, summary, nil, appCfg)
		if len(events) < 4 {
			t.Fatalf("Audit: expected at least 4 events, got %d", len(events))
		}
		if events[0].Stage != "SETUP_FORMED" || events[0].Direction != "BUY" {
			t.Errorf("Audit event[0] mismatch: got stage=%s dir=%s", events[0].Stage, events[0].Direction)
		}
		if events[1].Stage != "SETUP_FORMED" || events[1].Direction != "BUY" {
			t.Errorf("Audit event[1] mismatch: got stage=%s dir=%s", events[1].Stage, events[1].Direction)
		}
		if events[2].Stage != "CONFIRMATION_ARMED" || events[2].Direction != "BUY" {
			t.Errorf("Audit event[2] mismatch: got stage=%s dir=%s", events[2].Stage, events[2].Direction)
		}
		if events[3].Stage != "TRADE_TAKEN" || events[3].Direction != "BUY" {
			t.Errorf("Audit event[3] mismatch: got stage=%s dir=%s", events[3].Stage, events[3].Direction)
		}
	})

	// =========================================================================
	// 5. EMAS5_BREAKOUT SYNCHRONIZATION
	// =========================================================================
	t.Run("EMAS5_BREAKOUT_Sync", func(t *testing.T) {
		es5Engine := NewEMAS5BreakoutEngine(logger, 2, 2, 0.2, 2.0, 1, 1.0)
		sym := "DLF"
		es5Engine.SetPreviousDayLevels(sym, 680.0, 670.0, 675.0)
		es5Engine.SetMinPDHPDLRetracePct(0.0)
		es5Engine.SetEMATouchBufferPct(0.20)
		es5Engine.SetArcBounceTolerancePct(0.30)
		es5Engine.SetMaxEntryDistancePct(0.35)

		var allCandles []data.Candle
		var todayCandles []data.Candle

		// 30 historical warmup candles below EMA
		for i := 0; i < 30; i++ {
			cTime := baseTime.Add(-time.Duration(30-i) * 5 * time.Minute)
			c := data.Candle{
				Token:  12345,
				Time:   cTime,
				Open:   680.0,
				High:   681.0,
				Low:    678.0,
				Close:  679.0,
				Volume: 1000,
			}
			allCandles = append(allCandles, c)
		}

		// 5-candle U-shape
		c1 := data.Candle{Time: baseTime, Open: 679.0, High: 680.0, Low: 676.0, Close: 677.0, Volume: 2000}
		c2 := data.Candle{Time: baseTime.Add(5 * time.Minute), Open: 677.0, High: 678.0, Low: 675.0, Close: 676.0, Volume: 2000}
		c3 := data.Candle{Time: baseTime.Add(10 * time.Minute), Open: 676.0, High: 677.0, Low: 674.30, Close: 675.50, Volume: 2000} // Lowest Low
		c4 := data.Candle{Time: baseTime.Add(15 * time.Minute), Open: 675.50, High: 678.0, Low: 675.0, Close: 677.50, Volume: 2500}
		c5 := data.Candle{Time: baseTime.Add(20 * time.Minute), Open: 677.50, High: 681.0, Low: 677.0, Close: 680.50, Volume: 5000} // Master Candle
		c6 := data.Candle{Time: baseTime.Add(25 * time.Minute), Open: 680.50, High: 682.0, Low: 680.20, Close: 681.50, Volume: 4000} // Confirmation
		c7 := data.Candle{Time: baseTime.Add(30 * time.Minute), Open: 681.50, High: 683.0, Low: 681.20, Close: 682.50, Volume: 6000} // Breakout trigger

		todayPart := []data.Candle{c1, c2, c3, c4, c5, c6, c7}
		for _, c := range todayPart {
			allCandles = append(allCandles, c)
			todayCandles = append(todayCandles, c)
		}

		// Process through engine
		for _, c := range allCandles {
			es5Engine.ProcessCandle(sym, c)
		}
		es5Engine.mu.RLock()
		es5Master := es5Engine.masterCandles[sym]
		es5Confirm := es5Engine.confirmationCandles[sym]
		es5Engine.mu.RUnlock()
		if es5Master == nil {
			t.Fatal("Engine: expected Master candle to be established")
		}
		if es5Confirm == nil {
			t.Fatal("Engine: expected Confirmation candle to be established")
		}

		signal := es5Engine.CheckBreakout(sym, 682.20, "BUY")
		if signal == nil || signal.Action != "BUY" {
			t.Fatalf("Engine: expected BUY signal, got %+v", signal)
		}

		// Replay in AuditAnalyzer
		summary := StockDaySummary{Open: 679.0, High: 683.0, Low: 674.30, Close: 682.50, PDH: 680.0, PDL: 670.0, PDClose: 675.0}
		appCfg := AppliedStrategyConfig{
			StrategyName: "EMAS5_BREAKOUT",
			TradeEndTime: "14:30:30",
			Parameters: map[string]interface{}{
				"rally_candles":             2,
				"min_rebound_pct":           0.50,
				"master_max_pct":            2.0,
				"master_max_wick_pct":       40.0,
				"max_inside_candles":        1,
				"confirm_max_pct":           1.0,
				"ema_touch_buffer_pct":      0.20,
				"min_pdh_pdl_retrace_pct":   0.0,
				"arc_bounce_tolerance_pct":  0.30,
				"max_entry_distance_pct":    0.35,
			},
		}

		events, diags := analyzer.replayEMAS5(sym, allCandles, todayCandles, summary, nil, appCfg)
		if len(events) < 3 {
			t.Fatalf("Audit: expected at least 3 events (MASTER_FORMED, CONFIRMATION_ARMED, TRADE_TAKEN), got %d", len(events))
		}

		var foundMaster, foundConfirm, foundBreakout bool
		for _, d := range diags {
			if d.Status == "MASTER_ESTABLISHED" && d.Verdict == "PASS" {
				foundMaster = true
			}
			if d.Status == "CONFIRMATION_ARMED" && d.Verdict == "PASS" {
				foundConfirm = true
			}
			if (d.Status == "BREAKOUT_TRIGGERED" || d.Status == "BREAKOUT_TRIGGER") && (d.Verdict == "PASS" || d.Verdict == "ENTRY") {
				foundBreakout = true
			}
		}
		if !foundMaster || !foundConfirm || !foundBreakout {
			t.Errorf("Audit diags missing states: master=%v confirm=%v breakout=%v", foundMaster, foundConfirm, foundBreakout)
		}
	})
}
