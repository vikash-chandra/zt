package selection

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"zerodha-trading/config"
	"zerodha-trading/data"

	"go.uber.org/zap"
)

// DefaultSectorConstituents maps default key F&O sectors to constituent stock symbols (used as fallback)
var DefaultSectorConstituents = map[string][]string{
	"BANK":   {"HDFCBANK", "ICICIBANK", "KOTAKBANK", "SBIN", "AXISBANK", "INDUSINDBK", "AUBANK", "FEDERALBNK", "PNB", "BANKBARODA"},
	"IT":     {"TCS", "INFY", "WIPRO", "HCLTECH", "TECHM", "LTIM", "COFORGE", "MPHASIS", "PERSISTENT"},
	"AUTO":   {"MARUTI", "TATAMOTORS", "M&M", "BAJAJ-AUTO", "HEROMOTOCO", "TVSMOTOR", "EICHERMOT", "ASHOKLEY", "BALKRISIND"},
	"PHARMA": {"SUNPHARMA", "CIPLA", "DRREDDY", "DIVISLAB", "LUPIN", "AUROPHARMA", "BIOCON", "TORNTPHARM", "IPCALAB"},
	"METAL":  {"TATASTEEL", "JINDALSTEL", "HINDALCO", "JSWSTEEL", "SAIL", "NATIONALUM", "NMDC", "VEDL"},
	"FMCG":   {"HINDUNILVR", "ITC", "NESTLEIND", "BRITANNIA", "TATACONSUM", "DABUR", "MARICO", "GODREJCP", "COLPAL"},
	"ENERGY": {"RELIANCE", "ONGC", "NTPC", "POWERGRID", "BPCL", "IOC", "GAIL", "ADANIENT", "ADANIPORTS"},
	"REALTY": {"DLF", "GODREJPROP", "OBEROIRLTY"},
	"MEDIA":  {"ZEEL", "SUNTV", "PVRINOX"},
}

// Backward compatibility alias
var SectorConstituents = DefaultSectorConstituents

// SectoralSelector implements Selector for sectoral stock selection
type SectoralSelector struct {
	cfg   *config.Settings
	db    *data.Database
	Force bool
}

// NewSectoralSelector creates a new SectoralSelector instance
func NewSectoralSelector(cfg *config.Settings, db *data.Database) *SectoralSelector {
	return &SectoralSelector{cfg: cfg, db: db, Force: false}
}

// Name returns selector identity name
func (s *SectoralSelector) Name() string {
	return "SECTORAL_SELECTOR"
}

// SelectStocks runs sector calculations and stock percentage filters to return the watchlist
func (s *SectoralSelector) SelectStocks(ctx context.Context, logger *zap.Logger, client data.BrokerClient, secMaster *data.SecurityMaster, bias string, size int, maxPctChange float64) (map[string]int64, error) {
	kiteClient := client

	// Dynamically load active user-managed sector definitions from database
	var sectorConstituents map[string][]string
	if s.db != nil {
		if dbSectors, err := s.db.GetSectorConstituentsMap(ctx); err == nil && len(dbSectors) > 0 {
			sectorConstituents = dbSectors
		}
	}
	if len(sectorConstituents) == 0 {
		sectorConstituents = DefaultSectorConstituents
	}

	foStocksMap, err := secMaster.GetFOStocks(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch active F&O stocks: %w", err)
	}

	todayStr := data.GetEffectiveTradingDate(time.Now())

	// 1. If not forced and today's selected sectors already exist in database, reuse them directly without re-scanning or wiping DB
	if s.db != nil && !s.Force {
		existingSectors, err := s.db.GetSelectedSectors(ctx, todayStr)
		if err == nil && len(existingSectors) > 0 {
			logger.Info("Found existing selected sectors in database for today. Reusing without re-scanning.",
				zap.String("date", todayStr),
				zap.Int("count", len(existingSectors)),
			)

			selectedSectors := make(map[string]bool)
			for _, rec := range existingSectors {
				selectedSectors[rec.Sector] = true
				logger.Info("Reused selected sector from database",
					zap.String("sector", rec.Sector),
					zap.Float64("pct_change", rec.PctChange),
				)
			}

			// Gather keys for only constituent stocks in these already-selected sectors
			var keys []string
			symbolToToken := make(map[string]int64)
			for sector := range selectedSectors {
				for _, sym := range sectorConstituents[sector] {
					if token, ok := foStocksMap[sym]; ok {
						keys = append(keys, "NSE:"+sym)
						symbolToToken[sym] = token
					}
				}
			}

			if len(keys) > 0 {
				stockChanges := make(map[string]float64)
				if kiteClient != nil {
					batchSize := 400
					for i := 0; i < len(keys); i += batchSize {
						end := i + batchSize
						if end > len(keys) {
							end = len(keys)
						}
						batchKeys := keys[i:end]
						batchData, err := kiteClient.GetOHLC(batchKeys...)
						if err != nil {
							logger.Warn("Failed to fetch batch OHLC for existing sector constituents", zap.Error(err))
							break
						}
						for k, entry := range batchData {
							open := entry.OHLC.Open
							ltp := entry.LastPrice
							sym := k[4:]
							refPrice := open
							if refPrice == 0 {
								refPrice = entry.OHLC.Close
							}
							if refPrice == 0 {
								refPrice = ltp
							}
							if refPrice > 0 {
								stockChanges[sym] = ((ltp - refPrice) / refPrice) * 100.0
							}
						}
					}
				}

				type StockPerf struct {
					Symbol string
					Token  int64
					Change float64
				}
				var eligibleStocks []StockPerf

				for sector := range selectedSectors {
					for _, sym := range sectorConstituents[sector] {
						token, existsToken := symbolToToken[sym]
						if !existsToken {
							continue
						}
						change := stockChanges[sym]

						if bias == "BUY_ONLY" {
							if s.cfg == nil || change <= s.cfg.StockMaxBuyPct {
								eligibleStocks = append(eligibleStocks, StockPerf{Symbol: sym, Token: token, Change: change})
							}
						} else if bias == "SELL_ONLY" {
							if s.cfg == nil || change >= s.cfg.StockMaxSellPct {
								eligibleStocks = append(eligibleStocks, StockPerf{Symbol: sym, Token: token, Change: change})
							}
						} else { // BOTH / Setup-driven
							if s.cfg == nil || (change <= s.cfg.StockMaxBuyPct && change >= s.cfg.StockMaxSellPct) {
								eligibleStocks = append(eligibleStocks, StockPerf{Symbol: sym, Token: token, Change: change})
							}
						}
					}
				}

				if bias == "BUY_ONLY" {
					sort.Slice(eligibleStocks, func(i, j int) bool {
						return eligibleStocks[i].Change > eligibleStocks[j].Change
					})
				} else if bias == "SELL_ONLY" {
					sort.Slice(eligibleStocks, func(i, j int) bool {
						return eligibleStocks[i].Change < eligibleStocks[j].Change
					})
				} else {
					sort.Slice(eligibleStocks, func(i, j int) bool {
						return math.Abs(eligibleStocks[i].Change) > math.Abs(eligibleStocks[j].Change)
					})
				}

				finalSize := size
				if len(eligibleStocks) < finalSize {
					finalSize = len(eligibleStocks)
				}

				selectedWatchlist := make(map[string]int64)
				for i := 0; i < finalSize; i++ {
					selectedWatchlist[eligibleStocks[i].Symbol] = eligibleStocks[i].Token
					logger.Info("Sectoral stock selected from existing sectors",
						zap.Int("rank", i+1),
						zap.String("symbol", eligibleStocks[i].Symbol),
						zap.Float64("change", eligibleStocks[i].Change),
						zap.Int64("token", eligibleStocks[i].Token),
					)
				}

				return selectedWatchlist, nil
			}
		}
	}

	// 2. Full scan when scheduled, forced, or when no sectors exist for today in database
	logger.Info("Running full sectoral stock scan", zap.String("date", todayStr), zap.Bool("force", s.Force))

	// Get OHLC for all constituents in our active sector map
	var keys []string
	symbolToToken := make(map[string]int64)
	for _, constituents := range sectorConstituents {
		for _, sym := range constituents {
			if token, ok := foStocksMap[sym]; ok {
				keys = append(keys, "NSE:"+sym)
				symbolToToken[sym] = token
			}
		}
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("no mapped sector constituents found in active F&O list")
	}

	ohlcData := make(data.QuoteOHLC)
	if kiteClient != nil {
		batchSize := 400
		for i := 0; i < len(keys); i += batchSize {
			end := i + batchSize
			if end > len(keys) {
				end = len(keys)
			}
			batchKeys := keys[i:end]
			batchData, err := kiteClient.GetOHLC(batchKeys...)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch batch OHLC for constituents: %w", err)
			}
			for k, v := range batchData {
				ohlcData[k] = v
			}
		}
	}

	// Calculate stock performances
	stockChanges := make(map[string]float64)
	for key, entry := range ohlcData {
		open := entry.OHLC.Open
		ltp := entry.LastPrice
		sym := key[4:] // remove "NSE:"

		refPrice := open
		if refPrice == 0 {
			refPrice = entry.OHLC.Close
		}
		if refPrice == 0 {
			refPrice = ltp
		}
		if refPrice > 0 {
			stockChanges[sym] = ((ltp - refPrice) / refPrice) * 100.0
		}
	}

	// Calculate sector performances
	sectorChanges := make(map[string]float64)
	for sector, constituents := range sectorConstituents {
		var sum float64
		count := 0
		for _, sym := range constituents {
			if change, exists := stockChanges[sym]; exists {
				sum += change
				count++
			}
		}
		if count > 0 {
			sectorChanges[sector] = sum / float64(count)
		}
	}

	logger.Info("Calculated sector performances", zap.Any("sectors", sectorChanges))

	// Filter sectors based on bias
	type SectorPerf struct {
		Name   string
		Change float64
	}
	var filteredSectors []SectorPerf

	for name, change := range sectorChanges {
		if bias == "BUY_ONLY" {
			if s.cfg == nil || (change > 0.0 && change <= s.cfg.SectorMaxBuyPct) {
				filteredSectors = append(filteredSectors, SectorPerf{Name: name, Change: change})
			}
		} else if bias == "SELL_ONLY" {
			if s.cfg == nil || (change < 0.0 && change >= s.cfg.SectorMaxSellPct) {
				filteredSectors = append(filteredSectors, SectorPerf{Name: name, Change: change})
			}
		} else { // BOTH / Setup-driven (No global bias restriction)
			if s.cfg == nil || (change > 0.0 && change <= s.cfg.SectorMaxBuyPct) || (change < 0.0 && change >= s.cfg.SectorMaxSellPct) {
				filteredSectors = append(filteredSectors, SectorPerf{Name: name, Change: change})
			}
		}
	}

	if len(filteredSectors) == 0 {
		logger.Warn("No sectors satisfied threshold filter, taking top performing sectors by absolute move", zap.String("bias", bias))
		for name, change := range sectorChanges {
			filteredSectors = append(filteredSectors, SectorPerf{Name: name, Change: change})
		}
	}
	if len(filteredSectors) == 0 {
		return nil, nil
	}

	// Select top sectors with largest absolute change
	if bias == "BUY_ONLY" {
		sort.Slice(filteredSectors, func(i, j int) bool {
			return filteredSectors[i].Change > filteredSectors[j].Change // largest positive changes
		})
	} else if bias == "SELL_ONLY" {
		sort.Slice(filteredSectors, func(i, j int) bool {
			return filteredSectors[i].Change < filteredSectors[j].Change // most declined first
		})
	} else {
		sort.Slice(filteredSectors, func(i, j int) bool {
			return math.Abs(filteredSectors[i].Change) > math.Abs(filteredSectors[j].Change) // largest absolute change
		})
	}

	topSectorCount := 3
	if s.cfg != nil && s.cfg.SectorScannerTopN > 0 {
		topSectorCount = s.cfg.SectorScannerTopN
	}
	if len(filteredSectors) < topSectorCount {
		topSectorCount = len(filteredSectors)
	}

	if s.db != nil && topSectorCount > 0 {
		_ = s.db.ClearSelectedSectors(ctx, todayStr)
	}

	selectedSectors := make(map[string]bool)
	for i := 0; i < topSectorCount; i++ {
		selectedSectors[filteredSectors[i].Name] = true
		logger.Info("Selected sector for watchlist",
			zap.Int("rank", i+1),
			zap.String("sector", filteredSectors[i].Name),
			zap.Float64("change", filteredSectors[i].Change),
		)

		if s.db != nil {
			err := s.db.SaveSelectedSector(ctx, todayStr, filteredSectors[i].Name, filteredSectors[i].Change, time.Now().In(data.ISTLocation))
			if err != nil {
				logger.Error("Failed to save selected sector to database", zap.Error(err), zap.String("sector", filteredSectors[i].Name))
			}
		}
	}

	// Gather stocks in selected sectors and apply filters
	type StockPerf struct {
		Symbol string
		Token  int64
		Change float64
	}
	var eligibleStocks []StockPerf

	for sector := range selectedSectors {
		for _, sym := range sectorConstituents[sector] {
			change, exists := stockChanges[sym]
			if !exists {
				continue
			}

			token, existsToken := symbolToToken[sym]
			if !existsToken {
				continue
			}

			if bias == "BUY_ONLY" {
				if s.cfg == nil || change <= s.cfg.StockMaxBuyPct {
					eligibleStocks = append(eligibleStocks, StockPerf{Symbol: sym, Token: token, Change: change})
				}
			} else if bias == "SELL_ONLY" {
				if s.cfg == nil || change >= s.cfg.StockMaxSellPct {
					eligibleStocks = append(eligibleStocks, StockPerf{Symbol: sym, Token: token, Change: change})
				}
			} else { // BOTH / Setup-driven
				if s.cfg == nil || (change <= s.cfg.StockMaxBuyPct && change >= s.cfg.StockMaxSellPct) {
					eligibleStocks = append(eligibleStocks, StockPerf{Symbol: sym, Token: token, Change: change})
				}
			}
		}
	}

	// Sort and return the top stocks by absolute change
	if bias == "BUY_ONLY" {
		sort.Slice(eligibleStocks, func(i, j int) bool {
			return eligibleStocks[i].Change > eligibleStocks[j].Change // highest gainers first
		})
	} else if bias == "SELL_ONLY" {
		sort.Slice(eligibleStocks, func(i, j int) bool {
			return eligibleStocks[i].Change < eligibleStocks[j].Change // most declined first
		})
	} else {
		sort.Slice(eligibleStocks, func(i, j int) bool {
			return math.Abs(eligibleStocks[i].Change) > math.Abs(eligibleStocks[j].Change) // largest magnitude first
		})
	}

	finalSize := size
	if len(eligibleStocks) < finalSize {
		finalSize = len(eligibleStocks)
	}

	selectedWatchlist := make(map[string]int64)
	for i := 0; i < finalSize; i++ {
		selectedWatchlist[eligibleStocks[i].Symbol] = eligibleStocks[i].Token
		logger.Info("Sectoral stock selected",
			zap.Int("rank", i+1),
			zap.String("symbol", eligibleStocks[i].Symbol),
			zap.Float64("change", eligibleStocks[i].Change),
			zap.Int64("token", eligibleStocks[i].Token),
		)
	}

	return selectedWatchlist, nil
}
