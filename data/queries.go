package data

import (
	"context"
	"fmt"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
)

// PreSelectionResult mirrors the prediction matrix structure saved in DB
type PreSelectionResult struct {
	Date               string  `json:"date"`
	Ticker             string  `json:"ticker"`
	RuleSet            string  `json:"rule_set"`
	PredictedDirection string  `json:"predicted_direction"`
	ImbalanceRatio     float64 `json:"imbalance_ratio"`
	IndicativeGapPct   float64 `json:"indicative_gap_pct"`
	PreOpenVolVsADV    float64 `json:"pre_open_vol_vs_adv"`
	ProbabilityScore   float64 `json:"probability_score"`
	Reason             string  `json:"reason"`
}

// PersistOrder inserts a new order trace into the database
func (d *Database) PersistOrder(orderID string, symbol string, exchange string, quantity int, transactionType string, orderType string, product string, status string) error {
	query := `
		INSERT INTO orders (order_id, symbol, exchange, quantity, transaction_type, order_type, product, placed_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := d.conn.Exec(query, orderID, symbol, exchange, quantity, transactionType, orderType, product, time.Now(), status)
	return err
}

// UpdateOrderStatus updates an existing order's status in the database
func (d *Database) UpdateOrderStatus(orderID, status string) error {
	query := `UPDATE orders SET status = $1, updated_at = $2 WHERE order_id = $3`
	_, err := d.conn.Exec(query, status, time.Now(), orderID)
	return err
}

// GetLatestPreSelectionDate returns the latest date containing pre-selection results
func (d *Database) GetLatestPreSelectionDate() (string, error) {
	var dateStr string
	err := d.conn.QueryRow("SELECT MAX(date)::TEXT FROM pre_selection_results").Scan(&dateStr)
	return dateStr, err
}

// GetPreSelectionResults retrieves prediction records for a specific date and rule set
func (d *Database) GetPreSelectionResults(dateStr string, ruleSet string) ([]PreSelectionResult, error) {
	query := `
		SELECT date::TEXT, ticker, rule_set, predicted_direction, imbalance_ratio, indicative_gap_pct, pre_open_vol_vs_adv, probability_score, reason
		FROM pre_selection_results
		WHERE date = $1 AND rule_set = $2
		ORDER BY probability_score DESC
	`
	rows, err := d.conn.Query(query, dateStr, ruleSet)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PreSelectionResult
	for rows.Next() {
		var r PreSelectionResult
		err := rows.Scan(
			&r.Date,
			&r.Ticker,
			&r.RuleSet,
			&r.PredictedDirection,
			&r.ImbalanceRatio,
			&r.IndicativeGapPct,
			&r.PreOpenVolVsADV,
			&r.ProbabilityScore,
			&r.Reason,
		)
		if err == nil {
			results = append(results, r)
		}
	}
	return results, nil
}

// SavePreSelectionResults upserts batch predictions into pre_selection_results
func (d *Database) SavePreSelectionResults(results []PreSelectionResult) error {
	ctx := context.Background()
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO pre_selection_results (
			date, ticker, rule_set, predicted_direction, imbalance_ratio, indicative_gap_pct, pre_open_vol_vs_adv, probability_score, reason
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (date, ticker, rule_set) DO UPDATE SET
			predicted_direction = EXCLUDED.predicted_direction,
			imbalance_ratio = EXCLUDED.imbalance_ratio,
			indicative_gap_pct = EXCLUDED.indicative_gap_pct,
			pre_open_vol_vs_adv = EXCLUDED.pre_open_vol_vs_adv,
			probability_score = EXCLUDED.probability_score,
			reason = EXCLUDED.reason,
			created_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, pred := range results {
		_, err = stmt.Exec(
			pred.Date,
			pred.Ticker,
			pred.RuleSet,
			pred.PredictedDirection,
			pred.ImbalanceRatio,
			pred.IndicativeGapPct,
			pred.PreOpenVolVsADV,
			pred.ProbabilityScore,
			pred.Reason,
		)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

// GetHistoricalAggregatedCandles aggregates and retrieves past 5m EOD candles from DB
func (d *Database) GetHistoricalAggregatedCandles(token int64) ([]kiteconnect.HistoricalData, error) {
	query := `
		SELECT time, open, high, low, close, volume
		FROM candles_5m
		WHERE token = $1
		ORDER BY time ASC
	`
	rows, err := d.conn.Query(query, token)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	loc, lErr := time.LoadLocation("Asia/Kolkata")
	if lErr != nil {
		loc = time.Local
	}

	dailyAgg := make(map[string]*kiteconnect.HistoricalData)
	var dates []string

	for rows.Next() {
		var t time.Time
		var o, h, l, c float64
		var v int
		if err := rows.Scan(&t, &o, &h, &l, &c, &v); err != nil {
			continue
		}
		dateStr := t.In(loc).Format("2006-01-02")
		dayData, exists := dailyAgg[dateStr]
		if !exists {
			dayData = &kiteconnect.HistoricalData{
				Open:   o,
				High:   h,
				Low:    l,
				Close:  c,
				Volume: v,
			}
			dayData.Date.Time = t
			dailyAgg[dateStr] = dayData
			dates = append(dates, dateStr)
		} else {
			if h > dayData.High {
				dayData.High = h
			}
			if l < dayData.Low {
				dayData.Low = l
			}
			dayData.Close = c
			dayData.Volume += v
		}
	}

	var candles []kiteconnect.HistoricalData
	if len(dates) >= 5 {
		// Sort the dates
		for i := 0; i < len(dates); i++ {
			for j := i + 1; j < len(dates); j++ {
				if dates[i] > dates[j] {
					dates[i], dates[j] = dates[j], dates[i]
				}
			}
		}
		for _, dKey := range dates {
			candles = append(candles, *dailyAgg[dKey])
		}
	}
	return candles, nil
}

// InsertCandle saves a generated candle to a specific time-series table
func (d *Database) InsertCandle(tableName string, token int64, t time.Time, o, h, l, c float64, v int64, vwap, bid, ask float64, tickCount int, color string) error {
	if tableName != "candles_1m" && tableName != "candles_5m" {
		return fmt.Errorf("invalid candle table name: %s", tableName)
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (token, time) DO UPDATE SET
			close = EXCLUDED.close,
			high = EXCLUDED.high,
			low = EXCLUDED.low,
			volume = EXCLUDED.volume,
			vwap = EXCLUDED.vwap,
			bid = EXCLUDED.bid,
			ask = EXCLUDED.ask,
			tick_count = EXCLUDED.tick_count,
			color = EXCLUDED.color
	`, tableName)

	_, err := d.conn.Exec(query, token, t, o, h, l, c, v, vwap, bid, ask, tickCount, color)
	return err
}

// GetLastNCandles retrieves the last N candles chronologically from the database
func (d *Database) GetLastNCandles(tableName string, token int64, n int) ([]Candle, error) {
	if tableName != "candles_1m" && tableName != "candles_5m" {
		return nil, fmt.Errorf("invalid candle table name: %s", tableName)
	}

	query := fmt.Sprintf(`
		SELECT token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color
		FROM %s
		WHERE token = $1
		ORDER BY time DESC
		LIMIT $2
	`, tableName)

	rows, err := d.conn.Query(query, token, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candles := make([]Candle, 0, n)
	for rows.Next() {
		var c Candle
		if err := rows.Scan(&c.Token, &c.Time, &c.Open, &c.High, &c.Low, &c.Close,
			&c.Volume, &c.VWAP, &c.Bid, &c.Ask, &c.TickCount, &c.Color); err != nil {
			return nil, err
		}
		candles = append(candles, c)
	}

	// Reverse to chronological order
	for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
		candles[i], candles[j] = candles[j], candles[i]
	}

	return candles, nil
}

// GetMetadataCache returns cached json metadata value if not expired
func (d *Database) GetMetadataCache(ctx context.Context, key string, minUpdatedAt time.Time) (string, error) {
	var val string
	err := d.conn.QueryRowContext(ctx, "SELECT value FROM metadata_cache WHERE key = $1 AND updated_at > $2", key, minUpdatedAt).Scan(&val)
	return val, err
}

// SaveMetadataCache updates or inserts key-value metadata cache
func (d *Database) SaveMetadataCache(ctx context.Context, key string, value string) error {
	query := `
		INSERT INTO metadata_cache (key, value, updated_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP
	`
	_, err := d.conn.ExecContext(ctx, query, key, value)
	return err
}

// DeleteMetadataCache deletes key-value metadata pairs from PostgreSQL cache
func (d *Database) DeleteMetadataCache(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	// Direct single-query execution since it is localized
	query := "DELETE FROM metadata_cache WHERE key = ANY($1)"
	_, err := d.conn.ExecContext(ctx, query, keys)
	return err
}

// QuerySymbolToken retrieves cached token mapping inside 'fo:stocks' jsonb field
func (d *Database) QuerySymbolToken(ctx context.Context, symbol string) (int64, error) {
	var token int64
	err := d.conn.QueryRowContext(ctx, "SELECT (value::jsonb->$1)::bigint FROM metadata_cache WHERE key = 'fo:stocks'", symbol).Scan(&token)
	return token, err
}

// QueryRowSymbolToken queries cached token mapping without context
func (d *Database) QueryRowSymbolToken(symbol string) (int64, error) {
	var token int64
	err := d.conn.QueryRow("SELECT (value::jsonb->>$1)::bigint FROM metadata_cache WHERE key = 'fo:stocks'", symbol).Scan(&token)
	return token, err
}

// GetEquityVolumeGainersTickers retrieves selected tickers from pre_selection_results for a given date
func (d *Database) GetEquityVolumeGainersTickers(ctx context.Context, dateStr string) ([]string, error) {
	rows, err := d.conn.QueryContext(ctx, `
		SELECT ticker 
		FROM pre_selection_results 
		WHERE date = $1 AND predicted_direction != 'NEUTRAL'
		ORDER BY probability_score DESC
	`, dateStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tickers []string
	for rows.Next() {
		var ticker string
		if err := rows.Scan(&ticker); err == nil {
			tickers = append(tickers, ticker)
		}
	}
	return tickers, nil
}
