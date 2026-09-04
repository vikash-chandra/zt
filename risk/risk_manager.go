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
	MaxTradesPerDay    int
	MaxLossStreaks     int
	MaxHoldingTimeMin  int
	MaxDailyLossAmount float64
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
	HighestPrice      float64
	Strategy          string
	BrokerSLOrderID   string
	LastPlacedSLPrice float64
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
	EntryTime   time.Time
	ExitTime    time.Time
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
	rrStrategies      map[string]RiskRewardStrategy
	stratToRRStrategy map[string]string // Trading Strategy (e.g. "LOW_VOLUME") -> RR Strategy Name
	mu                sync.RWMutex
}

// NewRiskManager creates new risk manager
func NewRiskManager(db *sql.DB, logger *zap.Logger, initialCapital float64, limits RiskLimits) *RiskManager {
	rm := &RiskManager{
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
		rrStrategies:      make(map[string]RiskRewardStrategy),
		stratToRRStrategy: make(map[string]string),
	}

	// Register default strategies
	rm.rrStrategies["PARTIAL_BOOK_COST_SL"] = NewPartialBookCostSLStrategy(DefaultPartialBookCostSLConfig())
	rm.rrStrategies["DYNAMIC_TRAILING_SL"] = NewDynamicTrailingSLStrategy(DefaultDynamicTrailingSLConfig())
	rm.stratToRRStrategy["LOW_VOLUME"] = "PARTIAL_BOOK_COST_SL"
	rm.stratToRRStrategy["VANDE_BHARAT"] = "DYNAMIC_TRAILING_SL"

	return rm
}

// SetRiskRewardStrategies sets modular RR strategies and attachments
func (rm *RiskManager) SetRiskRewardStrategies(strategies map[string]RiskRewardStrategy, stratToRR map[string]string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if strategies != nil {
		rm.rrStrategies = strategies
	}
	if stratToRR != nil {
		rm.stratToRRStrategy = stratToRR
	}
}

// GetStrategyForPosition returns the RiskRewardStrategy for a given trading strategy name
func (rm *RiskManager) GetStrategyForPosition(strategyName string) RiskRewardStrategy {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	rrName := rm.stratToRRStrategy[strategyName]
	if rr, ok := rm.rrStrategies[rrName]; ok {
		return rr
	}
	if rr, ok := rm.rrStrategies["DYNAMIC_TRAILING_SL"]; ok {
		return rr
	}
	if rr, ok := rm.rrStrategies["PARTIAL_BOOK_COST_SL"]; ok {
		return rr
	}
	return NewDynamicTrailingSLStrategy(DefaultDynamicTrailingSLConfig())
}

// RestoreTradesToday sets the initial trades count and P&L on startup recovery
func (rm *RiskManager) RestoreTradesToday(count int, pnl float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.tradestoday = count
	rm.dailyPnL = pnl

	if rm.limits.MaxDailyLossAmount > 0 && rm.dailyPnL <= -rm.limits.MaxDailyLossAmount {
		rm.circuitBreakerHit = true
		rm.logger.Error("CIRCUIT BREAKER TRIGGERED ON STARTUP: Restored daily loss limit exceeded",
			zap.Float64("restored_daily_pnl", rm.dailyPnL),
			zap.Float64("max_daily_loss_amount", rm.limits.MaxDailyLossAmount),
		)
	}
}

// SetMaxTradesPerDay dynamically updates the max trades limit for equity trading
func (rm *RiskManager) SetMaxTradesPerDay(maxTrades int) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if maxTrades > 0 {
		rm.limits.MaxTradesPerDay = maxTrades
	}
}

// SetMaxHoldingTimeMin dynamically updates the max holding time limit in minutes
func (rm *RiskManager) SetMaxHoldingTimeMin(min int) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if min > 0 {
		rm.limits.MaxHoldingTimeMin = min
	}
}

// SetMaxDailyLossAmount dynamically updates the max daily loss threshold
func (rm *RiskManager) SetMaxDailyLossAmount(amount float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.limits.MaxDailyLossAmount = amount
}

// SetMaxLossStreaks dynamically updates the max consecutive loss streaks limit
func (rm *RiskManager) SetMaxLossStreaks(streaks int) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if streaks > 0 {
		rm.limits.MaxLossStreaks = streaks
	}
}

// CanPlaceOrder performs pre-trade risk checks
func (rm *RiskManager) CanPlaceOrder(quantity int, price float64) bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	checks := []struct {
		condition bool
		reason    string
	}{
		{rm.tradestoday < rm.limits.MaxTradesPerDay, "Max trades per day reached"},
		{rm.lossStreaks < rm.limits.MaxLossStreaks, "Loss streak limit exceeded"},
		{rm.limits.MaxDailyLossAmount <= 0 || rm.dailyPnL > -rm.limits.MaxDailyLossAmount, "Daily loss limit exceeded"},
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

// HasOpenPosition checks if there is already an open position for a given symbol
func (rm *RiskManager) HasOpenPosition(symbol string) bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	for _, pos := range rm.openPositions {
		if pos.Symbol == symbol {
			return true
		}
	}
	return false
}

// AddOpenPosition tracks a new position with its stop-loss and pre-calculated target
func (rm *RiskManager) AddOpenPosition(orderID string, symbol string, token int64, qty int, entryPrice float64, side string, sl float64, strategy string, target1 float64, createdAt time.Time) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	actualCreatedAt := createdAt
	if actualCreatedAt.IsZero() {
		actualCreatedAt = time.Now()
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
		CreatedAt:         actualCreatedAt,
		LatestPrice:       entryPrice,
		HighestPrice:      entryPrice,
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

	if exitQty <= 0 {
		return
	}

	// Calculate P&L
	var pnl float64
	if pos.Side == "BUY" {
		pnl = (exitPrice - pos.EntryPrice) * float64(exitQty)
	} else {
		pnl = (pos.EntryPrice - exitPrice) * float64(exitQty)
	}

	timeHeld := int(math.Round(time.Since(pos.CreatedAt).Minutes()))

	trade := ClosedTrade{
		Symbol:      pos.Symbol,
		Entry:       pos.EntryPrice,
		Exit:        exitPrice,
		Quantity:    exitQty,
		PnL:         pnl,
		Side:        pos.Side,
		TimeHeldMin: timeHeld,
		EntryTime:   pos.CreatedAt,
		ExitTime:    time.Now(),
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

	if rm.limits.MaxDailyLossAmount > 0 && rm.dailyPnL <= -rm.limits.MaxDailyLossAmount {
		rm.circuitBreakerHit = true
		rm.logger.Error("CIRCUIT BREAKER TRIGGERED: Daily loss limit exceeded",
			zap.Float64("daily_pnl", rm.dailyPnL),
			zap.Float64("max_daily_loss_amount", rm.limits.MaxDailyLossAmount),
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

// RoundTick rounds a price to the nearest tick size (default 0.05) and trims IEEE 754 binary float noise
func RoundTick(price float64, tickSize float64) float64 {
	if tickSize <= 0 {
		tickSize = 0.05
	}
	ticks := math.Round(price / tickSize)
	return math.Round(ticks*tickSize*100.0) / 100.0
}

// CheckTrailingSL evaluates trailing stop loss, target 1 partial exit, and time-decay guards
func (rm *RiskManager) CheckTrailingSL(orderID string, currentPrice float64) string {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	pos, exists := rm.openPositions[orderID]
	if !exists {
		return ""
	}

	holdTimeMin := int(time.Since(pos.CreatedAt).Minutes())
	const tickSize = 0.05

	// 1. Evaluate using the assigned RiskRewardStrategy
	rrName := rm.stratToRRStrategy[pos.Strategy]
	rrStrat := rm.rrStrategies[rrName]
	if rrStrat == nil {
		rrStrat = rm.rrStrategies["DYNAMIC_TRAILING_SL"]
	}
	if rrStrat == nil {
		rrStrat = rm.rrStrategies["PARTIAL_BOOK_COST_SL"]
	}

	if rrStrat != nil {
		action := rrStrat.EvaluatePosition(pos, currentPrice, holdTimeMin, tickSize)
		if action != "" {
			if action == "PARTIAL_EXIT" {
				rm.logger.Info("Partial exit triggered by Risk-Reward strategy",
					zap.String("symbol", pos.Symbol),
					zap.String("strategy", pos.Strategy),
					zap.String("rr_strategy", rrStrat.Name()),
					zap.Float64("entry", pos.EntryPrice),
					zap.Float64("new_sl", pos.SLPrice),
				)
			}
			return action
		}
	}

	// 2. Check Stop-Loss breach (skip if broker-side SL order is handling it)
	if pos.BrokerSLOrderID == "" {
		if pos.Side == "BUY" && currentPrice <= pos.SLPrice {
			rm.logger.Warn("SL breach BUY", zap.String("symbol", pos.Symbol), zap.Float64("sl", pos.SLPrice))
			return "CLOSE"
		}

		if pos.Side == "SELL" && currentPrice >= pos.SLPrice {
			rm.logger.Warn("SL breach SELL", zap.String("symbol", pos.Symbol), zap.Float64("sl", pos.SLPrice))
			return "CLOSE"
		}
	}

	// 3. Check time limit
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

	timeHeld := int(math.Round(time.Since(pos.CreatedAt).Minutes()))

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
		EntryTime:   pos.CreatedAt,
		ExitTime:    time.Now(),
		CreatedAt:   time.Now(),
		Strategy:    pos.Strategy,
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
	if rm.db == nil {
		return
	}
	strategyName := "LOW_VOLUME"
	if trade.Strategy != "" {
		strategyName = trade.Strategy
	}
	query := `
		INSERT INTO trades (symbol, entry_price, exit_price, quantity, pnl, side, time_held_minutes, entry_time, exit_time, created_at, strategy)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err := rm.db.Exec(query, trade.Symbol, trade.Entry, trade.Exit, trade.Quantity,
		trade.PnL, trade.Side, trade.TimeHeldMin, trade.EntryTime, trade.ExitTime, trade.CreatedAt, strategyName)

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

// SetBrokerSLOrderID associates a broker-side stop-loss order ID with the position
func (rm *RiskManager) SetBrokerSLOrderID(entryOrderID string, slOrderID string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if pos, exists := rm.openPositions[entryOrderID]; exists {
		pos.BrokerSLOrderID = slOrderID
	}
}

// UpdatePositionQuantity updates the quantity of an open position (e.g. for partial fills)
func (rm *RiskManager) UpdatePositionQuantity(entryOrderID string, qty int) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if pos, exists := rm.openPositions[entryOrderID]; exists {
		pos.Quantity = qty
	}
}
