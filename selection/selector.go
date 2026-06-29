package selection

import (
	"context"

	"zerodha-trading/data"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"go.uber.org/zap"
)

// Selector defines the interface for dynamic watchlist selection algorithms
type Selector interface {
	Name() string
	SelectStocks(ctx context.Context, logger *zap.Logger, client *kiteconnect.Client, secMaster *data.SecurityMaster, bias string, size int, maxPctChange float64) (map[string]int64, error)
}

// InitializeSelectors instantiates and maps active selectors by name
func InitializeSelectors(names []string) map[string]Selector {
	m := make(map[string]Selector)
	for _, name := range names {
		switch name {
		case "SECURITIES_FO":
			m["SECURITIES_FO"] = NewSecuritiesFOSelector()
		}
	}
	return m
}
