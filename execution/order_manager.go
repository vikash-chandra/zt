package execution

import (
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"zerodha-trading/data"
)

// OrderType represents order type
type OrderType string

const (
	OrderTypeMarket OrderType = "MARKET"
	OrderTypeLimit  OrderType = "LIMIT"
	OrderTypeSL     OrderType = "SL"
	OrderTypeSLM    OrderType = "SL-M"
)

// OrderRequest represents an order to be placed
type OrderRequest struct {
	TradingSymbol   string
	Exchange        string
	Quantity        int
	TransactionType string
	OrderType       OrderType
	Product         string
	Price           *float64
	TriggerPrice    *float64
	Validity        string
	Tag             string
	Strategy        string
}

// OrderStatus represents order state
type OrderStatus struct {
	OrderID         string
	Status          string
	FilledQuantity  int
	AveragePrice    float64
	RejectionReason string
	Timestamp       time.Time
}

// ExecutionManager handles order placement and modification
type ExecutionManager struct {
	db             *data.Database
	logger         *zap.Logger
	kiteClient     data.BrokerClient
	resilientExec  *ResilientExecutor
	LiveTrading    bool
	orderMap       map[string]*OrderRecord
	pendingOrders  map[string]string
	mu             sync.RWMutex
	maxRetries     int
	retryBackoffMs int
}

// OrderRecord tracks an order
type OrderRecord struct {
	Request     OrderRequest
	OrderID     string
	Status      string
	PlacedAt    time.Time
	Fills       []OrderFill
	LatestSL    float64
	LatestPrice float64
}

// OrderFill represents a partial fill
type OrderFill struct {
	Quantity  int
	Price     float64
	Timestamp time.Time
}

// NewExecutionManager creates new execution manager
func NewExecutionManager(db *data.Database, logger *zap.Logger, kiteClient data.BrokerClient, resilientExec *ResilientExecutor, liveTrading bool) *ExecutionManager {
	return &ExecutionManager{
		db:             db,
		logger:         logger,
		kiteClient:     kiteClient,
		resilientExec:  resilientExec,
		LiveTrading:    liveTrading,
		orderMap:       make(map[string]*OrderRecord),
		pendingOrders:  make(map[string]string),
		maxRetries:     3,
		retryBackoffMs: 100,
	}
}

// PlaceOrder places an order with validation and retry logic
func (em *ExecutionManager) PlaceOrder(req OrderRequest) (string, error) {
	// Validate order
	if err := em.validateOrder(req); err != nil {
		return "", err
	}

	var orderID string
	if em.LiveTrading {
		err := em.resilientExec.CallWithRetry(func() error {
			variety := "regular"
			orderType := string(req.OrderType)

			params := data.OrderParams{
				Exchange:        req.Exchange,
				TradingSymbol:   req.TradingSymbol,
				TransactionType: req.TransactionType,
				Quantity:        req.Quantity,
				OrderType:       orderType,
				Product:         req.Product,
				Validity:        req.Validity,
				Tag:             req.Tag,
			}
			if req.Price != nil {
				params.Price = *req.Price
			}
			if req.TriggerPrice != nil {
				params.TriggerPrice = *req.TriggerPrice
			}

			resp, execErr := em.kiteClient.PlaceOrder(variety, params)
			if execErr != nil {
				return execErr
			}
			orderID = resp.OrderID
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("failed to place live order: %w", err)
		}
	} else {
		// Simulation order placement
		orderID = em.generateOrderID()
	}

	record := &OrderRecord{
		Request:  req,
		OrderID:  orderID,
		Status:   "PENDING",
		PlacedAt: time.Now(),
		Fills:    make([]OrderFill, 0),
	}

	em.mu.Lock()
	em.orderMap[orderID] = record
	em.mu.Unlock()

	// Persist to database
	em.persistOrder(orderID, req)

	em.logger.Info("Order placed",
		zap.String("order_id", orderID),
		zap.String("symbol", req.TradingSymbol),
		zap.String("action", req.TransactionType),
		zap.Int("quantity", req.Quantity),
		zap.Bool("live", em.LiveTrading),
	)

	return orderID, nil
}

// ModifyOrderTrailingSL modifies stop-loss order to trail price
func (em *ExecutionManager) ModifyOrderTrailingSL(orderID string, currentPrice, atr float64) error {
	em.mu.Lock()
	defer em.mu.Unlock()

	record, exists := em.orderMap[orderID]
	if !exists {
		return fmt.Errorf("order not found: %s", orderID)
	}

	if record.Request.OrderType != OrderTypeSL && record.Request.OrderType != OrderTypeSLM {
		return fmt.Errorf("order is not SL type: %s", orderID)
	}

	// Calculate new SL: current price - 1.5 * ATR
	newSL := currentPrice - (1.5 * atr)

	// Only trail upward (lock in profits)
	if record.Request.TriggerPrice == nil {
		em.logger.Warn("Original SL not set", zap.String("order_id", orderID))
		return nil
	}

	if newSL <= *record.Request.TriggerPrice {
		return nil // Don't lower SL
	}

	record.LatestSL = newSL
	record.LatestPrice = currentPrice

	em.logger.Info("SL trailed",
		zap.String("order_id", orderID),
		zap.Float64("new_sl", newSL),
		zap.Float64("current_price", currentPrice),
	)

	return nil
}

// CancelOrder cancels an order
func (em *ExecutionManager) CancelOrder(orderID string) error {
	em.mu.Lock()
	defer em.mu.Unlock()

	record, exists := em.orderMap[orderID]
	if !exists {
		return fmt.Errorf("order not found: %s", orderID)
	}

	if em.LiveTrading {
		err := em.resilientExec.CallWithRetry(func() error {
			variety := "regular"
			_, execErr := em.kiteClient.CancelOrder(variety, orderID, nil)
			return execErr
		})
		if err != nil {
			return fmt.Errorf("failed to cancel live order: %w", err)
		}
	}

	record.Status = "CANCELLED"

	// Update database
	em.updateOrderStatus(orderID, "CANCELLED", 0, 0)

	em.logger.Info("Order cancelled", zap.String("order_id", orderID), zap.Bool("live", em.LiveTrading))

	return nil
}

// GetOrderStatus returns current order status
func (em *ExecutionManager) GetOrderStatus(orderID string) (*OrderStatus, error) {
	em.mu.RLock()
	record, exists := em.orderMap[orderID]
	em.mu.RUnlock()

	if !exists && !em.LiveTrading {
		return nil, fmt.Errorf("order not found: %s", orderID)
	}

	if em.LiveTrading {
		var history []data.Order
		err := em.resilientExec.CallWithRetry(func() error {
			var execErr error
			history, execErr = em.kiteClient.GetOrderHistory(orderID)
			return execErr
		})
		if err != nil {
			return nil, fmt.Errorf("failed to fetch live order history: %w", err)
		}
		if len(history) == 0 {
			return nil, fmt.Errorf("no history found for order %s", orderID)
		}

		latest := history[len(history)-1]

		if exists {
			em.mu.Lock()
			record.Status = latest.Status
			em.mu.Unlock()

			// Update database status
			em.updateOrderStatus(orderID, latest.Status, latest.AveragePrice, latest.FilledQuantity)
		}

		return &OrderStatus{
			OrderID:         orderID,
			Status:          latest.Status,
			FilledQuantity:  int(latest.FilledQuantity),
			AveragePrice:    latest.AveragePrice,
			RejectionReason: latest.StatusMessage,
			Timestamp:       latest.OrderTimestamp,
		}, nil
	}

	em.mu.RLock()
	defer em.mu.RUnlock()

	var avgPrice float64
	for _, fill := range record.Fills {
		avgPrice += fill.Price * float64(fill.Quantity)
	}

	totalFilled := 0
	for _, fill := range record.Fills {
		totalFilled += fill.Quantity
	}

	if totalFilled > 0 {
		avgPrice /= float64(totalFilled)
	}

	return &OrderStatus{
		OrderID:        orderID,
		Status:         record.Status,
		FilledQuantity: totalFilled,
		AveragePrice:   avgPrice,
		Timestamp:      time.Now(),
	}, nil
}

// GetOrderRecord retrieves an order record by order ID
func (em *ExecutionManager) GetOrderRecord(orderID string) (*OrderRecord, bool) {
	em.mu.RLock()
	defer em.mu.RUnlock()
	record, exists := em.orderMap[orderID]
	return record, exists
}

// SimulateOrderFill simulates order fill (for testing)
func (em *ExecutionManager) SimulateOrderFill(orderID string, filledQty int, price float64) error {
	em.mu.Lock()
	defer em.mu.Unlock()

	record, exists := em.orderMap[orderID]
	if !exists {
		return fmt.Errorf("order not found: %s", orderID)
	}

	fill := OrderFill{
		Quantity:  filledQty,
		Price:     price,
		Timestamp: time.Now(),
	}

	record.Fills = append(record.Fills, fill)

	totalFilled := 0
	for _, f := range record.Fills {
		totalFilled += f.Quantity
	}

	if totalFilled >= record.Request.Quantity {
		record.Status = "COMPLETE"
	} else {
		record.Status = "PARTIALLY_FILLED"
	}

	em.logger.Info("Order filled",
		zap.String("order_id", orderID),
		zap.Int("filled", filledQty),
		zap.Float64("price", price),
	)

	return nil
}

func (em *ExecutionManager) validateOrder(req OrderRequest) error {
	if req.Quantity <= 0 {
		return fmt.Errorf("invalid quantity: %d", req.Quantity)
	}
	if req.TradingSymbol == "" {
		return fmt.Errorf("symbol required")
	}
	if req.Exchange == "" {
		return fmt.Errorf("exchange required")
	}
	return nil
}

func (em *ExecutionManager) generateOrderID() string {
	return fmt.Sprintf("AUTO_%d_%d", time.Now().Unix(), time.Now().Nanosecond()%1000)
}

func (em *ExecutionManager) persistOrder(orderID string, req OrderRequest) {
	err := em.db.PersistOrder(orderID, req.TradingSymbol, req.Exchange, req.Quantity,
		req.TransactionType, string(req.OrderType), req.Product, "PENDING")
	if err != nil {
		em.logger.Error("Failed to persist order", zap.Error(err))
	}
}

func (em *ExecutionManager) updateOrderStatus(orderID, status string, averagePrice float64, filledQuantity int) {
	err := em.db.UpdateOrderStatus(orderID, status, averagePrice, filledQuantity)
	if err != nil {
		em.logger.Error("Failed to update order status", zap.Error(err))
	}
}

// RegisterRecoveredOrder registers a recovered order in memory
func (em *ExecutionManager) RegisterRecoveredOrder(orderID string, symbol string, side string, qty int, orderType string) {
	em.mu.Lock()
	defer em.mu.Unlock()

	// If already exists, do not overwrite
	if _, exists := em.orderMap[orderID]; exists {
		return
	}

	em.orderMap[orderID] = &OrderRecord{
		OrderID: orderID,
		Status:  "PENDING",
		Request: OrderRequest{
			TradingSymbol:   symbol,
			TransactionType: side,
			Quantity:        qty,
			OrderType:       OrderType(orderType),
		},
		PlacedAt: time.Now(),
		Fills:    make([]OrderFill, 0),
	}
	em.logger.Info("Registered recovered order in execution manager", zap.String("order_id", orderID), zap.String("symbol", symbol))
}
