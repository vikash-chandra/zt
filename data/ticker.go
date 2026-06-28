package data

import (
	"context"
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
}

// NewRobustKiteTicker creates a new ticker instance
func NewRobustKiteTicker(apiKey, accessToken string, logger *zap.Logger) *RobustKiteTicker {
	return &RobustKiteTicker{
		apiKey:               apiKey,
		accessToken:          accessToken,
		logger:               logger,
		tickBuffer:           make(map[int64]*Tick),
		lastTickTime:         make(map[int64]float64),
		maxReconnectAttempts: 5,
		reconnectDelay:       1 * time.Second,
	}
}

// Connect establishes WebSocket connection (falls back to mock if token is placeholder)
func (kt *RobustKiteTicker) Connect(ctx context.Context, instrumentTokens []int64) error {
	if kt.accessToken == "" || kt.accessToken == "your_access_token_here" {
		kt.logger.Warn("KITE_ACCESS_TOKEN is not configured. Starting in Mock/Simulated Ticker mode...")
		kt.connected = true
		go kt.mockTickerLoop(ctx, instrumentTokens)
		return nil
	}

	kt.logger.Info("KITE_ACCESS_TOKEN is configured. Connecting to live Zerodha WebSocket ticker...", zap.String("api_key", kt.apiKey))

	// Initialize the official Zerodha WebSocket ticker client
	ticker := kiteticker.New(kt.apiKey, kt.accessToken)

	// Assign callbacks using setter methods
	ticker.OnConnect(func() {
		kt.logger.Info("Successfully connected to Zerodha WebSocket! Subscribing to instruments...", zap.Int("count", len(instrumentTokens)))
		kt.connected = true
		kt.reconnectAttempts = 0

		// Convert int64 tokens to uint32 for the SDK
		uintTokens := make([]uint32, len(instrumentTokens))
		for i, v := range instrumentTokens {
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
		kt.logger.Warn("Zerodha WebSocket connection closed", zap.Int("code", code), zap.String("reason", reason))
		kt.connected = false
	})

	ticker.OnError(func(err error) {
		kt.logger.Error("Zerodha WebSocket error", zap.Error(err))
	})

	ticker.OnReconnect(func(attempt int, delay time.Duration) {
		kt.logger.Info("Reconnecting to Zerodha WebSocket...", zap.Int("attempt", attempt), zap.Duration("delay", delay))
	})

	ticker.OnTick(func(tick models.Tick) {
		// Find bid/ask price
		bid := tick.LastPrice
		ask := tick.LastPrice
		if len(tick.Depth.Buy) > 0 {
			bid = tick.Depth.Buy[0].Price
		}
		if len(tick.Depth.Sell) > 0 {
			ask = tick.Depth.Sell[0].Price
		}

		t := &Tick{
			Token:     int64(tick.InstrumentToken),
			LTP:       tick.LastPrice,
			Bid:       bid,
			Ask:       ask,
			Volume:    int64(tick.VolumeTraded),
			OI:        int64(tick.OI),
			Timestamp: float64(tick.Timestamp.Unix()),
		}
		kt.processTick(t)
	})

	kt.ticker = ticker

	// Serve the WebSocket loop in a background goroutine
	go ticker.Serve()

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

// GetMetrics returns ticker health metrics
func (kt *RobustKiteTicker) GetMetrics() (int64, int64) {
	kt.mu.RLock()
	defer kt.mu.RUnlock()

	return kt.ticksReceived, kt.packetLoss
}

// Close closes the WebSocket connection
func (kt *RobustKiteTicker) Close() error {
	kt.connected = false
	if kt.ticker != nil {
		kt.ticker.Close()
		kt.logger.Info("Live Ticker disconnected")
	} else {
		kt.logger.Info("Mock Ticker disconnected")
	}
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
