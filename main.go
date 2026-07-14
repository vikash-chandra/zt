package main

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/execution"
	"zerodha-trading/monitoring"
	"zerodha-trading/risk"
	"zerodha-trading/selection"
	"zerodha-trading/strategy"
)

//go:embed index.html
var dashboardHTML []byte

// TradingBot is the main orchestrator
type TradingBot struct {
	cfg                      *config.Settings
	logger                   *monitoring.Logger
	db                       *data.Database
	ticker                   *data.RobustKiteTicker
	candleAgg                *data.CandleAggregator
	candleAgg1m              *data.CandleAggregator
	securityMaster           *data.SecurityMaster
	activeStrategies         []strategy.Strategy
	riskMgr                  *risk.RiskManager
	rrCalculator             risk.RiskRewardCalculator
	execMgr                  *execution.ExecutionManager
	statusTracker            *execution.StatusTracker
	resilientExec            *execution.ResilientExecutor
	kiteClient               *kiteconnect.Client
	globalBias               string
	watchlist                map[string]int64
	watchlistMutex           sync.RWMutex
	broadSubscriptionTokens  map[int64]bool
	broadTokensMutex         sync.RWMutex
	watchlistLeverage        map[string]float64
	leverageMutex            sync.RWMutex
	tickSizes                map[string]float64
	tickSizesMutex           sync.RWMutex
	activeSelectors          map[string]selection.Selector
	strategySelectorMap      map[string]string           // strategy name -> selector name
	strategyWatchlists       map[string]map[string]int64 // strategy name -> symbol -> token
	watchlistDirections      map[string]string           // symbol -> predicted_direction ("BULLISH BREAKOUT", "BEARISH BREAKDOWN")
	watchlistDirectionsMutex sync.RWMutex
	running                  bool
	ctx                      context.Context
	cancel                   context.CancelFunc
	wg                       sync.WaitGroup
}

// NewTradingBot creates a new bot instance
func NewTradingBot(cfg *config.Settings) (*TradingBot, error) {
	logger, db, err := initLoggerAndDatabase(cfg)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	// Try to load KITE_ACCESS_TOKEN from database cache (persistent across container restarts)
	cachedToken, err := db.GetMetadataCache(ctx, "config:kite_access_token", time.Time{})
	if err == nil && cachedToken != "" {
		cfg.AccessToken = cachedToken
		logger.Info("Loaded persistent KITE_ACCESS_TOKEN from database cache", nil)
	}

	// Create components
	ticker := data.NewRobustKiteTicker(cfg.APIKey, cfg.AccessToken, logger.Logger)
	candleAgg := data.NewCandleAggregator(db, logger.Logger, cfg.CandleIntervalSec, 100, "candles_5m")
	candleAgg1m := data.NewCandleAggregator(db, logger.Logger, 60, 100, "candles_1m")

	// Initialize Kite Connect API Client
	kiteClient := kiteconnect.New(cfg.APIKey)
	kiteClient.SetAccessToken(cfg.AccessToken)

	securityMaster := data.NewSecurityMaster(db, kiteClient, logger.Logger)

	// Modularized strategies, selectors and watchlist initialization
	activeStrategies, activeSelMap, stratSelMap, stratWatchlists := initStrategiesAndSelectors(cfg, logger, securityMaster)

	// Modularized risk manager and execution manager initialization
	riskMgr, rrCalculator, execMgr, statusTracker, resilientExec := initRiskAndExecution(cfg, db, logger, kiteClient)

	ctx, cancel := context.WithCancel(context.Background())

	logger.Info("Trading bot initialized successfully", nil)

	bot := &TradingBot{
		cfg:                 cfg,
		logger:              logger,
		db:                  db,
		ticker:              ticker,
		candleAgg:           candleAgg,
		candleAgg1m:         candleAgg1m,
		securityMaster:      securityMaster,
		activeStrategies:    activeStrategies,
		riskMgr:             riskMgr,
		rrCalculator:        rrCalculator,
		execMgr:             execMgr,
		statusTracker:       statusTracker,
		resilientExec:       resilientExec,
		kiteClient:          kiteClient,
		activeSelectors:     activeSelMap,
		strategySelectorMap: stratSelMap,
		strategyWatchlists:  stratWatchlists,
		watchlistLeverage:   make(map[string]float64),
		tickSizes:           make(map[string]float64),
		watchlistDirections: make(map[string]string),
		broadSubscriptionTokens: make(map[int64]bool),
		running:             false,
		ctx:                 ctx,
		cancel:              cancel,
	}

	// Load tick sizes in the background to avoid blocking the main startup sequence
	go bot.loadTickSizes()

	return bot, nil
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
func initRiskAndExecution(cfg *config.Settings, db *data.Database, logger *monitoring.Logger, kiteClient *kiteconnect.Client) (*risk.RiskManager, risk.RiskRewardCalculator, *execution.ExecutionManager, *execution.StatusTracker, *execution.ResilientExecutor) {
	ctx := context.Background()

	riskLimits := risk.RiskLimits{
		MaxTradesPerDay:    cfg.MaxTradesPerDay,
		MaxLossStreaks:     cfg.MaxLossStreaks,
		MaxHoldingTimeMin:  cfg.MaxHoldingTimeMin,
		MaxDailyLossAmount: cfg.MaxDailyLossAmount,
	}

	riskMgr := risk.NewRiskManager(db.WithContext(ctx), logger.Logger, cfg.InitialCapital, riskLimits)
	resilientExec := execution.NewResilientExecutor(logger.Logger)
	execMgr := execution.NewExecutionManager(db, logger.Logger, kiteClient, resilientExec, cfg.LiveTrading)
	statusTracker := execution.NewStatusTracker(execMgr, riskMgr, logger.Logger)
	rrCalculator := risk.InitializeRiskRewardCalculator(cfg.RiskRewardType)

	return riskMgr, rrCalculator, execMgr, statusTracker, resilientExec
}

// initStrategiesAndSelectors initializes active trading strategies and active selectors
func initStrategiesAndSelectors(cfg *config.Settings, logger *monitoring.Logger, securityMaster *data.SecurityMaster) ([]strategy.Strategy, map[string]selection.Selector, map[string]string, map[string]map[string]int64) {
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
	activeSelMap := selection.InitializeSelectors(selectorNames, cfg)

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
								subSel = selection.NewSectoralSelector(cfg)
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

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}
	nowIST := time.Now().In(loc)

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

	// Reconcile and recover any active MIS positions and stop-loss orders on startup
	tb.reconcilePositions()

	// Populate triggered trades from database to prevent duplicate trades after restart
	tb.restoreTriggeredTrades()

	// Connect to ticker
	if err := tb.ticker.Connect(tb.ctx, instrumentTokens); err != nil {
		return fmt.Errorf("failed to connect ticker: %w", err)
	}

	time.Sleep(2 * time.Second) // Wait for connection

	// Store PDH/PDL for Nifty 50 stocks if not present
	tb.initializeNifty50PDH_PDL(loc)

	// Handle Catch-Up logic if bot started after GlobalTradeStartTime
	tb.handleCatchUpSequence(loc, nowIST)

	// Start main loops
	tb.wg.Add(4)
	go tb.tickProcessingLoop()
	go tb.strategyLoop()
	go tb.orderManagementLoop()
	go tb.monitoringLoop()

	tb.wg.Add(1)
	go tb.runDailyStrategyScheduler(loc)

	// Drain 1-minute completed candles channel in background
	go func() {
		for {
			select {
			case <-tb.ctx.Done():
				return
			case _, ok := <-tb.candleAgg1m.GetCompletedCandles():
				if !ok {
					return
				}
			}
		}
	}()

	// Start interactive web dashboard server on port 8080
	go tb.startWebDashboard()

	// Wait for shutdown
	tb.waitForShutdown()

	return nil
}

// handleCatchUpSequence runs the catch-up sequence if the bot started late
func (tb *TradingBot) handleCatchUpSequence(loc *time.Location, nowIST time.Time) {
	// If started at or after 09:15 AM, trigger catch-up sequence
	startBoundary := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 9, 15, 0, 0, loc)
	if !nowIST.Before(startBoundary) {
		tb.logger.Info("Bot started late. Initiating catch-up sequence...", nil)
		if err := tb.logMarketBreadth(loc); err != nil {
			tb.logger.Error("Failed to calculate catch-up market breadth", map[string]interface{}{"error": err.Error()})
		}
		if err := tb.selectWatchlist(loc); err != nil {
			tb.logger.Error("Failed to resolve catch-up dynamic watchlist", map[string]interface{}{"error": err.Error()})
		} else {
			// Catch up on historical 5-minute candles since 09:15 AM
			tb.watchlistMutex.RLock()
			for sym, tok := range tb.watchlist {
				tb.catchUpHistoricalCandles(sym, tok)
			}
			tb.watchlistMutex.RUnlock()
		}
	}
}



// strategyLoop processes completed candles and forwards them to strategy engines
func (tb *TradingBot) strategyLoop() {
	defer tb.wg.Done()

	tb.logger.Info("Strategy loop started", nil)

	candlesChan := tb.candleAgg.GetCompletedCandles()

	for {
		select {
		case <-tb.ctx.Done():
			return

		case candle := <-candlesChan:
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

			if symbol == "" {
				continue
			}

			// Inform all active strategies of the completed candle close
			for _, strat := range tb.activeStrategies {
				strat.OnCandleClose(candle, symbol)
			}

			// Completed candle closed - active strategies already process this via OnCandleClose above.
		}
	}
}





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
				tb.logger.CriticalRisk("Circuit breaker active, shutting down", map[string]interface{}{})
				tb.running = false
				tb.cancel()
				break
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
	mux.HandleFunc("/", tb.handleRootRedirect)
	mux.HandleFunc("/api/watchlist", tb.handleWatchlist)
	mux.HandleFunc("/api/candles", tb.handleCandles)
	mux.HandleFunc("/api/trades", tb.handleTrades)
	mux.HandleFunc("/api/trades/all", tb.handleTradesAll)
	mux.HandleFunc("/api/bias", tb.handleDailyBias)
	mux.HandleFunc("/api/manual-watchlist", tb.handleDailyManualWatchlist)
	mux.HandleFunc("/api/pre-selections", tb.handlePreSelections)
	mux.HandleFunc("/api/config/access-token", tb.handleConfigAccessToken)

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
		_, _, _, err := tb.queryPreviousDayHighLow(token, loc)
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
	var h, m int
	_, err := fmt.Sscanf(timeStr, "%d:%d", &h, &m)
	if err != nil {
		return 0, 0, err
	}
	return h, m, nil
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
			tb.tickSizes[inst.Tradingsymbol] = inst.TickSize
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

	// 3. Add Nifty Index Token (99926009)
	tokensMap[99926009] = true

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
