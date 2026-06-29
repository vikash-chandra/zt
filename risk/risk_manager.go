package risk

import (
	"database/sql"
	"math"
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
	OrderID           string
	Symbol            string
	Token             int64
	Quantity          int
	EntryPrice        float64
	Side              string
	SLPrice           float64
	Target1Price      float64
	IsPartialExitDone bool
	CreatedAt         time.Time
	LatestPrice       float64
	Strategy          string
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
	Strategy    string
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

// AddOpenPosition tracks a new position and calculates Target 1 (1:2 Risk-Reward)
func (rm *RiskManager) AddOpenPosition(orderID string, symbol string, token int64, qty int, entryPrice float64, side string, sl float64, strategy string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Calculate initial risk: Risk = |Entry - SL|
	risk := math.Abs(entryPrice - sl)
	var target1 float64
	if side == "BUY" {
		target1 = entryPrice + (2.0 * risk)
	} else {
		target1 = entryPrice - (2.0 * risk)
	}

	pos := &Position{
		OrderID:           orderID,
		Symbol:            symbol,
		Token:             token,
		Quantity:          qty,
		EntryPrice:        entryPrice,
		Side:              side,
		SLPrice:           sl,
		Target1Price:      target1,
		IsPartialExitDone: false,
		CreatedAt:         time.Now(),
		LatestPrice:       entryPrice,
		Strategy:          strategy,
	}

	rm.openPositions[orderID] = pos
	rm.tradestoday++

	rm.logger.Info("Position opened",
		zap.String("order_id", orderID),
		zap.String("symbol", symbol),
		zap.Int("qty", qty),
		zap.Float64("entry", entryPrice),
		zap.Float64("sl", sl),
		zap.Float64("target1", target1),
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
		Strategy:    pos.Strategy,
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
		rm.logger.Error("CIRCUIT BREAKER TRIGGERED",
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

// CheckTrailingSL checks if position SL has been breached or if Target 1 is hit
func (rm *RiskManager) CheckTrailingSL(orderID string, currentPrice float64) string {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	pos, exists := rm.openPositions[orderID]
	if !exists {
		return ""
	}

	// 1. Check if Target 1 (1:2 R:R) is hit for partial exit
	if !pos.IsPartialExitDone {
		if pos.Side == "BUY" && currentPrice >= pos.Target1Price {
			pos.IsPartialExitDone = true
			pos.SLPrice = pos.EntryPrice // Trail stop-loss to entry price (break-even)
			rm.logger.Info("Target 1 (1:2 R:R) hit! Trailing stop-loss to entry price.",
				zap.String("symbol", pos.Symbol),
				zap.Float64("target1", pos.Target1Price),
				zap.Float64("entry", pos.EntryPrice),
			)
			return "PARTIAL_EXIT"
		}
		if pos.Side == "SELL" && currentPrice <= pos.Target1Price {
			pos.IsPartialExitDone = true
			pos.SLPrice = pos.EntryPrice // Trail stop-loss to entry price (break-even)
			rm.logger.Info("Target 1 (1:2 R:R) hit! Trailing stop-loss to entry price.",
				zap.String("symbol", pos.Symbol),
				zap.Float64("target1", pos.Target1Price),
				zap.Float64("entry", pos.EntryPrice),
			)
			return "PARTIAL_EXIT"
		}
	}

	// 2. Check Stop-Loss breach
	if pos.Side == "BUY" && currentPrice <= pos.SLPrice {
		rm.logger.Warn("SL breach BUY", zap.String("symbol", pos.Symbol), zap.Float64("sl", pos.SLPrice))
		return "CLOSE"
	}

	if pos.Side == "SELL" && currentPrice >= pos.SLPrice {
		rm.logger.Warn("SL breach SELL", zap.String("symbol", pos.Symbol), zap.Float64("sl", pos.SLPrice))
		return "CLOSE"
	}

	// 3. Check time limit
	holdTimeMin := int(time.Since(pos.CreatedAt).Minutes())
	if holdTimeMin > rm.limits.MaxHoldingTimeMin {
		rm.logger.Info("Time limit exceeded", zap.String("symbol", pos.Symbol), zap.Int("minutes", holdTimeMin))
		return "CLOSE"
	}

	return ""
}

// RecordPartialExit logs a partial exit transaction in the database and updates the position quantity
func (rm *RiskManager) RecordPartialExit(orderID string, exitPrice float64, exitQty int) {
	rm.mu.Lock()
	pos, exists := rm.openPositions[orderID]
	if !exists {
		rm.mu.Unlock()
		return
	}

	// Calculate P&L for the partial lot
	var pnl float64
	if pos.Side == "BUY" {
		pnl = (exitPrice - pos.EntryPrice) * float64(exitQty)
	} else {
		pnl = (pos.EntryPrice - exitPrice) * float64(exitQty)
	}

	timeHeld := int(time.Since(pos.CreatedAt).Minutes())

	// Decrement remaining position tracking quantity
	pos.Quantity -= exitQty
	pos.IsPartialExitDone = true
	rm.mu.Unlock()

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
	rm.mu.Unlock()

	rm.logger.Info("Partial exit transaction recorded",
		zap.String("symbol", pos.Symbol),
		zap.Int("qty", exitQty),
		zap.Float64("entry", pos.EntryPrice),
		zap.Float64("exit", exitPrice),
		zap.Float64("pnl", pnl),
	)

	// Persist partial trade to PostgreSQL
	rm.persistTrade(trade)
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
	strategyName := "LOW_VOLUME"
	if trade.Strategy != "" {
		strategyName = trade.Strategy
	}
	query := `
		INSERT INTO trades (symbol, entry_price, exit_price, quantity, pnl, side, time_held_minutes, created_at, strategy)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := rm.db.Exec(query, trade.Symbol, trade.Entry, trade.Exit, trade.Quantity,
		trade.PnL, trade.Side, trade.TimeHeldMin, trade.CreatedAt, strategyName)

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
