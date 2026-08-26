package data

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// SecurityMaster manages instrument and security data
type SecurityMaster struct {
	db       *Database
	kite     BrokerClient
	logger   *zap.Logger
	cacheTTL time.Duration

	// In-memory cache
	mu           sync.RWMutex
	nifty50      map[string]int64 // symbol -> token
	foUnderlyings []FOUnderlying
	optCache     map[string]Instruments // exchange -> option instruments
	optCacheTime map[string]time.Time
	unresolved   map[string]time.Time
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
func NewSecurityMaster(db *Database, kite BrokerClient, logger *zap.Logger) *SecurityMaster {
	return &SecurityMaster{
		db:            db,
		kite:          kite,
		logger:        logger,
		cacheTTL:      24 * time.Hour,
		nifty50:       make(map[string]int64),
		foUnderlyings: []FOUnderlying{},
		optCache:      make(map[string]Instruments),
		optCacheTime:  make(map[string]time.Time),
		unresolved:    make(map[string]time.Time),
	}
}

// GetNifty50Constituents returns Nifty 50 constituents with their tokens
func (sm *SecurityMaster) GetNifty50Constituents(ctx context.Context) (map[string]int64, error) {
	cacheKey := "nifty50:constituents"

	// Try to get from PostgreSQL
	cached, err := sm.db.GetMetadataCache(ctx, cacheKey, time.Now().Add(-sm.cacheTTL))
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
				if nifty50Symbols[inst.TradingSymbol] {
					constituents[inst.TradingSymbol] = int64(inst.InstrumentToken)
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
		err = sm.db.SaveMetadataCache(ctx, cacheKey, string(data))
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
	cached, err := sm.db.GetMetadataCache(ctx, cacheKey, time.Now().Add(-sm.cacheTTL))
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
		err = sm.db.SaveMetadataCache(ctx, cacheKey, string(data))
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
	// Check Index Master registry for spot indices (e.g. NIFTY 50, NIFTY BANK, BSE SENSEX, FINNIFTY, MIDCPNIFTY)
	if spec, found := ResolveIndexSpec(symbol); found && (strings.EqualFold(spec.Name, symbol) || strings.EqualFold(spec.CleanPrefix, symbol)) {
		return spec.SpotToken, nil
	}
	// Also lookup in the cached fo:stocks list
	token, err := sm.db.QueryRowSymbolToken(symbol)
	if err == nil && token > 0 {
		return token, nil
	}
	// If option contract symbol, check exchange (BFO for SENSEX, NFO for others)
	exch := "NFO"
	if strings.HasPrefix(symbol, "SENSEX") {
		exch = "BFO"
	}
	if strings.HasPrefix(symbol, "NIFTY") || strings.HasPrefix(symbol, "BANKNIFTY") || strings.HasPrefix(symbol, "SENSEX") || strings.HasPrefix(symbol, "FINNIFTY") || strings.HasPrefix(symbol, "MIDCP") {
		cacheKey := "opt:" + exch + ":" + symbol
		if cached, err := sm.db.GetMetadataCache(context.Background(), cacheKey, time.Time{}); err == nil && cached != "" {
			var optToken int64
			if _, err := fmt.Sscanf(cached, "%d", &optToken); err == nil && optToken > 0 {
				return optToken, nil
			}
		}
		if optToken, err := sm.ResolveOptionSymbol(context.Background(), exch, symbol); err == nil && optToken > 0 {
			return optToken, nil
		}
	}
	return 0, fmt.Errorf("symbol not found in security master: %s", symbol)
}

// ResolveOptionSymbol attempts to lookup an NFO or BFO option/future symbol token from Zerodha API
func (sm *SecurityMaster) ResolveOptionSymbol(ctx context.Context, exchange, symbol string) (int64, error) {
	if sm.kite == nil {
		return 0, fmt.Errorf("kite client not initialized")
	}

	instruments, err := sm.kite.GetInstrumentsByExchange(exchange)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch %s instruments from Zerodha: %w", exchange, err)
	}

	for _, inst := range instruments {
		if inst.TradingSymbol == symbol {
			foundToken := int64(inst.InstrumentToken)
			_ = sm.db.SaveMetadataCache(ctx, "opt:"+exchange+":"+symbol, fmt.Sprintf("%d", foundToken))
			sm.logger.Info("Resolved and cached option instrument token",
				zap.String("exchange", exchange),
				zap.String("symbol", symbol),
				zap.Int64("token", foundToken),
			)
			return foundToken, nil
		}
	}
	return 0, fmt.Errorf("%s instrument not found for symbol: %s", exchange, symbol)
}

// ResolveNFOSymbol wraps ResolveOptionSymbol for backwards compatibility
func (sm *SecurityMaster) ResolveNFOSymbol(ctx context.Context, symbol string) (int64, error) {
	return sm.ResolveOptionSymbol(ctx, "NFO", symbol)
}

// GetOptionInstruments returns cached or live option instruments for an exchange ("NFO" or "BFO")
func (sm *SecurityMaster) GetOptionInstruments(ctx context.Context, exchange string) (Instruments, error) {
	sm.mu.RLock()
	if insts, ok := sm.optCache[exchange]; ok && time.Since(sm.optCacheTime[exchange]) < sm.cacheTTL {
		sm.mu.RUnlock()
		return insts, nil
	}
	sm.mu.RUnlock()

	if sm.kite == nil {
		return nil, fmt.Errorf("kite client not initialized")
	}

	insts, err := sm.kite.GetInstrumentsByExchange(exchange)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s instruments from Zerodha API: %w", exchange, err)
	}

	// Filter for options only to keep memory footprint minimal
	var optInsts Instruments
	for _, inst := range insts {
		if strings.Contains(inst.Segment, "OPT") || inst.InstrumentType == "CE" || inst.InstrumentType == "PE" {
			optInsts = append(optInsts, inst)
		}
	}

	sm.mu.Lock()
	if sm.optCache == nil {
		sm.optCache = make(map[string]Instruments)
		sm.optCacheTime = make(map[string]time.Time)
	}
	sm.optCache[exchange] = optInsts
	sm.optCacheTime[exchange] = time.Now()
	sm.mu.Unlock()

	sm.logger.Info("Cached option instruments from Zerodha",
		zap.String("exchange", exchange),
		zap.Int("count", len(optInsts)),
	)
	return optInsts, nil
}

// InjectOptionInstruments sets cached option instruments for testing and offline simulations
func (sm *SecurityMaster) InjectOptionInstruments(exchange string, insts Instruments) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.optCache == nil {
		sm.optCache = make(map[string]Instruments)
		sm.optCacheTime = make(map[string]time.Time)
	}
	sm.optCache[exchange] = insts
	sm.optCacheTime[exchange] = time.Now()
}

// GetIndexOptionChain returns real Zerodha option instruments for a given index, option type, and expiry type (MONTHLY vs WEEKLY)
func (sm *SecurityMaster) GetIndexOptionChain(ctx context.Context, indexName, optionType, expiryType string, rolloverDays int) ([]Instrument, error) {
	spec, _ := ResolveIndexSpec(indexName)
	if spec == nil {
		return nil, fmt.Errorf("unknown index spec: %s", indexName)
	}

	insts, err := sm.GetOptionInstruments(ctx, spec.OptionsExchange)
	if err != nil || len(insts) == 0 {
		return nil, fmt.Errorf("no option instruments available for exchange %s: %w", spec.OptionsExchange, err)
	}

	now := time.Now().In(ISTLocation)
	todayMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, ISTLocation)

	// 1. Filter by Name (CleanPrefix), InstrumentType ("CE" or "PE"), and Expiry >= Today
	var indexInsts []Instrument
	expiryMap := make(map[string]time.Time)

	for _, inst := range insts {
		if !strings.EqualFold(inst.Name, spec.CleanPrefix) && !strings.EqualFold(inst.Name, spec.Name) {
			continue
		}
		if inst.InstrumentType != optionType {
			continue
		}
		expIST := NormalizeToIST(inst.Expiry)
		expMidnight := time.Date(expIST.Year(), expIST.Month(), expIST.Day(), 0, 0, 0, 0, ISTLocation)
		if expMidnight.Before(todayMidnight) {
			continue // Expired
		}
		// If today is expiry day and past 15:30, exclude today
		if expMidnight.Equal(todayMidnight) && (now.Hour() > 15 || (now.Hour() == 15 && now.Minute() >= 30)) {
			continue
		}

		indexInsts = append(indexInsts, inst)
		expKey := expMidnight.Format("2006-01-02")
		expiryMap[expKey] = expMidnight
	}

	if len(indexInsts) == 0 {
		return nil, fmt.Errorf("no active %s options found for index %s", optionType, spec.Name)
	}

	// 2. Sort all available expiry dates chronologically
	var sortedExpiries []time.Time
	for _, exp := range expiryMap {
		sortedExpiries = append(sortedExpiries, exp)
	}
	sort.Slice(sortedExpiries, func(i, j int) bool {
		return sortedExpiries[i].Before(sortedExpiries[j])
	})

	if len(sortedExpiries) == 0 {
		return nil, fmt.Errorf("no future expiries found for %s", spec.Name)
	}

	// 3. Determine target expiry based on expiryType
	var targetExpiry time.Time
	if strings.ToUpper(expiryType) == "MONTHLY" {
		if rolloverDays <= 0 {
			rolloverDays = 7
		}
		// Group by Year-Month to find monthly (last expiry of month)
		monthExpiries := make(map[string]time.Time)
		for _, exp := range sortedExpiries {
			ym := exp.Format("2006-01")
			if curr, ok := monthExpiries[ym]; !ok || exp.After(curr) {
				monthExpiries[ym] = exp
			}
		}

		currYM := now.Format("2006-01")
		currMonthExpiry, hasCurr := monthExpiries[currYM]

		if hasCurr {
			daysRemaining := int(currMonthExpiry.Sub(now).Hours() / 24)
			if daysRemaining <= rolloverDays {
				// Roll over to next month's last expiry
				var futureYMs []string
				for ym := range monthExpiries {
					if ym > currYM {
						futureYMs = append(futureYMs, ym)
					}
				}
				sort.Strings(futureYMs)
				if len(futureYMs) > 0 {
					targetExpiry = monthExpiries[futureYMs[0]]
				} else {
					targetExpiry = currMonthExpiry
				}
			} else {
				targetExpiry = currMonthExpiry
			}
		} else if len(monthExpiries) > 0 {
			// Current month has no active contracts in Zerodha master (e.g. today in August, but master starts in September)
			// Find the earliest upcoming month >= currYM
			var upcomingYMs []string
			for ym := range monthExpiries {
				if ym >= currYM {
					upcomingYMs = append(upcomingYMs, ym)
				}
			}
			sort.Strings(upcomingYMs)
			if len(upcomingYMs) > 0 {
				targetExpiry = monthExpiries[upcomingYMs[0]]
			} else {
				var allYMs []string
				for ym := range monthExpiries {
					allYMs = append(allYMs, ym)
				}
				sort.Strings(allYMs)
				targetExpiry = monthExpiries[allYMs[len(allYMs)-1]]
			}
		} else if len(sortedExpiries) > 0 {
			targetExpiry = sortedExpiries[0]
		}
	} else {
		// WEEKLY: pick the nearest upcoming expiry
		targetExpiry = sortedExpiries[0]
	}

	// 4. Return all instruments for targetExpiry
	targetMidnight := time.Date(targetExpiry.Year(), targetExpiry.Month(), targetExpiry.Day(), 0, 0, 0, 0, ISTLocation)
	var finalContracts []Instrument
	for _, inst := range indexInsts {
		expIST := NormalizeToIST(inst.Expiry)
		expMidnight := time.Date(expIST.Year(), expIST.Month(), expIST.Day(), 0, 0, 0, 0, ISTLocation)
		if expMidnight.Equal(targetMidnight) {
			finalContracts = append(finalContracts, inst)
		}
	}

	return finalContracts, nil
}

// GetFOStocks returns NSE F&O underlyings with their tokens
func (sm *SecurityMaster) GetFOStocks(ctx context.Context) (map[string]int64, error) {
	cacheKey := "fo:stocks"

	// Try to get from PostgreSQL metadata_cache
	cached, err := sm.db.GetMetadataCache(ctx, cacheKey, time.Now().Add(-sm.cacheTTL))
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
				underlying := extractUnderlying(inst.TradingSymbol)
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
			if underlyingsMap[inst.TradingSymbol] {
				foStocks[inst.TradingSymbol] = int64(inst.InstrumentToken)
			}
		}
	}

	if len(foStocks) == 0 {
		return nil, fmt.Errorf("failed to resolve active F&O stocks from Zerodha Kite API")
	}

	// Cache in PostgreSQL
	if data, err := json.Marshal(foStocks); err == nil {
		err = sm.db.SaveMetadataCache(ctx, cacheKey, string(data))
		if err != nil {
			sm.logger.Error("Failed to cache F&O stocks in database", zap.Error(err))
		}
	}

	sm.logger.Info("Loaded F&O stocks", zap.Int("count", len(foStocks)))
	return foStocks, nil
}

// GetNifty500AndFOStocks returns the combined NIFTY 500 and F&O stock universe with their instrument tokens
func (sm *SecurityMaster) GetNifty500AndFOStocks(ctx context.Context) (map[string]int64, error) {
	cacheKey := "nifty500_fo:stocks"

	// Try to get from PostgreSQL metadata_cache
	cached, err := sm.db.GetMetadataCache(ctx, cacheKey, time.Now().Add(-sm.cacheTTL))
	if err == nil && cached != "" {
		var cachedStocks map[string]int64
		if err := json.Unmarshal([]byte(cached), &cachedStocks); err == nil && len(cachedStocks) > 0 {
			sm.logger.Info("Loaded Nifty500 & F&O stocks from cache", zap.Int("count", len(cachedStocks)))
			return cachedStocks, nil
		}
	}

	combined := make(map[string]int64)

	// 1. Load F&O stocks
	foStocks, err := sm.GetFOStocks(ctx)
	if err == nil && foStocks != nil {
		for sym, token := range foStocks {
			combined[sym] = token
		}
	}

	// 2. Load NIFTY 50 constituents
	nifty50, err := sm.GetNifty50Constituents(ctx)
	if err == nil && nifty50 != nil {
		for sym, token := range nifty50 {
			combined[sym] = token
		}
	}

	// 3. Load all NSE equity stocks and select top liquid candidates (up to 500)
	allNSE, err := sm.GetAllNSEStocks(ctx)
	if err == nil && allNSE != nil {
		for sym, token := range allNSE {
			if len(combined) >= 500 {
				break
			}
			combined[sym] = token
		}
	}

	if len(combined) == 0 {
		return sm.GetFOStocks(ctx)
	}

	// Cache in PostgreSQL
	if data, err := json.Marshal(combined); err == nil {
		_ = sm.db.SaveMetadataCache(ctx, cacheKey, string(data))
	}

	sm.logger.Info("Loaded Nifty 500 & F&O stocks universe", zap.Int("count", len(combined)))
	return combined, nil
}

// GetAllNSEStocks returns all active NSE equity stocks (EQ segment) with their instrument tokens
func (sm *SecurityMaster) GetAllNSEStocks(ctx context.Context) (map[string]int64, error) {
	cacheKey := "nse:all_stocks"

	// Try to get from PostgreSQL metadata_cache
	cached, err := sm.db.GetMetadataCache(ctx, cacheKey, time.Now().Add(-sm.cacheTTL))
	if err == nil && cached != "" {
		var cachedStocks map[string]int64
		if err := json.Unmarshal([]byte(cached), &cachedStocks); err == nil && len(cachedStocks) > 0 {
			sm.logger.Info("Loaded all NSE cash & F&O stocks from cache", zap.Int("count", len(cachedStocks)))
			return cachedStocks, nil
		}
	}

	// Fetch active NSE instruments from Zerodha Kite Connect API
	var allStocks = make(map[string]int64)
	if sm.kite != nil {
		sm.logger.Info("Fetching active NSE equity instruments from Zerodha API...")
		nseInstruments, err := sm.kite.GetInstrumentsByExchange("NSE")
		if err != nil {
			return nil, fmt.Errorf("failed to fetch NSE instruments from Zerodha API: %w", err)
		}

		for _, inst := range nseInstruments {
			if inst.Segment == "NSE" && inst.InstrumentType == "EQ" && inst.TradingSymbol != "" {
				// Exclude debt, bonds, G-Secs, rights entitlements, and secondary series (e.g. 0MOFSL27-N3, -BE, -BZ, -RE, -N1..N9, -SG)
				if strings.Contains(inst.TradingSymbol, "-") || (inst.TradingSymbol[0] >= '0' && inst.TradingSymbol[0] <= '9') {
					continue
				}
				allStocks[inst.TradingSymbol] = int64(inst.InstrumentToken)
			}
		}
	}

	if len(allStocks) == 0 {
		return sm.GetFOStocks(ctx)
	}

	// Cache in PostgreSQL
	if data, err := json.Marshal(allStocks); err == nil {
		_ = sm.db.SaveMetadataCache(ctx, cacheKey, string(data))
	}

	sm.logger.Info("Loaded all NSE cash & F&O stocks", zap.Int("count", len(allStocks)))
	return allStocks, nil
}

// RefreshMaster forces refresh of security master from API
func (sm *SecurityMaster) RefreshMaster(ctx context.Context) error {
	// Invalidate cache in PostgreSQL
	err := sm.db.DeleteMetadataCache(ctx, []string{"nifty50:constituents", "fo:underlyings", "fo:stocks", "nse:all_stocks"})
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

	if _, err := sm.GetAllNSEStocks(ctx); err != nil {
		sm.logger.Warn("Failed to refresh all NSE cash stocks", zap.Error(err))
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

// ResolveAndAddSymbol attempts to find the symbol token from Zerodha Kite API and inserts/merges it into the database 'fo:stocks' metadata cache.
func (sm *SecurityMaster) ResolveAndAddSymbol(ctx context.Context, symbol string) (int64, error) {
	sm.mu.RLock()
	if failTime, ok := sm.unresolved[symbol]; ok && time.Since(failTime) < 30*time.Minute {
		sm.mu.RUnlock()
		return 0, fmt.Errorf("symbol recently failed resolution (cached): %s", symbol)
	}
	sm.mu.RUnlock()

	// First, check if already present in memory
	if token, exists := sm.nifty50[symbol]; exists {
		return token, nil
	}

	// Try checking fo:stocks in database
	token, err := sm.db.QuerySymbolToken(ctx, symbol)
	if err == nil && token > 0 {
		return token, nil
	}

	// If not present, query Zerodha Kite API for all NSE instruments
	if sm.kite == nil {
		return 0, fmt.Errorf("kite client not initialized")
	}

	sm.logger.Info("Resolving symbol token from Zerodha Kite API...", zap.String("symbol", symbol))
	instruments, err := sm.kite.GetInstrumentsByExchange("NSE")
	if err != nil {
		return 0, fmt.Errorf("failed to fetch instruments from Zerodha: %w", err)
	}

	var foundToken int64
	for _, inst := range instruments {
		if inst.TradingSymbol == symbol && inst.InstrumentType == "EQ" {
			foundToken = int64(inst.InstrumentToken)
			break
		}
	}

	if foundToken == 0 {
		sm.mu.Lock()
		sm.unresolved[symbol] = time.Now()
		sm.mu.Unlock()
		return 0, fmt.Errorf("symbol not found in Zerodha NSE instruments list: %s", symbol)
	}

	// Add it to the metadata_cache under 'fo:stocks'
	// First, read the current 'fo:stocks' map
	var stocksMap = make(map[string]int64)
	cachedData, err := sm.db.GetMetadataCache(ctx, "fo:stocks", time.Time{})
	if err == nil {
		_ = json.Unmarshal([]byte(cachedData), &stocksMap)
	}

	// Put the new symbol-token mapping
	stocksMap[symbol] = foundToken

	// Marshal and save back
	marshaled, err := json.Marshal(stocksMap)
	if err == nil {
		_ = sm.db.SaveMetadataCache(ctx, "fo:stocks", string(marshaled))
	}

	sm.logger.Info("Successfully resolved and saved symbol token", zap.String("symbol", symbol), zap.Int64("token", foundToken))
	return foundToken, nil
}

type SelectedStock struct {
	Symbol string
	Token  int64
}

func (sm *SecurityMaster) GetEquityVolumeGainers(ctx context.Context, date time.Time) ([]SelectedStock, error) {
	dateStr := date.Format("2006-01-02")

	tickers, err := sm.db.GetEquityVolumeGainersTickers(ctx, dateStr)
	if err != nil {
		return nil, err
	}

	var selected []SelectedStock
	for _, ticker := range tickers {
		token, err := sm.ResolveAndAddSymbol(ctx, ticker)
		if err == nil && token > 0 {
			selected = append(selected, SelectedStock{Symbol: ticker, Token: token})
		} else {
			sm.logger.Warn("Failed to resolve instrument token for pre-selected ticker", zap.String("ticker", ticker), zap.Error(err))
		}
	}
	return selected, nil
}
