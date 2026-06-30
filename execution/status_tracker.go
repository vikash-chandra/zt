package execution

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// PositionManager defines interface to update position states on order status changes
type PositionManager interface {
	OnOrderClose(orderID string, price float64, qty int)
}

// StatusTracker monitors order status changes
type StatusTracker struct {
	em               *ExecutionManager
	posMgr           PositionManager
	logger           *zap.Logger
	activeOrders     map[string]bool
	orderStatusCache map[string]*OrderStatus
	pollingInterval  time.Duration
	mu               sync.RWMutex
}

// NewStatusTracker creates new status tracker
func NewStatusTracker(em *ExecutionManager, posMgr PositionManager, logger *zap.Logger) *StatusTracker {
	return &StatusTracker{
		em:               em,
		posMgr:           posMgr,
		logger:           logger,
		activeOrders:     make(map[string]bool),
		orderStatusCache: make(map[string]*OrderStatus),
		pollingInterval:  2 * time.Second,
	}
}

// StartTracking begins tracking an order
func (st *StatusTracker) StartTracking(orderID string) {
	st.mu.Lock()
	st.activeOrders[orderID] = true
	st.mu.Unlock()

	go st.pollStatus(orderID)
}

// pollStatus periodically polls order status
func (st *StatusTracker) pollStatus(orderID string) {
	ticker := time.NewTicker(st.pollingInterval)
	defer ticker.Stop()

	for {
		st.mu.RLock()
		active, exists := st.activeOrders[orderID]
		st.mu.RUnlock()

		if !active || !exists {
			break
		}

		status, err := st.em.GetOrderStatus(orderID)
		if err != nil {
			st.logger.Error("Failed to poll status", zap.String("order_id", orderID), zap.Error(err))
			<-ticker.C
			continue
		}

		st.mu.RLock()
		oldStatus := st.orderStatusCache[orderID]
		st.mu.RUnlock()

		// Check for status change
		if oldStatus == nil || status.Status != oldStatus.Status {
			st.handleStatusChange(orderID, oldStatus, status)
		}

		st.mu.Lock()
		st.orderStatusCache[orderID] = status
		st.mu.Unlock()

		// Stop tracking if completed
		if status.Status == "COMPLETE" || status.Status == "CANCELLED" || status.Status == "REJECTED" {
			st.mu.Lock()
			delete(st.activeOrders, orderID)
			st.mu.Unlock()
			break
		}

		<-ticker.C
	}
}

// handleStatusChange handles order status transitions
func (st *StatusTracker) handleStatusChange(orderID string, oldStatus, newStatus *OrderStatus) {
	statusMsg := "Unknown"
	if oldStatus != nil {
		statusMsg = oldStatus.Status
	}

	st.logger.Info("Order status changed",
		zap.String("order_id", orderID),
		zap.String("old_status", statusMsg),
		zap.String("new_status", newStatus.Status),
	)

	switch newStatus.Status {
	case "COMPLETE":
		st.logger.Info("Order fully filled",
			zap.String("order_id", orderID),
			zap.Float64("avg_price", newStatus.AveragePrice),
			zap.Int("quantity", newStatus.FilledQuantity),
		)

	case "PARTIALLY_FILLED":
		st.logger.Warn("Partial fill",
			zap.String("order_id", orderID),
			zap.Int("filled", newStatus.FilledQuantity),
			zap.Float64("avg_price", newStatus.AveragePrice),
		)

	case "REJECTED":
		st.logger.Error("Order rejected",
			zap.String("order_id", orderID),
			zap.String("reason", newStatus.RejectionReason),
		)
		if st.posMgr != nil {
			st.posMgr.OnOrderClose(orderID, 0, 0)
		}

	case "CANCELLED":
		st.logger.Info("Order cancelled", zap.String("order_id", orderID))
		if st.posMgr != nil {
			st.posMgr.OnOrderClose(orderID, 0, 0)
		}
	}
}

// GetCachedStatus returns cached status
func (st *StatusTracker) GetCachedStatus(orderID string) *OrderStatus {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.orderStatusCache[orderID]
}
