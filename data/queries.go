package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
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

// UpdateOrderStatus updates an existing order's status, average price, and filled quantity in the database
func (d *Database) UpdateOrderStatus(orderID, status string, averagePrice float64, filledQuantity int) error {
	query := `UPDATE orders SET status = $1, average_price = $2, filled_quantity = $3, updated_at = $4 WHERE order_id = $5`
	_, err := d.conn.Exec(query, status, averagePrice, filledQuantity, time.Now(), orderID)
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
func (d *Database) GetHistoricalAggregatedCandles(token int64) ([]HistoricalData, error) {
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

	loc := ISTLocation

	dailyAgg := make(map[string]*HistoricalData)
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
			dayData = &HistoricalData{
				Open:   o,
				High:   h,
				Low:    l,
				Close:  c,
				Volume: int64(v),
				Date:   t,
			}
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
			dayData.Volume += int64(v)
		}
	}

	var candles []HistoricalData
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
	if d == nil || d.conn == nil {
		return nil // Safe fallback for testing/dry-runs when DB is not running
	}

	normalizedTime := NormalizeToIST(t)
	if !IsMarketHoursCandle(normalizedTime) {
		return nil
	}

	if tableName != "candles_1m" && tableName != "candles_5m" {
		return fmt.Errorf("invalid candle table name: %s", tableName)
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (token, time) DO UPDATE SET
			open = EXCLUDED.open,
			high = EXCLUDED.high,
			low = EXCLUDED.low,
			close = EXCLUDED.close,
			volume = EXCLUDED.volume,
			vwap = EXCLUDED.vwap,
			bid = EXCLUDED.bid,
			ask = EXCLUDED.ask,
			tick_count = EXCLUDED.tick_count,
			color = EXCLUDED.color
	`, tableName)

	_, err := d.conn.Exec(query, token, normalizedTime, o, h, l, c, v, vwap, bid, ask, tickCount, color)
	return err
}

// GetLastNCandles retrieves the last N candles chronologically from the database with strict IST normalization
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
		c.Time = NormalizeToIST(c.Time)
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

// SaveOpenPosition upserts an open position tracking record into positions table
func (d *Database) SaveOpenPosition(ctx context.Context, orderID string, symbol string, qty int, entryPrice float64, side string, slPrice float64, strategy string, brokerSLOrderID string) error {
	query := `
		INSERT INTO positions (order_id, symbol, quantity, entry_price, side, sl_price, strategy, broker_sl_order_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		ON CONFLICT (order_id) DO UPDATE SET
			quantity = EXCLUDED.quantity,
			entry_price = EXCLUDED.entry_price,
			sl_price = EXCLUDED.sl_price,
			broker_sl_order_id = EXCLUDED.broker_sl_order_id,
			closed_at = NULL
	`
	_, err := d.conn.ExecContext(ctx, query, orderID, symbol, qty, entryPrice, side, slPrice, strategy, brokerSLOrderID)
	return err
}

// UpdateBrokerSLOrderID updates the broker SL order ID for an open position
func (d *Database) UpdateBrokerSLOrderID(ctx context.Context, orderID string, brokerSLOrderID string) error {
	query := `
		UPDATE positions
		SET broker_sl_order_id = $2
		WHERE order_id = $1
	`
	_, err := d.conn.ExecContext(ctx, query, orderID, brokerSLOrderID)
	return err
}

// CloseOpenPosition marks an open position as closed
func (d *Database) CloseOpenPosition(ctx context.Context, orderID string, exitPrice float64) error {
	query := `
		UPDATE positions
		SET closed_at = NOW(), current_price = $2
		WHERE order_id = $1 AND closed_at IS NULL
	`
	_, err := d.conn.ExecContext(ctx, query, orderID, exitPrice)
	return err
}

// SelectedSectorRecord holds details of a selected sector
type SelectedSectorRecord struct {
	Sector     string    `json:"sector"`
	PctChange  float64   `json:"pct_change"`
	SelectedAt time.Time `json:"selected_at"`
}

// SaveSelectedSector saves a selected sector's performance for a given date
func (d *Database) SaveSelectedSector(ctx context.Context, dateStr string, sector string, pctChange float64, selectedAt time.Time) error {
	if d == nil || d.conn == nil {
		return nil
	}
	query := `
		INSERT INTO selected_sectors (date, sector, pct_change, selected_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (date, sector) DO UPDATE SET
			pct_change = EXCLUDED.pct_change,
			selected_at = EXCLUDED.selected_at
	`
	_, err := d.conn.ExecContext(ctx, query, dateStr, sector, pctChange, selectedAt)
	return err
}

// GetSelectedSectors retrieves all selected sectors for a given date
func (d *Database) GetSelectedSectors(ctx context.Context, dateStr string) ([]SelectedSectorRecord, error) {
	if d == nil || d.conn == nil {
		return nil, nil
	}
	query := `
		SELECT sector, pct_change, selected_at 
		FROM selected_sectors 
		WHERE date = $1 
		ORDER BY ABS(pct_change) DESC
	`
	rows, err := d.conn.QueryContext(ctx, query, dateStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []SelectedSectorRecord
	for rows.Next() {
		var r SelectedSectorRecord
		if err := rows.Scan(&r.Sector, &r.PctChange, &r.SelectedAt); err != nil {
			continue
		}
		list = append(list, r)
	}
	return list, nil
}

// DailyWatchlistItem represents a stock stored in the daily selection watchlist
type DailyWatchlistItem struct {
	Date      string `json:"date"`
	Symbol    string `json:"symbol"`
	Token     int64  `json:"token"`
	Selectors string `json:"selectors"`
}

// SaveDailyWatchlist saves the daily selection watchlist to the database
func (d *Database) SaveDailyWatchlist(ctx context.Context, items []DailyWatchlistItem) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	query := `
		INSERT INTO daily_watchlists (date, symbol, token, selectors)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (date, symbol) DO UPDATE 
		SET token = EXCLUDED.token, selectors = EXCLUDED.selectors
	`
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, item := range items {
		_, err = stmt.ExecContext(ctx, item.Date, item.Symbol, item.Token, item.Selectors)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetDailyWatchlist retrieves the daily selection watchlist for a specific date
func (d *Database) GetDailyWatchlist(ctx context.Context, dateStr string) ([]DailyWatchlistItem, error) {
	query := `
		SELECT date::TEXT, symbol, token, selectors
		FROM daily_watchlists
		WHERE date = $1
		ORDER BY symbol ASC
	`
	rows, err := d.conn.QueryContext(ctx, query, dateStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []DailyWatchlistItem
	for rows.Next() {
		var item DailyWatchlistItem
		err := rows.Scan(&item.Date, &item.Symbol, &item.Token, &item.Selectors)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// GetAllFOStocks retrieves all F&O stocks mapped symbol to token from metadata cache
func (d *Database) GetAllFOStocks(ctx context.Context) (map[string]int64, error) {
	var val string
	err := d.conn.QueryRowContext(ctx, "SELECT value FROM metadata_cache WHERE key = 'fo:stocks'").Scan(&val)
	if err != nil {
		return nil, err
	}

	var stocks map[string]int64
	if err := json.Unmarshal([]byte(val), &stocks); err != nil {
		return nil, err
	}
	return stocks, nil
}

// OptionsBotState represents persistent state for the 5m Triple SuperTrend Options Bot
type OptionsBotState struct {
	ID               int       `json:"id"`
	Multiplier       int       `json:"multiplier"`
	LastTrend        string    `json:"last_trend"`
	SLStoppedTrend   string    `json:"sl_stopped_trend"`
	AwaitingReversal bool      `json:"awaiting_reversal"`
	ActiveOrderID    string    `json:"active_order_id"`
	ActiveSymbol     string    `json:"active_symbol"`
	ActiveSide       string    `json:"active_side"`
	ActiveQty        int       `json:"active_qty"`
	EntryPremium     float64   `json:"entry_premium"`
	SLPrice          float64   `json:"sl_price"`
	PaperBalance     float64   `json:"paper_balance"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SaveOptionsBotState upserts the single state row in options_bot_state table
func (d *Database) SaveOptionsBotState(ctx context.Context, state *OptionsBotState) error {
	query := `
		INSERT INTO options_bot_state (
			id, multiplier, last_trend, sl_stopped_trend, awaiting_reversal,
			active_order_id, active_symbol, active_side, active_qty,
			entry_premium, sl_price, paper_balance, updated_at
		) VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
		ON CONFLICT (id) DO UPDATE SET
			multiplier = EXCLUDED.multiplier,
			last_trend = EXCLUDED.last_trend,
			sl_stopped_trend = EXCLUDED.sl_stopped_trend,
			awaiting_reversal = EXCLUDED.awaiting_reversal,
			active_order_id = EXCLUDED.active_order_id,
			active_symbol = EXCLUDED.active_symbol,
			active_side = EXCLUDED.active_side,
			active_qty = EXCLUDED.active_qty,
			entry_premium = EXCLUDED.entry_premium,
			sl_price = EXCLUDED.sl_price,
			paper_balance = EXCLUDED.paper_balance,
			updated_at = NOW()
	`
	_, err := d.conn.ExecContext(ctx, query,
		state.Multiplier, state.LastTrend, state.SLStoppedTrend, state.AwaitingReversal,
		state.ActiveOrderID, state.ActiveSymbol, state.ActiveSide, state.ActiveQty,
		state.EntryPremium, state.SLPrice, state.PaperBalance,
	)
	return err
}

// GetOptionsBotState retrieves the options bot state row
func (d *Database) GetOptionsBotState(ctx context.Context) (*OptionsBotState, error) {
	query := `
		SELECT id, multiplier, last_trend, sl_stopped_trend, awaiting_reversal,
		       active_order_id, active_symbol, active_side, active_qty,
		       entry_premium, sl_price, paper_balance, updated_at
		FROM options_bot_state
		WHERE id = 1
	`
	var st OptionsBotState
	err := d.conn.QueryRowContext(ctx, query).Scan(
		&st.ID, &st.Multiplier, &st.LastTrend, &st.SLStoppedTrend, &st.AwaitingReversal,
		&st.ActiveOrderID, &st.ActiveSymbol, &st.ActiveSide, &st.ActiveQty,
		&st.EntryPremium, &st.SLPrice, &st.PaperBalance, &st.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return &OptionsBotState{
				ID:               1,
				Multiplier:       1,
				LastTrend:        "NEUTRAL",
				SLStoppedTrend:   "",
				AwaitingReversal: false,
				PaperBalance:     1000000.0,
			}, nil
		}
		return nil, err
	}
	return &st, nil
}

// DBScanResult matches scanner results for database storage
type DBScanResult struct {
	ID                int       `json:"id"`
	ScanDate          string    `json:"scan_date"`
	Symbol            string    `json:"symbol"`
	Segment           string    `json:"segment"`
	BreakoutType      string    `json:"breakout_type"`
	Direction         string    `json:"direction"`
	MomentumDays      int       `json:"momentum_days"`
	PctChange1D       float64   `json:"pct_change_1d"`
	PctChange3D       float64   `json:"pct_change_3d"`
	RangePctChange    float64   `json:"range_pct_change"`
	YearlyHigh        float64   `json:"yearly_high"`
	YearlyLow         float64   `json:"yearly_low"`
	AllTimeHigh       float64   `json:"all_time_high"`
	AllTimeLow        float64   `json:"all_time_low"`
	Volume1D          int64     `json:"volume_1d"`
	VolumeADV         int64     `json:"volume_adv"`
	VolumeMultiplier  float64   `json:"volume_multiplier"`
	ConfidenceScore   float64   `json:"confidence_score"`
	QuantDirection    string    `json:"quant_direction"`
	RecommendedAction string    `json:"recommended_action"`
	NewsSummary       string    `json:"news_summary"`
	NewsSentiment     string    `json:"news_sentiment"`
	CreatedAt         time.Time `json:"created_at"`
}

// SaveScannerResults saves/upserts scanner results to quant_scanner_results table
func (d *Database) SaveScannerResults(ctx context.Context, results []DBScanResult) error {
	if len(results) == 0 {
		return nil
	}
	query := `
		INSERT INTO quant_scanner_results (
			scan_date, symbol, segment, breakout_type, direction, momentum_days,
			pct_change_1d, pct_change_3d, range_pct_change, yearly_high, yearly_low, all_time_high, all_time_low,
			volume_1d, volume_adv, volume_multiplier,
			confidence_score, quant_direction, recommended_action, news_summary, news_sentiment, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
		ON CONFLICT (scan_date, symbol) DO UPDATE SET
			segment = EXCLUDED.segment,
			breakout_type = EXCLUDED.breakout_type,
			direction = EXCLUDED.direction,
			momentum_days = EXCLUDED.momentum_days,
			pct_change_1d = EXCLUDED.pct_change_1d,
			pct_change_3d = EXCLUDED.pct_change_3d,
			range_pct_change = EXCLUDED.range_pct_change,
			yearly_high = EXCLUDED.yearly_high,
			yearly_low = EXCLUDED.yearly_low,
			all_time_high = EXCLUDED.all_time_high,
			all_time_low = EXCLUDED.all_time_low,
			volume_1d = EXCLUDED.volume_1d,
			volume_adv = EXCLUDED.volume_adv,
			volume_multiplier = EXCLUDED.volume_multiplier,
			confidence_score = EXCLUDED.confidence_score,
			quant_direction = EXCLUDED.quant_direction,
			recommended_action = EXCLUDED.recommended_action,
			news_summary = EXCLUDED.news_summary,
			news_sentiment = EXCLUDED.news_sentiment,
			created_at = EXCLUDED.created_at
	`
	for _, r := range results {
		created := r.CreatedAt
		if created.IsZero() {
			created = time.Now()
		}
		scanDate := r.ScanDate
		if scanDate == "" {
			scanDate = NormalizeToIST(created).Format("2006-01-02")
		}
		seg := r.Segment
		if seg == "" {
			seg = "CASH"
		}

		_, err := d.conn.ExecContext(ctx, query,
			scanDate, r.Symbol, seg, r.BreakoutType, r.Direction, r.MomentumDays,
			r.PctChange1D, r.PctChange3D, r.RangePctChange, r.YearlyHigh, r.YearlyLow, r.AllTimeHigh, r.AllTimeLow,
			r.Volume1D, r.VolumeADV, r.VolumeMultiplier,
			r.ConfidenceScore, r.QuantDirection, r.RecommendedAction, r.NewsSummary, r.NewsSentiment, created,
		)
		if err != nil {
			return fmt.Errorf("failed to save scanner result for %s: %w", r.Symbol, err)
		}
	}
	// Auto-prune scanner results older than 14 days to keep database lean and sub-millisecond fast
	_, _ = d.conn.ExecContext(ctx, "DELETE FROM quant_scanner_results WHERE scan_date < CURRENT_DATE - INTERVAL '14 days'")
	return nil
}

// GetLatestScannerResults fetches the most recent scanner results from PostgreSQL
func (d *Database) GetLatestScannerResults(ctx context.Context) ([]DBScanResult, error) {
	return d.GetScannerResultsByDate(ctx, "")
}

// GetScannerResultsByDate fetches scanner results for a specific date (or latest date if empty)
func (d *Database) GetScannerResultsByDate(ctx context.Context, dateStr string) ([]DBScanResult, error) {
	var query string
	var args []interface{}

	if dateStr != "" {
		query = `
			SELECT id, scan_date::text, symbol, COALESCE(segment, 'CASH'), breakout_type, direction, momentum_days,
			       pct_change_1d, pct_change_3d, range_pct_change, COALESCE(yearly_high, 0), COALESCE(yearly_low, 0), COALESCE(all_time_high, 0), COALESCE(all_time_low, 0),
			       volume_1d, volume_adv, volume_multiplier,
			       confidence_score, quant_direction, COALESCE(recommended_action, ''), news_summary, news_sentiment, created_at
			FROM quant_scanner_results
			WHERE scan_date = $1
			ORDER BY confidence_score DESC
		`
		args = append(args, dateStr)
	} else {
		query = `
			SELECT id, scan_date::text, symbol, COALESCE(segment, 'CASH'), breakout_type, direction, momentum_days,
			       pct_change_1d, pct_change_3d, range_pct_change, COALESCE(yearly_high, 0), COALESCE(yearly_low, 0), COALESCE(all_time_high, 0), COALESCE(all_time_low, 0),
			       volume_1d, volume_adv, volume_multiplier,
			       confidence_score, quant_direction, COALESCE(recommended_action, ''), news_summary, news_sentiment, created_at
			FROM quant_scanner_results
			WHERE scan_date = (SELECT MAX(scan_date) FROM quant_scanner_results)
			ORDER BY confidence_score DESC
		`
	}

	rows, err := d.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []DBScanResult
	for rows.Next() {
		var r DBScanResult
		err := rows.Scan(
			&r.ID, &r.ScanDate, &r.Symbol, &r.Segment, &r.BreakoutType, &r.Direction, &r.MomentumDays,
			&r.PctChange1D, &r.PctChange3D, &r.RangePctChange, &r.YearlyHigh, &r.YearlyLow, &r.AllTimeHigh, &r.AllTimeLow,
			&r.Volume1D, &r.VolumeADV, &r.VolumeMultiplier,
			&r.ConfidenceScore, &r.QuantDirection, &r.RecommendedAction, &r.NewsSummary, &r.NewsSentiment, &r.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		r.CreatedAt = NormalizeToIST(r.CreatedAt)
		results = append(results, r)
	}
	return results, nil
}

// GetScannerDates returns a list of distinct historical scanner dates available in PostgreSQL
func (d *Database) GetScannerDates(ctx context.Context) ([]string, error) {
	query := `
		SELECT DISTINCT scan_date::text
		FROM quant_scanner_results
		ORDER BY scan_date DESC
	`
	rows, err := d.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dates []string
	for rows.Next() {
		var dt string
		if err := rows.Scan(&dt); err == nil {
			dates = append(dates, dt)
		}
	}
	return dates, nil
}

// GetRecentCandlesByToken fetches up to limit recent candles for a token
func (d *Database) GetRecentCandlesByToken(ctx context.Context, token int64, limit int) ([]Candle, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT token, time, open, high, low, close, volume
		FROM candles_5m
		WHERE token = $1
		ORDER BY time DESC
		LIMIT $2
	`
	rows, err := d.conn.QueryContext(ctx, query, token, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candles []Candle
	for rows.Next() {
		var c Candle
		err := rows.Scan(&c.Token, &c.Time, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume)
		if err != nil {
			return nil, err
		}
		candles = append(candles, c)
	}

	// Reverse to chronological order (oldest first)
	for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
		candles[i], candles[j] = candles[j], candles[i]
	}
	return candles, nil
}

// GetAllRecentDailyCandlesMap fetches daily candles for all tokens from candles_1d table and groups them by token
func (d *Database) GetAllRecentDailyCandlesMap(ctx context.Context, limitPerToken int) (map[int64][]Candle, error) {
	if limitPerToken <= 0 {
		limitPerToken = 365
	}
	query := `
		SELECT token, time, open, high, low, close, volume
		FROM (
			SELECT token, time, open, high, low, close, volume,
			       ROW_NUMBER() OVER (PARTITION BY token ORDER BY time DESC) as rn
			FROM candles_1d
		) t
		WHERE rn <= $1
		ORDER BY token, time ASC
	`
	rows, err := d.conn.QueryContext(ctx, query, limitPerToken)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64][]Candle)
	for rows.Next() {
		var c Candle
		err := rows.Scan(&c.Token, &c.Time, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume)
		if err != nil {
			return nil, err
		}
		result[c.Token] = append(result[c.Token], c)
	}
	return result, nil
}

// GetRecentDailyCandlesByToken fetches up to limit daily candles for a token from candles_1d table
func (d *Database) GetRecentDailyCandlesByToken(ctx context.Context, token int64, limit int) ([]Candle, error) {
	if limit <= 0 {
		limit = 365
	}
	query := `
		SELECT token, time, open, high, low, close, volume
		FROM candles_1d
		WHERE token = $1
		ORDER BY time DESC
		LIMIT $2
	`
	rows, err := d.conn.QueryContext(ctx, query, token, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candles []Candle
	for rows.Next() {
		var c Candle
		err := rows.Scan(&c.Token, &c.Time, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume)
		if err != nil {
			return nil, err
		}
		candles = append(candles, c)
	}

	// Reverse to chronological order (oldest first)
	for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
		candles[i], candles[j] = candles[j], candles[i]
	}
	return candles, nil
}

// UpsertDailyCandles batch inserts daily candles into candles_1d table
func (d *Database) UpsertDailyCandles(ctx context.Context, candles []Candle) error {
	if len(candles) == 0 {
		return nil
	}
	query := `
		INSERT INTO candles_1d (time, token, open, high, low, close, volume, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (token, time) DO UPDATE SET
			open = EXCLUDED.open,
			high = EXCLUDED.high,
			low = EXCLUDED.low,
			close = EXCLUDED.close,
			volume = EXCLUDED.volume,
			color = EXCLUDED.color
	`
	for _, c := range candles {
		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}
		_, err := d.conn.ExecContext(ctx, query, NormalizeToIST(c.Time), c.Token, c.Open, c.High, c.Low, c.Close, c.Volume, color)
		if err != nil {
			return fmt.Errorf("failed to upsert daily candle: %w", err)
		}
	}
	return nil
}
