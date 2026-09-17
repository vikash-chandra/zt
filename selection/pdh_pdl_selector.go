package selection

import (
	"context"
	"math"
	"sort"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

// PDHPDLSelector selects active F&O stocks based on proximity to or breakout of Previous Day High / Low
type PDHPDLSelector struct {
	db *data.Database
}

// NewPDHPDLSelector creates a new PDHPDLSelector instance
func NewPDHPDLSelector(db *data.Database) *PDHPDLSelector {
	return &PDHPDLSelector{db: db}
}

// Name returns selector identity name
func (s *PDHPDLSelector) Name() string {
	return "PDH_PDL"
}

// SelectStocks runs the PDH/PDL proximity and breakout algorithm on active F&O counters
func (s *PDHPDLSelector) SelectStocks(ctx context.Context, logger *zap.Logger, client data.BrokerClient, secMaster *data.SecurityMaster, bias string, size int, maxPctChange float64) (map[string]int64, error) {
	if bias == "NO_TRADE" {
		logger.Info("Global bias is NO_TRADE. Skipping PDH_PDL selection.", zap.String("bias", bias))
		return make(map[string]int64), nil
	}

	if size <= 0 {
		size = 10
	}
	if maxPctChange <= 0 {
		maxPctChange = 100.0
	}

	results := make(map[string]int64)



	if secMaster == nil {
		return results, nil
	}

	// 2. Fetch active F&O stocks universe
	foStocksMap, err := secMaster.GetFOStocks(ctx)
	if err != nil || len(foStocksMap) == 0 {
		foStocksMap, _ = secMaster.GetNifty50Constituents(ctx)
	}
	if len(foStocksMap) == 0 {
		return results, nil
	}

	if client == nil {
		// Fallback without live broker client: populate from universe up to size
		for sym, tok := range foStocksMap {
			results[sym] = tok
			if len(results) >= size {
				break
			}
		}
		return results, nil
	}

	// 3. Fetch real daily candles for PDH/PDL calculation from database
	var dailyCandlesMap map[int64][]data.Candle
	if s.db != nil {
		dailyCandlesMap, _ = s.db.GetAllRecentDailyCandlesMap(ctx, 5)
	}
	nowIST := data.NowIST()
	todayStart := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 0, 0, 0, 0, data.ISTLocation)

	var keys []string
	for symbol := range foStocksMap {
		keys = append(keys, "NSE:"+symbol)
	}

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
			logger.Warn("Failed to fetch batch OHLC for PDH_PDL stocks", zap.Error(err))
			break
		}
		for k, v := range batchData {
			ohlcData[k] = v
		}
	}

	type Candidate struct {
		Symbol string
		Token  int64
		Score  float64
	}
	var candidates []Candidate

	for key, entry := range ohlcData {
		symbol := key[4:]
		token := foStocksMap[symbol]
		if token <= 0 {
			continue
		}

		ltp := entry.LastPrice
		open := entry.OHLC.Open
		if ltp <= 0 {
			continue
		}

		// Resolve true Previous Day High and Previous Day Low
		pdh := 0.0
		pdl := 0.0
		if dailyCandlesMap != nil {
			if cList, exists := dailyCandlesMap[token]; exists {
				for i := len(cList) - 1; i >= 0; i-- {
					c := cList[i]
					if c.Time.Before(todayStart) && c.High > 0 && c.Low > 0 {
						pdh = c.High
						pdl = c.Low
						break
					}
				}
			}
		}

		if pdh <= 0 || pdl <= 0 {
			// Cannot verify PDH/PDL breakout without historical previous day levels
			continue
		}

		refPrice := open
		if refPrice == 0 {
			refPrice = entry.OHLC.Close
		}
		if refPrice > 0 && maxPctChange > 0 {
			pctChange := math.Abs((ltp-refPrice)/refPrice) * 100.0
			if pctChange > maxPctChange {
				continue
			}
		}

		var score float64
		if bias == "BUY_ONLY" || bias == "BULLISH" {
			if ltp >= pdh {
				// Already broken out above real PDH: highest score (1000 + breakout %)
				score = 1000.0 + ((ltp - pdh) / pdh * 100.0)
			} else {
				// Proximity to real PDH: closer is higher (100 - dist %)
				distPct := ((pdh - ltp) / pdh) * 100.0
				score = 100.0 - distPct
			}
		} else if bias == "SELL_ONLY" || bias == "BEARISH" {
			if ltp <= pdl {
				// Already broken down below real PDL: highest score (1000 + breakdown %)
				score = 1000.0 + ((pdl - ltp) / pdl * 100.0)
			} else {
				// Proximity to real PDL: closer is higher (100 - dist %)
				distPct := ((ltp - pdl) / pdl) * 100.0
				score = 100.0 - distPct
			}
		} else {
			// Neutral / ANY bias: breakouts above PDH or below PDL take top priority
			if ltp >= pdh {
				score = 1000.0 + ((ltp - pdh) / pdh * 100.0)
			} else if ltp <= pdl {
				score = 1000.0 + ((pdl - ltp) / pdl * 100.0)
			} else {
				distHigh := ((pdh - ltp) / pdh) * 100.0
				distLow := ((ltp - pdl) / pdl) * 100.0
				minDist := math.Min(distHigh, distLow)
				score = 100.0 - minDist
			}
		}

		candidates = append(candidates, Candidate{
			Symbol: symbol,
			Token:  token,
			Score:  score,
		})
	}

	// Sort candidates descending by score
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	for _, c := range candidates {
		results[c.Symbol] = c.Token
		if len(results) >= size {
			break
		}
	}

	return results, nil
}
