package risk

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"zerodha-trading/data"

	"go.uber.org/zap"
)

// OptionsPosition holds live options trade properties
type OptionsPosition struct {
	OrderID      string    `json:"order_id"`
	Symbol       string    `json:"symbol"`
	Side         string    `json:"side"` // "SELL" for option selling
	OptionType   string    `json:"option_type"` // "PE" or "CE"
	Quantity     int       `json:"quantity"`
	EntryPremium float64   `json:"entry_premium"`
	SLPrice      float64   `json:"sl_price"` // 1.5x Entry Premium (50% loss)
	LatestPrice  float64   `json:"latest_price"`
	LowestPrice  float64   `json:"lowest_price"` // Lowest premium reached (profit peak)
	CreatedAt    time.Time `json:"created_at"`
	Expiry       string    `json:"expiry"`
}

// GetUpcomingOptionExpiry calculates the next Thursday weekly expiry date in IST
func GetUpcomingOptionExpiry(t time.Time) string {
	loc, _ := time.LoadLocation("Asia/Kolkata")
	if loc == nil {
		loc = time.Local
	}
	t = t.In(loc)
	daysUntilThursday := (int(time.Thursday) - int(t.Weekday()) + 7) % 7
	if daysUntilThursday == 0 && t.Hour() >= 15 {
		daysUntilThursday = 7
	}
	expiryDate := t.AddDate(0, 0, daysUntilThursday)
	return expiryDate.Format("02-Jan-2006")
}

// OptionsPositionManager handles options trade state, dynamic multipliers, 50% SL, and post-SL reversal guard
type OptionsPositionManager struct {
	mu                   sync.RWMutex
	logger               *zap.Logger
	db                   *data.Database
	baseLotSize          int
	maxMultiplier        int
	slPct                float64
	multiplier           int
	lastTrend            string
	slStoppedTrend       string
	awaitingReversal     bool
	paperBalance         float64
	activePosition       *OptionsPosition
}

// NewOptionsPositionManager creates a new OptionsPositionManager
func NewOptionsPositionManager(db *data.Database, logger *zap.Logger, baseLotSize, maxMultiplier int, slPct float64, initialBalance float64) *OptionsPositionManager {
	return &OptionsPositionManager{
		db:               db,
		logger:           logger,
		baseLotSize:      baseLotSize,
		maxMultiplier:    maxMultiplier,
		slPct:            slPct,
		multiplier:       1,
		lastTrend:        "NEUTRAL",
		slStoppedTrend:   "",
		awaitingReversal: false,
		paperBalance:     initialBalance,
	}
}

// LoadState restores state from PostgreSQL database or initialized state
func (m *OptionsPositionManager) LoadState(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db == nil {
		return nil
	}

	st, err := m.db.GetOptionsBotState(ctx)
	if err != nil {
		return err
	}

	m.multiplier = st.Multiplier
	if m.multiplier <= 0 {
		m.multiplier = 1
	}
	m.lastTrend = st.LastTrend
	m.slStoppedTrend = st.SLStoppedTrend
	m.awaitingReversal = st.AwaitingReversal
	if st.PaperBalance > 0 {
		m.paperBalance = st.PaperBalance
	}

	if st.ActiveOrderID != "" && st.ActiveSymbol != "" {
		loc, err := time.LoadLocation("Asia/Kolkata")
		if err != nil {
			loc = time.Local
		}
		createdAt := st.UpdatedAt.In(loc)
		if strings.HasPrefix(st.ActiveOrderID, "PAPER-") {
			rawTsStr := strings.TrimPrefix(st.ActiveOrderID, "PAPER-")
			if unixTs, err := strconv.ParseInt(rawTsStr, 10, 64); err == nil && unixTs > 0 {
				if unixTs > 1e18 {
					unixTs = unixTs / 1e9
				} else if unixTs > 1e11 {
					unixTs = unixTs / 1000
				}
				createdAt = time.Unix(unixTs, 0).In(loc)
			}
		}
		m.activePosition = &OptionsPosition{
			OrderID:      st.ActiveOrderID,
			Symbol:       st.ActiveSymbol,
			Side:         st.ActiveSide,
			Quantity:     st.ActiveQty,
			EntryPremium: st.EntryPremium,
			SLPrice:      st.SLPrice,
			LatestPrice:  st.EntryPremium,
			LowestPrice:  st.EntryPremium,
			CreatedAt:    createdAt,
		}
	}

	return nil
}

// SaveState persists current options state to PostgreSQL database
func (m *OptionsPositionManager) SaveState(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.db == nil {
		return nil
	}

	st := &data.OptionsBotState{
		ID:               1,
		Multiplier:       m.multiplier,
		LastTrend:        m.lastTrend,
		SLStoppedTrend:   m.slStoppedTrend,
		AwaitingReversal: m.awaitingReversal,
		PaperBalance:     m.paperBalance,
	}

	if m.activePosition != nil {
		st.ActiveOrderID = m.activePosition.OrderID
		st.ActiveSymbol = m.activePosition.Symbol
		st.ActiveSide = m.activePosition.Side
		st.ActiveQty = m.activePosition.Quantity
		st.EntryPremium = m.activePosition.EntryPremium
		st.SLPrice = m.activePosition.SLPrice
	}

	return m.db.SaveOptionsBotState(ctx, st)
}

// EvaluateSignal evaluates a 5m candle close trend signal and determines trade actions
// Returns action: "OPEN_INITIAL", "REVERSAL", "IGNORE", "NONE"
func (m *OptionsPositionManager) EvaluateSignal(trend string) (string, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if trend == "NEUTRAL" {
		return "NONE", 0
	}

	// 1. Post-SL Reversal Guard: If stopped out by SL, block re-entry until a complete trend reversal occurs
	if m.awaitingReversal {
		if trend == m.slStoppedTrend {
			m.logger.Info("Post-SL Reversal Guard Active: ignoring same trend signal",
				zap.String("trend", trend),
				zap.String("sl_stopped_trend", m.slStoppedTrend),
			)
			return "IGNORE", 0
		}
		// Trend has reversed! Clear post-SL guard and reset multiplier to 1
		m.logger.Info("Trend complete reversal detected post-SL! Clearing cooldown guard.",
			zap.String("old_trend", m.slStoppedTrend),
			zap.String("new_trend", trend),
		)
		m.awaitingReversal = false
		m.slStoppedTrend = ""
		m.multiplier = 1
		m.lastTrend = trend
		qty := m.baseLotSize * m.multiplier
		return "OPEN_INITIAL", qty
	}

	// 2. Initial Entry: No active position
	if m.activePosition == nil {
		m.lastTrend = trend
		qty := m.baseLotSize * m.multiplier
		return "OPEN_INITIAL", qty
	}

	// 3. Trend Reversal: Active position exists and trend flips opposite (e.g. BULLISH -> BEARISH)
	if m.lastTrend != "NEUTRAL" && trend != m.lastTrend {
		// Increment multiplier up to max cap
		if m.multiplier < m.maxMultiplier {
			m.multiplier++
		}
		m.logger.Info("Trend Reversal Triggered! Incrementing multiplier.",
			zap.String("old_trend", m.lastTrend),
			zap.String("new_trend", trend),
			zap.Int("new_multiplier", m.multiplier),
		)
		m.lastTrend = trend
		qty := m.baseLotSize * m.multiplier
		return "REVERSAL", qty
	}

	return "NONE", 0
}

// ResetDailyMultiplier resets lot multiplier back to 1 on a new trading day
func (m *OptionsPositionManager) ResetDailyMultiplier() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.multiplier = 1
}

// OnTradeOpened registers a new open options position
func (m *OptionsPositionManager) OnTradeOpened(orderID, symbol, optionType string, qty int, entryPremium float64, entryTime ...time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	createdTime := time.Now()
	if len(entryTime) > 0 && !entryTime[0].IsZero() {
		createdTime = entryTime[0]
	}

	// Calculate 50% SL (1.5x of Entry Premium for Option Sellers)
	slPrice := math.Round((entryPremium*1.5)*100.0) / 100.0

	m.activePosition = &OptionsPosition{
		OrderID:      orderID,
		Symbol:       symbol,
		Side:         "SELL",
		OptionType:   optionType,
		Quantity:     qty,
		EntryPremium: entryPremium,
		SLPrice:      slPrice,
		LatestPrice:  entryPremium,
		LowestPrice:  entryPremium,
		CreatedAt:    createdTime,
		Expiry:       GetUpcomingOptionExpiry(createdTime),
	}

	m.logger.Info("Options Trade Registered",
		zap.String("order_id", orderID),
		zap.String("symbol", symbol),
		zap.Int("qty", qty),
		zap.Float64("entry_premium", entryPremium),
		zap.Float64("sl_price", slPrice),
		zap.String("expiry", m.activePosition.Expiry),
		zap.Time("created_at", createdTime),
	)
}

// UpdateLTP dynamically updates the current price (LTP) of the active options position
func (m *OptionsPositionManager) UpdateLTP(ltp float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.activePosition != nil && ltp > 0 {
		m.activePosition.LatestPrice = ltp
		if ltp < m.activePosition.LowestPrice || m.activePosition.LowestPrice == 0 {
			m.activePosition.LowestPrice = ltp
		}
	}
}



// FetchRealLTPFromBroker queries Zerodha API directly via GetQuote for the active option symbol's real market LTP
func (m *OptionsPositionManager) FetchRealLTPFromBroker(broker data.BrokerClient) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.activePosition == nil || broker == nil {
		return false
	}

	sym := m.activePosition.Symbol
	var strike float64
	var optType string
	if len(sym) >= 2 {
		optType = sym[len(sym)-2:]
	}
	var numStr string
	for _, ch := range sym {
		if ch >= '0' && ch <= '9' {
			numStr += string(ch)
		}
	}
	if len(numStr) > 0 {
		fmt.Sscanf(numStr, "%f", &strike)
	}

	keysToTry := []string{"NFO:" + sym}
	if strike > 0 && (optType == "PE" || optType == "CE") {
		keysToTry = append(keysToTry,
			fmt.Sprintf("NFO:NIFTY26806%.0f%s", strike, optType),
			fmt.Sprintf("NFO:NIFTY26AUG%.0f%s", strike, optType),
			fmt.Sprintf("NFO:NIFTY26813%.0f%s", strike, optType),
		)
	}

	quotes, err := broker.GetQuote(keysToTry...)
	if err == nil && len(quotes) > 0 {
		for _, key := range keysToTry {
			if q, ok := quotes[key]; ok && q.LastPrice > 0 {
				m.activePosition.LatestPrice = q.LastPrice
				if q.LastPrice < m.activePosition.LowestPrice || m.activePosition.LowestPrice == 0 {
					m.activePosition.LowestPrice = q.LastPrice
				}
				return true
			}
		}
	}
	return false
}

// CheckTickEvaluates options 1-second WebSocket ticks for 50% SL hit
// Returns true if SL is breached
func (m *OptionsPositionManager) CheckTick(optionLTP float64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.activePosition == nil {
		return false
	}

	m.activePosition.LatestPrice = optionLTP
	if optionLTP < m.activePosition.LowestPrice || m.activePosition.LowestPrice == 0 {
		m.activePosition.LowestPrice = optionLTP
	}

	// 50% Premium SL breach check (Since we are SELLING options, price rising >= SLPrice is a loss!)
	if optionLTP >= m.activePosition.SLPrice {
		m.logger.Warn("50% Options Premium Stop-Loss BREACHED!",
			zap.String("symbol", m.activePosition.Symbol),
			zap.Float64("entry", m.activePosition.EntryPremium),
			zap.Float64("ltp", optionLTP),
			zap.Float64("sl", m.activePosition.SLPrice),
		)
		return true
	}

	return false
}

// OnSLHit handles Stop-Loss execution cleanup: resets multiplier to 1 & activates reversal guard
func (m *OptionsPositionManager) OnSLHit(exitPremium float64) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.activePosition == nil {
		return 0
	}

	// PnL for Option Selling = (EntryPremium - ExitPremium) * Quantity
	pnl := (m.activePosition.EntryPremium - exitPremium) * float64(m.activePosition.Quantity)
	m.paperBalance += pnl

	m.logger.Info("Options 50% SL Exited",
		zap.String("symbol", m.activePosition.Symbol),
		zap.Float64("pnl", pnl),
		zap.Int("reset_multiplier", 1),
	)

	// Reset multiplier to 1 and require a complete trend reversal before re-entering
	m.multiplier = 1
	m.slStoppedTrend = m.lastTrend
	m.awaitingReversal = true
	m.activePosition = nil

	return pnl
}

// OnTradeClosed handles normal profit exit or reversal squareoff
func (m *OptionsPositionManager) OnTradeClosed(exitPremium float64) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.activePosition == nil {
		return 0
	}

	pnl := (m.activePosition.EntryPremium - exitPremium) * float64(m.activePosition.Quantity)
	m.paperBalance += pnl

	m.logger.Info("Options Trade Closed",
		zap.String("symbol", m.activePosition.Symbol),
		zap.Float64("pnl", pnl),
	)

	m.activePosition = nil
	return pnl
}

// GetStatus returns thread-safe options manager status snapshot
func (m *OptionsPositionManager) GetStatus() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := map[string]interface{}{
		"multiplier":         m.multiplier,
		"base_lot_size":      m.baseLotSize,
		"last_trend":         m.lastTrend,
		"sl_stopped_trend":   m.slStoppedTrend,
		"awaiting_reversal":  m.awaitingReversal,
		"paper_balance":      m.paperBalance,
		"has_active_trade":   m.activePosition != nil,
	}

	if m.activePosition != nil {
		pnl := (m.activePosition.EntryPremium - m.activePosition.LatestPrice) * float64(m.activePosition.Quantity)
		res["active_symbol"] = m.activePosition.Symbol
		res["active_side"] = m.activePosition.Side
		res["active_qty"] = m.activePosition.Quantity
		res["entry_premium"] = m.activePosition.EntryPremium
		res["latest_price"] = m.activePosition.LatestPrice
		res["sl_price"] = m.activePosition.SLPrice
		res["unrealized_pnl"] = fmt.Sprintf("%.2f", pnl)
	}

	return res
}

// GetActivePosition returns a copy of the active options position if present
func (m *OptionsPositionManager) GetActivePosition() *OptionsPosition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.activePosition == nil {
		return nil
	}
	cpy := *m.activePosition
	return &cpy
}
