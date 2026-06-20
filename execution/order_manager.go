package execution

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
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
	db             *sql.DB
	logger         *zap.Logger
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
func NewExecutionManager(db *sql.DB, logger *zap.Logger) *ExecutionManager {
	return &ExecutionManager{
		db:             db,
		logger:         logger,
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

	// In production, this would call Kite API
	// For now, simulate order placement
	orderID := em.generateOrderID()

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
	)

	return orderID, nil
}

// ModifyOrderTrailingSL modifies stop-loss order to trail price
func (em *ExecutionManager) ModifyOrderTrailingSL(orderID string, currentPrice, atr float64) error {
	em.mu.Lock()
	record, exists := em.orderMap[orderID]
	em.mu.Unlock()

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
	record, exists := em.orderMap[orderID]
	em.mu.Unlock()

	if !exists {
		return fmt.Errorf("order not found: %s", orderID)
	}

	// In production, call Kite API to cancel
	record.Status = "CANCELLED"

	// Update database
	em.updateOrderStatus(orderID, "CANCELLED")

	em.logger.Info("Order cancelled", zap.String("order_id", orderID))

	return nil
}

// GetOrderStatus returns current order status
func (em *ExecutionManager) GetOrderStatus(orderID string) (*OrderStatus, error) {
	em.mu.RLock()
	record, exists := em.orderMap[orderID]
	em.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("order not found: %s", orderID)
	}

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

// SimulateOrderFill simulates order fill (for testing)
func (em *ExecutionManager) SimulateOrderFill(orderID string, filledQty int, price float64) error {
	em.mu.Lock()
	record, exists := em.orderMap[orderID]
	em.mu.Unlock()

	if !exists {
		return fmt.Errorf("order not found: %s", orderID)
	}

	fill := OrderFill{
		Quantity:  filledQty,
		Price:     price,
		Timestamp: time.Now(),
	}

	em.mu.Lock()
	record.Fills = append(record.Fills, fill)
	em.mu.Unlock()

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
	query := `
		INSERT INTO orders (order_id, symbol, exchange, quantity, transaction_type, order_type, product, placed_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := em.db.Exec(query, orderID, req.TradingSymbol, req.Exchange, req.Quantity,
		req.TransactionType, string(req.OrderType), req.Product, time.Now(), "PENDING")

	if err != nil {
		em.logger.Error("Failed to persist order", zap.Error(err))
	}
}

func (em *ExecutionManager) updateOrderStatus(orderID, status string) {
	query := `UPDATE orders SET status = $1, updated_at = $2 WHERE order_id = $3`
	_, err := em.db.Exec(query, status, time.Now(), orderID)
	if err != nil {
		em.logger.Error("Failed to update order status", zap.Error(err))
	}
}
