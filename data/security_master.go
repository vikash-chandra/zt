package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

	// Fetch active instruments list from Zerodha Kite Connect API if client is available
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
			sm.logger.Error("Failed to fetch instruments from Zerodha API, falling back to dummy", zap.Error(err))
		}
	}

	// Fallback to dummy tokens if API call failed, returned empty, or token is still local mock
	if len(constituents) == 0 {
		sm.logger.Warn("Using dummy constituent tokens (live connection inactive or unauthorized)")
		nifty50SymbolsList := []string{
			"RELIANCE", "TCS", "HDFC", "INFY", "ICICIBANK", "LT", "SBIN", "ITC",
			"MARUTI", "WIPRO", "BAJAJFINSV", "HDFCBANK", "ADANIPORTS", "SUNPHARMA",
			"ASIANPAINT", "POWERGRID", "NTPC", "HINDUNILVR", "DRREDDY", "TECHM",
			"JSWSTEEL", "BAJAJ-AUTO", "AXISBANK", "M&M", "TITAN", "HEROMOTOCO",
			"INDIGO", "BAJAJHLDNG", "SBILIFE", "COALINDIA", "UPL", "DIVISLAB",
			"BPCL", "ATUL", "BHARTIARTL", "IPCALAB", "TORNTPHARM", "APOLLOHOSP",
			"LUPIN", "GAIL", "HDFC", "HCL", "CIPLA", "NESTLEIND", "GICRE", "MRF",
			"MARICO", "ADANIGREEN", "PERSISTNT", "TATACONSUM",
		}
		for i, symbol := range nifty50SymbolsList {
			constituents[symbol] = int64(100000 + i*1000)
		}
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
	return 0, fmt.Errorf("symbol not found: %s", symbol)
}

// RefreshMaster forces refresh of security master from API
func (sm *SecurityMaster) RefreshMaster(ctx context.Context) error {
	// Invalidate cache in PostgreSQL
	_, err := sm.db.ExecContext(ctx, "DELETE FROM metadata_cache WHERE key IN ('nifty50:constituents', 'fo:underlyings')")
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

	sm.logger.Info("Security master refreshed")
	return nil
}
