package data

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zerodha/gokiteconnect/v4/models"
	kiteticker "github.com/zerodha/gokiteconnect/v4/ticker"
	"go.uber.org/zap"
)

// RobustKiteTicker handles WebSocket connection to Kite ticker
type RobustKiteTicker struct {
	apiKey               string
	accessToken          string
	logger               *zap.Logger
	ticker               *kiteticker.Ticker
	tickBuffer           map[int64]*Tick
	mu                   sync.RWMutex
	ticksReceived        int64
	packetLoss           int64
	lastTickTime         map[int64]float64
	maxReconnectAttempts int
	reconnectAttempts    int
	reconnectDelay       time.Duration
	connected            bool
	subscribedTokens     map[int64]bool
	subMu                sync.RWMutex
}

// NewRobustKiteTicker creates a new ticker instance
func NewRobustKiteTicker(apiKey, accessToken string, logger *zap.Logger) *RobustKiteTicker {
	return &RobustKiteTicker{
		apiKey:               apiKey,
		accessToken:          accessToken,
		logger:               logger,
		tickBuffer:           make(map[int64]*Tick),
		lastTickTime:         make(map[int64]float64),
		subscribedTokens:     make(map[int64]bool),
		maxReconnectAttempts: 5,
		reconnectDelay:       1 * time.Second,
	}
}

// Connect establishes WebSocket connection using Zerodha Kite API
func (kt *RobustKiteTicker) Connect(ctx context.Context, instrumentTokens []int64) error {
	if kt.accessToken == "" || kt.accessToken == "your_access_token_here" {
		return fmt.Errorf("KITE_ACCESS_TOKEN is not configured; live connection requires a valid token")
	}

	kt.logger.Info("Connecting to live Zerodha WebSocket ticker...", zap.String("api_key", kt.apiKey))

	// Populate/merge initial tokens into our tracked subscriptions map
	kt.subMu.Lock()
	for _, token := range instrumentTokens {
		kt.subscribedTokens[token] = true
	}
	kt.subMu.Unlock()

	// Initialize the official Zerodha WebSocket ticker client
	ticker := kiteticker.New(kt.apiKey, kt.accessToken)

	// Assign callbacks using setter methods
	ticker.OnConnect(func() {
		kt.mu.Lock()
		isActive := (ticker == kt.ticker)
		if isActive {
			kt.connected = true
		}
		kt.mu.Unlock()
		if !isActive {
			return
		}

		// Retrieve all active subscribed tokens from map (preserves dynamic changes)
		kt.subMu.RLock()
		activeTokens := make([]int64, 0, len(kt.subscribedTokens))
		for token := range kt.subscribedTokens {
			activeTokens = append(activeTokens, token)
		}
		kt.subMu.RUnlock()

		kt.logger.Info("Successfully connected to Zerodha WebSocket! Subscribing to instruments...", zap.Int("count", len(activeTokens)))
		kt.reconnectAttempts = 0

		// Convert int64 tokens to uint32 for the SDK
		uintTokens := make([]uint32, len(activeTokens))
		for i, v := range activeTokens {
			uintTokens[i] = uint32(v)
		}

		// Subscribe to standard Quote mode (contains LTP, Volume, Bid/Ask)
		if err := ticker.Subscribe(uintTokens); err != nil {
			kt.logger.Error("Failed to subscribe to tokens", zap.Error(err))
		}
		if err := ticker.SetMode(kiteticker.ModeQuote, uintTokens); err != nil {
			kt.logger.Error("Failed to set ticker mode", zap.Error(err))
		}
	})

	ticker.OnClose(func(code int, reason string) {
		kt.mu.Lock()
		isActive := (ticker == kt.ticker)
		if isActive {
			kt.connected = false
		}
		kt.mu.Unlock()
		if !isActive {
			return
		}

		kt.logger.Warn("Zerodha WebSocket connection closed", zap.Int("code", code), zap.String("reason", reason))
	})

	ticker.OnError(func(err error) {
		kt.mu.RLock()
		isActive := (ticker == kt.ticker)
		kt.mu.RUnlock()
		if !isActive {
			return
		}

		kt.logger.Error("Zerodha WebSocket error", zap.Error(err))
	})

	ticker.OnReconnect(func(attempt int, delay time.Duration) {
		kt.mu.RLock()
		isActive := (ticker == kt.ticker)
		kt.mu.RUnlock()
		if !isActive {
			return
		}

		kt.logger.Info("Reconnecting to Zerodha WebSocket...", zap.Int("attempt", attempt), zap.Duration("delay", delay))
	})

	ticker.OnTick(func(tick models.Tick) {
		kt.mu.RLock()
		isActive := (ticker == kt.ticker)
		kt.mu.RUnlock()
		if !isActive {
			return
		}

		// Find bid/ask price
		bid := tick.LastPrice
		ask := tick.LastPrice
		if len(tick.Depth.Buy) > 0 {
			bid = tick.Depth.Buy[0].Price
		}
		if len(tick.Depth.Sell) > 0 {
			ask = tick.Depth.Sell[0].Price
		}

		tickTime := tick.Timestamp.Time
		if tickTime.IsZero() {
			tickTime = time.Now()
		}

		t := &Tick{
			Token:     int64(tick.InstrumentToken),
			LTP:       tick.LastPrice,
			Bid:       bid,
			Ask:       ask,
			Volume:    int64(tick.VolumeTraded),
			OI:        int64(tick.OI),
			Timestamp: float64(tickTime.Unix()),
		}
		kt.processTick(t)
	})

	kt.ticker = ticker

	// Serve the WebSocket loop in a background goroutine
	go ticker.Serve()

	return nil
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

// GetMetrics returns ticker health metrics
func (kt *RobustKiteTicker) GetMetrics() (int64, int64) {
	kt.mu.RLock()
	defer kt.mu.RUnlock()

	return kt.ticksReceived, kt.packetLoss
}

// Close closes the WebSocket connection cleanly without triggering background reconnect loops
func (kt *RobustKiteTicker) Close() error {
	kt.mu.Lock()
	kt.connected = false
	kt.mu.Unlock()
	if kt.ticker != nil {
		kt.ticker.Close()
	}
	kt.logger.Info("Ticker disconnected")
	return nil
}

// IsConnected checks if ticker is connected
func (kt *RobustKiteTicker) IsConnected() bool {
	kt.mu.RLock()
	defer kt.mu.RUnlock()
	return kt.connected
}

// Reconnect handles reconnection logic
func (kt *RobustKiteTicker) Reconnect(ctx context.Context, tokens []int64) error {
	if kt.reconnectAttempts >= kt.maxReconnectAttempts {
		kt.logger.Error("Max reconnection attempts reached")
		return nil
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

// Subscribe adds new tokens to the active WebSocket subscription dynamically without reconnecting
func (kt *RobustKiteTicker) Subscribe(tokens []int64) error {
	kt.mu.Lock()
	defer kt.mu.Unlock()

	if kt.ticker == nil || !kt.connected {
		return fmt.Errorf("ticker is not connected")
	}

	if len(tokens) == 0 {
		return nil
	}

	// Update our tracked subscriptions map (thread-safe)
	kt.subMu.Lock()
	for _, tok := range tokens {
		kt.subscribedTokens[tok] = true
	}
	kt.subMu.Unlock()

	uintTokens := make([]uint32, len(tokens))
	for i, v := range tokens {
		uintTokens[i] = uint32(v)
	}

	if err := kt.ticker.Subscribe(uintTokens); err != nil {
		return fmt.Errorf("failed to subscribe to new tokens: %w", err)
	}
	if err := kt.ticker.SetMode(kiteticker.ModeQuote, uintTokens); err != nil {
		return fmt.Errorf("failed to set ticker mode: %w", err)
	}

	kt.logger.Info("Dynamically subscribed to new instruments", zap.Int("count", len(tokens)))
	return nil
}
