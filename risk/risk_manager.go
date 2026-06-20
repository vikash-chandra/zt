package risk

import (
	"database/sql"
	"sync"
	"time"

	"go.uber.org/zap"
)

// RiskLimits defines risk thresholds
type RiskLimits struct {
	MaxDailyLossPct    float64
	MaxLossAmount      float64
	MaxPositionSize    float64
	MaxTradesPerDay    int
	MaxLossStreaks     int
	MaxQtyPerOrder     int
	MinProfitTargetPct float64
	MaxHoldingTimeMin  int
}

// Position represents an open position
type Position struct {
	OrderID     string
	Symbol      string
	Quantity    int
	EntryPrice  float64
	Side        string
	SLPrice     float64
	CreatedAt   time.Time
	LatestPrice float64
}

// ClosedTrade represents a completed trade
type ClosedTrade struct {
	Symbol      string
	Entry       float64
	Exit        float64
	Quantity    int
	PnL         float64
	Side        string
	TimeHeldMin int
	CreatedAt   time.Time
}

// RiskManager enforces capital preservation
type RiskManager struct {
	db                *sql.DB
	logger            *zap.Logger
	initialCapital    float64
	limits            RiskLimits
	dailyPnL          float64
	tradestoday       int
	lossStreaks       int
	openPositions     map[string]*Position
	closedTrades      []ClosedTrade
	circuitBreakerHit bool
	mu                sync.RWMutex
}

// NewRiskManager creates new risk manager
func NewRiskManager(db *sql.DB, logger *zap.Logger, initialCapital float64, limits RiskLimits) *RiskManager {
	return &RiskManager{
		db:                db,
		logger:            logger,
		initialCapital:    initialCapital,
		limits:            limits,
		dailyPnL:          0,
		tradestoday:       0,
		lossStreaks:       0,
		openPositions:     make(map[string]*Position),
		closedTrades:      make([]ClosedTrade, 0),
		circuitBreakerHit: false,
	}
}

// CanPlaceOrder performs pre-trade risk checks
func (rm *RiskManager) CanPlaceOrder(quantity int, price float64) bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	capitalNeeded := float64(quantity) * price

	checks := []struct {
		condition bool
		reason    string
	}{
		{capitalNeeded <= rm.limits.MaxPositionSize, "Position size exceeds limit"},
		{quantity <= rm.limits.MaxQtyPerOrder, "Quantity exceeds max per order"},
		{rm.tradestoday < rm.limits.MaxTradesPerDay, "Max trades per day reached"},
		{rm.dailyPnL > -rm.limits.MaxLossAmount, "Daily loss limit exceeded"},
		{rm.lossStreaks < rm.limits.MaxLossStreaks, "Loss streak limit exceeded"},
		{!rm.circuitBreakerHit, "Circuit breaker active"},
	}

	for _, check := range checks {
		if !check.condition {
			rm.logger.Error("RiskCheck FAILED", zap.String("reason", check.reason))
			return false
		}
	}

	return true
}

// AddOpenPosition tracks a new position
func (rm *RiskManager) AddOpenPosition(orderID string, symbol string, qty int, entryPrice float64, side string, sl float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	pos := &Position{
		OrderID:     orderID,
		Symbol:      symbol,
		Quantity:    qty,
		EntryPrice:  entryPrice,
		Side:        side,
		SLPrice:     sl,
		CreatedAt:   time.Now(),
		LatestPrice: entryPrice,
	}

	rm.openPositions[orderID] = pos
	rm.tradestoday++

	rm.logger.Info("Position opened",
		zap.String("order_id", orderID),
		zap.String("symbol", symbol),
		zap.Int("qty", qty),
		zap.Float64("entry", entryPrice),
		zap.Float64("sl", sl),
	)
}

// OnOrderClose records a closed trade
func (rm *RiskManager) OnOrderClose(orderID string, exitPrice float64, exitQty int) {
	rm.mu.Lock()
	pos, exists := rm.openPositions[orderID]
	if !exists {
		rm.mu.Unlock()
		return
	}

	delete(rm.openPositions, orderID)
	rm.mu.Unlock()

	// Calculate P&L
	var pnl float64
	if pos.Side == "BUY" {
		pnl = (exitPrice - pos.EntryPrice) * float64(exitQty)
	} else {
		pnl = (pos.EntryPrice - exitPrice) * float64(exitQty)
	}

	timeHeld := int(time.Since(pos.CreatedAt).Minutes())

	trade := ClosedTrade{
		Symbol:      pos.Symbol,
		Entry:       pos.EntryPrice,
		Exit:        exitPrice,
		Quantity:    exitQty,
		PnL:         pnl,
		Side:        pos.Side,
		TimeHeldMin: timeHeld,
		CreatedAt:   time.Now(),
	}

	rm.mu.Lock()
	rm.closedTrades = append(rm.closedTrades, trade)
	rm.dailyPnL += pnl

	if pnl < 0 {
		rm.lossStreaks++
	} else {
		rm.lossStreaks = 0
	}

	// Check circuit breaker
	drawdownPct := (rm.dailyPnL / rm.initialCapital) * 100
	if drawdownPct <= -rm.limits.MaxDailyLossPct {
		rm.circuitBreakerHit = true
		rm.logger.Critical("CIRCUIT BREAKER TRIGGERED",
			zap.Float64("drawdown_pct", drawdownPct),
			zap.Float64("limit_pct", rm.limits.MaxDailyLossPct),
		)
	}

	rm.mu.Unlock()

	rm.logger.Info("Trade closed",
		zap.String("symbol", pos.Symbol),
		zap.Float64("entry", pos.EntryPrice),
		zap.Float64("exit", exitPrice),
		zap.Float64("pnl", pnl),
		zap.Int("time_held_min", timeHeld),
	)

	// Persist trade
	rm.persistTrade(trade)
}

// CheckTrailingSL checks if position SL has been breached
func (rm *RiskManager) CheckTrailingSL(orderID string, currentPrice float64) string {
	rm.mu.RLock()
	pos, exists := rm.openPositions[orderID]
	if !exists {
		rm.mu.RUnlock()
		return ""
	}

	// Check SL breach
	if pos.Side == "BUY" && currentPrice <= pos.SLPrice {
		rm.mu.RUnlock()
		rm.logger.Warn("SL breach BUY", zap.String("symbol", pos.Symbol), zap.Float64("sl", pos.SLPrice))
		return "CLOSE"
	}

	if pos.Side == "SELL" && currentPrice >= pos.SLPrice {
		rm.mu.RUnlock()
		rm.logger.Warn("SL breach SELL", zap.String("symbol", pos.Symbol), zap.Float64("sl", pos.SLPrice))
		return "CLOSE"
	}

	// Check time limit
	holdTimeMin := int(time.Since(pos.CreatedAt).Minutes())
	if holdTimeMin > rm.limits.MaxHoldingTimeMin {
		rm.mu.RUnlock()
		rm.logger.Info("Time limit exceeded", zap.String("symbol", pos.Symbol), zap.Int("minutes", holdTimeMin))
		return "CLOSE"
	}

	rm.mu.RUnlock()
	return ""
}

// GetMetrics returns current risk metrics
func (rm *RiskManager) GetMetrics() map[string]interface{} {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	drawdownPct := (rm.dailyPnL / rm.initialCapital) * 100
	winCount := 0
	for _, trade := range rm.closedTrades {
		if trade.PnL > 0 {
			winCount++
		}
	}

	winRate := 0.0
	if len(rm.closedTrades) > 0 {
		winRate = float64(winCount) / float64(len(rm.closedTrades)) * 100
	}

	return map[string]interface{}{
		"daily_pnl":              rm.dailyPnL,
		"drawdown_pct":           drawdownPct,
		"trades_today":           rm.tradestoday,
		"loss_streaks":           rm.lossStreaks,
		"open_positions":         len(rm.openPositions),
		"closed_trades":          len(rm.closedTrades),
		"win_rate":               winRate,
		"circuit_breaker_active": rm.circuitBreakerHit,
	}
}

func (rm *RiskManager) persistTrade(trade ClosedTrade) {
	query := `
		INSERT INTO trades (symbol, entry_price, exit_price, quantity, pnl, side, time_held_minutes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := rm.db.Exec(query, trade.Symbol, trade.Entry, trade.Exit, trade.Quantity,
		trade.PnL, trade.Side, trade.TimeHeldMin, trade.CreatedAt)

	if err != nil {
		rm.logger.Error("Failed to persist trade", zap.Error(err))
	}
}

// GetOpenPositions returns copy of open positions
func (rm *RiskManager) GetOpenPositions() map[string]*Position {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	result := make(map[string]*Position)
	for k, v := range rm.openPositions {
		result[k] = v
	}
	return result
}

// UpdatePositionPrice updates current price for open position
func (rm *RiskManager) UpdatePositionPrice(orderID string, currentPrice float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if pos, exists := rm.openPositions[orderID]; exists {
		pos.LatestPrice = currentPrice
	}
}
