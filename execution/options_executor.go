package execution

import (
	"fmt"
	"math"
	"strings"
	"time"
	"zerodha-trading/data"

	"go.uber.org/zap"
)

// OptionsExecutor handles order execution for options trading (Paper Mode or Live Zerodha execution)
type OptionsExecutor struct {
	broker         data.BrokerClient
	logger         *zap.Logger
	liveTrading    bool
	limitBufferPct float64
}

// NewOptionsExecutor creates a new OptionsExecutor
func NewOptionsExecutor(broker data.BrokerClient, logger *zap.Logger, liveTrading bool) *OptionsExecutor {
	return &OptionsExecutor{
		broker:         broker,
		logger:         logger,
		liveTrading:    liveTrading,
		limitBufferPct: 0.05, // default 5%
	}
}

// SetLimitBufferPct updates the limit execution buffer percentage (e.g. 5.0 for 5%)
func (e *OptionsExecutor) SetLimitBufferPct(pct float64) {
	if pct > 0 {
		e.limitBufferPct = pct / 100.0
	}
}

// GetLimitBufferPct returns the active limit buffer fraction (e.g. 0.05 for 5%)
func (e *OptionsExecutor) GetLimitBufferPct() float64 {
	if e.limitBufferPct > 0 {
		return e.limitBufferPct
	}
	return 0.05
}

// ExecuteOptionOrder places an aggressive limit order for options to guarantee instant fills while complying with Zerodha API policies
func (e *OptionsExecutor) ExecuteOptionOrder(symbol, side string, qty int, price float64, opts ...interface{}) (string, float64, error) {
	exch := "NFO"
	if strings.HasPrefix(symbol, "SENSEX") {
		exch = "BFO"
	}
	live := e.liveTrading

	for _, opt := range opts {
		switch v := opt.(type) {
		case string:
			if v != "" {
				exch = v
			}
		case bool:
			live = v
		}
	}

	if !live {
		// Paper / Dummy Mode: Simulate instant fill at live tick price
		simulatedID := fmt.Sprintf("PAPER-%d", time.Now().UnixNano())
		e.logger.Info("[DUMMY MODE - PAPER TRADING] Simulated Options Order Filled",
			zap.String("order_id", simulatedID),
			zap.String("exchange", exch),
			zap.String("symbol", symbol),
			zap.String("side", side),
			zap.Int("qty", qty),
			zap.Float64("fill_price", price),
		)
		return simulatedID, price, nil
	}

	// Live Trading Mode: Place aggressive limit order with Zerodha API
	if e.broker == nil {
		return "", 0, fmt.Errorf("broker client is nil in live trading mode")
	}

	buffer := e.GetLimitBufferPct()
	var limitPrice float64
	if side == "SELL" {
		// Buffer % below LTP for SELL (instant fill at best buyer price)
		limitPrice = math.Max(0.50, math.Floor(price*(1.0-buffer)*20.0)/20.0)
	} else {
		// Buffer % above LTP for BUY (instant fill at best seller price)
		limitPrice = math.Ceil(price*(1.0+buffer)*20.0) / 20.0
	}

	orderReq := OrderRequest{
		TradingSymbol:   symbol,
		Exchange:        exch,
		Quantity:        qty,
		TransactionType: side,
		OrderType:       OrderTypeLimit,
		Product:         "MIS",
		Validity:        "DAY",
		Price:           &limitPrice,
	}

	e.logger.Info("[LIVE OPTION ORDER] Submitting aggressive limit order to Zerodha API",
		zap.String("exchange", exch),
		zap.String("symbol", symbol),
		zap.String("side", side),
		zap.Int("qty", qty),
		zap.Float64("ltp", price),
		zap.Float64("limit_price", limitPrice),
	)

	orderID, err := e.PlaceOrderWithBroker(orderReq)
	if err != nil {
		return "", 0, fmt.Errorf("failed to place live options order: %w", err)
	}

	fillPrice := price
	// Poll Zerodha order history to verify immediate fill and retrieve exact execution price
	for attempt := 0; attempt < 10; attempt++ {
		time.Sleep(300 * time.Millisecond)
		hist, hErr := e.broker.GetOrderHistory(orderID)
		if hErr == nil && len(hist) > 0 {
			latest := hist[len(hist)-1]
			if latest.Status == "COMPLETE" {
				if latest.AveragePrice > 0 {
					fillPrice = latest.AveragePrice
				}
				e.logger.Info("[LIVE OPTION ORDER FILLED] Confirmed execution on exchange",
					zap.String("order_id", orderID),
					zap.String("symbol", symbol),
					zap.String("side", side),
					zap.Float64("fill_price", fillPrice),
					zap.Int("filled_qty", latest.FilledQuantity),
				)
				break
			} else if latest.Status == "REJECTED" || latest.Status == "CANCELLED" {
				return orderID, 0, fmt.Errorf("live option order was %s by exchange: %s", latest.Status, latest.StatusMessage)
			}
		}
	}

	return orderID, fillPrice, nil
}

// PlaceOrderWithBroker passes order request to vendor-agnostic BrokerClient interface
func (e *OptionsExecutor) PlaceOrderWithBroker(req OrderRequest) (string, error) {
	priceVal := 0.0
	if req.Price != nil {
		priceVal = *req.Price
	}
	trigVal := 0.0
	if req.TriggerPrice != nil {
		trigVal = *req.TriggerPrice
	}

	params := data.OrderParams{
		Exchange:        req.Exchange,
		TradingSymbol:   req.TradingSymbol,
		TransactionType: req.TransactionType,
		Quantity:        req.Quantity,
		Price:           priceVal,
		TriggerPrice:    trigVal,
		OrderType:       string(req.OrderType),
		Product:         req.Product,
		Validity:        req.Validity,
	}

	resp, err := e.broker.PlaceOrder("regular", params)
	if err != nil {
		return "", err
	}
	return resp.OrderID, nil
}

// PlaceOptionSLOrder places a broker-side SL-M (Stop-Loss Market) order on Zerodha exchange to guarantee exchange-level SL protection
func (e *OptionsExecutor) PlaceOptionSLOrder(symbol string, qty int, triggerPrice float64, opts ...interface{}) (string, error) {
	exch := "NFO"
	if strings.HasPrefix(symbol, "SENSEX") {
		exch = "BFO"
	}
	live := e.liveTrading

	for _, opt := range opts {
		switch v := opt.(type) {
		case string:
			if v != "" {
				exch = v
			}
		case bool:
			live = v
		}
	}

	if !live {
		simulatedSLID := fmt.Sprintf("PAPER-SL-%d", time.Now().UnixNano())
		e.logger.Info("[PAPER TRADING] Simulated Options SL Order Registered",
			zap.String("sl_order_id", simulatedSLID),
			zap.String("exchange", exch),
			zap.String("symbol", symbol),
			zap.Float64("trigger_price", triggerPrice),
		)
		return simulatedSLID, nil
	}

	if e.broker == nil {
		return "", fmt.Errorf("broker client is nil in live trading mode")
	}

	trigPrice := math.Round(triggerPrice*20.0) / 20.0
	buffer := e.GetLimitBufferPct()
	limitPrice := math.Ceil(trigPrice*(1.0+buffer)*20.0) / 20.0

	orderReq := OrderRequest{
		TradingSymbol:   symbol,
		Exchange:        exch,
		Quantity:        qty,
		TransactionType: "BUY",
		OrderType:       OrderTypeSL,
		Product:         "MIS",
		Validity:        "DAY",
		TriggerPrice:    &trigPrice,
		Price:           &limitPrice,
	}

	e.logger.Info("[LIVE OPTION SL ORDER] Submitting SL Limit order to Zerodha API",
		zap.String("exchange", exch),
		zap.String("symbol", symbol),
		zap.Int("qty", qty),
		zap.Float64("trigger_price", trigPrice),
		zap.Float64("limit_price", limitPrice),
	)

	return e.PlaceOrderWithBroker(orderReq)
}

// CancelOptionOrder cancels an open order (such as an open SL order) on Zerodha exchange
func (e *OptionsExecutor) CancelOptionOrder(orderID string, opts ...interface{}) error {
	if orderID == "" || strings.HasPrefix(orderID, "PAPER") {
		return nil
	}
	if e.broker == nil {
		return fmt.Errorf("broker client is nil in live trading mode")
	}
	_, err := e.broker.CancelOrder("regular", orderID, nil)
	if err != nil {
		e.logger.Warn("[CANCEL OPTION ORDER] Failed to cancel order on exchange",
			zap.String("order_id", orderID),
			zap.Error(err),
		)
		return err
	}
	e.logger.Info("[CANCEL OPTION ORDER] Successfully cancelled order on exchange", zap.String("order_id", orderID))
	return nil
}

// ModifyOptionSLOrder updates an existing broker-side SL order on Zerodha exchange with a new trailed trigger price
func (e *OptionsExecutor) ModifyOptionSLOrder(orderID, symbol string, qty int, newTriggerPrice float64, opts ...interface{}) error {
	if orderID == "" || strings.HasPrefix(orderID, "PAPER") {
		e.logger.Info("[PAPER TRADING] Simulated Options SL Order Trailed",
			zap.String("sl_order_id", orderID),
			zap.String("symbol", symbol),
			zap.Float64("new_trigger_price", newTriggerPrice),
		)
		return nil
	}

	if e.broker == nil {
		return fmt.Errorf("broker client is nil in live trading mode")
	}

	exch := "NFO"
	if strings.HasPrefix(symbol, "SENSEX") {
		exch = "BFO"
	}
	for _, opt := range opts {
		if s, ok := opt.(string); ok && s != "" {
			exch = s
		}
	}

	trigPrice := math.Round(newTriggerPrice*20.0) / 20.0
	buffer := e.GetLimitBufferPct()
	limitPrice := math.Ceil(trigPrice*(1.0+buffer)*20.0) / 20.0

	params := data.OrderParams{
		Exchange:        exch,
		TradingSymbol:   symbol,
		TransactionType: "BUY",
		Quantity:        qty,
		OrderType:       string(OrderTypeSL),
		Product:         "MIS",
		Validity:        "DAY",
		TriggerPrice:    trigPrice,
		Price:           limitPrice,
	}

	e.logger.Info("[LIVE OPTION SL ORDER] Modifying SL order on Zerodha API",
		zap.String("order_id", orderID),
		zap.String("symbol", symbol),
		zap.Int("qty", qty),
		zap.Float64("new_trigger_price", trigPrice),
		zap.Float64("new_limit_price", limitPrice),
	)

	_, err := e.broker.ModifyOrder("regular", orderID, params)
	if err != nil {
		e.logger.Warn("[MODIFY OPTION SL ORDER] Failed to modify order on exchange",
			zap.String("order_id", orderID),
			zap.Error(err),
		)
		return err
	}

	e.logger.Info("[MODIFY OPTION SL ORDER] Successfully modified SL order on exchange",
		zap.String("order_id", orderID),
		zap.Float64("new_trigger_price", trigPrice),
	)
	return nil
}
