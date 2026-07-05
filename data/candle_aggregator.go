package data

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// Tick represents a single market tick
type Tick struct {
	Token     int64   `json:"token"`
	LTP       float64 `json:"ltp"`
	Bid       float64 `json:"bid"`
	Ask       float64 `json:"ask"`
	Volume    int64   `json:"volume"`
	OI        int64   `json:"oi"`
	Timestamp float64 `json:"timestamp"`
}

// Candle represents a candle
type Candle struct {
	Token     int64
	Time      time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    int64
	VWAP      float64
	TickCount int
	Bid       float64
	Ask       float64
	Color     string
}

// CandleState tracks state for in-progress candle
type CandleState struct {
	Open        float64
	High        float64
	Low         float64
	Close       float64
	Volume      int64
	TickCount   int
	VWAPSum     float64
	PVSum       float64
	CandleStart time.Time
	Bid         float64
	Ask         float64
}

// CandleAggregator converts raw ticks into clean OHLCV candles
type CandleAggregator struct {
	db             *Database
	logger         *zap.Logger
	candleInterval time.Duration
	marketOpen     time.Time
	marketClose    time.Time
	tableName      string

	mu               sync.RWMutex
	currentCandles   map[int64]*CandleState
	completedCandles chan *Candle
}

// NewCandleAggregator creates a new candle aggregator
func NewCandleAggregator(db *Database, logger *zap.Logger, intervalSec int, bufferSize int, tableName string) *CandleAggregator {
	return &CandleAggregator{
		db:               db,
		logger:           logger,
		candleInterval:   time.Duration(intervalSec) * time.Second,
		marketOpen:       time.Date(2020, 1, 1, 9, 15, 0, 0, time.UTC),
		marketClose:      time.Date(2020, 1, 1, 15, 30, 0, 0, time.UTC),
		currentCandles:   make(map[int64]*CandleState),
		completedCandles: make(chan *Candle, bufferSize),
		tableName:        tableName,
	}
}

// ProcessTick processes a single tick and returns completed candle if any
func (ca *CandleAggregator) ProcessTick(tick *Tick) *Candle {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	tickTime := time.Unix(int64(tick.Timestamp), 0).UTC()

	// Calculate candle bucket
	candleStart := ca.getCandleStart(tickTime)

	state, exists := ca.currentCandles[tick.Token]
	if !exists {
		state = &CandleState{
			Open:        tick.LTP,
			High:        tick.LTP,
			Low:         tick.LTP,
			Close:       tick.LTP,
			Volume:      tick.Volume,
			TickCount:   1,
			VWAPSum:     tick.LTP * float64(tick.Volume),
			PVSum:       float64(tick.Volume),
			CandleStart: candleStart,
			Bid:         tick.Bid,
			Ask:         tick.Ask,
		}
		ca.currentCandles[tick.Token] = state
		return nil
	}

	// Check if we've moved to a new candle
	if candleStart != state.CandleStart {
		// Finalize previous candle
		completed := ca.finalizeCandle(tick.Token, state)

		// Start new candle
		state = &CandleState{
			Open:        tick.LTP,
			High:        tick.LTP,
			Low:         tick.LTP,
			Close:       tick.LTP,
			Volume:      tick.Volume,
			TickCount:   1,
			VWAPSum:     tick.LTP * float64(tick.Volume),
			PVSum:       float64(tick.Volume),
			CandleStart: candleStart,
			Bid:         tick.Bid,
			Ask:         tick.Ask,
		}
		ca.currentCandles[tick.Token] = state

		return completed
	}

	// Update current candle
	state.Close = tick.LTP
	if tick.LTP > state.High {
		state.High = tick.LTP
	}
	if tick.LTP < state.Low {
		state.Low = tick.LTP
	}
	state.Volume += tick.Volume
	state.VWAPSum += tick.LTP * float64(tick.Volume)
	state.PVSum += float64(tick.Volume)
	state.TickCount++
	state.Bid = tick.Bid
	state.Ask = tick.Ask

	return nil
}

// finalizeCandle completes and persists a candle
func (ca *CandleAggregator) finalizeCandle(token int64, state *CandleState) *Candle {
	vwap := state.Close
	if state.PVSum > 0 {
		vwap = state.VWAPSum / state.PVSum
	}

	color := "DOJI"
	if state.Close > state.Open {
		color = "GREEN"
	} else if state.Close < state.Open {
		color = "RED"
	}

	candle := &Candle{
		Token:     token,
		Time:      state.CandleStart,
		Open:      state.Open,
		High:      state.High,
		Low:       state.Low,
		Close:     state.Close,
		Volume:    state.Volume,
		VWAP:      vwap,
		TickCount: state.TickCount,
		Bid:       state.Bid,
		Ask:       state.Ask,
		Color:     color,
	}

	// Persist to database asynchronously to avoid holding tick aggregation locks
	go ca.persistCandle(candle)

	// Send to channel
	select {
	case ca.completedCandles <- candle:
	default:
		ca.logger.Warn("Completed candles channel full, dropping candle")
	}

	return candle
}

// getCandleStart returns the start time of the candle containing the given time
func (ca *CandleAggregator) getCandleStart(t time.Time) time.Time {
	return t.Truncate(ca.candleInterval)
}

// persistCandle saves candle to database
func (ca *CandleAggregator) persistCandle(candle *Candle) {
	err := ca.db.InsertCandle(ca.tableName, candle.Token, candle.Time, candle.Open, candle.High,
		candle.Low, candle.Close, candle.Volume, candle.VWAP, candle.Bid, candle.Ask, candle.TickCount, candle.Color)
	if err != nil {
		ca.logger.Error("Failed to persist candle", zap.Error(err), zap.Int64("token", candle.Token))
	}
}

// GetCompletedCandles returns channel for completed candles
func (ca *CandleAggregator) GetCompletedCandles() <-chan *Candle {
	return ca.completedCandles
}

// GetCurrentCandle returns current in-progress candle state
func (ca *CandleAggregator) GetCurrentCandle(token int64) *CandleState {
	ca.mu.RLock()
	defer ca.mu.RUnlock()

	if state, exists := ca.currentCandles[token]; exists {
		// Return a copy to avoid data races
		stateCopy := *state
		return &stateCopy
	}
	return nil
}

// GetLastNCandles retrieves last N closed candles from database
func (ca *CandleAggregator) GetLastNCandles(token int64, n int) ([]Candle, error) {
	return ca.db.GetLastNCandles(ca.tableName, token, n)
}
