package main

import (
	"context"
	_ "embed"
	"encoding/json"
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
	strategyEngine      *strategy.StrategyEngine
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

	indicators := strategy.NewIndicators(logger.Logger, cfg.VWAPWindow, cfg.ATRPeriod, cfg.OBIWindow)
	strategyEngine := strategy.NewStrategyEngine(indicators, logger.Logger, 50)

	var activeNames []string
	if cfg.ActiveStrategies != "" {
		activeNames = strings.Split(cfg.ActiveStrategies, ",")
		for i := range activeNames {
			activeNames[i] = strings.TrimSpace(activeNames[i])
		}
	}
	activeStrategies := strategy.InitializeActiveStrategies(activeNames, logger.Logger, cfg)

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
		strategyEngine:      strategyEngine,
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

	// Start interactive web dashboard server on port 8080
	go tb.startWebDashboard()

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

						for _, strat := range tb.activeStrategies {
							var startH, startM, endH, endM int
							var errTime error
							if strat.Name() == "VANDE_BHARAT" {
								startH, startM, errTime = parseTimeHM(tb.cfg.VBTradeStartTime)
								if errTime != nil {
									startH, startM = 9, 26
								}
								endH, endM, errTime = parseTimeHM(tb.cfg.VBTradeEndTime)
								if errTime != nil {
									endH, endM = 11, 0
								}
							} else {
								startH, startM, errTime = parseTimeHM(tb.cfg.TradeStartTime)
								if errTime != nil {
									startH, startM = 9, 30
								}
								endH, endM, errTime = parseTimeHM(tb.cfg.TradeEndTime)
								if errTime != nil {
									endH, endM = 10, 45
								}
							}

							startBoundary := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), startH, startM, 0, 0, loc)
							endBoundary := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), endH, endM, 0, 0, loc)

							if nowIST.Before(startBoundary) || nowIST.After(endBoundary) {
								continue
							}

							tb.watchlistMutex.RLock()
							wList := tb.strategyWatchlists[strat.Name()]
							var inWatchlist bool
							if len(wList) > 0 {
								_, inWatchlist = wList[symbol]
							} else {
								_, inWatchlist = tb.watchlist[symbol]
							}
							tb.watchlistMutex.RUnlock()

							if !inWatchlist {
								continue
							}

							signal := strat.CheckBreakout(symbol, tick.LTP, tb.globalBias)
							if signal != nil {
								if tb.riskMgr.HasOpenPosition(symbol) {
									tb.logger.Info("Position already open for symbol, skipping breakout trigger", map[string]interface{}{
										"symbol":   symbol,
										"strategy": strat.Name(),
									})
									continue
								}

								tb.logger.InfoTrade(fmt.Sprintf("%s breakout signal triggered", strat.Name()), map[string]interface{}{
									"symbol": symbol,
									"action": signal.Action,
									"ltp":    tick.LTP,
									"reason": signal.Reason,
								})

								// Query margin per share for sizing
								var marginPerShare float64
								margins, err := tb.kiteClient.GetOrderMargins(kiteconnect.GetMarginParams{
									OrderParams: []kiteconnect.OrderMarginParam{
										{
											Exchange:        "NSE",
											Tradingsymbol:   symbol,
											TransactionType: signal.Action,
											Variety:         "regular",
											Product:         "MIS",
											OrderType:       "MARKET",
											Quantity:        1,
											Price:           tick.LTP,
										},
									},
								})
								if err == nil && len(margins) > 0 {
									marginPerShare = margins[0].Total
								}

								var setupHigh, setupLow float64
								setup := strat.GetSetupCandle(symbol)
								if setup != nil {
									setupHigh = setup.High
									setupLow = setup.Low
								}

								profile := tb.rrCalculator.CalculateProfile(tick.LTP, signal.Action, setupHigh, setupLow, tb.cfg.SLBufferPct, tb.cfg.MaxCapitalPerTrade, marginPerShare, tb.cfg.RiskRewardRatio)

								if profile.Quantity <= 0 {
									tb.logger.Warn("Calculated quantity is zero. Skipping breakout trade entry.", map[string]interface{}{
										"symbol":      symbol,
										"ltp":         tick.LTP,
										"max_capital": tb.cfg.MaxCapitalPerTrade,
									})
									continue
								}

								if tb.riskMgr.CanPlaceOrder(profile.Quantity, tick.LTP) {
									orderReq := execution.OrderRequest{
										TradingSymbol:   symbol,
										Exchange:        "NSE",
										Quantity:        profile.Quantity,
										TransactionType: signal.Action,
										OrderType:       execution.OrderTypeMarket,
										Product:         "MIS",
										Validity:        "DAY",
										Strategy:        strat.Name(),
									}

									orderID, err := tb.execMgr.PlaceOrder(orderReq)
									if err != nil {
										tb.logger.Error("Failed to place breakout order", map[string]interface{}{"error": err.Error(), "symbol": symbol})
									} else {
										tb.riskMgr.AddOpenPosition(orderID, symbol, token, profile.Quantity, tick.LTP, signal.Action, profile.StopLoss, strat.Name(), profile.Target1)
										tb.execMgr.SimulateOrderFill(orderID, profile.Quantity, tick.LTP)
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

			// Inform all active strategies of the completed candle close
			for _, strat := range tb.activeStrategies {
				strat.OnCandleClose(candle, symbol)
			}

			if tb.cfg.StrategyType == "VWAP_RSI" {
				// Default legacy VWAP_RSI strategy logic
				signal := tb.strategyEngine.OnCandleClose(candle)
				if signal == nil || signal.Action == "HOLD" {
					continue
				}

				if tb.riskMgr.CanPlaceOrder(100, candle.Close) {
					indicators := strategy.NewIndicators(tb.logger.Logger, tb.cfg.VWAPWindow, tb.cfg.ATRPeriod, tb.cfg.OBIWindow)
					rolling := tb.strategyEngine.GetRollingCandles(candle.Token)
					atrs := indicators.CalculateATR(rolling)
					currentATR := atrs[len(atrs)-1]

					var slPrice, target1Price float64
					riskAmt := 2.0 * currentATR
					if signal.Action == "BUY" {
						slPrice = candle.Close - riskAmt
						target1Price = candle.Close + (tb.cfg.RiskRewardRatio * riskAmt)
					} else {
						slPrice = candle.Close + riskAmt
						target1Price = candle.Close - (tb.cfg.RiskRewardRatio * riskAmt)
					}

					orderReq := execution.OrderRequest{
						TradingSymbol:   signal.Symbol,
						Exchange:        "NSE",
						Quantity:        100,
						TransactionType: signal.Action,
						OrderType:       execution.OrderTypeMarket,
						Product:         "MIS",
						Validity:        "DAY",
						Strategy:        "VWAP_RSI",
					}

					orderID, err := tb.execMgr.PlaceOrder(orderReq)
					if err != nil {
						tb.logger.Error("Failed to place order", map[string]interface{}{"error": err.Error(), "symbol": signal.Symbol})
					} else {
						tb.riskMgr.AddOpenPosition(orderID, signal.Symbol, candle.Token, 100, candle.Close, signal.Action, slPrice, "VWAP_RSI", target1Price)
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
				for _, strat := range tb.activeStrategies {
					strat.Reset()
				}
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
// selectWatchlist filters and aggregates the watchlist for all active strategies using their mapped selectors
func (tb *TradingBot) selectWatchlist(loc *time.Location) error {
	if tb.globalBias == "NO_TRADE" || tb.globalBias == "" {
		tb.logger.Info("Global bias is NO_TRADE or empty. Skipping watchlist dynamic selection.", map[string]interface{}{"bias": tb.globalBias})
		return nil
	}

	tb.watchlistMutex.Lock()
	tb.watchlist = make(map[string]int64)
	var selectedTokens []int64
	tokenSet := make(map[int64]bool)

	for _, strat := range tb.activeStrategies {
		// Look up mapped selector name, default to SECURITIES_FO if not set
		selectorName, exists := tb.strategySelectorMap[strat.Name()]
		if !exists || selectorName == "" {
			selectorName = "SECURITIES_FO"
		}

		selector, active := tb.activeSelectors[selectorName]
		if !active {
			tb.logger.Warn("Selector is not active or not initialized, defaulting to Securities F&O", map[string]interface{}{
				"strategy": strat.Name(),
				"selector": selectorName,
			})
			selector = tb.activeSelectors["SECURITIES_FO"]
		}

		if selector != nil {
			tb.logger.Info("Running stock selector for strategy", map[string]interface{}{
				"strategy": strat.Name(),
				"selector": selector.Name(),
			})
			wList, err := selector.SelectStocks(tb.ctx, tb.logger.Logger, tb.kiteClient, tb.securityMaster, tb.globalBias, tb.cfg.WatchlistSize, tb.cfg.WatchlistMaxPctChange)
			if err != nil {
				tb.logger.Error("Failed to select stocks for strategy", map[string]interface{}{
					"strategy": strat.Name(),
					"error":    err.Error(),
				})
				continue
			}

			tb.strategyWatchlists[strat.Name()] = wList

			// If strategy is VANDE_BHARAT, resolve and bind the PDH & PDL values
			if strat.Name() == "VANDE_BHARAT" {
				vbEngine, isVB := strat.(*strategy.VandeBharatEngine)
				if isVB {
					for symbol, token := range wList {
						high, low, err := tb.queryPreviousDayHighLow(token, loc)
						if err != nil {
							tb.logger.Error("Failed to query previous day high/low, using default fallback", map[string]interface{}{
								"symbol": symbol,
								"error":  err.Error(),
							})
							high, low = 0.0, 0.0
						}
						vbEngine.SetPreviousDayHighLow(symbol, high, low)
					}
				}
			}

			for symbol, token := range wList {
				tb.watchlist[symbol] = token
				if !tokenSet[token] {
					tokenSet[token] = true
					selectedTokens = append(selectedTokens, token)
				}
			}
		}
	}
	tb.watchlistMutex.Unlock()

	tb.logger.Info("Watchlist selection complete. Swapping WebSocket ticker subscriptions...", map[string]interface{}{"count": len(selectedTokens)})

	for _, strat := range tb.activeStrategies {
		strat.Reset()
	}

	_ = tb.ticker.Close()
	time.Sleep(1 * time.Second)
	if err := tb.ticker.Connect(tb.ctx, selectedTokens); err != nil {
		return fmt.Errorf("failed to reconnect ticker to unified watchlist: %w", err)
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
		for _, strat := range tb.activeStrategies {
			strat.OnCandleClose(candle, symbol)
		}
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

func (tb *TradingBot) startWebDashboard() {
	mux := http.NewServeMux()

	// Dashboard route: Serve HTML dashboard at /zt
	mux.HandleFunc("/zt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(dashboardHTML)
	})

	// Redirect root / to /zt
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/zt", http.StatusMovedPermanently)
			return
		}
		http.NotFound(w, r)
	})

	// API Watchlist: returns current bias, advances/declines, and active watchlist symbols
	mux.HandleFunc("/api/watchlist", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		tb.watchlistMutex.RLock()
		// Copy watchlist locally to avoid race conditions
		wlCopy := make(map[string]int64)
		for k, v := range tb.watchlist {
			wlCopy[k] = v
		}
		tb.watchlistMutex.RUnlock()

		// Get total completed trades count
		var totalTrades int
		_ = tb.db.WithContext(tb.ctx).QueryRowContext(tb.ctx, "SELECT COUNT(*) FROM trades").Scan(&totalTrades)

		// Get total P&L
		var totalPnL float64
		_ = tb.db.WithContext(tb.ctx).QueryRowContext(tb.ctx, "SELECT COALESCE(SUM(pnl), 0) FROM trades").Scan(&totalPnL)

		// Get total transaction value (entry_price * quantity)
		var totalTxValue float64
		_ = tb.db.WithContext(tb.ctx).QueryRowContext(tb.ctx, "SELECT COALESCE(SUM(entry_price * quantity), 0) FROM trades").Scan(&totalTxValue)

		// Calculate percentages
		var pctOnAccount float64 = 0.0
		if tb.cfg.InitialCapital > 0 {
			pctOnAccount = (totalPnL / tb.cfg.InitialCapital) * 100.0
		}

		var pctOnMargin float64 = 0.0
		if totalTxValue > 0 {
			// Assume 5x leverage standard for margin utilized calculation
			marginUtilized := totalTxValue / 5.0
			pctOnMargin = (totalPnL / marginUtilized) * 100.0
		}

		// Get market breadth stats
		var advances, declines, neutrals int
		var globalBias string
		_ = tb.db.WithContext(tb.ctx).QueryRowContext(tb.ctx,
			"SELECT advances, declines, neutrals, global_bias FROM market_breadth_logs ORDER BY time DESC LIMIT 1",
		).Scan(&advances, &declines, &neutrals, &globalBias)

		if globalBias == "" {
			globalBias = tb.globalBias
		}

		response := map[string]interface{}{
			"watchlist":         wlCopy,
			"global_bias":       globalBias,
			"advances":          advances,
			"declines":          declines,
			"neutrals":          neutrals,
			"stock_select_time": tb.cfg.StockSelectTime,
			"total_trades":      totalTrades,
			"total_pnl":         totalPnL,
			"pct_on_account":    pctOnAccount,
			"pct_on_margin":     pctOnMargin,
		}

		json.NewEncoder(w).Encode(response)
	})

	// API Candles: returns candles for the selected symbol
	mux.HandleFunc("/api/candles", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		symbol := r.URL.Query().Get("symbol")
		if symbol == "" {
			http.Error(w, `{"error":"symbol parameter required"}`, http.StatusBadRequest)
			return
		}

		// Resolve symbol to token
		tb.watchlistMutex.RLock()
		token, exists := tb.watchlist[symbol]
		tb.watchlistMutex.RUnlock()

		if !exists {
			http.Error(w, `{"error":"symbol not found in watchlist"}`, http.StatusNotFound)
			return
		}

		// Fetch candles from database since 09:15 AM today
		loc, err := time.LoadLocation("Asia/Kolkata")
		if err != nil {
			loc = time.Local
		}
		now := time.Now().In(loc)
		todayStart := time.Date(now.Year(), now.Month(), now.Day(), 9, 15, 0, 0, loc).UTC()

		rows, err := tb.db.WithContext(tb.ctx).QueryContext(tb.ctx,
			"SELECT time, open, high, low, close, volume FROM candles_5m WHERE token = $1 AND time >= $2 ORDER BY time ASC",
			token, todayStart,
		)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"database query failed: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		type APICandle struct {
			Time   int64   `json:"time"`
			Open   float64 `json:"open"`
			High   float64 `json:"high"`
			Low    float64 `json:"low"`
			Close  float64 `json:"close"`
			Volume int64   `json:"volume"`
		}

		list := make([]APICandle, 0)
		for rows.Next() {
			var t time.Time
			var o, h, l, c float64
			var v int64
			if err := rows.Scan(&t, &o, &h, &l, &c, &v); err != nil {
				continue
			}
			list = append(list, APICandle{
				Time:   t.In(loc).Unix(),
				Open:   o,
				High:   h,
				Low:    l,
				Close:  c,
				Volume: v,
			})
		}

		json.NewEncoder(w).Encode(list)
	})

	// API Trades: returns executed complete orders for marking buy/sell on chart
	mux.HandleFunc("/api/trades", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		symbol := r.URL.Query().Get("symbol")
		if symbol == "" {
			http.Error(w, `{"error":"symbol parameter required"}`, http.StatusBadRequest)
			return
		}

		// Query complete/filled orders for the symbol today
		loc, err := time.LoadLocation("Asia/Kolkata")
		if err != nil {
			loc = time.Local
		}
		now := time.Now().In(loc)
		todayStart := time.Date(now.Year(), now.Month(), now.Day(), 9, 15, 0, 0, loc).UTC()

		rows, err := tb.db.WithContext(tb.ctx).QueryContext(tb.ctx,
			"SELECT placed_at, transaction_type, average_price, quantity FROM orders WHERE symbol = $1 AND status = 'COMPLETE' AND placed_at >= $2 ORDER BY placed_at ASC",
			symbol, todayStart,
		)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"database query failed: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		type APITrade struct {
			Time            int64   `json:"time"`
			TransactionType string  `json:"transaction_type"`
			Price           float64 `json:"price"`
			Quantity        int     `json:"quantity"`
		}

		list := make([]APITrade, 0)
		for rows.Next() {
			var t time.Time
			var trType string
			var price float64
			var qty int
			if err := rows.Scan(&t, &trType, &price, &qty); err != nil {
				continue
			}
			list = append(list, APITrade{
				Time:            t.In(loc).Unix(),
				TransactionType: trType,
				Price:           price,
				Quantity:        qty,
			})
		}

		json.NewEncoder(w).Encode(list)
	})

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

func (tb *TradingBot) queryPreviousDayHighLow(token int64, loc *time.Location) (float64, float64, error) {
	// Find the most recent day where we have candles in DB prior to today
	nowIST := time.Now().In(loc)
	todayStart := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 0, 0, 0, 0, loc).UTC()

	var lastTime time.Time
	err := tb.db.WithContext(tb.ctx).QueryRowContext(tb.ctx, `
		SELECT MAX(time) FROM candles_5m WHERE token = $1 AND time < $2
	`, token, todayStart).Scan(&lastTime)
	if err != nil || lastTime.IsZero() {
		return 0, 0, fmt.Errorf("no historical date found for token %d: %w", token, err)
	}

	// The start and end of that previous trading day
	lastTimeIST := lastTime.In(loc)
	prevDayStart := time.Date(lastTimeIST.Year(), lastTimeIST.Month(), lastTimeIST.Day(), 0, 0, 0, 0, loc).UTC()
	prevDayEnd := time.Date(lastTimeIST.Year(), lastTimeIST.Month(), lastTimeIST.Day(), 23, 59, 59, 0, loc).UTC()

	var high, low float64
	err = tb.db.WithContext(tb.ctx).QueryRowContext(tb.ctx, `
		SELECT MAX(high), MIN(low) FROM candles_5m
		WHERE token = $1 AND time >= $2 AND time <= $3
	`, token, prevDayStart, prevDayEnd).Scan(&high, &low)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to scan high/low: %w", err)
	}

	return high, low, nil
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
