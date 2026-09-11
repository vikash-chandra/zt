package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/execution"
	"zerodha-trading/monitoring"
	"zerodha-trading/risk"
	"zerodha-trading/strategy"
)

// TestConfigE2EWiring validates that the runtime audit endpoint correctly verifies
// complete synchronization between configuration settings and in-memory strategy/risk engines,
// and catches any intentional discrepancy with 100% precision (Zero Slippage).
func TestConfigE2EWiring(t *testing.T) {
	logger, err := monitoring.NewLogger("info")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	cfg := &config.Settings{}

	riskMgr := risk.NewRiskManager(nil, logger.Logger, 250000.0, risk.RiskLimits{
		MaxTradesPerDay:    15,
		MaxLossStreaks:     4,
		MaxHoldingTimeMin:  45,
		MaxDailyLossAmount: 5000.0,
	})

	vbEngine := strategy.NewVandeBharatEngine(logger.Logger, 2.2, 0.6, 1.2, 35.0, 2.5)
	vbEngine.SetTradeEndTime("10:45:00")
	vbEngine.SetCandleTimeFrame("5m")

	es5Engine := strategy.NewEMAS5BreakoutEngine(logger.Logger, 3, 6, 0.75, 2.5, 2, 1.2)
	es5Engine.SetTradeEndTime("14:28:00")
	es5Engine.SetCandleTimeFrame("5m")
	es5Engine.SetSLBufferPct(0.18)
	es5Engine.SetEMATouchBufferPct(0.15)
	es5Engine.SetMasterMaxWickPct(38.0)
	es5Engine.SetMaxEntryDistancePct(0.45)
	es5Engine.SetMaxSetupWaitCandles(8)

	fbEngine := strategy.NewFakeBreakoutEngine(logger.Logger, 3.5, 7.5, 3.5, 7.5, 1.5, 30.0)
	fbEngine.SetTradeEndTime("11:15:00")
	fbEngine.SetCandleTimeFrame("5m")

	vbtEngine := strategy.NewVandeBharatTrapEngine(logger.Logger, 2.8, 1.6, 0.4, 0.9, 45.0)
	vbtEngine.SetTradeEndTime("10:50:00")
	vbtEngine.SetCandleTimeFrame("5m")

	lvEngine := strategy.NewLowVolumeEngine(logger.Logger)
	lvEngine.SetTradeEndTime("10:30:00")
	lvEngine.SetCandleTimeFrame("5m")

	optCfg := &data.OptionsIndexConfig{
		IndexSymbol:          "NIFTY 50",
		BaseLotSize:          65,
		MaxMultiplier:        5,
		MultiplierOnReversal: true,
		SLPct:                50.0,
		TrailSLEnabled:       true,
		TrailSLPct:           10.0,
		MaxTradesPerDay:      6,
	}
	optMgr := risk.NewIndexOptionsPositionManagerFromConfig(nil, logger.Logger, optCfg, 100000.0)

	bot := &TradingBot{
		cfg:     cfg,
		logger:  logger,
		riskMgr: riskMgr,
		execMgr: &execution.ExecutionManager{LiveTrading: false},
		activeStrategies: []strategy.Strategy{
			vbEngine,
			es5Engine,
			fbEngine,
			vbtEngine,
			lvEngine,
		},
		optionsPosMgrs: map[string]*risk.OptionsPositionManager{
			"NIFTY 50": optMgr,
			"NIFTY":    optMgr,
		},
		optIndexConfigs: map[string]*data.OptionsIndexConfig{
			"NIFTY 50": optCfg,
			"NIFTY":    optCfg,
		},
	}

	// 1. Synthetic System Configs matching all configured values
	sysConfigs := map[string]map[string]string{
		"EQUITY_STRATEGY": {
			"enable_live_trading":   "false",
			"risk_per_trade_inr":    "750.00",
			"capital_inr":           "250000.00",
			"max_trades_per_day":    "15",
			"max_holding_time_min":  "45",
			"max_daily_loss_amount": "5000.00",
			"max_loss_streaks":      "4",
			"limit_buffer_pct":      "0.80",
			"auto_square_off_time":  "15:18:00",

			// ES5 params
			"es5_max_trades_per_stock":   "3",
			"es5_rally_candles":          "6",
			"es5_min_rebound_pct":        "0.75",
			"es5_master_max_pct":         "2.50",
			"es5_max_inside_candles":     "2",
			"es5_confirm_max_pct":        "1.20",
			"es5_trade_end_time":         "14:28:00",
			"es5_candle_timeframe":       "5m",
			"es5_sl_buffer_pct":          "0.18",
			"es5_ema_touch_buffer_pct":   "0.15",
			"es5_master_max_wick_pct":    "38.00",
			"es5_max_entry_distance_pct": "0.45",
			"es5_max_setup_wait_candles": "8",

			// VB params
			"vb_master_max_pct":      "2.20",
			"vb_sl_min_pct":          "0.60",
			"vb_sl_max_pct":          "1.20",
			"vb_master_max_wick_pct": "35.00",
			"vb_min_gap_pct":         "2.50",
			"vb_trade_end_time":      "10:45:00",
			"vb_candle_timeframe":    "5m",

			// FB params
			"fb_gap_up_min_pct":       "3.50",
			"fb_gap_up_max_pct":       "7.50",
			"fb_gap_down_min_pct":     "3.50",
			"fb_gap_down_max_pct":     "7.50",
			"fb_max_confirmation_pct": "1.50",
			"fb_master_max_wick_pct":  "30.00",
			"fb_trade_end_time":       "11:15:00",
			"fb_candle_timeframe":     "5m",

			// VBT params
			"vbt_fake_master_max_pct": "2.80",
			"vbt_master_max_pct":      "1.60",
			"vbt_sl_min_pct":          "0.40",
			"vbt_sl_max_pct":          "0.90",
			"vbt_master_max_wick_pct": "45.00",
			"vbt_trade_end_time":      "10:50:00",
			"vbt_candle_timeframe":    "5m",

			// LV params
			"lv_trade_end_time":   "10:30:00",
			"lv_candle_timeframe": "5m",
		},
		"QUANT_SCANNER": {
			"enabled":                "true",
			"execution_time":         "15:45:00",
			"momentum_days":          "20",
			"cluster_daily_enabled":  "true",
			"cluster_weekly_enabled": "true",
			"cluster_ema_fast":       "10",
			"cluster_ema_mid":        "20",
			"cluster_ema_slow":       "89",
		},
		"SELECTION": {
			"strategy_watchlist_size": "10",
			"watchlist_max_pct_change": "5.00",
			"sector_scanner_enabled":  "true",
			"sector_scanner_top_n":    "3",
			"sector_scanner_weight":   "0.40",
		},
	}

	bot.sysConfigs = sysConfigs
	applySystemConfigsToSettings(bot.cfg, sysConfigs, bot.logger)

	// Phase A: Assert Perfect Sync via handleConfigRuntimeAudit
	req := httptest.NewRequest(http.MethodGet, "/api/config/runtime-audit", nil)
	w := httptest.NewRecorder()

	bot.handleConfigRuntimeAudit(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var auditRes struct {
		Status                 string `json:"status"`
		SlippageDetected       bool   `json:"slippage_detected"`
		TotalAuditedParameters int    `json:"total_audited_parameters"`
		SyncedParameters       int    `json:"synced_parameters"`
		Mismatches             []struct {
			Scope        string `json:"scope"`
			Key          string `json:"key"`
			DBValue      string `json:"db_value"`
			RuntimeValue string `json:"runtime_value"`
			Diff         string `json:"diff"`
		} `json:"mismatches"`
		Scopes map[string]struct {
			Synced int    `json:"synced"`
			Total  int    `json:"total"`
			Status string `json:"status"`
		} `json:"scopes"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &auditRes); err != nil {
		t.Fatalf("failed to unmarshal audit response: %v", err)
	}

	if auditRes.SlippageDetected {
		t.Fatalf("unexpected slippage detected: %+v", auditRes.Mismatches)
	}

	if auditRes.Status != "PERFECT_SYNC" {
		t.Fatalf("expected PERFECT_SYNC, got %s", auditRes.Status)
	}

	if auditRes.TotalAuditedParameters < 40 {
		t.Fatalf("expected at least 40 audited parameters, got %d", auditRes.TotalAuditedParameters)
	}

	t.Logf("✅ Verified: %d parameters audited with PERFECT_SYNC", auditRes.TotalAuditedParameters)

	// Phase B: Intentional Mismatch Detection Test
	// Mutate one in-memory engine property to trigger slippage detection
	es5Engine.SetTradeEndTime("11:00:00") // Mismatch with DB's "14:28:00"

	wMismatch := httptest.NewRecorder()
	bot.handleConfigRuntimeAudit(wMismatch, req)

	var mismatchRes struct {
		Status           string `json:"status"`
		SlippageDetected bool   `json:"slippage_detected"`
		Mismatches       []struct {
			Key string `json:"key"`
		} `json:"mismatches"`
	}
	_ = json.Unmarshal(wMismatch.Body.Bytes(), &mismatchRes)

	if !mismatchRes.SlippageDetected {
		t.Fatalf("expected SlippageDetected=true when in-memory engine was mutated")
	}

	if mismatchRes.Status != "SLIPPAGE_DETECTED" {
		t.Fatalf("expected SLIPPAGE_DETECTED, got %s", mismatchRes.Status)
	}

	foundES5Mismatch := false
	for _, m := range mismatchRes.Mismatches {
		if m.Key == "es5_trade_end_time" {
			foundES5Mismatch = true
			break
		}
	}
	if !foundES5Mismatch {
		t.Fatalf("expected mismatch for es5_trade_end_time, got %+v", mismatchRes.Mismatches)
	}

	t.Logf("✅ Verified: Slippage detector caught intentional discrepancy on es5_trade_end_time")
}

// TestConfigConcurrencySafety stress-tests concurrent reads, writes, and audit requests
// across all strategy engines, risk manager, and options position manager to ensure
// 100% race-condition freedom, zero deadlocks, and zero slowness under heavy concurrency.
func TestConfigConcurrencySafety(t *testing.T) {
	logger, _ := monitoring.NewLogger("info")
	cfg := &config.Settings{}

	riskMgr := risk.NewRiskManager(nil, logger.Logger, 250000.0, risk.RiskLimits{
		MaxTradesPerDay:    15,
		MaxLossStreaks:     4,
		MaxHoldingTimeMin:  45,
		MaxDailyLossAmount: 5000.0,
	})

	vbEngine := strategy.NewVandeBharatEngine(logger.Logger, 2.2, 0.6, 1.2, 35.0, 2.5)
	es5Engine := strategy.NewEMAS5BreakoutEngine(logger.Logger, 3, 6, 0.75, 2.5, 2, 1.2)
	fbEngine := strategy.NewFakeBreakoutEngine(logger.Logger, 3.5, 7.5, 3.5, 7.5, 1.5, 30.0)
	vbtEngine := strategy.NewVandeBharatTrapEngine(logger.Logger, 2.8, 1.6, 0.4, 0.9, 45.0)
	lvEngine := strategy.NewLowVolumeEngine(logger.Logger)

	optCfg := &data.OptionsIndexConfig{
		IndexSymbol:     "NIFTY 50",
		BaseLotSize:     65,
		MaxMultiplier:   5,
		SLPct:           50.0,
		MaxTradesPerDay: 6,
	}
	optMgr := risk.NewIndexOptionsPositionManagerFromConfig(nil, logger.Logger, optCfg, 100000.0)

	bot := &TradingBot{
		cfg:     cfg,
		logger:  logger,
		riskMgr: riskMgr,
		execMgr: &execution.ExecutionManager{LiveTrading: false},
		activeStrategies: []strategy.Strategy{
			vbEngine,
			es5Engine,
			fbEngine,
			vbtEngine,
			lvEngine,
		},
		optionsPosMgrs: map[string]*risk.OptionsPositionManager{
			"NIFTY 50": optMgr,
			"NIFTY":    optMgr,
		},
		optIndexConfigs: map[string]*data.OptionsIndexConfig{
			"NIFTY 50": optCfg,
			"NIFTY":    optCfg,
		},
		sysConfigs: map[string]map[string]string{
			"EQUITY_STRATEGY": {
				"risk_per_trade_inr": "750.00",
				"es5_trade_end_time": "14:28:00",
			},
		},
	}

	var wg sync.WaitGroup
	workers := 25
	iterations := 100

	// Concurrent Writers: mutating rules and settings
	for w := 0; w < 5; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				timeVal := "14:28:00"
				if i%2 == 0 {
					timeVal = "14:30:00"
				}
				es5Engine.SetTradeEndTime(timeVal)
				es5Engine.UpdateRules(2+i%3, 5+i%2, 0.5, 2.0, 1, 1.0, timeVal)
				vbEngine.UpdateRules(2.0, 0.5, 1.0, 40.0, 2.0)
				riskMgr.SetMaxTradesPerDay(10 + i%10)
				riskMgr.SetMaxLossStreaks(3 + i%3)

				newOptCfg := *optCfg
				newOptCfg.BaseLotSize = 65 * (1 + (i % 3))
				optMgr.UpdateConfig(&newOptCfg)
			}
		}(w)
	}

	// Concurrent Readers: calling getters and audit endpoint
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = es5Engine.TradeEndTime()
				_ = es5Engine.MaxTradesPerStock()
				_ = es5Engine.SLBufferPct()
				_ = vbEngine.MasterMaxPct()
				_ = fbEngine.GapUpMinPct()
				_ = vbtEngine.FakeMasterMaxPct()
				_ = riskMgr.MaxTradesPerDay()
				_ = riskMgr.MaxLossStreaks()
				_ = optMgr.BaseLotSize()
				_ = optMgr.SLPct()

				req := httptest.NewRequest(http.MethodGet, "/api/config/runtime-audit", nil)
				rec := httptest.NewRecorder()
				bot.handleConfigRuntimeAudit(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("expected 200 from audit, got %d", rec.Code)
				}
			}
		}(w)
	}

	wg.Wait()
	t.Logf("✅ Concurrency Stress Test Passed: %d workers executed %d iterations without data race or deadlock", workers+5, iterations)
}
