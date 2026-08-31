package data

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// SeederStatus represents real-time progress of pre-market historical data seeding
type SeederStatus struct {
	IsRunning        bool      `json:"is_running"`
	CurrentStage     string    `json:"current_stage"`
	TotalSymbols     int       `json:"total_symbols"`
	CompletedSymbols int       `json:"completed_symbols"`
	ProgressPct      float64   `json:"progress_pct"`
	LastRunTime      time.Time `json:"last_run_time"`
	LastError        string    `json:"last_error,omitempty"`
}

// HistoricalSeeder handles pre-market automated historical data fetching and database persistence
type HistoricalSeeder struct {
	db           *Database
	brokerClient BrokerClient
	secMaster    *SecurityMaster
	logger       *zap.Logger
	mu           sync.RWMutex

	isRunning        int32
	currentStage     string
	totalSymbols     int
	completedSymbols int
	lastRunTime      time.Time
	lastError        string
}

// NewHistoricalSeeder creates a new instance of HistoricalSeeder
func NewHistoricalSeeder(db *Database, broker BrokerClient, secMaster *SecurityMaster, logger *zap.Logger) *HistoricalSeeder {
	return &HistoricalSeeder{
		db:           db,
		brokerClient: broker,
		secMaster:    secMaster,
		logger:       logger,
	}
}

// GetStatus returns the live status of the historical data seeder
func (s *HistoricalSeeder) GetStatus() SeederStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	running := atomic.LoadInt32(&s.isRunning) == 1
	progress := 0.0
	if s.totalSymbols > 0 {
		progress = float64(s.completedSymbols) / float64(s.totalSymbols) * 100.0
		if progress > 100.0 {
			progress = 100.0
		}
	} else if !running && !s.lastRunTime.IsZero() {
		progress = 100.0
	}

	return SeederStatus{
		IsRunning:        running,
		CurrentStage:     s.currentStage,
		TotalSymbols:     s.totalSymbols,
		CompletedSymbols: s.completedSymbols,
		ProgressPct:      progress,
		LastRunTime:      s.lastRunTime,
		LastError:        s.lastError,
	}
}

// RunPreMarketSeeding orchestrates the full pre-market data seeding pipeline
func (s *HistoricalSeeder) RunPreMarketSeeding(ctx context.Context) error {
	if s.brokerClient == nil || s.db == nil {
		return fmt.Errorf("broker client or database not initialized")
	}

	if !atomic.CompareAndSwapInt32(&s.isRunning, 0, 1) {
		s.logger.Warn("[PRE_MARKET_SEEDER] Seeding is already in progress, skipping concurrent run")
		return nil
	}

	defer atomic.StoreInt32(&s.isRunning, 0)

	startTime := time.Now()
	s.logger.Info("[PRE_MARKET_SEEDER] Starting automated pre-market historical data seeding pipeline...")

	s.mu.Lock()
	s.lastError = ""
	s.currentStage = "INITIALIZING"
	s.completedSymbols = 0
	s.totalSymbols = 0
	s.mu.Unlock()

	// 1. Stage 1: Seed 5-day 5m Candles for All Supported Indices (NIFTY 50, BANKNIFTY, FINNIFTY, MIDCPNIFTY, SENSEX)
	s.updateStage("SEEDING_INDICES_5M")
	if err := s.SeedIndices5mHistory(ctx); err != nil {
		s.logger.Error("[PRE_MARKET_SEEDER] Failed to seed indices 5m history", zap.Error(err))
	}

	// 2. Stage 2: Seed 3-day 5m and 1m Candles for All F&O Stocks (209 Stocks)
	s.updateStage("SEEDING_FO_STOCKS_INTRADAY")
	if err := s.SeedFOStocksIntradayHistory(ctx); err != nil {
		s.logger.Error("[PRE_MARKET_SEEDER] Failed to seed F&O stocks intraday history", zap.Error(err))
	}

	// 3. Stage 3: Seed 3-Year Daily Candles for NIFTY 500 & F&O Universe (candles_1d)
	s.updateStage("SEEDING_UNIVERSE_DAILY")
	if err := s.SeedUniverseDailyCandles(ctx); err != nil {
		s.logger.Error("[PRE_MARKET_SEEDER] Failed to seed universe daily candles", zap.Error(err))
	}

	elapsed := time.Since(startTime)
	s.mu.Lock()
	s.currentStage = "COMPLETED"
	s.lastRunTime = NormalizeToIST(time.Now())
	s.mu.Unlock()

	s.logger.Info(fmt.Sprintf("[PRE_MARKET_SEEDER] Pre-market historical data seeding pipeline completed in %v", elapsed),
		zap.Duration("elapsed", elapsed),
		zap.Int("total_completed", s.completedSymbols),
	)

	return nil
}

// SeedIndices5mHistory seeds 5 trading days of 5m candles for all supported indices
func (s *HistoricalSeeder) SeedIndices5mHistory(ctx context.Context) error {
	nowIST := NormalizeToIST(time.Now())
	fromTimeIST := nowIST.AddDate(0, 0, -7)

	indices := GetAllSupportedIndices()
	s.logger.Info("[PRE_MARKET_SEEDER] Seeding 5m candles for major market indices...", zap.Int("count", len(indices)))

	for _, spec := range indices {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		time.Sleep(300 * time.Millisecond)
		candles, err := s.brokerClient.GetHistoricalData(int(spec.SpotToken), "5minute", fromTimeIST, nowIST, false, false)
		if err != nil {
			s.logger.Warn("[PRE_MARKET_SEEDER] Failed to fetch 5m candles for index",
				zap.String("index", spec.Name),
				zap.Int64("token", spec.SpotToken),
				zap.Error(err),
			)
			continue
		}

		if len(candles) > 0 {
			if err := s.db.SaveHistoricalCandles(ctx, spec.SpotToken, candles, "candles_5m"); err != nil {
				s.logger.Error("[PRE_MARKET_SEEDER] Failed to persist index candles", zap.String("index", spec.Name), zap.Error(err))
			} else {
				s.logger.Info("[PRE_MARKET_SEEDER] Persisted index 5m candles successfully",
					zap.String("index", spec.Name),
					zap.Int("candles_count", len(candles)),
				)
			}
		}
	}
	return nil
}

// SeedFOStocksIntradayHistory seeds 3 trading days of 5m and 1m candles for all 209 F&O stocks
func (s *HistoricalSeeder) SeedFOStocksIntradayHistory(ctx context.Context) error {
	foMap, err := s.secMaster.GetFOStocks(ctx)
	if err != nil || len(foMap) == 0 {
		return fmt.Errorf("failed to fetch F&O stock universe: %w", err)
	}

	s.mu.Lock()
	s.totalSymbols = len(foMap)
	s.completedSymbols = 0
	s.mu.Unlock()

	nowIST := NormalizeToIST(time.Now())
	fromTime5m := nowIST.AddDate(0, 0, -5)
	fromTime1m := nowIST.AddDate(0, 0, -3)

	s.logger.Info("[PRE_MARKET_SEEDER] Seeding intraday candles for F&O stock universe...", zap.Int("total_stocks", len(foMap)))

	for symbol, token := range foMap {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		time.Sleep(340 * time.Millisecond)

		// 1. Fetch & persist 5m candles
		c5m, err5m := s.brokerClient.GetHistoricalData(int(token), "5minute", fromTime5m, nowIST, false, false)
		if err5m == nil && len(c5m) > 0 {
			_ = s.db.SaveHistoricalCandles(ctx, token, c5m, "candles_5m")
		}

		time.Sleep(340 * time.Millisecond)

		// 2. Fetch & persist 1m candles
		c1m, err1m := s.brokerClient.GetHistoricalData(int(token), "minute", fromTime1m, nowIST, false, false)
		if err1m == nil && len(c1m) > 0 {
			_ = s.db.SaveHistoricalCandles(ctx, token, c1m, "candles_1m")
		}

		s.mu.Lock()
		s.completedSymbols++
		s.mu.Unlock()

		if s.completedSymbols%25 == 0 || s.completedSymbols == len(foMap) {
			s.logger.Info(fmt.Sprintf("[PRE_MARKET_SEEDER] F&O Intraday progress: %d/%d stocks (%.1f%%)",
				s.completedSymbols, len(foMap), float64(s.completedSymbols)/float64(len(foMap))*100.0),
				zap.String("last_symbol", symbol),
			)
		}
	}

	return nil
}

// SeedUniverseDailyCandles seeds 3 years of daily candles for NIFTY 500 & F&O stocks
func (s *HistoricalSeeder) SeedUniverseDailyCandles(ctx context.Context) error {
	allStocks, err := s.secMaster.GetNifty500AndFOStocks(ctx)
	if err != nil || len(allStocks) == 0 {
		return fmt.Errorf("failed to fetch NIFTY 500 universe: %w", err)
	}

	nowIST := NormalizeToIST(time.Now())
	fromTimeDaily := nowIST.AddDate(-3, 0, 0)

	s.logger.Info("[PRE_MARKET_SEEDER] Seeding 3-year daily candles for quant stock scanner universe...", zap.Int("total_stocks", len(allStocks)))

	completed := 0
	for symbol, token := range allStocks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Incremental update: If DB already has >= 250 daily candles, only fetch the last 7 days (to sync yesterday's candle)
		fromTime := fromTimeDaily
		if existing, err := s.db.GetRecentDailyCandlesByToken(ctx, token, 300); err == nil && len(existing) >= 250 {
			fromTime = nowIST.AddDate(0, 0, -7)
		}

		time.Sleep(340 * time.Millisecond)
		hist, err := s.brokerClient.GetHistoricalData(int(token), "day", fromTime, nowIST, false, false)
		if err != nil {
			s.logger.Warn("[PRE_MARKET_SEEDER] Failed to fetch daily candles for stock", zap.String("symbol", symbol), zap.Error(err))
			continue
		}

		if len(hist) > 0 {
			var fetched []Candle
			for _, h := range hist {
				fetched = append(fetched, Candle{
					Token:  token,
					Time:   NormalizeToIST(h.Date),
					Open:   h.Open,
					High:   h.High,
					Low:    h.Low,
					Close:  h.Close,
					Volume: int64(h.Volume),
				})
			}
			_ = s.db.UpsertDailyCandles(ctx, fetched)
		}
		completed++
	}

	s.logger.Info("[PRE_MARKET_SEEDER] 3-Year Daily candles seeding finished", zap.Int("total_stocks", completed))
	return nil
}

func (s *HistoricalSeeder) updateStage(stage string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentStage = stage
}
