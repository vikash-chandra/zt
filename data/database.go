package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/lib/pq"
	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"go.uber.org/zap"
)

// Database wraps database connections
type Database struct {
	conn   *sql.DB
	logger *zap.Logger
}

// NewDatabase creates database connection
func NewDatabase(host string, port int, user, password, dbname, sslmode string, logger *zap.Logger) (*Database, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode)

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	if err := conn.Ping(); err != nil {
		return nil, err
	}

	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(5)

	logger.Info("Database connected", zap.String("host", host))

	return &Database{conn: conn, logger: logger}, nil
}

// NewDatabaseFromConn wraps an existing sql.DB connection (for testing)
func NewDatabaseFromConn(conn *sql.DB, logger *zap.Logger) *Database {
	return &Database{conn: conn, logger: logger}
}

// InitSchema creates necessary tables
func (d *Database) InitSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS candles_5m (
		token BIGINT NOT NULL,
		time TIMESTAMP NOT NULL,
		open DECIMAL(10, 4),
		high DECIMAL(10, 4),
		low DECIMAL(10, 4),
		close DECIMAL(10, 4),
		volume BIGINT,
		vwap DECIMAL(10, 4),
		bid DECIMAL(10, 4),
		ask DECIMAL(10, 4),
		tick_count INT,
		color VARCHAR(10),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (token, time)
	) WITH (OIDS=FALSE);

	CREATE INDEX IF NOT EXISTS idx_candles_5m_token_time ON candles_5m (token, time DESC);

	CREATE TABLE IF NOT EXISTS candles_1m (
		token BIGINT NOT NULL,
		time TIMESTAMP NOT NULL,
		open DECIMAL(10, 4),
		high DECIMAL(10, 4),
		low DECIMAL(10, 4),
		close DECIMAL(10, 4),
		volume BIGINT,
		vwap DECIMAL(10, 4),
		bid DECIMAL(10, 4),
		ask DECIMAL(10, 4),
		tick_count INT,
		color VARCHAR(10),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (token, time)
	) WITH (OIDS=FALSE);

	CREATE INDEX IF NOT EXISTS idx_candles_1m_token_time ON candles_1m (token, time DESC);

	CREATE TABLE IF NOT EXISTS orders (
		order_id VARCHAR(50) PRIMARY KEY,
		symbol VARCHAR(20) NOT NULL,
		exchange VARCHAR(10) NOT NULL,
		quantity INT NOT NULL,
		transaction_type VARCHAR(10) NOT NULL,
		order_type VARCHAR(20) NOT NULL,
		product VARCHAR(10) NOT NULL,
		price DECIMAL(10, 4),
		trigger_price DECIMAL(10, 4),
		status VARCHAR(20) NOT NULL,
		filled_quantity INT DEFAULT 0,
		average_price DECIMAL(10, 4),
		placed_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		strategy VARCHAR(50) DEFAULT 'LOW_VOLUME'
	);

	CREATE TABLE IF NOT EXISTS positions (
		id SERIAL PRIMARY KEY,
		order_id VARCHAR(50) REFERENCES orders(order_id),
		symbol VARCHAR(20) NOT NULL,
		quantity INT NOT NULL,
		entry_price DECIMAL(10, 4) NOT NULL,
		current_price DECIMAL(10, 4),
		side VARCHAR(10) NOT NULL,
		sl_price DECIMAL(10, 4),
		created_at TIMESTAMP NOT NULL,
		closed_at TIMESTAMP,
		strategy VARCHAR(50) DEFAULT 'LOW_VOLUME'
	);

	CREATE TABLE IF NOT EXISTS trades (
		id SERIAL PRIMARY KEY,
		symbol VARCHAR(20) NOT NULL,
		entry_price DECIMAL(10, 4) NOT NULL,
		exit_price DECIMAL(10, 4) NOT NULL,
		quantity INT NOT NULL,
		pnl DECIMAL(15, 2) NOT NULL,
		side VARCHAR(10) NOT NULL,
		time_held_minutes INT,
		created_at TIMESTAMP NOT NULL,
		strategy VARCHAR(50) DEFAULT 'LOW_VOLUME'
	);

	CREATE TABLE IF NOT EXISTS metadata_cache (
		key VARCHAR(100) PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS market_breadth_logs (
		id SERIAL PRIMARY KEY,
		time TIMESTAMP NOT NULL,
		advances INT,
		declines INT,
		neutrals INT,
		global_bias VARCHAR(20),
		details JSONB
	);

	CREATE TABLE IF NOT EXISTS daily_market_bias (
		date DATE PRIMARY KEY,
		bias VARCHAR(20) NOT NULL,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS daily_manual_watchlist (
		date DATE PRIMARY KEY,
		symbols VARCHAR(500) NOT NULL,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	DROP TABLE IF EXISTS pre_selection_results CASCADE;

	CREATE TABLE pre_selection_results (
		date DATE NOT NULL,
		ticker VARCHAR(20) NOT NULL,
		rule_set VARCHAR(20) NOT NULL DEFAULT 'STANDARD',
		predicted_direction VARCHAR(50) NOT NULL,
		imbalance_ratio DOUBLE PRECISION NOT NULL,
		indicative_gap_pct DOUBLE PRECISION NOT NULL,
		pre_open_vol_vs_adv DOUBLE PRECISION NOT NULL,
		probability_score DOUBLE PRECISION NOT NULL,
		reason TEXT NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (date, ticker, rule_set)
	);
	`

	if _, err := d.conn.Exec(schema); err != nil {
		return err
	}

	// Migrations: ensure strategy columns exist for backward compatibility with active DB instances
	_, _ = d.conn.Exec("ALTER TABLE orders ADD COLUMN IF NOT EXISTS strategy VARCHAR(50) DEFAULT 'LOW_VOLUME'")
	_, _ = d.conn.Exec("ALTER TABLE positions ADD COLUMN IF NOT EXISTS strategy VARCHAR(50) DEFAULT 'LOW_VOLUME'")
	_, _ = d.conn.Exec("ALTER TABLE positions ADD COLUMN IF NOT EXISTS broker_sl_order_id VARCHAR(50) DEFAULT ''")
	_, _ = d.conn.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_positions_order_id ON positions (order_id)")
	_, _ = d.conn.Exec("ALTER TABLE trades ADD COLUMN IF NOT EXISTS strategy VARCHAR(50) DEFAULT 'LOW_VOLUME'")

	return nil
}

// Close closes database connection
func (d *Database) Close() error {
	return d.conn.Close()
}

// Exec executes a statement
func (d *Database) Exec(query string, args ...interface{}) (sql.Result, error) {
	return d.conn.Exec(query, args...)
}

// Query executes a query
func (d *Database) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return d.conn.Query(query, args...)
}

// QueryRow executes a query returning single row
func (d *Database) QueryRow(query string, args ...interface{}) *sql.Row {
	return d.conn.QueryRow(query, args...)
}

// WithContext returns context-aware connection
func (d *Database) WithContext(ctx context.Context) *sql.DB {
	return d.conn
}

// GetWatchlistFallback retrieves watchlist symbols and active tokens from cache
func (d *Database) GetWatchlistFallback(ctx context.Context) (map[string]int64, error) {
	wlCopy := make(map[string]int64)
	var cacheVal string
	err := d.conn.QueryRowContext(ctx,
		"SELECT value FROM metadata_cache WHERE key = 'fo:stocks'",
	).Scan(&cacheVal)
	if err != nil {
		return nil, err
	}

	var stocksMap map[string]int64
	if err := json.Unmarshal([]byte(cacheVal), &stocksMap); err != nil {
		return nil, err
	}

	// Get tokens that have candle data in the last 24 hours
	rows, err := d.conn.QueryContext(ctx,
		"SELECT DISTINCT token FROM candles_5m WHERE time >= NOW() - INTERVAL '24 hours'",
	)
	if err == nil {
		activeTokens := make(map[int64]bool)
		for rows.Next() {
			var tok int64
			if rows.Scan(&tok) == nil {
				activeTokens[tok] = true
			}
		}
		rows.Close()

		for sym, tok := range stocksMap {
			if activeTokens[tok] {
				wlCopy[sym] = tok
			}
		}
	}

	// Also add symbols from trades table
	tRows, err := d.conn.QueryContext(ctx,
		"SELECT DISTINCT symbol FROM trades",
	)
	if err == nil {
		for tRows.Next() {
			var sym string
			if tRows.Scan(&sym) == nil {
				if tok, exists := stocksMap[sym]; exists {
					wlCopy[sym] = tok
				}
			}
		}
		tRows.Close()
	}

	return wlCopy, nil
}

// GetTradingMetrics returns count, total pnl and tx value of trades for the current day (Kolkata timezone)
func (d *Database) GetTradingMetrics(ctx context.Context) (int, float64, float64, error) {
	var totalTrades int
	var totalPnL float64
	var totalTxValue float64

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	err = d.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM trades WHERE created_at >= $1", startOfDay).Scan(&totalTrades)
	if err != nil {
		return 0, 0, 0, err
	}
	err = d.conn.QueryRowContext(ctx, "SELECT COALESCE(SUM(pnl), 0) FROM trades WHERE created_at >= $1", startOfDay).Scan(&totalPnL)
	if err != nil {
		return 0, 0, 0, err
	}
	err = d.conn.QueryRowContext(ctx, "SELECT COALESCE(SUM(entry_price * quantity), 0) FROM trades WHERE created_at >= $1", startOfDay).Scan(&totalTxValue)
	if err != nil {
		return 0, 0, 0, err
	}

	return totalTrades, totalPnL, totalTxValue, nil
}

// GetLatestMarketBreadth gets last market breadth logs
func (d *Database) GetLatestMarketBreadth(ctx context.Context) (int, int, int, string, error) {
	var advances, declines, neutrals int
	var globalBias string
	err := d.conn.QueryRowContext(ctx,
		"SELECT advances, declines, neutrals, global_bias FROM market_breadth_logs ORDER BY time DESC LIMIT 1",
	).Scan(&advances, &declines, &neutrals, &globalBias)
	return advances, declines, neutrals, globalBias, err
}

// SaveMarketBreadthLog stores a breadth indicator snapshot
func (d *Database) SaveMarketBreadthLog(ctx context.Context, t time.Time, advances, declines, neutrals int, globalBias string, details string) error {
	_, err := d.conn.ExecContext(ctx, `
		INSERT INTO market_breadth_logs (time, advances, declines, neutrals, global_bias, details)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, t, advances, declines, neutrals, globalBias, details)
	return err
}

// ResolveSymbolToken looks up token by symbol from metadata_cache
func (d *Database) ResolveSymbolToken(ctx context.Context, symbol string) (int64, error) {
	var token int64
	err := d.conn.QueryRowContext(ctx,
		"SELECT (value::jsonb->$1)::bigint FROM metadata_cache WHERE key = 'fo:stocks'",
		symbol,
	).Scan(&token)
	return token, err
}

// CandleRecord matches basic candle format for frontend consumption
type CandleRecord struct {
	Time   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
}

// GetCandlesForDay gets candles for a token since start of day
func (d *Database) GetCandlesForDay(ctx context.Context, token int64, todayStart time.Time) ([]CandleRecord, error) {
	rows, err := d.conn.QueryContext(ctx,
		"SELECT time, open, high, low, close, volume FROM candles_5m WHERE token = $1 AND time >= $2 ORDER BY time ASC",
		token, todayStart,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []CandleRecord
	for rows.Next() {
		var t time.Time
		var o, h, l, c float64
		var v int64
		if err := rows.Scan(&t, &o, &h, &l, &c, &v); err != nil {
			continue
		}
		list = append(list, CandleRecord{
			Time:   t,
			Open:   o,
			High:   h,
			Low:    l,
			Close:  c,
			Volume: v,
		})
	}
	return list, nil
}

// TradeExecRecord matches executions today for markings on chart
type TradeExecRecord struct {
	Time            time.Time
	TransactionType string
	Price           float64
	Quantity        int
}

// GetTradesForSymbolToday gets complete orders for a symbol today
func (d *Database) GetTradesForSymbolToday(ctx context.Context, symbol string, todayStart time.Time) ([]TradeExecRecord, error) {
	rows, err := d.conn.QueryContext(ctx,
		"SELECT placed_at, transaction_type, average_price, quantity FROM orders WHERE symbol = $1 AND status = 'COMPLETE' AND placed_at >= $2 ORDER BY placed_at ASC",
		symbol, todayStart,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []TradeExecRecord
	for rows.Next() {
		var t time.Time
		var trType string
		var price float64
		var qty int
		if err := rows.Scan(&t, &trType, &price, &qty); err != nil {
			continue
		}
		list = append(list, TradeExecRecord{
			Time:            t,
			TransactionType: trType,
			Price:           price,
			Quantity:        qty,
		})
	}
	return list, nil
}

// TradeHistoryRecord represents completed trades history
type TradeHistoryRecord struct {
	ID              int       `json:"id"`
	Symbol          string    `json:"symbol"`
	EntryPrice      float64   `json:"entry_price"`
	ExitPrice       float64   `json:"exit_price"`
	Quantity        int       `json:"quantity"`
	PnL             float64   `json:"pnl"`
	Side            string    `json:"side"`
	TimeHeldMinutes int       `json:"time_held_minutes"`
	CreatedAt       time.Time `json:"created_at"`
	Strategy        string    `json:"strategy"`
}

// GetAllTradesHistory loads all trades from database
func (d *Database) GetAllTradesHistory(ctx context.Context) ([]TradeHistoryRecord, error) {
	rows, err := d.conn.QueryContext(ctx,
		"SELECT id, symbol, entry_price, exit_price, quantity, pnl, side, COALESCE(time_held_minutes, 0), created_at, COALESCE(strategy, 'LOW_VOLUME') FROM trades ORDER BY created_at DESC",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []TradeHistoryRecord
	for rows.Next() {
		var tr TradeHistoryRecord
		err := rows.Scan(
			&tr.ID,
			&tr.Symbol,
			&tr.EntryPrice,
			&tr.ExitPrice,
			&tr.Quantity,
			&tr.PnL,
			&tr.Side,
			&tr.TimeHeldMinutes,
			&tr.CreatedAt,
			&tr.Strategy,
		)
		if err != nil {
			continue
		}
		list = append(list, tr)
	}
	return list, nil
}

// GetLastCandleTimeBefore finds the most recent candle time prior to today
func (d *Database) GetLastCandleTimeBefore(ctx context.Context, token int64, before time.Time) (time.Time, error) {
	var lastTime time.Time
	err := d.conn.QueryRowContext(ctx, `
		SELECT MAX(time) FROM candles_5m WHERE token = $1 AND time < $2
	`, token, before).Scan(&lastTime)
	return lastTime, err
}

// GetPreviousDayHighLow gets high/low for a token on a range
func (d *Database) GetPreviousDayHighLow(ctx context.Context, token int64, prevDayStart, prevDayEnd time.Time) (float64, float64, error) {
	var high, low float64
	err := d.conn.QueryRowContext(ctx, `
		SELECT MAX(high), MIN(low) FROM candles_5m
		WHERE token = $1 AND time >= $2 AND time <= $3
	`, token, prevDayStart, prevDayEnd).Scan(&high, &low)
	return high, low, err
}

// GetDailyBias fetches manual market bias configured for a given date
func (d *Database) GetDailyBias(ctx context.Context, date time.Time) (string, error) {
	query := `SELECT bias FROM daily_market_bias WHERE date = $1`
	var bias string
	err := d.conn.QueryRowContext(ctx, query, date.Format("2006-01-02")).Scan(&bias)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return bias, err
}

// SaveDailyBias stores or updates the manual market bias configured for a given date
func (d *Database) SaveDailyBias(ctx context.Context, date time.Time, bias string) error {
	query := `
		INSERT INTO daily_market_bias (date, bias, updated_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP)
		ON CONFLICT (date) DO UPDATE
		SET bias = EXCLUDED.bias, updated_at = CURRENT_TIMESTAMP
	`
	_, err := d.conn.ExecContext(ctx, query, date.Format("2006-01-02"), bias)
	return err
}

// DeleteDailyBias deletes the manual market bias configured for a given date
func (d *Database) DeleteDailyBias(ctx context.Context, date time.Time) error {
	query := `DELETE FROM daily_market_bias WHERE date = $1`
	_, err := d.conn.ExecContext(ctx, query, date.Format("2006-01-02"))
	return err
}

// GetDailyManualWatchlist fetches manual stock symbols configured for a given date
func (d *Database) GetDailyManualWatchlist(ctx context.Context, date time.Time) ([]string, error) {
	query := `SELECT symbols FROM daily_manual_watchlist WHERE date = $1`
	var symbolsStr string
	err := d.conn.QueryRowContext(ctx, query, date.Format("2006-01-02")).Scan(&symbolsStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Parse CSV and clean spaces
	var symbols []string
	var current string
	for i := 0; i < len(symbolsStr); i++ {
		c := symbolsStr[i]
		if c == ',' {
			if len(current) > 0 {
				symbols = append(symbols, current)
				current = ""
			}
		} else {
			if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
				current += string(c)
			}
		}
	}
	if len(current) > 0 {
		symbols = append(symbols, current)
	}

	return symbols, nil
}

// SaveDailyManualWatchlist stores or updates the manual stock symbols configured for a given date
func (d *Database) SaveDailyManualWatchlist(ctx context.Context, date time.Time, symbols string) error {
	query := `
		INSERT INTO daily_manual_watchlist (date, symbols, updated_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP)
		ON CONFLICT (date) DO UPDATE
		SET symbols = EXCLUDED.symbols, updated_at = CURRENT_TIMESTAMP
	`
	_, err := d.conn.ExecContext(ctx, query, date.Format("2006-01-02"), symbols)
	return err
}

// DeleteDailyManualWatchlist deletes the manual stock symbols configured for a given date
func (d *Database) DeleteDailyManualWatchlist(ctx context.Context, date time.Time) error {
	query := `DELETE FROM daily_manual_watchlist WHERE date = $1`
	_, err := d.conn.ExecContext(ctx, query, date.Format("2006-01-02"))
	return err
}

// SaveHistoricalCandles inserts historical candles into the specified database table
func (d *Database) SaveHistoricalCandles(ctx context.Context, token int64, candles []kiteconnect.HistoricalData, tableName string) error {
	query := `
		INSERT INTO ` + tableName + ` (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (token, time) DO UPDATE SET
			close = EXCLUDED.close,
			high = EXCLUDED.high,
			low = EXCLUDED.low,
			volume = EXCLUDED.volume,
			vwap = EXCLUDED.vwap,
			color = EXCLUDED.color
	`

	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range candles {
		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}
		vwap := (c.Open + c.High + c.Low + c.Close) / 4.0

		// Bid, Ask, TickCount are not provided by historical data, we default them
		_, err = stmt.ExecContext(ctx, token, c.Date.Time, c.Open, c.High, c.Low, c.Close, int64(c.Volume), vwap, c.Low, c.High, 100, color)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}
