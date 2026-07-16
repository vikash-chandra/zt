package selection

import (
	"context"
	"fmt"
	"math"
	"sort"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// SecuritiesFOSelector selects active F&O stocks based on percentage price moves
type SecuritiesFOSelector struct{}

// NewSecuritiesFOSelector creates a new SecuritiesFOSelector instance
func NewSecuritiesFOSelector() *SecuritiesFOSelector {
	return &SecuritiesFOSelector{}
}

// Name returns selector identity name
func (s *SecuritiesFOSelector) Name() string {
	return "SECURITIES_FO"
}

// SelectStocks runs the OHLC filtering algorithm on active F&O counters
func (s *SecuritiesFOSelector) SelectStocks(ctx context.Context, logger *zap.Logger, client data.BrokerClient, secMaster *data.SecurityMaster, bias string, size int, maxPctChange float64) (map[string]int64, error) {
	if bias == "NO_TRADE" || bias == "" {
		logger.Info("Global bias is NO_TRADE or empty. Skipping Securities F&O selection.", zap.String("bias", bias))
		return make(map[string]int64), nil
	}

	foStocksMap, err := secMaster.GetFOStocks(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch active F&O stocks list: %w", err)
	}

	var keys []string
	for symbol := range foStocksMap {
		keys = append(keys, "NSE:"+symbol)
	}

	logger.Info("Fetching OHLC snapshots for F&O stock selection...", zap.Int("count", len(keys)))

	ohlcData := make(data.QuoteOHLC)
	batchSize := 400
	for i := 0; i < len(keys); i += batchSize {
		end := i + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		batchKeys := keys[i:end]
		batchData, err := client.GetOHLC(batchKeys...)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch batch OHLC for F&O stocks: %w", err)
		}
		for k, v := range batchData {
			ohlcData[k] = v
		}
	}

	type StockPerf struct {
		Symbol    string
		Token     int64
		PctChange float64
	}
	var performances []StockPerf

	for key, entry := range ohlcData {
		open := entry.OHLC.Open
		ltp := entry.LastPrice
		symbol := key[4:] // remove "NSE:"

		if open == 0 {
			continue
		}

		pctChange := ((ltp - open) / open) * 100.0
		if math.Abs(pctChange) > maxPctChange {
			continue
		}
		token := foStocksMap[symbol]

		performances = append(performances, StockPerf{
			Symbol:    symbol,
			Token:     token,
			PctChange: pctChange,
		})
	}

	if bias == "BUY_ONLY" {
		sort.Slice(performances, func(i, j int) bool {
			return performances[i].PctChange > performances[j].PctChange
		})
	} else if bias == "SELL_ONLY" {
		sort.Slice(performances, func(i, j int) bool {
			return performances[i].PctChange < performances[j].PctChange
		})
	}

	topCount := size
	if len(performances) < topCount {
		topCount = len(performances)
	}

	selectedWatchlist := make(map[string]int64)
	for i := 0; i < topCount; i++ {
		selectedWatchlist[performances[i].Symbol] = performances[i].Token
		logger.Info("Securities F&O stock selected",
			zap.Int("rank", i+1),
			zap.String("symbol", performances[i].Symbol),
			zap.Float64("pct_change", performances[i].PctChange),
			zap.Int64("token", performances[i].Token),
		)
	}

	return selectedWatchlist, nil
}
