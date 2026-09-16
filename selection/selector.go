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

// Canonical Stock Selection Strategy Constants
const (
	SelectorFO             = "FO"
	SelectorSector         = "SECTOR"
	SelectorPDHPDL         = "PDH_PDL"
	Selector52WH52WL       = "52WH_52WL"
	SelectorATHATL         = "ATH_ATL"
	SelectorQuantScanner   = "QUANT_SCANNER"
	SelectorNews           = "NEWS"
	SelectorHighImpactNews = "HIGH_IMPACT_NEWS"
	SelectorResult         = "RESULT"
	SelectorPTScreener     = "PT_SCREENER"
	SelectorPTAdvance      = "PT_ADVANCE"
	SelectorOthers         = "OTHERS"
	SelectorManual         = "MANUAL"
)

// AllSelectorMethods returns all 12 supported stock selection methods
var AllSelectorMethods = []string{
	SelectorFO,
	SelectorSector,
	SelectorPDHPDL,
	Selector52WH52WL,
	SelectorATHATL,
	SelectorQuantScanner,
	SelectorNews,
	SelectorHighImpactNews,
	SelectorResult,
	SelectorPTScreener,
	SelectorPTAdvance,
	SelectorOthers,
}

// GetSelectorInstance instantiates a concrete Selector for any of the supported stock selection strategies
func GetSelectorInstance(name string, cfg *config.Settings, db *data.Database, force bool) Selector {
	norm := NormalizeSelectorName(name)
	switch norm {
	case SelectorFO, "SECURITIES_FO":
		return NewSecuritiesFOSelector()
	case SelectorSector, "SECTORAL", "SECTORAL_SELECTOR":
		secSel := NewSectoralSelector(cfg, db)
		secSel.Force = force
		return secSel
	case "EQUITY_VOLUME_GAINERS", "EVG":
		return NewEquityVolumeGainersSelector()
	case SelectorPDHPDL:
		return NewPDHPDLSelector(db)
	case SelectorATHATL:
		return NewHighLowBreakoutSelector(SelectorATHATL, db)
	case Selector52WH52WL:
		return NewHighLowBreakoutSelector(Selector52WH52WL, db)
	case SelectorQuantScanner:
		return NewQuantScannerSelector(db)
	case SelectorNews, SelectorHighImpactNews, SelectorResult, SelectorPTScreener, SelectorPTAdvance, SelectorOthers, SelectorManual:
		return NewDatabaseProvenanceSelector(norm, db)
	default:
		return nil
	}
}

// InitializeSelectors instantiates and maps active selectors by name
func InitializeSelectors(names []string, cfg *config.Settings, db *data.Database) map[string]Selector {
	m := make(map[string]Selector)
	for _, name := range names {
		norm := NormalizeSelectorName(name)
		sel := GetSelectorInstance(norm, cfg, db, false)
		if sel != nil {
			if norm == SelectorFO && (name == "SECURITIES_FO" || strings.EqualFold(name, "SECURITIES_FO")) {
				m["SECURITIES_FO"] = sel
			} else if norm == SelectorSector && (name == "SECTORAL" || strings.EqualFold(name, "SECTORAL")) {
				m["SECTORAL"] = sel
			} else if norm == "EQUITY_VOLUME_GAINERS" && (name == "EQUITY_VOLUME_GAINERS" || strings.EqualFold(name, "EQUITY_VOLUME_GAINERS")) {
				m["EQUITY_VOLUME_GAINERS"] = sel
			} else {
				m[norm] = sel
			}
		}
	}
	return m
}

// NormalizeSelectorName maps various user/UI aliases to canonical selector names using explicit case statements
func NormalizeSelectorName(name string) string {
	clean := strings.ToUpper(strings.TrimSpace(name))
	if strings.HasPrefix(clean, "MANUAL:") {
		clean = strings.TrimPrefix(clean, "MANUAL:")
	} else if strings.HasPrefix(clean, "PROV:") {
		clean = strings.TrimPrefix(clean, "PROV:")
	}

	switch clean {
	case "FO", "SECURITIES_FO", "F&O", "FNO":
		return SelectorFO
	case "SECTOR", "SECTORAL", "SEC", "SECTOR_ALLOCATION":
		return SelectorSector
	case "PDH_PDL", "PDH-PDL", "PDH", "PDL":
		return SelectorPDHPDL
	case "52WH_52WL", "52WH-52WL", "52WH", "52WL", "52W":
		return Selector52WH52WL
	case "ATH_ATL", "ATH-ATL", "ATH", "ATL":
		return SelectorATHATL
	case "QUANT_SCANNER", "QUANT", "QUANT SCANNER":
		return SelectorQuantScanner
	case "NEWS", "NEWS_MOMENTUM":
		return SelectorNews
	case "HIGH_IMPACT_NEWS", "HIGH IMPACT NEWS", "HIN":
		return SelectorHighImpactNews
	case "RESULT", "RESULTS", "EARNINGS":
		return SelectorResult
	case "PT_SCREENER", "PT-SCREENER", "PTSCREENER", "PTS":
		return SelectorPTScreener
	case "PT_ADVANCE", "PT-ADVANCE", "PTADVANCE", "PTA":
		return SelectorPTAdvance
	case "OTHERS", "OTHER", "MISC", "OTH":
		return SelectorOthers
	case "MANUAL", "MA", "M":
		return SelectorManual
	default:
		return clean
	}
}

// ValidateSelectorMethod checks if a string is a valid stock selection strategy, returning canonical name and boolean
func ValidateSelectorMethod(name string) (string, bool) {
	norm := NormalizeSelectorName(name)
	switch norm {
	case SelectorFO, SelectorSector, SelectorPDHPDL, Selector52WH52WL, SelectorATHATL,
		SelectorQuantScanner, SelectorNews, SelectorHighImpactNews, SelectorResult,
		SelectorPTScreener, SelectorPTAdvance, SelectorOthers:
		return norm, true
	case SelectorManual:
		return SelectorManual, true
	default:
		return "", false
	}
}

// FormatSelectorBadge formats a selector name into a clean concise UI badge string
func FormatSelectorBadge(name string) string {
	norm := NormalizeSelectorName(name)
	switch norm {
	case SelectorFO:
		return "FO"
	case SelectorSector:
		return "SEC"
	case SelectorPDHPDL:
		return "PDH_PDL"
	case Selector52WH52WL:
		return "52W"
	case SelectorATHATL:
		return "ATH"
	case SelectorQuantScanner:
		return "QUANT"
	case SelectorNews:
		return "NEWS"
	case SelectorHighImpactNews:
		return "HIN"
	case SelectorResult:
		return "RESULT"
	case SelectorPTScreener:
		return "PTS"
	case SelectorPTAdvance:
		return "PTA"
	case SelectorOthers:
		return "OTH"
	case SelectorManual:
		return "MANUAL"
	default:
		return norm
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
