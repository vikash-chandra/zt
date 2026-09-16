package selection

import (
	"context"
	"strings"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// QuantScannerSelector selects top ranked candidates from PostgreSQL quant_scanner_results
type QuantScannerSelector struct {
	db *data.Database
}

// NewQuantScannerSelector creates a new QuantScannerSelector instance
func NewQuantScannerSelector(db *data.Database) *QuantScannerSelector {
	return &QuantScannerSelector{db: db}
}

// Name returns selector identity name
func (s *QuantScannerSelector) Name() string {
	return "QUANT_SCANNER"
}

// SelectStocks selects candidates from daily_watchlists, quant_scanner_results, or universe
func (s *QuantScannerSelector) SelectStocks(ctx context.Context, logger *zap.Logger, client data.BrokerClient, secMaster *data.SecurityMaster, bias string, size int, maxPctChange float64) (map[string]int64, error) {
	if bias == "NO_TRADE" {
		logger.Info("Global bias is NO_TRADE. Skipping QUANT_SCANNER selection.", zap.String("bias", bias))
		return make(map[string]int64), nil
	}

	if size <= 0 {
		size = 10
	}

	results := make(map[string]int64)

	// 1. Sourcing from daily_watchlists if already tagged with QUANT_SCANNER
	if s.db != nil {
		todayStr := data.GetEffectiveTradingDate(data.NowIST())
		if items, err := s.db.GetDailyWatchlistStocksBySelector(ctx, todayStr, "QUANT"); err == nil && len(items) > 0 {
			for _, item := range items {
				results[item.Symbol] = item.Token
				if len(results) >= size {
					return results, nil
				}
			}
		}
	}

	// 2. Query quant_scanner_results from PostgreSQL
	if s.db != nil {
		todayStr := data.GetEffectiveTradingDate(data.NowIST())
		candidates, _ := s.db.GetQuantScannerCandidates(ctx, todayStr, nil, bias, size*2)

		for _, c := range candidates {
			sym := strings.TrimSpace(c.Symbol)
			if sym == "" || sym == "NIFTY 50" || sym == "GOLD" || sym == "CRUDEOIL" {
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

	return results, nil
}
