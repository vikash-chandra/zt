package selection

import (
	"context"
	"strings"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// HighLowBreakoutSelector selects stocks based on All-Time High/Low or 52-Week High/Low breakout criteria
type HighLowBreakoutSelector struct {
	Mode string // "ATH_ATL" or "52WH_52WL"
	db   *data.Database
}

// NewHighLowBreakoutSelector creates a new HighLowBreakoutSelector instance
func NewHighLowBreakoutSelector(mode string, db *data.Database) *HighLowBreakoutSelector {
	return &HighLowBreakoutSelector{
		Mode: NormalizeSelectorName(mode),
		db:   db,
	}
}

// Name returns selector identity name
func (s *HighLowBreakoutSelector) Name() string {
	return s.Mode
}

// SelectStocks selects candidates from daily_watchlists, quant_scanner_results, or universe
func (s *HighLowBreakoutSelector) SelectStocks(ctx context.Context, logger *zap.Logger, client data.BrokerClient, secMaster *data.SecurityMaster, bias string, size int, maxPctChange float64) (map[string]int64, error) {
	if bias == "NO_TRADE" {
		logger.Info("Global bias is NO_TRADE. Skipping "+s.Mode+" selection.", zap.String("bias", bias))
		return make(map[string]int64), nil
	}

	if size <= 0 {
		size = 10
	}

	results := make(map[string]int64)

	// 1. Sourcing from daily_watchlists if already tagged with this selector
	if s.db != nil {
		todayStr := data.GetEffectiveTradingDate(data.NowIST())
		pattern := s.Mode
		if s.Mode == "52WH_52WL" {
			pattern = "52W"
		} else if s.Mode == "ATH_ATL" {
			pattern = "ATH"
		}
		if items, err := s.db.GetDailyWatchlistStocksBySelector(ctx, todayStr, pattern); err == nil && len(items) > 0 {
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
		var breakoutTypes []string
		if s.Mode == "ATH_ATL" {
			breakoutTypes = []string{"AllTimeHighBreak", "AllTimeLowBreak"}
		} else {
			breakoutTypes = []string{"YearlyHighBreak", "YearlyLowBreak", "AllTimeHighBreak", "AllTimeLowBreak"}
		}

		todayStr := data.GetEffectiveTradingDate(data.NowIST())
		candidates, _ := s.db.GetQuantScannerCandidates(ctx, todayStr, breakoutTypes, bias, size*2)

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
