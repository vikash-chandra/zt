package data

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

type mockSeederBrokerClient struct {
	BrokerClient
}

func (m *mockSeederBrokerClient) GetHistoricalData(instrumentToken int, interval string, fromTime time.Time, toTime time.Time, continuous bool, oi bool) ([]HistoricalData, error) {
	return []HistoricalData{
		{
			Date:   time.Date(2026, 8, 31, 9, 15, 0, 0, ISTLocation),
			Open:   100.0,
			High:   105.0,
			Low:    99.0,
			Close:  104.0,
			Volume: 15000,
		},
	}, nil
}

func TestHistoricalSeederStatus(t *testing.T) {
	logger := zap.NewNop()
	seeder := NewHistoricalSeeder(nil, &mockSeederBrokerClient{}, nil, logger)

	status := seeder.GetStatus()
	if status.IsRunning {
		t.Errorf("expected is_running=false, got true")
	}

	seeder.updateStage("TEST_STAGE")
	status = seeder.GetStatus()
	if status.CurrentStage != "TEST_STAGE" {
		t.Errorf("expected current_stage=TEST_STAGE, got %s", status.CurrentStage)
	}
}

func TestHistoricalSeederNilGuard(t *testing.T) {
	logger := zap.NewNop()
	seeder := NewHistoricalSeeder(nil, nil, nil, logger)
	err := seeder.RunPreMarketSeeding(context.Background())
	if err == nil {
		t.Errorf("expected error when broker or database is nil, got nil")
	}
}

func TestHistoricalSeederConcurrentAccess(t *testing.T) {
	logger := zap.NewNop()
	seeder := NewHistoricalSeeder(nil, &mockSeederBrokerClient{}, nil, logger)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			seeder.updateStage(fmt.Sprintf("STAGE_%d", idx))
			status := seeder.GetStatus()
			if status.ProgressPct < 0 || status.ProgressPct > 100 {
				t.Errorf("invalid progress: %f", status.ProgressPct)
			}
		}(i)
	}
	wg.Wait()
}
