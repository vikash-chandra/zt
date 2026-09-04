package selection

import (
	"context"
	"sort"
	"strings"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/risk"

	"go.uber.org/zap"
)

// StockSelectionStrategyConfig defines configuration properties for a stock selection algorithm
type StockSelectionStrategyConfig struct {
	Name          string  `json:"name"`
	DisplayName   string  `json:"display_name"`
	Enabled       bool    `json:"enabled"`
	PriorityRank  int     `json:"priority_rank"`   // 1 = Highest priority, 2, 3...
	LevelShiftPct float64 `json:"level_shift_pct"` // Price level shift % (e.g. -2.0% on PDH)
	WatchlistSize int     `json:"watchlist_size"`
	Description   string  `json:"description"`
}

// DefaultStockSelectionConfigs returns default setup for all 9 stock selection strategies
func DefaultStockSelectionConfigs() map[string]StockSelectionStrategyConfig {
	return map[string]StockSelectionStrategyConfig{
		"PDH_PDL": {
			Name:          "PDH_PDL",
			DisplayName:   "PDH-PDL Breakout",
			Enabled:       true,
			PriorityRank:  1,
			LevelShiftPct: 0.0,
			WatchlistSize: 10,
			Description:   "Previous Day High & Low breakout levels with configurable shift buffer",
		},
		"ATH_ATL": {
			Name:          "ATH_ATL",
			DisplayName:   "ATH-ATL Breakout",
			Enabled:       true,
			PriorityRank:  2,
			LevelShiftPct: 0.0,
			WatchlistSize: 5,
			Description:   "All Time High & All Time Low multi-year expansion levels",
		},
		"52WH_52WL": {
			Name:          "52WH_52WL",
			DisplayName:   "52WH-52WL Breakout",
			Enabled:       true,
			PriorityRank:  3,
			LevelShiftPct: 0.0,
			WatchlistSize: 5,
			Description:   "52-Week High & 52-Week Low major annual boundary levels",
		},
		"NEWS": {
			Name:          "NEWS",
			DisplayName:   "News Momentum",
			Enabled:       true,
			PriorityRank:  4,
			LevelShiftPct: 0.0,
			WatchlistSize: 5,
			Description:   "Sentiment and regular corporate news catalyst momentum",
		},
		"HIGH_IMPACT_NEWS": {
			Name:          "HIGH_IMPACT_NEWS",
			DisplayName:   "High Impact News",
			Enabled:       true,
			PriorityRank:  5,
			LevelShiftPct: 0.0,
			WatchlistSize: 5,
			Description:   "Breaking macro & high-impact stock news events",
		},
		"RESULT": {
			Name:          "RESULT",
			DisplayName:   "Results",
			Enabled:       true,
			PriorityRank:  6,
			LevelShiftPct: 0.0,
			WatchlistSize: 5,
			Description:   "Quarterly financial results & earnings calendar announcements",
		},
		"FO": {
			Name:          "FO",
			DisplayName:   "F&O Momentum",
			Enabled:       true,
			PriorityRank:  7,
			LevelShiftPct: 0.0,
			WatchlistSize: 10,
			Description:   "F&O universe top gainers, losers, and open gap momentum",
		},
		"SECTOR": {
			Name:          "SECTOR",
			DisplayName:   "Sector Allocation",
			Enabled:       true,
			PriorityRank:  8,
			LevelShiftPct: 0.0,
			WatchlistSize: 10,
			Description:   "Top performing sector weighted momentum allocation",
		},
		"QUANT_SCANNER": {
			Name:          "QUANT_SCANNER",
			DisplayName:   "Quant Scanner",
			Enabled:       true,
			PriorityRank:  9,
			LevelShiftPct: 0.0,
			WatchlistSize: 10,
			Description:   "Multi-factor quant scanner (momentum, RSI, ATR)",
		},
		"PT_SCREENER": {
			Name:          "PT_SCREENER",
			DisplayName:   "PT Screener",
			Enabled:       true,
			PriorityRank:  10,
			LevelShiftPct: 0.0,
			WatchlistSize: 5,
			Description:   "Price Action Trend screener universe",
		},
		"PT_ADVANCE": {
			Name:          "PT_ADVANCE",
			DisplayName:   "PT Advance",
			Enabled:       true,
			PriorityRank:  11,
			LevelShiftPct: 0.0,
			WatchlistSize: 5,
			Description:   "Advanced technical trend candidates",
		},
		"OTHERS": {
			Name:          "OTHERS",
			DisplayName:   "Others",
			Enabled:       true,
			PriorityRank:  12,
			LevelShiftPct: 0.0,
			WatchlistSize: 5,
			Description:   "Special / discretionary custom momentum candidates",
		},
	}
}

// Selector defines the interface for dynamic watchlist selection algorithms
type Selector interface {
	Name() string
	SelectStocks(ctx context.Context, logger *zap.Logger, client data.BrokerClient, secMaster *data.SecurityMaster, bias string, size int, maxPctChange float64) (map[string]int64, error)
}

// InitializeSelectors instantiates and maps active selectors by name
func InitializeSelectors(names []string, cfg *config.Settings, db *data.Database) map[string]Selector {
	m := make(map[string]Selector)
	for _, name := range names {
		norm := NormalizeSelectorName(name)
		switch norm {
		case "FO", "SECURITIES_FO":
			m["SECURITIES_FO"] = NewSecuritiesFOSelector()
		case "SECTOR", "SECTORAL", "SECTORAL_SELECTOR":
			m["SECTORAL"] = NewSectoralSelector(cfg, db)
		case "EQUITY_VOLUME_GAINERS", "EVG":
			m["EQUITY_VOLUME_GAINERS"] = NewEquityVolumeGainersSelector()
		case "PDH_PDL", "ATH_ATL", "52WH_52WL":
			m[norm] = NewSecuritiesFOSelector()
		}
	}
	return m
}

// NormalizeSelectorName maps various user/UI aliases to canonical selector names
func NormalizeSelectorName(name string) string {
	upper := strings.ToUpper(strings.TrimSpace(name))
	switch upper {
	case "PDH-PDL", "PDH_PDL", "PDH":
		return "PDH_PDL"
	case "ATH-ATL", "ATH_ATL", "ATH":
		return "ATH_ATL"
	case "52WH-52WL", "52WH_52WL", "52WH", "52W":
		return "52WH_52WL"
	case "NEWS":
		return "NEWS"
	case "HIGH_IMPACT_NEWS", "HIGH IMPACT NEWS", "HIN":
		return "HIGH_IMPACT_NEWS"
	case "RESULT", "EARNINGS":
		return "RESULT"
	case "FO", "SECURITIES_FO", "F&O":
		return "FO"
	case "SECTOR", "SECTORAL", "SEC":
		return "SECTOR"
	case "QUANT_SCANNER", "QUANT", "QUANT SCANNER":
		return "QUANT_SCANNER"
	case "PT_SCREENER", "PT-SCREENER", "PTSCREENER", "PTS":
		return "PT_SCREENER"
	case "PT_ADVANCE", "PT-ADVANCE", "PTADVANCE", "PTA":
		return "PT_ADVANCE"
	case "OTHERS", "OTHER", "MISC", "OTH":
		return "OTHERS"
	case "MANUAL", "MA", "M":
		return "MANUAL"
	default:
		return upper
	}
}

// ResolveWinningSelector chooses the winning selection strategy based on highest priority rank (lowest numerical rank)
func ResolveWinningSelector(symbol string, candidateSelectors []string, configs map[string]StockSelectionStrategyConfig) (string, float64) {
	if len(candidateSelectors) == 0 {
		return "DEFAULT", 0.0
	}

	type RankedSelector struct {
		Name          string
		PriorityRank  int
		LevelShiftPct float64
	}

	var candidates []RankedSelector
	for _, rawSel := range candidateSelectors {
		selName := NormalizeSelectorName(rawSel)
		cfg, exists := configs[selName]
		if !exists {
			cfg = StockSelectionStrategyConfig{
				Name:          selName,
				Enabled:       true,
				PriorityRank:  99,
				LevelShiftPct: 0.0,
			}
		}
		if cfg.Enabled {
			candidates = append(candidates, RankedSelector{
				Name:          selName,
				PriorityRank:  cfg.PriorityRank,
				LevelShiftPct: cfg.LevelShiftPct,
			})
		}
	}

	if len(candidates) == 0 {
		return "DEFAULT", 0.0
	}

	// Sort ascending by PriorityRank (Rank 1 comes before Rank 2)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].PriorityRank < candidates[j].PriorityRank
	})

	return candidates[0].Name, candidates[0].LevelShiftPct
}

// CalculateLevelShiftedPrice shifts a reference price boundary (e.g. PDH or PDL) by levelShiftPct
func CalculateLevelShiftedPrice(refPrice float64, shiftPct float64, tickSize float64) float64 {
	if refPrice <= 0 {
		return 0
	}
	if tickSize <= 0 {
		tickSize = 0.05
	}
	shifted := refPrice * (1.0 + (shiftPct / 100.0))
	return risk.RoundTick(shifted, tickSize)
}
