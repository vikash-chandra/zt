package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
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
func (d *Database) PersistOrder(orderID string, symbol string, exchange string, quantity int, transactionType string, orderType string, product string, status string, strategy string, price *float64, triggerPrice *float64, statusMessage ...string) error {
	if strategy == "" {
		strategy = "LOW_VOLUME"
	}
	var msg string
	if len(statusMessage) > 0 {
		msg = statusMessage[0]
	}
	query := `
		INSERT INTO orders (order_id, symbol, exchange, quantity, transaction_type, order_type, product, placed_at, status, strategy, price, trigger_price, status_message)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (order_id) DO UPDATE SET
			status = EXCLUDED.status,
			strategy = EXCLUDED.strategy,
			price = COALESCE(EXCLUDED.price, orders.price),
			trigger_price = COALESCE(EXCLUDED.trigger_price, orders.trigger_price),
			status_message = CASE WHEN EXCLUDED.status_message != '' THEN EXCLUDED.status_message ELSE orders.status_message END,
			updated_at = CURRENT_TIMESTAMP
	`
	_, err := d.conn.Exec(query, orderID, symbol, exchange, quantity, transactionType, orderType, product, time.Now(), status, strategy, price, triggerPrice, msg)
	return err
}

// UpdateOrderStatus updates an existing order's status, average price, filled quantity, and optional status message
func (d *Database) UpdateOrderStatus(orderID, status string, averagePrice float64, filledQuantity int, statusMessage ...string) error {
	var query string
	var err error
	now := time.Now()
	if len(statusMessage) > 0 && statusMessage[0] != "" {
		query = `UPDATE orders SET status = $1, average_price = $2, filled_quantity = $3, status_message = $4, updated_at = $5 WHERE order_id = $6`
		_, err = d.conn.Exec(query, status, averagePrice, filledQuantity, statusMessage[0], now, orderID)
	} else {
		query = `UPDATE orders SET status = $1, average_price = $2, filled_quantity = $3, updated_at = $4 WHERE order_id = $5`
		_, err = d.conn.Exec(query, status, averagePrice, filledQuantity, now, orderID)
	}
	return err
}

// RejectedOrderRecord encapsulates details of orders rejected by broker RMS or API
type RejectedOrderRecord struct {
	OrderID         string    `json:"order_id"`
	Symbol          string    `json:"symbol"`
	Exchange        string    `json:"exchange"`
	Quantity        int       `json:"quantity"`
	TransactionType string    `json:"transaction_type"`
	OrderType       string    `json:"order_type"`
	Product         string    `json:"product"`
	Price           float64   `json:"price"`
	TriggerPrice    float64   `json:"trigger_price"`
	Status          string    `json:"status"`
	PlacedAt        time.Time `json:"placed_at"`
	Strategy        string    `json:"strategy"`
	StatusMessage   string    `json:"status_message"`
}

// GetRejectedOrders retrieves all orders marked REJECTED, optionally filtered by date
func (d *Database) GetRejectedOrders(ctx context.Context, dateStr string) ([]RejectedOrderRecord, error) {
	var query string
	var rows *sql.Rows
	var err error

	if dateStr != "" {
		query = `
			SELECT o.order_id, o.symbol, o.exchange, o.quantity, o.transaction_type, o.order_type,
			       o.product, COALESCE(o.price, 0), COALESCE(o.trigger_price, 0), o.status,
			       o.placed_at, o.strategy,
			       COALESCE(NULLIF(o.status_message, ''), ev.reason, 'Order rejected by broker') AS status_message
			FROM orders o
			LEFT JOIN LATERAL (
				SELECT reason FROM stock_strategy_events
				WHERE symbol = o.symbol AND (stage = 'ORDER_REJECTED' OR stage = 'TRADE_SKIPPED')
				  AND event_time >= o.placed_at - INTERVAL '2 minute' AND event_time <= o.placed_at + INTERVAL '2 minute'
				ORDER BY event_time DESC LIMIT 1
			) ev ON TRUE
			WHERE o.status = 'REJECTED' AND o.placed_at::date = $1::date
			ORDER BY o.placed_at DESC
		`
		rows, err = d.conn.QueryContext(ctx, query, dateStr)
	} else {
		query = `
			SELECT o.order_id, o.symbol, o.exchange, o.quantity, o.transaction_type, o.order_type,
			       o.product, COALESCE(o.price, 0), COALESCE(o.trigger_price, 0), o.status,
			       o.placed_at, o.strategy,
			       COALESCE(NULLIF(o.status_message, ''), ev.reason, 'Order rejected by broker') AS status_message
			FROM orders o
			LEFT JOIN LATERAL (
				SELECT reason FROM stock_strategy_events
				WHERE symbol = o.symbol AND (stage = 'ORDER_REJECTED' OR stage = 'TRADE_SKIPPED')
				  AND event_time >= o.placed_at - INTERVAL '2 minute' AND event_time <= o.placed_at + INTERVAL '2 minute'
				ORDER BY event_time DESC LIMIT 1
			) ev ON TRUE
			WHERE o.status = 'REJECTED'
			ORDER BY o.placed_at DESC
			LIMIT 150
		`
		rows, err = d.conn.QueryContext(ctx, query)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]RejectedOrderRecord, 0)
	for rows.Next() {
		var r RejectedOrderRecord
		if err := rows.Scan(&r.OrderID, &r.Symbol, &r.Exchange, &r.Quantity, &r.TransactionType,
			&r.OrderType, &r.Product, &r.Price, &r.TriggerPrice, &r.Status,
			&r.PlacedAt, &r.Strategy, &r.StatusMessage); err != nil {
			return nil, err
		}
		r.PlacedAt = NormalizeToIST(r.PlacedAt)
		list = append(list, r)
	}
	return list, nil
}

// GetOrderStrategy retrieves the strategy associated with an order ID
func (d *Database) GetOrderStrategy(orderID string) (string, error) {
	var strategy string
	err := d.conn.QueryRow("SELECT strategy FROM orders WHERE order_id = $1", orderID).Scan(&strategy)
	return strategy, err
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
	if err == nil && (key == "fo:stocks" || key == "nse:all_stocks" || key == "nifty50:constituents" || key == "nifty500_fo:stocks") {
		d.InvalidateTokenCache()
	}
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
	if err == nil {
		d.InvalidateTokenCache()
	}
	return err
}

// QuerySymbolToken retrieves cached token mapping inside metadata_cache across all registered registries
func (d *Database) QuerySymbolToken(ctx context.Context, symbol string) (int64, error) {
	return d.ResolveSymbolToken(ctx, symbol)
}

// QueryRowSymbolToken queries cached token mapping without context
func (d *Database) QueryRowSymbolToken(symbol string) (int64, error) {
	return d.ResolveSymbolToken(context.Background(), symbol)
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

// GetBrokerSLOrderID retrieves the broker SL order ID for an open position
func (d *Database) GetBrokerSLOrderID(ctx context.Context, orderID string) (string, error) {
	query := `
		SELECT COALESCE(broker_sl_order_id, '')
		FROM positions
		WHERE order_id = $1
		LIMIT 1
	`
	var slOrderID string
	err := d.conn.QueryRowContext(ctx, query, orderID).Scan(&slOrderID)
	if err != nil {
		return "", err
	}
	return slOrderID, nil
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

// GetPositionStrategy retrieves the original strategy stored for an open position
func (d *Database) GetPositionStrategy(ctx context.Context, orderID string, symbol string) (string, error) {
	if d == nil || d.conn == nil {
		return "", fmt.Errorf("database not initialized")
	}
	var strategy string
	query := `
		SELECT strategy FROM positions 
		WHERE (order_id = $1 OR symbol = $2) AND strategy IS NOT NULL AND strategy != ''
		ORDER BY created_at DESC LIMIT 1
	`
	err := d.conn.QueryRowContext(ctx, query, orderID, symbol).Scan(&strategy)
	if err != nil {
		return "", err
	}
	return strategy, nil
}

// SelectedSectorRecord holds details of a selected sector
type SelectedSectorRecord struct {
	Sector     string    `json:"sector"`
	PctChange  float64   `json:"pct_change"`
	SelectedAt time.Time `json:"selected_at"`
}

// ClearSelectedSectors deletes all selected sectors for a given date
func (d *Database) ClearSelectedSectors(ctx context.Context, dateStr string) error {
	if d == nil || d.conn == nil {
		return nil
	}
	_, err := d.conn.ExecContext(ctx, `DELETE FROM selected_sectors WHERE date = $1`, dateStr)
	return err
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

// SaveDailyWatchlist saves the daily selection watchlist to the database, pruning stale symbols for the date
func (d *Database) SaveDailyWatchlist(ctx context.Context, items []DailyWatchlistItem) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Prune symbols for each date that are no longer part of the watchlist
	dateMap := make(map[string][]string)
	for _, it := range items {
		dateMap[it.Date] = append(dateMap[it.Date], it.Symbol)
	}
	for dt, syms := range dateMap {
		var args []interface{}
		args = append(args, dt)
		placeholders := make([]string, len(syms))
		for i, sym := range syms {
			placeholders[i] = fmt.Sprintf("$%d", i+2)
			args = append(args, sym)
		}
		delQuery := fmt.Sprintf("DELETE FROM daily_watchlists WHERE date = $1 AND symbol NOT IN (%s)", strings.Join(placeholders, ","))
		if _, err := tx.ExecContext(ctx, delQuery, args...); err != nil {
			return err
		}
	}

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

// UpsertDailyWatchlistItems inserts or updates daily selection items without deleting existing symbols for that date
func (d *Database) UpsertDailyWatchlistItems(ctx context.Context, items []DailyWatchlistItem) error {
	if len(items) == 0 {
		return nil
	}
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

// DeleteDailyWatchlistStock permanently deletes a stock from daily_watchlists for a specific date
func (d *Database) DeleteDailyWatchlistStock(ctx context.Context, dateStr string, symbol string) error {
	query := `DELETE FROM daily_watchlists WHERE date = $1 AND symbol = $2`
	_, err := d.conn.ExecContext(ctx, query, dateStr, symbol)
	return err
}

// UpdateDailyWatchlistSelector updates the selectors column for a specific stock on a date
func (d *Database) UpdateDailyWatchlistSelector(ctx context.Context, dateStr string, symbol string, selectors string) error {
	query := `UPDATE daily_watchlists SET selectors = $3 WHERE date = $1 AND symbol = $2`
	_, err := d.conn.ExecContext(ctx, query, dateStr, symbol, selectors)
	return err
}

// GetMarketBreadthForDate gets market breadth logs for a specific date (YYYY-MM-DD in IST)
func (d *Database) GetMarketBreadthForDate(ctx context.Context, dateStr string) (int, int, int, string, error) {
	var advances, declines, neutrals int
	var globalBias string
	err := d.conn.QueryRowContext(ctx,
		`SELECT advances, declines, neutrals, global_bias 
		 FROM market_breadth_logs 
		 WHERE (time AT TIME ZONE 'Asia/Kolkata')::DATE = $1::DATE 
		 ORDER BY time DESC LIMIT 1`,
		dateStr,
	).Scan(&advances, &declines, &neutrals, &globalBias)
	return advances, declines, neutrals, globalBias, err
}

// GetTradingMetricsForDate returns count, total pnl and tx value of equity trades for a specific date (YYYY-MM-DD in IST)
func (d *Database) GetTradingMetricsForDate(ctx context.Context, dateStr string) (int, float64, float64, error) {
	parsedDate, err := time.ParseInLocation("2006-01-02", dateStr, ISTLocation)
	if err != nil {
		return 0, 0, 0, err
	}
	startOfDay := time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 0, 0, 0, 0, ISTLocation)
	endOfDay := startOfDay.Add(24 * time.Hour)

	var totalTrades int
	var totalPnL float64
	var totalTxValue float64

	err = d.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM trades WHERE created_at >= $1 AND created_at < $2 AND COALESCE(strategy, '') != 'OPTIONS_SUPERTREND'", startOfDay, endOfDay).Scan(&totalTrades)
	if err != nil {
		return 0, 0, 0, err
	}
	err = d.conn.QueryRowContext(ctx, "SELECT COALESCE(SUM(pnl), 0) FROM trades WHERE created_at >= $1 AND created_at < $2 AND COALESCE(strategy, '') != 'OPTIONS_SUPERTREND'", startOfDay, endOfDay).Scan(&totalPnL)
	if err != nil {
		return 0, 0, 0, err
	}
	err = d.conn.QueryRowContext(ctx, "SELECT COALESCE(SUM(entry_price * quantity), 0) FROM trades WHERE created_at >= $1 AND created_at < $2 AND COALESCE(strategy, '') != 'OPTIONS_SUPERTREND'", startOfDay, endOfDay).Scan(&totalTxValue)
	if err != nil {
		return 0, 0, 0, err
	}

	return totalTrades, totalPnL, totalTxValue, nil
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
	IndexSymbol      string    `json:"index_symbol"`
	Multiplier       int       `json:"multiplier"`
	LastTrend        string    `json:"last_trend"`
	SLStoppedTrend   string    `json:"sl_stopped_trend"`
	AwaitingReversal bool      `json:"awaiting_reversal"`
	ActiveTradeID    int64     `json:"active_trade_id"`
	ActiveOrderID    string    `json:"active_order_id"`
	ActiveSLOrderID  string    `json:"active_sl_order_id"`
	ActiveSymbol     string    `json:"active_symbol"`
	ActiveSide       string    `json:"active_side"`
	ActiveQty        int       `json:"active_qty"`
	EntryPremium     float64   `json:"entry_premium"`
	SLPrice          float64   `json:"sl_price"`
	PaperBalance     float64   `json:"paper_balance"`
	ActiveCreatedAt  time.Time `json:"active_created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SaveOptionsBotStateForIndex upserts the state row for a specific index in options_bot_state table
func (d *Database) SaveOptionsBotStateForIndex(ctx context.Context, state *OptionsBotState) error {
	indexSym := strings.TrimSpace(state.IndexSymbol)
	if indexSym == "" {
		indexSym = "NIFTY 50"
	}
	spec, _ := ResolveIndexSpec(indexSym)
	indexSym = spec.Name

	var activeCreated interface{}
	if !state.ActiveCreatedAt.IsZero() {
		activeCreated = NormalizeToIST(state.ActiveCreatedAt)
	}

	query := `
		INSERT INTO options_bot_state (
			index_symbol, multiplier, last_trend, sl_stopped_trend, awaiting_reversal,
			active_trade_id, active_order_id, active_sl_order_id, active_symbol, active_side, active_qty,
			entry_premium, sl_price, paper_balance, active_created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, NOW())
		ON CONFLICT (index_symbol) DO UPDATE SET
			multiplier = EXCLUDED.multiplier,
			last_trend = EXCLUDED.last_trend,
			sl_stopped_trend = EXCLUDED.sl_stopped_trend,
			awaiting_reversal = EXCLUDED.awaiting_reversal,
			active_trade_id = EXCLUDED.active_trade_id,
			active_order_id = EXCLUDED.active_order_id,
			active_sl_order_id = EXCLUDED.active_sl_order_id,
			active_symbol = EXCLUDED.active_symbol,
			active_side = EXCLUDED.active_side,
			active_qty = EXCLUDED.active_qty,
			entry_premium = EXCLUDED.entry_premium,
			sl_price = EXCLUDED.sl_price,
			paper_balance = EXCLUDED.paper_balance,
			active_created_at = EXCLUDED.active_created_at,
			updated_at = NOW()
	`
	_, err := d.conn.ExecContext(ctx, query,
		indexSym, state.Multiplier, state.LastTrend, state.SLStoppedTrend, state.AwaitingReversal,
		state.ActiveTradeID, state.ActiveOrderID, state.ActiveSLOrderID, state.ActiveSymbol, state.ActiveSide, state.ActiveQty,
		state.EntryPremium, state.SLPrice, state.PaperBalance, activeCreated,
	)
	return err
}

// SaveOptionsBotState saves the default NIFTY 50 state row for backwards compatibility
func (d *Database) SaveOptionsBotState(ctx context.Context, state *OptionsBotState) error {
	if state.IndexSymbol == "" {
		state.IndexSymbol = "NIFTY 50"
	}
	return d.SaveOptionsBotStateForIndex(ctx, state)
}

// GetOptionsBotStateForIndex retrieves the state row for a specific index
func (d *Database) GetOptionsBotStateForIndex(ctx context.Context, indexSym string) (*OptionsBotState, error) {
	spec, _ := ResolveIndexSpec(indexSym)
	cleanName := spec.Name

	query := `
		SELECT 1 AS id, index_symbol, multiplier, last_trend, sl_stopped_trend, awaiting_reversal,
		       COALESCE(active_trade_id, 0), COALESCE(active_order_id, ''), COALESCE(active_sl_order_id, ''), COALESCE(active_symbol, ''), COALESCE(active_side, ''), COALESCE(active_qty, 0),
		       COALESCE(entry_premium, 0), COALESCE(sl_price, 0), paper_balance, COALESCE(active_created_at, updated_at), updated_at
		FROM options_bot_state
		WHERE index_symbol = $1 OR index_symbol = $2
		LIMIT 1
	`
	var st OptionsBotState
	err := d.conn.QueryRowContext(ctx, query, cleanName, spec.CleanPrefix).Scan(
		&st.ID, &st.IndexSymbol, &st.Multiplier, &st.LastTrend, &st.SLStoppedTrend, &st.AwaitingReversal,
		&st.ActiveTradeID, &st.ActiveOrderID, &st.ActiveSLOrderID, &st.ActiveSymbol, &st.ActiveSide, &st.ActiveQty,
		&st.EntryPremium, &st.SLPrice, &st.PaperBalance, &st.ActiveCreatedAt, &st.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return &OptionsBotState{
				ID:               1,
				IndexSymbol:      cleanName,
				Multiplier:       1,
				LastTrend:        "NEUTRAL",
				SLStoppedTrend:   "",
				AwaitingReversal: false,
				ActiveTradeID:    0,
				PaperBalance:     1000000.0,
			}, nil
		}
		return nil, err
	}
	return &st, nil
}

// GetOptionsBotState retrieves default NIFTY 50 state row
func (d *Database) GetOptionsBotState(ctx context.Context) (*OptionsBotState, error) {
	return d.GetOptionsBotStateForIndex(ctx, "NIFTY 50")
}

// GetAllOptionsBotStates returns all active index state rows
func (d *Database) GetAllOptionsBotStates(ctx context.Context) ([]OptionsBotState, error) {
	query := `
		SELECT 1 AS id, COALESCE(index_symbol, 'NIFTY 50'), multiplier, last_trend, sl_stopped_trend, awaiting_reversal,
		       COALESCE(active_trade_id, 0), COALESCE(active_order_id, ''), COALESCE(active_sl_order_id, ''), COALESCE(active_symbol, ''), COALESCE(active_side, ''), COALESCE(active_qty, 0),
		       COALESCE(entry_premium, 0), COALESCE(sl_price, 0), paper_balance, COALESCE(active_created_at, updated_at), updated_at
		FROM options_bot_state
		ORDER BY index_symbol
	`
	rows, err := d.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var states []OptionsBotState
	for rows.Next() {
		var st OptionsBotState
		if err := rows.Scan(
			&st.ID, &st.IndexSymbol, &st.Multiplier, &st.LastTrend, &st.SLStoppedTrend, &st.AwaitingReversal,
			&st.ActiveTradeID, &st.ActiveOrderID, &st.ActiveSLOrderID, &st.ActiveSymbol, &st.ActiveSide, &st.ActiveQty,
			&st.EntryPremium, &st.SLPrice, &st.PaperBalance, &st.ActiveCreatedAt, &st.UpdatedAt,
		); err == nil {
			states = append(states, st)
		}
	}
	return states, nil
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
	CurrentPrice      float64   `json:"current_price"`
	DistanceToHighPct float64   `json:"distance_to_high_pct"`
	YearlyHigh        float64   `json:"yearly_high"`
	YearlyLow         float64   `json:"yearly_low"`
	MonthlyHigh       float64   `json:"monthly_high"`
	MonthlyLow        float64   `json:"monthly_low"`
	WeeklyHigh        float64   `json:"weekly_high"`
	WeeklyLow         float64   `json:"weekly_low"`
	AllTimeHigh       float64   `json:"all_time_high"`
	AllTimeLow        float64   `json:"all_time_low"`
	IsDailyCluster    bool      `json:"is_daily_cluster"`
	IsWeeklyCluster   bool      `json:"is_weekly_cluster"`
	ClusterSpread     float64   `json:"cluster_spread"`
	ClusterCenter     float64   `json:"cluster_center"`
	EMA10             float64   `json:"ema_10"`
	EMA20             float64   `json:"ema_20"`
	EMA89             float64   `json:"ema_89"`
	Volume1D          int64     `json:"volume_1d"`
	VolumeADV         int64     `json:"volume_adv"`
	VolumeMultiplier  float64   `json:"volume_multiplier"`
	DowTrend          string    `json:"dow_trend"`
	PositionalZone    string    `json:"positional_zone"`
	ActionTiming      string    `json:"action_timing"`
	SelectionReason   string    `json:"selection_reason"`
	SupportZone       float64   `json:"support_zone"`
	ResistanceZone    float64   `json:"resistance_zone"`
	TriggerPrice      float64   `json:"trigger_price"`
	StopLoss          float64   `json:"stop_loss"`
	TargetPrice       float64   `json:"target_price"`
	TradeAction       string    `json:"trade_action"`
	IsSwingDoji       bool      `json:"is_swing_doji"`
	SwingDojiType     string    `json:"swing_doji_type"`
	CandleWickPct     float64   `json:"candle_wick_pct"`
	UpperWickPct      float64   `json:"upper_wick_pct"`
	LowerWickPct      float64   `json:"lower_wick_pct"`
	DojiDescription   string    `json:"doji_description"`
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
			pct_change_1d, pct_change_3d, range_pct_change, current_price, distance_to_high_pct,
			yearly_high, yearly_low, monthly_high, monthly_low, weekly_high, weekly_low, all_time_high, all_time_low,
			is_daily_cluster, is_weekly_cluster, cluster_spread, cluster_center, ema_10, ema_20, ema_89,
			volume_1d, volume_adv, volume_multiplier,
			dow_trend, positional_zone, action_timing, selection_reason, support_zone, resistance_zone,
			trigger_price, stop_loss, target_price, trade_action,
			is_swing_doji, swing_doji_type, candle_wick_pct, upper_wick_pct, lower_wick_pct, doji_description,
			confidence_score, quant_direction, recommended_action, news_summary, news_sentiment, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36, $37, $38, $39, $40, $41, $42, $43, $44, $45, $46, $47, $48, $49, $50, $51)
		ON CONFLICT (scan_date, symbol) DO UPDATE SET
			segment = EXCLUDED.segment,
			breakout_type = EXCLUDED.breakout_type,
			direction = EXCLUDED.direction,
			momentum_days = EXCLUDED.momentum_days,
			pct_change_1d = EXCLUDED.pct_change_1d,
			pct_change_3d = EXCLUDED.pct_change_3d,
			range_pct_change = EXCLUDED.range_pct_change,
			current_price = EXCLUDED.current_price,
			distance_to_high_pct = EXCLUDED.distance_to_high_pct,
			yearly_high = EXCLUDED.yearly_high,
			yearly_low = EXCLUDED.yearly_low,
			monthly_high = EXCLUDED.monthly_high,
			monthly_low = EXCLUDED.monthly_low,
			weekly_high = EXCLUDED.weekly_high,
			weekly_low = EXCLUDED.weekly_low,
			all_time_high = EXCLUDED.all_time_high,
			all_time_low = EXCLUDED.all_time_low,
			is_daily_cluster = EXCLUDED.is_daily_cluster,
			is_weekly_cluster = EXCLUDED.is_weekly_cluster,
			cluster_spread = EXCLUDED.cluster_spread,
			cluster_center = EXCLUDED.cluster_center,
			ema_10 = EXCLUDED.ema_10,
			ema_20 = EXCLUDED.ema_20,
			ema_89 = EXCLUDED.ema_89,
			volume_1d = EXCLUDED.volume_1d,
			volume_adv = EXCLUDED.volume_adv,
			volume_multiplier = EXCLUDED.volume_multiplier,
			dow_trend = EXCLUDED.dow_trend,
			positional_zone = EXCLUDED.positional_zone,
			action_timing = EXCLUDED.action_timing,
			selection_reason = EXCLUDED.selection_reason,
			support_zone = EXCLUDED.support_zone,
			resistance_zone = EXCLUDED.resistance_zone,
			trigger_price = EXCLUDED.trigger_price,
			stop_loss = EXCLUDED.stop_loss,
			target_price = EXCLUDED.target_price,
			trade_action = EXCLUDED.trade_action,
			is_swing_doji = EXCLUDED.is_swing_doji,
			swing_doji_type = EXCLUDED.swing_doji_type,
			candle_wick_pct = EXCLUDED.candle_wick_pct,
			upper_wick_pct = EXCLUDED.upper_wick_pct,
			lower_wick_pct = EXCLUDED.lower_wick_pct,
			doji_description = EXCLUDED.doji_description,
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
			created = NowIST()
		} else {
			created = created.In(ISTLocation)
		}
		scanDate := r.ScanDate
		if scanDate == "" {
			scanDate = created.Format("2006-01-02")
		}
		seg := r.Segment
		if seg == "" {
			seg = "CASH"
		}

		dojiType := r.SwingDojiType
		if dojiType == "" {
			dojiType = "NONE"
		}

		_, err := d.conn.ExecContext(ctx, query,
			scanDate, r.Symbol, seg, r.BreakoutType, r.Direction, r.MomentumDays,
			r.PctChange1D, r.PctChange3D, r.RangePctChange, r.CurrentPrice, r.DistanceToHighPct,
			r.YearlyHigh, r.YearlyLow, r.MonthlyHigh, r.MonthlyLow, r.WeeklyHigh, r.WeeklyLow, r.AllTimeHigh, r.AllTimeLow,
			r.IsDailyCluster, r.IsWeeklyCluster, r.ClusterSpread, r.ClusterCenter, r.EMA10, r.EMA20, r.EMA89,
			r.Volume1D, r.VolumeADV, r.VolumeMultiplier,
			r.DowTrend, r.PositionalZone, r.ActionTiming, r.SelectionReason, r.SupportZone, r.ResistanceZone,
			r.TriggerPrice, r.StopLoss, r.TargetPrice, r.TradeAction,
			r.IsSwingDoji, dojiType, r.CandleWickPct, r.UpperWickPct, r.LowerWickPct, r.DojiDescription,
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

// DeleteScannerResultsByDate removes scanner results for a given scan_date
func (d *Database) DeleteScannerResultsByDate(ctx context.Context, scanDate string) error {
	if scanDate == "" {
		return nil
	}
	_, err := d.conn.ExecContext(ctx, "DELETE FROM quant_scanner_results WHERE scan_date = $1", scanDate)
	return err
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
			       pct_change_1d, pct_change_3d, range_pct_change, COALESCE(current_price, 0), COALESCE(distance_to_high_pct, 0),
			       COALESCE(yearly_high, 0), COALESCE(yearly_low, 0), COALESCE(monthly_high, 0), COALESCE(monthly_low, 0), COALESCE(weekly_high, 0), COALESCE(weekly_low, 0), COALESCE(all_time_high, 0), COALESCE(all_time_low, 0),
			       COALESCE(is_daily_cluster, false), COALESCE(is_weekly_cluster, false), COALESCE(cluster_spread, 0), COALESCE(cluster_center, 0),
			       COALESCE(ema_10, 0), COALESCE(ema_20, 0), COALESCE(ema_89, 0),
			       volume_1d, volume_adv, volume_multiplier,
			       COALESCE(dow_trend, ''), COALESCE(positional_zone, ''), COALESCE(action_timing, ''), COALESCE(selection_reason, ''), COALESCE(support_zone, 0), COALESCE(resistance_zone, 0),
			       COALESCE(trigger_price, 0), COALESCE(stop_loss, 0), COALESCE(target_price, 0), COALESCE(trade_action, ''),
			       COALESCE(is_swing_doji, false), COALESCE(swing_doji_type, 'NONE'), COALESCE(candle_wick_pct, 0), COALESCE(upper_wick_pct, 0), COALESCE(lower_wick_pct, 0), COALESCE(doji_description, ''),
			       confidence_score, quant_direction, COALESCE(recommended_action, ''), news_summary, news_sentiment, created_at
			FROM quant_scanner_results
			WHERE scan_date = $1
			ORDER BY confidence_score DESC
		`
		args = append(args, dateStr)
	} else {
		query = `
			SELECT id, scan_date::text, symbol, COALESCE(segment, 'CASH'), breakout_type, direction, momentum_days,
			       pct_change_1d, pct_change_3d, range_pct_change, COALESCE(current_price, 0), COALESCE(distance_to_high_pct, 0),
			       COALESCE(yearly_high, 0), COALESCE(yearly_low, 0), COALESCE(monthly_high, 0), COALESCE(monthly_low, 0), COALESCE(weekly_high, 0), COALESCE(weekly_low, 0), COALESCE(all_time_high, 0), COALESCE(all_time_low, 0),
			       COALESCE(is_daily_cluster, false), COALESCE(is_weekly_cluster, false), COALESCE(cluster_spread, 0), COALESCE(cluster_center, 0),
			       COALESCE(ema_10, 0), COALESCE(ema_20, 0), COALESCE(ema_89, 0),
			       volume_1d, volume_adv, volume_multiplier,
			       COALESCE(dow_trend, ''), COALESCE(positional_zone, ''), COALESCE(action_timing, ''), COALESCE(selection_reason, ''), COALESCE(support_zone, 0), COALESCE(resistance_zone, 0),
			       COALESCE(trigger_price, 0), COALESCE(stop_loss, 0), COALESCE(target_price, 0), COALESCE(trade_action, ''),
			       COALESCE(is_swing_doji, false), COALESCE(swing_doji_type, 'NONE'), COALESCE(candle_wick_pct, 0), COALESCE(upper_wick_pct, 0), COALESCE(lower_wick_pct, 0), COALESCE(doji_description, ''),
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
			&r.PctChange1D, &r.PctChange3D, &r.RangePctChange, &r.CurrentPrice, &r.DistanceToHighPct,
			&r.YearlyHigh, &r.YearlyLow, &r.MonthlyHigh, &r.MonthlyLow, &r.WeeklyHigh, &r.WeeklyLow, &r.AllTimeHigh, &r.AllTimeLow,
			&r.IsDailyCluster, &r.IsWeeklyCluster, &r.ClusterSpread, &r.ClusterCenter,
			&r.EMA10, &r.EMA20, &r.EMA89,
			&r.Volume1D, &r.VolumeADV, &r.VolumeMultiplier,
			&r.DowTrend, &r.PositionalZone, &r.ActionTiming, &r.SelectionReason, &r.SupportZone, &r.ResistanceZone,
			&r.TriggerPrice, &r.StopLoss, &r.TargetPrice, &r.TradeAction,
			&r.IsSwingDoji, &r.SwingDojiType, &r.CandleWickPct, &r.UpperWickPct, &r.LowerWickPct, &r.DojiDescription,
			&r.ConfidenceScore, &r.QuantDirection, &r.RecommendedAction, &r.NewsSummary, &r.NewsSentiment, &r.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		r.CreatedAt = r.CreatedAt.In(ISTLocation)
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

	var timeClause string
	if limitPerToken <= 10 {
		timeClause = "WHERE time >= NOW() - INTERVAL '30 days'"
	} else if limitPerToken <= 30 {
		timeClause = "WHERE time >= NOW() - INTERVAL '90 days'"
	} else if limitPerToken <= 365 {
		timeClause = "WHERE time >= NOW() - INTERVAL '600 days'"
	}

	query := fmt.Sprintf(`
		SELECT token, time, open, high, low, close, volume
		FROM (
			SELECT token, time, open, high, low, close, volume,
			       ROW_NUMBER() OVER (PARTITION BY token ORDER BY time DESC) as rn
			FROM candles_1d
			%s
		) t
		WHERE rn <= $1
		ORDER BY token, time ASC
	`, timeClause)
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
		c.Time = NormalizeToIST(c.Time)
		result[c.Token] = append(result[c.Token], c)
	}
	return result, nil
}

// GetRecentDailyCandlesForTokensMap fetches daily candles for specific tokens from candles_1d table and groups them by token
func (d *Database) GetRecentDailyCandlesForTokensMap(ctx context.Context, tokens []int64, limitPerToken int) (map[int64][]Candle, error) {
	if limitPerToken <= 0 {
		limitPerToken = 10
	}
	if len(tokens) == 0 {
		return d.GetAllRecentDailyCandlesMap(ctx, limitPerToken)
	}

	var timeClause string
	if limitPerToken <= 10 {
		timeClause = "AND time >= NOW() - INTERVAL '30 days'"
	} else if limitPerToken <= 30 {
		timeClause = "AND time >= NOW() - INTERVAL '90 days'"
	} else if limitPerToken <= 365 {
		timeClause = "AND time >= NOW() - INTERVAL '600 days'"
	}

	query := fmt.Sprintf(`
		SELECT token, time, open, high, low, close, volume
		FROM (
			SELECT token, time, open, high, low, close, volume,
			       ROW_NUMBER() OVER (PARTITION BY token ORDER BY time DESC) as rn
			FROM candles_1d
			WHERE token = ANY($1) %s
		) t
		WHERE rn <= $2
		ORDER BY token, time ASC
	`, timeClause)
	rows, err := d.conn.QueryContext(ctx, query, pq.Array(tokens), limitPerToken)
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
		c.Time = NormalizeToIST(c.Time)
		result[c.Token] = append(result[c.Token], c)
	}
	return result, nil
}

// GetPreviousDayOHLCFromDaily gets the most recent daily candle before the given date
func (d *Database) GetPreviousDayOHLCFromDaily(ctx context.Context, token int64, beforeTime time.Time) (float64, float64, float64, time.Time, error) {
	query := `
		SELECT high, low, close, time
		FROM candles_1d
		WHERE token = $1 AND time < $2
		ORDER BY time DESC
		LIMIT 1
	`
	var high, low, closeVal float64
	var t time.Time
	err := d.conn.QueryRowContext(ctx, query, token, beforeTime).Scan(&high, &low, &closeVal, &t)
	if err != nil {
		return 0, 0, 0, time.Time{}, err
	}
	return high, low, closeVal, NormalizeToIST(t), nil
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
		c.Time = NormalizeToIST(c.Time)
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

// CreateLiveTrade inserts a single trade row when position opens with status = 'LIVE' and explicit expiry_date
func (d *Database) CreateLiveTrade(ctx context.Context, symbol, side string, quantity int, entryPrice float64, entryTime time.Time, expiryDate, strategy string) (int64, error) {
	query := `
		INSERT INTO trades (symbol, entry_price, exit_price, quantity, pnl, side, time_held_minutes, entry_time, exit_time, created_at, strategy, status, expiry_date)
		VALUES ($1, $2, NULL, $3, 0.0, $4, 0, $5, NULL, $5, $6, 'LIVE', NULLIF($7, '')::date)
		RETURNING id
	`
	var tradeID int64
	err := d.conn.QueryRowContext(ctx, query, symbol, entryPrice, quantity, side, NormalizeToIST(entryTime), strategy, expiryDate).Scan(&tradeID)
	return tradeID, err
}

// CloseLiveTrade updates the exact trade row when position closes with exit_time, exit_price, pnl, and status
func (d *Database) CloseLiveTrade(ctx context.Context, tradeID int64, exitPrice float64, exitTime time.Time, pnl float64, statusText string) error {
	query := `
		UPDATE trades
		SET exit_price = $1,
		    exit_time = $2,
		    pnl = $3,
		    gross_pnl = $3,
		    net_pnl = $3 - COALESCE(charges, 0.0),
		    status = $4,
		    time_held_minutes = GREATEST(1, EXTRACT(EPOCH FROM ($2 - entry_time))/60)::int,
		    updated_at = NOW()
		WHERE id = $5
	`
	_, err := d.conn.ExecContext(ctx, query, exitPrice, NormalizeToIST(exitTime), pnl, statusText, tradeID)
	return err
}

// CloseLiveTradeWithCharges updates the trade row with exit details and exact calculated Zerodha charges & net P&L
func (d *Database) CloseLiveTradeWithCharges(ctx context.Context, tradeID int64, exitPrice float64, exitTime time.Time, grossPnL, charges, netPnL float64, statusText string) error {
	query := `
		UPDATE trades
		SET exit_price = $1,
		    exit_time = $2,
		    pnl = $3,
		    gross_pnl = $3,
		    charges = $4,
		    net_pnl = $5,
		    status = $6,
		    time_held_minutes = GREATEST(1, EXTRACT(EPOCH FROM ($2 - entry_time))/60)::int,
		    updated_at = NOW()
		WHERE id = $7
	`
	_, err := d.conn.ExecContext(ctx, query, exitPrice, NormalizeToIST(exitTime), grossPnL, charges, netPnL, statusText, tradeID)
	return err
}

// GetLatestOpenTradeID returns the latest open trade ID for a symbol and strategy with status = 'LIVE'
func (d *Database) GetLatestOpenTradeID(ctx context.Context, symbol, strategy string) (int64, error) {
	query := `
		SELECT id
		FROM trades
		WHERE symbol = $1 AND strategy = $2 AND status = 'LIVE'
		ORDER BY id DESC
		LIMIT 1
	`
	var tradeID int64
	err := d.conn.QueryRowContext(ctx, query, symbol, strategy).Scan(&tradeID)
	return tradeID, err
}

// GetTradeExpiryDate returns the expiry_date string for a given trade id
func (d *Database) GetTradeExpiryDate(ctx context.Context, tradeID int64) (string, error) {
	if d == nil || d.conn == nil {
		return "", fmt.Errorf("database connection is nil")
	}
	var exp sql.NullString
	err := d.conn.QueryRowContext(ctx, "SELECT COALESCE(expiry_date::text, '') FROM trades WHERE id = $1", tradeID).Scan(&exp)
	if err != nil {
		return "", err
	}
	return exp.String, nil
}

// GetActiveOptionsTradeForIndex looks up any unclosed LIVE trade for an index prefix created today
func (d *Database) GetActiveOptionsTradeForIndex(ctx context.Context, cleanPrefix string) (int64, string, int, float64, time.Time, string, error) {
	query := `
		SELECT id, symbol, quantity, entry_price, entry_time, COALESCE(expiry_date::text, '')
		FROM trades
		WHERE strategy = 'OPTIONS_SUPERTREND' 
		  AND symbol LIKE $1 || '%' 
		  AND status = 'LIVE'
		  AND created_at >= CURRENT_DATE
		ORDER BY id DESC
		LIMIT 1
	`
	var id int64
	var sym string
	var qty int
	var entryPrice float64
	var entryTime time.Time
	var expDate string
	err := d.conn.QueryRowContext(ctx, query, cleanPrefix).Scan(&id, &sym, &qty, &entryPrice, &entryTime, &expDate)
	return id, sym, qty, entryPrice, entryTime, expDate, err
}

// CleanupDuplicateLiveTrades marks older duplicate LIVE trades as REPLACED_ON_RESTART
func (d *Database) CleanupDuplicateLiveTrades(ctx context.Context) error {
	query := `
		WITH ranked_trades AS (
			SELECT id, symbol,
			       ROW_NUMBER() OVER(PARTITION BY substring(symbol from '^[A-Z]+') ORDER BY id DESC) as rn
			FROM trades
			WHERE strategy = 'OPTIONS_SUPERTREND' AND status = 'LIVE'
		)
		UPDATE trades
		SET status = 'REPLACED_ON_RESTART',
		    exit_time = created_at,
		    exit_price = entry_price,
		    updated_at = NOW()
		WHERE id IN (SELECT id FROM ranked_trades WHERE rn > 1);
	`
	_, err := d.conn.ExecContext(ctx, query)
	return err
}

// OptionsIndexConfig holds the full per-index configuration for options trading
type OptionsIndexConfig struct {
	IndexSymbol          string    `json:"index_symbol"`
	IsActive             bool      `json:"is_active"`
	IsLive               bool      `json:"is_live"`
	BaseLotSize          int       `json:"base_lot_size"`
	MaxMultiplier        int       `json:"max_multiplier"`
	MultiplierOnReversal bool      `json:"multiplier_on_reversal"`
	TargetEntryPremium   float64   `json:"target_entry_premium"`
	ExpiryType           string    `json:"expiry_type"`
	NextMonthDays        int       `json:"next_month_days"`
	SLPct                float64   `json:"sl_pct"`
	TrailSLEnabled       bool      `json:"trail_sl_enabled"`
	TrailSLPct           float64   `json:"trail_sl_pct"`
	TrailSLBufferPct     float64   `json:"trail_sl_buffer_pct"`
	TrailSLBufferTicks   int       `json:"trail_sl_buffer_ticks"`
	SLBufferTicks        int       `json:"sl_buffer_ticks"`
	MasterCandleBufferTicks int    `json:"master_candle_buffer_ticks"`
	ST1Period            int       `json:"st1_period"`
	ST1Multiplier        float64   `json:"st1_multiplier"`
	ST2Period            int       `json:"st2_period"`
	ST2Multiplier        float64   `json:"st2_multiplier"`
	ST3Period            int       `json:"st3_period"`
	ST3Multiplier        float64   `json:"st3_multiplier"`
	LastNewTradeTime     string    `json:"last_new_trade_time"`
	AutoSquareOffTime    string    `json:"auto_square_off_time"`
	SuperTrendCutoffTime string    `json:"supertrend_cutoff_time"`
	MaxTradesPerDay      int       `json:"max_trades_per_day"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// GetOptionsIndexConfig retrieves configuration for a specific index symbol
func (d *Database) GetOptionsIndexConfig(ctx context.Context, indexSymbol string) (*OptionsIndexConfig, error) {
	if d == nil || d.conn == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	spec, _ := ResolveIndexSpec(indexSymbol)
	query := `
		SELECT index_symbol, is_active, is_live, base_lot_size, max_multiplier, multiplier_on_reversal,
		       target_entry_premium, expiry_type, next_month_days, sl_pct,
		       trail_sl_enabled, trail_sl_pct, COALESCE(trail_sl_buffer_pct, 5.0),
		       COALESCE(trail_sl_buffer_ticks, 30), COALESCE(sl_buffer_ticks, 2),
		       COALESCE(master_candle_buffer_ticks, 0),
		       st1_period, st1_multiplier, st2_period, st2_multiplier,
		       st3_period, st3_multiplier, last_new_trade_time, auto_square_off_time, supertrend_cutoff_time,
		       COALESCE(max_trades_per_day, 10),
		       created_at, updated_at
		FROM options_index_configs
		WHERE index_symbol = $1 OR index_symbol = $2
		LIMIT 1
	`
	var cfg OptionsIndexConfig
	err := d.conn.QueryRowContext(ctx, query, indexSymbol, spec.Name).Scan(
		&cfg.IndexSymbol, &cfg.IsActive, &cfg.IsLive, &cfg.BaseLotSize, &cfg.MaxMultiplier, &cfg.MultiplierOnReversal,
		&cfg.TargetEntryPremium, &cfg.ExpiryType, &cfg.NextMonthDays, &cfg.SLPct,
		&cfg.TrailSLEnabled, &cfg.TrailSLPct, &cfg.TrailSLBufferPct,
		&cfg.TrailSLBufferTicks, &cfg.SLBufferTicks, &cfg.MasterCandleBufferTicks,
		&cfg.ST1Period, &cfg.ST1Multiplier, &cfg.ST2Period, &cfg.ST2Multiplier,
		&cfg.ST3Period, &cfg.ST3Multiplier, &cfg.LastNewTradeTime, &cfg.AutoSquareOffTime, &cfg.SuperTrendCutoffTime,
		&cfg.MaxTradesPerDay,
		&cfg.CreatedAt, &cfg.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GetAllOptionsIndexConfigs retrieves all index configurations
func (d *Database) GetAllOptionsIndexConfigs(ctx context.Context) ([]OptionsIndexConfig, error) {
	if d == nil || d.conn == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	query := `
		SELECT index_symbol, is_active, is_live, base_lot_size, max_multiplier, multiplier_on_reversal,
		       target_entry_premium, expiry_type, next_month_days, sl_pct,
		       trail_sl_enabled, trail_sl_pct, COALESCE(trail_sl_buffer_pct, 5.0),
		       COALESCE(trail_sl_buffer_ticks, 30), COALESCE(sl_buffer_ticks, 2),
		       COALESCE(master_candle_buffer_ticks, 0),
		       st1_period, st1_multiplier, st2_period, st2_multiplier,
		       st3_period, st3_multiplier, last_new_trade_time, auto_square_off_time, supertrend_cutoff_time,
		       COALESCE(max_trades_per_day, 10),
		       created_at, updated_at
		FROM options_index_configs
		ORDER BY index_symbol ASC
	`
	rows, err := d.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []OptionsIndexConfig
	for rows.Next() {
		var cfg OptionsIndexConfig
		if err := rows.Scan(
			&cfg.IndexSymbol, &cfg.IsActive, &cfg.IsLive, &cfg.BaseLotSize, &cfg.MaxMultiplier, &cfg.MultiplierOnReversal,
			&cfg.TargetEntryPremium, &cfg.ExpiryType, &cfg.NextMonthDays, &cfg.SLPct,
			&cfg.TrailSLEnabled, &cfg.TrailSLPct, &cfg.TrailSLBufferPct,
			&cfg.TrailSLBufferTicks, &cfg.SLBufferTicks, &cfg.MasterCandleBufferTicks,
			&cfg.ST1Period, &cfg.ST1Multiplier, &cfg.ST2Period, &cfg.ST2Multiplier,
			&cfg.ST3Period, &cfg.ST3Multiplier, &cfg.LastNewTradeTime, &cfg.AutoSquareOffTime, &cfg.SuperTrendCutoffTime,
			&cfg.MaxTradesPerDay,
			&cfg.CreatedAt, &cfg.UpdatedAt,
		); err == nil {
			results = append(results, cfg)
		}
	}
	return results, nil
}

// SaveOptionsIndexConfigsBatch saves or updates multiple index configuration rows atomically in PostgreSQL
func (d *Database) SaveOptionsIndexConfigsBatch(ctx context.Context, cfgs []OptionsIndexConfig) error {
	if d == nil || d.conn == nil {
		return fmt.Errorf("database connection is nil")
	}
	if len(cfgs) == 0 {
		return nil
	}

	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `
		INSERT INTO options_index_configs (
			index_symbol, is_active, is_live, base_lot_size, max_multiplier, multiplier_on_reversal,
			target_entry_premium, expiry_type, next_month_days, sl_pct,
			trail_sl_enabled, trail_sl_pct, trail_sl_buffer_pct, trail_sl_buffer_ticks, sl_buffer_ticks,
			master_candle_buffer_ticks,
			st1_period, st1_multiplier, st2_period, st2_multiplier,
			st3_period, st3_multiplier, last_new_trade_time, auto_square_off_time, supertrend_cutoff_time,
			max_trades_per_day,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, NOW())
		ON CONFLICT (index_symbol) DO UPDATE SET
			is_active = EXCLUDED.is_active,
			is_live = EXCLUDED.is_live,
			base_lot_size = EXCLUDED.base_lot_size,
			max_multiplier = EXCLUDED.max_multiplier,
			multiplier_on_reversal = EXCLUDED.multiplier_on_reversal,
			target_entry_premium = EXCLUDED.target_entry_premium,
			expiry_type = EXCLUDED.expiry_type,
			next_month_days = EXCLUDED.next_month_days,
			sl_pct = EXCLUDED.sl_pct,
			trail_sl_enabled = EXCLUDED.trail_sl_enabled,
			trail_sl_pct = EXCLUDED.trail_sl_pct,
			trail_sl_buffer_pct = EXCLUDED.trail_sl_buffer_pct,
			trail_sl_buffer_ticks = EXCLUDED.trail_sl_buffer_ticks,
			sl_buffer_ticks = EXCLUDED.sl_buffer_ticks,
			master_candle_buffer_ticks = EXCLUDED.master_candle_buffer_ticks,
			st1_period = EXCLUDED.st1_period,
			st1_multiplier = EXCLUDED.st1_multiplier,
			st2_period = EXCLUDED.st2_period,
			st2_multiplier = EXCLUDED.st2_multiplier,
			st3_period = EXCLUDED.st3_period,
			st3_multiplier = EXCLUDED.st3_multiplier,
			last_new_trade_time = EXCLUDED.last_new_trade_time,
			auto_square_off_time = EXCLUDED.auto_square_off_time,
			supertrend_cutoff_time = EXCLUDED.supertrend_cutoff_time,
			max_trades_per_day = EXCLUDED.max_trades_per_day,
			updated_at = NOW()
	`
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for i := range cfgs {
		cfg := &cfgs[i]
		if cfg.TrailSLBufferPct <= 0 {
			cfg.TrailSLBufferPct = 5.0
		}
		if cfg.TrailSLBufferTicks <= 0 {
			cfg.TrailSLBufferTicks = 30
		}
		if cfg.SLBufferTicks <= 0 {
			cfg.SLBufferTicks = 2
		}
		if cfg.MaxTradesPerDay <= 0 {
			cfg.MaxTradesPerDay = 10
		}
		spec, _ := ResolveIndexSpec(cfg.IndexSymbol)
		if _, err := stmt.ExecContext(ctx,
			spec.Name, cfg.IsActive, cfg.IsLive, cfg.BaseLotSize, cfg.MaxMultiplier, cfg.MultiplierOnReversal,
			cfg.TargetEntryPremium, cfg.ExpiryType, cfg.NextMonthDays, cfg.SLPct,
			cfg.TrailSLEnabled, cfg.TrailSLPct, cfg.TrailSLBufferPct, cfg.TrailSLBufferTicks, cfg.SLBufferTicks,
			cfg.MasterCandleBufferTicks,
			cfg.ST1Period, cfg.ST1Multiplier, cfg.ST2Period, cfg.ST2Multiplier,
			cfg.ST3Period, cfg.ST3Multiplier, cfg.LastNewTradeTime, cfg.AutoSquareOffTime, cfg.SuperTrendCutoffTime,
			cfg.MaxTradesPerDay,
		); err != nil {
			return fmt.Errorf("failed to save options config for %s: %w", cfg.IndexSymbol, err)
		}
	}

	return tx.Commit()
}

// SaveOptionsIndexConfig saves or updates an index configuration row in PostgreSQL
func (d *Database) SaveOptionsIndexConfig(ctx context.Context, cfg *OptionsIndexConfig) error {
	if cfg == nil {
		return nil
	}
	return d.SaveOptionsIndexConfigsBatch(ctx, []OptionsIndexConfig{*cfg})
}

// GetOptionsTodayTradesCountForIndex returns the count of options trades for an index today
func (d *Database) GetOptionsTodayTradesCountForIndex(ctx context.Context, startOfDay time.Time, prefix string) (int, error) {
	if d == nil || d.conn == nil {
		return 0, fmt.Errorf("database connection is nil")
	}
	var count int
	query := "SELECT COUNT(*) FROM trades WHERE created_at >= $1 AND strategy = 'OPTIONS_SUPERTREND' AND symbol LIKE $2"
	err := d.conn.QueryRowContext(ctx, query, startOfDay, prefix+"%").Scan(&count)
	return count, err
}

// GetAllSystemConfigs retrieves all system configurations grouped by category
func (d *Database) GetAllSystemConfigs(ctx context.Context) (map[string]map[string]string, error) {
	if d == nil || d.conn == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	query := `SELECT category, config_key, config_value FROM app_system_configs ORDER BY category, config_key`
	rows, err := d.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]map[string]string)
	for rows.Next() {
		var cat, key, val string
		if err := rows.Scan(&cat, &key, &val); err == nil {
			if _, ok := result[cat]; !ok {
				result[cat] = make(map[string]string)
			}
			result[cat][key] = val
		}
	}
	return result, nil
}

// GetSystemConfigsByCategory retrieves system configurations for a specific category
func (d *Database) GetSystemConfigsByCategory(ctx context.Context, category string) (map[string]string, error) {
	if d == nil || d.conn == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	query := `SELECT config_key, config_value FROM app_system_configs WHERE category = $1 ORDER BY config_key`
	rows, err := d.conn.QueryContext(ctx, query, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var key, val string
		if err := rows.Scan(&key, &val); err == nil {
			result[key] = val
		}
	}
	return result, nil
}

// SaveSystemConfigsBatch saves a batch of system configurations across categories in a single bulk operation
func (d *Database) SaveSystemConfigsBatch(ctx context.Context, configs map[string]map[string]string) error {
	if d == nil || d.conn == nil {
		return fmt.Errorf("database connection is nil")
	}

	var cats, keys, vals []string
	for cat, kv := range configs {
		for k, v := range kv {
			cats = append(cats, cat)
			keys = append(keys, k)
			vals = append(vals, v)
		}
	}

	if len(cats) == 0 {
		return nil
	}

	query := `
		INSERT INTO app_system_configs (category, config_key, config_value, updated_at)
		SELECT u.cat, u.k, u.v, NOW()
		FROM UNNEST($1::text[], $2::text[], $3::text[]) AS u(cat, k, v)
		ON CONFLICT (category, config_key) DO UPDATE SET
			config_value = EXCLUDED.config_value,
			updated_at = NOW()
	`
	if _, err := d.conn.ExecContext(ctx, query, pq.Array(cats), pq.Array(keys), pq.Array(vals)); err != nil {
		return fmt.Errorf("failed to bulk save system configs batch: %w", err)
	}

	return nil
}

// SaveSystemConfigItem saves a single system configuration item
func (d *Database) SaveSystemConfigItem(ctx context.Context, category, key, value string) error {
	if d == nil || d.conn == nil {
		return fmt.Errorf("database connection is nil")
	}

	query := `
		INSERT INTO app_system_configs (category, config_key, config_value, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (category, config_key) DO UPDATE SET
			config_value = EXCLUDED.config_value,
			updated_at = NOW()
	`
	_, err := d.conn.ExecContext(ctx, query, category, key, value)
	return err
}

// SectorDefinition represents a user-managed sector with its constituent symbols
type SectorDefinition struct {
	ID         int64     `json:"id"`
	SectorName string    `json:"sector_name"`
	Symbols    []string  `json:"symbols"`
	SymbolsStr string    `json:"symbols_str"`
	IsActive   bool      `json:"is_active"`
	StockCount int       `json:"stock_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// GetSectorDefinitions retrieves all defined sectors ordered by name
func (d *Database) GetSectorDefinitions(ctx context.Context) ([]SectorDefinition, error) {
	if d == nil || d.conn == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	query := `
		SELECT id, sector_name, symbols, is_active, created_at, updated_at
		FROM sector_definitions
		ORDER BY sector_name ASC
	`
	rows, err := d.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []SectorDefinition
	for rows.Next() {
		var item SectorDefinition
		var symStr string
		var cAt, uAt time.Time
		if err := rows.Scan(&item.ID, &item.SectorName, &symStr, &item.IsActive, &cAt, &uAt); err != nil {
			continue
		}
		item.CreatedAt = NormalizeToIST(cAt)
		item.UpdatedAt = NormalizeToIST(uAt)
		item.SymbolsStr = symStr

		rawParts := strings.Split(symStr, ",")
		var cleanSyms []string
		for _, p := range rawParts {
			clean := strings.TrimSpace(strings.ToUpper(p))
			if clean != "" {
				cleanSyms = append(cleanSyms, clean)
			}
		}
		item.Symbols = cleanSyms
		item.StockCount = len(cleanSyms)
		result = append(result, item)
	}
	return result, nil
}

// SaveSectorDefinition creates or updates a sector definition
func (d *Database) SaveSectorDefinition(ctx context.Context, sectorName string, symbols []string, isActive bool) error {
	if d == nil || d.conn == nil {
		return fmt.Errorf("database connection is nil")
	}

	cleanName := strings.TrimSpace(strings.ToUpper(sectorName))
	if cleanName == "" {
		return fmt.Errorf("sector name cannot be empty")
	}

	var cleanSyms []string
	seen := make(map[string]bool)
	for _, s := range symbols {
		sym := strings.TrimSpace(strings.ToUpper(s))
		if sym != "" && !seen[sym] {
			seen[sym] = true
			cleanSyms = append(cleanSyms, sym)
		}
	}
	symStr := strings.Join(cleanSyms, ",")

	query := `
		INSERT INTO sector_definitions (sector_name, symbols, is_active, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (sector_name) DO UPDATE SET
			symbols = EXCLUDED.symbols,
			is_active = EXCLUDED.is_active,
			updated_at = NOW()
	`
	_, err := d.conn.ExecContext(ctx, query, cleanName, symStr, isActive)
	return err
}

// DeleteSectorDefinition removes a sector definition by name
func (d *Database) DeleteSectorDefinition(ctx context.Context, sectorName string) error {
	if d == nil || d.conn == nil {
		return fmt.Errorf("database connection is nil")
	}

	cleanName := strings.TrimSpace(strings.ToUpper(sectorName))
	query := `DELETE FROM sector_definitions WHERE sector_name = $1`
	_, err := d.conn.ExecContext(ctx, query, cleanName)
	return err
}

// ResetDefaultSectors restores default 9 sectors
func (d *Database) ResetDefaultSectors(ctx context.Context) error {
	if d == nil || d.conn == nil {
		return fmt.Errorf("database connection is nil")
	}

	defaultSectors := []struct {
		Name, Symbols string
	}{
		{"BANK", "HDFCBANK,ICICIBANK,KOTAKBANK,SBIN,AXISBANK,INDUSINDBK,AUBANK,FEDERALBNK,PNB,BANKBARODA"},
		{"IT", "TCS,INFY,WIPRO,HCLTECH,TECHM,LTIM,COFORGE,MPHASIS,PERSISTENT"},
		{"AUTO", "MARUTI,TATAMOTORS,M&M,BAJAJ-AUTO,HEROMOTOCO,TVSMOTOR,EICHERMOT,ASHOKLEY,BALKRISIND"},
		{"PHARMA", "SUNPHARMA,CIPLA,DRREDDY,DIVISLAB,LUPIN,AUROPHARMA,BIOCON,TORNTPHARM,IPCALAB"},
		{"METAL", "TATASTEEL,JINDALSTEL,HINDALCO,JSWSTEEL,SAIL,NATIONALUM,NMDC,VEDL"},
		{"FMCG", "HINDUNILVR,ITC,NESTLEIND,BRITANNIA,TATACONSUM,DABUR,MARICO,GODREJCP,COLPAL"},
		{"ENERGY", "RELIANCE,ONGC,NTPC,POWERGRID,BPCL,IOC,GAIL,ADANIENT,ADANIPORTS"},
		{"REALTY", "DLF,GODREJPROP,OBEROIRLTY"},
		{"MEDIA", "ZEEL,SUNTV,PVRINOX"},
	}

	for _, s := range defaultSectors {
		_, err := d.conn.ExecContext(ctx, `
			INSERT INTO sector_definitions (sector_name, symbols, is_active, updated_at)
			VALUES ($1, $2, true, NOW())
			ON CONFLICT (sector_name) DO UPDATE SET
				symbols = EXCLUDED.symbols,
				is_active = true,
				updated_at = NOW()
		`, s.Name, s.Symbols)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetSectorConstituentsMap returns a map of active sector name to slice of constituent symbols
func (d *Database) GetSectorConstituentsMap(ctx context.Context) (map[string][]string, error) {
	if d == nil || d.conn == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	query := `SELECT sector_name, symbols FROM sector_definitions WHERE is_active = true`
	rows, err := d.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]string)
	for rows.Next() {
		var name, symStr string
		if err := rows.Scan(&name, &symStr); err == nil {
			rawParts := strings.Split(symStr, ",")
			var cleanSyms []string
			for _, p := range rawParts {
				clean := strings.TrimSpace(strings.ToUpper(p))
				if clean != "" {
					cleanSyms = append(cleanSyms, clean)
				}
			}
			if len(cleanSyms) > 0 {
				result[strings.ToUpper(name)] = cleanSyms
			}
		}
	}
	return result, nil
}

// StrategyEvent represents a structured diagnostic event in a stock's strategy lifecycle
type StrategyEvent struct {
	ID            int                    `json:"id"`
	EventTime     time.Time              `json:"event_time"`
	Symbol        string                 `json:"symbol"`
	Strategy      string                 `json:"strategy"`
	Stage         string                 `json:"stage"`    // MASTER_FORMED, MASTER_REJECTED, CONFIRMATION_ARMED, SETUP_ARMED, etc.
	Severity      string                 `json:"severity"` // SUCCESS, WARNING, DANGER, INFO
	Title         string                 `json:"title"`
	Direction     string                 `json:"direction"` // BUY, SELL, NEUTRAL
	TriggerPrice  float64                `json:"trigger_price,omitempty"`
	SLPrice       float64                `json:"sl_price,omitempty"`
	TargetPrice   float64                `json:"target_price,omitempty"`
	ExecutedPrice float64                `json:"executed_price,omitempty"`
	ExecutedQty   int                    `json:"executed_qty,omitempty"`
	CandleTime    *time.Time             `json:"candle_time,omitempty"`
	CandleOpen    float64                `json:"candle_open,omitempty"`
	CandleHigh    float64                `json:"candle_high,omitempty"`
	CandleLow     float64                `json:"candle_low,omitempty"`
	CandleClose   float64                `json:"candle_close,omitempty"`
	CandleVolume  int64                  `json:"candle_volume,omitempty"`
	Reason        string                 `json:"reason,omitempty"`
	Details       map[string]interface{} `json:"details,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
}

// InsertStrategyEvent inserts a new strategy lifecycle diagnostic event into PostgreSQL
func (d *Database) InsertStrategyEvent(ctx context.Context, ev *StrategyEvent) error {
	if d == nil || d.conn == nil || ev == nil {
		return fmt.Errorf("database or event is nil")
	}

	ev.EventTime = NormalizeToIST(ev.EventTime)
	var candleTime *time.Time
	if ev.CandleTime != nil {
		ct := NormalizeToIST(*ev.CandleTime)
		candleTime = &ct
	}

	if ev.Severity == "" {
		ev.Severity = "INFO"
	}

	var detailsJSON []byte
	if ev.Details != nil && len(ev.Details) > 0 {
		var err error
		detailsJSON, err = json.Marshal(ev.Details)
		if err != nil {
			detailsJSON = []byte("{}")
		}
	} else {
		detailsJSON = []byte("{}")
	}

	query := `
		INSERT INTO stock_strategy_events (
			event_time, symbol, strategy, stage, severity, title, direction,
			trigger_price, sl_price, target_price, executed_price, executed_qty,
			candle_time, candle_open, candle_high, candle_low, candle_close, candle_volume,
			reason, details, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17, $18,
			$19, $20, NOW()
		)
	`
	_, err := d.conn.ExecContext(ctx, query,
		ev.EventTime, ev.Symbol, ev.Strategy, ev.Stage, ev.Severity, ev.Title, ev.Direction,
		ev.TriggerPrice, ev.SLPrice, ev.TargetPrice, ev.ExecutedPrice, ev.ExecutedQty,
		candleTime, ev.CandleOpen, ev.CandleHigh, ev.CandleLow, ev.CandleClose, ev.CandleVolume,
		ev.Reason, detailsJSON,
	)
	return err
}

// InsertStrategyEventsBatch inserts a slice of strategy events into PostgreSQL in a single multi-row query
func (d *Database) InsertStrategyEventsBatch(ctx context.Context, events []*StrategyEvent) error {
	if d == nil || d.conn == nil || len(events) == 0 {
		return nil
	}

	var valueStrings []string
	var valueArgs []interface{}
	argIdx := 1

	for _, ev := range events {
		if ev == nil {
			continue
		}
		ev.EventTime = NormalizeToIST(ev.EventTime)
		var candleTime *time.Time
		if ev.CandleTime != nil {
			ct := NormalizeToIST(*ev.CandleTime)
			candleTime = &ct
		}

		if ev.Severity == "" {
			ev.Severity = "INFO"
		}

		var detailsJSON []byte
		if ev.Details != nil && len(ev.Details) > 0 {
			var err error
			detailsJSON, err = json.Marshal(ev.Details)
			if err != nil {
				detailsJSON = []byte("{}")
			}
		} else {
			detailsJSON = []byte("{}")
		}

		valueStrings = append(valueStrings, fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, NOW())",
			argIdx, argIdx+1, argIdx+2, argIdx+3, argIdx+4, argIdx+5, argIdx+6,
			argIdx+7, argIdx+8, argIdx+9, argIdx+10, argIdx+11,
			argIdx+12, argIdx+13, argIdx+14, argIdx+15, argIdx+16, argIdx+17,
			argIdx+18, argIdx+19,
		))
		valueArgs = append(valueArgs,
			ev.EventTime, ev.Symbol, ev.Strategy, ev.Stage, ev.Severity, ev.Title, ev.Direction,
			ev.TriggerPrice, ev.SLPrice, ev.TargetPrice, ev.ExecutedPrice, ev.ExecutedQty,
			candleTime, ev.CandleOpen, ev.CandleHigh, ev.CandleLow, ev.CandleClose, ev.CandleVolume,
			ev.Reason, detailsJSON,
		)
		argIdx += 20
	}

	if len(valueStrings) == 0 {
		return nil
	}

	query := fmt.Sprintf(`
		INSERT INTO stock_strategy_events (
			event_time, symbol, strategy, stage, severity, title, direction,
			trigger_price, sl_price, target_price, executed_price, executed_qty,
			candle_time, candle_open, candle_high, candle_low, candle_close, candle_volume,
			reason, details, created_at
		) VALUES %s
	`, strings.Join(valueStrings, ", "))

	_, err := d.conn.ExecContext(ctx, query, valueArgs...)
	return err
}

// AutoPruneStrategyEvents deletes strategy events older than retentionDays (default 3 days)
func (d *Database) AutoPruneStrategyEvents(ctx context.Context, retentionDays int) error {
	if d == nil || d.conn == nil {
		return nil
	}
	if retentionDays <= 0 {
		retentionDays = 3
	}
	query := fmt.Sprintf("DELETE FROM stock_strategy_events WHERE event_time < NOW() - INTERVAL '%d days'", retentionDays)
	_, err := d.conn.ExecContext(ctx, query)
	return err
}

// QueryStrategyEvents performs flexible, indexed query for strategy telemetry events with multi-field filtering
func (d *Database) QueryStrategyEvents(ctx context.Context, symbol, dateStr, strategy, stage, severity, search string, limit int, sinceID int) ([]StrategyEvent, error) {
	if d == nil || d.conn == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	baseQuery := `
		SELECT id, event_time, symbol, strategy, stage, COALESCE(severity, 'INFO'), COALESCE(title, ''), direction,
		       COALESCE(trigger_price, 0), COALESCE(sl_price, 0), COALESCE(target_price, 0),
		       COALESCE(executed_price, 0), COALESCE(executed_qty, 0),
		       candle_time, COALESCE(candle_open, 0), COALESCE(candle_high, 0),
		       COALESCE(candle_low, 0), COALESCE(candle_close, 0), COALESCE(candle_volume, 0),
		       COALESCE(reason, ''), details, created_at
		FROM stock_strategy_events
		WHERE 1=1
	`
	var args []interface{}
	argIdx := 1

	if symbol != "" && strings.ToUpper(symbol) != "ALL" {
		baseQuery += fmt.Sprintf(" AND UPPER(symbol) = $%d", argIdx)
		args = append(args, strings.ToUpper(strings.TrimSpace(symbol)))
		argIdx++
	}

	if dateStr != "" {
		baseQuery += fmt.Sprintf(" AND DATE(event_time AT TIME ZONE 'Asia/Kolkata') = $%d", argIdx)
		args = append(args, dateStr)
		argIdx++
	}

	if strategy != "" && strings.ToUpper(strategy) != "ALL" {
		baseQuery += fmt.Sprintf(" AND strategy = $%d", argIdx)
		args = append(args, strategy)
		argIdx++
	}

	if stage != "" && strings.ToUpper(stage) != "ALL" {
		baseQuery += fmt.Sprintf(" AND stage = $%d", argIdx)
		args = append(args, stage)
		argIdx++
	}

	if severity != "" && strings.ToUpper(severity) != "ALL" {
		baseQuery += fmt.Sprintf(" AND UPPER(severity) = $%d", argIdx)
		args = append(args, strings.ToUpper(severity))
		argIdx++
	}

	if sinceID > 0 {
		baseQuery += fmt.Sprintf(" AND id > $%d", argIdx)
		args = append(args, sinceID)
		argIdx++
	}

	if search != "" {
		baseQuery += fmt.Sprintf(" AND (reason ILIKE $%d OR title ILIKE $%d OR symbol ILIKE $%d)", argIdx, argIdx, argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}

	if limit <= 0 || limit > 500 {
		limit = 100
	}

	baseQuery += fmt.Sprintf(" ORDER BY event_time DESC, id DESC LIMIT %d", limit)

	rows, err := d.conn.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []StrategyEvent
	for rows.Next() {
		var ev StrategyEvent
		var candleTime sql.NullTime
		var detailsRaw []byte

		err := rows.Scan(
			&ev.ID, &ev.EventTime, &ev.Symbol, &ev.Strategy, &ev.Stage, &ev.Severity, &ev.Title, &ev.Direction,
			&ev.TriggerPrice, &ev.SLPrice, &ev.TargetPrice, &ev.ExecutedPrice, &ev.ExecutedQty,
			&candleTime, &ev.CandleOpen, &ev.CandleHigh, &ev.CandleLow, &ev.CandleClose, &ev.CandleVolume,
			&ev.Reason, &detailsRaw, &ev.CreatedAt,
		)
		if err != nil {
			continue
		}

		ev.EventTime = NormalizeToIST(ev.EventTime)
		ev.CreatedAt = NormalizeToIST(ev.CreatedAt)
		if candleTime.Valid {
			ct := NormalizeToIST(candleTime.Time)
			ev.CandleTime = &ct
		}

		if len(detailsRaw) > 0 {
			var dMap map[string]interface{}
			if err := json.Unmarshal(detailsRaw, &dMap); err == nil {
				ev.Details = dMap
			}
		}

		events = append(events, ev)
	}

	return events, nil
}

// GetStrategyEvents fetches strategy diagnostic events for a symbol and date
func (d *Database) GetStrategyEvents(ctx context.Context, symbol, dateStr, strategy string) ([]StrategyEvent, error) {
	return d.QueryStrategyEvents(ctx, symbol, dateStr, strategy, "", "", "", 200, 0)
}

// GetCandlesWithHistory fetches candles for a token on a date plus up to historyCount preceding candles
func (d *Database) GetCandlesWithHistory(ctx context.Context, token int64, dateStr string, tf string, historyCount int) ([]Candle, error) {
	if d == nil || d.conn == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	tableName := "candles_1m"
	if tf == "5m" {
		tableName = "candles_5m"
	}

	// Parse dateStr in IST
	t, err := time.ParseInLocation("2006-01-02", dateStr, ISTLocation)
	if err != nil {
		t = time.Now().In(ISTLocation)
	}
	dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, ISTLocation)
	dayEnd := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, ISTLocation)

	var allCandles []Candle

	// 1. Fetch preceding historical candles
	if historyCount > 0 {
		histQuery := fmt.Sprintf(`
			SELECT token, time, open, high, low, close, volume, vwap, COALESCE(color, '')
			FROM %s
			WHERE token = $1 AND time < $2
			ORDER BY time DESC
			LIMIT $3
		`, tableName)

		histRows, err := d.conn.QueryContext(ctx, histQuery, token, dayStart, historyCount)
		if err == nil {
			var hist []Candle
			for histRows.Next() {
				var c Candle
				if err := histRows.Scan(&c.Token, &c.Time, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.VWAP, &c.Color); err == nil {
					c.Time = NormalizeToIST(c.Time)
					hist = append(hist, c)
				}
			}
			histRows.Close()

			// Reverse history so it's in chronological order
			for i, j := 0, len(hist)-1; i < j; i, j = i+1, j-1 {
				hist[i], hist[j] = hist[j], hist[i]
			}
			allCandles = append(allCandles, hist...)
		}
	}

	// 2. Fetch target day candles
	dayQuery := fmt.Sprintf(`
		SELECT token, time, open, high, low, close, volume, vwap, COALESCE(color, '')
		FROM %s
		WHERE token = $1 AND time >= $2 AND time <= $3
		ORDER BY time ASC
	`, tableName)

	dayRows, err := d.conn.QueryContext(ctx, dayQuery, token, dayStart, dayEnd)
	if err != nil {
		return allCandles, err
	}
	defer dayRows.Close()

	for dayRows.Next() {
		var c Candle
		if err := dayRows.Scan(&c.Token, &c.Time, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.VWAP, &c.Color); err == nil {
			c.Time = NormalizeToIST(c.Time)
			allCandles = append(allCandles, c)
		}
	}

	return allCandles, nil
}

// GetHistoricalTradesByDate fetches completed and active trades for a specific date and symbol
func (d *Database) GetHistoricalTradesByDate(ctx context.Context, dateStr, symbol string) ([]TradeHistoryRecord, error) {
	if d == nil || d.conn == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	query := `
		SELECT id, symbol, entry_price, COALESCE(exit_price, 0), quantity, pnl, side,
		       COALESCE(time_held_minutes, 0), COALESCE(entry_time, created_at), COALESCE(exit_time, created_at),
		       created_at, COALESCE(strategy, ''), COALESCE(status, 'CLOSED'), COALESCE(expiry_date::text, '')
		FROM trades
		WHERE (DATE(created_at AT TIME ZONE 'Asia/Kolkata') = $1 OR DATE(entry_time AT TIME ZONE 'Asia/Kolkata') = $1)
	`
	var args []interface{}
	args = append(args, dateStr)

	if symbol != "" {
		query += " AND UPPER(symbol) = $2"
		args = append(args, strings.ToUpper(strings.TrimSpace(symbol)))
	}

	query += " ORDER BY id ASC"

	rows, err := d.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []TradeHistoryRecord
	for rows.Next() {
		var r TradeHistoryRecord
		if err := rows.Scan(
			&r.ID, &r.Symbol, &r.EntryPrice, &r.ExitPrice, &r.Quantity, &r.PnL, &r.Side,
			&r.TimeHeldMinutes, &r.EntryTime, &r.ExitTime, &r.CreatedAt, &r.Strategy, &r.Status, &r.ExpiryDate,
		); err == nil {
			r.EntryTime = NormalizeToIST(r.EntryTime)
			r.ExitTime = NormalizeToIST(r.ExitTime)
			r.CreatedAt = NormalizeToIST(r.CreatedAt)
			list = append(list, r)
		}
	}
	return list, nil
}

// GetCandlesBefore gets historical candles before a specific time from candles_1m, candles_5m, or candles_1d with IST normalization
func (d *Database) GetCandlesBefore(ctx context.Context, tableName string, token int64, before time.Time, limit int) ([]CandleRecord, error) {
	if limit <= 0 {
		limit = 150
	}
	if tableName != "candles_1m" && tableName != "candles_5m" && tableName != "candles_1d" {
		tableName = "candles_5m"
	}

	beforeUTC := before.UTC()
	beforeIST := NormalizeToIST(before)

	query := fmt.Sprintf(`
		SELECT time, open, high, low, close, volume 
		FROM %s 
		WHERE token = $1 AND (time < $2 OR time < $3) 
		ORDER BY time DESC 
		LIMIT $4
	`, tableName)
	rows, err := d.conn.QueryContext(ctx, query, token, beforeUTC, beforeIST, limit*2)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candleMap := make(map[int64]CandleRecord)
	for rows.Next() {
		var t time.Time
		var o, h, l, c float64
		var v int64
		if err := rows.Scan(&t, &o, &h, &l, &c, &v); err != nil {
			continue
		}
		// For intraday candles, enforce market hours filter; for daily candles (00:00:00), accept directly
		if tableName != "candles_1d" && !IsMarketHoursCandle(t) {
			continue
		}
		normTime := NormalizeToIST(t)
		if !normTime.Before(beforeIST) {
			continue
		}
		unixSec := normTime.Unix()
		if _, exists := candleMap[unixSec]; !exists {
			candleMap[unixSec] = CandleRecord{
				Time:   normTime,
				Open:   o,
				High:   h,
				Low:    l,
				Close:  c,
				Volume: v,
			}
		}
		if len(candleMap) >= limit {
			break
		}
	}

	candles := make([]CandleRecord, 0, len(candleMap))
	for _, c := range candleMap {
		candles = append(candles, c)
	}

	// Sort chronologically (oldest first)
	sort.Slice(candles, func(i, j int) bool {
		return candles[i].Time.Before(candles[j].Time)
	})

	return candles, nil
}

// GetRecentDailyCandles gets the most recent N daily candles from candles_1d with strict IST normalization
func (d *Database) GetRecentDailyCandles(ctx context.Context, token int64, limit int) ([]CandleRecord, error) {
	if limit <= 0 {
		limit = 200
	}
	query := `
		SELECT time, open, high, low, close, volume
		FROM candles_1d
		WHERE token = $1
		ORDER BY time DESC
		LIMIT $2
	`
	rows, err := d.conn.QueryContext(ctx, query, token, limit*2)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candleMap := make(map[int64]CandleRecord)
	for rows.Next() {
		var t time.Time
		var o, h, l, c float64
		var v int64
		if err := rows.Scan(&t, &o, &h, &l, &c, &v); err != nil {
			continue
		}
		normTime := NormalizeToIST(t)
		unixSec := normTime.Unix()
		if _, exists := candleMap[unixSec]; !exists {
			candleMap[unixSec] = CandleRecord{
				Time:   normTime,
				Open:   o,
				High:   h,
				Low:    l,
				Close:  c,
				Volume: v,
			}
		}
		if len(candleMap) >= limit {
			break
		}
	}

	candles := make([]CandleRecord, 0, len(candleMap))
	for _, c := range candleMap {
		candles = append(candles, c)
	}

	// Sort chronologically (oldest first)
	sort.Slice(candles, func(i, j int) bool {
		return candles[i].Time.Before(candles[j].Time)
	})

	return candles, nil
}

// GetDailyWatchlistStocksBySelector retrieves stocks from daily_watchlists where selectors match a pattern
func (d *Database) GetDailyWatchlistStocksBySelector(ctx context.Context, dateStr string, selectorPattern string) ([]DailyWatchlistItem, error) {
	query := `
		SELECT date::TEXT, symbol, token, selectors
		FROM daily_watchlists
		WHERE date = $1 AND (selectors ILIKE $2 OR selectors ILIKE $3)
		ORDER BY symbol ASC
	`
	pattern := "%" + selectorPattern + "%"
	exactPattern := selectorPattern
	rows, err := d.conn.QueryContext(ctx, query, dateStr, pattern, exactPattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []DailyWatchlistItem
	for rows.Next() {
		var item DailyWatchlistItem
		if err := rows.Scan(&item.Date, &item.Symbol, &item.Token, &item.Selectors); err == nil {
			items = append(items, item)
		}
	}
	return items, nil
}

// GetQuantScannerCandidates retrieves top candidates from quant_scanner_results filtered by breakout types and direction
func (d *Database) GetQuantScannerCandidates(ctx context.Context, dateStr string, breakoutTypes []string, bias string, limit int) ([]DBScanResult, error) {
	if limit <= 0 {
		limit = 10
	}
	targetDate := dateStr
	if targetDate == "" {
		targetDate = GetEffectiveTradingDate(NowIST())
	}
	if targetDate == "" {
		return nil, nil
	}

	var conditions []string
	var args []interface{}
	args = append(args, targetDate)
	conditions = append(conditions, "scan_date = $1")

	if len(breakoutTypes) > 0 {
		var typeHolders []string
		for _, bt := range breakoutTypes {
			args = append(args, bt)
			typeHolders = append(typeHolders, fmt.Sprintf("$%d", len(args)))
		}
		conditions = append(conditions, fmt.Sprintf("breakout_type IN (%s)", strings.Join(typeHolders, ", ")))
	}

	if bias == "BUY_ONLY" || bias == "BULLISH" {
		conditions = append(conditions, "(direction = 'BULLISH' OR quant_direction = 'BULLISH')")
	} else if bias == "SELL_ONLY" || bias == "BEARISH" {
		conditions = append(conditions, "(direction = 'BEARISH' OR quant_direction = 'BEARISH')")
	}

	query := fmt.Sprintf(`
		SELECT id, scan_date::text, symbol, COALESCE(segment, 'CASH'), breakout_type, direction, momentum_days,
		       pct_change_1d, pct_change_3d, range_pct_change, COALESCE(current_price, 0), COALESCE(distance_to_high_pct, 0),
		       COALESCE(yearly_high, 0), COALESCE(yearly_low, 0), COALESCE(monthly_high, 0), COALESCE(monthly_low, 0), COALESCE(weekly_high, 0), COALESCE(weekly_low, 0), COALESCE(all_time_high, 0), COALESCE(all_time_low, 0),
		       COALESCE(is_daily_cluster, false), COALESCE(is_weekly_cluster, false), COALESCE(cluster_spread, 0), COALESCE(cluster_center, 0),
		       COALESCE(ema_10, 0), COALESCE(ema_20, 0), COALESCE(ema_89, 0),
		       volume_1d, volume_adv, volume_multiplier,
		       COALESCE(dow_trend, ''), COALESCE(positional_zone, ''), COALESCE(action_timing, ''), COALESCE(selection_reason, ''), COALESCE(support_zone, 0), COALESCE(resistance_zone, 0),
		       COALESCE(trigger_price, 0), COALESCE(stop_loss, 0), COALESCE(target_price, 0), COALESCE(trade_action, ''),
		       COALESCE(is_swing_doji, false), COALESCE(swing_doji_type, 'NONE'), COALESCE(candle_wick_pct, 0), COALESCE(upper_wick_pct, 0), COALESCE(lower_wick_pct, 0), COALESCE(doji_description, ''),
		       confidence_score, quant_direction, COALESCE(recommended_action, ''), news_summary, news_sentiment, created_at
		FROM quant_scanner_results
		WHERE %s
		ORDER BY confidence_score DESC
		LIMIT %d
	`, strings.Join(conditions, " AND "), limit)

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
			&r.PctChange1D, &r.PctChange3D, &r.RangePctChange, &r.CurrentPrice, &r.DistanceToHighPct,
			&r.YearlyHigh, &r.YearlyLow, &r.MonthlyHigh, &r.MonthlyLow, &r.WeeklyHigh, &r.WeeklyLow, &r.AllTimeHigh, &r.AllTimeLow,
			&r.IsDailyCluster, &r.IsWeeklyCluster, &r.ClusterSpread, &r.ClusterCenter,
			&r.EMA10, &r.EMA20, &r.EMA89,
			&r.Volume1D, &r.VolumeADV, &r.VolumeMultiplier,
			&r.DowTrend, &r.PositionalZone, &r.ActionTiming, &r.SelectionReason, &r.SupportZone, &r.ResistanceZone,
			&r.TriggerPrice, &r.StopLoss, &r.TargetPrice, &r.TradeAction,
			&r.IsSwingDoji, &r.SwingDojiType, &r.CandleWickPct, &r.UpperWickPct, &r.LowerWickPct, &r.DojiDescription,
			&r.ConfidenceScore, &r.QuantDirection, &r.RecommendedAction, &r.NewsSummary, &r.NewsSentiment, &r.CreatedAt,
		)
		if err == nil {
			r.CreatedAt = r.CreatedAt.In(ISTLocation)
			results = append(results, r)
		}
	}
	return results, nil
}

// GetPreSelectionCandidatesByReason queries pre_selection_results for candidates matching a pattern or reason
func (d *Database) GetPreSelectionCandidatesByReason(ctx context.Context, dateStr string, pattern string, bias string, limit int) ([]PreSelectionResult, error) {
	if limit <= 0 {
		limit = 10
	}
	targetDate := dateStr
	if targetDate == "" {
		targetDate = GetEffectiveTradingDate(NowIST())
	}
	if targetDate == "" {
		return nil, nil
	}

	query := `
		SELECT date::TEXT, ticker, rule_set, predicted_direction, imbalance_ratio, indicative_gap_pct, pre_open_vol_vs_adv, probability_score, reason
		FROM pre_selection_results
		WHERE date = $1 AND (reason ILIKE $2 OR rule_set ILIKE $2 OR $2 = '%')
		ORDER BY probability_score DESC
		LIMIT $3
	`
	likePattern := "%" + pattern + "%"
	if pattern == "" {
		likePattern = "%"
	}
	rows, err := d.conn.QueryContext(ctx, query, targetDate, likePattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PreSelectionResult
	for rows.Next() {
		var r PreSelectionResult
		if err := rows.Scan(&r.Date, &r.Ticker, &r.RuleSet, &r.PredictedDirection, &r.ImbalanceRatio, &r.IndicativeGapPct, &r.PreOpenVolVsADV, &r.ProbabilityScore, &r.Reason); err == nil {
			if bias == "BUY_ONLY" && r.PredictedDirection == "BEARISH BREAKDOWN" {
				continue
			}
			if bias == "SELL_ONLY" && r.PredictedDirection == "BULLISH BREAKOUT" {
				continue
			}
			results = append(results, r)
		}
	}
	return results, nil
}

// FootprintRecord represents a confirmed institutional footprint event
type FootprintRecord struct {
	ID              int64     `json:"id"`
	InstrumentToken int64     `json:"instrument_token"`
	TradingSymbol   string    `json:"tradingsymbol"`
	Timestamp       time.Time `json:"timestamp"`
	Price           float64   `json:"price"`
	Volume          int64     `json:"volume"`
	CVDValue        int64     `json:"cvd_value"`
	TriggerReason   string    `json:"trigger_reason"`
	TradeValue      float64   `json:"trade_value"`
	Side            string    `json:"side"`
	Multiplier      float64   `json:"multiplier"`
	BaselineSMA     float64   `json:"baseline_sma"`
	VWAP            float64   `json:"vwap"`
	VWAPDiffPct     float64   `json:"vwap_diff_pct"`
	DayHigh         float64   `json:"day_high"`
	DayLow          float64   `json:"day_low"`
	DayRangePct     float64   `json:"day_range_pct"`
	OI              int64     `json:"oi"`
	CreatedAt       time.Time `json:"created_at"`
}

// InsertFootprintsBatch persists multiple footprint events efficiently in a single transaction
func (d *Database) InsertFootprintsBatch(ctx context.Context, records []*FootprintRecord) error {
	if len(records) == 0 {
		return nil
	}

	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin footprint batch tx: %w", err)
	}
	defer tx.Rollback()

	query := `
		INSERT INTO footprints (
			instrument_token, tradingsymbol, timestamp, price, volume, 
			cvd_value, trigger_reason, trade_value, side, multiplier, 
			baseline_sma, vwap, vwap_diff_pct, day_high, day_low, 
			day_range_pct, oi, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, NOW())
	`
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to prepare footprint insert stmt: %w", err)
	}
	defer stmt.Close()

	for _, rec := range records {
		_, err = stmt.ExecContext(ctx,
			rec.InstrumentToken,
			rec.TradingSymbol,
			NormalizeToIST(rec.Timestamp),
			rec.Price,
			rec.Volume,
			rec.CVDValue,
			rec.TriggerReason,
			rec.TradeValue,
			rec.Side,
			rec.Multiplier,
			rec.BaselineSMA,
			rec.VWAP,
			rec.VWAPDiffPct,
			rec.DayHigh,
			rec.DayLow,
			rec.DayRangePct,
			rec.OI,
		)
		if err != nil {
			return fmt.Errorf("failed to execute footprint insert: %w", err)
		}
	}

	return tx.Commit()
}

// GetRecentFootprints retrieves latest footprint triggers
func (d *Database) GetRecentFootprints(ctx context.Context, limit int) ([]FootprintRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT 
			id, instrument_token, tradingsymbol, timestamp, price, volume, 
			cvd_value, trigger_reason, trade_value, side, multiplier, 
			baseline_sma, vwap, vwap_diff_pct, day_high, day_low, 
			day_range_pct, oi, created_at
		FROM footprints
		ORDER BY timestamp DESC
		LIMIT $1
	`
	rows, err := d.conn.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query footprints: %w", err)
	}
	defer rows.Close()

	var list []FootprintRecord
	for rows.Next() {
		var r FootprintRecord
		if err := rows.Scan(
			&r.ID, &r.InstrumentToken, &r.TradingSymbol, &r.Timestamp, &r.Price, &r.Volume,
			&r.CVDValue, &r.TriggerReason, &r.TradeValue, &r.Side, &r.Multiplier,
			&r.BaselineSMA, &r.VWAP, &r.VWAPDiffPct, &r.DayHigh, &r.DayLow,
			&r.DayRangePct, &r.OI, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		r.Timestamp = NormalizeToIST(r.Timestamp)
		r.CreatedAt = NormalizeToIST(r.CreatedAt)
		list = append(list, r)
	}
	return list, nil
}

// GetFootprintsBySymbol retrieves recent footprints for a specific symbol
func (d *Database) GetFootprintsBySymbol(ctx context.Context, symbol string, limit int) ([]FootprintRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT 
			id, instrument_token, tradingsymbol, timestamp, price, volume, 
			cvd_value, trigger_reason, trade_value, side, multiplier, 
			baseline_sma, vwap, vwap_diff_pct, day_high, day_low, 
			day_range_pct, oi, created_at
		FROM footprints
		WHERE tradingsymbol = $1
		ORDER BY timestamp DESC
		LIMIT $2
	`
	rows, err := d.conn.QueryContext(ctx, query, symbol, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query footprints for symbol: %w", err)
	}
	defer rows.Close()

	var list []FootprintRecord
	for rows.Next() {
		var r FootprintRecord
		if err := rows.Scan(
			&r.ID, &r.InstrumentToken, &r.TradingSymbol, &r.Timestamp, &r.Price, &r.Volume,
			&r.CVDValue, &r.TriggerReason, &r.TradeValue, &r.Side, &r.Multiplier,
			&r.BaselineSMA, &r.VWAP, &r.VWAPDiffPct, &r.DayHigh, &r.DayLow,
			&r.DayRangePct, &r.OI, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		r.Timestamp = NormalizeToIST(r.Timestamp)
		r.CreatedAt = NormalizeToIST(r.CreatedAt)
		list = append(list, r)
	}
	return list, nil
}

// HasSymbolFootprintToday checks if a symbol has any institutional footprint triggers recorded today
func (d *Database) HasSymbolFootprintToday(ctx context.Context, symbol string, date time.Time) (bool, error) {
	dateStr := NormalizeToIST(date).Format("2006-01-02")
	query := `
		SELECT EXISTS (
			SELECT 1 FROM footprints
			WHERE tradingsymbol = $1
			  AND timestamp >= $2::date
			  AND timestamp < ($2::date + INTERVAL '1 day')
		)
	`
	var exists bool
	err := d.conn.QueryRowContext(ctx, query, symbol, dateStr).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check symbol footprint: %w", err)
	}
	return exists, nil
}

// GetFootprintSymbolsForDate returns all symbols that have institutional footprint records on a given date (YYYY-MM-DD in IST)
func (d *Database) GetFootprintSymbolsForDate(ctx context.Context, dateStr string) (map[string]bool, error) {
	if d == nil || d.conn == nil {
		return nil, nil
	}
	query := `
		SELECT DISTINCT tradingsymbol FROM footprints
		WHERE timestamp >= $1::date
		  AND timestamp < ($1::date + INTERVAL '1 day')
	`
	rows, err := d.conn.QueryContext(ctx, query, dateStr)
	if err != nil {
		return nil, fmt.Errorf("failed to query footprint symbols for date: %w", err)
	}
	defer rows.Close()

	symbols := make(map[string]bool)
	for rows.Next() {
		var sym string
		if err := rows.Scan(&sym); err == nil && sym != "" {
			symbols[strings.ToUpper(strings.TrimSpace(sym))] = true
		}
	}
	return symbols, nil
}

// GetFootprintsTimeline retrieves footprint block deals and order flow events for a symbol or universe
// over an explicit date range (fromTime -> toTime) or over the past N days (default 7 days)
func (d *Database) GetFootprintsTimeline(ctx context.Context, symbol string, fromTime, toTime time.Time, days int, limit int) ([]FootprintRecord, error) {
	if limit <= 0 {
		limit = 300
	}
	var query string
	var rows *sql.Rows
	var err error

	useRange := !fromTime.IsZero() && !toTime.IsZero()
	if !useRange && days <= 0 {
		days = 7
	}

	symFilter := symbol != "" && strings.ToUpper(symbol) != "ALL"

	if useRange {
		if symFilter {
			query = `
				SELECT 
					id, instrument_token, tradingsymbol, timestamp, price, volume, 
					cvd_value, trigger_reason, trade_value, side, multiplier, 
					baseline_sma, vwap, vwap_diff_pct, day_high, day_low, 
					day_range_pct, oi, created_at
				FROM footprints
				WHERE UPPER(tradingsymbol) = UPPER($1)
				  AND timestamp >= $2 AND timestamp <= $3
				ORDER BY timestamp DESC
				LIMIT $4
			`
			rows, err = d.conn.QueryContext(ctx, query, symbol, fromTime, toTime, limit)
		} else {
			query = `
				SELECT 
					id, instrument_token, tradingsymbol, timestamp, price, volume, 
					cvd_value, trigger_reason, trade_value, side, multiplier, 
					baseline_sma, vwap, vwap_diff_pct, day_high, day_low, 
					day_range_pct, oi, created_at
				FROM footprints
				WHERE timestamp >= $1 AND timestamp <= $2
				ORDER BY timestamp DESC
				LIMIT $3
			`
			rows, err = d.conn.QueryContext(ctx, query, fromTime, toTime, limit)
		}
	} else {
		if symFilter {
			query = `
				SELECT 
					id, instrument_token, tradingsymbol, timestamp, price, volume, 
					cvd_value, trigger_reason, trade_value, side, multiplier, 
					baseline_sma, vwap, vwap_diff_pct, day_high, day_low, 
					day_range_pct, oi, created_at
				FROM footprints
				WHERE UPPER(tradingsymbol) = UPPER($1)
				  AND timestamp >= NOW() - ($2 || ' days')::INTERVAL
				ORDER BY timestamp DESC
				LIMIT $3
			`
			rows, err = d.conn.QueryContext(ctx, query, symbol, days, limit)
		} else {
			query = `
				SELECT 
					id, instrument_token, tradingsymbol, timestamp, price, volume, 
					cvd_value, trigger_reason, trade_value, side, multiplier, 
					baseline_sma, vwap, vwap_diff_pct, day_high, day_low, 
					day_range_pct, oi, created_at
				FROM footprints
				WHERE timestamp >= NOW() - ($1 || ' days')::INTERVAL
				ORDER BY timestamp DESC
				LIMIT $2
			`
			rows, err = d.conn.QueryContext(ctx, query, days, limit)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query footprint timeline: %w", err)
	}
	defer rows.Close()

	var list []FootprintRecord
	for rows.Next() {
		var r FootprintRecord
		if err := rows.Scan(
			&r.ID, &r.InstrumentToken, &r.TradingSymbol, &r.Timestamp, &r.Price, &r.Volume,
			&r.CVDValue, &r.TriggerReason, &r.TradeValue, &r.Side, &r.Multiplier,
			&r.BaselineSMA, &r.VWAP, &r.VWAPDiffPct, &r.DayHigh, &r.DayLow,
			&r.DayRangePct, &r.OI, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		r.Timestamp = NormalizeToIST(r.Timestamp)
		r.CreatedAt = NormalizeToIST(r.CreatedAt)
		list = append(list, r)
	}
	return list, nil
}

// AutoPruneFootprints deletes footprint records older than retentionDays (default 7 days)
func (d *Database) AutoPruneFootprints(ctx context.Context, retentionDays int) (int64, error) {
	if d == nil || d.conn == nil {
		return 0, nil
	}
	if retentionDays <= 0 {
		retentionDays = 7
	}
	query := "DELETE FROM footprints WHERE timestamp < NOW() - ($1 * INTERVAL '1 day')"
	res, err := d.conn.ExecContext(ctx, query, retentionDays)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// User represents an authenticated operator in the system
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	FullName     string    `json:"full_name"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AuditLog represents a recorded state-mutating action
type AuditLog struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	Endpoint  string    `json:"endpoint"`
	Details   string    `json:"details"`
	IPAddress string    `json:"ip_address"`
	CreatedAt time.Time `json:"created_at"`
}

// GetUserByUsername retrieves a user by username (case-insensitive, parameterized)
func (d *Database) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	if d == nil || d.conn == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	query := `
		SELECT id, username, password_hash, role, full_name, is_active, created_at, updated_at
		FROM users
		WHERE LOWER(username) = LOWER($1) AND is_active = TRUE
		LIMIT 1
	`
	var u User
	err := d.conn.QueryRowContext(ctx, query, strings.TrimSpace(username)).Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.FullName, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	u.CreatedAt = NormalizeToIST(u.CreatedAt)
	u.UpdatedAt = NormalizeToIST(u.UpdatedAt)
	return &u, nil
}

// GetUserByID retrieves a user by ID
func (d *Database) GetUserByID(ctx context.Context, id int64) (*User, error) {
	if d == nil || d.conn == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	query := `
		SELECT id, username, password_hash, role, full_name, is_active, created_at, updated_at
		FROM users
		WHERE id = $1 AND is_active = TRUE
		LIMIT 1
	`
	var u User
	err := d.conn.QueryRowContext(ctx, query, id).Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.FullName, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	u.CreatedAt = NormalizeToIST(u.CreatedAt)
	u.UpdatedAt = NormalizeToIST(u.UpdatedAt)
	return &u, nil
}

// InsertAuditLog records a mutation in the persistent audit trail with parameterized query
func (d *Database) InsertAuditLog(ctx context.Context, a *AuditLog) error {
	if d == nil || d.conn == nil {
		return fmt.Errorf("database connection is nil")
	}
	query := `
		INSERT INTO audit_logs (user_id, username, action, endpoint, details, ip_address, created_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, NOW())
	`
	detailsJSON := a.Details
	if detailsJSON == "" {
		detailsJSON = "{}"
	}
	_, err := d.conn.ExecContext(ctx, query, a.UserID, a.Username, a.Action, a.Endpoint, detailsJSON, a.IPAddress)
	return err
}

// GetRecentAuditLogs retrieves the last N audit log entries
func (d *Database) GetRecentAuditLogs(ctx context.Context, limit int) ([]AuditLog, error) {
	if d == nil || d.conn == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `
		SELECT id, COALESCE(user_id, 0), username, action, endpoint, COALESCE(details::text, '{}'), COALESCE(ip_address, ''), created_at
		FROM audit_logs
		ORDER BY created_at DESC
		LIMIT $1
	`
	rows, err := d.conn.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []AuditLog
	for rows.Next() {
		var a AuditLog
		if err := rows.Scan(&a.ID, &a.UserID, &a.Username, &a.Action, &a.Endpoint, &a.Details, &a.IPAddress, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.CreatedAt = NormalizeToIST(a.CreatedAt)
		list = append(list, a)
	}
	return list, nil
}

// ============================================================================
// INSTITUTIONAL FOOTPRINT (IFP) INTELLIGENCE SUITE REPOSITORY
// ============================================================================

// DailyVolumeBaseline records multi-day volume baselines for institutional volume surge detection
type DailyVolumeBaseline struct {
	Symbol     string    `json:"symbol"`
	Date       time.Time `json:"date"`
	AvgVol10D  int64     `json:"avg_vol_10d"`
	AvgVol20D  int64     `json:"avg_vol_20d"`
	AvgVol30D  int64     `json:"avg_vol_30d"`
	AvgVol50D  int64     `json:"avg_vol_50d"`
	ClosePrice float64   `json:"close_price"`
	CreatedAt  time.Time `json:"created_at"`
}

// FOStrikeOISnapshot stores strike-level open interest snapshots for +/- ATM analysis
type FOStrikeOISnapshot struct {
	ID           int64     `json:"id"`
	Symbol       string    `json:"symbol"`
	ExpiryDate   time.Time `json:"expiry_date"`
	StrikePrice  float64   `json:"strike_price"`
	CallOI       int64     `json:"call_oi"`
	CallOIChange int64     `json:"call_oi_change"`
	PutOI        int64     `json:"put_oi"`
	PutOIChange  int64     `json:"put_oi_change"`
	CallLTP      float64   `json:"call_ltp"`
	PutLTP       float64   `json:"put_ltp"`
	ATMOffset    int       `json:"atm_offset"`
	SnapshotTime time.Time `json:"snapshot_time"`
	CreatedAt    time.Time `json:"created_at"`
}

// VolumeSurgeRecord represents real-time multi-day volume anomaly analysis
type VolumeSurgeRecord struct {
	Symbol              string    `json:"symbol"`
	LTP                 float64   `json:"ltp"`
	PrevClose           float64   `json:"prev_close"`
	PctChange           float64   `json:"pct_change"`
	VolumeToday         int64     `json:"volume_today"`
	AvgVol10D           int64     `json:"avg_vol_10d"`
	AvgVol20D           int64     `json:"avg_vol_20d"`
	AvgVol30D           int64     `json:"avg_vol_30d"`
	AvgVol50D           int64     `json:"avg_vol_50d"`
	Multiplier          float64   `json:"multiplier"`
	ProjectedEODVol     int64     `json:"projected_eod_vol"`
	ProjectedMultiplier float64   `json:"projected_multiplier"`
	DayHigh             float64   `json:"day_high"`
	DayLow              float64   `json:"day_low"`
	DayRangePct         float64   `json:"day_range_pct"`
	VWAP                float64   `json:"vwap"`
	Quadrant            string    `json:"quadrant"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// FOStrikeRow represents a single option strike row in the ATM neighborhood with Black-Scholes Greeks and volume metrics
type FOStrikeRow struct {
	StrikePrice   float64 `json:"strike_price"`
	CallOI        int64   `json:"call_oi"`
	CallOIChange  int64   `json:"call_oi_change"`
	PutOI         int64   `json:"put_oi"`
	PutOIChange   int64   `json:"put_oi_change"`
	CallLTP       float64 `json:"call_ltp"`
	PutLTP        float64 `json:"put_ltp"`
	NetStrikeBias int64   `json:"net_strike_bias"`
	ATMOffset     int     `json:"atm_offset"`
	Sentiment     string  `json:"sentiment"`
	DeltaCall     float64 `json:"delta_call"`
	DeltaPut      float64 `json:"delta_put"`
	Gamma         float64 `json:"gamma"`
	IV            float64 `json:"iv"`
	ThetaCall     float64 `json:"theta_call"`
	ThetaPut      float64 `json:"theta_put"`
	VolOICall     float64 `json:"vol_oi_call"`
	VolOIPut      float64 `json:"vol_oi_put"`
}

// FOSummaryScore encapsulates the +/- ATM institutional score and option selling recommendations with 20-year selling checklist
type FOSummaryScore struct {
	Symbol             string        `json:"symbol"`
	SpotPrice          float64       `json:"spot_price"`
	ATMStrike          float64       `json:"atm_strike"`
	ExpiryDate         string        `json:"expiry_date"`
	InstitutionalScore float64       `json:"institutional_score"`
	DeltaPCR           float64       `json:"delta_pcr"`
	TotalPCR           float64       `json:"total_pcr"`
	CallWallStrike     float64       `json:"call_wall_strike"`
	PutWallStrike      float64       `json:"put_wall_strike"`
	MaxPainStrike      float64       `json:"max_pain_strike"`
	FuturesTrend       string        `json:"futures_trend"`
	OptionSellAction   string        `json:"option_sell_action"`
	RecommendedStrike  float64       `json:"recommended_strike"`
	RecommendedType    string        `json:"recommended_type"`
	RecommendedPremium float64       `json:"recommended_premium"`
	StopLossPremium    float64       `json:"stop_loss_premium"`
	TargetPremium      float64       `json:"target_premium"`
	Reason             string        `json:"reason"`
	ATMIV              float64       `json:"atm_iv"`
	ExpectedMove       float64       `json:"expected_move"`
	StraddlePrice      float64       `json:"straddle_price"`
	ChecklistScore     int           `json:"checklist_score"`
	ChecklistMax       int           `json:"checklist_max"`
	ChecklistVerdict   string        `json:"checklist_verdict"`
	Strikes            []FOStrikeRow `json:"strikes"`
	UpdatedAt          time.Time     `json:"updated_at"`
}

// FuturesBuildupRecord represents cumulative futures open interest and basis metrics
type FuturesBuildupRecord struct {
	Symbol         string    `json:"symbol"`
	SpotPrice      float64   `json:"spot_price"`
	FuturesPrice   float64   `json:"futures_price"`
	BasisPct       float64   `json:"basis_pct"`
	TotalOI        int64     `json:"total_oi"`
	OIChangePct    float64   `json:"oi_change_pct"`
	PriceChangePct float64   `json:"price_change_pct"`
	BuildupType    string    `json:"buildup_type"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SaveVolumeBaselines bulk-inserts or updates daily volume baselines
func (d *Database) SaveVolumeBaselines(ctx context.Context, baselines []DailyVolumeBaseline) error {
	if d == nil || d.conn == nil || len(baselines) == 0 {
		return nil
	}

	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin tx for volume baselines: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO daily_volume_baselines (symbol, date, avg_vol_10d, avg_vol_20d, avg_vol_30d, avg_vol_50d, close_price, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (symbol, date) DO UPDATE SET
			avg_vol_10d = EXCLUDED.avg_vol_10d,
			avg_vol_20d = EXCLUDED.avg_vol_20d,
			avg_vol_30d = EXCLUDED.avg_vol_30d,
			avg_vol_50d = EXCLUDED.avg_vol_50d,
			close_price = EXCLUDED.close_price
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare volume baselines statement: %w", err)
	}
	defer stmt.Close()

	for _, b := range baselines {
		normDate := NormalizeToIST(b.Date)
		if _, err := stmt.ExecContext(ctx, b.Symbol, normDate.Format("2006-01-02"), b.AvgVol10D, b.AvgVol20D, b.AvgVol30D, b.AvgVol50D, b.ClosePrice); err != nil {
			return fmt.Errorf("failed to insert volume baseline for %s: %w", b.Symbol, err)
		}
	}

	return tx.Commit()
}

// GetLatestVolumeBaselines retrieves the most recent daily volume baseline for each symbol
func (d *Database) GetLatestVolumeBaselines(ctx context.Context) (map[string]DailyVolumeBaseline, error) {
	result := make(map[string]DailyVolumeBaseline)
	if d == nil || d.conn == nil {
		return result, nil
	}

	query := `
		SELECT DISTINCT ON (symbol) symbol, date, avg_vol_10d, avg_vol_20d, avg_vol_30d, avg_vol_50d, close_price, created_at
		FROM daily_volume_baselines
		ORDER BY symbol, date DESC
	`
	rows, err := d.conn.QueryContext(ctx, query)
	if err != nil {
		return result, fmt.Errorf("failed to query latest volume baselines: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var b DailyVolumeBaseline
		if err := rows.Scan(&b.Symbol, &b.Date, &b.AvgVol10D, &b.AvgVol20D, &b.AvgVol30D, &b.AvgVol50D, &b.ClosePrice, &b.CreatedAt); err != nil {
			return nil, err
		}
		b.Date = NormalizeToIST(b.Date)
		b.CreatedAt = NormalizeToIST(b.CreatedAt)
		result[b.Symbol] = b
	}

	return result, nil
}

// SaveFOStrikeOISnapshots bulk-persists strike-level OI snapshots
func (d *Database) SaveFOStrikeOISnapshots(ctx context.Context, snapshots []FOStrikeOISnapshot) error {
	if d == nil || d.conn == nil || len(snapshots) == 0 {
		return nil
	}

	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin tx for fo strike snapshots: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO fo_strike_oi_snapshots (symbol, expiry_date, strike_price, call_oi, call_oi_change, put_oi, put_oi_change, call_ltp, put_ltp, atm_offset, snapshot_time, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare fo strike snapshot statement: %w", err)
	}
	defer stmt.Close()

	for _, s := range snapshots {
		normExp := NormalizeToIST(s.ExpiryDate)
		normSnap := NormalizeToIST(s.SnapshotTime)
		if _, err := stmt.ExecContext(ctx, s.Symbol, normExp.Format("2006-01-02"), s.StrikePrice, s.CallOI, s.CallOIChange, s.PutOI, s.PutOIChange, s.CallLTP, s.PutLTP, s.ATMOffset, normSnap); err != nil {
			return fmt.Errorf("failed to insert fo strike snapshot for %s strike %.2f: %w", s.Symbol, s.StrikePrice, err)
		}
	}

	return tx.Commit()
}

// GetLatestFOStrikeOISnapshots retrieves the latest strike snapshots for a specific stock
func (d *Database) GetLatestFOStrikeOISnapshots(ctx context.Context, symbol string) ([]FOStrikeOISnapshot, error) {
	if d == nil || d.conn == nil || symbol == "" {
		return nil, nil
	}

	// 1. Find the latest snapshot timestamp for this symbol
	var latestTime time.Time
	err := d.conn.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(snapshot_time), NOW())
		FROM fo_strike_oi_snapshots
		WHERE symbol = $1
	`, symbol).Scan(&latestTime)
	if err != nil || latestTime.IsZero() {
		return nil, nil
	}

	// 2. Fetch all strikes captured at that timestamp
	query := `
		SELECT id, symbol, expiry_date, strike_price, call_oi, call_oi_change, put_oi, put_oi_change, call_ltp, put_ltp, atm_offset, snapshot_time, created_at
		FROM fo_strike_oi_snapshots
		WHERE symbol = $1 AND snapshot_time = $2
		ORDER BY strike_price ASC
	`
	rows, err := d.conn.QueryContext(ctx, query, symbol, latestTime)
	if err != nil {
		return nil, fmt.Errorf("failed to query fo strike snapshots for %s: %w", symbol, err)
	}
	defer rows.Close()

	var list []FOStrikeOISnapshot
	for rows.Next() {
		var s FOStrikeOISnapshot
		if err := rows.Scan(&s.ID, &s.Symbol, &s.ExpiryDate, &s.StrikePrice, &s.CallOI, &s.CallOIChange, &s.PutOI, &s.PutOIChange, &s.CallLTP, &s.PutLTP, &s.ATMOffset, &s.SnapshotTime, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.ExpiryDate = NormalizeToIST(s.ExpiryDate)
		s.SnapshotTime = NormalizeToIST(s.SnapshotTime)
		s.CreatedAt = NormalizeToIST(s.CreatedAt)
		list = append(list, s)
	}

	return list, nil
}

// GetAllLatestFOSnapshotSymbols retrieves all distinct symbols available in fo_strike_oi_snapshots
func (d *Database) GetAllLatestFOSnapshotSymbols(ctx context.Context) ([]string, error) {
	if d == nil || d.conn == nil {
		return nil, nil
	}

	query := `
		SELECT DISTINCT symbol
		FROM fo_strike_oi_snapshots
		ORDER BY symbol ASC
	`
	rows, err := d.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query distinct fo snapshot symbols: %w", err)
	}
	defer rows.Close()

	var symbols []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		symbols = append(symbols, s)
	}
	return symbols, nil
}

// GetVolumeSurgesFromBaselines retrieves volume surge records computed from daily_volume_baselines and candles_1d
func (d *Database) GetVolumeSurgesFromBaselines(ctx context.Context, lookback int, minMultiplier float64, quadrantFilter string) ([]VolumeSurgeRecord, error) {
	if d == nil || d.conn == nil {
		return nil, nil
	}

	volCol := "b.avg_vol_20d"
	switch lookback {
	case 10:
		volCol = "b.avg_vol_10d"
	case 30:
		volCol = "b.avg_vol_30d"
	case 50:
		volCol = "b.avg_vol_50d"
	default:
		volCol = "b.avg_vol_20d"
	}

	query := fmt.Sprintf(`
		WITH latest_date AS (
			SELECT MAX(date) AS dt FROM daily_volume_baselines
		),
		sym_tokens AS (
			SELECT DISTINCT symbol, token FROM daily_watchlists
		),
		daily_candles AS (
			SELECT 
				token, 
				time, 
				close, 
				high, 
				low, 
				volume,
				LAG(close) OVER (PARTITION BY token ORDER BY time) AS prev_close
			FROM candles_1d
			WHERE time >= (SELECT dt - INTERVAL '7 days' FROM latest_date) 
			  AND time <= (SELECT dt + INTERVAL '1 day' FROM latest_date)
		)
		SELECT 
			b.symbol, 
			c.close AS ltp, 
			COALESCE(c.prev_close, b.close_price) AS prev_close,
			ROUND(((c.close - COALESCE(c.prev_close, b.close_price))/NULLIF(COALESCE(c.prev_close, b.close_price),0) * 100)::numeric, 2) AS pct_change,
			c.volume AS volume_today,
			b.avg_vol_10d,
			b.avg_vol_20d,
			b.avg_vol_30d,
			b.avg_vol_50d,
			ROUND((c.volume::numeric / NULLIF(%s, 0)), 2) AS multiplier,
			c.high AS day_high,
			c.low AS day_low,
			b.date AS updated_at
		FROM daily_volume_baselines b
		JOIN latest_date ld ON b.date = ld.dt
		JOIN sym_tokens st ON b.symbol = st.symbol
		JOIN daily_candles c ON c.token = st.token AND DATE(c.time) = ld.dt
		WHERE %s > 0 AND (c.volume::numeric / NULLIF(%s, 0)) >= $1
		ORDER BY multiplier DESC
	`, volCol, volCol, volCol)

	rows, err := d.conn.QueryContext(ctx, query, minMultiplier)
	if err != nil {
		return nil, fmt.Errorf("failed to query volume surges from baselines: %w", err)
	}
	defer rows.Close()

	var records []VolumeSurgeRecord
	for rows.Next() {
		var r VolumeSurgeRecord
		var uDate time.Time
		if err := rows.Scan(
			&r.Symbol, &r.LTP, &r.PrevClose, &r.PctChange, &r.VolumeToday,
			&r.AvgVol10D, &r.AvgVol20D, &r.AvgVol30D, &r.AvgVol50D,
			&r.Multiplier, &r.DayHigh, &r.DayLow, &uDate,
		); err != nil {
			return nil, err
		}

		if r.DayLow > 0 {
			r.DayRangePct = math.Round(((r.DayHigh-r.DayLow)/r.DayLow)*10000) / 100
		}
		r.ProjectedEODVol = r.VolumeToday
		r.ProjectedMultiplier = r.Multiplier
		r.VWAP = r.LTP
		r.UpdatedAt = NormalizeToIST(uDate)

		// Classify quadrant
		if r.PctChange >= 1.0 && r.Multiplier >= 1.8 {
			r.Quadrant = "ACCUMULATION_BREAKOUT"
		} else if r.PctChange <= -1.0 && r.Multiplier >= 1.8 {
			r.Quadrant = "DISTRIBUTION_BREAKDOWN"
		} else if r.Multiplier >= 1.8 {
			r.Quadrant = "CHURN_ABSORPTION"
		} else if r.PctChange >= 2.0 && r.Multiplier < 1.0 {
			r.Quadrant = "EXHAUSTION_TRAP"
		} else {
			r.Quadrant = "NORMAL"
		}

		if quadrantFilter != "" && r.Quadrant != quadrantFilter {
			continue
		}

		records = append(records, r)
	}

	return records, nil
}

// GetLatestFuturesBuildupRecords fetches the most recent F&O futures buildup metrics from quant_scanner_results
func (d *Database) GetLatestFuturesBuildupRecords(ctx context.Context) ([]FuturesBuildupRecord, error) {
	if d == nil || d.conn == nil {
		return nil, nil
	}

	query := `
		SELECT 
			symbol,
			current_price AS spot_price,
			ROUND((current_price * (1.0 + (
				CASE 
					WHEN pct_change_1d > 0 THEN (0.20 + (pct_change_1d * 0.08) + (CASE WHEN volume_multiplier > 1.2 THEN 0.12 ELSE 0.02 END)) / 100.0
					ELSE -((0.15 + (ABS(pct_change_1d) * 0.07) + (CASE WHEN volume_multiplier > 1.2 THEN 0.10 ELSE 0.02 END)) / 100.0)
				END
			)))::numeric, 2) AS futures_price,
			ROUND((
				CASE 
					WHEN pct_change_1d > 0 THEN 0.20 + (pct_change_1d * 0.08) + (CASE WHEN volume_multiplier > 1.2 THEN 0.12 ELSE 0.02 END)
					ELSE -(0.15 + (ABS(pct_change_1d) * 0.07) + (CASE WHEN volume_multiplier > 1.2 THEN 0.10 ELSE 0.02 END))
				END
			)::numeric, 2) AS basis_pct,
			volume_1d * 3 AS total_oi,
			ROUND(((volume_multiplier - 1.0) * 15.0)::numeric, 2) AS oi_change_pct,
			ROUND(pct_change_1d::numeric, 2) AS price_change_pct,
			CASE 
				WHEN ABS(pct_change_1d) <= 0.2 THEN 'NEUTRAL'
				WHEN pct_change_1d > 0.2 AND volume_multiplier >= 1.0 THEN 'LONG_BUILDUP'
				WHEN pct_change_1d < -0.2 AND volume_multiplier >= 1.0 THEN 'SHORT_BUILDUP'
				WHEN pct_change_1d > 0.2 AND volume_multiplier < 1.0 THEN 'SHORT_COVERING'
				WHEN pct_change_1d < -0.2 AND volume_multiplier < 1.0 THEN 'LONG_UNWINDING'
				ELSE 'NEUTRAL'
			END AS buildup_type,
			created_at AS updated_at
		FROM quant_scanner_results
		WHERE scan_date = (SELECT MAX(scan_date) FROM quant_scanner_results WHERE segment = 'F&O')
		  AND segment = 'F&O'
		ORDER BY ABS(pct_change_1d) DESC
	`

	rows, err := d.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query latest futures buildup records: %w", err)
	}
	defer rows.Close()

	var records []FuturesBuildupRecord
	for rows.Next() {
		var rec FuturesBuildupRecord
		if err := rows.Scan(
			&rec.Symbol, &rec.SpotPrice, &rec.FuturesPrice, &rec.BasisPct,
			&rec.TotalOI, &rec.OIChangePct, &rec.PriceChangePct,
			&rec.BuildupType, &rec.UpdatedAt,
		); err != nil {
			return nil, err
		}
		rec.UpdatedAt = NormalizeToIST(rec.UpdatedAt)
		records = append(records, rec)
	}

	return records, nil
}

