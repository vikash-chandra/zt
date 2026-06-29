package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"go.uber.org/zap"
)

// SecurityMaster manages instrument and security data
type SecurityMaster struct {
	db       *sql.DB
	kite     *kiteconnect.Client
	logger   *zap.Logger
	cacheTTL time.Duration

	// In-memory cache
	nifty50       map[string]int64 // symbol -> token
	foUnderlyings []FOUnderlying
}

// FOUnderlying represents a futures & options underlying
type FOUnderlying struct {
	Symbol       string
	Token        int64
	Expiry       string
	Strike       float64
	LotSize      int
	ContractSpec string
}

// NewSecurityMaster creates a new security master
func NewSecurityMaster(db *sql.DB, kite *kiteconnect.Client, logger *zap.Logger) *SecurityMaster {
	return &SecurityMaster{
		db:            db,
		kite:          kite,
		logger:        logger,
		cacheTTL:      24 * time.Hour,
		nifty50:       make(map[string]int64),
		foUnderlyings: []FOUnderlying{},
	}
}

// GetNifty50Constituents returns Nifty 50 constituents with their tokens
func (sm *SecurityMaster) GetNifty50Constituents(ctx context.Context) (map[string]int64, error) {
	cacheKey := "nifty50:constituents"

	// Try to get from PostgreSQL
	var cached string
	err := sm.db.QueryRowContext(ctx, "SELECT value FROM metadata_cache WHERE key = $1 AND updated_at > $2", cacheKey, time.Now().Add(-sm.cacheTTL)).Scan(&cached)
	if err == nil {
		if err := json.Unmarshal([]byte(cached), &sm.nifty50); err == nil {
			sm.logger.Info("Loaded Nifty50 from cache", zap.Int("count", len(sm.nifty50)))
			return sm.nifty50, nil
		}
	}

	// Fetch active instruments list from Zerodha Kite Connect API
	var constituents = make(map[string]int64)
	if sm.kite != nil {
		sm.logger.Info("Fetching active instruments from Zerodha Kite API...")
		instruments, err := sm.kite.GetInstrumentsByExchange("NSE")
		if err == nil {
			nifty50Symbols := map[string]bool{
				"ADANIENT":     true,
				"ADANIPORTS":   true,
				"APOLLOHOSP":   true,
				"ASIANPAINT":   true,
				"AXISBANK":     true,
				"BAJAJ-AUTO":   true,
				"BAJAJFINSV":   true,
				"BAJAJFINANCE": true,
				"BHARTIARTL":   true,
				"BPCL":         true,
				"BRITANNIA":    true,
				"CIPLA":        true,
				"COALINDIA":    true,
				"DIVISLAB":     true,
				"DRREDDY":      true,
				"EICHERMOT":    true,
				"GRASIM":       true,
				"HCLTECH":      true,
				"HDFCBANK":     true,
				"HDFCLIFE":     true,
				"HEROMOTOCO":   true,
				"HINDALCO":     true,
				"HINDUNILVR":   true,
				"ICICIBANK":    true,
				"INDUSINDBK":   true,
				"INFY":         true,
				"ITC":          true,
				"JSWSTEEL":     true,
				"KOTAKBANK":    true,
				"LT":           true,
				"LTIM":         true,
				"M&M":          true,
				"MARUTI":       true,
				"NESTLEIND":    true,
				"NTPC":         true,
				"ONGC":         true,
				"POWERGRID":    true,
				"RELIANCE":     true,
				"SBILIFE":      true,
				"SBIN":         true,
				"SHRIRAMFIN":   true,
				"SUNPHARMA":    true,
				"TATACONSUM":   true,
				"TATAMOTORS":   true,
				"TATASTEEL":    true,
				"TCS":          true,
				"TECHM":        true,
				"TITAN":        true,
				"TRENT":        true,
				"ULTRACEMCO":   true,
				"WIPRO":        true,
			}

			for _, inst := range instruments {
				if nifty50Symbols[inst.Tradingsymbol] {
					constituents[inst.Tradingsymbol] = int64(inst.InstrumentToken)
				}
			}
		} else {
			return nil, fmt.Errorf("failed to fetch instruments from Zerodha API: %w", err)
		}
	}

	if len(constituents) == 0 {
		return nil, fmt.Errorf("failed to resolve active Nifty 50 constituents from Zerodha Kite API")
	}

	sm.nifty50 = constituents

	// Cache in PostgreSQL
	if data, err := json.Marshal(constituents); err == nil {
		_, err = sm.db.ExecContext(ctx, `
			INSERT INTO metadata_cache (key, value, updated_at) 
			VALUES ($1, $2, CURRENT_TIMESTAMP) 
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP
		`, cacheKey, string(data))
		if err != nil {
			sm.logger.Error("Failed to cache Nifty50 in database", zap.Error(err))
		}
	}

	sm.logger.Info("Loaded Nifty50 constituents", zap.Int("count", len(constituents)))
	return constituents, nil
}

// GetFOUnderlyings returns all F&O eligible underlyings
func (sm *SecurityMaster) GetFOUnderlyings(ctx context.Context) ([]FOUnderlying, error) {
	cacheKey := "fo:underlyings"

	// Try to get from PostgreSQL
	var cached string
	err := sm.db.QueryRowContext(ctx, "SELECT value FROM metadata_cache WHERE key = $1 AND updated_at > $2", cacheKey, time.Now().Add(-sm.cacheTTL)).Scan(&cached)
	if err == nil {
		var underlyings []FOUnderlying
		if err := json.Unmarshal([]byte(cached), &underlyings); err == nil {
			sm.logger.Info("Loaded F&O underlyings from cache", zap.Int("count", len(underlyings)))
			return underlyings, nil
		}
	}

	// Hardcoded F&O underlyings for demo (or fetch via sm.kite in real production)
	underlyings := []FOUnderlying{
		{Symbol: "NIFTY", Token: 99926009, Expiry: "2026-06-25", Strike: 0, LotSize: 50, ContractSpec: "INDEX"},
		{Symbol: "BANKNIFTY", Token: 99926037, Expiry: "2026-06-25", Strike: 0, LotSize: 15, ContractSpec: "INDEX"},
		{Symbol: "RELIANCE", Token: 1333761, Expiry: "2026-06-25", Strike: 2500, LotSize: 1, ContractSpec: "EQUITY"},
		{Symbol: "TCS", Token: 1364481, Expiry: "2026-06-25", Strike: 3500, LotSize: 1, ContractSpec: "EQUITY"},
	}

	sm.foUnderlyings = underlyings

	// Cache in PostgreSQL
	if data, err := json.Marshal(underlyings); err == nil {
		_, err = sm.db.ExecContext(ctx, `
			INSERT INTO metadata_cache (key, value, updated_at) 
			VALUES ($1, $2, CURRENT_TIMESTAMP) 
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP
		`, cacheKey, string(data))
		if err != nil {
			sm.logger.Error("Failed to cache F&O underlyings in database", zap.Error(err))
		}
	}

	sm.logger.Info("Loaded F&O underlyings", zap.Int("count", len(underlyings)))
	return underlyings, nil
}

// GetInstrumentToken retrieves token for a symbol
func (sm *SecurityMaster) GetInstrumentToken(symbol string) (int64, error) {
	if token, exists := sm.nifty50[symbol]; exists {
		return token, nil
	}
	// Also lookup in the cached fo:stocks list
	var token int64
	err := sm.db.QueryRow("SELECT (value::jsonb->>$1)::bigint FROM metadata_cache WHERE key = 'fo:stocks'", symbol).Scan(&token)
	if err == nil && token > 0 {
		return token, nil
	}
	return 0, fmt.Errorf("symbol not found in nifty50 or fo:stocks: %s", symbol)
}

// GetFOStocks returns NSE F&O underlyings with their tokens
func (sm *SecurityMaster) GetFOStocks(ctx context.Context) (map[string]int64, error) {
	cacheKey := "fo:stocks"

	// Try to get from PostgreSQL metadata_cache
	var cached string
	err := sm.db.QueryRowContext(ctx, "SELECT value FROM metadata_cache WHERE key = $1 AND updated_at > $2", cacheKey, time.Now().Add(-sm.cacheTTL)).Scan(&cached)
	if err == nil {
		var cachedStocks map[string]int64
		if err := json.Unmarshal([]byte(cached), &cachedStocks); err == nil {
			sm.logger.Info("Loaded F&O stocks from cache", zap.Int("count", len(cachedStocks)))
			return cachedStocks, nil
		}
	}

	// Fetch active instruments list from Zerodha Kite Connect API
	var foStocks = make(map[string]int64)
	if sm.kite != nil {
		sm.logger.Info("Fetching active F&O instruments to resolve stocks...")

		// 1. Get all NFO instruments to extract underlying symbols
		nfoInstruments, err := sm.kite.GetInstrumentsByExchange("NFO")
		if err != nil {
			return nil, fmt.Errorf("failed to fetch NFO instruments from Zerodha API: %w", err)
		}

		underlyingsMap := make(map[string]bool)
		for _, inst := range nfoInstruments {
			if inst.Segment == "NFO-FUT" {
				underlying := extractUnderlying(inst.Tradingsymbol)
				if underlying != "" {
					underlyingsMap[underlying] = true
				}
			}
		}

		// 2. Get all NSE instruments to map underlyings to their NSE tokens
		nseInstruments, err := sm.kite.GetInstrumentsByExchange("NSE")
		if err != nil {
			return nil, fmt.Errorf("failed to fetch NSE instruments from Zerodha API: %w", err)
		}

		for _, inst := range nseInstruments {
			if underlyingsMap[inst.Tradingsymbol] {
				foStocks[inst.Tradingsymbol] = int64(inst.InstrumentToken)
			}
		}
	}

	if len(foStocks) == 0 {
		return nil, fmt.Errorf("failed to resolve active F&O stocks from Zerodha Kite API")
	}

	// Cache in PostgreSQL
	if data, err := json.Marshal(foStocks); err == nil {
		_, err = sm.db.ExecContext(ctx, `
			INSERT INTO metadata_cache (key, value, updated_at) 
			VALUES ($1, $2, CURRENT_TIMESTAMP) 
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP
		`, cacheKey, string(data))
		if err != nil {
			sm.logger.Error("Failed to cache F&O stocks in database", zap.Error(err))
		}
	}

	sm.logger.Info("Loaded F&O stocks", zap.Int("count", len(foStocks)))
	return foStocks, nil
}

// RefreshMaster forces refresh of security master from API
func (sm *SecurityMaster) RefreshMaster(ctx context.Context) error {
	// Invalidate cache in PostgreSQL
	_, err := sm.db.ExecContext(ctx, "DELETE FROM metadata_cache WHERE key IN ('nifty50:constituents', 'fo:underlyings', 'fo:stocks')")
	if err != nil {
		sm.logger.Error("Failed to invalidate cache in database", zap.Error(err))
	}

	// Reload
	if _, err := sm.GetNifty50Constituents(ctx); err != nil {
		return err
	}

	if _, err := sm.GetFOUnderlyings(ctx); err != nil {
		return err
	}

	if _, err := sm.GetFOStocks(ctx); err != nil {
		return err
	}

	sm.logger.Info("Security master refreshed")
	return nil
}

var expiryRegex = regexp.MustCompile(`[0-9]{2}[A-Z]{3}`)

func extractUnderlying(tradingSymbol string) string {
	loc := expiryRegex.FindStringIndex(tradingSymbol)
	if loc == nil {
		return ""
	}
	return tradingSymbol[:loc[0]]
}
