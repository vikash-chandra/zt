package execution

import (
	"database/sql"
	"database/sql/driver"
	"testing"
	"time"
	"zerodha-trading/data"

	"go.uber.org/zap"
)

// Mock SQL Driver to bypass real database connection in tests
type mockDriver struct{}
type mockConn struct{}

func (d *mockDriver) Open(name string) (driver.Conn, error) {
	return &mockConn{}, nil
}

func (c *mockConn) Prepare(query string) (driver.Stmt, error) {
	return &mockStmt{}, nil
}

func (c *mockConn) Close() error {
	return nil
}

func (c *mockConn) Begin() (driver.Tx, error) {
	return nil, nil
}

type mockStmt struct{}

func (s *mockStmt) Close() error                                    { return nil }
func (s *mockStmt) NumInput() int                                   { return -1 }
func (s *mockStmt) Exec(args []driver.Value) (driver.Result, error) { return &mockResult{}, nil }
func (s *mockStmt) Query(args []driver.Value) (driver.Rows, error)  { return nil, nil }

type mockResult struct{}

func (r *mockResult) LastInsertId() (int64, error) { return 0, nil }
func (r *mockResult) RowsAffected() (int64, error) { return 1, nil }

func init() {
	sql.Register("mock_db", &mockDriver{})
}

// Mock PositionManager to track OnOrderClose callbacks
type MockPositionManager struct {
	LastOrderID   string
	LastExitPrice float64
	LastExitQty   int
	CloseCalled   bool
}

func (m *MockPositionManager) OnOrderClose(orderID string, exitPrice float64, exitQty int) {
	m.CloseCalled = true
	m.LastOrderID = orderID
	m.LastExitPrice = exitPrice
	m.LastExitQty = exitQty
}

func TestStatusTrackerPartialFillCancellation(t *testing.T) {
	logger := zap.NewNop()

	// 1. Initialize mock database
	sqlConn, err := sql.Open("mock_db", "")
	if err != nil {
		t.Fatalf("failed to open mock db: %v", err)
	}
	db := data.NewDatabaseFromConn(sqlConn, logger)

	// 2. Initialize ExecutionManager in simulation mode (LiveTrading = false)
	resilientExec := NewResilientExecutor(logger)
	em := NewExecutionManager(db, logger, nil, resilientExec, false)

	// 3. Register a mock entry order in ExecutionManager map
	entryOrderID := "order-1"
	em.orderMap[entryOrderID] = &OrderRecord{
		OrderID: entryOrderID,
		Status:  "PENDING",
		Request: OrderRequest{
			TradingSymbol:   "SBIN",
			Exchange:        "NSE",
			Quantity:        100,
			TransactionType: "BUY",
			OrderType:       OrderTypeLimit,
			Product:         "MIS",
		},
		PlacedAt: time.Now(),
	}

	// 4. Initialize StatusTracker
	mockPM := &MockPositionManager{}
	st := &StatusTracker{
		em:               em,
		posMgr:           mockPM,
		logger:           logger,
		orderStatusCache: make(map[string]*OrderStatus),
	}

	// 5. Simulate CANCELLED status update with a partial fill of 40 shares
	cancelledStatus := &OrderStatus{
		OrderID:        entryOrderID,
		Status:         "CANCELLED",
		FilledQuantity: 40,
		AveragePrice:   105.50,
		Timestamp:      time.Now(),
	}

	st.handleStatusChange(entryOrderID, nil, cancelledStatus)

	// 6. Verify MockPositionManager was NOT notified of close (since position remains active)
	if mockPM.CloseCalled {
		t.Error("expected OnOrderClose NOT to be called, allowing position to remain active")
	}

	// 7. Verify no square-off order was placed in em.orderMap
	em.mu.RLock()
	defer em.mu.RUnlock()

	for placedID, record := range em.orderMap {
		if placedID != entryOrderID {
			t.Errorf("expected no additional orders to be placed, found order %s: %+v", placedID, record)
		}
	}
}

func TestStatusTrackerUnfilledCancellation(t *testing.T) {
	logger := zap.NewNop()

	sqlConn, err := sql.Open("mock_db", "")
	if err != nil {
		t.Fatalf("failed to open mock db: %v", err)
	}
	db := data.NewDatabaseFromConn(sqlConn, logger)
	resilientExec := NewResilientExecutor(logger)
	em := NewExecutionManager(db, logger, nil, resilientExec, false)

	entryOrderID := "order-unfilled"
	em.orderMap[entryOrderID] = &OrderRecord{
		OrderID: entryOrderID,
		Status:  "PENDING",
		Request: OrderRequest{
			TradingSymbol: "SBIN",
			Quantity:      100,
		},
	}

	mockPM := &MockPositionManager{}
	st := &StatusTracker{
		em:               em,
		posMgr:           mockPM,
		logger:           logger,
		orderStatusCache: make(map[string]*OrderStatus),
	}

	// Cancelled with 0 filled quantity
	cancelledStatus := &OrderStatus{
		OrderID:        entryOrderID,
		Status:         "CANCELLED",
		FilledQuantity: 0,
		Timestamp:      time.Now(),
	}

	st.handleStatusChange(entryOrderID, nil, cancelledStatus)

	if !mockPM.CloseCalled {
		t.Error("expected OnOrderClose to be called for completely unfilled cancellation")
	}
	if mockPM.LastOrderID != entryOrderID {
		t.Errorf("expected closed order ID '%s', got '%s'", entryOrderID, mockPM.LastOrderID)
	}
}

func TestStatusTrackerOrderRejected(t *testing.T) {
	logger := zap.NewNop()

	sqlConn, err := sql.Open("mock_db", "")
	if err != nil {
		t.Fatalf("failed to open mock db: %v", err)
	}
	db := data.NewDatabaseFromConn(sqlConn, logger)
	resilientExec := NewResilientExecutor(logger)
	em := NewExecutionManager(db, logger, nil, resilientExec, false)

	entryOrderID := "order-rejected"
	em.orderMap[entryOrderID] = &OrderRecord{
		OrderID: entryOrderID,
		Status:  "PENDING",
		Request: OrderRequest{
			TradingSymbol: "SBIN",
			Quantity:      100,
		},
	}

	mockPM := &MockPositionManager{}
	st := &StatusTracker{
		em:               em,
		posMgr:           mockPM,
		logger:           logger,
		orderStatusCache: make(map[string]*OrderStatus),
	}

	// Rejected order
	rejectedStatus := &OrderStatus{
		OrderID:         entryOrderID,
		Status:          "REJECTED",
		RejectionReason: "Insufficient Margin",
		Timestamp:       time.Now(),
	}

	st.handleStatusChange(entryOrderID, nil, rejectedStatus)

	if !mockPM.CloseCalled {
		t.Error("expected OnOrderClose to be called for rejected order")
	}
}
