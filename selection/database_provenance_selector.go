package selection

import (
	"context"
	"sort"
	"strings"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// DatabaseProvenanceSelector dynamically selects stocks based on database provenance tags,
// pre-selection results, or quant scanner news annotations for categories like NEWS, RESULT,
// HIGH_IMPACT_NEWS, PT_SCREENER, PT_ADVANCE, and OTHERS.
type DatabaseProvenanceSelector struct {
	Category string
	db       *data.Database
}

// NewDatabaseProvenanceSelector creates a new DatabaseProvenanceSelector instance
func NewDatabaseProvenanceSelector(category string, db *data.Database) *DatabaseProvenanceSelector {
	return &DatabaseProvenanceSelector{
		Category: NormalizeSelectorName(category),
		db:       db,
	}
}

// Name returns selector identity name
func (s *DatabaseProvenanceSelector) Name() string {
	return s.Category
}

// SelectStocks selects candidates from daily_watchlists, pre_selection_results, quant_scanner_results, or universe
func (s *DatabaseProvenanceSelector) SelectStocks(ctx context.Context, logger *zap.Logger, client data.BrokerClient, secMaster *data.SecurityMaster, bias string, size int, maxPctChange float64) (map[string]int64, error) {
	if bias == "NO_TRADE" {
		logger.Info("Global bias is NO_TRADE. Skipping "+s.Category+" selection.", zap.String("bias", bias))
		return make(map[string]int64), nil
	}

	if size <= 0 {
		size = 10
	}

	results := make(map[string]int64)

	// 1. Sourcing from daily_watchlists for today if already tagged with this category
	if s.db != nil {
		todayStr := data.GetEffectiveTradingDate(data.NowIST())
		if items, err := s.db.GetDailyWatchlistStocksBySelector(ctx, todayStr, s.Category); err == nil && len(items) > 0 {
			for _, item := range items {
				results[item.Symbol] = item.Token
				if len(results) >= size {
					return results, nil
				}
			}
		}
	}

	// 2. Sourcing from quant_scanner_results (especially for NEWS and HIGH_IMPACT_NEWS)
	if (s.Category == "NEWS" || s.Category == "HIGH_IMPACT_NEWS") && s.db != nil {
		todayStr := data.GetEffectiveTradingDate(data.NowIST())
		allScan, err := s.db.GetScannerResultsByDate(ctx, todayStr)
		if err != nil || len(allScan) == 0 {
			allScan, _ = s.db.GetScannerResultsByDate(ctx, "")
		}

		for _, sc := range allScan {
			sym := strings.TrimSpace(sc.Symbol)
			if sym == "" || sym == "NIFTY 50" || sym == "GOLD" || sym == "CRUDEOIL" {
				continue
			}
			if sc.NewsSummary != "" || sc.NewsSentiment != "" {
				var token int64
				if secMaster != nil {
					token, _ = secMaster.GetInstrumentToken(sym)
				}
				if token <= 0 && s.db != nil {
					token, _ = s.db.ResolveSymbolToken(ctx, sym)
				}
				if token > 0 {
					results[sym] = token
					if len(results) >= size {
						return results, nil
					}
				}
			}
		}
	}

	// 3. Sourcing from pre_selection_results matching the category
	if s.db != nil {
		todayStr := data.GetEffectiveTradingDate(data.NowIST())
		preCandidates, err := s.db.GetPreSelectionCandidatesByReason(ctx, todayStr, s.Category, bias, size*2)
		if err != nil || len(preCandidates) == 0 {
			preCandidates, _ = s.db.GetPreSelectionCandidatesByReason(ctx, "", s.Category, bias, size*2)
		}

		for _, p := range preCandidates {
			sym := strings.TrimSpace(p.Ticker)
			if sym == "" {
				continue
			}
			var token int64
			if secMaster != nil {
				token, _ = secMaster.GetInstrumentToken(sym)
			}
			if token <= 0 && s.db != nil {
				token, _ = s.db.ResolveSymbolToken(ctx, sym)
			}
			if token > 0 {
				results[sym] = token
				if len(results) >= size {
					return results, nil
				}
			}
		}
	}

	// 4. Sourcing top general pre_selection_results if specific category matches were fewer than size
	if len(results) < size && s.db != nil {
		todayStr := data.GetEffectiveTradingDate(data.NowIST())
		generalCandidates, _ := s.db.GetPreSelectionCandidatesByReason(ctx, todayStr, "", bias, size*2)
		for _, p := range generalCandidates {
			sym := strings.TrimSpace(p.Ticker)
			if sym == "" {
				continue
			}
			if _, exists := results[sym]; !exists {
				var token int64
				if secMaster != nil {
					token, _ = secMaster.GetInstrumentToken(sym)
				}
				if token <= 0 && s.db != nil {
					token, _ = s.db.ResolveSymbolToken(ctx, sym)
				}
				if token > 0 {
					results[sym] = token
					if len(results) >= size {
						return results, nil
					}
				}
			}
		}
	}

	// 5. Fallback to active F&O counters if needed
	if len(results) < size && secMaster != nil {
		foStocksMap, _ := secMaster.GetFOStocks(ctx)
		if len(foStocksMap) == 0 {
			foStocksMap, _ = secMaster.GetNifty50Constituents(ctx)
		}

		var syms []string
		for sym := range foStocksMap {
			if _, exists := results[sym]; !exists {
				syms = append(syms, sym)
			}
		}
		sort.Strings(syms)

		for _, sym := range syms {
			results[sym] = foStocksMap[sym]
			if len(results) >= size {
				break
			}
		}
	}

	return results, nil
}
