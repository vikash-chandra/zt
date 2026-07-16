package selection

import (
	"context"

	"zerodha-trading/config"
	"zerodha-trading/data"

	"go.uber.org/zap"
)

// Selector defines the interface for dynamic watchlist selection algorithms
type Selector interface {
	Name() string
	SelectStocks(ctx context.Context, logger *zap.Logger, client data.BrokerClient, secMaster *data.SecurityMaster, bias string, size int, maxPctChange float64) (map[string]int64, error)
}

// InitializeSelectors instantiates and maps active selectors by name
func InitializeSelectors(names []string, cfg *config.Settings, db *data.Database) map[string]Selector {
	m := make(map[string]Selector)
	for _, name := range names {
		switch name {
		case "SECURITIES_FO":
			m["SECURITIES_FO"] = NewSecuritiesFOSelector()
		case "SECTORAL":
			m["SECTORAL"] = NewSectoralSelector(cfg, db)
		case "EQUITY_VOLUME_GAINERS":
			m["EQUITY_VOLUME_GAINERS"] = NewEquityVolumeGainersSelector()
		}
	}
	return m
}
