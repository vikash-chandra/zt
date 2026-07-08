package main

import (
	"fmt"
	"math"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"zerodha-trading/execution"
	"zerodha-trading/risk"
)

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
					if tb.globalBias != "NO_TRADE" && tb.globalBias != "" {
						nowIST := time.Now().In(loc)

						for _, strat := range tb.activeStrategies {
							var endH, endM int
							var errTime error
							if strat.Name() == "VANDE_BHARAT" {
								endH, endM, errTime = parseTimeHM(tb.cfg.VBTradeEndTime)
								if errTime != nil {
									endH, endM = 11, 0
								}
							} else {
								endH, endM, errTime = parseTimeHM(tb.cfg.LVTradeEndTime)
								if errTime != nil {
									endH, endM = 10, 45
								}
							}

							endBoundary := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), endH, endM, 0, 0, loc)

							if nowIST.After(endBoundary) {
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

								// Compute margin per share using pre-cached leverage
								leverage := tb.getLeverage(symbol)
								marginPerShare := tick.LTP / leverage

								var setupHigh, setupLow float64
								setup := strat.GetSetupCandle(symbol)
								if setup != nil {
									setupHigh = setup.High
									setupLow = setup.Low
								}

								var bufferPct float64
								if strat.Name() == "VANDE_BHARAT" {
									bufferPct = tb.cfg.VBSLBufferPct
								} else {
									bufferPct = tb.cfg.SLBufferPct
								}

								profile := tb.rrCalculator.CalculateProfile(tick.LTP, signal.Action, setupHigh, setupLow, bufferPct, tb.cfg.MaxCapitalPerTrade, marginPerShare, tb.cfg.RiskRewardRatio)

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
										OrderType:       execution.OrderType(tb.cfg.DefaultOrderType),
										Product:         "MIS",
										Validity:        "DAY",
										Strategy:        strat.Name(),
									}
									if orderReq.OrderType == execution.OrderTypeLimit {
										orderReq.Price = &tick.LTP
									}

									orderID, err := tb.execMgr.PlaceOrder(orderReq)
									if err != nil {
										tb.logger.Error("Failed to place breakout order", map[string]interface{}{"error": err.Error(), "symbol": symbol, "strategy": strat.Name()})
									} else {
										tb.riskMgr.AddOpenPosition(orderID, symbol, token, profile.Quantity, tick.LTP, signal.Action, profile.StopLoss, strat.Name(), profile.Target1)
										_ = tb.db.SaveOpenPosition(tb.ctx, orderID, symbol, profile.Quantity, tick.LTP, signal.Action, profile.StopLoss, strat.Name(), "")
										if !tb.execMgr.LiveTrading {
											tb.execMgr.SimulateOrderFill(orderID, profile.Quantity, tick.LTP)
										}
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
				// Cancel pending entry orders if they did not fill within the next candle interval
				orderStatus := tb.statusTracker.GetCachedStatus(orderID)
				if orderStatus != nil {
					isPending := orderStatus.Status != "COMPLETE" && orderStatus.Status != "CANCELLED" && orderStatus.Status != "REJECTED"
					if isPending {
						if time.Since(pos.CreatedAt) >= time.Duration(tb.cfg.CandleIntervalSec)*time.Second {
							tb.logger.Warn("Cancelling pending entry order: did not complete in next candle window",
								map[string]interface{}{"order_id": orderID, "symbol": pos.Symbol, "status": orderStatus.Status})
							tb.execMgr.CancelOrder(orderID)
							if orderStatus.FilledQuantity == 0 {
								tb.riskMgr.OnOrderClose(orderID, 0, 0)
							}
							continue
						}
					} else if orderStatus.Status == "COMPLETE" {
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

							// 2. Place stop-loss order at Zerodha for the updated quantity
							tb.placeBrokerStopLoss(orderID, pos)
						}
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
						tb.riskMgr.OnOrderClose(orderID, slStatus.AveragePrice, pos.Quantity)
						_ = tb.db.CloseOpenPosition(tb.ctx, orderID, slStatus.AveragePrice)
						continue
					}
				}

				// Check risk limits (Stop-Loss and Target 1 partial exits)
				action := tb.riskMgr.CheckTrailingSL(orderID, currentPrice)
				if action == "CLOSE" {
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
						}
						if orderReq.OrderType == execution.OrderTypeLimit {
							orderReq.Price = &currentPrice
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
						}
					} else {
						tb.execMgr.CancelOrder(orderID)
						tb.riskMgr.OnOrderClose(orderID, currentPrice, pos.Quantity)
						_ = tb.db.CloseOpenPosition(tb.ctx, orderID, currentPrice)
					}
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
							OrderType:       execution.OrderType(tb.cfg.DefaultOrderType),
							Product:         "MIS",
							Validity:        "DAY",
						}
						if orderReq.OrderType == execution.OrderTypeLimit {
							orderReq.Price = &currentPrice
						}

						exitOrderID, err := tb.execMgr.PlaceOrder(orderReq)
						if err != nil {
							tb.logger.Error("Failed to place partial exit order", map[string]interface{}{"error": err.Error(), "symbol": pos.Symbol, "strategy": pos.Strategy})
						} else {
							tb.logger.Info("Target 1 partial exit order placed", map[string]interface{}{
								"order_id": exitOrderID,
								"symbol":   pos.Symbol,
								"qty":      closeQty,
							})
							if !tb.execMgr.LiveTrading {
								tb.execMgr.SimulateOrderFill(exitOrderID, closeQty, currentPrice)
							} else {
								tb.statusTracker.StartTracking(exitOrderID)
							}
							tb.riskMgr.RecordPartialExit(orderID, currentPrice, closeQty)

							// Re-evaluate broker stop-loss for the remaining quantity
							tb.replaceBrokerSLOnPartialExit(orderID, pos, closeQty)
						}
					}
				}

				// Update current price
				tb.riskMgr.UpdatePositionPrice(orderID, currentPrice)
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
	activePositions := make(map[string]kiteconnect.Position)
	for _, p := range livePositions.Net {
		if p.Product == "MIS" && p.Quantity != 0 {
			activePositions[p.Tradingsymbol] = p
		}
	}

	// Clean up any trigger pending SL orders for symbols that do NOT have active positions.
	// For symbols with active positions, keep their SL orders to recover them.
	pendingSLOrders := make(map[string]kiteconnect.Order)
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
		var strategy string = "LOW_VOLUME" // default fallback

		// Check Watchlist to map strategy
		tb.watchlistMutex.RLock()
		for sym := range tb.watchlist {
			if sym == symbol {
				strategy = "VANDE_BHARAT" // If in watchlist, it's Vande Bharat
				break
			}
		}
		tb.watchlistMutex.RUnlock()

		// Search for the latest completed entry order for this symbol today on the same side
		var latestCompletedOrder *kiteconnect.Order
		for _, o := range orders {
			if o.TradingSymbol == symbol && o.TransactionType == side && o.Status == "COMPLETE" {
				if latestCompletedOrder == nil || o.OrderTimestamp.Time.After(latestCompletedOrder.OrderTimestamp.Time) {
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
			token = tb.watchlist[symbol]
		}

		// Target 1 fallback (3% reward target)
		var target1Price float64
		if side == "BUY" {
			target1Price = entryPrice * 1.03
		} else {
			target1Price = entryPrice * 0.97
		}

		// Add to risk manager openPositions map so the bot tracks it in memory
		tb.riskMgr.AddOpenPosition(entryOrderID, symbol, token, absQty, entryPrice, side, slPrice, strategy, target1Price)
		_ = tb.db.SaveOpenPosition(tb.ctx, entryOrderID, symbol, absQty, entryPrice, side, slPrice, strategy, slOrderID)

		// Start tracking the entry order status
		tb.statusTracker.StartTracking(entryOrderID)

		// If we recovered or created an SL order ID, track it in risk manager
		if slOrderID != "" {
			tb.riskMgr.SetBrokerSLOrderID(entryOrderID, slOrderID)
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
	// Round trigger price (SLPrice) to tick size
	pos.SLPrice = math.Round(pos.SLPrice/tickSize) * tickSize

	var limitPrice float64
	if txnType == "SELL" {
		limitPrice = math.Round((pos.SLPrice*0.99)/tickSize) * tickSize
	} else {
		limitPrice = math.Round((pos.SLPrice*1.01)/tickSize) * tickSize
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
		tb.riskMgr.SetBrokerSLOrderID(orderID, slOrderID)
		_ = tb.db.UpdateBrokerSLOrderID(tb.ctx, orderID, slOrderID)
		tb.statusTracker.StartTracking(slOrderID)
	}
}

// replaceBrokerSLOnPartialExit cancels the old SL and places a new SL for the remaining qty
func (tb *TradingBot) replaceBrokerSLOnPartialExit(orderID string, pos *risk.Position, closeQty int) {
	if !tb.execMgr.LiveTrading || pos.BrokerSLOrderID == "" {
		return
	}

	tb.logger.Info("Cancelling old broker stop-loss after partial exit...", map[string]interface{}{"sl_order_id": pos.BrokerSLOrderID})
	tb.execMgr.CancelOrder(pos.BrokerSLOrderID)

	// Fetch updated position state (with reduced quantity and trailed SL price)
	updatedPositions := tb.riskMgr.GetOpenPositions()
	updatedPos, exists := updatedPositions[orderID]
	if !exists || updatedPos.Quantity <= 0 {
		return
	}

	// Place new stop-loss order at Zerodha for the remaining quantity
	var exitTxnType string
	if updatedPos.Side == "BUY" {
		exitTxnType = "SELL"
	} else {
		exitTxnType = "BUY"
	}

	tickSize := tb.getTickSize(updatedPos.Symbol)
	// Round trigger price to tick size
	updatedPos.SLPrice = math.Round(updatedPos.SLPrice/tickSize) * tickSize

	var limitPrice float64
	if exitTxnType == "SELL" {
		limitPrice = math.Round((updatedPos.SLPrice*0.99)/tickSize) * tickSize
	} else {
		limitPrice = math.Round((updatedPos.SLPrice*1.01)/tickSize) * tickSize
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
		tb.logger.Error("Failed to replace broker-side stop-loss order after partial exit", map[string]interface{}{
			"symbol":   updatedPos.Symbol,
			"error":    err.Error(),
			"strategy": updatedPos.Strategy,
		})
		tb.riskMgr.SetBrokerSLOrderID(orderID, "")
		_ = tb.db.UpdateBrokerSLOrderID(tb.ctx, orderID, "")
	} else {
		tb.logger.Info("Successfully replaced broker-side stop-loss order after partial exit", map[string]interface{}{
			"symbol":        updatedPos.Symbol,
			"sl_order_id":   slOrderID,
			"trigger_price": updatedPos.SLPrice,
			"strategy":      updatedPos.Strategy,
		})
		tb.riskMgr.SetBrokerSLOrderID(orderID, slOrderID)
		_ = tb.db.UpdateBrokerSLOrderID(tb.ctx, orderID, slOrderID)
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

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}
	todayStr := time.Now().In(loc).Format("2006-01-02")

	count := 0
	for _, tr := range history {
		if tr.CreatedAt.In(loc).Format("2006-01-02") == todayStr {
			for _, strat := range tb.activeStrategies {
				if strat.Name() == tr.Strategy {
					strat.RestoreTriggeredTrade(tr.Symbol)
					count++
				}
			}
		}
	}
	tb.logger.Info("Restored triggered trades state on startup", map[string]interface{}{"count": count})
}
