package selection

import (
	"context"
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



	// 1. Sourcing from pre_selection_results matching the category strictly for today
	if s.db != nil {
		todayStr := data.GetEffectiveTradingDate(data.NowIST())
		preCandidates, err := s.db.GetPreSelectionCandidatesByReason(ctx, todayStr, s.Category, bias, size*2)
		if err == nil && len(preCandidates) > 0 {
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
	}

	return results, nil
}
