package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
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
			active_trade_id, active_order_id, active_symbol, active_side, active_qty,
			entry_premium, sl_price, paper_balance, active_created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW())
		ON CONFLICT (index_symbol) DO UPDATE SET
			multiplier = EXCLUDED.multiplier,
			last_trend = EXCLUDED.last_trend,
			sl_stopped_trend = EXCLUDED.sl_stopped_trend,
			awaiting_reversal = EXCLUDED.awaiting_reversal,
			active_trade_id = EXCLUDED.active_trade_id,
			active_order_id = EXCLUDED.active_order_id,
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
		state.ActiveTradeID, state.ActiveOrderID, state.ActiveSymbol, state.ActiveSide, state.ActiveQty,
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
		       COALESCE(active_trade_id, 0), COALESCE(active_order_id, ''), COALESCE(active_symbol, ''), COALESCE(active_side, ''), COALESCE(active_qty, 0),
		       COALESCE(entry_premium, 0), COALESCE(sl_price, 0), paper_balance, COALESCE(active_created_at, updated_at), updated_at
		FROM options_bot_state
		WHERE index_symbol = $1 OR index_symbol = $2
		LIMIT 1
	`
	var st OptionsBotState
	err := d.conn.QueryRowContext(ctx, query, cleanName, spec.CleanPrefix).Scan(
		&st.ID, &st.IndexSymbol, &st.Multiplier, &st.LastTrend, &st.SLStoppedTrend, &st.AwaitingReversal,
		&st.ActiveTradeID, &st.ActiveOrderID, &st.ActiveSymbol, &st.ActiveSide, &st.ActiveQty,
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
		       COALESCE(active_trade_id, 0), COALESCE(active_order_id, ''), COALESCE(active_symbol, ''), COALESCE(active_side, ''), COALESCE(active_qty, 0),
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
			&st.ActiveTradeID, &st.ActiveOrderID, &st.ActiveSymbol, &st.ActiveSide, &st.ActiveQty,
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
	YearlyHigh        float64   `json:"yearly_high"`
	YearlyLow         float64   `json:"yearly_low"`
	MonthlyHigh       float64   `json:"monthly_high"`
	MonthlyLow        float64   `json:"monthly_low"`
	WeeklyHigh        float64   `json:"weekly_high"`
	WeeklyLow         float64   `json:"weekly_low"`
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
			pct_change_1d, pct_change_3d, range_pct_change, yearly_high, yearly_low, monthly_high, monthly_low, weekly_high, weekly_low, all_time_high, all_time_low,
			volume_1d, volume_adv, volume_multiplier,
			confidence_score, quant_direction, recommended_action, news_summary, news_sentiment, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26)
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
			monthly_high = EXCLUDED.monthly_high,
			monthly_low = EXCLUDED.monthly_low,
			weekly_high = EXCLUDED.weekly_high,
			weekly_low = EXCLUDED.weekly_low,
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
			r.PctChange1D, r.PctChange3D, r.RangePctChange, r.YearlyHigh, r.YearlyLow, r.MonthlyHigh, r.MonthlyLow, r.WeeklyHigh, r.WeeklyLow, r.AllTimeHigh, r.AllTimeLow,
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
			       pct_change_1d, pct_change_3d, range_pct_change, COALESCE(yearly_high, 0), COALESCE(yearly_low, 0), COALESCE(monthly_high, 0), COALESCE(monthly_low, 0), COALESCE(weekly_high, 0), COALESCE(weekly_low, 0), COALESCE(all_time_high, 0), COALESCE(all_time_low, 0),
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
			       pct_change_1d, pct_change_3d, range_pct_change, COALESCE(yearly_high, 0), COALESCE(yearly_low, 0), COALESCE(monthly_high, 0), COALESCE(monthly_low, 0), COALESCE(weekly_high, 0), COALESCE(weekly_low, 0), COALESCE(all_time_high, 0), COALESCE(all_time_low, 0),
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
			&r.PctChange1D, &r.PctChange3D, &r.RangePctChange, &r.YearlyHigh, &r.YearlyLow, &r.MonthlyHigh, &r.MonthlyLow, &r.WeeklyHigh, &r.WeeklyLow, &r.AllTimeHigh, &r.AllTimeLow,
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
		    status = $4,
		    time_held_minutes = GREATEST(1, EXTRACT(EPOCH FROM ($2 - entry_time))/60)::int,
		    updated_at = NOW()
		WHERE id = $5
	`
	_, err := d.conn.ExecContext(ctx, query, exitPrice, NormalizeToIST(exitTime), pnl, statusText, tradeID)
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
	ST1Period            int       `json:"st1_period"`
	ST1Multiplier        float64   `json:"st1_multiplier"`
	ST2Period            int       `json:"st2_period"`
	ST2Multiplier        float64   `json:"st2_multiplier"`
	ST3Period            int       `json:"st3_period"`
	ST3Multiplier        float64   `json:"st3_multiplier"`
	LastNewTradeTime     string    `json:"last_new_trade_time"`
	AutoSquareOffTime    string    `json:"auto_square_off_time"`
	SuperTrendCutoffTime string    `json:"supertrend_cutoff_time"`
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
		       st1_period, st1_multiplier, st2_period, st2_multiplier,
		       st3_period, st3_multiplier, last_new_trade_time, auto_square_off_time, supertrend_cutoff_time,
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
		&cfg.ST1Period, &cfg.ST1Multiplier, &cfg.ST2Period, &cfg.ST2Multiplier,
		&cfg.ST3Period, &cfg.ST3Multiplier, &cfg.LastNewTradeTime, &cfg.AutoSquareOffTime, &cfg.SuperTrendCutoffTime,
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
		       st1_period, st1_multiplier, st2_period, st2_multiplier,
		       st3_period, st3_multiplier, last_new_trade_time, auto_square_off_time, supertrend_cutoff_time,
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
			&cfg.ST1Period, &cfg.ST1Multiplier, &cfg.ST2Period, &cfg.ST2Multiplier,
			&cfg.ST3Period, &cfg.ST3Multiplier, &cfg.LastNewTradeTime, &cfg.AutoSquareOffTime, &cfg.SuperTrendCutoffTime,
			&cfg.CreatedAt, &cfg.UpdatedAt,
		); err == nil {
			results = append(results, cfg)
		}
	}
	return results, nil
}

// SaveOptionsIndexConfig saves or updates an index configuration row in PostgreSQL
func (d *Database) SaveOptionsIndexConfig(ctx context.Context, cfg *OptionsIndexConfig) error {
	if d == nil || d.conn == nil {
		return fmt.Errorf("database connection is nil")
	}

	if cfg.TrailSLBufferPct <= 0 {
		cfg.TrailSLBufferPct = 5.0
	}

	spec, _ := ResolveIndexSpec(cfg.IndexSymbol)
	query := `
		INSERT INTO options_index_configs (
			index_symbol, is_active, is_live, base_lot_size, max_multiplier, multiplier_on_reversal,
			target_entry_premium, expiry_type, next_month_days, sl_pct,
			trail_sl_enabled, trail_sl_pct, trail_sl_buffer_pct,
			st1_period, st1_multiplier, st2_period, st2_multiplier,
			st3_period, st3_multiplier, last_new_trade_time, auto_square_off_time, supertrend_cutoff_time,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, NOW())
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
			st1_period = EXCLUDED.st1_period,
			st1_multiplier = EXCLUDED.st1_multiplier,
			st2_period = EXCLUDED.st2_period,
			st2_multiplier = EXCLUDED.st2_multiplier,
			st3_period = EXCLUDED.st3_period,
			st3_multiplier = EXCLUDED.st3_multiplier,
			last_new_trade_time = EXCLUDED.last_new_trade_time,
			auto_square_off_time = EXCLUDED.auto_square_off_time,
			supertrend_cutoff_time = EXCLUDED.supertrend_cutoff_time,
			updated_at = NOW()
	`
	_, err := d.conn.ExecContext(ctx, query,
		spec.Name, cfg.IsActive, cfg.IsLive, cfg.BaseLotSize, cfg.MaxMultiplier, cfg.MultiplierOnReversal,
		cfg.TargetEntryPremium, cfg.ExpiryType, cfg.NextMonthDays, cfg.SLPct,
		cfg.TrailSLEnabled, cfg.TrailSLPct, cfg.TrailSLBufferPct,
		cfg.ST1Period, cfg.ST1Multiplier, cfg.ST2Period, cfg.ST2Multiplier,
		cfg.ST3Period, cfg.ST3Multiplier, cfg.LastNewTradeTime, cfg.AutoSquareOffTime, cfg.SuperTrendCutoffTime,
	)
	return err
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

// SaveSystemConfigsBatch saves a batch of system configurations across categories atomically
func (d *Database) SaveSystemConfigsBatch(ctx context.Context, configs map[string]map[string]string) error {
	if d == nil || d.conn == nil {
		return fmt.Errorf("database connection is nil")
	}

	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `
		INSERT INTO app_system_configs (category, config_key, config_value, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (category, config_key) DO UPDATE SET
			config_value = EXCLUDED.config_value,
			updated_at = NOW()
	`
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for cat, keys := range configs {
		for k, v := range keys {
			if _, err := stmt.ExecContext(ctx, cat, k, v); err != nil {
				return fmt.Errorf("failed to execute batch config update for %s.%s: %w", cat, k, err)
			}
		}
	}

	return tx.Commit()
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


