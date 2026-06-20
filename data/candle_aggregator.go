package data

import (
	"database/sql"
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

// Candle represents a 5-minute OHLCV candle
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
	db             *sql.DB
	logger         *zap.Logger
	candleInterval time.Duration
	marketOpen     time.Time
	marketClose    time.Time

	mu               sync.RWMutex
	currentCandles   map[int64]*CandleState
	completedCandles chan *Candle
}

// NewCandleAggregator creates a new candle aggregator
func NewCandleAggregator(db *sql.DB, logger *zap.Logger, intervalSec int, bufferSize int) *CandleAggregator {
	return &CandleAggregator{
		db:               db,
		logger:           logger,
		candleInterval:   time.Duration(intervalSec) * time.Second,
		marketOpen:       time.Date(2020, 1, 1, 9, 15, 0, 0, time.UTC),
		marketClose:      time.Date(2020, 1, 1, 15, 30, 0, 0, time.UTC),
		currentCandles:   make(map[int64]*CandleState),
		completedCandles: make(chan *Candle, bufferSize),
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
	}

	// Persist to database
	ca.persistCandle(candle)

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
	// Calculate seconds since market open
	secondsSinceOpen := int64(t.Sub(ca.marketOpen).Seconds())
	if secondsSinceOpen < 0 {
		secondsSinceOpen = 0
	}

	intervalSec := int64(ca.candleInterval.Seconds())
	candleIndex := secondsSinceOpen / intervalSec
	candleStart := ca.marketOpen.Add(time.Duration(candleIndex*intervalSec) * time.Second)

	return candleStart
}

// persistCandle saves candle to database
func (ca *CandleAggregator) persistCandle(candle *Candle) {
	query := `
		INSERT INTO candles_5m (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (token, time) DO UPDATE SET
			close = EXCLUDED.close,
			high = EXCLUDED.high,
			low = EXCLUDED.low,
			volume = EXCLUDED.volume,
			vwap = EXCLUDED.vwap,
			bid = EXCLUDED.bid,
			ask = EXCLUDED.ask,
			tick_count = EXCLUDED.tick_count;
	`

	if _, err := ca.db.Exec(query, candle.Token, candle.Time, candle.Open, candle.High,
		candle.Low, candle.Close, candle.Volume, candle.VWAP, candle.Bid, candle.Ask, candle.TickCount); err != nil {
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
		return state
	}
	return nil
}

// GetLastNCandles retrieves last N closed candles from database
func (ca *CandleAggregator) GetLastNCandles(token int64, n int) ([]Candle, error) {
	query := `
		SELECT token, time, open, high, low, close, volume, vwap, bid, ask, tick_count
		FROM candles_5m
		WHERE token = $1
		ORDER BY time DESC
		LIMIT $2;
	`

	rows, err := ca.db.Query(query, token, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candles := make([]Candle, 0, n)
	for rows.Next() {
		var c Candle
		if err := rows.Scan(&c.Token, &c.Time, &c.Open, &c.High, &c.Low, &c.Close,
			&c.Volume, &c.VWAP, &c.Bid, &c.Ask, &c.TickCount); err != nil {
			return nil, err
		}
		candles = append(candles, c)
	}

	// Reverse to chronological order
	for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
		candles[i], candles[j] = candles[j], candles[i]
	}

	return candles, nil
}
