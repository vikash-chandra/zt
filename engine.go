package main

import (
	"fmt"
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
								startH, startM, errTime = parseTimeHM(tb.cfg.LVTradeStartTime)
								if errTime != nil {
									startH, startM = 9, 30
								}
								endH, endM, errTime = parseTimeHM(tb.cfg.LVTradeEndTime)
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
							tb.riskMgr.OnOrderClose(orderID, 0, 0)
							continue
						}
					} else if orderStatus.Status == "COMPLETE" {
						tb.placeBrokerStopLoss(orderID, pos)
					}
				}

				tick := tb.ticker.GetLatestTick(pos.Token)
				if tick == nil {
					continue
				}

				currentPrice := tick.LTP

				// Check risk limits (Stop-Loss and Target 1 partial exits)
				action := tb.riskMgr.CheckTrailingSL(orderID, currentPrice)
				if action == "CLOSE" {
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
							OrderType:       execution.OrderTypeMarket,
							Product:         "MIS",
							Validity:        "DAY",
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
						}
					} else {
						tb.execMgr.CancelOrder(orderID)
						tb.riskMgr.OnOrderClose(orderID, currentPrice, pos.Quantity)
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
							OrderType:       execution.OrderTypeMarket,
							Product:         "MIS",
							Validity:        "DAY",
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

// reconcilePositions checks Zerodha on startup and squares off any unmanaged MIS positions for safety
func (tb *TradingBot) reconcilePositions() {
	if !tb.execMgr.LiveTrading {
		return
	}

	tb.logger.Info("Reconciling open positions on startup...", nil)
	livePositions, err := tb.kiteClient.GetPositions()
	if err != nil {
		tb.logger.Error("Failed to fetch open positions from Zerodha on startup", map[string]interface{}{"error": err.Error()})
		return
	}

	for _, pos := range livePositions.Net {
		if pos.Product == "MIS" && pos.Quantity != 0 {
			tb.logger.Warn("Orphan open MIS position found on startup. Squaring off for safety.", map[string]interface{}{
				"symbol": pos.Tradingsymbol,
				"qty":    pos.Quantity,
			})

			var txnType string
			var exitQty int
			if pos.Quantity > 0 {
				txnType = "SELL"
				exitQty = pos.Quantity
			} else {
				txnType = "BUY"
				exitQty = -pos.Quantity
			}

			orderReq := execution.OrderRequest{
				TradingSymbol:   pos.Tradingsymbol,
				Exchange:        "NSE",
				Quantity:        exitQty,
				TransactionType: txnType,
				OrderType:       execution.OrderTypeMarket,
				Product:         "MIS",
				Validity:        "DAY",
			}

			_, err := tb.execMgr.PlaceOrder(orderReq)
			if err != nil {
				tb.logger.Error("Failed to square off orphan position on startup", map[string]interface{}{
					"symbol": pos.Tradingsymbol,
					"error":  err.Error(),
				})
			} else {
				tb.logger.Info("Successfully squared off orphan position on startup", map[string]interface{}{
					"symbol": pos.Tradingsymbol,
				})
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

	var limitPrice float64
	if txnType == "SELL" {
		limitPrice = float64(int((pos.SLPrice*0.99)/0.05)) * 0.05
	} else {
		limitPrice = float64(int((pos.SLPrice*1.01)/0.05)) * 0.05
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

	var limitPrice float64
	if exitTxnType == "SELL" {
		limitPrice = float64(int((updatedPos.SLPrice*0.99)/0.05)) * 0.05
	} else {
		limitPrice = float64(int((updatedPos.SLPrice*1.01)/0.05)) * 0.05
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
	} else {
		tb.logger.Info("Successfully replaced broker-side stop-loss order after partial exit", map[string]interface{}{
			"symbol":        updatedPos.Symbol,
			"sl_order_id":   slOrderID,
			"trigger_price": updatedPos.SLPrice,
			"strategy":      updatedPos.Strategy,
		})
		tb.riskMgr.SetBrokerSLOrderID(orderID, slOrderID)
		tb.statusTracker.StartTracking(slOrderID)
	}
}
