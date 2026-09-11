package strategy

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"zerodha-trading/data"
)

// EventTracer is an ultra-low latency, asynchronous event telemetry collector and query engine.
// It decouples the critical strategy execution hot-path (< 50ns) from database I/O,
// maintains an in-memory ring buffer for sub-millisecond UI telemetry streaming,
// and micro-batches persistence into PostgreSQL.
type EventTracer struct {
	logger       *zap.Logger
	db           *data.Database
	eventChan    chan *data.StrategyEvent
	droppedCount uint64
	seqID        int64

	// Ring buffer for fast in-memory telemetry reads
	bufferMu   sync.RWMutex
	ringBuffer []*data.StrategyEvent
	bufferCap  int
	bufferHead int
	bufferLen  int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewEventTracer creates and starts a new EventTracer instance
func NewEventTracer(logger *zap.Logger, db *data.Database) *EventTracer {
	ctx, cancel := context.WithCancel(context.Background())
	tracer := &EventTracer{
		logger:     logger,
		db:         db,
		eventChan:  make(chan *data.StrategyEvent, 4096),
		bufferCap:  1500,
		ringBuffer: make([]*data.StrategyEvent, 1500),
		ctx:        ctx,
		cancel:     cancel,
	}

	tracer.wg.Add(2)
	go tracer.workerLoop()
	go tracer.retentionLoop()

	return tracer
}

// Emit sends a strategy event to the telemetry pipeline without blocking the strategy execution loop (< 50ns)
func (et *EventTracer) Emit(evt *data.StrategyEvent) {
	if et == nil || evt == nil {
		return
	}

	if evt.EventTime.IsZero() {
		evt.EventTime = time.Now().In(data.ISTLocation)
	} else {
		evt.EventTime = data.NormalizeToIST(evt.EventTime)
	}

	if evt.Severity == "" {
		evt.Severity = "INFO"
	}

	// Assign incremental in-memory sequence ID for live delta streaming
	evt.ID = int(atomic.AddInt64(&et.seqID, 1))

	// Also immediately record into in-memory ring buffer so instant polling sees it
	et.recordToRingBuffer(evt)

	// Non-blocking push to the DB persistence batching channel
	select {
	case et.eventChan <- evt:
	default:
		atomic.AddUint64(&et.droppedCount, 1)
		if et.logger != nil {
			et.logger.Warn("Strategy event buffer full, dropped telemetry event",
				zap.String("symbol", evt.Symbol),
				zap.String("strategy", evt.Strategy),
				zap.String("stage", evt.Stage),
			)
		}
	}
}

// recordToRingBuffer writes an event into the circular buffer in memory
func (et *EventTracer) recordToRingBuffer(evt *data.StrategyEvent) {
	et.bufferMu.Lock()
	defer et.bufferMu.Unlock()

	et.ringBuffer[et.bufferHead] = evt
	et.bufferHead = (et.bufferHead + 1) % et.bufferCap
	if et.bufferLen < et.bufferCap {
		et.bufferLen++
	}
}

// workerLoop drains events from the channel and persists them to PostgreSQL in micro-batches
func (et *EventTracer) workerLoop() {
	defer et.wg.Done()

	ticker := time.NewTicker(1000 * time.Millisecond)
	defer ticker.Stop()

	var batch []*data.StrategyEvent
	const maxBatchSize = 50

	flush := func() {
		if len(batch) == 0 || et.db == nil {
			return
		}
		if err := et.db.InsertStrategyEventsBatch(et.ctx, batch); err != nil {
			if et.logger != nil {
				et.logger.Error("Failed to flush strategy events batch to database", zap.Error(err), zap.Int("batch_size", len(batch)))
			}
		}
		batch = nil
	}

	for {
		select {
		case <-et.ctx.Done():
			// Drain remaining events in channel on shutdown
			for {
				select {
				case evt := <-et.eventChan:
					batch = append(batch, evt)
					if len(batch) >= maxBatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}

		case evt := <-et.eventChan:
			batch = append(batch, evt)
			if len(batch) >= maxBatchSize {
				flush()
			}

		case <-ticker.C:
			flush()
		}
	}
}

// retentionLoop runs hourly to prune events older than 3 days
func (et *EventTracer) retentionLoop() {
	defer et.wg.Done()

	// Initial pruning on startup
	if et.db != nil {
		_ = et.db.AutoPruneStrategyEvents(et.ctx, 3)
	}

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-et.ctx.Done():
			return
		case <-ticker.C:
			if et.db != nil {
				if err := et.db.AutoPruneStrategyEvents(et.ctx, 3); err != nil && et.logger != nil {
					et.logger.Warn("Failed to auto-prune strategy events", zap.Error(err))
				}
			}
		}
	}
}

// QueryRecent fetches strategy telemetry events with multi-field filtering.
// For today's active session, it serves directly from the in-memory ring buffer (< 1ms).
// For historical queries or cache misses, it queries the PostgreSQL database.
func (et *EventTracer) QueryRecent(ctx context.Context, symbol, dateStr, strategy, stage, severity, search string, limit int, sinceID int) ([]data.StrategyEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	todayIST := time.Now().In(data.ISTLocation).Format("2006-01-02")
	isToday := dateStr == "" || dateStr == todayIST

	// If querying today and we have an in-memory buffer, query memory first
	if isToday {
		memResults := et.queryFromMemory(symbol, strategy, stage, severity, search, limit, sinceID)
		// If caller provided sinceID (live delta polling), in-memory results are 100% authoritative
		if sinceID > 0 || len(memResults) > 0 {
			return memResults, nil
		}
	}

	// Fallback to PostgreSQL database for historical dates or cold start
	if et.db != nil {
		return et.db.QueryStrategyEvents(ctx, symbol, dateStr, strategy, stage, severity, search, limit, sinceID)
	}

	return []data.StrategyEvent{}, nil
}

// queryFromMemory scans the in-memory circular buffer (newest to oldest)
func (et *EventTracer) queryFromMemory(symbol, strategy, stage, severity, search string, limit int, sinceID int) []data.StrategyEvent {
	et.bufferMu.RLock()
	defer et.bufferMu.RUnlock()

	if et.bufferLen == 0 {
		return []data.StrategyEvent{}
	}

	var results []data.StrategyEvent
	searchLower := strings.ToLower(strings.TrimSpace(search))
	symUpper := strings.ToUpper(strings.TrimSpace(symbol))
	stratUpper := strings.ToUpper(strings.TrimSpace(strategy))
	stageUpper := strings.ToUpper(strings.TrimSpace(stage))
	sevUpper := strings.ToUpper(strings.TrimSpace(severity))

	// Walk backwards from bufferHead - 1 (most recent event)
	for i := 0; i < et.bufferLen; i++ {
		idx := (et.bufferHead - 1 - i + et.bufferCap) % et.bufferCap
		ev := et.ringBuffer[idx]
		if ev == nil {
			continue
		}

		if sinceID > 0 && ev.ID <= sinceID {
			continue
		}

		if symUpper != "" && symUpper != "ALL" && strings.ToUpper(ev.Symbol) != symUpper {
			continue
		}

		if stratUpper != "" && stratUpper != "ALL" && strings.ToUpper(ev.Strategy) != stratUpper {
			continue
		}

		if stageUpper != "" && stageUpper != "ALL" && strings.ToUpper(ev.Stage) != stageUpper {
			continue
		}

		if sevUpper != "" && sevUpper != "ALL" && strings.ToUpper(ev.Severity) != sevUpper {
			continue
		}

		if searchLower != "" {
			match := strings.Contains(strings.ToLower(ev.Reason), searchLower) ||
				strings.Contains(strings.ToLower(ev.Title), searchLower) ||
				strings.Contains(strings.ToLower(ev.Symbol), searchLower) ||
				strings.Contains(strings.ToLower(ev.Stage), searchLower)
			if !match {
				continue
			}
		}

		results = append(results, *ev)
		if len(results) >= limit {
			break
		}
	}

	return results
}

// Close gracefully stops the background workers and flushes any pending events
func (et *EventTracer) Close() {
	if et == nil {
		return
	}
	et.cancel()
	et.wg.Wait()
}
