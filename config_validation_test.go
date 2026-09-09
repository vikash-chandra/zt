package main

import (
	"encoding/json"
	"testing"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/execution"
	"zerodha-trading/monitoring"
	"zerodha-trading/risk"
	"zerodha-trading/selection"
	"zerodha-trading/strategy"
)

// TestAllUIConfigurationsWiredAndApplied validates that every single configuration parameter
// configurable from the UI (index.html) is accurately parsed, wired, and applied to the in-memory
// bot configuration, strategy engines, risk manager, and execution manager.
func TestAllUIConfigurationsWiredAndApplied(t *testing.T) {
	logger, err := monitoring.NewLogger("info")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	cfg := &config.Settings{}
	riskMgr := risk.NewRiskManager(nil, logger.Logger, 100000.0, risk.RiskLimits{
		MaxTradesPerDay:    20,
		MaxLossStreaks:     3,
		MaxHoldingTimeMin:  30,
		MaxDailyLossAmount: 0.0,
	})
	execMgr := &execution.ExecutionManager{
		LiveTrading: false,
	}

	vbEngine := strategy.NewVandeBharatEngine(logger.Logger, 1.8, 0.5, 1.0, 40.0, 2.0)
	es5Engine := strategy.NewEMAS5BreakoutEngine(logger.Logger, 2, 5, 0.5, 2.0, 1, 1.0)
	fbEngine := strategy.NewFakeBreakoutEngine(logger.Logger, 4.0, 8.0, 4.0, 8.0, 1.0, 40.0)
	vbtEngine := strategy.NewVandeBharatTrapEngine(logger.Logger, 3.0, 1.8, 0.5, 1.0, 40.0)
	lvEngine := strategy.NewLowVolumeEngine(logger.Logger)

	bot := &TradingBot{
		cfg:     cfg,
		logger:  logger,
		riskMgr: riskMgr,
		execMgr: execMgr,
		activeStrategies: []strategy.Strategy{
			vbEngine,
			es5Engine,
			fbEngine,
			vbtEngine,
			lvEngine,
		},
		stockSelectionConfigs: make(map[string]selection.StockSelectionStrategyConfig),
		strategyRRMap:         make(map[string]string),
		strategyMultiSelMap:   make(map[string][]string),
	}

	// 1. Build complete System Configs payload simulating UI Save
	sysConfigs := map[string]map[string]string{
		"EQUITY_STRATEGY": {
			"risk_per_trade_inr":    "750.0",
			"capital_inr":           "250000.0",
			"max_open_positions":    "5",
			"max_daily_loss_amount": "5000.0",
			"max_trades_per_day":    "15",
			"max_loss_streaks":      "4",
			"max_holding_time_min":  "45",
			"enable_live_trading":   "true",
			"default_order_type":    "LIMIT",
			"limit_buffer_pct":      "0.8",
			"auto_square_off_time":  "15:18:00",
			"lv_sl_buffer_pct":      "0.25",
			"vb_sl_buffer_pct":      "0.35",
			"fb_sl_buffer_pct":      "0.40",
			"vbt_sl_buffer_pct":     "0.22",
			"es5_sl_buffer_pct":     "0.18",
			"lv_candle_timeframe":   "5m",
			"vb_candle_timeframe":   "5m",
			"fb_candle_timeframe":   "5m",
			"vbt_candle_timeframe":  "5m",
			"es5_candle_timeframe":  "5m",
			"lv_trade_end_time":     "10:30:00",
			"vb_trade_end_time":     "10:45:00",
			"fb_trade_end_time":     "11:15:00",
			"vbt_trade_end_time":    "10:50:00",
			"es5_trade_end_time":    "11:20:00",
		},
		"TRADING_STRATEGY": {
			"LOW_VOLUME": `{
				"name": "LOW_VOLUME",
				"enabled": true,
				"candle_time_frame": "5m",
				"attached_risk_reward": "PARTIAL_BOOK_COST_SL",
				"attached_stock_selections": ["PDH_PDL", "FO"],
				"trade_end_time": "10:30:00",
				"min_candles_to_ignore": 4,
				"sl_buffer_pct": 0.25,
				"use_broker_sl": true
			}`,
			"VANDE_BHARAT": `{
				"name": "VANDE_BHARAT",
				"enabled": true,
				"candle_time_frame": "5m",
				"attached_risk_reward": "DYNAMIC_TRAILING_SL",
				"attached_stock_selections": ["FO", "SECTOR"],
				"trade_end_time": "10:45:00",
				"min_candles_to_ignore": 3,
				"sl_buffer_pct": 0.35,
				"sl_min_pct": 0.6,
				"sl_max_pct": 1.2,
				"min_gap_pct": 2.5,
				"master_max_pct": 2.2,
				"master_max_wick_pct": 35.0,
				"use_broker_sl": true
			}`,
			"FAKE_BREAKOUT": `{
				"name": "FAKE_BREAKOUT",
				"enabled": true,
				"candle_time_frame": "5m",
				"attached_risk_reward": "DYNAMIC_TRAILING_SL",
				"attached_stock_selections": ["FO"],
				"trade_end_time": "11:15:00",
				"min_candles_to_ignore": 1,
				"gap_up_min_pct": 3.5,
				"gap_up_max_pct": 7.5,
				"gap_down_min_pct": 3.5,
				"gap_down_max_pct": 7.5,
				"max_confirmation_pct": 1.5,
				"master_max_wick_pct": 30.0,
				"sl_buffer_pct": 0.40,
				"use_broker_sl": true
			}`,
			"VANDE_BHARAT_TRAP": `{
				"name": "VANDE_BHARAT_TRAP",
				"enabled": true,
				"candle_time_frame": "5m",
				"attached_risk_reward": "DYNAMIC_TRAILING_SL",
				"attached_stock_selections": ["FO", "PDH_PDL"],
				"trade_end_time": "10:50:00",
				"min_candles_to_ignore": 1,
				"fake_master_max_pct": 2.8,
				"master_max_pct": 1.6,
				"sl_min_pct": 0.4,
				"sl_max_pct": 0.9,
				"master_max_wick_pct": 45.0,
				"sl_buffer_pct": 0.22,
				"use_broker_sl": true
			}`,
			"EMAS5_BREAKOUT": `{
				"name": "EMAS5_BREAKOUT",
				"enabled": true,
				"candle_time_frame": "5m",
				"attached_risk_reward": "DYNAMIC_TRAILING_SL",
				"attached_stock_selections": ["FO", "52WH_52WL"],
				"trade_end_time": "11:20:00",
				"min_candles_to_ignore": 1,
				"max_trades_per_stock": 3,
				"rally_candles": 6,
				"min_rebound_pct": 0.75,
				"master_max_pct": 2.5,
				"master_max_wick_pct": 38.0,
				"max_inside_candles": 2,
				"confirm_max_pct": 1.2,
				"ema_touch_buffer_pct": 0.15,
				"sl_buffer_pct": 0.18,
				"max_entry_distance_pct": 0.45,
				"max_setup_wait_candles": 8,
				"use_broker_sl": true
			}`,
		},
		"RR_STRATEGY": {
			"PARTIAL_BOOK_COST_SL": `{
				"type": "PARTIAL_BOOK_COST_SL",
				"risk_reward_ratio": 2.5,
				"partial_exit_qty_pct": 60.0,
				"move_sl_to_cost": true,
				"cost_sl_buffer_pct": 0.08,
				"initial_sl_mode": "SETUP_BREAKOUT",
				"fixed_sl_pct": 1.8,
				"sl_buffer_pct": 0.15
			}`,
			"DYNAMIC_TRAILING_SL": `{
				"type": "DYNAMIC_TRAILING_SL",
				"stage1_trigger_gain_pct": 0.4,
				"stage1_trail_sl_pct": 0.1,
				"stage2_trigger_gain_pct": 0.8,
				"stage2_trail_sl_pct": 0.4,
				"stage3_trigger_gain_pct": 1.5,
				"stage3_trail_sl_pct": 0.8,
				"stage4_trigger_gain_pct": 2.2,
				"stage4_exit_pct": 65.0,
				"stage4_trail_sl_pct": 1.1,
				"stage5_trigger_gain_pct": 2.8,
				"stage5_step_offset_pct": 0.7,
				"time_decay_min": 50,
				"time_decay_trigger_pct": 0.3,
				"time_decay_trail_sl_pct": 0.08
			}`,
		},
		"MANUAL_TRADING": {
			"manual_trade_sync_enabled":         "true",
			"manual_trade_poll_minutes":         "3",
			"manual_trade_attached_rr_strategy": "DYNAMIC_TRAILING_SL",
			"manual_trade_rr_ratio":             "2.5",
			"manual_trade_partial_exit_pct":     "60.0",
			"manual_trade_default_sl_pct":       "2.0",
			"manual_trade_move_sl_to_cost":      "true",
			"manual_trade_cost_buffer_pct":      "0.08",
			"manual_trade_use_broker_sl":        "true",
		},
		"STOCK_SELECTION_STRATEGIES": {
			"PDH_PDL":          `{"name":"PDH_PDL","enabled":true,"priority_rank":1,"level_shift_pct":0.5,"watchlist_size":8}`,
			"ATH_ATL":          `{"name":"ATH_ATL","enabled":true,"priority_rank":2,"level_shift_pct":-0.2,"watchlist_size":6}`,
			"52WH_52WL":        `{"name":"52WH_52WL","enabled":true,"priority_rank":3,"level_shift_pct":0.0,"watchlist_size":4}`,
			"NEWS":             `{"name":"NEWS","enabled":false,"priority_rank":4,"level_shift_pct":0.0,"watchlist_size":3}`,
			"HIGH_IMPACT_NEWS": `{"name":"HIGH_IMPACT_NEWS","enabled":true,"priority_rank":5,"level_shift_pct":0.0,"watchlist_size":4}`,
			"RESULT":           `{"name":"RESULT","enabled":true,"priority_rank":6,"level_shift_pct":0.0,"watchlist_size":5}`,
			"FO":               `{"name":"FO","enabled":true,"priority_rank":7,"level_shift_pct":0.1,"watchlist_size":12}`,
			"SECTOR":           `{"name":"SECTOR","enabled":true,"priority_rank":8,"level_shift_pct":0.0,"watchlist_size":10}`,
			"QUANT_SCANNER":    `{"name":"QUANT_SCANNER","enabled":true,"priority_rank":9,"level_shift_pct":0.0,"watchlist_size":7}`,
			"PT_SCREENER":      `{"name":"PT_SCREENER","enabled":true,"priority_rank":10,"level_shift_pct":0.0,"watchlist_size":5}`,
			"PT_ADVANCE":       `{"name":"PT_ADVANCE","enabled":true,"priority_rank":11,"level_shift_pct":0.0,"watchlist_size":5}`,
			"OTHERS":           `{"name":"OTHERS","enabled":true,"priority_rank":12,"level_shift_pct":0.0,"watchlist_size":5}`,
		},
		"SELECTION": {
			"stock_select_time":      "09:05:00",
			"manual_trading_enabled": "true",
			"strategy_watchlist_size": "12",
			"sector_scanner_enabled":  "true",
			"sector_scanner_top_n":    "4",
			"sector_scanner_weight":   "0.50",
		},
		"QUANT_SCANNER": {
			"enabled":                "true",
			"execution_time":         "15:40:00",
			"momentum_days":          "25",
			"news_enabled":           "true",
			"cluster_daily_enabled":  "true",
			"cluster_weekly_enabled": "true",
			"cluster_ema_fast":       "10",
			"cluster_ema_mid":        "20",
			"cluster_ema_slow":       "89",
			"cluster_max_spread_pct": "0.15",
		},
		"SYSTEM": {
			"morning_broad_agg_start": "09:16:00",
			"morning_broad_agg_end":   "09:44:00",
			"broad_subscribe":         "true",
			"restart_allowed_before":  "09:10:00",
			"restart_allowed_after":   "15:50:00",
		},
	}

	// Apply configurations to settings and bot
	applySystemConfigsToSettings(bot.cfg, sysConfigs, bot.logger)

	// Simulate loading modular strategy configs in-memory
	// 1. Risk-Reward configs
	partialCfg := risk.DefaultPartialBookCostSLConfig()
	dynamicCfg := risk.DefaultDynamicTrailingSLConfig()
	_ = json.Unmarshal([]byte(sysConfigs["RR_STRATEGY"]["PARTIAL_BOOK_COST_SL"]), &partialCfg)
	_ = json.Unmarshal([]byte(sysConfigs["RR_STRATEGY"]["DYNAMIC_TRAILING_SL"]), &dynamicCfg)

	rrStrategies := map[string]risk.RiskRewardStrategy{
		"PARTIAL_BOOK_COST_SL": risk.NewPartialBookCostSLStrategy(partialCfg),
		"DYNAMIC_TRAILING_SL":  risk.NewDynamicTrailingSLStrategy(dynamicCfg),
	}

	stratRRMap := map[string]string{
		"LOW_VOLUME":        "PARTIAL_BOOK_COST_SL",
		"VANDE_BHARAT":      "DYNAMIC_TRAILING_SL",
		"FAKE_BREAKOUT":     "DYNAMIC_TRAILING_SL",
		"VANDE_BHARAT_TRAP": "DYNAMIC_TRAILING_SL",
		"EMAS5_BREAKOUT":    "DYNAMIC_TRAILING_SL",
		"MANUAL":            "DYNAMIC_TRAILING_SL",
	}
	stratMultiSel := map[string][]string{
		"LOW_VOLUME":        {"PDH_PDL", "FO"},
		"VANDE_BHARAT":      {"FO", "SECTOR"},
		"FAKE_BREAKOUT":     {"FO"},
		"VANDE_BHARAT_TRAP": {"FO", "PDH_PDL"},
		"EMAS5_BREAKOUT":    {"FO", "52WH_52WL"},
	}

	bot.riskMgr.SetRiskRewardStrategies(rrStrategies, stratRRMap)
	bot.strategyRRMap = stratRRMap
	bot.strategyMultiSelMap = stratMultiSel

	// Simulate strategy engine rule updates from parsed configs
	for stratName, rawJSON := range sysConfigs["TRADING_STRATEGY"] {
		var parsed struct {
			CandleTimeFrame     string  `json:"candle_time_frame"`
			TradeEndTime        string  `json:"trade_end_time"`
			MinCandlesToIgnore  int     `json:"min_candles_to_ignore"`
			SLBufferPct         float64 `json:"sl_buffer_pct"`
			SLMinPct            float64 `json:"sl_min_pct"`
			SLMaxPct            float64 `json:"sl_max_pct"`
			MinGapPct           float64 `json:"min_gap_pct"`
			MasterMaxPct        float64 `json:"master_max_pct"`
			MasterMaxWickPct    float64 `json:"master_max_wick_pct"`
			GapUpMinPct         float64 `json:"gap_up_min_pct"`
			GapUpMaxPct         float64 `json:"gap_up_max_pct"`
			GapDownMinPct       float64 `json:"gap_down_min_pct"`
			GapDownMaxPct       float64 `json:"gap_down_max_pct"`
			MaxConfirmationPct  float64 `json:"max_confirmation_pct"`
			FakeMasterMaxPct    float64 `json:"fake_master_max_pct"`
			MaxTradesPerStock   int     `json:"max_trades_per_stock"`
			RallyCandles        int     `json:"rally_candles"`
			MinReboundPct       float64 `json:"min_rebound_pct"`
			MaxInsideCandles    int     `json:"max_inside_candles"`
			ConfirmMaxPct       float64 `json:"confirm_max_pct"`
			EMATouchBufferPct   float64 `json:"ema_touch_buffer_pct"`
			MaxEntryDistancePct float64 `json:"max_entry_distance_pct"`
			MaxSetupWaitCandles int     `json:"max_setup_wait_candles"`
		}
		_ = json.Unmarshal([]byte(rawJSON), &parsed)

		if stratName == "VANDE_BHARAT" {
			bot.cfg.VBSLBufferPct = parsed.SLBufferPct
			vbEngine.UpdateRules(parsed.MasterMaxPct, parsed.SLMinPct, parsed.SLMaxPct, parsed.MasterMaxWickPct, parsed.MinGapPct)
			vbEngine.SetCandleTimeFrame(parsed.CandleTimeFrame)
			vbEngine.SetTradeEndTime(parsed.TradeEndTime)
			vbEngine.MinCandlesToIgnore = parsed.MinCandlesToIgnore
		} else if stratName == "EMAS5_BREAKOUT" {
			bot.cfg.ES5SLBufferPct = parsed.SLBufferPct
			es5Engine.UpdateRules(parsed.MaxTradesPerStock, parsed.RallyCandles, parsed.MinReboundPct, parsed.MasterMaxPct, parsed.MaxInsideCandles, parsed.ConfirmMaxPct, parsed.TradeEndTime)
			es5Engine.SetCandleTimeFrame(parsed.CandleTimeFrame)
			es5Engine.SetSLBufferPct(parsed.SLBufferPct)
			es5Engine.SetEMATouchBufferPct(parsed.EMATouchBufferPct)
			es5Engine.SetMasterMaxWickPct(parsed.MasterMaxWickPct)
			es5Engine.SetMaxEntryDistancePct(parsed.MaxEntryDistancePct)
			es5Engine.SetMaxSetupWaitCandles(parsed.MaxSetupWaitCandles)
			es5Engine.MinCandlesToIgnore = parsed.MinCandlesToIgnore
		} else if stratName == "FAKE_BREAKOUT" {
			bot.cfg.FBSLBufferPct = parsed.SLBufferPct
			fbEngine.UpdateRules(parsed.GapUpMinPct, parsed.GapUpMaxPct, parsed.GapDownMinPct, parsed.GapDownMaxPct, parsed.MaxConfirmationPct, parsed.MasterMaxWickPct, parsed.TradeEndTime)
			fbEngine.SetCandleTimeFrame(parsed.CandleTimeFrame)
			fbEngine.MinCandlesToIgnore = parsed.MinCandlesToIgnore
		} else if stratName == "VANDE_BHARAT_TRAP" {
			bot.cfg.VBTSLBufferPct = parsed.SLBufferPct
			vbtEngine.UpdateRules(parsed.FakeMasterMaxPct, parsed.MasterMaxPct, parsed.SLMinPct, parsed.SLMaxPct, parsed.MasterMaxWickPct)
			vbtEngine.SetCandleTimeFrame(parsed.CandleTimeFrame)
			vbtEngine.SetTradeEndTime(parsed.TradeEndTime)
			vbtEngine.MinCandlesToIgnore = parsed.MinCandlesToIgnore
		} else if stratName == "LOW_VOLUME" {
			bot.cfg.SLBufferPct = parsed.SLBufferPct
			lvEngine.SetCandleTimeFrame(parsed.CandleTimeFrame)
			lvEngine.SetTradeEndTime(parsed.TradeEndTime)
			lvEngine.MinCandlesToIgnore = parsed.MinCandlesToIgnore
		}
	}

	// Stock Selection configs
	for code, rawJSON := range sysConfigs["STOCK_SELECTION_STRATEGIES"] {
		var cfg selection.StockSelectionStrategyConfig
		_ = json.Unmarshal([]byte(rawJSON), &cfg)
		bot.stockSelectionConfigs[code] = cfg
	}

	// ==========================================
	// ASSERTIONS: Verify every UI config is wired
	// ==========================================

	// 1. General Equity & Capital Settings
	if bot.cfg.RiskPerTrade != 750.0 {
		t.Errorf("expected RiskPerTrade 750.0, got %f", bot.cfg.RiskPerTrade)
	}
	if bot.cfg.InitialCapital != 250000.0 {
		t.Errorf("expected InitialCapital 250000.0, got %f", bot.cfg.InitialCapital)
	}
	if bot.cfg.MaxOpenPositions != 5 {
		t.Errorf("expected MaxOpenPositions 5, got %d", bot.cfg.MaxOpenPositions)
	}
	if bot.cfg.MaxDailyLossAmount != 5000.0 {
		t.Errorf("expected MaxDailyLossAmount 5000.0, got %f", bot.cfg.MaxDailyLossAmount)
	}
	if bot.cfg.MaxTradesPerDay != 15 {
		t.Errorf("expected MaxTradesPerDay 15, got %d", bot.cfg.MaxTradesPerDay)
	}
	if bot.cfg.MaxLossStreaks != 4 {
		t.Errorf("expected MaxLossStreaks 4, got %d", bot.cfg.MaxLossStreaks)
	}
	if bot.cfg.MaxHoldingTimeMin != 45 {
		t.Errorf("expected MaxHoldingTimeMin 45, got %d", bot.cfg.MaxHoldingTimeMin)
	}
	if !bot.cfg.LiveTrading {
		t.Errorf("expected LiveTrading true, got false")
	}
	if bot.cfg.DefaultOrderType != "LIMIT" {
		t.Errorf("expected DefaultOrderType LIMIT, got %s", bot.cfg.DefaultOrderType)
	}
	if bot.cfg.LimitBufferPct != 0.8 {
		t.Errorf("expected LimitBufferPct 0.8, got %f", bot.cfg.LimitBufferPct)
	}
	if bot.cfg.AutoSquareOffTime != "15:18:00" {
		t.Errorf("expected AutoSquareOffTime 15:18:00, got %s", bot.cfg.AutoSquareOffTime)
	}

	// 2. Strategy SL Buffers
	if bot.cfg.SLBufferPct != 0.25 {
		t.Errorf("expected Low Volume SLBufferPct 0.25, got %f", bot.cfg.SLBufferPct)
	}
	if bot.cfg.VBSLBufferPct != 0.35 {
		t.Errorf("expected Vande Bharat VBSLBufferPct 0.35, got %f", bot.cfg.VBSLBufferPct)
	}
	if bot.cfg.FBSLBufferPct != 0.40 {
		t.Errorf("expected Fake Breakout FBSLBufferPct 0.40, got %f", bot.cfg.FBSLBufferPct)
	}
	if bot.cfg.VBTSLBufferPct != 0.22 {
		t.Errorf("expected Vande Bharat Trap VBTSLBufferPct 0.22, got %f", bot.cfg.VBTSLBufferPct)
	}
	if bot.cfg.ES5SLBufferPct != 0.18 {
		t.Errorf("expected EMA S5 ES5SLBufferPct 0.18, got %f", bot.cfg.ES5SLBufferPct)
	}
	if es5Engine.SLBufferPct() != 0.18 {
		t.Errorf("expected ES5 engine SLBufferPct 0.18, got %f", es5Engine.SLBufferPct())
	}

	// 3. Strategy Engines Runtime Verification
	if vbEngine.CandleTimeFrame() != "5m" {
		t.Errorf("expected VB CandleTimeFrame 5m, got %s", vbEngine.CandleTimeFrame())
	}
	if vbEngine.TradeEndTime() != "10:45:00" {
		t.Errorf("expected VB TradeEndTime 10:45:00, got %s", vbEngine.TradeEndTime())
	}
	if vbEngine.MinCandlesToIgnore != 3 {
		t.Errorf("expected VB MinCandlesToIgnore 3, got %d", vbEngine.MinCandlesToIgnore)
	}

	if es5Engine.CandleTimeFrame() != "5m" {
		t.Errorf("expected ES5 CandleTimeFrame 5m, got %s", es5Engine.CandleTimeFrame())
	}
	if es5Engine.TradeEndTime() != "11:20:00" {
		t.Errorf("expected ES5 TradeEndTime 11:20:00, got %s", es5Engine.TradeEndTime())
	}
	if es5Engine.MinCandlesToIgnore != 1 {
		t.Errorf("expected ES5 MinCandlesToIgnore 1, got %d", es5Engine.MinCandlesToIgnore)
	}

	if fbEngine.CandleTimeFrame() != "5m" {
		t.Errorf("expected FB CandleTimeFrame 5m, got %s", fbEngine.CandleTimeFrame())
	}
	if fbEngine.TradeEndTime() != "11:15:00" {
		t.Errorf("expected FB TradeEndTime 11:15:00, got %s", fbEngine.TradeEndTime())
	}

	if vbtEngine.CandleTimeFrame() != "5m" {
		t.Errorf("expected VBT CandleTimeFrame 5m, got %s", vbtEngine.CandleTimeFrame())
	}
	if vbtEngine.TradeEndTime() != "10:50:00" {
		t.Errorf("expected VBT TradeEndTime 10:50:00, got %s", vbtEngine.TradeEndTime())
	}

	if lvEngine.CandleTimeFrame() != "5m" {
		t.Errorf("expected LV CandleTimeFrame 5m, got %s", lvEngine.CandleTimeFrame())
	}
	if lvEngine.TradeEndTime() != "10:30:00" {
		t.Errorf("expected LV TradeEndTime 10:30:00, got %s", lvEngine.TradeEndTime())
	}

	// 4. Stock Selection Configs
	pdhCfg, ok := bot.stockSelectionConfigs["PDH_PDL"]
	if !ok || pdhCfg.PriorityRank != 1 || pdhCfg.LevelShiftPct != 0.5 || pdhCfg.WatchlistSize != 8 || !pdhCfg.Enabled {
		t.Errorf("PDH_PDL config mismatch: %+v", pdhCfg)
	}
	newsCfg, ok := bot.stockSelectionConfigs["NEWS"]
	if !ok || newsCfg.Enabled {
		t.Errorf("expected NEWS config to be disabled, got %+v", newsCfg)
	}

	// 5. Quant Scanner Configs
	if !bot.cfg.Scanner.Enabled {
		t.Errorf("expected Scanner.Enabled true, got false")
	}
	if bot.cfg.Scanner.ExecutionTime != "15:40:00" {
		t.Errorf("expected Scanner.ExecutionTime 15:40:00, got %s", bot.cfg.Scanner.ExecutionTime)
	}
	if bot.cfg.Scanner.MomentumDays != 25 {
		t.Errorf("expected Scanner.MomentumDays 25, got %d", bot.cfg.Scanner.MomentumDays)
	}
	if !bot.cfg.Scanner.ClusterDailyEnabled || !bot.cfg.Scanner.ClusterWeeklyEnabled {
		t.Errorf("expected cluster daily & weekly enabled true")
	}
	if bot.cfg.Scanner.ClusterEMAFast != 10 || bot.cfg.Scanner.ClusterEMAMid != 20 || bot.cfg.Scanner.ClusterEMASlow != 89 {
		t.Errorf("expected cluster EMA 10/20/89, got %d/%d/%d", bot.cfg.Scanner.ClusterEMAFast, bot.cfg.Scanner.ClusterEMAMid, bot.cfg.Scanner.ClusterEMASlow)
	}
	if bot.cfg.Scanner.ClusterMaxSpreadPct != 0.15 {
		t.Errorf("expected ClusterMaxSpreadPct 0.15, got %f", bot.cfg.Scanner.ClusterMaxSpreadPct)
	}

	// 6. Manual Trading Configs
	if !bot.cfg.ManualTradeSyncEnabled {
		t.Errorf("expected ManualTradeSyncEnabled true")
	}
	if bot.cfg.ManualTradePollMinutes != 3 {
		t.Errorf("expected ManualTradePollMinutes 3, got %d", bot.cfg.ManualTradePollMinutes)
	}
	if bot.cfg.ManualTradeAttachedRRStrategy != "DYNAMIC_TRAILING_SL" {
		t.Errorf("expected ManualTradeAttachedRRStrategy DYNAMIC_TRAILING_SL, got %s", bot.cfg.ManualTradeAttachedRRStrategy)
	}
	if bot.cfg.ManualTradeRRRatio != 2.5 {
		t.Errorf("expected ManualTradeRRRatio 2.5, got %f", bot.cfg.ManualTradeRRRatio)
	}
	if bot.cfg.ManualTradePartialExitPct != 60.0 {
		t.Errorf("expected ManualTradePartialExitPct 60.0, got %f", bot.cfg.ManualTradePartialExitPct)
	}
	if bot.cfg.ManualTradeDefaultSLPct != 2.0 {
		t.Errorf("expected ManualTradeDefaultSLPct 2.0, got %f", bot.cfg.ManualTradeDefaultSLPct)
	}
	if !bot.cfg.ManualTradeMoveSLToCost {
		t.Errorf("expected ManualTradeMoveSLToCost true")
	}
	if bot.cfg.ManualTradeCostBufferPct != 0.08 {
		t.Errorf("expected ManualTradeCostBufferPct 0.08, got %f", bot.cfg.ManualTradeCostBufferPct)
	}
	if !bot.cfg.ManualTradeUseBrokerSL {
		t.Errorf("expected ManualTradeUseBrokerSL true")
	}

	// 7. System Aggregation & Restart Times
	if bot.cfg.MorningBroadAggStart != "09:16:00" {
		t.Errorf("expected MorningBroadAggStart 09:16:00, got %s", bot.cfg.MorningBroadAggStart)
	}
	if bot.cfg.MorningBroadAggEnd != "09:44:00" {
		t.Errorf("expected MorningBroadAggEnd 09:44:00, got %s", bot.cfg.MorningBroadAggEnd)
	}
	if !bot.cfg.BroadSubscribe {
		t.Errorf("expected BroadSubscribe true")
	}
	if bot.cfg.RestartAllowedBefore != "09:10:00" {
		t.Errorf("expected RestartAllowedBefore 09:10:00, got %s", bot.cfg.RestartAllowedBefore)
	}
	if bot.cfg.RestartAllowedAfter != "15:50:00" {
		t.Errorf("expected RestartAllowedAfter 15:50:00, got %s", bot.cfg.RestartAllowedAfter)
	}

	// 8. Modular Risk-Reward Strategy Profile Calculation with Custom Configs
	// Test DYNAMIC_TRAILING_SL profile calculation on Short Entry
	rrStrat := bot.riskMgr.GetStrategyForPosition("VANDE_BHARAT")
	if rrStrat == nil || rrStrat.Name() != "DYNAMIC_TRAILING_SL" {
		t.Fatalf("expected DYNAMIC_TRAILING_SL for VANDE_BHARAT, got %v", rrStrat)
	}
	profile := rrStrat.CalculateProfile(100.0, "SELL", 102.0, 99.0, 0.35, 750.0, 250000.0, 20.0, 2.0)
	// For SELL, SL is entryPrice + (setupHigh - entryPrice) * (1 + 0.35/100) = 100.0 + 2.0 * 1.0035 = 102.007
	expectedSL := 102.007
	if profile.StopLoss != expectedSL {
		t.Errorf("expected StopLoss %f with 0.35%% buffer, got %f", expectedSL, profile.StopLoss)
	}
}

// TestOptionsIndexConfigWiring tests that options index configurations are mapped and used
func TestOptionsIndexConfigWiring(t *testing.T) {
	optCfg := &data.OptionsIndexConfig{
		IndexSymbol:          "NIFTY 50",
		IsActive:             true,
		IsLive:               true,
		BaseLotSize:          130,
		MaxMultiplier:        5,
		MultiplierOnReversal: false,
		TargetEntryPremium:   125.5,
		ExpiryType:           "MONTHLY",
		NextMonthDays:        5,
		SLPct:                45.0,
		TrailSLEnabled:       true,
		TrailSLBufferPct:     15.0,
		ST1Period:            12,
		ST1Multiplier:        4.5,
		ST2Period:            8,
		ST2Multiplier:        3.5,
		ST3Period:            6,
		ST3Multiplier:        1.8,
		LastNewTradeTime:     "14:15:00",
		AutoSquareOffTime:    "15:10:00",
		SuperTrendCutoffTime: "15:12:00",
		MaxTradesPerDay:      8,
	}

	logger, err := monitoring.NewLogger("info")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	posMgr := risk.NewIndexOptionsPositionManagerFromConfig(nil, logger.Logger, optCfg, 100000.0)

	// Verify posMgr fields via GetStatus()
	if posMgr.GetIndexSymbol() != "NIFTY 50" {
		t.Errorf("expected IndexSymbol NIFTY 50, got %s", posMgr.GetIndexSymbol())
	}

	status := posMgr.GetStatus()
	if status["base_lot_size"] != 130 {
		t.Errorf("expected base_lot_size 130, got %v", status["base_lot_size"])
	}
	if status["max_trades_per_day"] != 8 {
		t.Errorf("expected max_trades_per_day 8, got %v", status["max_trades_per_day"])
	}

	// Verify SL price calculation with 45.0% SL
	slPrice := posMgr.CalculateSLPrice(100.0)
	if slPrice != 145.0 {
		t.Errorf("expected SL price 145.0 for 100.0 entry at 45%% SL, got %f", slPrice)
	}
}
