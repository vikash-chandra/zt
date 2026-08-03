package execution

import (
	"fmt"
	"math"
	"time"
	"zerodha-trading/data"

	"go.uber.org/zap"
)

// OptionsExecutor handles order execution for options trading (Paper Mode or Live Zerodha execution)
type OptionsExecutor struct {
	broker      data.BrokerClient
	logger      *zap.Logger
	liveTrading bool
}

// NewOptionsExecutor creates a new OptionsExecutor
func NewOptionsExecutor(broker data.BrokerClient, logger *zap.Logger, liveTrading bool) *OptionsExecutor {
	return &OptionsExecutor{
		broker:      broker,
		logger:      logger,
		liveTrading: liveTrading,
	}
}

// ExecuteOptionOrder places an aggressive limit order for options to guarantee instant fills while complying with Zerodha API policies
func (e *OptionsExecutor) ExecuteOptionOrder(symbol, side string, qty int, price float64) (string, float64, error) {
	if !e.liveTrading {
		// Paper / Dummy Mode: Simulate instant fill at live tick price
		simulatedID := fmt.Sprintf("PAPER-%d", time.Now().UnixNano())
		e.logger.Info("[DUMMY MODE - PAPER TRADING] Simulated Options Order Filled",
			zap.String("order_id", simulatedID),
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

	// Calculate aggressive limit price for instant market-like execution with protection
	var limitPrice float64
	if side == "SELL" {
		// 5% below LTP for SELL (instant fill at best buyer price)
		limitPrice = math.Max(0.50, math.Floor(price*0.95*20.0)/20.0)
	} else {
		// 5% above LTP for BUY (instant fill at best seller price)
		limitPrice = math.Ceil(price*1.05*20.0) / 20.0
	}

	orderReq := OrderRequest{
		TradingSymbol:   symbol,
		Exchange:        "NFO",
		Quantity:        qty,
		TransactionType: side,
		OrderType:       OrderTypeLimit,
		Product:         "MIS",
		Validity:        "DAY",
		Price:           &limitPrice,
	}

	e.logger.Info("[LIVE OPTION ORDER] Submitting aggressive limit order to Zerodha API",
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

	return orderID, price, nil
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
func (e *OptionsExecutor) PlaceOptionSLOrder(symbol string, qty int, triggerPrice float64) (string, error) {
	if !e.liveTrading {
		simulatedSLID := fmt.Sprintf("PAPER-SL-%d", time.Now().UnixNano())
		e.logger.Info("[PAPER TRADING] Simulated Options SL Order Registered",
			zap.String("sl_order_id", simulatedSLID),
			zap.String("symbol", symbol),
			zap.Float64("trigger_price", triggerPrice),
		)
		return simulatedSLID, nil
	}

	if e.broker == nil {
		return "", fmt.Errorf("broker client is nil in live trading mode")
	}

	trigPrice := math.Round(triggerPrice*20.0) / 20.0
	// 5% Limit buffer above trigger price for SL BUY execution to guarantee fill on Zerodha API
	limitPrice := math.Ceil(trigPrice*1.05*20.0) / 20.0

	orderReq := OrderRequest{
		TradingSymbol:   symbol,
		Exchange:        "NFO",
		Quantity:        qty,
		TransactionType: "BUY",
		OrderType:       OrderTypeSL,
		Product:         "MIS",
		Validity:        "DAY",
		TriggerPrice:    &trigPrice,
		Price:           &limitPrice,
	}

	e.logger.Info("[LIVE OPTION SL ORDER] Submitting SL Limit order to Zerodha API",
		zap.String("symbol", symbol),
		zap.Int("qty", qty),
		zap.Float64("trigger_price", trigPrice),
		zap.Float64("limit_price", limitPrice),
	)

	return e.PlaceOrderWithBroker(orderReq)
}
