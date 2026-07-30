package execution

import (
	"fmt"
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

// ExecuteOptionOrder places a limit or market order for options (supports Paper simulation when liveTrading=false)
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

	// Live Trading Mode: Place limit order with Zerodha API
	if e.broker == nil {
		return "", 0, fmt.Errorf("broker client is nil in live trading mode")
	}

	orderReq := OrderRequest{
		TradingSymbol:   symbol,
		Exchange:        "NFO",
		Quantity:        qty,
		TransactionType: side,
		OrderType:       OrderTypeLimit,
		Product:         "MIS",
		Validity:        "DAY",
		Price:           &price,
	}

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

	params := data.OrderParams{
		Exchange:        req.Exchange,
		TradingSymbol:   req.TradingSymbol,
		TransactionType: req.TransactionType,
		Quantity:        req.Quantity,
		Price:           priceVal,
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
