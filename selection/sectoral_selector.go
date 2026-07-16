package selection

import (
	"context"
	"fmt"
	"sort"
	"time"

	"zerodha-trading/config"
	"zerodha-trading/data"

	"go.uber.org/zap"
)

// SectorConstituents maps key F&O sectors to their constituent stock symbols
var SectorConstituents = map[string][]string{
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

// SectoralSelector implements Selector for sectoral stock selection
type SectoralSelector struct {
	cfg *config.Settings
	db  *data.Database
}

// NewSectoralSelector creates a new SectoralSelector instance
func NewSectoralSelector(cfg *config.Settings, db *data.Database) *SectoralSelector {
	return &SectoralSelector{cfg: cfg, db: db}
}

// Name returns selector identity name
func (s *SectoralSelector) Name() string {
	return "SECTORAL_SELECTOR"
}

// SelectStocks runs sector calculations and stock percentage filters to return the watchlist
func (s *SectoralSelector) SelectStocks(ctx context.Context, logger *zap.Logger, client data.BrokerClient, secMaster *data.SecurityMaster, bias string, size int, maxPctChange float64) (map[string]int64, error) {
	kiteClient := client

	foStocksMap, err := secMaster.GetFOStocks(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch active F&O stocks: %w", err)
	}

	// 1. Get OHLC for all constituents in our sector map
	var keys []string
	symbolToToken := make(map[string]int64)
	for _, constituents := range SectorConstituents {
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

	// Calculate stock performances
	stockChanges := make(map[string]float64)
	for key, entry := range ohlcData {
		open := entry.OHLC.Open
		ltp := entry.LastPrice
		sym := key[4:] // remove "NSE:"

		if open > 0 {
			stockChanges[sym] = ((ltp - open) / open) * 100.0
		}
	}

	// 2. Calculate sector performances
	sectorChanges := make(map[string]float64)
	for sector, constituents := range SectorConstituents {
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

	// 3. Filter sectors based on bias
	type SectorPerf struct {
		Name   string
		Change float64
	}
	var filteredSectors []SectorPerf

	for name, change := range sectorChanges {
		if bias == "BUY_ONLY" {
			if change > 0.0 && change <= s.cfg.SectorMaxBuyPct {
				filteredSectors = append(filteredSectors, SectorPerf{Name: name, Change: change})
			}
		} else { // SELL_ONLY
			if change < 0.0 && change >= s.cfg.SectorMaxSellPct {
				filteredSectors = append(filteredSectors, SectorPerf{Name: name, Change: change})
			}
		}
	}

	if len(filteredSectors) == 0 {
		logger.Warn("No sectors satisfied the threshold filter", zap.String("bias", bias))
		return nil, nil
	}

	// 4. Select top 2 sectors with largest absolute change
	if bias == "BUY_ONLY" {
		sort.Slice(filteredSectors, func(i, j int) bool {
			return filteredSectors[i].Change > filteredSectors[j].Change // largest positive changes
		})
	} else { // SELL_ONLY
		sort.Slice(filteredSectors, func(i, j int) bool {
			return filteredSectors[i].Change < filteredSectors[j].Change // most declined first
		})
	}

	topSectorCount := 2
	if len(filteredSectors) < topSectorCount {
		topSectorCount = len(filteredSectors)
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
			todayStr := time.Now().Format("2006-01-02")
			err := s.db.SaveSelectedSector(ctx, todayStr, filteredSectors[i].Name, filteredSectors[i].Change, time.Now())
			if err != nil {
				logger.Error("Failed to save selected sector to database", zap.Error(err), zap.String("sector", filteredSectors[i].Name))
			}
		}
	}

	// 5. Gather stocks in selected sectors and apply filters
	type StockPerf struct {
		Symbol string
		Token  int64
		Change float64
	}
	var eligibleStocks []StockPerf

	for sector := range selectedSectors {
		for _, sym := range SectorConstituents[sector] {
			change, exists := stockChanges[sym]
			if !exists {
				continue
			}

			token, existsToken := symbolToToken[sym]
			if !existsToken {
				continue
			}

			if bias == "BUY_ONLY" {
				if change <= s.cfg.StockMaxBuyPct {
					eligibleStocks = append(eligibleStocks, StockPerf{Symbol: sym, Token: token, Change: change})
				}
			} else { // SELL_ONLY
				if change >= s.cfg.StockMaxSellPct {
					eligibleStocks = append(eligibleStocks, StockPerf{Symbol: sym, Token: token, Change: change})
				}
			}
		}
	}

	// 6. Sort and return the top 10 stocks by absolute change
	if bias == "BUY_ONLY" {
		sort.Slice(eligibleStocks, func(i, j int) bool {
			return eligibleStocks[i].Change > eligibleStocks[j].Change // highest gainers first
		})
	} else {
		sort.Slice(eligibleStocks, func(i, j int) bool {
			return eligibleStocks[i].Change < eligibleStocks[j].Change // most declined first
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
