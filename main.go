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
	cfg                 *config.Settings
	logger              *monitoring.Logger
	db                  *data.Database
	ticker              *data.RobustKiteTicker
	candleAgg           *data.CandleAggregator
	candleAgg1m         *data.CandleAggregator
	securityMaster      *data.SecurityMaster
	activeStrategies    []strategy.Strategy
	riskMgr             *risk.RiskManager
	rrCalculator        risk.RiskRewardCalculator
	execMgr             *execution.ExecutionManager
	statusTracker       *execution.StatusTracker
	resilientExec       *execution.ResilientExecutor
	kiteClient          *kiteconnect.Client
	globalBias          string
	watchlist           map[string]int64
	watchlistMutex      sync.RWMutex
	activeSelectors     map[string]selection.Selector
	strategySelectorMap map[string]string           // strategy name -> selector name
	strategyWatchlists  map[string]map[string]int64 // strategy name -> symbol -> token
	running             bool
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
}

// NewTradingBot creates a new bot instance
func NewTradingBot(cfg *config.Settings) (*TradingBot, error) {
	// Create logger
	logger, err := monitoring.NewLogger(cfg.LogLevel)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	// Create database
	db, err := data.NewDatabase(
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		logger.Logger,
	)
	if err != nil {
		logger.Error("Database connection failed", map[string]interface{}{"error": err.Error()})
		return nil, err
	}

	// Initialize schema
	if err := db.InitSchema(); err != nil {
		logger.Error("Schema initialization failed", map[string]interface{}{"error": err.Error()})
		return nil, err
	}

	ctx := context.Background()

	// Create components
	ticker := data.NewRobustKiteTicker(cfg.APIKey, cfg.AccessToken, logger.Logger)
	candleAgg := data.NewCandleAggregator(db.WithContext(ctx), logger.Logger, cfg.CandleIntervalSec, 100, "candles_5m")
	candleAgg1m := data.NewCandleAggregator(db.WithContext(ctx), logger.Logger, 60, 100, "candles_1m")

	// Initialize Kite Connect API Client
	kiteClient := kiteconnect.New(cfg.APIKey)
	kiteClient.SetAccessToken(cfg.AccessToken)

	securityMaster := data.NewSecurityMaster(db.WithContext(ctx), kiteClient, logger.Logger)

	var activeNames []string
	if cfg.ActiveStrategies != "" {
		activeNames = strings.Split(cfg.ActiveStrategies, ",")
		for i := range activeNames {
			activeNames[i] = strings.TrimSpace(activeNames[i])
		}
	}
	activeStrategies := strategy.InitializeActiveStrategies(activeNames, logger.Logger, cfg)

	riskLimits := risk.RiskLimits{
		MaxTradesPerDay:    cfg.MaxTradesPerDay,
		MaxLossStreaks:     cfg.MaxLossStreaks,
		MaxHoldingTimeMin:  cfg.MaxHoldingTimeMin,
		MaxDailyLossAmount: cfg.MaxDailyLossAmount,
	}

	riskMgr := risk.NewRiskManager(db.WithContext(ctx), logger.Logger, cfg.InitialCapital, riskLimits)
	resilientExec := execution.NewResilientExecutor(logger.Logger)
	execMgr := execution.NewExecutionManager(db.WithContext(ctx), logger.Logger, kiteClient, resilientExec, cfg.LiveTrading)
	statusTracker := execution.NewStatusTracker(execMgr, riskMgr, logger.Logger)

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
				stratSelMap[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}

	stratWatchlists := make(map[string]map[string]int64)
	for _, strat := range activeStrategies {
		stratWatchlists[strat.Name()] = make(map[string]int64)
	}

	rrCalculator := risk.InitializeRiskRewardCalculator(cfg.RiskRewardType)

	ctx, cancel := context.WithCancel(context.Background())

	logger.Info("Trading bot initialized successfully", nil)

	return &TradingBot{
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
		running:             false,
		ctx:                 ctx,
		cancel:              cancel,
	}, nil
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

	var niftyWatchlist map[string]int64
	niftyWatchlist, err = tb.securityMaster.GetNifty50Constituents(tb.ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch Nifty 50 watchlist: %w", err)
	}

	tb.watchlistMutex.Lock()
	tb.watchlist = niftyWatchlist
	tb.watchlistMutex.Unlock()

	// Connect to ticker
	instrumentTokens := make([]int64, 0, len(niftyWatchlist))
	for _, token := range niftyWatchlist {
		instrumentTokens = append(instrumentTokens, token)
	}

	// Reconcile and Square off any orphan MIS positions on startup
	tb.reconcilePositions()

	// Connect to ticker
	if err := tb.ticker.Connect(tb.ctx, instrumentTokens); err != nil {
		return fmt.Errorf("failed to connect ticker: %w", err)
	}

	time.Sleep(2 * time.Second) // Wait for connection

	// Handle Catch-Up logic if bot started after TradeStartTime in LOW_VOLUME mode
	startHour, startMin, err := parseTimeHM(tb.cfg.TradeStartTime)
	if err != nil {
		startHour, startMin = 9, 30
	}
	startBoundary := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), startHour, startMin, 0, 0, loc)
	if tb.cfg.StrategyType == "LOW_VOLUME" && !nowIST.Before(startBoundary) {
		tb.logger.Info("[LOW_VOLUME] Bot started late. Initiating catch-up sequence...", nil)
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

	// Start main loops
	tb.wg.Add(4)
	go tb.tickProcessingLoop()
	go tb.strategyLoop()
	go tb.orderManagementLoop()
	go tb.monitoringLoop()

	if tb.cfg.StrategyType == "LOW_VOLUME" {
		tb.wg.Add(1)
		go tb.runLOWVOLUMEStrategyScheduler(loc)
	}

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
				OrderType:       execution.OrderTypeMarket,
				Product:         "MIS",
				Validity:        "DAY",
			}

			_, err := tb.execMgr.PlaceOrder(orderReq)
			if err != nil {
				tb.logger.Error("Failed to square off position on shutdown", map[string]interface{}{"error": err.Error(), "symbol": pos.Symbol})
			} else {
				tb.logger.Info("Squared off live position on shutdown", map[string]interface{}{"symbol": pos.Symbol, "qty": pos.Quantity})
			}
		} else {
			tb.execMgr.CancelOrder(orderID)
		}
		tb.riskMgr.OnOrderClose(orderID, pos.LatestPrice, pos.Quantity)
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





func parseTimeHM(timeStr string) (int, int, error) {
	var h, m int
	_, err := fmt.Sscanf(timeStr, "%d:%d", &h, &m)
	if err != nil {
		return 0, 0, err
	}
	return h, m, nil
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
