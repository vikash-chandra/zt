package scanner

import (
	"testing"
)

func TestLiveNewsAggregator(t *testing.T) {
	agg := NewNewsAggregator()

	symbols := []string{"TATAMOTORS", "RELIANCE", "INFY"}

	for _, sym := range symbols {
		t.Run(sym, func(t *testing.T) {
			items, summary, sentiment := agg.FetchNewsForStock(sym)

			t.Logf("Symbol: %s | Sentiment: %s", sym, sentiment)
			t.Logf("Summary: %s", summary)

			if len(items) > 0 {
				t.Logf("Fetched %d news headlines for %s", len(items), sym)
				for i, item := range items {
					t.Logf("  [%d] %s (%s) - %s", i+1, item.Title, item.Source, item.Sentiment)
				}
			} else {
				t.Logf("No live news returned for %s (using fallback default)", sym)
			}
		})
	}
}
