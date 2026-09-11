package strategy

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"zerodha-trading/data"
)

func TestEventTracer_NonBlockingAndFiltering(t *testing.T) {
	logger := zap.NewNop()
	tracer := NewEventTracer(logger, nil)
	defer tracer.Close()

	// 1. Emit 20 sample events across multiple symbols and strategies
	now := time.Now().In(data.ISTLocation)
	for i := 1; i <= 20; i++ {
		sym := "JSWSTEEL"
		strat := "EMAS5_BREAKOUT"
		stage := "CANDLE_CLOSE"
		sev := "INFO"
		if i%4 == 0 {
			sym = "RELIANCE"
			strat = "VANDE_BHARAT"
			stage = "MASTER_FORMED"
			sev = "SUCCESS"
		} else if i%3 == 0 {
			sym = "TATASTEEL"
			strat = "FAKE_BREAKOUT"
			stage = "MASTER_REJECTED"
			sev = "WARNING"
		}

		tracer.Emit(&data.StrategyEvent{
			EventTime: now.Add(time.Duration(i) * time.Minute),
			Symbol:    sym,
			Strategy:  strat,
			Stage:     stage,
			Severity:  sev,
			Title:     fmt.Sprintf("Event %d for %s", i, sym),
			Reason:    fmt.Sprintf("Telemetry details for step %d", i),
			Details: map[string]interface{}{
				"step": i,
				"ltp":  100.0 + float64(i),
			},
		})
	}

	ctx := context.Background()

	// 2. Query all events for today
	allEvents, err := tracer.QueryRecent(ctx, "ALL", "", "ALL", "ALL", "ALL", "", 100, 0)
	if err != nil {
		t.Fatalf("QueryRecent failed: %v", err)
	}
	if len(allEvents) != 20 {
		t.Fatalf("Expected 20 events, got %d", len(allEvents))
	}

	// 3. Filter by Symbol: RELIANCE
	relEvents, err := tracer.QueryRecent(ctx, "RELIANCE", "", "ALL", "ALL", "ALL", "", 100, 0)
	if err != nil {
		t.Fatalf("QueryRecent for RELIANCE failed: %v", err)
	}
	for _, ev := range relEvents {
		if ev.Symbol != "RELIANCE" {
			t.Errorf("Expected symbol RELIANCE, got %s", ev.Symbol)
		}
		if ev.Severity != "SUCCESS" {
			t.Errorf("Expected severity SUCCESS for RELIANCE, got %s", ev.Severity)
		}
	}
	if len(relEvents) != 5 { // i = 4, 8, 12, 16, 20
		t.Errorf("Expected 5 RELIANCE events, got %d", len(relEvents))
	}

	// 4. Filter by Severity: WARNING
	warnEvents, err := tracer.QueryRecent(ctx, "ALL", "", "ALL", "ALL", "WARNING", "", 100, 0)
	if err != nil {
		t.Fatalf("QueryRecent for WARNING failed: %v", err)
	}
	for _, ev := range warnEvents {
		if ev.Severity != "WARNING" {
			t.Errorf("Expected severity WARNING, got %s", ev.Severity)
		}
	}

	// 5. Test since_id incremental delta streaming
	lastID := allEvents[5].ID // 6th event from top
	newerEvents, err := tracer.QueryRecent(ctx, "ALL", "", "ALL", "ALL", "ALL", "", 100, lastID)
	if err != nil {
		t.Fatalf("QueryRecent with sinceID failed: %v", err)
	}
	for _, ev := range newerEvents {
		if ev.ID <= lastID {
			t.Errorf("Expected ev.ID > %d, got %d", lastID, ev.ID)
		}
	}

	// 6. Test full-text search
	searchEvents, err := tracer.QueryRecent(ctx, "ALL", "", "ALL", "ALL", "ALL", "step 12", 100, 0)
	if err != nil {
		t.Fatalf("QueryRecent with search failed: %v", err)
	}
	if len(searchEvents) != 1 {
		t.Errorf("Expected 1 match for 'step 12', got %d", len(searchEvents))
	}
}

func TestEventTracer_ConcurrentEmission(t *testing.T) {
	logger := zap.NewNop()
	tracer := NewEventTracer(logger, nil)
	defer tracer.Close()

	var wg sync.WaitGroup
	workers := 20
	eventsPerWorker := 100

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < eventsPerWorker; i++ {
				tracer.Emit(&data.StrategyEvent{
					Symbol:   "HDFCBANK",
					Strategy: "EMAS5_BREAKOUT",
					Stage:    "CANDLE_CLOSE",
					Severity: "INFO",
					Title:    fmt.Sprintf("Worker %d Emit %d", workerID, i),
					Reason:   "Concurrent tick load test",
				})
			}
		}(w)
	}

	wg.Wait()

	events, err := tracer.QueryRecent(context.Background(), "HDFCBANK", "", "ALL", "ALL", "ALL", "", 500, 0)
	if err != nil {
		t.Fatalf("QueryRecent failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("Expected buffered events, got 0")
	}
}
