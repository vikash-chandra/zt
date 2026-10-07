package scanner

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"zerodha-trading/data"

	"github.com/zerodha/gokiteconnect/v4/models"
	"go.uber.org/zap"
)

const (
	// RetailWindowSize defines the 50-tick moving average window for retail order sizes
	RetailWindowSize = 50

	// AbsorptionWindowSize defines rolling ticks used for price compression & CVD divergence evaluation
	AbsorptionWindowSize = 30

	// DefaultBlockMultiplier flags single trades >= 10x the rolling retail average
	DefaultBlockMultiplier = 10.0

	// DefaultAbsVolumeBlock flags aggressive single trades exceeding absolute size (e.g. 10,000 shares)
	DefaultAbsVolumeBlock = 10000

	// PriceCompressionPct defines maximum percentage price movement (<= 0.15%) to classify as absorption
	PriceCompressionPct = 0.15

	// MinAbsorptionCVDDelta defines the cumulative volume delta shift required during price compression
	MinAbsorptionCVDDelta = 50000

	// FootprintQueueCapacity is the buffer size for the non-blocking persistence queue
	FootprintQueueCapacity = 5000

	// TriggerCooldownDuration prevents spamming repetitive triggers on the same stock
	TriggerCooldownDuration = 15 * time.Second
)

// RingBuffer50 tracks fixed 50-tick retail sizes with zero heap allocations.
// Thread safety: Caller MUST hold tracker.mu before invoking Add.
type RingBuffer50 struct {
	window [RetailWindowSize]uint32
	head   int
	count  int
	sum    uint64
}

// Add inserts a new quantity and returns the updated rolling simple moving average (SMA).
func (rb *RingBuffer50) Add(val uint32) float64 {
	if rb.count < RetailWindowSize {
		rb.sum += uint64(val)
		rb.window[rb.head] = val
		rb.head = (rb.head + 1) % RetailWindowSize
		rb.count++
		return float64(rb.sum) / float64(rb.count)
	}

	// Evict oldest value from rolling sum and insert new value
	oldVal := rb.window[rb.head]
	rb.sum = rb.sum - uint64(oldVal) + uint64(val)
	rb.window[rb.head] = val
	rb.head = (rb.head + 1) % RetailWindowSize
	return float64(rb.sum) / float64(RetailWindowSize)
}

// SMA returns the current average without mutating state.
func (rb *RingBuffer50) SMA() float64 {
	if rb.count == 0 {
		return 0
	}
	return float64(rb.sum) / float64(rb.count)
}

// TickSnapshot holds compact historical tick state for absorption divergence calculations.
type TickSnapshot struct {
	Price float64
	CVD   int64
}

// InstrumentTracker maintains the rolling state and order-flow footprint metrics for a single stock.
// Thread safety: All mutable state is strictly guarded by mu.
type InstrumentTracker struct {
	mu              sync.Mutex
	token           int64
	symbol          string
	lastPrice       float64
	lastDirection   int8 // +1: Buyer Uptick, -1: Seller Downtick, 0: Neutral
	prevTotalVolume uint32
	cvd             int64
	retailLTQ       RingBuffer50
	recentTicks     [AbsorptionWindowSize]TickSnapshot
	recentTickHead  int
	recentTickCount int
	lastTriggerTime time.Time
}

// FootprintScanner manages real-time footprint tracking across all subscribed F&O tokens.
// Concurrency Architecture:
// 1. regMu protects the instrument registry map. During normal tick ingestion, only a read-lock
//    is held for a few nanoseconds to look up the pointer.
// 2. Each InstrumentTracker has its own independent mutex. Ticks for separate instruments
//    process fully in parallel without cross-symbol lock contention.
// 3. Footprint persistence is fully decoupled via a non-blocking buffered channel (eventQueue),
//    guaranteeing that the WebSocket network loop is never delayed by database operations.
type FootprintScanner struct {
	db           *data.Database
	brokerClient data.BrokerClient
	logger       *zap.Logger

	regMu       sync.RWMutex
	instruments map[int64]*InstrumentTracker

	eventQueue chan *data.FootprintRecord
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	onFootprint func(record *data.FootprintRecord)
}

// NewFootprintScanner initializes scanner with non-blocking async DB worker.
func NewFootprintScanner(
	db *data.Database,
	brokerClient data.BrokerClient,
	logger *zap.Logger,
	onFootprint func(record *data.FootprintRecord),
) *FootprintScanner {
	ctx, cancel := context.WithCancel(context.Background())
	fs := &FootprintScanner{
		db:           db,
		brokerClient: brokerClient,
		logger:       logger,
		instruments:  make(map[int64]*InstrumentTracker),
		eventQueue:   make(chan *data.FootprintRecord, FootprintQueueCapacity),
		ctx:          ctx,
		cancel:       cancel,
		onFootprint:  onFootprint,
	}

	// Start asynchronous database batch writer worker
	fs.wg.Add(1)
	go fs.asyncPersistenceWorker()

	return fs
}

// RegisterInstrument adds an instrument to the scanner's tracking registry.
func (fs *FootprintScanner) RegisterInstrument(token int64, symbol string) {
	fs.regMu.Lock()
	defer fs.regMu.Unlock()

	if _, exists := fs.instruments[token]; !exists {
		fs.instruments[token] = &InstrumentTracker{
			token:  token,
			symbol: symbol,
		}
	}
}

// ProcessTick processes an incoming ModeFull market tick.
// Executed on the high-frequency ticker path; carefully optimized for zero heap allocations.
func (fs *FootprintScanner) ProcessTick(tick models.Tick) {
	token := int64(tick.InstrumentToken)

	// Step 1: Fast read-locked lookup in registry
	fs.regMu.RLock()
	tracker, exists := fs.instruments[token]
	fs.regMu.RUnlock()

	if !exists || tracker == nil {
		return
	}

	// Step 2: Lock ONLY this instrument's private mutex
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	ltp := tick.LastPrice
	ltq := tick.LastTradedQuantity
	totalVol := tick.VolumeTraded

	// Handle initial tick for baseline anchoring
	if tracker.lastPrice == 0 {
		tracker.lastPrice = ltp
		tracker.prevTotalVolume = totalVol
		if ltq > 0 {
			tracker.retailLTQ.Add(ltq)
		}
		return
	}

	// Step 3: Incremental Volume calculation
	var incVol int64
	if totalVol >= tracker.prevTotalVolume {
		incVol = int64(totalVol - tracker.prevTotalVolume)
	} else {
		// Daily session counter reset or reconnect
		incVol = int64(ltq)
	}
	tracker.prevTotalVolume = totalVol

	if incVol <= 0 {
		return
	}

	// Step 4: Tick Test Direction (+1 = Buyer-Initiated Uptick, -1 = Seller-Initiated Downtick)
	direction := tracker.lastDirection
	if ltp > tracker.lastPrice {
		direction = 1
	} else if ltp < tracker.lastPrice {
		direction = -1
	}
	// If ltp == tracker.lastPrice, direction inherits lastDirection (standard tick test)
	tracker.lastDirection = direction
	tracker.lastPrice = ltp

	// Step 5: Cumulative Volume Delta (CVD) update
	tracker.cvd += incVol * int64(direction)

	// Step 6: Update 50-Tick Retail SMA using circular ring buffer
	var baselineSMA float64
	if ltq > 0 {
		baselineSMA = tracker.retailLTQ.Add(ltq)
	} else {
		baselineSMA = tracker.retailLTQ.SMA()
	}

	// Step 7: Update rolling 30-tick absorption buffer
	snap := TickSnapshot{Price: ltp, CVD: tracker.cvd}
	tracker.recentTicks[tracker.recentTickHead] = snap
	tracker.recentTickHead = (tracker.recentTickHead + 1) % AbsorptionWindowSize
	if tracker.recentTickCount < AbsorptionWindowSize {
		tracker.recentTickCount++
	}

	// Step 8: Evaluate Footprint Triggers
	triggerReason := ""

	// Condition A: Aggressive Block Trade / 10x Retail Multiplier
	if ltq >= DefaultAbsVolumeBlock {
		triggerReason = fmt.Sprintf("AGGRESSIVE_LTQ_BLOCK: %d shares", ltq)
	} else if tracker.retailLTQ.count >= 20 && baselineSMA > 0 && float64(ltq) >= DefaultBlockMultiplier*baselineSMA {
		triggerReason = fmt.Sprintf("BLOCK_TRADE_10X: %d shares (%.1fx SMA %.0f)", ltq, float64(ltq)/baselineSMA, baselineSMA)
	}

	// Condition B: Absorption Divergence (Flat Price with Aggressive CVD Surge/Drop)
	if triggerReason == "" && tracker.recentTickCount >= AbsorptionWindowSize {
		minP := tracker.recentTicks[0].Price
		maxP := tracker.recentTicks[0].Price
		for i := 1; i < tracker.recentTickCount; i++ {
			p := tracker.recentTicks[i].Price
			if p < minP {
				minP = p
			}
			if p > maxP {
				maxP = p
			}
		}

		if minP > 0 {
			priceSpreadPct := ((maxP - minP) / minP) * 100.0
			oldestIdx := (tracker.recentTickHead - tracker.recentTickCount + AbsorptionWindowSize) % AbsorptionWindowSize
			cvdDelta := tracker.cvd - tracker.recentTicks[oldestIdx].CVD

			if priceSpreadPct <= PriceCompressionPct && math.Abs(float64(cvdDelta)) >= MinAbsorptionCVDDelta {
				if cvdDelta > 0 {
					triggerReason = fmt.Sprintf("ABSORPTION_BUY: CVD +%d in %.2f%% price range", cvdDelta, priceSpreadPct)
				} else {
					triggerReason = fmt.Sprintf("ABSORPTION_SELL: CVD %d in %.2f%% price range", cvdDelta, priceSpreadPct)
				}
			}
		}
	}

	// Step 9: Cooldown guard & asynchronous dispatch
	now := time.Now()
	if triggerReason != "" && now.Sub(tracker.lastTriggerTime) > TriggerCooldownDuration {
		tracker.lastTriggerTime = now

		// Calculate analytical block deal and order flow metrics
		tradeValue := ltp * float64(ltq)

		side := "BUY"
		if direction < 0 {
			side = "SELL"
		} else if direction == 0 {
			if strings.Contains(triggerReason, "BUY") {
				side = "BUY"
			} else if strings.Contains(triggerReason, "SELL") {
				side = "SELL"
			}
		}

		mult := 0.0
		if baselineSMA > 0 {
			mult = math.Round((float64(ltq)/baselineSMA)*100) / 100
		}

		vwap := tick.AverageTradePrice
		vwapDiffPct := 0.0
		if vwap > 0 {
			vwapDiffPct = math.Round(((ltp-vwap)/vwap)*10000) / 100
		}

		dayHigh := tick.OHLC.High
		dayLow := tick.OHLC.Low
		dayRangePct := 50.0
		if dayHigh > dayLow && dayLow > 0 {
			dayRangePct = math.Round(((ltp-dayLow)/(dayHigh-dayLow))*10000) / 100
			if dayRangePct < 0 {
				dayRangePct = 0
			} else if dayRangePct > 100 {
				dayRangePct = 100
			}
		}

		record := &data.FootprintRecord{
			InstrumentToken: token,
			TradingSymbol:   tracker.symbol,
			Timestamp:       now,
			Price:           ltp,
			Volume:          int64(ltq),
			CVDValue:        tracker.cvd,
			TriggerReason:   triggerReason,
			TradeValue:      tradeValue,
			Side:            side,
			Multiplier:      mult,
			BaselineSMA:     baselineSMA,
			VWAP:            vwap,
			VWAPDiffPct:     vwapDiffPct,
			DayHigh:         dayHigh,
			DayLow:          dayLow,
			DayRangePct:     dayRangePct,
			OI:              int64(tick.OI),
		}

		// Non-blocking push: If buffer is full, drop to protect the WebSocket thread
		select {
		case fs.eventQueue <- record:
		default:
			fs.logger.Warn("Footprint event channel full; dropping record to protect ticker ingestion loop",
				zap.String("symbol", tracker.symbol),
				zap.Int64("token", token))
		}

		// Notify external watchlist listener
		if fs.onFootprint != nil {
			go fs.onFootprint(record)
		}
	}
}

// asyncPersistenceWorker accumulates footprint records and persists them in batches
func (fs *FootprintScanner) asyncPersistenceWorker() {
	defer fs.wg.Done()

	batch := make([]*data.FootprintRecord, 0, 50)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if fs.db != nil {
			if err := fs.db.InsertFootprintsBatch(context.Background(), batch); err != nil {
				fs.logger.Error("Failed to persist footprints batch to database",
					zap.Error(err),
					zap.Int("count", len(batch)))
			}
		}
		batch = make([]*data.FootprintRecord, 0, 50)
	}

	for {
		select {
		case <-fs.ctx.Done():
			flush()
			return
		case record, ok := <-fs.eventQueue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, record)
			if len(batch) >= 50 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// RecalculateFootprints flushes in-memory metrics, queries recent DB candles, and rebuilds baselines on demand.
func (fs *FootprintScanner) RecalculateFootprints(ctx context.Context) error {
	fs.logger.Info("Starting manual footprint recalculation routine...")

	fs.regMu.RLock()
	tokens := make([]int64, 0, len(fs.instruments))
	for token := range fs.instruments {
		tokens = append(tokens, token)
	}
	fs.regMu.RUnlock()

	recalculatedCount := 0
	for _, token := range tokens {
		fs.regMu.RLock()
		tracker := fs.instruments[token]
		fs.regMu.RUnlock()

		if tracker == nil {
			continue
		}

		// Fetch today's latest 5m candles to rebuild volume baselines
		if fs.db != nil {
			candles, err := fs.db.GetLastNCandles("candles_5m", token, 50)
			if err == nil && len(candles) > 0 {
				tracker.mu.Lock()
				// Reset dynamic variables
				tracker.cvd = 0
				tracker.retailLTQ = RingBuffer50{}
				tracker.recentTickCount = 0
				tracker.recentTickHead = 0

				// Re-anchor price
				lastCandle := candles[len(candles)-1]
				tracker.lastPrice = lastCandle.Close

				// Seed initial retail baseline using candle volume distributions
				for _, c := range candles {
					ticks := c.TickCount
					if ticks <= 0 {
						ticks = 100
					}
					avgTickSize := uint32(c.Volume / int64(ticks))
					if avgTickSize > 0 {
						tracker.retailLTQ.Add(avgTickSize)
					}
				}
				tracker.mu.Unlock()
				recalculatedCount++
			}
		}
	}

	fs.logger.Info("Manual footprint recalculation completed successfully",
		zap.Int("instruments_recalculated", recalculatedCount),
		zap.Int("total_instruments", len(tokens)))

	return nil
}

// GetTrackerState retrieves snapshot of current instrument metrics (useful for testing & telemetry).
func (fs *FootprintScanner) GetTrackerState(token int64) (cvd int64, baselineSMA float64, lastPrice float64, found bool) {
	fs.regMu.RLock()
	tracker, exists := fs.instruments[token]
	fs.regMu.RUnlock()

	if !exists || tracker == nil {
		return 0, 0, 0, false
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	return tracker.cvd, tracker.retailLTQ.SMA(), tracker.lastPrice, true
}

// Close gracefully terminates background persistence workers and flushes any pending queue items.
func (fs *FootprintScanner) Close() {
	fs.cancel()
	close(fs.eventQueue)
	fs.wg.Wait()
}
