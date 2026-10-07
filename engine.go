package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	"zerodha-trading/data"
	"zerodha-trading/execution"
	"zerodha-trading/risk"
)

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
			nowIST := time.Now().In(data.ISTLocation)

			// Block processing any ticks or candles before the official market open at 09:15 AM IST
			marketOpenTime := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 9, 15, 0, 0, data.ISTLocation)
			if nowIST.Before(marketOpenTime) {
				continue
			}

			// Resolve start and end bounds for morning broad aggregation
			startH, startM, errStart := parseTimeHM(tb.cfg.MorningBroadAggStart)
			if errStart != nil {
				startH, startM = 9, 15
			}
			endH, endM, errEnd := parseTimeHM(tb.cfg.MorningBroadAggEnd)
			if errEnd != nil {
				endH, endM = 9, 45
			}

			morningStart := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), startH, startM, 0, 0, data.ISTLocation)
			morningEnd := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), endH, endM, 0, 0, data.ISTLocation)
			isMorningBroadWindow := !nowIST.Before(morningStart) && !nowIST.After(morningEnd)

			// Resolve list of tokens to aggregate
			tokensToProcess := make(map[int64]string) // token -> symbol (empty if not in active watchlist)

			if isMorningBroadWindow && tb.cfg.BroadSubscribe {
				tb.broadTokensMutex.RLock()
				for token := range tb.broadSubscriptionTokens {
					tokensToProcess[token] = ""
				}
				tb.broadTokensMutex.RUnlock()
			}

			// Always include the active watchlist symbols
			tb.watchlistMutex.RLock()
			for symbol, token := range tb.watchlist {
				tokensToProcess[token] = symbol
			}
			tb.watchlistMutex.RUnlock()

			// Include all supported Index Tokens (NIFTY, BANKNIFTY, SENSEX, FINNIFTY, MIDCPNIFTY) for options bot live 5m candles
			for _, spec := range data.GetAllSupportedIndices() {
				tokensToProcess[spec.SpotToken] = spec.Name
			}

			// Check and update live LTP for active options positions across all active managers
			if tb.optionsPosMgrs != nil {
				for _, mgr := range tb.optionsPosMgrs {
					if mgr == nil {
						continue
					}
					if optPos := mgr.GetActivePosition(); optPos != nil {
						if optToken, err := tb.securityMaster.GetInstrumentToken(optPos.Symbol); err == nil && optToken > 0 {
							tokensToProcess[optToken] = optPos.Symbol
							if optTick := tb.ticker.GetLatestTick(optToken); optTick != nil && optTick.LTP > 0 {
								mgr.UpdateLTP(optTick.LTP)
							}
						}
					}
				}
			} else if tb.optionsPosMgr != nil {
				if optPos := tb.optionsPosMgr.GetActivePosition(); optPos != nil {
					if optToken, err := tb.securityMaster.GetInstrumentToken(optPos.Symbol); err == nil && optToken > 0 {
						tokensToProcess[optToken] = optPos.Symbol
						if optTick := tb.ticker.GetLatestTick(optToken); optTick != nil && optTick.LTP > 0 {
							tb.optionsPosMgr.UpdateLTP(optTick.LTP)
						}
					}
				}
			}

			for token, symbol := range tokensToProcess {
				tick := tb.ticker.GetLatestTick(token)
				if tick != nil {
					tb.candleAgg1m.ProcessTick(tick)
					tb.candleAgg.ProcessTick(tick)

					// If strategy is active and inside trading window, check breakout for active watchlist symbols
					if symbol != "" && tb.globalBias != "NO_TRADE" {
						if tb.globalBias == "" {
							// Lazy load from DB if not yet set, or default to BUY_ONLY so live breakout ticks are never dropped
							_, _, _, latestBias, errBreadth := tb.db.GetLatestMarketBreadth(tb.ctx)
							if errBreadth == nil && latestBias != "" {
								tb.globalBias = latestBias
							} else {
								tb.globalBias = "BUY_ONLY"
							}
						}
						for _, strat := range tb.activeStrategies {
							// Strategy trade window check:
							// ALL stocks strictly obey the strategy's configured Trade Cutoff Time (regardless of whether from automated scanner or manual watchlist)
							var endH, endM, endS int
							endH, endM, endS, errTime := data.ParseTimeHMS(strat.TradeEndTime())
							if errTime != nil {
								endH, endM, endS = 11, 0, 0
							}

							endBoundary := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), endH, endM, endS, 0, data.ISTLocation)

							if !nowIST.Before(endBoundary) {
								continue
							}

							tb.watchlistMutex.RLock()
							_, inMaster := tb.watchlist[symbol]
							wList := tb.strategyWatchlists[strat.Name()]
							var inWatchlist bool
							if len(wList) > 0 {
								_, inWatchlist = wList[symbol]
							}
							tb.watchlistMutex.RUnlock()

							if !inWatchlist && inMaster {
								// Dynamically verify if symbol matches strategy's configured attached_stock_selections
								if tb.isSymbolAllowedForStrategy(symbol, strat.Name()) {
									inWatchlist = true
									tb.watchlistMutex.Lock()
									if tb.strategyWatchlists[strat.Name()] == nil {
										tb.strategyWatchlists[strat.Name()] = make(map[string]int64)
									}
									tb.strategyWatchlists[strat.Name()][symbol] = tick.Token
									tb.watchlistMutex.Unlock()
								}
							}

							if !inWatchlist || tb.IsStockExcluded(symbol) {
								continue
							}

							signal := strat.CheckBreakout(symbol, tick.LTP, tb.globalBias)
							if signal != nil {
								// Enforce stock-specific directional bias from pre-selection results
								tb.watchlistDirectionsMutex.RLock()
								predDir, hasDir := tb.watchlistDirections[symbol]
								tb.watchlistDirectionsMutex.RUnlock()

								if hasDir && !tb.isManualStock(symbol) {
									if predDir == "BULLISH BREAKOUT" && signal.Action != "BUY" {
										tb.logger.Info("Skipping breakout signal due to BULLISH directional bias mismatch", map[string]interface{}{
											"symbol": symbol,
											"action": signal.Action,
											"bias":   predDir,
										})
										if tb.tracer != nil {
											tb.tracer.Emit(&data.StrategyEvent{
												Symbol:       symbol,
												Strategy:     strat.Name(),
												Stage:        "TRADE_SKIPPED",
												Severity:     "WARNING",
												Direction:    signal.Action,
												Title:        fmt.Sprintf("%s Trade Skipped: Direction Bias Mismatch", strat.Name()),
												Reason:       fmt.Sprintf("Signal %s skipped: Stock pre-selection bias is %s", signal.Action, predDir),
												TriggerPrice: tick.LTP,
												Details:      map[string]interface{}{"action": signal.Action, "bias": predDir, "ltp": tick.LTP},
											})
										}
										continue
									}
									if predDir == "BEARISH BREAKDOWN" && signal.Action != "SELL" {
										tb.logger.Info("Skipping breakout signal due to BEARISH directional bias mismatch", map[string]interface{}{
											"symbol": symbol,
											"action": signal.Action,
											"bias":   predDir,
										})
										if tb.tracer != nil {
											tb.tracer.Emit(&data.StrategyEvent{
												Symbol:       symbol,
												Strategy:     strat.Name(),
												Stage:        "TRADE_SKIPPED",
												Severity:     "WARNING",
												Direction:    signal.Action,
												Title:        fmt.Sprintf("%s Trade Skipped: Direction Bias Mismatch", strat.Name()),
												Reason:       fmt.Sprintf("Signal %s skipped: Stock pre-selection bias is %s", signal.Action, predDir),
												TriggerPrice: tick.LTP,
												Details:      map[string]interface{}{"action": signal.Action, "bias": predDir, "ltp": tick.LTP},
											})
										}
										continue
									}
								}

								// Mandatory Multi-Validation: Check if strategy requires confirmed Institutional Footprint (IFP)
								if tb.IsStrategyRequireIFP(strat.Name()) && !tb.HasSymbolIFP(symbol) {
									tb.logger.Info("Skipping breakout signal due to missing mandatory Institutional Footprint (IFP) confirmation", map[string]interface{}{
										"symbol":   symbol,
										"strategy": strat.Name(),
										"action":   signal.Action,
										"ltp":      tick.LTP,
									})
									if tb.tracer != nil {
										tb.tracer.Emit(&data.StrategyEvent{
											Symbol:       symbol,
											Strategy:     strat.Name(),
											Stage:        "TRADE_SKIPPED",
											Severity:     "WARNING",
											Direction:    signal.Action,
											Title:        fmt.Sprintf("%s Trade Blocked: Missing IFP Confirmation", strat.Name()),
											Reason:       fmt.Sprintf("Signal %s blocked: Strategy %s requires confirmed Institutional Footprint (IFP) order flow, but none detected today for %s", signal.Action, strat.Name(), symbol),
											TriggerPrice: tick.LTP,
											Details: map[string]interface{}{
												"action":      signal.Action,
												"strategy":    strat.Name(),
												"ltp":         tick.LTP,
												"require_ifp": true,
											},
										})
									}
									continue
								}

								if tb.riskMgr.HasOpenPosition(symbol) {
									tb.logger.Info("Position already open for symbol, skipping breakout trigger", map[string]interface{}{
										"symbol":   symbol,
										"strategy": strat.Name(),
									})
									if tb.tracer != nil {
										tb.tracer.Emit(&data.StrategyEvent{
											Symbol:       symbol,
											Strategy:     strat.Name(),
											Stage:        "TRADE_SKIPPED",
											Severity:     "INFO",
											Direction:    signal.Action,
											Title:        fmt.Sprintf("%s Trade Skipped: Position Already Open", strat.Name()),
											Reason:       fmt.Sprintf("Open position already exists for %s. Skipping duplicate breakout.", symbol),
											TriggerPrice: tick.LTP,
											Details:      map[string]interface{}{"symbol": symbol, "ltp": tick.LTP},
										})
									}
									continue
								}

								tb.logger.InfoTrade(fmt.Sprintf("%s breakout signal triggered", strat.Name()), map[string]interface{}{
									"symbol": symbol,
									"action": signal.Action,
									"ltp":    tick.LTP,
									"reason": signal.Reason,
								})

								// Compute margin per share using pre-cached leverage
								leverage := tb.getLeverage(symbol)
								var setupHigh, setupLow float64
								setup := strat.GetSetupCandle(symbol)
								if setup != nil {
									setupHigh = setup.High
									setupLow = setup.Low
								}

								tickSize := tb.getTickSize(symbol)
								if tickSize <= 0 {
									tickSize = 0.05
								}

								// In RETEST_BAND mode, enforce the Max Chase Ceiling:
								// If price has already spiked beyond ConfirmationCandle ± MaxChaseTicks, skip trade to prevent chasing!
								if overextended, reason, ceilingPrice := tb.isBreakoutOverextended(signal.Action, tick.LTP, setupHigh, setupLow, tickSize); overextended {
									tb.logger.InfoTrade("Breakout trade skipped: LTP exceeded max chase ceiling", map[string]interface{}{
										"symbol":          symbol,
										"strategy":        strat.Name(),
										"action":          signal.Action,
										"ltp":             tick.LTP,
										"ceiling_price":   ceilingPrice,
										"max_chase_ticks": tb.cfg.EntryLimitMaxChaseTicks,
									})
									if tb.tracer != nil {
										tb.tracer.Emit(&data.StrategyEvent{
											Symbol:       symbol,
											Strategy:     strat.Name(),
											Stage:        "TRADE_SKIPPED",
											Severity:     "WARNING",
											Direction:    signal.Action,
											Title:        fmt.Sprintf("%s Trade Skipped: Breakout Overextended", strat.Name()),
											Reason:       reason,
											TriggerPrice: tick.LTP,
											Details:      map[string]interface{}{"ltp": tick.LTP, "ceiling_price": ceilingPrice, "max_chase_ticks": tb.cfg.EntryLimitMaxChaseTicks},
										})
									}
									continue
								}

								// Compute planned entry price and order type
								orderType, plannedEntryPrice, limitPrice := tb.calculatePlannedEntryPrice(symbol, signal.Action, tick.LTP, setupHigh, setupLow)

								// Compute margin per share using pre-cached leverage and planned entry price
								marginPerShare := plannedEntryPrice / leverage

								var bufferPct float64
								if strat.Name() == "VANDE_BHARAT" {
									bufferPct = tb.cfg.VBSLBufferPct
								} else if strat.Name() == "FAKE_BREAKOUT" {
									bufferPct = tb.cfg.FBSLBufferPct
								} else if strat.Name() == "VANDE_BHARAT_TRAP" {
									bufferPct = tb.cfg.VBTSLBufferPct
								} else if strat.Name() == "EMAS5_BREAKOUT" {
									bufferPct = tb.cfg.ES5SLBufferPct
								} else {
									bufferPct = tb.cfg.SLBufferPct
								}

								rrStrat := tb.riskMgr.GetStrategyForPosition(strat.Name())
								profile := rrStrat.CalculateProfile(plannedEntryPrice, signal.Action, setupHigh, setupLow, bufferPct, tb.cfg.RiskPerTrade, tb.cfg.InitialCapital, marginPerShare, 0)

								if profile.Quantity <= 0 {
									tb.logger.Warn("Calculated quantity is zero. Skipping breakout trade entry.", map[string]interface{}{
										"symbol":         symbol,
										"ltp":            tick.LTP,
										"planned_price":  plannedEntryPrice,
										"risk_per_trade": tb.cfg.RiskPerTrade,
										"capital":        tb.cfg.InitialCapital,
									})
									if tb.tracer != nil {
										tb.tracer.Emit(&data.StrategyEvent{
											Symbol:       symbol,
											Strategy:     strat.Name(),
											Stage:        "TRADE_SKIPPED",
											Severity:     "DANGER",
											Direction:    signal.Action,
											Title:        fmt.Sprintf("%s Trade Skipped: Insufficient Margin / Zero Qty", strat.Name()),
											Reason:       fmt.Sprintf("Position sizing calculated 0 shares (Price ₹%.2f, RiskPerTrade ₹%.2f, Capital ₹%.2f)", plannedEntryPrice, tb.cfg.RiskPerTrade, tb.cfg.InitialCapital),
											TriggerPrice: plannedEntryPrice,
											Details:      map[string]interface{}{"ltp": tick.LTP, "planned_price": plannedEntryPrice, "risk_per_trade": tb.cfg.RiskPerTrade, "capital": tb.cfg.InitialCapital},
										})
									}
									continue
								}

								tb.logger.InfoTrade(fmt.Sprintf("%s position sizing calculated based on Risk Per Trade", strat.Name()), map[string]interface{}{
									"symbol":         symbol,
									"action":         signal.Action,
									"ltp":            tick.LTP,
									"planned_price":  plannedEntryPrice,
									"sl":             profile.StopLoss,
									"target1":        profile.Target1,
									"sl_distance":    profile.SLDistance,
									"risk_per_trade": tb.cfg.RiskPerTrade,
									"quantity":       profile.Quantity,
									"max_loss_inr":   profile.MaxLoss,
								})

								if !tb.riskMgr.CanPlaceOrder(profile.Quantity, plannedEntryPrice) {
									if tb.tracer != nil {
										tb.tracer.Emit(&data.StrategyEvent{
											Symbol:       symbol,
											Strategy:     strat.Name(),
											Stage:        "TRADE_SKIPPED",
											Severity:     "DANGER",
											Direction:    signal.Action,
											Title:        fmt.Sprintf("%s Trade Blocked: Risk Circuit Breaker", strat.Name()),
											Reason:       fmt.Sprintf("Risk Manager blocked order for Qty %d at ₹%.2f (Daily loss or max trades limit reached)", profile.Quantity, plannedEntryPrice),
											TriggerPrice: plannedEntryPrice,
											Details:      map[string]interface{}{"qty": profile.Quantity, "price": plannedEntryPrice},
										})
									}
									continue
								}

								orderReq := execution.OrderRequest{
									TradingSymbol:   symbol,
									Exchange:        "NSE",
									Quantity:        profile.Quantity,
									TransactionType: signal.Action,
									OrderType:       orderType,
									Product:         "MIS",
									Validity:        "DAY",
									Strategy:        strat.Name(),
									Price:           limitPrice,
								}

								orderID, err := tb.execMgr.PlaceOrder(orderReq)
								if err != nil {
									tb.logger.Error("Failed to place breakout order", map[string]interface{}{"error": err.Error(), "symbol": symbol, "strategy": strat.Name()})
									if tb.tracer != nil {
										tb.tracer.Emit(&data.StrategyEvent{
											Symbol:       symbol,
											Strategy:     strat.Name(),
											Stage:        "TRADE_SKIPPED",
											Severity:     "DANGER",
											Direction:    signal.Action,
											Title:        fmt.Sprintf("%s Order Placement Failed", strat.Name()),
											Reason:       fmt.Sprintf("Execution manager failed to place order: %v", err),
											TriggerPrice: plannedEntryPrice,
											Details:      map[string]interface{}{"error": err.Error(), "symbol": symbol},
										})
									}
								} else {
									tb.riskMgr.AddOpenPosition(orderID, symbol, token, profile.Quantity, plannedEntryPrice, signal.Action, profile.StopLoss, strat.Name(), profile.Target1, time.Now())
									_ = tb.db.SaveOpenPosition(tb.ctx, orderID, symbol, profile.Quantity, plannedEntryPrice, signal.Action, profile.StopLoss, strat.Name(), "")
									if !tb.execMgr.LiveTrading {
										isMarketable := orderReq.OrderType == execution.OrderTypeMarket ||
											(signal.Action == "BUY" && plannedEntryPrice >= tick.LTP) ||
											(signal.Action == "SELL" && plannedEntryPrice <= tick.LTP)
										if isMarketable {
											tb.execMgr.SimulateOrderFill(orderID, profile.Quantity, plannedEntryPrice)
										}
									}
									tb.statusTracker.StartTracking(orderID)
									if tb.tracer != nil {
										tb.tracer.Emit(&data.StrategyEvent{
											Symbol:        symbol,
											Strategy:      strat.Name(),
											Stage:         "TRADE_ORDER_PLACED",
											Severity:      "SUCCESS",
											Direction:     signal.Action,
											Title:         fmt.Sprintf("%s %s Order Placed [%s]", strat.Name(), signal.Action, orderID),
											Reason:        fmt.Sprintf("Order placed successfully. Qty: %d, Entry: ₹%.2f, SL: ₹%.2f, Target 1: ₹%.2f, Type: %s", profile.Quantity, plannedEntryPrice, profile.StopLoss, profile.Target1, orderType),
											TriggerPrice:  plannedEntryPrice,
											SLPrice:       profile.StopLoss,
											TargetPrice:   profile.Target1,
											ExecutedPrice: plannedEntryPrice,
											ExecutedQty:   profile.Quantity,
											Details:       map[string]interface{}{"order_id": orderID, "qty": profile.Quantity, "sl": profile.StopLoss, "target1": profile.Target1, "risk_per_trade": tb.cfg.RiskPerTrade, "order_type": orderType},
										})
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

// orderManagementLoop monitors open positions and processes risk exits / partial exits
func (tb *TradingBot) orderManagementLoop() {
	defer tb.wg.Done()

	tb.logger.Info("Order management loop started", nil)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastBrokerReconcile time.Time

	for {
		select {
		case <-tb.ctx.Done():
			return
		case <-ticker.C:
			// Continuous broker position reconciliation every 10 seconds in live mode
			if tb.execMgr.LiveTrading && tb.kiteClient != nil && time.Since(lastBrokerReconcile) >= 10*time.Second {
				lastBrokerReconcile = time.Now()
				tb.reconcilePositionsWithBroker()
			}

			positions := tb.riskMgr.GetOpenPositions()
			for orderID, pos := range positions {
				// Cancel pending entry orders if they did not fill within entry limit timeout
				orderStatus := tb.statusTracker.GetCachedStatus(orderID)
				timeoutSec := tb.cfg.EntryLimitTimeoutSec
				if timeoutSec <= 0 {
					timeoutSec = tb.cfg.CandleIntervalSec
				}
				if timeoutSec <= 0 {
					timeoutSec = 60
				}

				if orderStatus != nil {
					isPending := orderStatus.Status != "COMPLETE" && orderStatus.Status != "CANCELLED" && orderStatus.Status != "REJECTED"
					if isPending {
						// For paper trading: check if retest price was touched
						if !tb.execMgr.LiveTrading {
							latestTick := tb.ticker.GetLatestTick(pos.Token)
							if latestTick != nil {
								retestFilled := (pos.Side == "BUY" && latestTick.LTP <= pos.EntryPrice) ||
									(pos.Side == "SELL" && latestTick.LTP >= pos.EntryPrice)
								if retestFilled {
									tb.logger.Info("Paper trading limit entry retest executed", map[string]interface{}{
										"order_id": orderID,
										"symbol":   pos.Symbol,
										"side":     pos.Side,
										"limit":    pos.EntryPrice,
										"ltp":      latestTick.LTP,
									})
									tb.execMgr.SimulateOrderFill(orderID, pos.Quantity, pos.EntryPrice)
									continue
								}
							}
						}

						if time.Since(pos.CreatedAt) >= time.Duration(timeoutSec)*time.Second {
							tb.logger.Warn("Cancelling pending entry order: did not fill within entry limit timeout",
								map[string]interface{}{"order_id": orderID, "symbol": pos.Symbol, "status": orderStatus.Status, "timeout_sec": timeoutSec})
							tb.execMgr.CancelOrder(orderID)
							if orderStatus.FilledQuantity == 0 {
								tb.riskMgr.OnOrderClose(orderID, 0, 0)
								_ = tb.db.CloseOpenPosition(tb.ctx, orderID, 0)
								if tb.tracer != nil {
									tb.tracer.Emit(&data.StrategyEvent{
										Symbol:       pos.Symbol,
										Strategy:     pos.Strategy,
										Stage:        "TRADE_SKIPPED",
										Severity:     "WARNING",
										Direction:    pos.Side,
										Title:        fmt.Sprintf("%s Entry Limit Order Expired", pos.Strategy),
										Reason:       fmt.Sprintf("Entry limit order %s cancelled after %ds timeout without retest fill", orderID, timeoutSec),
										TriggerPrice: pos.EntryPrice,
										Details:      map[string]interface{}{"order_id": orderID, "timeout_sec": timeoutSec, "status": orderStatus.Status},
									})
								}
							}
							continue
						}

						// Guard: Entry limit order is still pending on the broker.
						// DO NOT fall through to trailing SL or target exit evaluation!
						continue
					} else if orderStatus.Status == "COMPLETE" {
						if orderStatus.AveragePrice > 0 && math.Abs(orderStatus.AveragePrice-pos.EntryPrice) > 0.01 {
							tb.riskMgr.UpdatePositionEntryPrice(orderID, orderStatus.AveragePrice)
							pos.EntryPrice = orderStatus.AveragePrice
							_ = tb.db.SaveOpenPosition(tb.ctx, orderID, pos.Symbol, pos.Quantity, pos.EntryPrice, pos.Side, pos.SLPrice, pos.Strategy, pos.BrokerSLOrderID)
						}
						tb.placeBrokerStopLoss(orderID, pos)
					} else if orderStatus.Status == "CANCELLED" {
						if orderStatus.FilledQuantity > 0 && pos.BrokerSLOrderID == "" {
							// Entry order was partially filled and cancelled!
							// 1. Update position quantity to actual filled quantity in risk manager
							tb.logger.Info("Partial fill detected on cancelled entry order. Updating quantity and placing broker SL.",
								map[string]interface{}{
									"order_id":   orderID,
									"symbol":     pos.Symbol,
									"filled_qty": orderStatus.FilledQuantity,
								})
							tb.riskMgr.UpdatePositionQuantity(orderID, orderStatus.FilledQuantity)
							pos.Quantity = orderStatus.FilledQuantity
							_ = tb.db.SaveOpenPosition(tb.ctx, orderID, pos.Symbol, pos.Quantity, pos.EntryPrice, pos.Side, pos.SLPrice, pos.Strategy, pos.BrokerSLOrderID)

							// 2. Place stop-loss order at Zerodha for the updated quantity
							tb.placeBrokerStopLoss(orderID, pos)
						} else if orderStatus.FilledQuantity == 0 {
							tb.riskMgr.OnOrderClose(orderID, 0, 0)
							_ = tb.db.CloseOpenPosition(tb.ctx, orderID, 0)
							continue
						}
					} else if orderStatus.Status == "REJECTED" {
						if orderStatus.FilledQuantity == 0 {
							tb.logger.Warn("Entry order rejected by broker, cleaning up position tracking",
								map[string]interface{}{"order_id": orderID, "symbol": pos.Symbol})
							tb.riskMgr.OnOrderClose(orderID, 0, 0)
							_ = tb.db.CloseOpenPosition(tb.ctx, orderID, 0)
							continue
						}
					}
				} else {
					if time.Since(pos.CreatedAt) >= time.Duration(timeoutSec)*time.Second {
						tb.logger.Warn("Cancelling untracked pending entry order after timeout", map[string]interface{}{
							"order_id": orderID,
							"symbol":   pos.Symbol,
						})
						tb.execMgr.CancelOrder(orderID)
						tb.riskMgr.OnOrderClose(orderID, 0, 0)
						_ = tb.db.CloseOpenPosition(tb.ctx, orderID, 0)
						continue
					}
					// If order status is not yet available in live trading, wait for next tick
					if tb.execMgr.LiveTrading {
						continue
					}
				}

				tick := tb.ticker.GetLatestTick(pos.Token)
				if tick == nil {
					continue
				}

				currentPrice := tick.LTP

				// If broker-side SL is enabled, first check if it has been filled on the broker's system
				var useBrokerSL bool
				if pos.Strategy == "VANDE_BHARAT" {
					useBrokerSL = tb.cfg.VBUseBrokerSL
				} else if pos.Strategy == "FAKE_BREAKOUT" {
					useBrokerSL = tb.cfg.FBUseBrokerSL
				} else if pos.Strategy == "VANDE_BHARAT_TRAP" {
					useBrokerSL = tb.cfg.VBTUseBrokerSL
				} else if pos.Strategy == "EMAS5_BREAKOUT" {
					useBrokerSL = tb.cfg.ES5UseBrokerSL
				} else if pos.Strategy == "MANUAL" {
					useBrokerSL = tb.cfg.ManualTradeUseBrokerSL
				} else {
					useBrokerSL = tb.cfg.LVUseBrokerSL
				}

				if useBrokerSL && pos.BrokerSLOrderID != "" {
					slStatus, err := tb.execMgr.GetOrderStatus(pos.BrokerSLOrderID)
					if err == nil && slStatus != nil && (slStatus.Status == "COMPLETE" || slStatus.Status == "FILLED") {
						tb.logger.Info("Broker-side stop-loss order got filled", map[string]interface{}{
							"symbol":      pos.Symbol,
							"sl_order_id": pos.BrokerSLOrderID,
							"price":       slStatus.AveragePrice,
							"strategy":    pos.Strategy,
						})
						if tb.tracer != nil {
							tb.tracer.Emit(&data.StrategyEvent{
								Symbol:        pos.Symbol,
								Strategy:      pos.Strategy,
								Stage:         "TRADE_CLOSED",
								Severity:      "DANGER",
								Direction:     pos.Side,
								Title:         fmt.Sprintf("%s Broker Stop-Loss Hit at ₹%.2f", pos.Strategy, slStatus.AveragePrice),
								Reason:        fmt.Sprintf("Broker SL order %s filled at ₹%.2f for %d shares", pos.BrokerSLOrderID, slStatus.AveragePrice, pos.Quantity),
								ExecutedPrice: slStatus.AveragePrice,
								ExecutedQty:   pos.Quantity,
								Details:       map[string]interface{}{"order_id": orderID, "sl_order_id": pos.BrokerSLOrderID, "price": slStatus.AveragePrice, "qty": pos.Quantity},
							})
						}
						tb.riskMgr.OnOrderClose(orderID, slStatus.AveragePrice, pos.Quantity)
						_ = tb.db.CloseOpenPosition(tb.ctx, orderID, slStatus.AveragePrice)
						continue
					} else if slStatus.Status == "CANCELLED" || slStatus.Status == "REJECTED" {
						tb.logger.Warn("Broker stop-loss order was cancelled or rejected on broker. Clearing broker SL tracking.", map[string]interface{}{
							"symbol":      pos.Symbol,
							"sl_order_id": pos.BrokerSLOrderID,
							"status":      slStatus.Status,
						})
						pos.BrokerSLOrderID = ""
						tb.riskMgr.SetBrokerSLOrderID(orderID, "")
						_ = tb.db.SaveOpenPosition(tb.ctx, orderID, pos.Symbol, pos.Quantity, pos.EntryPrice, pos.Side, pos.SLPrice, pos.Strategy, "")
					}
				}

				// Check risk limits (Stop-Loss and Target 1 partial exits)
				action := tb.riskMgr.CheckTrailingSL(orderID, currentPrice)
				if fresh := tb.riskMgr.GetPosition(orderID); fresh != nil {
					pos = fresh
				}
				if action == "SL_TRAILED" && tb.tracer != nil {
					tb.tracer.Emit(&data.StrategyEvent{
						Symbol:    pos.Symbol,
						Strategy:  pos.Strategy,
						Stage:     "SL_TRAILED",
						Severity:  "INFO",
						Direction: pos.Side,
						Title:     fmt.Sprintf("%s Trailing SL Moved to ₹%.2f", pos.Strategy, pos.SLPrice),
						Reason:    fmt.Sprintf("Peak price ₹%.2f pushed trailing SL to ₹%.2f (Current LTP ₹%.2f)", pos.HighestPrice, pos.SLPrice, currentPrice),
						SLPrice:   pos.SLPrice,
						Details:   map[string]interface{}{"order_id": orderID, "new_sl": pos.SLPrice, "highest_price": pos.HighestPrice, "ltp": currentPrice},
					})
				}
				if action == "CLOSE" {
					if tb.execMgr.LiveTrading {
						ordStat := tb.statusTracker.GetCachedStatus(orderID)
						if ordStat == nil || ordStat.Status != "COMPLETE" {
							continue
						}
					}

					if useBrokerSL && pos.BrokerSLOrderID != "" {
						// Under broker-side SL, we let the broker execute the trigger order.
						// Do NOT place a duplicate market order.
						continue
					}

					if tb.execMgr.LiveTrading {
						// For live trading, to close an open position, we MUST place an opposite market order!
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
							Strategy:        pos.Strategy,
						}
						if orderReq.OrderType == execution.OrderTypeLimit {
							limitBuf := tb.cfg.LimitBufferPct
							if limitBuf <= 0 {
								limitBuf = 0.2
							}
							tickSize := tb.getTickSize(pos.Symbol)
							var limPrice float64
							if txnType == "BUY" {
								limPrice = risk.RoundTick(currentPrice*(1.0+limitBuf/100.0), tickSize)
							} else {
								limPrice = risk.RoundTick(currentPrice*(1.0-limitBuf/100.0), tickSize)
							}
							orderReq.Price = &limPrice
						}

						exitOrderID, err := tb.execMgr.PlaceOrder(orderReq)
						if err != nil {
							tb.logger.Error("Failed to place market exit order in live trading", map[string]interface{}{"error": err.Error(), "symbol": pos.Symbol, "strategy": pos.Strategy})
						} else {
							tb.logger.Info("Live market exit order placed", map[string]interface{}{
								"order_id": exitOrderID,
								"symbol":   pos.Symbol,
								"qty":      pos.Quantity,
							})
							if pos.BrokerSLOrderID != "" {
								tb.logger.Info("Cancelling broker-side stop-loss order for closed position", map[string]interface{}{
									"symbol":      pos.Symbol,
									"sl_order_id": pos.BrokerSLOrderID,
								})
								tb.execMgr.CancelOrder(pos.BrokerSLOrderID)
							}
							tb.statusTracker.StartTracking(exitOrderID)
							tb.riskMgr.OnOrderClose(orderID, currentPrice, pos.Quantity)
							_ = tb.db.CloseOpenPosition(tb.ctx, orderID, currentPrice)
							if tb.tracer != nil {
								tb.tracer.Emit(&data.StrategyEvent{
									Symbol:        pos.Symbol,
									Strategy:      pos.Strategy,
									Stage:         "TRADE_CLOSED",
									Severity:      "INFO",
									Direction:     pos.Side,
									Title:         fmt.Sprintf("%s Position Closed at ₹%.2f", pos.Strategy, currentPrice),
									Reason:        fmt.Sprintf("Live market exit order %s executed for %d shares at ₹%.2f", exitOrderID, pos.Quantity, currentPrice),
									ExecutedPrice: currentPrice,
									ExecutedQty:   pos.Quantity,
									Details:       map[string]interface{}{"order_id": orderID, "exit_order_id": exitOrderID, "price": currentPrice, "qty": pos.Quantity},
								})
							}
						}
					} else {
						tb.execMgr.CancelOrder(orderID)
						tb.riskMgr.OnOrderClose(orderID, currentPrice, pos.Quantity)
						_ = tb.db.CloseOpenPosition(tb.ctx, orderID, currentPrice)
						if tb.tracer != nil {
							tb.tracer.Emit(&data.StrategyEvent{
								Symbol:        pos.Symbol,
								Strategy:      pos.Strategy,
								Stage:         "TRADE_CLOSED",
								Severity:      "INFO",
								Direction:     pos.Side,
								Title:         fmt.Sprintf("%s Paper Position Closed at ₹%.2f", pos.Strategy, currentPrice),
								Reason:        fmt.Sprintf("Paper exit completed for %d shares at ₹%.2f", pos.Quantity, currentPrice),
								ExecutedPrice: currentPrice,
								ExecutedQty:   pos.Quantity,
								Details:       map[string]interface{}{"order_id": orderID, "price": currentPrice, "qty": pos.Quantity},
							})
						}
					}
				} else if action == "PARTIAL_EXIT" {
					if tb.execMgr.LiveTrading {
						ordStat := tb.statusTracker.GetCachedStatus(orderID)
						if ordStat == nil || ordStat.Status != "COMPLETE" {
							continue
						}
					}

					// Perform Target 1 partial exit based on strategy configuration
					var txnType string
					if pos.Side == "BUY" {
						txnType = "SELL"
					} else {
						txnType = "BUY"
					}

					exitPct := tb.riskMgr.GetPartialExitPct(pos.Strategy) / 100.0
					if pos.Strategy == "MANUAL" && tb.cfg.ManualTradePartialExitPct > 0 {
						exitPct = tb.cfg.ManualTradePartialExitPct / 100.0
					}
					if exitPct <= 0 || exitPct > 1.0 {
						exitPct = 0.50
					}

					closeQty := int(math.Round(float64(pos.Quantity) * exitPct))
					if closeQty == 0 && pos.Quantity > 0 {
						closeQty = 1
					}
					if pos.Quantity > 1 && closeQty >= pos.Quantity {
						closeQty = pos.Quantity - 1
					}
					if closeQty > 0 {
						orderReq := execution.OrderRequest{
							TradingSymbol:   pos.Symbol,
							Exchange:        "NSE",
							Quantity:        closeQty,
							TransactionType: txnType,
							OrderType:       execution.OrderType(tb.cfg.DefaultOrderType),
							Product:         "MIS",
							Validity:        "DAY",
							Strategy:        pos.Strategy,
						}
						if orderReq.OrderType == execution.OrderTypeLimit {
							limitBuf := tb.cfg.LimitBufferPct
							if limitBuf <= 0 {
								limitBuf = 0.2
							}
							tickSize := tb.getTickSize(pos.Symbol)
							var limPrice float64
							if txnType == "BUY" {
								limPrice = risk.RoundTick(currentPrice*(1.0+limitBuf/100.0), tickSize)
							} else {
								limPrice = risk.RoundTick(currentPrice*(1.0-limitBuf/100.0), tickSize)
							}
							orderReq.Price = &limPrice
						}

						exitOrderID, err := tb.execMgr.PlaceOrder(orderReq)
						if err != nil {
							tb.logger.Error("Failed to place partial exit order", map[string]interface{}{"error": err.Error(), "symbol": pos.Symbol, "strategy": pos.Strategy})
						} else {
							tb.logger.Info(fmt.Sprintf("Target 1 %.0f%% partial exit order placed", exitPct*100.0), map[string]interface{}{
								"order_id": exitOrderID,
								"symbol":   pos.Symbol,
								"qty":      closeQty,
								"strategy": pos.Strategy,
							})
							if !tb.execMgr.LiveTrading {
								tb.execMgr.SimulateOrderFill(exitOrderID, closeQty, currentPrice)
							} else {
								tb.statusTracker.StartTracking(exitOrderID)
							}
							tb.riskMgr.RecordPartialExit(orderID, currentPrice, closeQty)

							if pos.Quantity <= 0 {
								_ = tb.db.CloseOpenPosition(tb.ctx, orderID, currentPrice)
							} else {
								// Re-evaluate broker stop-loss for the remaining quantity
								tb.replaceBrokerSLOnPartialExit(orderID, pos, closeQty)
							}
						}
					}
				} else if action == "SL_TRAILED" {
					// Update broker-side SL order with the new trailed trigger price
					if useBrokerSL && pos.BrokerSLOrderID != "" {
						tb.logger.Info("Updating broker-side SL order to trailed trigger price", map[string]interface{}{
							"symbol":       pos.Symbol,
							"sl_order_id":  pos.BrokerSLOrderID,
							"new_sl_price": pos.SLPrice,
						})
						tb.replaceBrokerSLOnPartialExit(orderID, pos, 0)
					}
				}

				// Update current price
				tb.riskMgr.UpdatePositionPrice(orderID, currentPrice)
			}
		}
	}
}

// reconcilePositionsWithBroker continuously reconciles open positions with Zerodha
// to detect manual square-offs, broker RMS auto-square-offs, or external quantity changes.
func (tb *TradingBot) reconcilePositionsWithBroker() {
	if !tb.execMgr.LiveTrading || tb.kiteClient == nil {
		return
	}

	livePositions, err := tb.kiteClient.GetPositions()
	if err != nil {
		tb.logger.Error("Failed to fetch live positions for broker reconciliation", map[string]interface{}{"error": err.Error()})
		return
	}

	// Map net MIS quantities from Zerodha: symbol -> netQty
	brokerNetQty := make(map[string]int)
	for _, p := range livePositions.Net {
		if p.Product == "MIS" {
			brokerNetQty[p.TradingSymbol] = p.Quantity
		}
	}

	// Fetch all open positions tracked by the bot
	openPositions := tb.riskMgr.GetOpenPositions()
	for orderID, pos := range openPositions {
		// Only reconcile positions that have already completed entry on the exchange
		orderStatus := tb.statusTracker.GetCachedStatus(orderID)
		if orderStatus == nil || orderStatus.Status != "COMPLETE" {
			continue
		}

		brokerQty, exists := brokerNetQty[pos.Symbol]

		// Case 1: Zerodha net quantity is 0 or position does not exist on Zerodha
		if !exists || brokerQty == 0 {
			tb.logger.Warn("[BROKER_SYNC] Position closed externally on Zerodha (net qty is 0). Cleaning up local bot state.", map[string]interface{}{
				"symbol":   pos.Symbol,
				"order_id": orderID,
				"bot_qty":  pos.Quantity,
				"strategy": pos.Strategy,
			})

			// Cancel any open broker stop-loss order to prevent orphaned triggers
			if pos.BrokerSLOrderID != "" && pos.BrokerSLOrderID != "RECOVERING" {
				tb.logger.Info("[BROKER_SYNC] Cancelling orphaned broker SL order", map[string]interface{}{
					"symbol":      pos.Symbol,
					"sl_order_id": pos.BrokerSLOrderID,
				})
				_ = tb.execMgr.CancelOrder(pos.BrokerSLOrderID)
			}

			// Clean up risk manager and database
			currentPrice := pos.LatestPrice
			if currentPrice <= 0 {
				if tick := tb.ticker.GetLatestTick(pos.Token); tick != nil && tick.LTP > 0 {
					currentPrice = tick.LTP
				} else {
					currentPrice = pos.EntryPrice
				}
			}

			tb.riskMgr.OnOrderClose(orderID, currentPrice, pos.Quantity)
			_ = tb.db.CloseOpenPosition(tb.ctx, orderID, currentPrice)

			if tb.tracer != nil {
				tb.tracer.Emit(&data.StrategyEvent{
					Symbol:        pos.Symbol,
					Strategy:      pos.Strategy,
					Stage:         "TRADE_CLOSED",
					Severity:      "WARNING",
					Direction:     pos.Side,
					Title:         fmt.Sprintf("%s External Broker Square-Off Reconciled", pos.Strategy),
					Reason:        fmt.Sprintf("Position for %s reconciled to 0 shares (closed externally on Zerodha)", pos.Symbol),
					ExecutedPrice: currentPrice,
					ExecutedQty:   pos.Quantity,
					Details:       map[string]interface{}{"order_id": orderID, "price": currentPrice, "qty": pos.Quantity},
				})
			}
			continue
		}

		// Case 2: Quantity mismatch (e.g. user partially squared off or partial fill)
		var expectedBrokerQty int
		if pos.Side == "BUY" {
			expectedBrokerQty = pos.Quantity
		} else {
			expectedBrokerQty = -pos.Quantity
		}

		if brokerQty != expectedBrokerQty {
			// Check if side matches
			sameSide := (pos.Side == "BUY" && brokerQty > 0) || (pos.Side == "SELL" && brokerQty < 0)
			if sameSide {
				actualQty := brokerQty
				if actualQty < 0 {
					actualQty = -actualQty
				}
				if actualQty < pos.Quantity {
					tb.logger.Info("[BROKER_SYNC] Quantity reduced on Zerodha. Updating local tracking.", map[string]interface{}{
						"symbol":   pos.Symbol,
						"order_id": orderID,
						"old_qty":  pos.Quantity,
						"new_qty":  actualQty,
					})
					closedPortion := pos.Quantity - actualQty
					tb.riskMgr.UpdatePositionQuantity(orderID, actualQty)
					pos.Quantity = actualQty
					_ = tb.db.SaveOpenPosition(tb.ctx, orderID, pos.Symbol, actualQty, pos.EntryPrice, pos.Side, pos.SLPrice, pos.Strategy, pos.BrokerSLOrderID)
					// If broker SL order is active, replace it for the updated quantity
					if pos.BrokerSLOrderID != "" && pos.BrokerSLOrderID != "RECOVERING" {
						tb.replaceBrokerSLOnPartialExit(orderID, pos, closedPortion)
					}
				}
			} else {
				// Side completely flipped on Zerodha! Close old position tracking
				tb.logger.Warn("[BROKER_SYNC] Position side inverted on Zerodha. Closing tracked position.", map[string]interface{}{
					"symbol":     pos.Symbol,
					"order_id":   orderID,
					"bot_side":   pos.Side,
					"broker_qty": brokerQty,
				})
				if pos.BrokerSLOrderID != "" && pos.BrokerSLOrderID != "RECOVERING" {
					_ = tb.execMgr.CancelOrder(pos.BrokerSLOrderID)
				}
				tb.riskMgr.OnOrderClose(orderID, pos.LatestPrice, pos.Quantity)
				_ = tb.db.CloseOpenPosition(tb.ctx, orderID, pos.LatestPrice)
			}
		}
	}
}

// reconcilePositions checks Zerodha on startup to recover open positions and their SL orders
func (tb *TradingBot) reconcilePositions() {
	if !tb.execMgr.LiveTrading {
		return
	}

	tb.logger.Info("Reconciling open positions and active orders on startup...", nil)

	// Fetch all live positions and orders from Zerodha
	livePositions, err := tb.kiteClient.GetPositions()
	if err != nil {
		tb.logger.Error("Failed to fetch open positions from Zerodha on startup", map[string]interface{}{"error": err.Error()})
		return
	}

	orders, err := tb.kiteClient.GetOrders()
	if err != nil {
		tb.logger.Error("Failed to fetch orders from Zerodha on startup", map[string]interface{}{"error": err.Error()})
		return
	}

	// Map to track active positions on Zerodha
	activePositions := make(map[string]data.Position)
	for _, p := range livePositions.Net {
		if p.Product == "MIS" && p.Quantity != 0 {
			activePositions[p.TradingSymbol] = p
		}
	}

	// Clean up any trigger pending SL orders for symbols that do NOT have active positions.
	// For symbols with active positions, keep their SL orders to recover them.
	pendingSLOrders := make(map[string]data.Order)
	for _, o := range orders {
		if o.Product == "MIS" && (o.Status == "TRIGGER PENDING" || o.Status == "OPEN") && (o.OrderType == "SL" || o.OrderType == "SL-M") {
			if _, hasPosition := activePositions[o.TradingSymbol]; hasPosition {
				pendingSLOrders[o.TradingSymbol] = o
			} else {
				tb.logger.Warn("Cancelling orphaned stop-loss order on startup (no matching open position)", map[string]interface{}{
					"symbol":      o.TradingSymbol,
					"sl_order_id": o.OrderID,
					"status":      o.Status,
				})
				tb.execMgr.CancelOrder(o.OrderID)
			}
		}
	}

	// Recover each active MIS position
	for symbol, p := range activePositions {
		var side string
		var absQty int
		if p.Quantity > 0 {
			side = "BUY"
			absQty = p.Quantity
		} else {
			side = "SELL"
			absQty = -p.Quantity
		}

		tb.logger.Info("Recovering active position on startup", map[string]interface{}{
			"symbol":   symbol,
			"side":     side,
			"quantity": absQty,
		})

		// 1. Determine entry price and entry order ID from today's completed orders
		var entryPrice float64
		var entryOrderID string

		// Search for the latest completed entry order for this symbol today on the same side
		var latestCompletedOrder *data.Order
		for _, o := range orders {
			if o.TradingSymbol == symbol && o.TransactionType == side && o.Status == "COMPLETE" {
				if latestCompletedOrder == nil || o.OrderTimestamp.After(latestCompletedOrder.OrderTimestamp) {
					oCopy := o
					latestCompletedOrder = &oCopy
				}
			}
		}

		if latestCompletedOrder != nil {
			entryPrice = latestCompletedOrder.AveragePrice
			entryOrderID = latestCompletedOrder.OrderID
		} else {
			entryPrice = p.AveragePrice
			entryOrderID = "recovery-" + symbol
		}

		// Determine original strategy from DB positions table, strategy watchlists, or order tag
		var strategy string
		if tb.db != nil {
			if dbStrat, err := tb.db.GetPositionStrategy(tb.ctx, entryOrderID, symbol); err == nil && dbStrat != "" {
				strategy = dbStrat
			}
		}

		if strategy == "" && latestCompletedOrder != nil && latestCompletedOrder.Tag != "" {
			strategy = latestCompletedOrder.Tag
		}

		if strategy == "" {
			tb.watchlistMutex.RLock()
			for stratName, wList := range tb.strategyWatchlists {
				if _, ok := wList[symbol]; ok {
					strategy = stratName
					break
				}
			}
			tb.watchlistMutex.RUnlock()
		}

		if strategy == "" {
			strategy = "MANUAL" // If trade does not exist in DB and has no bot strategy tag, classify as MANUAL
		}

		// 2. Check if there is an active SL order for this symbol on Zerodha
		var slPrice float64
		var slOrderID string

		if slOrder, exists := pendingSLOrders[symbol]; exists {
			slOrderID = slOrder.OrderID
			if slOrder.TriggerPrice > 0 {
				slPrice = slOrder.TriggerPrice
			} else {
				slPrice = slOrder.Price
			}
			tb.logger.Info("Recovered active broker stop-loss order", map[string]interface{}{
				"symbol":      symbol,
				"sl_order_id": slOrderID,
				"sl_price":    slPrice,
			})
		} else {
			// No SL order found! We must calculate and place a new one!
			tb.logger.Warn("No active stop-loss order found on Zerodha for recovered position. Calculating and placing new SL.", map[string]interface{}{
				"symbol": symbol,
			})

			// Calculate SL Price (1.5% risk fallback)
			if side == "BUY" {
				slPrice = entryPrice * 0.985
			} else {
				slPrice = entryPrice * 1.015
			}

			// Get tick size and round
			tickSize := tb.getTickSize(symbol)
			slPrice = math.Round(slPrice/tickSize) * tickSize

			tb.logger.Info("Calculated new stop-loss price", map[string]interface{}{
				"symbol":   symbol,
				"sl_price": slPrice,
			})
		}

		// Get token from security master
		token, err := tb.securityMaster.GetInstrumentToken(symbol)
		if err != nil {
			tb.watchlistMutex.RLock()
			token = tb.watchlist[symbol]
			tb.watchlistMutex.RUnlock()
		}

		// Target 1 fallback (3% reward target)
		var target1Price float64
		if side == "BUY" {
			target1Price = entryPrice * 1.03
		} else {
			target1Price = entryPrice * 0.97
		}

		// Determine position creation time based on the entry order's timestamp
		recoveredCreatedAt := time.Now()
		if latestCompletedOrder != nil {
			recoveredCreatedAt = latestCompletedOrder.OrderTimestamp
		}

		// Register recovered entry order in execution manager so status tracker can poll it
		tb.execMgr.RegisterRecoveredOrder(entryOrderID, symbol, side, absQty, string(tb.cfg.DefaultOrderType))

		// Add to risk manager openPositions map so the bot tracks it in memory
		tb.riskMgr.AddOpenPosition(entryOrderID, symbol, token, absQty, entryPrice, side, slPrice, strategy, target1Price, recoveredCreatedAt)

		// Set BrokerSLOrderID immediately BEFORE starting status tracking to prevent ticker loop race condition
		if slOrderID != "" {
			tb.riskMgr.SetBrokerSLOrderID(entryOrderID, slOrderID)
		} else {
			// Placeholder to prevent ticker loop fallback exit while new SL order is being calculated
			tb.riskMgr.SetBrokerSLOrderID(entryOrderID, "RECOVERING")
		}

		_ = tb.db.SaveOpenPosition(tb.ctx, entryOrderID, symbol, absQty, entryPrice, side, slPrice, strategy, slOrderID)

		// Start tracking the entry order status
		tb.statusTracker.StartTracking(entryOrderID)

		// If we recovered or created an SL order ID, track it in risk manager
		if slOrderID != "" {
			// Register and start tracking the recovered SL order
			var slTxnType string
			if side == "BUY" {
				slTxnType = "SELL"
			} else {
				slTxnType = "BUY"
			}
			tb.execMgr.RegisterRecoveredOrder(slOrderID, symbol, slTxnType, absQty, "SL")
			tb.statusTracker.StartTracking(slOrderID)
		} else {
			// Place the broker stop-loss order now!
			posMap := tb.riskMgr.GetOpenPositions()
			if recoveredPos, ok := posMap[entryOrderID]; ok {
				tb.placeBrokerStopLoss(entryOrderID, recoveredPos)
			}
		}
	}
}

// placeBrokerStopLoss places a hard stop-loss order at the broker (Zerodha)
func (tb *TradingBot) placeBrokerStopLoss(orderID string, pos *risk.Position) {
	if !tb.execMgr.LiveTrading || pos.BrokerSLOrderID != "" {
		return
	}

	var useBrokerSL bool
	if pos.Strategy == "VANDE_BHARAT" {
		useBrokerSL = tb.cfg.VBUseBrokerSL
	} else if pos.Strategy == "FAKE_BREAKOUT" {
		useBrokerSL = tb.cfg.FBUseBrokerSL
	} else if pos.Strategy == "VANDE_BHARAT_TRAP" {
		useBrokerSL = tb.cfg.VBTUseBrokerSL
	} else if pos.Strategy == "EMAS5_BREAKOUT" {
		useBrokerSL = tb.cfg.ES5UseBrokerSL
	} else if pos.Strategy == "MANUAL" {
		useBrokerSL = tb.cfg.ManualTradeUseBrokerSL
	} else {
		useBrokerSL = tb.cfg.LVUseBrokerSL
	}

	if !useBrokerSL {
		return
	}

	var txnType string
	if pos.Side == "BUY" {
		txnType = "SELL"
	} else {
		txnType = "BUY"
	}

	tickSize := tb.getTickSize(pos.Symbol)
	// Round trigger price (SLPrice) to tick size and trim float noise
	pos.SLPrice = risk.RoundTick(pos.SLPrice, tickSize)

	var limitPrice float64
	if txnType == "SELL" {
		limitPrice = risk.RoundTick(pos.SLPrice*0.99, tickSize)
	} else {
		limitPrice = risk.RoundTick(pos.SLPrice*1.01, tickSize)
	}

	slOrderReq := execution.OrderRequest{
		TradingSymbol:   pos.Symbol,
		Exchange:        "NSE",
		Quantity:        pos.Quantity,
		TransactionType: txnType,
		OrderType:       execution.OrderTypeSL,
		TriggerPrice:    &pos.SLPrice,
		Price:           &limitPrice,
		Product:         "MIS",
		Validity:        "DAY",
		Strategy:        pos.Strategy,
	}

	slOrderID, err := tb.execMgr.PlaceOrder(slOrderReq)
	if err != nil {
		tb.logger.Error("Failed to place broker-side stop-loss order", map[string]interface{}{
			"symbol":   pos.Symbol,
			"error":    err.Error(),
			"strategy": pos.Strategy,
		})
	} else {
		tb.logger.Info("Placed broker-side stop-loss order successfully", map[string]interface{}{
			"symbol":        pos.Symbol,
			"sl_order_id":   slOrderID,
			"trigger_price": pos.SLPrice,
			"strategy":      pos.Strategy,
		})
		tb.riskMgr.SetBrokerSLDetails(orderID, slOrderID, pos.SLPrice)
		_ = tb.db.UpdateBrokerSLOrderID(tb.ctx, orderID, slOrderID)
		tb.statusTracker.StartTracking(slOrderID)
	}
}

// replaceBrokerSLOnPartialExit cancels the old SL and places a new SL for the remaining qty
func (tb *TradingBot) replaceBrokerSLOnPartialExit(orderID string, pos *risk.Position, closeQty int) {
	if !tb.execMgr.LiveTrading {
		return
	}

	// Fetch fresh position state from RiskManager
	freshPos := tb.riskMgr.GetPosition(orderID)
	if freshPos == nil || freshPos.Quantity <= 0 || freshPos.BrokerSLOrderID == "" {
		return
	}

	tickSize := tb.getTickSize(freshPos.Symbol)
	roundedSL := risk.RoundTick(freshPos.SLPrice, tickSize)

	// Skip unnecessary broker SL order replacement if price tick has not changed and no qty was closed
	if closeQty == 0 && freshPos.LastPlacedSLPrice == roundedSL {
		return
	}

	tb.logger.Info("Cancelling old broker stop-loss for SL update...", map[string]interface{}{
		"sl_order_id":   freshPos.BrokerSLOrderID,
		"old_placed_sl": freshPos.LastPlacedSLPrice,
		"new_sl":        roundedSL,
		"qty":           freshPos.Quantity,
	})
	tb.execMgr.CancelOrder(freshPos.BrokerSLOrderID)

	// Re-verify position existence and quantity
	updatedPos := tb.riskMgr.GetPosition(orderID)
	if updatedPos == nil || updatedPos.Quantity <= 0 {
		return
	}

	// Place new stop-loss order at Zerodha for the remaining quantity
	var exitTxnType string
	if updatedPos.Side == "BUY" {
		exitTxnType = "SELL"
	} else {
		exitTxnType = "BUY"
	}

	// Round trigger price to tick size and trim float noise
	updatedPos.SLPrice = roundedSL

	var limitPrice float64
	if exitTxnType == "SELL" {
		limitPrice = risk.RoundTick(updatedPos.SLPrice*0.99, tickSize)
	} else {
		limitPrice = risk.RoundTick(updatedPos.SLPrice*1.01, tickSize)
	}

	slOrderReq := execution.OrderRequest{
		TradingSymbol:   updatedPos.Symbol,
		Exchange:        "NSE",
		Quantity:        updatedPos.Quantity,
		TransactionType: exitTxnType,
		OrderType:       execution.OrderTypeSL,
		TriggerPrice:    &updatedPos.SLPrice,
		Price:           &limitPrice,
		Product:         "MIS",
		Validity:        "DAY",
		Strategy:        updatedPos.Strategy,
	}

	slOrderID, err := tb.execMgr.PlaceOrder(slOrderReq)
	if err != nil {
		tb.logger.Error("Failed to place broker-side stop-loss order", map[string]interface{}{
			"symbol":   updatedPos.Symbol,
			"error":    err.Error(),
			"strategy": updatedPos.Strategy,
		})
	} else {
		tb.logger.Info("Successfully replaced broker-side stop-loss order", map[string]interface{}{
			"symbol":        updatedPos.Symbol,
			"sl_order_id":   slOrderID,
			"trigger_price": updatedPos.SLPrice,
			"strategy":      updatedPos.Strategy,
			"qty":           updatedPos.Quantity,
		})
		tb.riskMgr.SetBrokerSLDetails(orderID, slOrderID, roundedSL)
		_ = tb.db.UpdateBrokerSLOrderID(tb.ctx, orderID, slOrderID)
		_ = tb.db.SaveOpenPosition(tb.ctx, orderID, updatedPos.Symbol, updatedPos.Quantity, updatedPos.EntryPrice, updatedPos.Side, updatedPos.SLPrice, updatedPos.Strategy, slOrderID)
		tb.statusTracker.StartTracking(slOrderID)
	}
}

// restoreTriggeredTrades queries today's trades from the database and populates the strategies' triggeredTrades maps
func (tb *TradingBot) restoreTriggeredTrades() {
	history, err := tb.db.GetAllTradesHistory(tb.ctx)
	if err != nil {
		tb.logger.Error("Failed to fetch trades history for startup recovery", map[string]interface{}{"error": err.Error()})
		return
	}

	todayStr := time.Now().In(data.ISTLocation).Format("2006-01-02")

	count := 0
	for _, tr := range history {
		if tr.CreatedAt.In(data.ISTLocation).Format("2006-01-02") == todayStr {
			for _, strat := range tb.activeStrategies {
				if strat.Name() == tr.Strategy {
					strat.RestoreTriggeredTrade(tr.Symbol)
					count++
				}
			}
		}
	}
	tb.logger.Info("Restored triggered trades state on startup", map[string]interface{}{"count": count})

	// Also restore watchlistDirections on startup
	tb.watchlistDirectionsMutex.Lock()
	tb.watchlistDirections = make(map[string]string)
	for _, ruleSet := range []string{"STANDARD", "ADJUSTED"} {
		results, err := tb.db.GetPreSelectionResults(todayStr, ruleSet)
		if err == nil {
			for _, res := range results {
				tb.watchlistDirections[res.Ticker] = res.PredictedDirection
			}
		}
	}
	tb.watchlistDirectionsMutex.Unlock()
	tb.logger.Info("Restored watchlist directions on startup", map[string]interface{}{"count": len(tb.watchlistDirections)})

	// Also restore tb.globalBias on startup if market breadth was already logged today
	advances, declines, neutrals, latestBias, errBreadth := tb.db.GetLatestMarketBreadth(tb.ctx)
	if errBreadth == nil && latestBias != "" {
		tb.globalBias = latestBias
		tb.logger.Info("Restored global market bias on startup", map[string]interface{}{
			"bias":     tb.globalBias,
			"advances": advances,
			"declines": declines,
			"neutrals": neutrals,
		})
	}
}

// SyncManualTradesFromBroker polls Zerodha for any manual MIS trades, attaches the configured Risk-Reward strategy,
// verifies/places broker stop-loss orders, and registers them into risk management and ticker streaming.
func (tb *TradingBot) SyncManualTradesFromBroker() (int, error) {
	if !tb.execMgr.LiveTrading || tb.kiteClient == nil {
		return 0, nil
	}

	tb.manualSyncMutex.Lock()
	defer tb.manualSyncMutex.Unlock()

	tb.logger.Info("[MANUAL_SYNC] Polling Zerodha for manual trades...", nil)

	livePositions, err := tb.kiteClient.GetPositions()
	if err != nil {
		tb.logger.Error("[MANUAL_SYNC] Failed to fetch positions from Zerodha", map[string]interface{}{"error": err.Error()})
		return 0, fmt.Errorf("failed to fetch positions from Zerodha: %w", err)
	}

	orders, err := tb.kiteClient.GetOrders()
	if err != nil {
		tb.logger.Error("[MANUAL_SYNC] Failed to fetch orders from Zerodha", map[string]interface{}{"error": err.Error()})
		return 0, fmt.Errorf("failed to fetch orders from Zerodha: %w", err)
	}

	// 1. Map active MIS positions on Zerodha
	activePositions := make(map[string]data.Position)
	for _, p := range livePositions.Net {
		if p.Product == "MIS" && p.Quantity != 0 {
			activePositions[p.TradingSymbol] = p
		}
	}

	// 2. Identify currently tracked open positions in RiskManager
	openPositions := tb.riskMgr.GetOpenPositions()
	trackedSymbols := make(map[string]bool)
	for _, pos := range openPositions {
		trackedSymbols[pos.Symbol] = true
	}

	// 3. Map active pending broker stop-loss orders
	pendingSLOrders := make(map[string]data.Order)
	for _, o := range orders {
		if o.Product == "MIS" && (o.Status == "TRIGGER PENDING" || o.Status == "OPEN") && (o.OrderType == "SL" || o.OrderType == "SL-M") {
			pendingSLOrders[o.TradingSymbol] = o
		}
	}

	newTradesCount := 0

	// 4. Attach each untracked manual position
	for symbol, p := range activePositions {
		if trackedSymbols[symbol] {
			continue // Already managed
		}

		var side string
		var absQty int
		if p.Quantity > 0 {
			side = "BUY"
			absQty = p.Quantity
		} else {
			side = "SELL"
			absQty = -p.Quantity
		}

		tb.logger.Info("[MANUAL_SYNC] New manual trade detected on Zerodha! Attaching risk management...", map[string]interface{}{
			"symbol":   symbol,
			"side":     side,
			"quantity": absQty,
		})

		// Determine entry price and entry order ID from today's completed orders
		var entryPrice float64
		var entryOrderID string
		var entryTime time.Time

		var latestCompletedOrder *data.Order
		for _, o := range orders {
			if o.TradingSymbol == symbol && o.TransactionType == side && o.Status == "COMPLETE" {
				if latestCompletedOrder == nil || o.OrderTimestamp.After(latestCompletedOrder.OrderTimestamp) {
					oCopy := o
					latestCompletedOrder = &oCopy
				}
			}
		}

		if latestCompletedOrder != nil {
			var isBotOrder bool
			var botStrategy string
			if tb.db != nil {
				if s, err := tb.db.GetOrderStrategy(latestCompletedOrder.OrderID); err == nil && s != "" && s != "MANUAL" {
					isBotOrder = true
					botStrategy = s
				}
			}
			if latestCompletedOrder.Tag != "" && latestCompletedOrder.Tag != "MANUAL" {
				isBotOrder = true
				if botStrategy == "" {
					botStrategy = latestCompletedOrder.Tag
				}
			}

			if isBotOrder {
				tb.logger.Warn("[MANUAL_SYNC] Skipping order - belongs to automated bot strategy, not a manual trade", map[string]interface{}{
					"symbol":   symbol,
					"order_id": latestCompletedOrder.OrderID,
					"strategy": botStrategy,
				})
				continue
			}

			entryPrice = latestCompletedOrder.AveragePrice
			entryOrderID = latestCompletedOrder.OrderID
			entryTime = latestCompletedOrder.OrderTimestamp
		} else {
			entryPrice = p.AveragePrice
			entryOrderID = fmt.Sprintf("manual-%s-%d", symbol, time.Now().Unix())
			entryTime = time.Now()
		}

		// Check if symbol has instrument token
		token, errTok := tb.securityMaster.GetInstrumentToken(symbol)
		if errTok != nil || token <= 0 {
			token, errTok = tb.db.ResolveSymbolToken(tb.ctx, symbol)
		}
		if errTok != nil || token <= 0 {
			token, errTok = tb.securityMaster.ResolveAndAddSymbol(tb.ctx, symbol)
		}
		if token <= 0 {
			tb.watchlistMutex.RLock()
			token = tb.watchlist[symbol]
			tb.watchlistMutex.RUnlock()
		}

		// Ensure token is in watchlist and subscribed to live WebSocket ticker
		if token > 0 {
			tb.watchlistMutex.Lock()
			tb.watchlist[symbol] = token
			tb.watchlistMutex.Unlock()
			if tb.ticker != nil {
				tb.ticker.Subscribe([]int64{token})
			}
		}

		// Check if an existing stop-loss order exists on Zerodha
		var slPrice float64
		var slOrderID string
		tickSize := tb.getTickSize(symbol)

		if slOrder, exists := pendingSLOrders[symbol]; exists {
			slOrderID = slOrder.OrderID
			if slOrder.TriggerPrice > 0 {
				slPrice = slOrder.TriggerPrice
			} else {
				slPrice = slOrder.Price
			}
			tb.logger.Info("[MANUAL_SYNC] Linked existing broker stop-loss order", map[string]interface{}{
				"symbol":      symbol,
				"sl_order_id": slOrderID,
				"sl_price":    slPrice,
			})
		} else {
			// Calculate default Stop-Loss Price
			slPct := tb.cfg.ManualTradeDefaultSLPct
			if slPct <= 0 {
				slPct = 1.5
			}
			if side == "BUY" {
				slPrice = entryPrice * (1.0 - slPct/100.0)
			} else {
				slPrice = entryPrice * (1.0 + slPct/100.0)
			}
			slPrice = risk.RoundTick(slPrice, tickSize)

			tb.logger.Info("[MANUAL_SYNC] Calculated default stop-loss price for manual trade", map[string]interface{}{
				"symbol":   symbol,
				"sl_price": slPrice,
				"sl_pct":   slPct,
			})
		}

		// Calculate Target 1 Price based on attached Risk-Reward strategy
		rrRatio := tb.cfg.ManualTradeRRRatio
		if rrRatio <= 0 {
			rrRatio = 2.0
		}
		riskDistance := math.Abs(entryPrice - slPrice)
		if riskDistance <= 0 {
			riskDistance = entryPrice * 0.015
		}

		var target1Price float64
		if side == "BUY" {
			target1Price = risk.RoundTick(entryPrice+(rrRatio*riskDistance), tickSize)
		} else {
			target1Price = risk.RoundTick(entryPrice-(rrRatio*riskDistance), tickSize)
		}

		// Register recovered entry order in execution manager
		tb.execMgr.RegisterRecoveredOrder(entryOrderID, symbol, side, absQty, string(tb.cfg.DefaultOrderType))

		// Add to risk manager openPositions map with Strategy = "MANUAL"
		tb.riskMgr.AddOpenPosition(entryOrderID, symbol, token, absQty, entryPrice, side, slPrice, "MANUAL", target1Price, entryTime)

		if slOrderID != "" {
			tb.riskMgr.SetBrokerSLOrderID(entryOrderID, slOrderID)
			var slTxnType string
			if side == "BUY" {
				slTxnType = "SELL"
			} else {
				slTxnType = "BUY"
			}
			tb.execMgr.RegisterRecoveredOrder(slOrderID, symbol, slTxnType, absQty, "SL")
			tb.statusTracker.StartTracking(slOrderID)
		} else {
			tb.riskMgr.SetBrokerSLOrderID(entryOrderID, "RECOVERING")
		}

		_ = tb.db.SaveOpenPosition(tb.ctx, entryOrderID, symbol, absQty, entryPrice, side, slPrice, "MANUAL", slOrderID)
		tb.statusTracker.StartTracking(entryOrderID)

		if slOrderID == "" && tb.cfg.ManualTradeUseBrokerSL {
			posMap := tb.riskMgr.GetOpenPositions()
			if registeredPos, ok := posMap[entryOrderID]; ok {
				registeredPos.BrokerSLOrderID = ""
				tb.placeBrokerStopLoss(entryOrderID, registeredPos)
			}
		}

		newTradesCount++
		tb.logger.Info("[MANUAL_SYNC] Manual trade successfully registered and managed!", map[string]interface{}{
			"symbol":      symbol,
			"order_id":    entryOrderID,
			"side":        side,
			"qty":         absQty,
			"entry":       entryPrice,
			"sl":          slPrice,
			"target1":     target1Price,
			"rr_strategy": tb.strategyRRMap["MANUAL"],
		})
	}

	return newTradesCount, nil
}

// isBreakoutOverextended checks if the current LTP has spiked beyond the setup candle boundary
// by more than EntryLimitMaxChaseTicks in RETEST_BAND mode.
func (tb *TradingBot) isBreakoutOverextended(action string, ltp float64, setupHigh float64, setupLow float64, tickSize float64) (bool, string, float64) {
	entryMode := strings.ToUpper(strings.TrimSpace(tb.cfg.EntryLimitMode))
	if entryMode != "RETEST_BAND" || tb.cfg.EntryLimitMaxChaseTicks <= 0 {
		return false, "", 0
	}
	if tickSize <= 0 {
		tickSize = 0.05
	}
	if action == "BUY" && setupHigh > 0 {
		maxAllowedPrice := setupHigh + float64(tb.cfg.EntryLimitMaxChaseTicks)*tickSize
		if ltp > maxAllowedPrice {
			reason := fmt.Sprintf("LTP ₹%.2f exceeded max chase ceiling ₹%.2f (+%d ticks above candle high ₹%.2f)", ltp, maxAllowedPrice, tb.cfg.EntryLimitMaxChaseTicks, setupHigh)
			return true, reason, maxAllowedPrice
		}
	} else if action == "SELL" && setupLow > 0 {
		minAllowedPrice := setupLow - float64(tb.cfg.EntryLimitMaxChaseTicks)*tickSize
		if ltp < minAllowedPrice {
			reason := fmt.Sprintf("LTP ₹%.2f exceeded max chase ceiling ₹%.2f (-%d ticks below candle low ₹%.2f)", ltp, minAllowedPrice, tb.cfg.EntryLimitMaxChaseTicks, setupLow)
			return true, reason, minAllowedPrice
		}
	}
	return false, "", 0
}

// calculatePlannedEntryPrice computes the order type, planned entry price, and limit price pointer
// based on default order type, anchor mode (CONFIRMATION_CANDLE vs LTP), tick offset, and tick size.
func (tb *TradingBot) calculatePlannedEntryPrice(symbol string, action string, ltp float64, setupHigh float64, setupLow float64) (execution.OrderType, float64, *float64) {
	entryMode := strings.ToUpper(strings.TrimSpace(tb.cfg.EntryLimitMode))
	if entryMode == "" {
		if tb.cfg.DefaultOrderType == "LIMIT" {
			entryMode = "DIRECT_LIMIT"
		} else {
			entryMode = "MARKET"
		}
	}

	orderType := execution.OrderTypeMarket
	if entryMode == "DIRECT_LIMIT" || entryMode == "RETEST_BAND" {
		orderType = execution.OrderTypeLimit
	} else if entryMode == "MARKET" {
		orderType = execution.OrderTypeMarket
	} else if tb.cfg.DefaultOrderType == "LIMIT" {
		orderType = execution.OrderTypeLimit
	}

	tickSize := tb.getTickSize(symbol)
	if tickSize <= 0 {
		tickSize = 0.05
	}

	plannedEntryPrice := ltp
	var limitPrice *float64

	if orderType == execution.OrderTypeLimit {
		anchor := strings.ToUpper(strings.TrimSpace(tb.cfg.EntryLimitAnchor))
		if anchor == "" {
			anchor = "CONFIRMATION_CANDLE"
		}
		offsetTicks := tb.cfg.EntryLimitOffsetTicks

		var targetPrice float64
		if (entryMode == "RETEST_BAND" || anchor == "CONFIRMATION_CANDLE") && (action == "BUY" && setupHigh > 0 || action == "SELL" && setupLow > 0) {
			if action == "BUY" {
				targetPrice = setupHigh + float64(offsetTicks)*tickSize
			} else {
				targetPrice = setupLow - float64(offsetTicks)*tickSize
			}
		} else if anchor == "LTP" && offsetTicks != 0 {
			if action == "BUY" {
				targetPrice = ltp + float64(offsetTicks)*tickSize
			} else {
				targetPrice = ltp - float64(offsetTicks)*tickSize
			}
		} else {
			// Fallback: Marketable Limit with LimitBufferPct
			limitBuf := tb.cfg.LimitBufferPct
			if limitBuf <= 0 {
				limitBuf = 0.2
			}
			if action == "BUY" {
				targetPrice = ltp * (1.0 + limitBuf/100.0)
			} else {
				targetPrice = ltp * (1.0 - limitBuf/100.0)
			}
		}

		// Sanity checks: targetPrice must be positive and within reasonable bounds of LTP (within 5%)
		if targetPrice <= 0 || math.Abs(targetPrice-ltp)/ltp > 0.05 {
			if tb.logger != nil {
				tb.logger.Warn("Computed limit price out of bounds, falling back to LTP with buffer", map[string]interface{}{
					"symbol":       symbol,
					"ltp":          ltp,
					"target_price": targetPrice,
					"anchor":       anchor,
				})
			}
			limitBuf := tb.cfg.LimitBufferPct
			if limitBuf <= 0 {
				limitBuf = 0.2
			}
			if action == "BUY" {
				targetPrice = ltp * (1.0 + limitBuf/100.0)
			} else {
				targetPrice = ltp * (1.0 - limitBuf/100.0)
			}
		}

		limPrice := risk.RoundTick(targetPrice, tickSize)
		limitPrice = &limPrice
		plannedEntryPrice = limPrice
	}

	return orderType, plannedEntryPrice, limitPrice
}
