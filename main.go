package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/execution"
	"zerodha-trading/monitoring"
	"zerodha-trading/risk"
	"zerodha-trading/strategy"
)

// TradingBot is the main orchestrator
type TradingBot struct {
	cfg            *config.Settings
	logger         *monitoring.Logger
	db             *data.Database
	ticker         *data.RobustKiteTicker
	candleAgg      *data.CandleAggregator
	securityMaster *data.SecurityMaster
	strategyEngine *strategy.StrategyEngine
	riskMgr        *risk.RiskManager
	execMgr        *execution.ExecutionManager
	statusTracker  *execution.StatusTracker
	resilientExec  *execution.ResilientExecutor
	running        bool
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
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
	ticker := data.NewRobustKiteTicker(cfg.AccessToken, logger.Logger)
	candleAgg := data.NewCandleAggregator(db.WithContext(ctx), logger.Logger, cfg.CandleIntervalSec, 100)
	securityMaster := data.NewSecurityMaster(db.WithContext(ctx), logger.Logger)

	indicators := strategy.NewIndicators(logger.Logger, cfg.VWAPWindow, cfg.ATRPeriod, cfg.OBIWindow)
	strategyEngine := strategy.NewStrategyEngine(indicators, logger.Logger, 50)

	riskLimits := risk.RiskLimits{
		MaxDailyLossPct:    cfg.MaxDailyLossPct,
		MaxLossAmount:      cfg.MaxLossAmount,
		MaxPositionSize:    cfg.MaxPositionSize,
		MaxTradesPerDay:    cfg.MaxTradesPerDay,
		MaxLossStreaks:     cfg.MaxLossStreaks,
		MaxQtyPerOrder:     cfg.MaxQtyPerOrder,
		MinProfitTargetPct: cfg.MinProfitTargetPct,
		MaxHoldingTimeMin:  cfg.MaxHoldingTimeMin,
	}

	riskMgr := risk.NewRiskManager(db.WithContext(ctx), logger.Logger, cfg.InitialCapital, riskLimits)
	execMgr := execution.NewExecutionManager(db.WithContext(ctx), logger.Logger)
	statusTracker := execution.NewStatusTracker(execMgr, logger.Logger)
	resilientExec := execution.NewResilientExecutor(logger.Logger)

	ctx, cancel := context.WithCancel(context.Background())

	logger.Info("Trading bot initialized successfully", nil)

	return &TradingBot{
		cfg:            cfg,
		logger:         logger,
		db:             db,
		ticker:         ticker,
		candleAgg:      candleAgg,
		securityMaster: securityMaster,
		strategyEngine: strategyEngine,
		riskMgr:        riskMgr,
		execMgr:        execMgr,
		statusTracker:  statusTracker,
		resilientExec:  resilientExec,
		running:        false,
		ctx:            ctx,
		cancel:         cancel,
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

	// Fetch watchlist
	watchlist, err := tb.securityMaster.GetNifty50Constituents(tb.ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch watchlist: %w", err)
	}

	instrumentTokens := make([]int64, 0, len(watchlist))
	for _, token := range watchlist {
		instrumentTokens = append(instrumentTokens, token)
	}

	// Connect to ticker
	if err := tb.ticker.Connect(tb.ctx, instrumentTokens); err != nil {
		return fmt.Errorf("failed to connect ticker: %w", err)
	}

	time.Sleep(2 * time.Second) // Wait for connection

	// Start main loops
	tb.wg.Add(4)
	go tb.tickProcessingLoop()
	go tb.strategyLoop()
	go tb.orderManagementLoop()
	go tb.monitoringLoop()

	// Wait for shutdown
	tb.waitForShutdown()

	return nil
}

// tickProcessingLoop continuously processes incoming ticks
func (tb *TradingBot) tickProcessingLoop() {
	defer tb.wg.Done()

	tb.logger.Info("Tick processing loop started", nil)

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-tb.ctx.Done():
			return
		case <-ticker.C:
			// Get latest ticks and process them
			// In real app, these come from WebSocket
		}
	}
}

// strategyLoop generates trading signals
func (tb *TradingBot) strategyLoop() {
	defer tb.wg.Done()

	tb.logger.Info("Strategy loop started", nil)

	candlesChan := tb.candleAgg.GetCompletedCandles()
	signalsChan := tb.strategyEngine.GetSignals()

	for {
		select {
		case <-tb.ctx.Done():
			return

		case candle := <-candlesChan:
			if candle == nil {
				continue
			}

			// Generate signal
			signal := tb.strategyEngine.OnCandleClose(candle)
			if signal == nil || signal.Action == "HOLD" {
				continue
			}

			// Risk checks
			if !tb.riskMgr.CanPlaceOrder(100, candle.Close) {
				tb.logger.InfoTrade("Order rejected by risk manager", map[string]interface{}{"signal": signal.Action})
				continue
			}

			// Place order
			orderReq := execution.OrderRequest{
				TradingSymbol:   signal.Symbol,
				Exchange:        "NSE",
				Quantity:        100,
				TransactionType: signal.Action,
				OrderType:       execution.OrderTypeMarket,
				Product:         "MIS",
				Validity:        "DAY",
			}

			orderID, err := tb.execMgr.PlaceOrder(orderReq)
			if err != nil {
				tb.logger.ErrorTrade("Failed to place order", err, map[string]interface{}{})
				continue
			}

			// Calculate SL based on ATR
			candles := tb.strategyEngine.GetRollingCandles(candle.Token)
			if len(candles) > 0 {
				indicators := strategy.NewIndicators(tb.logger.Logger, tb.cfg.VWAPWindow, tb.cfg.ATRPeriod, tb.cfg.OBIWindow)
				atrs := indicators.CalculateATR(candles)
				currentATR := atrs[len(atrs)-1]

				var slPrice float64
				if signal.Action == "BUY" {
					slPrice = candle.Close - (2.0 * currentATR)
				} else {
					slPrice = candle.Close + (2.0 * currentATR)
				}

				tb.riskMgr.AddOpenPosition(orderID, signal.Symbol, candle.Token, 100, candle.Close, signal.Action, slPrice)
			}

			// Start tracking
			tb.statusTracker.StartTracking(orderID)

		case signal := <-signalsChan:
			_ = signal // Process if needed
		}
	}
}

// orderManagementLoop monitors open positions
func (tb *TradingBot) orderManagementLoop() {
	defer tb.wg.Done()

	tb.logger.Info("Order management loop started", nil)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-tb.ctx.Done():
			return
		case <-ticker.C:
			positions := tb.riskMgr.GetOpenPositions()
			for orderID, pos := range positions {
				// Get latest price
				tick := tb.ticker.GetLatestTick(pos.Token)
				if tick == nil {
					continue
				}

				currentPrice := tick.LTP

				// Check SL
				action := tb.riskMgr.CheckTrailingSL(orderID, currentPrice)
				if action == "CLOSE" {
					tb.execMgr.CancelOrder(orderID)
					tb.riskMgr.OnOrderClose(orderID, currentPrice, pos.Quantity)
				}

				// Update position price
				tb.riskMgr.UpdatePositionPrice(orderID, currentPrice)
			}
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

			// Check margins every 5 minutes
			if now.Sub(lastMarginCheck) > 5*time.Minute {
				tb.resilientExec.HandleMarginChange(50000)
				lastMarginCheck = now
			}

			// Log P&L every 15 minutes
			if now.Sub(lastPnLLog) > 15*time.Minute {
				metrics := tb.riskMgr.GetMetrics()
				tb.logger.Info("P&L Update", map[string]interface{}{
					"daily_pnl":    metrics["daily_pnl"].(float64),
					"trades":       metrics["trades_today"].(int),
					"drawdown_pct": metrics["drawdown_pct"].(float64),
				})

				lastPnLLog = now
			}

			// Check circuit breaker
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

	// Check market hours
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

	// Close all open positions
	positions := tb.riskMgr.GetOpenPositions()
	for orderID, pos := range positions {
		tb.execMgr.CancelOrder(orderID)
		tb.riskMgr.OnOrderClose(orderID, pos.LatestPrice, pos.Quantity)
	}

	// Wait for loops to finish
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

	// Cleanup
	tb.ticker.Close()
	tb.db.Close()

	// Log final metrics
	metrics := tb.riskMgr.GetMetrics()
	tb.logger.Info("=== Bot Shutdown Complete ===", map[string]interface{}{
		"final_pnl":      metrics["daily_pnl"].(float64),
		"total_trades":   metrics["closed_trades"].(int),
	})

	tb.logger.Sync()
}

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Create bot
	bot, err := NewTradingBot(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create bot: %v\n", err)
		os.Exit(1)
	}

	// Run bot
	if err := bot.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Bot error: %v\n", err)
		os.Exit(1)
	}
}