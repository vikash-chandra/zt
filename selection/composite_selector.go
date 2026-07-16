package selection

import (
	"context"
	"strings"
	"zerodha-trading/data"

	"go.uber.org/zap"
)

// CompositeSelector merges results from multiple sub-selectors
type CompositeSelector struct {
	selectors []Selector
	name      string
}

// NewCompositeSelector creates a new CompositeSelector
func NewCompositeSelector(selectors []Selector) *CompositeSelector {
	var names []string
	for _, s := range selectors {
		names = append(names, s.Name())
	}
	return &CompositeSelector{
		selectors: selectors,
		name:      strings.Join(names, "+"),
	}
}

// Name returns the joined name of all sub-selectors
func (cs *CompositeSelector) Name() string {
	return cs.name
}

// SelectStocks runs all sub-selectors and merges their results, up to the requested size
func (cs *CompositeSelector) SelectStocks(ctx context.Context, logger *zap.Logger, client data.BrokerClient, secMaster *data.SecurityMaster, bias string, size int, maxPctChange float64) (map[string]int64, error) {
	merged := make(map[string]int64)
	for _, selector := range cs.selectors {
		stocks, err := selector.SelectStocks(ctx, logger, client, secMaster, bias, size, maxPctChange)
		if err != nil {
			logger.Error("Composite sub-selector failed", zap.String("selector", selector.Name()), zap.Error(err))
			continue
		}
		for k, v := range stocks {
			merged[k] = v
		}
	}

	// Truncate to size if merged exceeds size
	if len(merged) > size {
		truncated := make(map[string]int64)
		count := 0
		for k, v := range merged {
			truncated[k] = v
			count++
			if count >= size {
				break
			}
		}
		return truncated, nil
	}

	return merged, nil
}
