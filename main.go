package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"go.uber.org/zap"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/execution"
	"zerodha-trading/monitoring"
	"zerodha-trading/risk"
	"zerodha-trading/scanner"
	"zerodha-trading/selection"
	"zerodha-trading/strategy"
)

//go:embed index.html
var dashboardHTML []byte

// applySystemConfigsToSettings overrides runtime configuration fields from persistent database system configs
func applySystemConfigsToSettings(cfg *config.Settings, sysConfigs map[string]map[string]string, logger *monitoring.Logger) {
	if sysConfigs == nil || cfg == nil {
		return
	}

	// 1. EQUITY_STRATEGY
	if eq, ok := sysConfigs["EQUITY_STRATEGY"]; ok {
		if val, exists := eq["active_strategies"]; exists && val != "" {
			cfg.ActiveStrategies = val
		}
		if val, exists := eq["enable_live_trading"]; exists {
			cfg.LiveTrading = strings.ToLower(val) == "true"
		}
		if val, exists := eq["risk_per_trade_inr"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.RiskPerTrade = v
				cfg.MaxCapitalPerTrade = v
			}
		}
		if val, exists := eq["capital_inr"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.InitialCapital = v
			}
		}
		if val, exists := eq["max_open_positions"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v > 0 {
				cfg.MaxTradesPerDay = v * 5 // Sane limit
			}
		}
		if val, exists := eq["max_daily_loss_amount"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				cfg.MaxDailyLossAmount = v
			}
		}
		if val, exists := eq["max_trades_per_day"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v > 0 {
				cfg.MaxTradesPerDay = v
			}
		}
		if val, exists := eq["max_holding_time_min"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v > 0 {
				cfg.MaxHoldingTimeMin = v
			}
		}
		if val, exists := eq["max_loss_streaks"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v > 0 {
				cfg.MaxLossStreaks = v
			}
		}
		if val, exists := eq["default_order_type"]; exists && val != "" {
			cfg.DefaultOrderType = strings.ToUpper(val)
		}
		if val, exists := eq["risk_reward_type"]; exists && val != "" {
			cfg.RiskRewardType = strings.ToUpper(val)
		}
		if val, exists := eq["risk_reward_ratio"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.RiskRewardRatio = v
			}
		}
		if val, exists := eq["sl_buffer_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				cfg.SLBufferPct = v
				cfg.VBSLBufferPct = v
			}
		}
		if val, exists := eq["lv_sl_buffer_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				cfg.SLBufferPct = v
			}
		}
		if val, exists := eq["lv_trade_end_time"]; exists && val != "" {
			cfg.LVTradeEndTime = data.NormalizeTimeHHMMSS(val)
		}
		if val, exists := eq["lv_min_candles_to_ignore"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v >= 0 {
				cfg.LVMinCandlesToIgnore = v
			}
		}
		if val, exists := eq["lv_use_broker_sl"]; exists {
			cfg.LVUseBrokerSL = strings.ToLower(val) == "true"
		}
		if val, exists := eq["vb_trade_end_time"]; exists && val != "" {
			cfg.VBTradeEndTime = data.NormalizeTimeHHMMSS(val)
		}
		if val, exists := eq["vb_min_candles_to_ignore"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v >= 0 {
				cfg.VBMinCandlesToIgnore = v
			}
		}
		if val, exists := eq["vb_sl_buffer_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				cfg.VBSLBufferPct = v
			}
		}
		if val, exists := eq["vb_use_broker_sl"]; exists {
			cfg.VBUseBrokerSL = strings.ToLower(val) == "true"
		}
		if val, exists := eq["vb_sector_max_buy_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				cfg.SectorMaxBuyPct = v
			}
		}
		if val, exists := eq["vb_sector_max_sell_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				cfg.SectorMaxSellPct = v
			}
		}
		if val, exists := eq["vb_stock_max_buy_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				cfg.StockMaxBuyPct = v
			}
		}
		if val, exists := eq["vb_stock_max_sell_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				cfg.StockMaxSellPct = v
			}
		}
		if val, exists := eq["vb_master_max_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.VBMasterMaxPct = v
			}
		}
		if val, exists := eq["vb_sl_min_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.VBSLMinPct = v
			}
		} else if val, exists := eq["vb_confirm_min_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.VBSLMinPct = v
			}
		}
		if val, exists := eq["vb_sl_max_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.VBSLMaxPct = v
			}
		} else if val, exists := eq["vb_confirm_max_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.VBSLMaxPct = v
			}
		}
		if val, exists := eq["vb_master_max_wick_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.VBMasterMaxWickPct = v
			}
		}
		if val, exists := eq["vb_min_gap_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v >= 0 {
				cfg.VBMinGapPct = v
			}
		}
		if val, exists := eq["vb_stock_max_day_change_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.VBStockMaxDayChangePct = v
			}
		}
		if val, exists := eq["fb_gap_up_min_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v >= 0 {
				cfg.FBGapUpMinPct = v
			}
		}
		if val, exists := eq["fb_gap_up_max_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.FBGapUpMaxPct = v
			}
		}
		if val, exists := eq["fb_gap_down_min_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v >= 0 {
				cfg.FBGapDownMinPct = v
			}
		}
		if val, exists := eq["fb_gap_down_max_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.FBGapDownMaxPct = v
			}
		}
		if val, exists := eq["fb_max_confirmation_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.FBMaxConfirmationPct = v
			}
		}
		if val, exists := eq["fb_master_max_wick_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.FBMasterMaxWickPct = v
			}
		}
		if val, exists := eq["fb_trade_end_time"]; exists && val != "" {
			cfg.FBTradeEndTime = val
		}
		if val, exists := eq["fb_sl_buffer_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v >= 0 {
				cfg.FBSLBufferPct = v
			}
		}
		if val, exists := eq["fb_candle_timeframe"]; exists && val != "" {
			cfg.FBCandleTimeframe = val
		}
		if val, exists := eq["fb_use_broker_sl"]; exists {
			cfg.FBUseBrokerSL = strings.ToLower(val) == "true"
		}
		if val, exists := eq["vbt_fake_master_max_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.VBTFakeMasterMaxPct = v
			}
		}
		if val, exists := eq["vbt_master_max_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.VBTMasterMaxPct = v
			}
		}
		if val, exists := eq["vbt_sl_min_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.VBTSLMinPct = v
			}
		}
		if val, exists := eq["vbt_sl_max_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.VBTSLMaxPct = v
			}
		}
		if val, exists := eq["vbt_master_max_wick_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.VBTMasterMaxWickPct = v
			}
		}
		if val, exists := eq["vbt_trade_end_time"]; exists && val != "" {
			cfg.VBTTradeEndTime = val
		}
		if val, exists := eq["vbt_sl_buffer_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v >= 0 {
				cfg.VBTSLBufferPct = v
			}
		}
		if val, exists := eq["vbt_candle_timeframe"]; exists && val != "" {
			cfg.VBTCandleTimeframe = val
		}
		if val, exists := eq["vbt_use_broker_sl"]; exists {
			cfg.VBTUseBrokerSL = strings.ToLower(val) == "true"
		}
		if val, exists := eq["es5_max_trades_per_stock"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v > 0 {
				cfg.ES5MaxTradesPerStock = v
			}
		}
		if val, exists := eq["es5_rally_candles"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v > 0 {
				cfg.ES5RallyCandles = v
			}
		}
		if val, exists := eq["es5_min_rebound_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v >= 0 {
				cfg.ES5MinReboundPct = v
			}
		}
		if val, exists := eq["es5_master_max_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.ES5MasterMaxPct = v
			}
		}
		if val, exists := eq["es5_max_inside_candles"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v >= 0 {
				cfg.ES5MaxInsideCandles = v
			}
		}
		if val, exists := eq["es5_confirm_max_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.ES5ConfirmMaxPct = v
			}
		}
		if val, exists := eq["es5_trade_end_time"]; exists && val != "" {
			cfg.ES5TradeEndTime = val
		}
		if val, exists := eq["es5_sl_buffer_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v >= 0 {
				cfg.ES5SLBufferPct = v
			}
		}
		if val, exists := eq["es5_ema_touch_buffer_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v >= 0 {
				cfg.ES5EMATouchBufferPct = v
			}
		}
		if val, exists := eq["es5_candle_timeframe"]; exists && val != "" {
			cfg.ES5CandleTimeframe = val
		}
		if val, exists := eq["es5_use_broker_sl"]; exists {
			cfg.ES5UseBrokerSL = strings.ToLower(val) == "true"
		}
		if val, exists := eq["auto_square_off_time"]; exists && val != "" {
			cfg.AutoSquareOffTime = data.NormalizeTimeHHMMSS(val)
		}
	}

	// 2. SELECTION
	if sel, ok := sysConfigs["SELECTION"]; ok {
		if val, exists := sel["pre_selection_strategy"]; exists && val != "" {
			cfg.ActiveSelectors = val
		}
		if val, exists := sel["stock_select_time"]; exists && val != "" {
			cfg.StockSelectTime = data.NormalizeTimeHHMMSS(val)
		}
		if val, exists := sel["evg_stock_select_time"]; exists && val != "" {
			cfg.EVGStockSelectTime = data.NormalizeTimeHHMMSS(val)
		}
		if val, exists := sel["strategy_watchlist_size"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v > 0 {
				cfg.StrategyWatchlistSize = v
			}
		}
		if val, exists := sel["watchlist_max_pct_change"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.WatchlistMaxPctChange = v
			}
		}
		if val, exists := sel["sector_scanner_enabled"]; exists {
			cfg.SectorScannerEnabled = strings.ToLower(val) == "true"
		}
		if val, exists := sel["sector_scanner_top_n"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v > 0 {
				cfg.SectorScannerTopN = v
			}
		}
		if val, exists := sel["sector_scanner_weight"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.SectorScannerWeight = v
			}
		}
	}

	// 3. QUANT_SCANNER
	if sc, ok := sysConfigs["QUANT_SCANNER"]; ok {
		if val, exists := sc["enabled"]; exists {
			cfg.Scanner.Enabled = strings.ToLower(val) == "true"
		}
		if val, exists := sc["execution_time"]; exists && val != "" {
			cfg.Scanner.ExecutionTime = data.NormalizeTimeHHMMSS(val)
		}
		if val, exists := sc["momentum_days"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v > 0 {
				cfg.Scanner.MomentumDays = v
			}
		}
		if val, exists := sc["news_enabled"]; exists {
			cfg.Scanner.NewsEnabled = strings.ToLower(val) == "true"
		}
	}

	// 4. SYSTEM
	if sys, ok := sysConfigs["SYSTEM"]; ok {
		if val, exists := sys["restart_allowed_before"]; exists && val != "" {
			cfg.RestartAllowedBefore = data.NormalizeTimeHHMMSS(val)
		}
		if val, exists := sys["restart_allowed_after"]; exists && val != "" {
			cfg.RestartAllowedAfter = data.NormalizeTimeHHMMSS(val)
		}
	}

	// 5. OPTIONS
	if opt, ok := sysConfigs["OPTIONS"]; ok {
		if val, exists := opt["options_max_trades_per_day"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v > 0 {
				cfg.Options.MaxTradesPerDay = v
			}
		}
	}

	// 6. MANUAL_TRADING
	if man, ok := sysConfigs["MANUAL_TRADING"]; ok {
		if val, exists := man["manual_trade_sync_enabled"]; exists {
			cfg.ManualTradeSyncEnabled = strings.ToLower(val) == "true"
		}
		if val, exists := man["manual_trade_poll_minutes"]; exists {
			if v, err := strconv.Atoi(val); err == nil && v > 0 {
				cfg.ManualTradePollMinutes = v
			}
		}
		if val, exists := man["manual_trade_attached_rr_strategy"]; exists && val != "" {
			cfg.ManualTradeAttachedRRStrategy = strings.ToUpper(val)
		}
		if val, exists := man["manual_trade_rr_ratio"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.ManualTradeRRRatio = v
			}
		}
		if val, exists := man["manual_trade_partial_exit_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.ManualTradePartialExitPct = v
			}
		}
		if val, exists := man["manual_trade_default_sl_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil && v > 0 {
				cfg.ManualTradeDefaultSLPct = v
			}
		}
		if val, exists := man["manual_trade_move_sl_to_cost"]; exists {
			cfg.ManualTradeMoveSLToCost = strings.ToLower(val) == "true"
		}
		if val, exists := man["manual_trade_cost_buffer_pct"]; exists {
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				cfg.ManualTradeCostBufferPct = v
			}
		}
		if val, exists := man["manual_trade_use_broker_sl"]; exists {
			cfg.ManualTradeUseBrokerSL = strings.ToLower(val) == "true"
		}
	}

	logger.Info("Applied persistent database system configs to runtime settings", nil)
}

// TradingBot is the main orchestrator
type TradingBot struct {
	cfg                        *config.Settings
	logger                     *monitoring.Logger
	db                         *data.Database
	ticker                     *data.RobustKiteTicker
	candleAgg                  *data.CandleAggregator
	candleAgg1m                *data.CandleAggregator
	securityMaster             *data.SecurityMaster
	activeStrategies           []strategy.Strategy
	riskMgr                    *risk.RiskManager
	rrCalculator               risk.RiskRewardCalculator
	execMgr                    *execution.ExecutionManager
	statusTracker              *execution.StatusTracker
	resilientExec              *execution.ResilientExecutor
	kiteClient                 data.BrokerClient
	globalBias                 string
	watchlist                  map[string]int64
	watchlistMutex             sync.RWMutex
	broadSubscriptionTokens    map[int64]bool
	broadTokensMutex           sync.RWMutex
	watchlistLeverage          map[string]float64
	leverageMutex              sync.RWMutex
	tickSizes                  map[string]float64
	tickSizesMutex             sync.RWMutex
	activeSelectors            map[string]selection.Selector
	strategySelectorMap        map[string]string           // strategy name -> selector name
	strategyWatchlists         map[string]map[string]int64 // strategy name -> symbol -> token
	watchlistDirections        map[string]string           // symbol -> predicted_direction ("BULLISH BREAKOUT", "BEARISH BREAKDOWN")
	watchlistDirectionsMutex   sync.RWMutex
	stockSelectionConfigs      map[string]selection.StockSelectionStrategyConfig
	stockSelectionConfigsMutex sync.RWMutex
	strategyRRMap              map[string]string // Trading Strategy -> Attached RR Strategy
	strategyRRMapMutex         sync.RWMutex
	strategyMultiSelMap        map[string][]string // Trading Strategy -> Attached Selection Strategies
	strategyMultiSelMapMutex   sync.RWMutex
	watchlistSelectorMap       map[string]string // Symbol -> Assigned Selection Strategy
	watchlistSelectorMapMutex  sync.RWMutex
	symbolProvenance           map[string][]string // Symbol -> list of actual selection strategies that selected it
	symbolProvenanceMutex      sync.RWMutex
	excludedStocks             map[string]bool
	excludedStocksMutex        sync.RWMutex
	running                    bool
	optionsPosMgr              *risk.OptionsPositionManager
	optionsPosMgrs             map[string]*risk.OptionsPositionManager
	optIndexConfigs            map[string]*data.OptionsIndexConfig
	optIndexConfigsMutex       sync.RWMutex
	scanner                    *scanner.QuantScanner
	isScannerRunning           int32
	seeder                     *data.HistoricalSeeder
	autoSelectionDoneToday     bool
	autoSelectionMutex         sync.RWMutex
	lastNiftyHistSync          time.Time
	manualSyncMutex            sync.Mutex
	ctx                        context.Context
	cancel                     context.CancelFunc
	wg                         sync.WaitGroup
}

// NewTradingBot creates a new bot instance
func NewTradingBot(cfg *config.Settings) (*TradingBot, error) {
	logger, db, err := initLoggerAndDatabase(cfg)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	// Load persistent system configs and access token from database
	if db != nil {
		if sysConfigs, err := db.GetAllSystemConfigs(ctx); err == nil && len(sysConfigs) > 0 {
			applySystemConfigsToSettings(cfg, sysConfigs, logger)
		}

		cachedToken, err := db.GetMetadataCache(ctx, "config:kite_access_token", time.Time{})
		if err == nil && cachedToken != "" {
			cfg.AccessToken = cachedToken
			logger.Info("Loaded persistent KITE_ACCESS_TOKEN from database cache", nil)
		} else if cfg.AccessToken != "" {
			_ = db.SaveMetadataCache(ctx, "config:kite_access_token", cfg.AccessToken)
		}
	}

	// Create components
	ticker := data.NewRobustKiteTicker(cfg.APIKey, cfg.AccessToken, logger.Logger)
	candleAgg := data.NewCandleAggregator(db, logger.Logger, cfg.CandleIntervalSec, 100, "candles_5m")
	candleAgg1m := data.NewCandleAggregator(db, logger.Logger, 60, 100, "candles_1m")

	// Initialize Kite Connect API Client
	rawKiteClient := kiteconnect.New(cfg.APIKey)
	rawKiteClient.SetAccessToken(cfg.AccessToken)
	kiteClient := data.NewZerodhaBrokerAdapter(rawKiteClient)

	securityMaster := data.NewSecurityMaster(db, kiteClient, logger.Logger)

	// Modularized strategies, selectors and watchlist initialization
	activeStrategies, activeSelMap, stratSelMap, stratWatchlists := initStrategiesAndSelectors(cfg, db, logger, securityMaster)

	// Modularized risk manager and execution manager initialization
	riskMgr, rrCalculator, execMgr, statusTracker, resilientExec := initRiskAndExecution(cfg, db, logger, kiteClient)

	ctx, cancel := context.WithCancel(context.Background())

	logger.Info("Trading bot initialized successfully", nil)

	// Load per-index configurations from database
	optIndexConfigs := make(map[string]*data.OptionsIndexConfig)
	if db != nil {
		if dbConfigs, err := db.GetAllOptionsIndexConfigs(ctx); err == nil && len(dbConfigs) > 0 {
			for i := range dbConfigs {
				c := dbConfigs[i]
				optIndexConfigs[c.IndexSymbol] = &c
				spec, _ := data.ResolveIndexSpec(c.IndexSymbol)
				optIndexConfigs[spec.CleanPrefix] = &c
			}
			logger.Info("Loaded per-index configurations from database", map[string]interface{}{"count": len(dbConfigs)})
		}
	}

	// Initialize 5m Triple SuperTrend Multi-Index Options Position Managers
	optionsPosMgrs := make(map[string]*risk.OptionsPositionManager)
	allSupportedIndices := []string{"NIFTY 50", "NIFTY BANK", "BSE SENSEX", "FINNIFTY", "MIDCPNIFTY"}
	for _, idxName := range allSupportedIndices {
		spec, _ := data.ResolveIndexSpec(idxName)
		idxCfg := optIndexConfigs[spec.Name]
		var mgr *risk.OptionsPositionManager
		if idxCfg != nil {
			mgr = risk.NewIndexOptionsPositionManagerFromConfig(db, logger.Logger, idxCfg, cfg.Options.PaperBalance)
		} else {
			lotSize := spec.BaseLotSize
			if lotSize <= 0 {
				lotSize = cfg.Options.BaseLotSize
			}
			mgr = risk.NewIndexOptionsPositionManager(
				db, logger.Logger, spec.Name, lotSize, cfg.Options.MaxQuantityMultiplier,
				cfg.Options.OptionsSLPct, cfg.Options.PaperBalance,
			)
		}
		if err := mgr.LoadState(ctx); err != nil {
			logger.Warn("Failed to load options state for index from DB", map[string]interface{}{"index": spec.Name, "error": err.Error()})
		}
		optionsPosMgrs[spec.Name] = mgr
		optionsPosMgrs[spec.CleanPrefix] = mgr
	}

	primarySpec, _ := data.ResolveIndexSpec(cfg.Options.IndexSymbol)
	optionsPosMgr := optionsPosMgrs[primarySpec.Name]
	if optionsPosMgr == nil {
		optionsPosMgr = optionsPosMgrs["NIFTY 50"]
	}

	// Initialize Quant Stock Scanner Engine
	quantScanner := scanner.NewQuantScanner(
		db, securityMaster, kiteClient, logger.Logger,
		cfg.Scanner.MomentumDays, cfg.Scanner.NewsEnabled,
	)

	bot := &TradingBot{
		cfg:                   cfg,
		logger:                logger,
		db:                    db,
		ticker:                ticker,
		candleAgg:             candleAgg,
		candleAgg1m:           candleAgg1m,
		securityMaster:        securityMaster,
		activeStrategies:      activeStrategies,
		riskMgr:               riskMgr,
		rrCalculator:          rrCalculator,
		execMgr:               execMgr,
		statusTracker:         statusTracker,
		resilientExec:         resilientExec,
		kiteClient:            kiteClient,
		activeSelectors:       activeSelMap,
		strategySelectorMap:   stratSelMap,
		strategyWatchlists:    stratWatchlists,
		watchlistLeverage:     make(map[string]float64),
		tickSizes:             make(map[string]float64),
		watchlistDirections:   make(map[string]string),
		stockSelectionConfigs: selection.DefaultStockSelectionConfigs(),
		strategyRRMap: map[string]string{
			"LOW_VOLUME":   "PARTIAL_BOOK_COST_SL",
			"VANDE_BHARAT": "DYNAMIC_TRAILING_SL",
		},
		strategyMultiSelMap: map[string][]string{
			"LOW_VOLUME":   {"PDH_PDL", "FO", "SECTOR"},
			"VANDE_BHARAT": {"FO", "SECTOR", "52WH_52WL"},
		},
		watchlistSelectorMap:    make(map[string]string),
		symbolProvenance:        make(map[string][]string),
		excludedStocks:          make(map[string]bool),
		broadSubscriptionTokens: make(map[int64]bool),
		optionsPosMgr:           optionsPosMgr,
		optionsPosMgrs:          optionsPosMgrs,
		optIndexConfigs:         optIndexConfigs,
		scanner:                 quantScanner,
		seeder:                  data.NewHistoricalSeeder(db, kiteClient, securityMaster, logger.Logger),
		running:                 false,
		ctx:                     ctx,
		cancel:                  cancel,
	}

	bot.loadModularStrategyConfigs()

	// Load tick sizes in the background to avoid blocking the main startup sequence
	go bot.loadTickSizes()

	return bot, nil
}

// resolveSymbolSelectorAndShift returns the active selector name and price level shift % for a symbol
func (tb *TradingBot) resolveSymbolSelectorAndShift(symbol string) (string, float64) {
	tb.watchlistSelectorMapMutex.RLock()
	assignedSel, hasAssigned := tb.watchlistSelectorMap[symbol]
	tb.watchlistSelectorMapMutex.RUnlock()

	tb.stockSelectionConfigsMutex.RLock()
	defer tb.stockSelectionConfigsMutex.RUnlock()

	if hasAssigned && assignedSel != "" {
		normSel := selection.NormalizeSelectorName(assignedSel)
		cfg, exists := tb.stockSelectionConfigs[normSel]
		if exists && cfg.Enabled {
			return normSel, cfg.LevelShiftPct
		}
	}

	return selection.ResolveWinningSelector(symbol, []string{"PDH_PDL", "FO", "SECTOR"}, tb.stockSelectionConfigs)
}

// loadModularStrategyConfigs loads and wires modular trading, risk-reward, and stock selection parameters
func (tb *TradingBot) loadModularStrategyConfigs() {
	if tb.db == nil {
		return
	}
	ctx := context.Background()
	sysConfigs, err := tb.db.GetAllSystemConfigs(ctx)
	if err != nil || len(sysConfigs) == 0 {
		return
	}

	// 1. Load Risk-Reward Configs
	rrCfgMap := sysConfigs["RR_STRATEGY"]
	partialCfg := risk.DefaultPartialBookCostSLConfig()
	dynamicCfg := risk.DefaultDynamicTrailingSLConfig()

	if rrCfgMap != nil {
		// Attempt JSON unmarshaling first
		if jsonStr, ok := rrCfgMap["PARTIAL_BOOK_COST_SL"]; ok && jsonStr != "" && strings.HasPrefix(strings.TrimSpace(jsonStr), "{") {
			_ = json.Unmarshal([]byte(jsonStr), &partialCfg)
		} else {
			if v, err := strconv.ParseFloat(rrCfgMap["partial_book_rr_ratio"], 64); err == nil && v > 0 {
				partialCfg.RiskRewardRatio = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["partial_book_exit_pct"], 64); err == nil && v > 0 {
				partialCfg.PartialExitPct = v
			}
			if v, ok := rrCfgMap["partial_book_move_sl_cost"]; ok {
				partialCfg.MoveSLToCost = strings.ToLower(v) == "true"
			}
			if v, err := strconv.ParseFloat(rrCfgMap["partial_book_cost_buffer_pct"], 64); err == nil {
				partialCfg.CostBufferPct = v
			}
			if v, ok := rrCfgMap["partial_book_initial_sl_mode"]; ok && v != "" {
				partialCfg.InitialSLMode = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["partial_book_initial_sl_pct"], 64); err == nil && v > 0 {
				partialCfg.InitialSLPct = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["partial_book_sl_buffer_pct"], 64); err == nil {
				partialCfg.SLBufferPct = v
			}
		}

		if jsonStr, ok := rrCfgMap["DYNAMIC_TRAILING_SL"]; ok && jsonStr != "" && strings.HasPrefix(strings.TrimSpace(jsonStr), "{") {
			_ = json.Unmarshal([]byte(jsonStr), &dynamicCfg)
		} else {
			if v, err := strconv.ParseFloat(rrCfgMap["trailing_sl_stage1_trigger_pct"], 64); err == nil && v > 0 {
				dynamicCfg.Stage1TriggerPct = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["trailing_sl_stage1_trail_pct"], 64); err == nil {
				dynamicCfg.Stage1TrailPct = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["trailing_sl_stage2_trigger_pct"], 64); err == nil && v > 0 {
				dynamicCfg.Stage2TriggerPct = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["trailing_sl_stage2_trail_pct"], 64); err == nil {
				dynamicCfg.Stage2TrailPct = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["trailing_sl_stage3_trigger_pct"], 64); err == nil && v > 0 {
				dynamicCfg.Stage3TriggerPct = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["trailing_sl_stage3_trail_pct"], 64); err == nil {
				dynamicCfg.Stage3TrailPct = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["trailing_sl_stage4_trigger_pct"], 64); err == nil && v > 0 {
				dynamicCfg.Stage4TriggerPct = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["trailing_sl_stage4_exit_pct"], 64); err == nil && v > 0 {
				dynamicCfg.Stage4ExitPct = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["trailing_sl_stage4_trail_pct"], 64); err == nil {
				dynamicCfg.Stage4TrailPct = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["trailing_sl_stage5_trigger_pct"], 64); err == nil && v > 0 {
				dynamicCfg.Stage5TriggerPct = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["trailing_sl_step_offset_pct"], 64); err == nil {
				dynamicCfg.StepTrailOffsetPct = v
			}
			if v, err := strconv.Atoi(rrCfgMap["trailing_sl_time_decay_min"]); err == nil && v > 0 {
				dynamicCfg.TimeDecayMin = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["trailing_sl_time_decay_trigger_pct"], 64); err == nil {
				dynamicCfg.TimeDecayTriggerPct = v
			}
			if v, err := strconv.ParseFloat(rrCfgMap["trailing_sl_time_decay_trail_pct"], 64); err == nil {
				dynamicCfg.TimeDecayTrailPct = v
			}
		}
	}

	rrStrategies := map[string]risk.RiskRewardStrategy{
		"PARTIAL_BOOK_COST_SL": risk.NewPartialBookCostSLStrategy(partialCfg),
		"DYNAMIC_TRAILING_SL":  risk.NewDynamicTrailingSLStrategy(dynamicCfg),
	}

	// 2. Load Trading Strategy RR Attachments
	stratRRMap := map[string]string{
		"LOW_VOLUME":    "PARTIAL_BOOK_COST_SL",
		"VANDE_BHARAT":  "DYNAMIC_TRAILING_SL",
		"FAKE_BREAKOUT": "DYNAMIC_TRAILING_SL",
		"MANUAL":        "PARTIAL_BOOK_COST_SL",
	}
	stratMultiSel := map[string][]string{
		"LOW_VOLUME":        {"PDH_PDL", "FO", "SECTOR", "QUANT_SCANNER"},
		"VANDE_BHARAT":      {"FO", "SECTOR", "PDH_PDL", "ATH_ATL", "52WH_52WL", "NEWS", "HIGH_IMPACT_NEWS", "RESULT", "QUANT_SCANNER"},
		"FAKE_BREAKOUT":     {"FO", "SECTOR", "PDH_PDL", "52WH_52WL"},
		"VANDE_BHARAT_TRAP": {"FO", "SECTOR", "PDH_PDL", "52WH_52WL"},
		"EMAS5_BREAKOUT":    {"FO", "SECTOR", "PDH_PDL", "52WH_52WL"},
	}

	type TradingStrategyParsedConfig struct {
		Name                    string   `json:"name"`
		Enabled                 bool     `json:"enabled"`
		CandleTimeFrame         string   `json:"candle_time_frame"`
		AttachedRiskReward      string   `json:"attached_risk_reward"`
		AttachedStockSelections []string `json:"attached_stock_selections"`
		TradeEndTime            string   `json:"trade_end_time"`
		MinCandlesToIgnore      int      `json:"min_candles_to_ignore"`
		SLBufferPct             float64  `json:"sl_buffer_pct"`
		UseBrokerSL             bool     `json:"use_broker_sl"`
		SLMinPct                float64  `json:"sl_min_pct"`
		SLMaxPct                float64  `json:"sl_max_pct"`
		MinGapPct               float64  `json:"min_gap_pct"`
		ConfirmMinPct           float64  `json:"confirm_min_pct"`
		ConfirmMaxPct           float64  `json:"confirm_max_pct"`
		MasterMaxPct            float64  `json:"master_max_pct"`
		MasterMaxWickPct        float64  `json:"master_max_wick_pct"`
		StockMaxDayChangePct    float64  `json:"stock_max_day_change_pct"`
		GapUpMinPct             float64  `json:"gap_up_min_pct"`
		GapUpMaxPct             float64  `json:"gap_up_max_pct"`
		GapDownMinPct           float64  `json:"gap_down_min_pct"`
		GapDownMaxPct           float64  `json:"gap_down_max_pct"`
		MaxConfirmationPct      float64  `json:"max_confirmation_pct"`
		FakeMasterMaxPct        float64  `json:"fake_master_max_pct"`
		MaxTradesPerStock       int      `json:"max_trades_per_stock"`
		RallyCandles            int      `json:"rally_candles"`
		MinReboundPct           float64  `json:"min_rebound_pct"`
		MaxInsideCandles        int      `json:"max_inside_candles"`
		EMATouchBufferPct       float64  `json:"ema_touch_buffer_pct"`
	}

	tStratMap := sysConfigs["TRADING_STRATEGY"]
	if tStratMap != nil {
		for stratName, rawVal := range tStratMap {
			if strings.HasPrefix(strings.TrimSpace(rawVal), "{") {
				var parsed TradingStrategyParsedConfig
				if err := json.Unmarshal([]byte(rawVal), &parsed); err == nil {
					if parsed.CandleTimeFrame != "" {
						for _, s := range tb.activeStrategies {
							if s.Name() == stratName {
								s.SetCandleTimeFrame(parsed.CandleTimeFrame)
							}
						}
						if stratName == "LOW_VOLUME" {
							tb.cfg.LVCandleTimeframe = parsed.CandleTimeFrame
						} else if stratName == "VANDE_BHARAT" {
							tb.cfg.VBCandleTimeframe = parsed.CandleTimeFrame
						} else if stratName == "FAKE_BREAKOUT" {
							tb.cfg.FBCandleTimeframe = parsed.CandleTimeFrame
						} else if stratName == "VANDE_BHARAT_TRAP" {
							tb.cfg.VBTCandleTimeframe = parsed.CandleTimeFrame
						} else if stratName == "EMAS5_BREAKOUT" {
							tb.cfg.ES5CandleTimeframe = parsed.CandleTimeFrame
						}
					}
					if parsed.AttachedRiskReward != "" {
						stratRRMap[stratName] = parsed.AttachedRiskReward
					}
					if len(parsed.AttachedStockSelections) > 0 {
						var normSels []string
						for _, s := range parsed.AttachedStockSelections {
							if norm := selection.NormalizeSelectorName(s); norm != "" {
								normSels = append(normSels, norm)
							}
						}
						stratMultiSel[stratName] = normSels
					}
					if stratName == "VANDE_BHARAT" {
						if parsed.TradeEndTime != "" {
							tb.cfg.VBTradeEndTime = data.NormalizeTimeHHMMSS(parsed.TradeEndTime)
						}
						for _, s := range tb.activeStrategies {
							if vb, ok := s.(*strategy.VandeBharatEngine); ok {
								slMin := parsed.SLMinPct
								if slMin <= 0 {
									slMin = parsed.ConfirmMinPct
								}
								slMax := parsed.SLMaxPct
								if slMax <= 0 {
									slMax = parsed.ConfirmMaxPct
								}
								vb.UpdateRules(
									parsed.MasterMaxPct,
									slMin,
									slMax,
									parsed.MasterMaxWickPct,
									parsed.MinGapPct,
								)
								if parsed.MinCandlesToIgnore >= 0 {
									vb.MinCandlesToIgnore = parsed.MinCandlesToIgnore
								}
							}
						}
					} else if stratName == "LOW_VOLUME" {
						if parsed.TradeEndTime != "" {
							tb.cfg.LVTradeEndTime = data.NormalizeTimeHHMMSS(parsed.TradeEndTime)
						}
						for _, s := range tb.activeStrategies {
							if lv, ok := s.(*strategy.LowVolumeEngine); ok {
								if parsed.MinCandlesToIgnore >= 0 {
									lv.MinCandlesToIgnore = parsed.MinCandlesToIgnore
								}
							}
						}
					} else if stratName == "FAKE_BREAKOUT" {
						if parsed.TradeEndTime != "" {
							tb.cfg.FBTradeEndTime = data.NormalizeTimeHHMMSS(parsed.TradeEndTime)
						}
						for _, s := range tb.activeStrategies {
							if fb, ok := s.(*strategy.FakeBreakoutEngine); ok {
								gapUpMin := parsed.GapUpMinPct
								if gapUpMin < 0 {
									gapUpMin = 4.0
								}
								gapUpMax := parsed.GapUpMaxPct
								if gapUpMax <= 0 {
									gapUpMax = 8.0
								}
								gapDownMin := parsed.GapDownMinPct
								if gapDownMin < 0 {
									gapDownMin = 4.0
								}
								gapDownMax := parsed.GapDownMaxPct
								if gapDownMax <= 0 {
									gapDownMax = 8.0
								}
								maxConfirm := parsed.MaxConfirmationPct
								if maxConfirm <= 0 {
									maxConfirm = 1.0
								}
								masterWick := parsed.MasterMaxWickPct
								if masterWick <= 0 {
									masterWick = 40.0
								}
								fb.UpdateRules(
									gapUpMin,
									gapUpMax,
									gapDownMin,
									gapDownMax,
									maxConfirm,
									masterWick,
									parsed.TradeEndTime,
								)
								if parsed.MinCandlesToIgnore >= 0 {
									fb.MinCandlesToIgnore = parsed.MinCandlesToIgnore
								}
							}
						}
					} else if stratName == "VANDE_BHARAT_TRAP" {
						if parsed.TradeEndTime != "" {
							tb.cfg.VBTTradeEndTime = data.NormalizeTimeHHMMSS(parsed.TradeEndTime)
						}
						for _, s := range tb.activeStrategies {
							if vbt, ok := s.(*strategy.VandeBharatTrapEngine); ok {
								fakeMasterMax := parsed.FakeMasterMaxPct
								if fakeMasterMax <= 0 {
									fakeMasterMax = 3.0
								}
								masterMax := parsed.MasterMaxPct
								if masterMax <= 0 {
									masterMax = 1.8
								}
								slMin := parsed.SLMinPct
								if slMin <= 0 {
									slMin = 0.5
								}
								slMax := parsed.SLMaxPct
								if slMax <= 0 {
									slMax = 1.0
								}
								masterWick := parsed.MasterMaxWickPct
								if masterWick <= 0 {
									masterWick = 40.0
								}
								vbt.UpdateRules(
									fakeMasterMax,
									masterMax,
									slMin,
									slMax,
									masterWick,
								)
								if parsed.MinCandlesToIgnore >= 0 {
									vbt.MinCandlesToIgnore = parsed.MinCandlesToIgnore
								}
							}
						}
					} else if stratName == "EMAS5_BREAKOUT" {
						if parsed.TradeEndTime != "" {
							tb.cfg.ES5TradeEndTime = data.NormalizeTimeHHMMSS(parsed.TradeEndTime)
						}
						for _, s := range tb.activeStrategies {
							if es5, ok := s.(*strategy.EMAS5BreakoutEngine); ok {
								maxTrades := parsed.MaxTradesPerStock
								if maxTrades <= 0 {
									maxTrades = 2
								}
								rallyCandles := parsed.RallyCandles
								if rallyCandles <= 0 {
									rallyCandles = 5
								}
								minRebound := parsed.MinReboundPct
								if minRebound < 0 {
									minRebound = 0.5
								}
								masterMax := parsed.MasterMaxPct
								if masterMax <= 0 {
									masterMax = 2.0
								}
								maxInside := parsed.MaxInsideCandles
								if maxInside < 0 {
									maxInside = 1
								}
								confirmMax := parsed.ConfirmMaxPct
								if confirmMax <= 0 {
									confirmMax = 1.0
								}
								es5.UpdateRules(
									maxTrades,
									rallyCandles,
									minRebound,
									masterMax,
									maxInside,
									confirmMax,
									parsed.TradeEndTime,
								)
								if parsed.EMATouchBufferPct >= 0 {
									es5.SetEMATouchBufferPct(parsed.EMATouchBufferPct)
								}
								if parsed.MasterMaxWickPct > 0 {
									es5.SetMasterMaxWickPct(parsed.MasterMaxWickPct)
								}
								if parsed.MinCandlesToIgnore >= 0 {
									es5.MinCandlesToIgnore = parsed.MinCandlesToIgnore
								}
							}
						}
					}
				}
			}
		}

		if v := tStratMap["lv_attached_rr_strategy"]; v != "" {
			stratRRMap["LOW_VOLUME"] = v
		}
		if v := tStratMap["vb_attached_rr_strategy"]; v != "" {
			stratRRMap["VANDE_BHARAT"] = v
		}
		if v := tStratMap["fb_attached_rr_strategy"]; v != "" {
			stratRRMap["FAKE_BREAKOUT"] = v
		}
		if v := tStratMap["vbt_attached_rr_strategy"]; v != "" {
			stratRRMap["VANDE_BHARAT_TRAP"] = v
		}
		if v := tStratMap["es5_attached_rr_strategy"]; v != "" {
			stratRRMap["EMAS5_BREAKOUT"] = v
		}
		if v := tStratMap["es5_attached_selection_strategies"]; v != "" {
			var sels []string
			for _, s := range strings.Split(v, ",") {
				if norm := selection.NormalizeSelectorName(s); norm != "" {
					sels = append(sels, norm)
				}
			}
			if len(sels) > 0 {
				stratMultiSel["EMAS5_BREAKOUT"] = sels
			}
		}
		if v := tStratMap["vbt_attached_selection_strategies"]; v != "" {
			var sels []string
			for _, s := range strings.Split(v, ",") {
				if norm := selection.NormalizeSelectorName(s); norm != "" {
					sels = append(sels, norm)
				}
			}
			if len(sels) > 0 {
				stratMultiSel["VANDE_BHARAT_TRAP"] = sels
			}
		}
		if v := tStratMap["lv_attached_selection_strategies"]; v != "" {
			var sels []string
			for _, s := range strings.Split(v, ",") {
				if norm := selection.NormalizeSelectorName(s); norm != "" {
					sels = append(sels, norm)
				}
			}
			if len(sels) > 0 {
				stratMultiSel["LOW_VOLUME"] = sels
			}
		}
		if v := tStratMap["vb_attached_selection_strategies"]; v != "" {
			var sels []string
			for _, s := range strings.Split(v, ",") {
				if norm := selection.NormalizeSelectorName(s); norm != "" {
					sels = append(sels, norm)
				}
			}
			if len(sels) > 0 {
				stratMultiSel["VANDE_BHARAT"] = sels
			}
		}
		if v := tStratMap["fb_attached_selection_strategies"]; v != "" {
			var sels []string
			for _, s := range strings.Split(v, ",") {
				if norm := selection.NormalizeSelectorName(s); norm != "" {
					sels = append(sels, norm)
				}
			}
			if len(sels) > 0 {
				stratMultiSel["FAKE_BREAKOUT"] = sels
			}
		}
	}

	eqCfgMap := sysConfigs["EQUITY_STRATEGY"]
	if eqCfgMap != nil {
		var mMax, slMin, slMax, mWick, minGap float64
		if v, err := strconv.ParseFloat(eqCfgMap["vb_master_max_pct"], 64); err == nil && v > 0 {
			mMax = v
			tb.cfg.VBMasterMaxPct = v
		}
		if v, err := strconv.ParseFloat(eqCfgMap["vb_sl_min_pct"], 64); err == nil && v > 0 {
			slMin = v
			tb.cfg.VBSLMinPct = v
		} else if v, err := strconv.ParseFloat(eqCfgMap["vb_confirm_min_pct"], 64); err == nil && v > 0 {
			slMin = v
			tb.cfg.VBSLMinPct = v
		}
		if v, err := strconv.ParseFloat(eqCfgMap["vb_sl_max_pct"], 64); err == nil && v > 0 {
			slMax = v
			tb.cfg.VBSLMaxPct = v
		} else if v, err := strconv.ParseFloat(eqCfgMap["vb_confirm_max_pct"], 64); err == nil && v > 0 {
			slMax = v
			tb.cfg.VBSLMaxPct = v
		}
		if v, err := strconv.ParseFloat(eqCfgMap["vb_master_max_wick_pct"], 64); err == nil && v > 0 {
			mWick = v
			tb.cfg.VBMasterMaxWickPct = v
		}
		hasMinGap := false
		if v, err := strconv.ParseFloat(eqCfgMap["vb_min_gap_pct"], 64); err == nil && v >= 0 {
			minGap = v
			hasMinGap = true
			tb.cfg.VBMinGapPct = v
		}
		if mMax > 0 || slMin > 0 || slMax > 0 || mWick > 0 || hasMinGap {
			for _, s := range tb.activeStrategies {
				if vb, ok := s.(*strategy.VandeBharatEngine); ok {
					vb.UpdateRules(mMax, slMin, slMax, mWick, minGap)
				}
			}
		}

		var fbGapUpMin, fbGapUpMax, fbGapDownMin, fbGapDownMax, fbMaxConfirm, fbMasterWick float64
		if v, err := strconv.ParseFloat(eqCfgMap["fb_gap_up_min_pct"], 64); err == nil && v > 0 {
			fbGapUpMin = v
			tb.cfg.FBGapUpMinPct = v
		}
		if v, err := strconv.ParseFloat(eqCfgMap["fb_gap_up_max_pct"], 64); err == nil && v > 0 {
			fbGapUpMax = v
			tb.cfg.FBGapUpMaxPct = v
		}
		if v, err := strconv.ParseFloat(eqCfgMap["fb_gap_down_min_pct"], 64); err == nil && v > 0 {
			fbGapDownMin = v
			tb.cfg.FBGapDownMinPct = v
		}
		if v, err := strconv.ParseFloat(eqCfgMap["fb_gap_down_max_pct"], 64); err == nil && v > 0 {
			fbGapDownMax = v
			tb.cfg.FBGapDownMaxPct = v
		}
		if v, err := strconv.ParseFloat(eqCfgMap["fb_max_confirmation_pct"], 64); err == nil && v > 0 {
			fbMaxConfirm = v
			tb.cfg.FBMaxConfirmationPct = v
		}
		if v, err := strconv.ParseFloat(eqCfgMap["fb_master_max_wick_pct"], 64); err == nil && v > 0 {
			fbMasterWick = v
			tb.cfg.FBMasterMaxWickPct = v
		}
		if v := eqCfgMap["fb_trade_end_time"]; v != "" {
			tb.cfg.FBTradeEndTime = v
		}
		if fbGapUpMin > 0 || fbGapUpMax > 0 || fbGapDownMin > 0 || fbGapDownMax > 0 || fbMaxConfirm > 0 || fbMasterWick > 0 || tb.cfg.FBTradeEndTime != "" {
			for _, s := range tb.activeStrategies {
				if fb, ok := s.(*strategy.FakeBreakoutEngine); ok {
					fb.UpdateRules(fbGapUpMin, fbGapUpMax, fbGapDownMin, fbGapDownMax, fbMaxConfirm, fbMasterWick, tb.cfg.FBTradeEndTime)
				}
			}
		}

		if v := eqCfgMap["lv_candle_timeframe"]; v != "" {
			tb.cfg.LVCandleTimeframe = v
			for _, s := range tb.activeStrategies {
				if s.Name() == "LOW_VOLUME" {
					s.SetCandleTimeFrame(v)
				}
			}
		}
		if v := eqCfgMap["vb_candle_timeframe"]; v != "" {
			tb.cfg.VBCandleTimeframe = v
			for _, s := range tb.activeStrategies {
				if s.Name() == "VANDE_BHARAT" {
					s.SetCandleTimeFrame(v)
				}
			}
		}
		if v := eqCfgMap["fb_candle_timeframe"]; v != "" {
			tb.cfg.FBCandleTimeframe = v
			for _, s := range tb.activeStrategies {
				if s.Name() == "FAKE_BREAKOUT" {
					s.SetCandleTimeFrame(v)
				}
			}
		}
		if v := eqCfgMap["vbt_candle_timeframe"]; v != "" {
			tb.cfg.VBTCandleTimeframe = v
			for _, s := range tb.activeStrategies {
				if s.Name() == "VANDE_BHARAT_TRAP" {
					s.SetCandleTimeFrame(v)
				}
			}
		}
		if v := eqCfgMap["es5_candle_timeframe"]; v != "" {
			tb.cfg.ES5CandleTimeframe = v
			for _, s := range tb.activeStrategies {
				if s.Name() == "EMAS5_BREAKOUT" {
					s.SetCandleTimeFrame(v)
				}
			}
		}
		if v := eqCfgMap["lv_trade_end_time"]; v != "" {
			tb.cfg.LVTradeEndTime = data.NormalizeTimeHHMMSS(v)
		}
		if v := eqCfgMap["vb_trade_end_time"]; v != "" {
			tb.cfg.VBTradeEndTime = data.NormalizeTimeHHMMSS(v)
		}
		if v := eqCfgMap["vbt_trade_end_time"]; v != "" {
			tb.cfg.VBTTradeEndTime = data.NormalizeTimeHHMMSS(v)
		}
		if v := eqCfgMap["es5_trade_end_time"]; v != "" {
			tb.cfg.ES5TradeEndTime = data.NormalizeTimeHHMMSS(v)
		}
		if v, err := strconv.ParseFloat(eqCfgMap["risk_per_trade_inr"], 64); err == nil && v > 0 {
			tb.cfg.RiskPerTrade = v
			tb.cfg.MaxCapitalPerTrade = v
		}
		if v, err := strconv.ParseFloat(eqCfgMap["capital_inr"], 64); err == nil && v > 0 {
			tb.cfg.InitialCapital = v
		}
		if v, err := strconv.Atoi(eqCfgMap["max_trades_per_day"]); err == nil && v > 0 {
			tb.cfg.MaxTradesPerDay = v
			if tb.riskMgr != nil {
				tb.riskMgr.SetMaxTradesPerDay(v)
			}
		}
	}

	// 2b. Load Manual Trading Strategy Configs
	manualRRStrategy := "PARTIAL_BOOK_COST_SL"
	if tb.cfg.ManualTradeAttachedRRStrategy != "" {
		manualRRStrategy = tb.cfg.ManualTradeAttachedRRStrategy
	}
	if manCfgMap := sysConfigs["MANUAL_TRADING"]; manCfgMap != nil {
		if v, ok := manCfgMap["manual_trade_attached_rr_strategy"]; ok && v != "" {
			manualRRStrategy = strings.ToUpper(v)
			tb.cfg.ManualTradeAttachedRRStrategy = manualRRStrategy
		}
		if v, ok := manCfgMap["manual_trade_sync_enabled"]; ok {
			tb.cfg.ManualTradeSyncEnabled = strings.ToLower(v) == "true"
		}
		if v, err := strconv.Atoi(manCfgMap["manual_trade_poll_minutes"]); err == nil && v > 0 {
			tb.cfg.ManualTradePollMinutes = v
		}
		if v, err := strconv.ParseFloat(manCfgMap["manual_trade_rr_ratio"], 64); err == nil && v > 0 {
			tb.cfg.ManualTradeRRRatio = v
		}
		if v, err := strconv.ParseFloat(manCfgMap["manual_trade_partial_exit_pct"], 64); err == nil && v > 0 {
			tb.cfg.ManualTradePartialExitPct = v
		}
		if v, err := strconv.ParseFloat(manCfgMap["manual_trade_default_sl_pct"], 64); err == nil && v > 0 {
			tb.cfg.ManualTradeDefaultSLPct = v
		}
		if v, ok := manCfgMap["manual_trade_move_sl_to_cost"]; ok {
			tb.cfg.ManualTradeMoveSLToCost = strings.ToLower(v) == "true"
		}
		if v, err := strconv.ParseFloat(manCfgMap["manual_trade_cost_buffer_pct"], 64); err == nil {
			tb.cfg.ManualTradeCostBufferPct = v
		}
		if v, ok := manCfgMap["manual_trade_use_broker_sl"]; ok {
			tb.cfg.ManualTradeUseBrokerSL = strings.ToLower(v) == "true"
		}
	}
	stratRRMap["MANUAL"] = manualRRStrategy

	tb.strategyRRMapMutex.Lock()
	tb.strategyRRMap = stratRRMap
	tb.strategyRRMapMutex.Unlock()

	tb.strategyMultiSelMapMutex.Lock()
	tb.strategyMultiSelMap = stratMultiSel
	tb.strategyMultiSelMapMutex.Unlock()

	if tb.riskMgr != nil {
		tb.riskMgr.SetRiskRewardStrategies(rrStrategies, stratRRMap)
	}

	// 3. Load Stock Selection Strategy Configs (Ranks & Level Shifts)
	selCfgMap := sysConfigs["STOCK_SELECTION_STRATEGIES"]
	if selCfgMap != nil {
		tb.stockSelectionConfigsMutex.Lock()
		for code, defCfg := range selection.DefaultStockSelectionConfigs() {
			cfg := defCfg

			// Check JSON unmarshaling by exact key code first
			if rawJSON, ok := selCfgMap[code]; ok && rawJSON != "" && strings.HasPrefix(strings.TrimSpace(rawJSON), "{") {
				_ = json.Unmarshal([]byte(rawJSON), &cfg)
			} else {
				prefix := strings.ToLower(code)
				if v, ok := selCfgMap[prefix+"_enabled"]; ok {
					cfg.Enabled = strings.ToLower(v) == "true"
				}
				if v, err := strconv.Atoi(selCfgMap[prefix+"_rank"]); err == nil && v > 0 {
					cfg.PriorityRank = v
				}
				if v, err := strconv.ParseFloat(selCfgMap[prefix+"_shift_pct"], 64); err == nil {
					cfg.LevelShiftPct = v
				}
				if v, err := strconv.Atoi(selCfgMap[prefix+"_size"]); err == nil && v > 0 {
					cfg.WatchlistSize = v
				}
			}
			tb.stockSelectionConfigs[code] = cfg
		}
		tb.stockSelectionConfigsMutex.Unlock()
	}

	// 4. Load SYSTEM Configs (Broad aggregation times & restart gates)
	sysMap := sysConfigs["SYSTEM"]
	if sysMap != nil {
		if v, ok := sysMap["morning_broad_agg_start"]; ok && v != "" {
			tb.cfg.MorningBroadAggStart = data.NormalizeTimeHHMMSS(v)
		}
		if v, ok := sysMap["morning_broad_agg_end"]; ok && v != "" {
			tb.cfg.MorningBroadAggEnd = data.NormalizeTimeHHMMSS(v)
		}
		if v, ok := sysMap["broad_subscribe"]; ok {
			tb.cfg.BroadSubscribe = strings.ToLower(v) == "true"
		}
		if v, ok := sysMap["restart_allowed_before"]; ok && v != "" {
			tb.cfg.RestartAllowedBefore = data.NormalizeTimeHHMMSS(v)
		}
		if v, ok := sysMap["restart_allowed_after"]; ok && v != "" {
			tb.cfg.RestartAllowedAfter = data.NormalizeTimeHHMMSS(v)
		}
	}
}

// initLoggerAndDatabase initializes the logger, DB connection and schema migrations
func initLoggerAndDatabase(cfg *config.Settings) (*monitoring.Logger, *data.Database, error) {
	logger, err := monitoring.NewLogger(cfg.LogLevel)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create logger: %w", err)
	}

	db, err := data.NewDatabase(
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		logger.Logger,
	)
	if err != nil {
		logger.Error("Database connection failed", map[string]interface{}{"error": err.Error()})
		return nil, nil, err
	}

	if err := db.InitSchema(); err != nil {
		logger.Error("Schema initialization failed", map[string]interface{}{"error": err.Error()})
		return nil, nil, err
	}

	return logger, db, nil
}

// initRiskAndExecution initializes risk limits, risk managers and orders executor layers
func initRiskAndExecution(cfg *config.Settings, db *data.Database, logger *monitoring.Logger, kiteClient data.BrokerClient) (*risk.RiskManager, risk.RiskRewardCalculator, *execution.ExecutionManager, *execution.StatusTracker, *execution.ResilientExecutor) {
	ctx := context.Background()

	riskLimits := risk.RiskLimits{
		MaxTradesPerDay:    cfg.MaxTradesPerDay,
		MaxLossStreaks:     cfg.MaxLossStreaks,
		MaxHoldingTimeMin:  cfg.MaxHoldingTimeMin,
		MaxDailyLossAmount: cfg.MaxDailyLossAmount,
	}

	riskMgr := risk.NewRiskManager(db.WithContext(ctx), logger.Logger, cfg.InitialCapital, riskLimits)

	// Restore today's trade count and daily P&L from the database to prevent exceeding limits after restarts
	totalTrades, totalPnL, _, err := db.GetTradingMetrics(ctx)
	if err == nil {
		riskMgr.RestoreTradesToday(totalTrades, totalPnL)
		logger.Logger.Info("Restored RiskManager trades count and PnL on startup",
			zap.Int("trades_today", totalTrades),
			zap.Float64("daily_pnl", totalPnL),
		)
	} else {
		logger.Logger.Error("Failed to restore RiskManager trades count on startup", zap.Error(err))
	}

	resilientExec := execution.NewResilientExecutor(logger.Logger)
	execMgr := execution.NewExecutionManager(db, logger.Logger, kiteClient, resilientExec, cfg.LiveTrading)
	statusTracker := execution.NewStatusTracker(execMgr, riskMgr, logger.Logger)
	rrCalculator := risk.InitializeRiskRewardCalculator(cfg.RiskRewardType)

	return riskMgr, rrCalculator, execMgr, statusTracker, resilientExec
}

// initStrategiesAndSelectors initializes active trading strategies and active selectors
func initStrategiesAndSelectors(cfg *config.Settings, db *data.Database, logger *monitoring.Logger, securityMaster *data.SecurityMaster) ([]strategy.Strategy, map[string]selection.Selector, map[string]string, map[string]map[string]int64) {
	var activeNames []string
	if cfg.ActiveStrategies != "" {
		activeNames = strings.Split(cfg.ActiveStrategies, ",")
		for i := range activeNames {
			activeNames[i] = strings.TrimSpace(activeNames[i])
		}
	}
	activeStrategies := strategy.InitializeActiveStrategies(activeNames, logger.Logger, cfg)

	var selectorNames []string
	if cfg.ActiveSelectors != "" {
		selectorNames = strings.Split(cfg.ActiveSelectors, ",")
		for i := range selectorNames {
			selectorNames[i] = strings.TrimSpace(selectorNames[i])
		}
	}
	activeSelMap := selection.InitializeSelectors(selectorNames, cfg, db)

	stratSelMap := make(map[string]string)
	if cfg.StrategySelectorMap != "" {
		pairs := strings.Split(cfg.StrategySelectorMap, ",")
		for _, pair := range pairs {
			kv := strings.Split(strings.TrimSpace(pair), ":")
			if len(kv) == 2 {
				stratName := strings.TrimSpace(kv[0])
				selName := strings.TrimSpace(kv[1])
				stratSelMap[stratName] = selName

				// If it's a composite selector, build and register it dynamically
				if strings.Contains(selName, "+") {
					parts := strings.Split(selName, "+")
					var subSelectors []selection.Selector
					for _, part := range parts {
						part = strings.TrimSpace(part)
						subSel, exists := activeSelMap[part]
						if !exists {
							switch part {
							case "SECURITIES_FO":
								subSel = selection.NewSecuritiesFOSelector()
							case "SECTORAL":
								subSel = selection.NewSectoralSelector(cfg, db)
							case "EQUITY_VOLUME_GAINERS":
								subSel = selection.NewEquityVolumeGainersSelector()
							}
						}
						if subSel != nil {
							subSelectors = append(subSelectors, subSel)
						}
					}
					if len(subSelectors) > 0 {
						activeSelMap[selName] = selection.NewCompositeSelector(subSelectors)
					}
				}
			}
		}
	}

	stratWatchlists := make(map[string]map[string]int64)
	for _, strat := range activeStrategies {
		stratWatchlists[strat.Name()] = make(map[string]int64)
	}

	return activeStrategies, activeSelMap, stratSelMap, stratWatchlists
}

// Run starts the main trading loop
func (tb *TradingBot) Run() error {
	tb.running = true
	tb.logger.InfoMarket("=== Automated Trading Bot Started ===", nil)

	// Startup checks
	if err := tb.startupChecks(); err != nil {
		return err
	}

	nowIST := time.Now().In(data.ISTLocation)

	tb.watchlistMutex.Lock()
	tb.watchlist = make(map[string]int64)
	tb.watchlistMutex.Unlock()

	// Connect to ticker with broad subscription if enabled
	instrumentTokens := make([]int64, 0)
	if tb.cfg.BroadSubscribe {
		tokens, err := tb.getBroadSubscriptionTokens()
		if err == nil && len(tokens) > 0 {
			instrumentTokens = tokens
			tb.logger.Info("Broad subscription enabled. Subscribing to all F&O and Nifty50 constituents.", map[string]interface{}{"count": len(tokens)})
		} else {
			tb.logger.Error("Failed to query broad subscription tokens", map[string]interface{}{"error": err})
		}
	}
	// Always include NIFTY 50 Index Token (256265) for options bot live 5m candles
	instrumentTokens = append(instrumentTokens, 256265)

	// Reconcile and recover any active MIS positions and stop-loss orders on startup
	tb.reconcilePositions()

	// Populate triggered trades from database to prevent duplicate trades after restart
	tb.restoreTriggeredTrades()

	// Connect to ticker
	if err := tb.ticker.Connect(tb.ctx, instrumentTokens); err != nil {
		return fmt.Errorf("failed to connect ticker: %w", err)
	}

	time.Sleep(2 * time.Second) // Wait for connection

	// Start interactive web dashboard server on port 8080 (makes it responsive immediately on boot)
	go tb.startWebDashboard()

	// Store PDH/PDL for Nifty 50 stocks if not present
	tb.initializeNifty50PDH_PDL(data.ISTLocation)

	// Handle Catch-Up logic if bot started after GlobalTradeStartTime in background (prevents blocking main loops)
	go tb.handleCatchUpSequence(data.ISTLocation, nowIST)

	// Start main loops
	tb.wg.Add(5)
	go tb.tickProcessingLoop()
	go tb.strategyLoop()
	go tb.orderManagementLoop()
	go tb.monitoringLoop()
	go tb.runOptionsBotLoop(data.ISTLocation)

	tb.wg.Add(1)
	go tb.runDailyStrategyScheduler(data.ISTLocation)

	// Wait for shutdown
	tb.waitForShutdown()

	return nil
}

// handleCatchUpSequence runs the catch-up sequence if the bot started late
func (tb *TradingBot) handleCatchUpSequence(loc *time.Location, nowIST time.Time) {
	// Skip catch-up if today is weekend (Saturday/Sunday)
	if nowIST.Weekday() == time.Saturday || nowIST.Weekday() == time.Sunday {
		return
	}

	calendarTodayStr := nowIST.Format("2006-01-02")
	dbItems, errDb := tb.db.GetDailyWatchlist(tb.ctx, calendarTodayStr)
	if errDb == nil && len(dbItems) > 0 {
		hasAutoSelected := false
		for _, item := range dbItems {
			if !strings.HasPrefix(item.Selectors, "MANUAL") {
				hasAutoSelected = true
				break
			}
		}
		if hasAutoSelected {
			tb.setAutoSelectionDone(true)
			_ = tb.selectWatchlist(loc, false)
		}
	}

	// If started at or after 09:15 AM on a trading day, trigger catch-up sequence
	startBoundary := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 9, 15, 0, 0, loc)
	if !nowIST.Before(startBoundary) {
		tb.logger.Info("Bot started during/after market hours. Initiating catch-up sequence...", nil)
		if err := tb.logMarketBreadth(loc); err != nil {
			tb.logger.Error("Failed to calculate catch-up market breadth", map[string]interface{}{"error": err.Error()})
		}
		if err := tb.selectWatchlist(loc, false); err != nil {
			tb.logger.Error("Failed to resolve catch-up dynamic watchlist", map[string]interface{}{"error": err.Error()})
		} else {
			// Catch up on historical 5-minute candles since 09:15 AM
			tb.watchlistMutex.RLock()
			watchlistCopy := make(map[string]int64)
			for sym, tok := range tb.watchlist {
				watchlistCopy[sym] = tok
			}
			tb.watchlistMutex.RUnlock()

			for sym, tok := range watchlistCopy {
				go tb.catchUpHistoricalCandles(sym, tok)
			}
		}
	}
	// Always ensure NIFTY 50 (Token 256265) 5m historical candles exist in DB for options bot UI & strategy
	go tb.ensureNifty50OptionsHistoricalData()
}

// strategyLoop processes completed candles and forwards them to strategy engines based on their configured timeframe
func (tb *TradingBot) strategyLoop() {
	defer tb.wg.Done()

	tb.logger.Info("Strategy loop started", nil)

	candles5mChan := tb.candleAgg.GetCompletedCandles()
	candles1mChan := tb.candleAgg1m.GetCompletedCandles()

	for {
		select {
		case <-tb.ctx.Done():
			return

		case candle := <-candles5mChan:
			if candle == nil {
				continue
			}

			// Map token to symbol
			var symbol string
			tb.watchlistMutex.RLock()
			for sym, tok := range tb.watchlist {
				if tok == candle.Token {
					symbol = sym
					break
				}
			}
			tb.watchlistMutex.RUnlock()

			// Check if token is any supported index token (NIFTY, BANKNIFTY, SENSEX, FINNIFTY, MIDCPNIFTY), persist completed 5m candle into DB
			isIndexToken := false
			for _, spec := range data.GetAllSupportedIndices() {
				if candle.Token == spec.SpotToken {
					isIndexToken = true
					break
				}
			}

			if (symbol == "" || tb.IsStockExcluded(symbol)) && !isIndexToken {
				continue
			}

			if isIndexToken {
				color := "DOJI"
				if candle.Close > candle.Open {
					color = "GREEN"
				} else if candle.Close < candle.Open {
					color = "RED"
				}
				_ = tb.db.InsertCandle("candles_5m", candle.Token, candle.Time, candle.Open, candle.High, candle.Low, candle.Close, candle.Volume, candle.VWAP, candle.Low, candle.High, 500, color)
				continue
			}

			// Inform active strategies configured for 5m candles (or default)
			for _, strat := range tb.activeStrategies {
				tf := strat.CandleTimeFrame()
				if tf == "5m" || tf == "" {
					strat.OnCandleClose(candle, symbol)
				}
			}

		case candle := <-candles1mChan:
			if candle == nil {
				continue
			}

			// Map token to symbol
			var symbol string
			tb.watchlistMutex.RLock()
			for sym, tok := range tb.watchlist {
				if tok == candle.Token {
					symbol = sym
					break
				}
			}
			tb.watchlistMutex.RUnlock()

			// Check if token is any supported index token, persist completed 1m candle into DB
			isIndexToken := false
			for _, spec := range data.GetAllSupportedIndices() {
				if candle.Token == spec.SpotToken {
					isIndexToken = true
					break
				}
			}

			if (symbol == "" || tb.IsStockExcluded(symbol)) && !isIndexToken {
				continue
			}

			if isIndexToken {
				color := "DOJI"
				if candle.Close > candle.Open {
					color = "GREEN"
				} else if candle.Close < candle.Open {
					color = "RED"
				}
				_ = tb.db.InsertCandle("candles_1m", candle.Token, candle.Time, candle.Open, candle.High, candle.Low, candle.Close, candle.Volume, candle.VWAP, candle.Low, candle.High, 500, color)
				continue
			}

			// Inform active strategies configured for 1m candles
			for _, strat := range tb.activeStrategies {
				tf := strat.CandleTimeFrame()
				if tf == "1m" {
					strat.OnCandleClose(candle, symbol)
				}
			}
		}
	}
}

// GetIndexConfig returns the stored OptionsIndexConfig for an index symbol
func (tb *TradingBot) GetIndexConfig(indexSymbol string) *data.OptionsIndexConfig {
	tb.optIndexConfigsMutex.RLock()
	defer tb.optIndexConfigsMutex.RUnlock()
	if tb.optIndexConfigs == nil {
		return nil
	}
	spec, _ := data.ResolveIndexSpec(indexSymbol)
	if cfg, ok := tb.optIndexConfigs[spec.Name]; ok {
		return cfg
	}
	if cfg, ok := tb.optIndexConfigs[spec.CleanPrefix]; ok {
		return cfg
	}
	return nil
}

// runOptionsBotLoop runs the Triple SuperTrend Options strategy loop continuously
func (tb *TradingBot) runOptionsBotLoop(loc *time.Location) {
	defer tb.wg.Done()

	tb.logger.Info("Triple SuperTrend Options Bot loop started", nil)

	strikeSelector := selection.NewOptionStrikeSelector(tb.securityMaster)
	optionsExec := execution.NewOptionsExecutor(tb.kiteClient, tb.logger.Logger, tb.cfg.Options.LiveTrading)
	if tb.cfg.Options.LimitBufferPct > 0 {
		optionsExec.SetLimitBufferPct(tb.cfg.Options.LimitBufferPct)
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var lastSeenDay string

	for {
		select {
		case <-tb.ctx.Done():
			tb.logger.Info("Options Bot loop shutting down...", nil)
			return
		case <-ticker.C:
			nowIST := time.Now().In(loc)
			dayStr := nowIST.Format("2006-01-02")

			isNewDay := lastSeenDay != dayStr
			if isNewDay {
				lastSeenDay = dayStr
			}

			// Sync historical 5m candles across active indices (throttled to once per 60 seconds)
			if time.Since(tb.lastNiftyHistSync) >= 60*time.Second && nowIST.Second() < 5 {
				tb.lastNiftyHistSync = time.Now()
				tb.ensureOptionsHistoricalData()
			}

			activeIndices := []string{"NIFTY 50", "NIFTY BANK", "BSE SENSEX", "FINNIFTY", "MIDCPNIFTY"}

			for _, idxName := range activeIndices {
				spec, _ := data.ResolveIndexSpec(idxName)
				mgr := tb.GetOptionsPosManager(spec.Name)
				if mgr == nil {
					continue
				}

				idxCfg := tb.GetIndexConfig(spec.Name)
				if idxCfg != nil && !idxCfg.IsActive {
					continue // Index disabled in settings
				}

				isIndexLive := false
				if idxCfg != nil {
					isIndexLive = idxCfg.IsLive
				} else {
					isIndexLive = tb.cfg.Options.IsIndexLiveTrading(spec.Name)
				}

				// Per-index cutoff timings & risk params
				autoSquareTime := tb.cfg.Options.AutoSquareOffTime
				lastTradeTime := tb.cfg.Options.LastNewTradeTime
				stCutoffTime := tb.cfg.Options.SuperTrendCutoffTime
				targetPrem := tb.cfg.Options.TargetEntryPremium
				expiryType := tb.cfg.Options.ExpiryType
				nextMonthDays := tb.cfg.Options.NextMonthDays
				trailSLEnabled := tb.cfg.Options.TrailSLEnabled

				if idxCfg != nil {
					if idxCfg.AutoSquareOffTime != "" {
						autoSquareTime = idxCfg.AutoSquareOffTime
					}
					if idxCfg.LastNewTradeTime != "" {
						lastTradeTime = idxCfg.LastNewTradeTime
					}
					if idxCfg.SuperTrendCutoffTime != "" {
						stCutoffTime = idxCfg.SuperTrendCutoffTime
					}
					if idxCfg.TargetEntryPremium > 0 {
						targetPrem = idxCfg.TargetEntryPremium
					}
					if idxCfg.ExpiryType != "" {
						expiryType = idxCfg.ExpiryType
					}
					if idxCfg.NextMonthDays > 0 {
						nextMonthDays = idxCfg.NextMonthDays
					}
					trailSLEnabled = idxCfg.TrailSLEnabled
				}

				if targetPrem <= 0 {
					targetPrem = spec.DefaultTargetPremium
				}

				nowSecOfDay := nowIST.Hour()*3600 + nowIST.Minute()*60 + nowIST.Second()
				sqSecOfDay := data.ParseTimeToSeconds(autoSquareTime)
				isEOD := nowSecOfDay >= sqSecOfDay

				lastSecOfDay := data.ParseTimeToSeconds(lastTradeTime)
				isPastLastNewTradeTime := nowSecOfDay >= lastSecOfDay

				stCutoffSecOfDay := data.ParseTimeToSeconds(stCutoffTime)
				isBeforeMarketOpen := (nowIST.Hour() < 9) || (nowIST.Hour() == 9 && nowIST.Minute() < 15)

				// Rule 1: Do NOT evaluate strategy or take trades before today's 1st 5m candle closes at 09:20 AM IST
				todayFirstCandleClose := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 9, 20, 0, 0, loc)
				if nowIST.Before(todayFirstCandleClose) {
					continue
				}

				if isNewDay {
					mgr.ResetDailyState()
				}

				// 1. If Position Active: Check 50% Stop-Loss & EOD Auto Square-Off
				status := mgr.GetStatus()
				activeSym, _ := status["active_symbol"].(string)
				hasActive := activeSym != ""

				if hasActive {
					activeQty, _ := status["active_qty"].(int)
					entryPrem, _ := status["entry_premium"].(float64)

					ltp := 0.0
					if tb.kiteClient != nil {
						quoteKey := spec.OptionsExchange + ":" + activeSym
						if quotes, err := tb.kiteClient.GetQuote(quoteKey); err == nil {
							if q, ok := quotes[quoteKey]; ok && q.LastPrice > 0 {
								ltp = q.LastPrice
							}
						}
					}

					// If quote is available, evaluate SL hit. Never fall back to fictitious entryPrem for SL check.
					if ltp > 0 && mgr.CheckTick(ltp) {
						tb.logger.Warn("[SL-HIT] Option premium breached 50% SL!", map[string]interface{}{"index": spec.Name, "symbol": activeSym, "ltp": ltp, "is_live": isIndexLive})
						optPos := mgr.GetActivePosition()
						timeHeldMins := 5
						if optPos != nil {
							timeHeldMins = int(nowIST.Sub(optPos.CreatedAt).Minutes())
							if timeHeldMins < 1 {
								timeHeldMins = 1
							}
						}
						_, fillPrice, err := optionsExec.ExecuteOptionOrder(activeSym, "BUY", activeQty, ltp, spec.OptionsExchange, isIndexLive)
						if err == nil {
							if optPos != nil && optPos.SLOrderID != "" {
								_ = optionsExec.CancelOptionOrder(optPos.SLOrderID, isIndexLive)
							}
							_ = mgr.OnSLHit(fillPrice)
							if optPos != nil {
								_ = tb.db.CloseOpenPosition(tb.ctx, optPos.OrderID, fillPrice)
							}
							_ = mgr.SaveState(tb.ctx)
							hasActive = false
						}
					} else if isEOD {
						exitPrice := ltp
						if exitPrice <= 0 {
							exitPrice = entryPrem
						}
						tb.logger.Info("[EOD AUTO SQUARE-OFF] Closing active option position for EOD", map[string]interface{}{"index": spec.Name, "symbol": activeSym, "is_live": isIndexLive})
						optPos := mgr.GetActivePosition()
						_, fillPrice, err := optionsExec.ExecuteOptionOrder(activeSym, "BUY", activeQty, exitPrice, spec.OptionsExchange, isIndexLive)
						if err == nil {
							if optPos != nil && optPos.SLOrderID != "" {
								_ = optionsExec.CancelOptionOrder(optPos.SLOrderID, isIndexLive)
							}
							_ = mgr.OnTradeClosed(fillPrice, "EOD SQUARE-OFF")
							if optPos != nil {
								_ = tb.db.CloseOpenPosition(tb.ctx, optPos.OrderID, fillPrice)
							}
							_ = mgr.SaveState(tb.ctx)
							hasActive = false
						}
					}
				}

				// Check EOD cutoff before evaluating new signals or reversals
				if isEOD {
					continue
				}

				// 2. Query last 100 5m candles for this index
				candles, err := tb.db.GetLastNCandles("candles_5m", spec.SpotToken, 100)
				if err != nil || len(candles) < 20 {
					continue
				}

				// Strict Rule 25: Filter candles to include ONLY fully completed closed candles
				nowFloored := nowIST.Truncate(5 * time.Minute)
				var completedCandles []data.Candle
				for _, c := range candles {
					cIST := data.NormalizeToIST(c.Time)
					if cIST.Before(nowFloored) {
						cSecOfDay := cIST.Hour()*3600 + cIST.Minute()*60 + cIST.Second()
						if cSecOfDay <= stCutoffSecOfDay {
							completedCandles = append(completedCandles, c)
						}
					}
				}

				if len(completedCandles) < 20 {
					continue
				}

				// Calculate 3 SuperTrends on completed candles
				stEngine := strategy.NewSuperTrendOptionsEngineFromConfig(idxCfg)
				res := stEngine.CalculateTripleSuperTrend(completedCandles)

				action, qty := mgr.EvaluateSignal(res.Trend)

				if !isBeforeMarketOpen && !isEOD {
					// Evaluate Trailing Stop-Loss ONLY on 5m candle close boundaries (not on every second)
					is5mBoundary := (nowIST.Minute()%5 == 0) && nowIST.Second() < 10
					if hasActive && action != "REVERSAL" && trailSLEnabled && is5mBoundary {
						optPos := mgr.GetActivePosition()
						if optPos != nil {
							currPrem := optPos.LatestPrice
							if tb.kiteClient != nil {
								quoteKey := spec.OptionsExchange + ":" + activeSym
								if quotes, err := tb.kiteClient.GetQuote(quoteKey); err == nil {
									if q, ok := quotes[quoteKey]; ok && q.LastPrice > 0 {
										currPrem = q.LastPrice
									}
								}
							}

							bufferPct := 5.0
							if idxCfg != nil && idxCfg.TrailSLBufferPct > 0 {
								bufferPct = idxCfg.TrailSLBufferPct
							} else if tb.cfg.Options.TrailSLBufferPct > 0 {
								bufferPct = tb.cfg.Options.TrailSLBufferPct
							}

							trailed := false
							newSL := 0.0

							// 1. Primary: Apply SuperTrend directly on Option Price Chart 5m candles
							var optCandles []data.Candle
							optToken, tokenErr := tb.securityMaster.GetInstrumentToken(activeSym)
							if tokenErr == nil && optToken > 0 {
								// Fetch 5m candles from Zerodha API if available
								if tb.kiteClient != nil {
									startTime := nowIST.AddDate(0, 0, -4)
									if apiCandles, apiErr := tb.kiteClient.GetHistoricalData(int(optToken), "5minute", startTime, nowIST, false, false); apiErr == nil && len(apiCandles) > 0 {
										for _, c := range apiCandles {
											cDateIST := time.Date(c.Date.Year(), c.Date.Month(), c.Date.Day(), c.Date.Hour(), c.Date.Minute(), c.Date.Second(), 0, data.ISTLocation)
											color := "DOJI"
											if c.Close > c.Open {
												color = "GREEN"
											} else if c.Close < c.Open {
												color = "RED"
											}
											vwap := (c.Open + c.High + c.Low + c.Close) / 4.0
											_ = tb.db.InsertCandle("candles_5m", optToken, cDateIST, c.Open, c.High, c.Low, c.Close, int64(c.Volume), vwap, c.Low, c.High, 500, color)
										}
									}
								}

								// Query candles from DB
								if tb.db != nil {
									if dbCandles, dbErr := tb.db.GetLastNCandles("candles_5m", optToken, 100); dbErr == nil && len(dbCandles) > 0 {
										// Filter to completed closed candles
										for _, c := range dbCandles {
											cIST := data.NormalizeToIST(c.Time)
											if cIST.Before(nowFloored) {
												optCandles = append(optCandles, c)
											}
										}
									}
								}
							}

							if len(optCandles) >= 10 {
								newSL, trailed = mgr.TrailSLWithOptionSuperTrend(optCandles, bufferPct, stEngine)
							}

							if trailed {
								_ = mgr.SaveState(tb.ctx)
								if tb.db != nil {
									_ = tb.db.SaveOpenPosition(tb.ctx, optPos.OrderID, optPos.Symbol, optPos.Quantity, optPos.EntryPremium, optPos.Side, newSL, "OPTIONS_SUPERTREND", optPos.SLOrderID)
								}
								if optPos.SLOrderID != "" {
									_ = optionsExec.ModifyOptionSLOrder(optPos.SLOrderID, optPos.Symbol, optPos.Quantity, newSL, spec.OptionsExchange, isIndexLive)
								}
								tb.logger.Info("[OPTIONS SL TRAILED] Successfully ratcheted SL using Option Price Chart SuperTrend",
									map[string]interface{}{
										"index":           spec.Name,
										"symbol":          activeSym,
										"current_premium": currPrem,
										"new_sl":          newSL,
										"buffer_pct":      bufferPct,
										"candles_count":   len(optCandles),
										"is_live":         isIndexLive,
									},
								)
							}
						}
					}

					if action == "REVERSAL" && hasActive {
						activeQty, _ := status["active_qty"].(int)
						optPos := mgr.GetActivePosition()
						exitPrem := 0.0
						if optPos != nil {
							exitPrem = optPos.LatestPrice
							if exitPrem <= 0 {
								exitPrem = optPos.EntryPremium
							}
						}
						if tb.kiteClient != nil {
							quoteKey := spec.OptionsExchange + ":" + activeSym
							if quotes, err := tb.kiteClient.GetQuote(quoteKey); err == nil {
								if q, ok := quotes[quoteKey]; ok && q.LastPrice > 0 {
									exitPrem = q.LastPrice
								}
							}
						}
						if exitPrem > 0 {
							_, fillPrice, err := optionsExec.ExecuteOptionOrder(activeSym, "BUY", activeQty, exitPrem, spec.OptionsExchange, isIndexLive)
							if err == nil {
								if optPos != nil && optPos.SLOrderID != "" {
									_ = optionsExec.CancelOptionOrder(optPos.SLOrderID, isIndexLive)
								}
								_ = mgr.OnTradeClosed(fillPrice, "REVERSAL EXIT")
								if optPos != nil {
									_ = tb.db.CloseOpenPosition(tb.ctx, optPos.OrderID, fillPrice)
								}
								_ = mgr.SaveState(tb.ctx)
								hasActive = false
								tb.logger.Info("[REVERSAL EXIT] Active option trade squared off on trend reversal", map[string]interface{}{"index": spec.Name, "symbol": activeSym, "exit_price": fillPrice, "is_live": isIndexLive})
							}
						}
					}

					if !isPastLastNewTradeTime && (action == "OPEN_INITIAL" || action == "REVERSAL") {
						lastSpot := completedCandles[len(completedCandles)-1].Close
						strikeRes, err := strikeSelector.SelectStrikeByTargetPremium(
							spec.Name, lastSpot, res.Trend,
							targetPrem, expiryType, nextMonthDays,
							tb.kiteClient,
						)
						if err != nil {
							tb.logger.Error("Failed to select OTM strike", map[string]interface{}{"index": spec.Name, "error": err.Error()})
							continue
						}

						realOptionPremium := strikeRes.SelectedLTP
						if tb.kiteClient != nil {
							quoteKey := strikeRes.Exchange + ":" + strikeRes.OptionSymbol
							if quotes, err := tb.kiteClient.GetQuote(quoteKey); err == nil && len(quotes) > 0 {
								if q, ok := quotes[quoteKey]; ok && q.LastPrice > 0 {
									realOptionPremium = q.LastPrice
									tb.logger.Info("Fetched 100% real live Zerodha option market price", map[string]interface{}{"index": spec.Name, "quote_key": quoteKey, "live_price": realOptionPremium, "is_live": isIndexLive})
								}
							} else if isIndexLive {
								tb.logger.Error("Failed to fetch live Zerodha option market quote in LIVE mode - trade aborted", map[string]interface{}{"error": err, "quote_key": quoteKey})
								continue
							}
						} else if isIndexLive {
							tb.logger.Error("Zerodha Kite client uninitialized in LIVE mode - trade aborted", map[string]interface{}{"index": spec.Name, "symbol": strikeRes.OptionSymbol})
							continue
						}

						if realOptionPremium <= 0 {
							realOptionPremium = targetPrem
						}

						orderID, fillPrice, err := optionsExec.ExecuteOptionOrder(strikeRes.OptionSymbol, "SELL", qty, realOptionPremium, strikeRes.Exchange, isIndexLive)
						if err != nil {
							tb.logger.Error("Failed to execute option order", map[string]interface{}{"error": err.Error(), "symbol": strikeRes.OptionSymbol})
							continue
						}

						mgr.OnTradeOpened(orderID, strikeRes.OptionSymbol, strikeRes.OptionType, qty, fillPrice, strikeRes.ExpiryDate, nowIST)

						slTriggerPrice := mgr.CalculateSLPrice(fillPrice)
						slOrderID, _ := optionsExec.PlaceOptionSLOrder(strikeRes.OptionSymbol, qty, slTriggerPrice, strikeRes.Exchange, isIndexLive)
						if slOrderID != "" {
							mgr.SetSLOrderID(slOrderID)
						}

						if tb.db != nil {
							_ = tb.db.SaveOpenPosition(tb.ctx, orderID, strikeRes.OptionSymbol, qty, fillPrice, "SELL", slTriggerPrice, "OPTIONS_SUPERTREND", slOrderID)
						}

						_ = mgr.SaveState(tb.ctx)
					} else if isPastLastNewTradeTime && action == "REVERSAL" {
						tb.logger.Info("[POST-3PM REVERSAL] Reversal occurred after OPTIONS_LAST_NEW_TRADE_TIME cutoff. Active trade exited, new trade skipped.", map[string]interface{}{
							"index":  spec.Name,
							"time":   nowIST.Format("15:04:05"),
							"cutoff": tb.cfg.Options.LastNewTradeTime,
						})
					}
				}
			}
		}
	}
}

// monitoringLoop handles health checks and P&L logging

// monitoringLoop handles health checks and P&L logging
func (tb *TradingBot) monitoringLoop() {
	defer tb.wg.Done()

	tb.logger.Info("Monitoring loop started", nil)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	lastMarginCheck := time.Now()
	lastPnLLog := time.Now()

	for {
		select {
		case <-tb.ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()

			ticks, loss := tb.ticker.GetMetrics()
			tb.logger.Info("Ticker Health Status", map[string]interface{}{
				"ticks_received": ticks,
				"packet_loss":    loss,
				"connected":      tb.ticker.IsConnected(),
			})

			if now.Sub(lastMarginCheck) > 5*time.Minute {
				tb.resilientExec.HandleMarginChange(50000)
				lastMarginCheck = now
			}

			if now.Sub(lastPnLLog) > 15*time.Minute {
				metrics := tb.riskMgr.GetMetrics()
				tb.logger.Info("P&L Update", map[string]interface{}{
					"daily_pnl":    metrics["daily_pnl"].(float64),
					"trades":       metrics["trades_today"].(int),
					"drawdown_pct": metrics["drawdown_pct"].(float64),
				})

				lastPnLLog = now
			}

			metrics := tb.riskMgr.GetMetrics()
			if metrics["circuit_breaker_active"].(bool) {
				tb.logger.Warn("Equity Circuit Breaker Active: New equity orders paused for today", map[string]interface{}{
					"daily_pnl": metrics["daily_pnl"],
				})
			}
		}
	}
}

func (tb *TradingBot) startupChecks() error {
	tb.logger.Info("Running startup checks...", nil)

	now := time.Now()
	if now.Hour() < 9 || (now.Hour() == 9 && now.Minute() < 15) || now.Hour() > 15 {
		tb.logger.Warn("Market closed, but continuing anyway", map[string]interface{}{})
	}

	tb.logger.Info("✓ Startup checks passed", nil)
	return nil
}

func (tb *TradingBot) waitForShutdown() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	tb.logger.Info("Shutdown signal received", nil)

	tb.shutdown()
}

func (tb *TradingBot) shutdown() {
	tb.logger.Info("Initiating shutdown...", nil)
	tb.running = false
	tb.cancel()

	positions := tb.riskMgr.GetOpenPositions()
	for orderID, pos := range positions {
		if tb.execMgr.LiveTrading && tb.cfg.SquareOffOnShutdown {
			// Live trading safety square-off: place opposite market order
			var txnType string
			if pos.Side == "BUY" {
				txnType = "SELL"
			} else {
				txnType = "BUY"
			}

			orderReq := execution.OrderRequest{
				TradingSymbol:   pos.Symbol,
				Exchange:        "NSE",
				Quantity:        pos.Quantity,
				TransactionType: txnType,
				OrderType:       execution.OrderType(tb.cfg.DefaultOrderType),
				Product:         "MIS",
				Validity:        "DAY",
			}
			if orderReq.OrderType == execution.OrderTypeLimit {
				price := pos.LatestPrice
				if price == 0 {
					price = pos.EntryPrice
				}
				orderReq.Price = &price
			}

			_, err := tb.execMgr.PlaceOrder(orderReq)
			if err != nil {
				tb.logger.Error("Failed to square off position on shutdown", map[string]interface{}{"error": err.Error(), "symbol": pos.Symbol})
			} else {
				tb.logger.Info("Squared off live position on shutdown", map[string]interface{}{"symbol": pos.Symbol, "qty": pos.Quantity})
				tb.riskMgr.OnOrderClose(orderID, pos.LatestPrice, pos.Quantity)
				_ = tb.db.CloseOpenPosition(tb.ctx, orderID, pos.LatestPrice)
			}
		} else if !tb.execMgr.LiveTrading {
			tb.execMgr.CancelOrder(orderID)
			tb.riskMgr.OnOrderClose(orderID, pos.LatestPrice, pos.Quantity)
			_ = tb.db.CloseOpenPosition(tb.ctx, orderID, pos.LatestPrice)
		} else {
			tb.execMgr.CancelOrder(orderID)
			_ = tb.db.CloseOpenPosition(tb.ctx, orderID, pos.LatestPrice)
		}
	}

	done := make(chan struct{})
	go func() {
		tb.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		tb.logger.Warn("Shutdown timeout exceeded", map[string]interface{}{})
	}

	tb.ticker.Close()
	tb.db.Close()

	metrics := tb.riskMgr.GetMetrics()
	tb.logger.Info("=== Bot Shutdown Complete ===", map[string]interface{}{
		"final_pnl":    metrics["daily_pnl"].(float64),
		"total_trades": metrics["closed_trades"].(int),
	})

	tb.logger.Sync()
}

func (tb *TradingBot) startWebDashboard() {
	mux := http.NewServeMux()

	mux.HandleFunc("/zt", tb.handleDashboard)
	mux.HandleFunc("/api/watchlist", tb.handleWatchlist)
	mux.HandleFunc("/api/candles", tb.handleCandles)
	mux.HandleFunc("/api/trades", tb.handleTrades)
	mux.HandleFunc("/api/trades/all", tb.handleTradesAll)
	mux.HandleFunc("/api/manual-watchlist", tb.handleDailyManualWatchlist)
	mux.HandleFunc("/api/watchlist/recalculate", tb.handleRecalculateWatchlist)
	mux.HandleFunc("/api/daily-watchlists", tb.handleDailyWatchlistsHistory)
	mux.HandleFunc("/api/config/access-token", tb.handleConfigAccessToken)
	mux.HandleFunc("/api/config/all", tb.handleConfigAll)
	mux.HandleFunc("/api/config/save", tb.handleConfigSave)
	mux.HandleFunc("/api/system/restart", tb.handleSystemRestart)
	mux.HandleFunc("/api/positions", tb.handleActivePositions)
	mux.HandleFunc("/api/options/state", tb.handleOptionsState)
	mux.HandleFunc("/api/options/indices", tb.handleOptionsIndices)
	mux.HandleFunc("/api/options/reset", tb.handleOptionsReset)
	mux.HandleFunc("/api/options/supertrends", tb.handleOptionsSuperTrends)
	mux.HandleFunc("/api/options/expected-move", tb.handleOptionsExpectedMove)
	mux.HandleFunc("/api/options/mode", tb.handleOptionsMode)
	mux.HandleFunc("/api/scanner/results", tb.handleScannerResults)
	mux.HandleFunc("/api/scanner/dates", tb.handleScannerDates)
	mux.HandleFunc("/api/scanner/run", tb.handleScannerRun)
	mux.HandleFunc("/api/seeder/status", tb.handleSeederStatus)
	mux.HandleFunc("/api/seeder/run", tb.handleSeederRun)
	mux.HandleFunc("/api/daily-watchlist/update-strategy", tb.handleUpdateDailyWatchlistStrategy)
	mux.HandleFunc("/api/exclude-stock", tb.handleExcludeStock)
	mux.HandleFunc("/api/sectors", tb.handleSectors)
	mux.HandleFunc("/api/sectors/reset", tb.handleResetSectors)
	mux.HandleFunc("/api/manual-trades/sync", tb.handleManualTradesSync)
	mux.HandleFunc("/api/manual-trades/status", tb.handleManualTradesStatus)
	mux.HandleFunc("/", tb.handleRootRedirect)

	tb.logger.Info("Starting interactive web dashboard on port :8080...", nil)
	srv := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	// Handle graceful shutdown of the HTTP server
	go func() {
		<-tb.ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		tb.logger.Error("Web dashboard server failed", map[string]interface{}{"error": err.Error()})
	}
}

// triggerPreMarketSeeding runs the automated historical candle seeder in background
func (tb *TradingBot) triggerPreMarketSeeding(phase string) {
	if tb.seeder == nil {
		return
	}
	go func() {
		tb.logger.Info(fmt.Sprintf("[PRE_MARKET_SEEDER] Starting %s automated historical data seeding...", phase), nil)
		if err := tb.seeder.RunPreMarketSeeding(context.Background()); err != nil {
			tb.logger.Error(fmt.Sprintf("[PRE_MARKET_SEEDER] %s seeding error", phase), map[string]interface{}{"error": err.Error()})
		}
	}()
}

// initializeNifty50PDH_PDL fetches Nifty 50 constituents and ensures their previous day's candles are stored in DB.
func (tb *TradingBot) initializeNifty50PDH_PDL(loc *time.Location) {
	tb.logger.Info("Initializing Nifty 50 previous day high/low reference database...", nil)
	nifty50Map, err := tb.securityMaster.GetNifty50Constituents(tb.ctx)
	if err != nil {
		tb.logger.Error("Failed to fetch Nifty 50 constituents for startup PDH/PDL caching", map[string]interface{}{"error": err.Error()})
		return
	}

	countCached := 0
	countFetched := 0

	for symbol, token := range nifty50Map {
		_, _, _, _, err := tb.queryPreviousDayHighLow(token, loc)
		if err == nil {
			countCached++
			continue
		}

		// Not in database, fetch from Zerodha
		if err := tb.fetchAndStorePreviousDayCandles(token, symbol, loc); err != nil {
			tb.logger.Error("Failed to cache previous day candles for Nifty 50 stock on startup", map[string]interface{}{
				"symbol": symbol,
				"error":  err.Error(),
			})
		} else {
			countFetched++
		}
	}

	tb.logger.Info("Nifty 50 startup PDH/PDL caching complete", map[string]interface{}{
		"already_cached": countCached,
		"newly_fetched":  countFetched,
	})
}

func parseTimeHM(timeStr string) (int, int, error) {
	return data.ParseTimeHM(timeStr)
}

// getLeverage retrieves the cached leverage for a symbol, defaulting to 5.0
func (tb *TradingBot) getLeverage(symbol string) float64 {
	tb.leverageMutex.RLock()
	defer tb.leverageMutex.RUnlock()
	if lev, exists := tb.watchlistLeverage[symbol]; exists && lev > 0 {
		return lev
	}
	return 5.0
}

// loadTickSizes fetches the NSE instrument list from Zerodha and caches the tick sizes
func (tb *TradingBot) loadTickSizes() {
	tb.logger.Info("Loading NSE instrument tick sizes in background...", nil)
	instruments, err := tb.kiteClient.GetInstrumentsByExchange("NSE")
	if err != nil {
		tb.logger.Error("Failed to fetch NSE instruments for tick sizes, using static fallback map", map[string]interface{}{"error": err.Error()})
		return
	}

	tb.tickSizesMutex.Lock()
	defer tb.tickSizesMutex.Unlock()

	for _, inst := range instruments {
		if inst.Segment == "NSE" && inst.InstrumentType == "EQ" {
			tb.tickSizes[inst.TradingSymbol] = inst.TickSize
		}
	}
	tb.logger.Info("Successfully loaded background NSE tick size cache", map[string]interface{}{"count": len(tb.tickSizes)})
}

// getTickSize retrieves the tick size for a symbol, defaulting to 0.05
func (tb *TradingBot) getTickSize(symbol string) float64 {
	tb.tickSizesMutex.RLock()
	size, exists := tb.tickSizes[symbol]
	tb.tickSizesMutex.RUnlock()

	if exists && size > 0 {
		return size
	}
	return 0.05
}

// getBroadSubscriptionTokens retrieves all F&O stock tokens and Nifty 50 constituent tokens
func (tb *TradingBot) getBroadSubscriptionTokens() ([]int64, error) {
	tokensMap := make(map[int64]bool)

	// 1. Fetch active F&O stocks
	foStocks, err := tb.securityMaster.GetFOStocks(tb.ctx)
	if err != nil {
		tb.logger.Warn("Failed to fetch F&O stocks for broad subscription. Continuing with Nifty 50 only.", map[string]interface{}{"error": err.Error()})
	} else {
		for _, token := range foStocks {
			if token > 0 {
				tokensMap[token] = true
			}
		}
	}

	// 2. Fetch Nifty 50 constituents
	nifty50, err := tb.securityMaster.GetNifty50Constituents(tb.ctx)
	if err != nil {
		tb.logger.Warn("Failed to fetch Nifty 50 constituents for broad subscription.", map[string]interface{}{"error": err.Error()})
	} else {
		for _, token := range nifty50 {
			if token > 0 {
				tokensMap[token] = true
			}
		}
	}

	// 3. Add Nifty Index Tokens (99926009 & 256265)
	tokensMap[99926009] = true
	tokensMap[256265] = true

	// 4. Save to tb.broadSubscriptionTokens in memory
	tb.broadTokensMutex.Lock()
	for token := range tokensMap {
		tb.broadSubscriptionTokens[token] = true
	}
	tb.broadTokensMutex.Unlock()

	// 5. Convert to slice
	tokens := make([]int64, 0, len(tokensMap))
	for token := range tokensMap {
		tokens = append(tokens, token)
	}

	return tokens, nil
}

// isBroadSubscriptionToken checks if a token is part of the broad subscription watchlist
func (tb *TradingBot) isBroadSubscriptionToken(token int64) bool {
	tb.broadTokensMutex.RLock()
	defer tb.broadTokensMutex.RUnlock()
	return tb.broadSubscriptionTokens[token]
}

// isManualStock checks if a symbol was added as a manual stock for today
func (tb *TradingBot) isManualStock(symbol string) bool {
	tb.watchlistSelectorMapMutex.RLock()
	assigned, exists := tb.watchlistSelectorMap[symbol]
	tb.watchlistSelectorMapMutex.RUnlock()
	if exists && (strings.HasPrefix(assigned, "MANUAL") || assigned == "MA" || assigned == "MANUAL") {
		return true
	}

	manualStocks, err := tb.db.GetDailyManualWatchlist(tb.ctx, time.Now().In(data.ISTLocation))
	if err == nil {
		for _, m := range manualStocks {
			parts := strings.Split(m, ":")
			if strings.TrimSpace(parts[0]) == symbol {
				return true
			}
		}
	}
	return false
}

// setAutoSelectionDone sets the autoSelectionDoneToday flag thread-safely
func (tb *TradingBot) setAutoSelectionDone(done bool) {
	tb.autoSelectionMutex.Lock()
	tb.autoSelectionDoneToday = done
	tb.autoSelectionMutex.Unlock()
}

// isAutoSelectionDone checks the autoSelectionDoneToday flag thread-safely
func (tb *TradingBot) isAutoSelectionDone() bool {
	tb.autoSelectionMutex.RLock()
	defer tb.autoSelectionMutex.RUnlock()
	return tb.autoSelectionDoneToday
}

// GetOptionsPosManager returns the position manager for a given index symbol or default
func (tb *TradingBot) GetOptionsPosManager(indexSym string) *risk.OptionsPositionManager {
	spec, _ := data.ResolveIndexSpec(indexSym)
	if tb.optionsPosMgrs != nil {
		if mgr, ok := tb.optionsPosMgrs[spec.Name]; ok && mgr != nil {
			return mgr
		}
		if mgr, ok := tb.optionsPosMgrs[spec.CleanPrefix]; ok && mgr != nil {
			return mgr
		}
	}
	return tb.optionsPosMgr
}

// ensureOptionsHistoricalData fetches historical 5m candles for specified or active indices up to current time
func (tb *TradingBot) ensureOptionsHistoricalData(indexNames ...string) {
	if tb.kiteClient == nil {
		return
	}

	targets := indexNames
	if len(targets) == 0 {
		targets = []string{"NIFTY 50", "NIFTY BANK", "BSE SENSEX", "FINNIFTY", "MIDCPNIFTY"}
	}

	now := time.Now().In(data.ISTLocation)
	startDate := now.AddDate(0, 0, -5)

	for _, idxName := range targets {
		time.Sleep(500 * time.Millisecond)
		spec, _ := data.ResolveIndexSpec(idxName)
		token := spec.SpotToken
		if token <= 0 {
			continue
		}

		tb.logger.Info("Syncing latest 5m historical candles from Zerodha API...", map[string]interface{}{"index": spec.Name, "token": token, "from": startDate.Format("2006-01-02 15:04:05"), "to": now.Format("2006-01-02 15:04:05")})

		var hist []data.HistoricalData
		var err error
		for attempt := 1; attempt <= 3; attempt++ {
			hist, err = tb.kiteClient.GetHistoricalData(int(token), "5minute", startDate, now, false, false)
			if err == nil && len(hist) > 0 {
				break
			}
			time.Sleep(1000 * time.Millisecond)
		}
		if err != nil || len(hist) == 0 {
			tb.logger.Error("Failed to fetch historical candles for index", map[string]interface{}{"index": spec.Name, "token": token, "error": fmt.Sprintf("%v", err)})
			continue
		}

		inserted := 0
		for _, c := range hist {
			color := "DOJI"
			if c.Close > c.Open {
				color = "GREEN"
			} else if c.Close < c.Open {
				color = "RED"
			}
			vwap := (c.Open + c.High + c.Low + c.Close) / 4.0
			cDateIST := time.Date(c.Date.Year(), c.Date.Month(), c.Date.Day(), c.Date.Hour(), c.Date.Minute(), c.Date.Second(), 0, data.ISTLocation)
			err := tb.db.InsertCandle("candles_5m", token, cDateIST, c.Open, c.High, c.Low, c.Close, int64(c.Volume), vwap, c.Low, c.High, 500, color)
			if err == nil {
				inserted++
			}
		}
		tb.logger.Info("Seeded/Updated 5m historical candles successfully into DB", map[string]interface{}{"index": spec.Name, "inserted": inserted, "total_fetched": len(hist)})
	}
}

// ensureNifty50OptionsHistoricalData wraps ensureOptionsHistoricalData for backwards compatibility
func (tb *TradingBot) ensureNifty50OptionsHistoricalData() {
	tb.ensureOptionsHistoricalData()
}

// ExcludeStock marks a symbol as manually excluded from trade consideration
func (tb *TradingBot) ExcludeStock(symbol string) {
	tb.excludedStocksMutex.Lock()
	defer tb.excludedStocksMutex.Unlock()
	if tb.excludedStocks == nil {
		tb.excludedStocks = make(map[string]bool)
	}
	tb.excludedStocks[symbol] = true
}

// IsStockExcluded checks if a symbol is manually excluded from trading
func (tb *TradingBot) IsStockExcluded(symbol string) bool {
	tb.excludedStocksMutex.RLock()
	defer tb.excludedStocksMutex.RUnlock()
	if tb.excludedStocks == nil {
		return false
	}
	return tb.excludedStocks[symbol]
}

// ClearStockExclusion removes exclusion for a symbol
func (tb *TradingBot) ClearStockExclusion(symbol string) {
	tb.excludedStocksMutex.Lock()
	defer tb.excludedStocksMutex.Unlock()
	if tb.excludedStocks != nil {
		delete(tb.excludedStocks, symbol)
	}
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	bot, err := NewTradingBot(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create bot: %v\n", err)
		os.Exit(1)
	}

	if err := bot.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Bot error: %v\n", err)
		os.Exit(1)
	}
}
