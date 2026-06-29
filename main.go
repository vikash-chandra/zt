package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/execution"
	"zerodha-trading/monitoring"
	"zerodha-trading/risk"
	"zerodha-trading/strategy"
)

// TradingBot is the main orchestrator
type TradingBot struct {
	cfg             *config.Settings
	logger          *monitoring.Logger
	db              *data.Database
	ticker          *data.RobustKiteTicker
	candleAgg       *data.CandleAggregator
	candleAgg1m     *data.CandleAggregator
	securityMaster  *data.SecurityMaster
	strategyEngine  *strategy.StrategyEngine
	lowVolumeEngine *strategy.LowVolumeEngine
	riskMgr         *risk.RiskManager
	execMgr         *execution.ExecutionManager
	statusTracker   *execution.StatusTracker
	resilientExec   *execution.ResilientExecutor
	kiteClient      *kiteconnect.Client
	globalBias      string
	watchlist       map[string]int64
	watchlistMutex  sync.RWMutex
	running         bool
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
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

	indicators := strategy.NewIndicators(logger.Logger, cfg.VWAPWindow, cfg.ATRPeriod, cfg.OBIWindow)
	strategyEngine := strategy.NewStrategyEngine(indicators, logger.Logger, 50)
	lowVolumeEngine := strategy.NewLowVolumeEngine(logger.Logger)

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
		cfg:             cfg,
		logger:          logger,
		db:              db,
		ticker:          ticker,
		candleAgg:       candleAgg,
		candleAgg1m:     candleAgg1m,
		securityMaster:  securityMaster,
		strategyEngine:  strategyEngine,
		lowVolumeEngine: lowVolumeEngine,
		riskMgr:         riskMgr,
		execMgr:         execMgr,
		statusTracker:   statusTracker,
		resilientExec:   resilientExec,
		kiteClient:      kiteClient,
		running:         false,
		ctx:             ctx,
		cancel:          cancel,
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
	if tb.cfg.StrategyType == "LOW_VOLUME" && !nowIST.Before(startBoundary) && nowIST.Hour() < 15 {
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

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}

	for {
		select {
		case <-tb.ctx.Done():
			return
		case <-ticker.C:
			// Copy watchlist locally to avoid race conditions
			tb.watchlistMutex.RLock()
			currentWatchlist := make(map[string]int64)
			for symbol, token := range tb.watchlist {
				currentWatchlist[symbol] = token
			}
			tb.watchlistMutex.RUnlock()

			for symbol, token := range currentWatchlist {
				tick := tb.ticker.GetLatestTick(token)
				if tick != nil {
					tb.candleAgg1m.ProcessTick(tick)
					tb.candleAgg.ProcessTick(tick)

					// If LOW_VOLUME breakout strategy is active and inside trading window (09:30:01 - 10:45:00)
					if tb.cfg.StrategyType == "LOW_VOLUME" && tb.globalBias != "NO_TRADE" && tb.globalBias != "" {
						nowIST := time.Now().In(loc)
						startHour, startMin, err := parseTimeHM(tb.cfg.TradeStartTime)
						if err != nil {
							startHour, startMin = 9, 30
						}
						endHour, endMin, err := parseTimeHM(tb.cfg.TradeEndTime)
						if err != nil {
							endHour, endMin = 10, 45
						}

						startBoundary := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), startHour, startMin, 0, 0, loc)
						endBoundary := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), endHour, endMin, 0, 0, loc)

						inTradingWindow := !nowIST.Before(startBoundary) && !nowIST.After(endBoundary)
						if inTradingWindow {
							signal := tb.lowVolumeEngine.CheckBreakout(symbol, tick.LTP, tb.globalBias)
							if signal != nil {
								tb.logger.InfoTrade("LOW_VOLUME breakout signal triggered", map[string]interface{}{
									"symbol": symbol,
									"action": signal.Action,
									"ltp":    tick.LTP,
									"reason": signal.Reason,
								})

								// Calculate dynamic quantity using live leverage from Zerodha Kite API
								tradeQty, _ := tb.calculateQuantityWithLiveLeverage(symbol, tick.LTP)
								if tradeQty <= 0 {
									tb.logger.Warn("Calculated quantity is zero. Skipping breakout trade entry.", map[string]interface{}{
										"symbol":      symbol,
										"ltp":         tick.LTP,
										"max_capital": tb.cfg.MaxCapitalPerTrade,
									})
									continue
								}

								if tb.riskMgr.CanPlaceOrder(tradeQty, tick.LTP) {
									var slPrice float64
									setup := tb.lowVolumeEngine.GetSetupCandle(symbol)
									if setup != nil {
										originalRisk := math.Abs(tick.LTP - setup.Low)
										if signal.Action == "SELL" {
											originalRisk = math.Abs(setup.High - tick.LTP)
										}
										multiplier := 1.0 + (tb.cfg.SLBufferPct / 100.0)
										bufferedRisk := multiplier * originalRisk

										if signal.Action == "BUY" {
											slPrice = tick.LTP - bufferedRisk // setup.Low - 20% of risk
										} else {
											slPrice = tick.LTP + bufferedRisk // setup.High + 20% of risk
										}
									} else {
										slPrice = tick.LTP * 0.99 // Fallback
									}

									orderReq := execution.OrderRequest{
										TradingSymbol:   symbol,
										Exchange:        "NSE",
										Quantity:        tradeQty,
										TransactionType: signal.Action,
										OrderType:       execution.OrderTypeMarket,
										Product:         "MIS",
										Validity:        "DAY",
									}

									orderID, err := tb.execMgr.PlaceOrder(orderReq)
									if err != nil {
										tb.logger.Error("Failed to place breakout order", map[string]interface{}{"error": err.Error(), "symbol": symbol})
									} else {
										tb.riskMgr.AddOpenPosition(orderID, symbol, token, tradeQty, tick.LTP, signal.Action, slPrice)
										tb.execMgr.SimulateOrderFill(orderID, tradeQty, tick.LTP)
										tb.statusTracker.StartTracking(orderID)
									}
								}
							}
						}
					}
				}
			}
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

			if tb.cfg.StrategyType == "LOW_VOLUME" {
				// LOW_VOLUME strategy processes completed candles to update low-volume setup baselines
				tb.lowVolumeEngine.OnCandleClose(candle, symbol)
			} else {
				// Default VWAP_RSI strategy logic
				signal := tb.strategyEngine.OnCandleClose(candle)
				if signal == nil || signal.Action == "HOLD" {
					continue
				}

				if tb.riskMgr.CanPlaceOrder(100, candle.Close) {
					indicators := strategy.NewIndicators(tb.logger.Logger, tb.cfg.VWAPWindow, tb.cfg.ATRPeriod, tb.cfg.OBIWindow)
					rolling := tb.strategyEngine.GetRollingCandles(candle.Token)
					atrs := indicators.CalculateATR(rolling)
					currentATR := atrs[len(atrs)-1]

					var slPrice float64
					if signal.Action == "BUY" {
						slPrice = candle.Close - (2.0 * currentATR)
					} else {
						slPrice = candle.Close + (2.0 * currentATR)
					}

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
						tb.logger.Error("Failed to place order", map[string]interface{}{"error": err.Error(), "symbol": signal.Symbol})
					} else {
						tb.riskMgr.AddOpenPosition(orderID, signal.Symbol, candle.Token, 100, candle.Close, signal.Action, slPrice)
						tb.execMgr.SimulateOrderFill(orderID, 100, candle.Close)
						tb.statusTracker.StartTracking(orderID)
					}
				}
			}
		}
	}
}

// orderManagementLoop monitors open positions and processes risk exits / partial exits
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
				tick := tb.ticker.GetLatestTick(pos.Token)
				if tick == nil {
					continue
				}

				currentPrice := tick.LTP

				// Check risk limits (Stop-Loss and Target 1 partial exits)
				action := tb.riskMgr.CheckTrailingSL(orderID, currentPrice)
				if action == "CLOSE" {
					tb.execMgr.CancelOrder(orderID)
					tb.riskMgr.OnOrderClose(orderID, currentPrice, pos.Quantity)
				} else if action == "PARTIAL_EXIT" {
					// Perform Target 1 (1:2 R:R) partial exit of 50%
					var txnType string
					if pos.Side == "BUY" {
						txnType = "SELL"
					} else {
						txnType = "BUY"
					}

					closeQty := pos.Quantity / 2
					if closeQty > 0 {
						orderReq := execution.OrderRequest{
							TradingSymbol:   pos.Symbol,
							Exchange:        "NSE",
							Quantity:        closeQty,
							TransactionType: txnType,
							OrderType:       execution.OrderTypeMarket,
							Product:         "MIS",
							Validity:        "DAY",
						}

						exitOrderID, err := tb.execMgr.PlaceOrder(orderReq)
						if err != nil {
							tb.logger.Error("Failed to place partial exit order", map[string]interface{}{"error": err.Error(), "symbol": pos.Symbol})
						} else {
							tb.logger.Info("Target 1 partial exit order placed", map[string]interface{}{
								"order_id": exitOrderID,
								"symbol":   pos.Symbol,
								"qty":      closeQty,
							})
							tb.execMgr.SimulateOrderFill(exitOrderID, closeQty, currentPrice)
							tb.riskMgr.RecordPartialExit(orderID, currentPrice, closeQty)
						}
					}
				}

				// Update current price
				tb.riskMgr.UpdatePositionPrice(orderID, currentPrice)
			}
		}
	}
}

// runLOWVOLUMEStrategyScheduler schedules strategy actions for the day
func (tb *TradingBot) runLOWVOLUMEStrategyScheduler(loc *time.Location) {
	defer tb.wg.Done()

	tb.logger.Info("[LOW_VOLUME] Strategy scheduler loop started", nil)

	selectHour, selectMin, err := parseTimeHM(tb.cfg.StockSelectTime)
	if err != nil {
		selectHour, selectMin = 9, 30
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	breadthLogged := false
	watchlistFiltered := false
	hardSquareOffDone := false

	for {
		select {
		case <-tb.ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().In(loc)
			hour := now.Hour()
			minute := now.Minute()
			second := now.Second()

			selectBoundary := time.Date(now.Year(), now.Month(), now.Day(), selectHour, selectMin, 0, 0, loc)
			breadthBoundary := selectBoundary.Add(-1 * time.Minute)

			// 1. Step 1: Pre-market breadth logging (1 minute before stock selection time)
			if !breadthLogged && !now.Before(breadthBoundary) && now.Hour() < 15 {
				tb.logger.Info(fmt.Sprintf("[LOW_VOLUME] Triggering %02d:%02d:00 pre-market breadth calculations...", breadthBoundary.Hour(), breadthBoundary.Minute()), nil)
				if err := tb.logMarketBreadth(loc); err != nil {
					tb.logger.Error("Failed to run pre-market breadth check", map[string]interface{}{"error": err.Error()})
				} else {
					breadthLogged = true
				}
			}

			// 2. Step 2: Dynamic Stock Selection Filter (exactly at stock selection time)
			if !watchlistFiltered && breadthLogged && !now.Before(selectBoundary) && now.Hour() < 15 {
				tb.logger.Info(fmt.Sprintf("[LOW_VOLUME] Triggering %02d:%02d:00 dynamic watchlist filter...", selectHour, selectMin), nil)
				if err := tb.selectWatchlist(loc); err != nil {
					tb.logger.Error("Failed to resolve dynamic watchlist selection", map[string]interface{}{"error": err.Error()})
				} else {
					watchlistFiltered = true
				}
			}

			// 3. Step 7: Hard Square-off Override (03:15:00 PM)
			if !hardSquareOffDone && ((hour == 15 && minute >= 15) || hour > 15) {
				tb.logger.Info("[LOW_VOLUME] Triggering 03:15:00 PM hard square-off override...", nil)
				tb.hardSquareOff()
				hardSquareOffDone = true
			}

			// Reset daily state at midnight
			if hour == 0 && minute == 0 && second == 0 {
				breadthLogged = false
				watchlistFiltered = false
				hardSquareOffDone = false
				tb.lowVolumeEngine.Reset()
				tb.globalBias = ""
				
				// Reset watchlist back to Nifty 50 constituents for pre-market calculation
				niftyWatchlist, err := tb.securityMaster.GetNifty50Constituents(tb.ctx)
				if err == nil {
					tb.watchlistMutex.Lock()
					tb.watchlist = niftyWatchlist
					tb.watchlistMutex.Unlock()
				}
			}
		}
	}
}

// logMarketBreadth performs the pre-market Advance-Decline breadth calculation
func (tb *TradingBot) logMarketBreadth(loc *time.Location) error {
	nifty50Map, err := tb.securityMaster.GetNifty50Constituents(tb.ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch Nifty 50 constituents: %w", err)
	}

	var keys []string
	for symbol := range nifty50Map {
		keys = append(keys, "NSE:"+symbol)
	}

	tb.logger.Info("[LOW_VOLUME] Fetching Nifty 50 OHLC snapshot...", map[string]interface{}{"stocks": len(keys)})
	ohlcData, err := tb.kiteClient.GetOHLC(keys...)
	if err != nil {
		return fmt.Errorf("failed to fetch Nifty 50 OHLC snapshot from Zerodha: %w", err)
	}

	advances := 0
	declines := 0
	neutrals := 0

	type Detail struct {
		Symbol    string  `json:"symbol"`
		Open      float64 `json:"open"`
		LTP       float64 `json:"ltp"`
		PctChange float64 `json:"pct_change"`
		Category  string  `json:"category"`
	}
	var details []Detail

	for key, entry := range ohlcData {
		open := entry.OHLC.Open
		ltp := entry.LastPrice
		symbol := key[4:] // remove "NSE:"

		if open == 0 {
			continue
		}

		pctChange := ((ltp - open) / open) * 100.0
		category := "NEUTRAL"
		if pctChange > 0.0 {
			category = "ADVANCE"
			advances++
		} else if pctChange < 0.0 {
			category = "DECLINE"
			declines++
		} else {
			neutrals++
		}

		details = append(details, Detail{
			Symbol:    symbol,
			Open:      open,
			LTP:       ltp,
			PctChange: pctChange,
			Category:  category,
		})
	}

	tb.globalBias = "SELL_ONLY"
	if advances > declines {
		tb.globalBias = "BUY_ONLY"
	}

	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("failed to marshal details JSON: %w", err)
	}

	_, err = tb.db.WithContext(tb.ctx).ExecContext(tb.ctx, `
		INSERT INTO market_breadth_logs (time, advances, declines, neutrals, global_bias, details)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, time.Now().In(loc), advances, declines, neutrals, tb.globalBias, string(detailsJSON))
	if err != nil {
		tb.logger.Error("Failed to save market breadth logs to database", map[string]interface{}{"error": err.Error()})
	}

	tb.logger.Info("[LOW_VOLUME] Daily global bias established", map[string]interface{}{
		"advances":    advances,
		"declines":    declines,
		"neutrals":    neutrals,
		"global_bias": tb.globalBias,
	})

	return nil
}

// selectWatchlist filters the active NSE F&O Stocks list into the Top 10 Gainers or Losers
func (tb *TradingBot) selectWatchlist(loc *time.Location) error {
	if tb.globalBias == "NO_TRADE" || tb.globalBias == "" {
		tb.logger.Info("[LOW_VOLUME] Global bias is NO_TRADE or empty. Skipping watchlist dynamic selection.", map[string]interface{}{"bias": tb.globalBias})
		return nil
	}

	foStocksMap, err := tb.securityMaster.GetFOStocks(tb.ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch active F&O stocks list: %w", err)
	}

	var keys []string
	for symbol := range foStocksMap {
		keys = append(keys, "NSE:"+symbol)
	}

	tb.logger.Info("[LOW_VOLUME] Fetching OHLC snapshot for all F&O stocks...", map[string]interface{}{"count": len(keys)})

	ohlcData := make(kiteconnect.QuoteOHLC)
	batchSize := 400
	for i := 0; i < len(keys); i += batchSize {
		end := i + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		batchKeys := keys[i:end]
		batchData, err := tb.kiteClient.GetOHLC(batchKeys...)
		if err != nil {
			return fmt.Errorf("failed to fetch batch OHLC for F&O stocks: %w", err)
		}
		for k, v := range batchData {
			ohlcData[k] = v
		}
	}

	type StockPerf struct {
		Symbol    string
		Token     int64
		PctChange float64
	}
	var performances []StockPerf

	for key, entry := range ohlcData {
		open := entry.OHLC.Open
		ltp := entry.LastPrice
		symbol := key[4:] // remove "NSE:"

		if open == 0 {
			continue
		}

		pctChange := ((ltp - open) / open) * 100.0
		if math.Abs(pctChange) > tb.cfg.WatchlistMaxPctChange {
			continue
		}
		token := foStocksMap[symbol]

		performances = append(performances, StockPerf{
			Symbol:    symbol,
			Token:     token,
			PctChange: pctChange,
		})
	}

	if tb.globalBias == "BUY_ONLY" {
		sort.Slice(performances, func(i, j int) bool {
			return performances[i].PctChange > performances[j].PctChange
		})
	} else if tb.globalBias == "SELL_ONLY" {
		sort.Slice(performances, func(i, j int) bool {
			return performances[i].PctChange < performances[j].PctChange
		})
	}

	topCount := tb.cfg.WatchlistSize
	if len(performances) < topCount {
		topCount = len(performances)
	}

	selectedWatchlist := make(map[string]int64)
	var selectedTokens []int64
	for i := 0; i < topCount; i++ {
		selectedWatchlist[performances[i].Symbol] = performances[i].Token
		selectedTokens = append(selectedTokens, performances[i].Token)
		tb.logger.Info("[LOW_VOLUME] Watchlist Stock Selected", map[string]interface{}{
			"rank":       i + 1,
			"symbol":     performances[i].Symbol,
			"pct_change": performances[i].PctChange,
			"token":      performances[i].Token,
		})
	}

	tb.watchlistMutex.Lock()
	tb.watchlist = selectedWatchlist
	tb.watchlistMutex.Unlock()

	tb.logger.Info("[LOW_VOLUME] Watchlist selection complete. Swapping WebSocket ticker subscriptions...", map[string]interface{}{"count": len(selectedTokens)})

	tb.lowVolumeEngine.Reset()

	_ = tb.ticker.Close()
	time.Sleep(1 * time.Second)
	if err := tb.ticker.Connect(tb.ctx, selectedTokens); err != nil {
		return fmt.Errorf("failed to reconnect ticker to new F&O watchlist: %w", err)
	}

	return nil
}

// catchUpHistoricalCandles retrieves historical 5m candles since 09:15 AM
func (tb *TradingBot) catchUpHistoricalCandles(symbol string, token int64) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}
	nowIST := time.Now().In(loc)
	today0915 := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 9, 15, 0, 0, loc).UTC()

	now := time.Now().UTC()
	if now.Before(today0915) {
		return
	}

	tb.logger.Info("[LOW_VOLUME] Catching up historical 5-minute candles...", map[string]interface{}{
		"symbol": symbol,
		"from":   today0915,
	})

	candles, err := tb.kiteClient.GetHistoricalData(int(token), "5minute", today0915, now, false, false)
	if err != nil {
		tb.logger.Error("Failed to fetch historical candles for catch-up", map[string]interface{}{"error": err.Error(), "symbol": symbol})
		return
	}

	for _, c := range candles {
		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}

		candle := &data.Candle{
			Token:     token,
			Time:      c.Date.Time,
			Open:      c.Open,
			High:      c.High,
			Low:       c.Low,
			Close:     c.Close,
			Volume:    int64(c.Volume),
			VWAP:      (c.Open + c.High + c.Low + c.Close) / 4.0,
			Bid:       c.Low,
			Ask:       c.High,
			TickCount: int(c.Volume / 10),
			Color:     color,
		}
		tb.lowVolumeEngine.OnCandleClose(candle, symbol)
	}
}

// hardSquareOff closes all active positions and cancels pending orders
func (tb *TradingBot) hardSquareOff() {
	tb.logger.Warn("[LOW_VOLUME] Executing 03:15:00 PM hard square-off override...", nil)

	positions := tb.riskMgr.GetOpenPositions()
	for orderID, pos := range positions {
		tb.execMgr.CancelOrder(orderID)

		tick := tb.ticker.GetLatestTick(pos.Token)
		var exitPrice float64
		if tick != nil {
			exitPrice = tick.LTP
		} else {
			exitPrice = pos.LatestPrice
		}

		tb.riskMgr.OnOrderClose(orderID, exitPrice, pos.Quantity)
	}

	tb.logger.Info("[LOW_VOLUME] Hard square-off complete. Exposure is zero.", nil)
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
		tb.execMgr.CancelOrder(orderID)
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

func (tb *TradingBot) calculateQuantityWithLiveLeverage(symbol string, price float64) (int, error) {
	// If no Kite client is available (e.g. backtesting or offline mode), fallback to 5x leverage
	if tb.kiteClient == nil {
		marginPerShare := price / 5.0
		return int(math.Floor(tb.cfg.MaxCapitalPerTrade / marginPerShare)), nil
	}

	// Request margin calculation for 1 share
	params := kiteconnect.GetMarginParams{
		OrderParams: []kiteconnect.OrderMarginParam{
			{
				Exchange:        "NSE",
				Tradingsymbol:   symbol,
				TransactionType: "BUY",
				Variety:         "regular",
				Product:         "MIS",
				OrderType:       "MARKET",
				Quantity:        1,
				Price:           price,
			},
		},
	}

	margins, err := tb.kiteClient.GetOrderMargins(params)
	if err != nil {
		tb.logger.Warn("Failed to fetch live margin from Zerodha, falling back to 5x leverage", map[string]interface{}{"error": err.Error(), "symbol": symbol})
		marginPerShare := price / 5.0
		return int(math.Floor(tb.cfg.MaxCapitalPerTrade / marginPerShare)), nil
	}

	if len(margins) == 0 {
		marginPerShare := price / 5.0
		return int(math.Floor(tb.cfg.MaxCapitalPerTrade / marginPerShare)), nil
	}

	marginPerShare := margins[0].Total
	if marginPerShare <= 0 {
		marginPerShare = price / 5.0
	}

	qty := int(math.Floor(tb.cfg.MaxCapitalPerTrade / marginPerShare))
	return qty, nil
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