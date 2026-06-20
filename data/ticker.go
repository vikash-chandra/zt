package data

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// RobustKiteTicker handles WebSocket connection to Kite ticker
type RobustKiteTicker struct {
	token                string
	logger               *zap.Logger
	tickBuffer           map[int64]*Tick
	mu                   sync.RWMutex
	ticksReceived        int64
	packetLoss           int64
	lastTickTime         map[int64]float64
	maxReconnectAttempts int
	reconnectAttempts    int
	reconnectDelay       time.Duration
	connected            bool
}

// NewRobustKiteTicker creates a new ticker instance
func NewRobustKiteTicker(token string, logger *zap.Logger) *RobustKiteTicker {
	return &RobustKiteTicker{
		token:                token,
		logger:               logger,
		tickBuffer:           make(map[int64]*Tick),
		lastTickTime:         make(map[int64]float64),
		maxReconnectAttempts: 5,
		reconnectDelay:       1 * time.Second,
	}
}

// Connect establishes WebSocket connection (stub for real Kite ticker)
func (kt *RobustKiteTicker) Connect(ctx context.Context, instrumentTokens []int64) error {
	// In production, this would connect to: wss://ws.kite.trade
	// For this blueprint, we'll simulate with a ticker
	kt.connected = true
	kt.logger.Info("Mock ticker connected", zap.Int("instruments", len(instrumentTokens)))

	// Start mock ticker
	go kt.mockTickerLoop(ctx, instrumentTokens)

	return nil
}

// mockTickerLoop simulates incoming ticks for demo purposes
func (kt *RobustKiteTicker) mockTickerLoop(ctx context.Context, tokens []int64) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, token := range tokens {
				tick := &Tick{
					Token:     token,
					LTP:       50000 + float64(token%1000),
					Bid:       49999,
					Ask:       50001,
					Volume:    1000 + int64(token%5000),
					OI:        100000,
					Timestamp: float64(time.Now().Unix()),
				}
				kt.processTick(tick)
			}
		}
	}
}

// processTick processes an incoming tick
func (kt *RobustKiteTicker) processTick(tick *Tick) {
	kt.mu.Lock()
	defer kt.mu.Unlock()

	// Detect packet loss
	if lastTime, exists := kt.lastTickTime[tick.Token]; exists {
		gap := tick.Timestamp - lastTime
		if gap > 1.0 { // > 1 second gap
			kt.logger.Warn("Potential packet loss", zap.Int64("token", tick.Token), zap.Float64("gap_sec", gap))
			kt.packetLoss++
		}
	}

	kt.lastTickTime[tick.Token] = tick.Timestamp
	kt.tickBuffer[tick.Token] = tick
	kt.ticksReceived++
}

// GetLatestTick returns latest tick for instrument
func (kt *RobustKiteTicker) GetLatestTick(token int64) *Tick {
	kt.mu.RLock()
	defer kt.mu.RUnlock()

	if tick, exists := kt.tickBuffer[token]; exists {
		return tick
	}
	return nil
}

// GetMetrics returns ticker metrics
func (kt *RobustKiteTicker) GetMetrics() (ticksReceived, packetLoss int64) {
	kt.mu.RLock()
	defer kt.mu.RUnlock()

	return kt.ticksReceived, kt.packetLoss
}

// Close closes the WebSocket connection
func (kt *RobustKiteTicker) Close() error {
	kt.connected = false
	kt.logger.Info("Ticker disconnected")
	return nil
}

// IsConnected checks if ticker is connected
func (kt *RobustKiteTicker) IsConnected() bool {
	return kt.connected
}

// Reconnect handles reconnection logic
func (kt *RobustKiteTicker) Reconnect(ctx context.Context, tokens []int64) error {
	if kt.reconnectAttempts >= kt.maxReconnectAttempts {
		kt.logger.Error("Max reconnection attempts reached")
		return websocket.ErrCloseSent
	}

	kt.reconnectAttempts++
	delay := time.Duration(1<<uint(kt.reconnectAttempts)) * kt.reconnectDelay
	if delay > 60*time.Second {
		delay = 60 * time.Second
	}

	kt.logger.Info("Reconnecting ticker", zap.Int("attempt", kt.reconnectAttempts), zap.Duration("delay", delay))
	time.Sleep(delay)

	return kt.Connect(ctx, tokens)
}
