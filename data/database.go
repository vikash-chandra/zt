package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	_ "github.com/lib/pq"
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

	_, _ = conn.Exec("SET timezone = 'Asia/Kolkata';")
	_, _ = conn.Exec("ALTER DATABASE zerodha_trading SET timezone TO 'Asia/Kolkata';")

	logger.Info("Database connected with Asia/Kolkata IST timezone", zap.String("host", host))

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
		time TIMESTAMPTZ NOT NULL,
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
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (token, time)
	) WITH (OIDS=FALSE);

	CREATE INDEX IF NOT EXISTS idx_candles_5m_token_time ON candles_5m (token, time DESC);

	CREATE TABLE IF NOT EXISTS candles_1m (
		token BIGINT NOT NULL,
		time TIMESTAMPTZ NOT NULL,
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
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
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
		placed_at TIMESTAMPTZ NOT NULL,
		updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
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
		created_at TIMESTAMPTZ NOT NULL,
		closed_at TIMESTAMPTZ,
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
		entry_time TIMESTAMPTZ,
		exit_time TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		strategy VARCHAR(50) DEFAULT 'LOW_VOLUME',
		expiry_date DATE
	);
	ALTER TABLE trades ADD COLUMN IF NOT EXISTS expiry_date DATE;

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

	CREATE TABLE IF NOT EXISTS candles_1d (
		time TIMESTAMP NOT NULL,
		token BIGINT NOT NULL,
		open DOUBLE PRECISION NOT NULL,
		high DOUBLE PRECISION NOT NULL,
		low DOUBLE PRECISION NOT NULL,
		close DOUBLE PRECISION NOT NULL,
		volume BIGINT NOT NULL,
		color VARCHAR(10) NOT NULL,
		PRIMARY KEY (token, time)
	);

	CREATE TABLE IF NOT EXISTS options_index_configs (
		index_symbol VARCHAR(32) PRIMARY KEY,
		is_active BOOLEAN DEFAULT true,
		is_live BOOLEAN DEFAULT false,
		base_lot_size INT NOT NULL,
		max_multiplier INT DEFAULT 4,
		multiplier_on_reversal BOOLEAN DEFAULT true,
		target_entry_premium DOUBLE PRECISION DEFAULT 100.0,
		expiry_type VARCHAR(16) DEFAULT 'MONTHLY',
		next_month_days INT DEFAULT 3,
		sl_pct DOUBLE PRECISION DEFAULT 50.0,
		trail_sl_enabled BOOLEAN DEFAULT true,
		trail_sl_pct DOUBLE PRECISION DEFAULT 20.0,
		st1_period INT DEFAULT 10,
		st1_multiplier DOUBLE PRECISION DEFAULT 4.0,
		st2_period INT DEFAULT 7,
		st2_multiplier DOUBLE PRECISION DEFAULT 3.0,
		st3_period INT DEFAULT 7,
		st3_multiplier DOUBLE PRECISION DEFAULT 2.0,
		last_new_trade_time VARCHAR(16) DEFAULT '14:30:00',
		auto_square_off_time VARCHAR(16) DEFAULT '15:15:00',
		supertrend_cutoff_time VARCHAR(16) DEFAULT '15:15:00',
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS app_system_configs (
		category VARCHAR(32) NOT NULL,
		config_key VARCHAR(64) NOT NULL,
		config_value TEXT NOT NULL,
		description TEXT,
		updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (category, config_key)
	);

	CREATE TABLE IF NOT EXISTS quant_scanner_results (
		id SERIAL PRIMARY KEY,
		scan_date DATE NOT NULL DEFAULT CURRENT_DATE,
		symbol VARCHAR(32) NOT NULL,
		breakout_type VARCHAR(32) NOT NULL,
		direction VARCHAR(32) NOT NULL,
		momentum_days INT NOT NULL,
		pct_change_1d DOUBLE PRECISION NOT NULL,
		pct_change_3d DOUBLE PRECISION NOT NULL,
		range_pct_change DOUBLE PRECISION NOT NULL,
		volume_1d BIGINT NOT NULL DEFAULT 0,
		volume_adv BIGINT NOT NULL DEFAULT 0,
		volume_multiplier DOUBLE PRECISION NOT NULL DEFAULT 1.0,
		confidence_score DOUBLE PRECISION NOT NULL,
		quant_direction VARCHAR(32) NOT NULL,
		recommended_action VARCHAR(64) DEFAULT '',
		news_summary TEXT,
		news_sentiment VARCHAR(16),
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	ALTER TABLE quant_scanner_results ADD COLUMN IF NOT EXISTS scan_date DATE NOT NULL DEFAULT CURRENT_DATE;
	DROP INDEX IF EXISTS idx_quant_scanner_symbol_unique;
	ALTER TABLE quant_scanner_results DROP CONSTRAINT IF EXISTS quant_scanner_results_symbol_key;
	CREATE UNIQUE INDEX IF NOT EXISTS idx_quant_scanner_date_symbol_unique ON quant_scanner_results (scan_date, symbol);

	CREATE TABLE IF NOT EXISTS pre_selection_results (
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

	CREATE TABLE IF NOT EXISTS selected_sectors (
		date DATE NOT NULL,
		sector VARCHAR(50) NOT NULL,
		pct_change DOUBLE PRECISION NOT NULL,
		selected_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (date, sector)
	);

	CREATE TABLE IF NOT EXISTS options_bot_state (
		index_symbol VARCHAR(50) PRIMARY KEY,
		multiplier INT NOT NULL DEFAULT 1,
		last_trend VARCHAR(20) NOT NULL DEFAULT 'NEUTRAL',
		sl_stopped_trend VARCHAR(20) NOT NULL DEFAULT '',
		awaiting_reversal BOOLEAN NOT NULL DEFAULT FALSE,
		active_trade_id BIGINT DEFAULT 0,
		active_order_id VARCHAR(50) DEFAULT '',
		active_symbol VARCHAR(50) DEFAULT '',
		active_side VARCHAR(10) DEFAULT '',
		active_qty INT DEFAULT 0,
		entry_premium DOUBLE PRECISION DEFAULT 0,
		sl_price DOUBLE PRECISION DEFAULT 0,
		paper_balance DOUBLE PRECISION DEFAULT 1000000.0,
		active_created_at TIMESTAMPTZ,
		updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS daily_watchlists (
		date DATE NOT NULL,
		symbol VARCHAR(20) NOT NULL,
		token BIGINT NOT NULL,
		selectors VARCHAR(200) NOT NULL,
		PRIMARY KEY (date, symbol)
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
	_, _ = d.conn.Exec("ALTER TABLE trades ADD COLUMN IF NOT EXISTS entry_time TIMESTAMPTZ")
	_, _ = d.conn.Exec("ALTER TABLE trades ADD COLUMN IF NOT EXISTS exit_time TIMESTAMPTZ")
	_, _ = d.conn.Exec("ALTER TABLE trades ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP")
	_, _ = d.conn.Exec("ALTER TABLE trades ADD COLUMN IF NOT EXISTS status VARCHAR(32) DEFAULT 'CLOSED'")
	_, _ = d.conn.Exec("ALTER TABLE trades ALTER COLUMN exit_price DROP NOT NULL")
	_, _ = d.conn.Exec("ALTER TABLE options_bot_state ADD COLUMN IF NOT EXISTS active_created_at TIMESTAMPTZ")
	_, _ = d.conn.Exec("ALTER TABLE options_bot_state ADD COLUMN IF NOT EXISTS active_trade_id BIGINT DEFAULT 0")
	_, _ = d.conn.Exec("ALTER TABLE options_bot_state ADD COLUMN IF NOT EXISTS index_symbol VARCHAR(50) DEFAULT 'NIFTY 50'")
	_, _ = d.conn.Exec("ALTER TABLE options_bot_state DROP CONSTRAINT IF EXISTS single_row")
	_, _ = d.conn.Exec("ALTER TABLE options_bot_state DROP CONSTRAINT IF EXISTS options_bot_state_pkey")
	_, _ = d.conn.Exec("ALTER TABLE options_bot_state DROP COLUMN IF EXISTS id")
	_, _ = d.conn.Exec("DROP INDEX IF EXISTS idx_options_bot_state_index")
	_, _ = d.conn.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_options_bot_state_index ON options_bot_state (index_symbol)")
	_, _ = d.conn.Exec("ALTER TABLE options_bot_state ADD CONSTRAINT pk_options_bot_state_index PRIMARY KEY (index_symbol)")

	// TIMESTAMPTZ Migrations: convert legacy TIMESTAMP columns to TIMESTAMPTZ with explicit Asia/Kolkata timezone
	_, _ = d.conn.Exec("ALTER TABLE trades ALTER COLUMN entry_time TYPE TIMESTAMPTZ USING entry_time AT TIME ZONE 'Asia/Kolkata'")
	_, _ = d.conn.Exec("ALTER TABLE trades ALTER COLUMN exit_time TYPE TIMESTAMPTZ USING exit_time AT TIME ZONE 'Asia/Kolkata'")
	_, _ = d.conn.Exec("ALTER TABLE trades ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Kolkata'")
	_, _ = d.conn.Exec("ALTER TABLE trades ALTER COLUMN updated_at TYPE TIMESTAMPTZ USING updated_at AT TIME ZONE 'Asia/Kolkata'")

	_, _ = d.conn.Exec("ALTER TABLE positions ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Kolkata'")
	_, _ = d.conn.Exec("ALTER TABLE positions ALTER COLUMN closed_at TYPE TIMESTAMPTZ USING closed_at AT TIME ZONE 'Asia/Kolkata'")

	_, _ = d.conn.Exec("ALTER TABLE candles_5m ALTER COLUMN time TYPE TIMESTAMPTZ USING time AT TIME ZONE 'Asia/Kolkata'")
	_, _ = d.conn.Exec("ALTER TABLE candles_5m ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Kolkata'")

	_, _ = d.conn.Exec("ALTER TABLE candles_1m ALTER COLUMN time TYPE TIMESTAMPTZ USING time AT TIME ZONE 'Asia/Kolkata'")
	_, _ = d.conn.Exec("ALTER TABLE candles_1m ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Kolkata'")

	_, _ = d.conn.Exec("ALTER TABLE orders ALTER COLUMN placed_at TYPE TIMESTAMPTZ USING placed_at AT TIME ZONE 'Asia/Kolkata'")
	_, _ = d.conn.Exec("ALTER TABLE orders ALTER COLUMN updated_at TYPE TIMESTAMPTZ USING updated_at AT TIME ZONE 'Asia/Kolkata'")
	_, _ = d.conn.Exec("ALTER TABLE quant_scanner_results ADD COLUMN IF NOT EXISTS yearly_high DOUBLE PRECISION DEFAULT 0")
	_, _ = d.conn.Exec("ALTER TABLE quant_scanner_results ADD COLUMN IF NOT EXISTS yearly_low DOUBLE PRECISION DEFAULT 0")
	_, _ = d.conn.Exec("ALTER TABLE quant_scanner_results ADD COLUMN IF NOT EXISTS monthly_high DOUBLE PRECISION DEFAULT 0")
	_, _ = d.conn.Exec("ALTER TABLE quant_scanner_results ADD COLUMN IF NOT EXISTS monthly_low DOUBLE PRECISION DEFAULT 0")
	_, _ = d.conn.Exec("ALTER TABLE quant_scanner_results ADD COLUMN IF NOT EXISTS weekly_high DOUBLE PRECISION DEFAULT 0")
	_, _ = d.conn.Exec("ALTER TABLE quant_scanner_results ADD COLUMN IF NOT EXISTS weekly_low DOUBLE PRECISION DEFAULT 0")
	_, _ = d.conn.Exec("ALTER TABLE quant_scanner_results ADD COLUMN IF NOT EXISTS all_time_high DOUBLE PRECISION DEFAULT 0")
	_, _ = d.conn.Exec("ALTER TABLE quant_scanner_results ADD COLUMN IF NOT EXISTS all_time_low DOUBLE PRECISION DEFAULT 0")
	_, _ = d.conn.Exec("ALTER TABLE quant_scanner_results ADD COLUMN IF NOT EXISTS segment VARCHAR(32) DEFAULT 'CASH'")

	// Database Audit Optimization: High-performance composite indexes
	_, _ = d.conn.Exec("CREATE INDEX IF NOT EXISTS idx_quant_scanner_lookup ON quant_scanner_results (scan_date DESC, confidence_score DESC)")
	_, _ = d.conn.Exec("CREATE INDEX IF NOT EXISTS idx_quant_scanner_segment ON quant_scanner_results (scan_date, segment)")
	_, _ = d.conn.Exec("CREATE INDEX IF NOT EXISTS idx_candles_1d_token_time ON candles_1d (token, time DESC)")
	_, _ = d.conn.Exec("CREATE INDEX IF NOT EXISTS idx_trades_strategy_created ON trades (strategy, created_at DESC)")
	_, _ = d.conn.Exec("CREATE INDEX IF NOT EXISTS idx_trades_symbol ON trades (symbol)")
	_, _ = d.conn.Exec("CREATE INDEX IF NOT EXISTS idx_positions_order_id ON positions (order_id)")
	_, _ = d.conn.Exec("CREATE INDEX IF NOT EXISTS idx_options_bot_state_index ON options_bot_state (index_symbol)")
	_, _ = d.conn.Exec("ALTER TABLE options_index_configs DROP COLUMN IF EXISTS strike_offset_points")
	_, _ = d.conn.Exec("ALTER TABLE options_index_configs ALTER COLUMN last_new_trade_time TYPE VARCHAR(16)")
	_, _ = d.conn.Exec("ALTER TABLE options_index_configs ALTER COLUMN auto_square_off_time TYPE VARCHAR(16)")
	_, _ = d.conn.Exec("ALTER TABLE options_index_configs ALTER COLUMN supertrend_cutoff_time TYPE VARCHAR(16)")

	// Seed default options index configs if table is empty
	var optCfgCount int
	if err := d.conn.QueryRow("SELECT COUNT(*) FROM options_index_configs").Scan(&optCfgCount); err == nil && optCfgCount == 0 {
		defaultOptConfigs := []struct {
			Symbol, ExpiryType, LastTrade, AutoSquare, STCutoff string
			BaseLot, MaxMult, NextMonthDays, ST1P, ST2P, ST3P   int
			TargetPrem, SLPct, TrailSLPct                       float64
			ST1M, ST2M, ST3M                                    float64
			IsActive, IsLive, MultOnRev, TrailSLEnabled         bool
		}{
			{"NIFTY 50", "MONTHLY", "14:30:00", "15:15:00", "15:15:00", 65, 4, 3, 10, 7, 7, 100.0, 50.0, 20.0, 4.0, 3.0, 2.0, true, false, true, true},
			{"NIFTY BANK", "MONTHLY", "14:30:00", "15:15:00", "15:15:00", 15, 4, 3, 10, 7, 7, 250.0, 50.0, 20.0, 4.0, 3.0, 2.0, true, false, true, true},
			{"BSE SENSEX", "MONTHLY", "14:30:00", "15:15:00", "15:15:00", 20, 4, 3, 10, 7, 7, 250.0, 50.0, 20.0, 4.0, 3.0, 2.0, true, false, true, true},
			{"FINNIFTY", "MONTHLY", "14:30:00", "15:15:00", "15:15:00", 65, 4, 3, 10, 7, 7, 100.0, 50.0, 20.0, 4.0, 3.0, 2.0, true, false, true, true},
			{"MIDCPNIFTY", "MONTHLY", "14:30:00", "15:15:00", "15:15:00", 120, 4, 3, 10, 7, 7, 80.0, 50.0, 20.0, 4.0, 3.0, 2.0, true, false, true, true},
		}

		for _, row := range defaultOptConfigs {
			_, _ = d.conn.Exec(`
				INSERT INTO options_index_configs (
					index_symbol, is_active, is_live, base_lot_size, max_multiplier, multiplier_on_reversal,
					target_entry_premium, expiry_type, next_month_days, sl_pct,
					trail_sl_enabled, trail_sl_pct, st1_period, st1_multiplier, st2_period, st2_multiplier,
					st3_period, st3_multiplier, last_new_trade_time, auto_square_off_time, supertrend_cutoff_time
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
				ON CONFLICT (index_symbol) DO NOTHING
			`, row.Symbol, row.IsActive, row.IsLive, row.BaseLot, row.MaxMult, row.MultOnRev,
				row.TargetPrem, row.ExpiryType, row.NextMonthDays, row.SLPct,
				row.TrailSLEnabled, row.TrailSLPct, row.ST1P, row.ST1M, row.ST2P, row.ST2M,
				row.ST3P, row.ST3M, row.LastTrade, row.AutoSquare, row.STCutoff)
		}
	}

	// Seed default system configs if table is empty
	var sysCfgCount int
	if err := d.conn.QueryRow("SELECT COUNT(*) FROM app_system_configs").Scan(&sysCfgCount); err == nil && sysCfgCount == 0 {
		defaultSysConfigs := []struct {
			Category, Key, Value, Description string
		}{
			{"EQUITY_STRATEGY", "active_strategies", "LOW_VOLUME,VANDE_BHARAT,OPTIONS_SUPERTREND", "Active trading strategies"},
			{"EQUITY_STRATEGY", "enable_live_trading", "false", "Enable live broker execution for equity strategies"},
			{"EQUITY_STRATEGY", "risk_per_trade_inr", "500.0", "Risk per equity trade in INR"},
			{"EQUITY_STRATEGY", "capital_inr", "100000.0", "Total allocated capital in INR"},
			{"EQUITY_STRATEGY", "target_profit_pct", "2.0", "Target profit percentage for equity"},
			{"EQUITY_STRATEGY", "stop_loss_pct", "1.5", "Fixed stop-loss percentage for equity"},
			{"EQUITY_STRATEGY", "trailing_stop_loss", "true", "Enable high-water mark multi-stage trailing SL"},
			{"EQUITY_STRATEGY", "max_open_positions", "3", "Maximum concurrent open equity positions"},
			{"EQUITY_STRATEGY", "auto_square_off_time", "15:15", "Market-close hard square-off time (IST)"},
			{"EQUITY_STRATEGY", "low_volume_min_candles_to_ignore", "0", "Min initial candles to ignore for Low Volume"},
			{"SELECTION", "pre_selection_strategy", "FO", "Stock selection algorithm (FO, SECTOR, COMBINED, MANUAL)"},
			{"SELECTION", "sector_scanner_enabled", "true", "Enable sector momentum scanner"},
			{"SELECTION", "sector_scanner_top_n", "3", "Number of top performing sectors to allocate"},
			{"SELECTION", "sector_scanner_weight", "0.40", "Weight of sector ranking in stock scoring"},
			{"SELECTION", "strategy_watchlist_size", "10", "Number of stocks selected in morning watchlist"},
			{"SELECTION", "watchlist_max_pct_change", "5.0", "Maximum open gap percentage to consider"},
			{"QUANT_SCANNER", "enabled", "true", "Enable automated daily quant stock scanner"},
			{"QUANT_SCANNER", "execution_time", "15:45", "Daily scanner execution time (IST)"},
			{"QUANT_SCANNER", "momentum_days", "20", "Momentum lookback days for technical scans"},
			{"QUANT_SCANNER", "news_enabled", "false", "Enable sentiment news filter"},
			{"SYSTEM", "restart_allowed_before", "09:15", "Pre-market cutoff for UI bot restarts (IST)"},
			{"SYSTEM", "restart_allowed_after", "15:45", "Post-market cutoff for UI bot restarts (IST)"},
		}

		for _, row := range defaultSysConfigs {
			_, _ = d.conn.Exec(`
				INSERT INTO app_system_configs (category, config_key, config_value, description)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (category, config_key) DO NOTHING
			`, row.Category, row.Key, row.Value, row.Description)
		}
	}

	// Auto Data Pruning: Retain only necessary active data (14-day window for scanner & 1m candles)
	_, _ = d.conn.Exec("DELETE FROM quant_scanner_results WHERE scan_date < CURRENT_DATE - INTERVAL '14 days'")
	_, _ = d.conn.Exec("DELETE FROM candles_1m WHERE time < NOW() - INTERVAL '14 days'")
	_, _ = d.conn.Exec("DELETE FROM trades WHERE quantity <= 0")
	_ = d.CleanupDuplicateLiveTrades(context.Background())

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

// QueryContext executes a query with context
func (d *Database) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return d.conn.QueryContext(ctx, query, args...)
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

	var err error
	now := time.Now().In(ISTLocation)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, ISTLocation)

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

// normalizeCandleTime normalizes timezones between seeded UTC-named times and live UTC times.
func normalizeCandleTime(t time.Time) time.Time {
	return NormalizeToIST(t)
}

// IsMarketHoursCandle returns true if the candle timestamp (in IST) is strictly within trading hours [09:15, 15:30)
func IsMarketHoursCandle(t time.Time) bool {
	tIST := normalizeCandleTime(t).In(ISTLocation)
	h, m := tIST.Hour(), tIST.Minute()
	timeNum := h*100 + m

	// Strictly ignore any candle before 09:15 AM (< 0915) and at/after 03:30 PM (>= 1530)
	return timeNum >= 915 && timeNum < 1530
}

// GetCandlesForDay gets candles for a token since start of day
func (d *Database) GetCandlesForDay(ctx context.Context, token int64, todayStart time.Time) ([]CandleRecord, error) {
	tLoc := todayStart.In(ISTLocation)
	startOfDay := time.Date(tLoc.Year(), tLoc.Month(), tLoc.Day(), 0, 0, 0, 0, ISTLocation).UTC()
	endOfDay := startOfDay.Add(24 * time.Hour)

	rows, err := d.conn.QueryContext(ctx,
		"SELECT time, open, high, low, close, volume FROM candles_5m WHERE token = $1 AND time >= $2 AND time < $3",
		token, startOfDay, endOfDay,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Use map to de-duplicate by normalized local time
	candleMap := make(map[int64]CandleRecord)
	for rows.Next() {
		var t time.Time
		var o, h, l, c float64
		var v int64
		if err := rows.Scan(&t, &o, &h, &l, &c, &v); err != nil {
			continue
		}
		if !IsMarketHoursCandle(t) {
			continue
		}
		normTime := normalizeCandleTime(t)
		normUnix := normTime.Unix()

		if existing, exists := candleMap[normUnix]; !exists || v >= existing.Volume {
			candleMap[normUnix] = CandleRecord{
				Time:   normTime,
				Open:   o,
				High:   h,
				Low:    l,
				Close:  c,
				Volume: v,
			}
		}
	}

	list := make([]CandleRecord, 0, len(candleMap))
	for _, c := range candleMap {
		list = append(list, c)
	}

	// Sort chronologically by normalized time
	sort.Slice(list, func(i, j int) bool {
		return list[i].Time.Before(list[j].Time)
	})

	return list, nil
}

// GetHistoricalCandlesBeforeDate gets up to maxCount candles for a token strictly prior to dayStart (ordered chronologically ASC)
func (d *Database) GetHistoricalCandlesBeforeDate(ctx context.Context, token int64, dayStart time.Time, maxCount int) ([]CandleRecord, error) {
	rows, err := d.conn.QueryContext(ctx,
		"SELECT time, open, high, low, close, volume FROM candles_5m WHERE token = $1 AND time < $2 ORDER BY time DESC LIMIT $3",
		token, dayStart, maxCount,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candles []CandleRecord
	for rows.Next() {
		var t time.Time
		var o, h, l, c float64
		var v int64
		if err := rows.Scan(&t, &o, &h, &l, &c, &v); err != nil {
			continue
		}
		if !IsMarketHoursCandle(t) {
			continue
		}
		normTime := normalizeCandleTime(t)
		candles = append(candles, CandleRecord{
			Time:   normTime,
			Open:   o,
			High:   h,
			Low:    l,
			Close:  c,
			Volume: v,
		})
	}

	// Reverse to chronological order (oldest first)
	for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
		candles[i], candles[j] = candles[j], candles[i]
	}

	return candles, nil
}

// GetCandlesForDate gets candles for a token for a specific 24-hour day window
func (d *Database) GetCandlesForDate(ctx context.Context, token int64, dayStart time.Time) ([]CandleRecord, error) {
	rows, err := d.conn.QueryContext(ctx,
		"SELECT time, open, high, low, close, volume FROM candles_5m WHERE token = $1 AND time >= $2 AND time < $2 + INTERVAL '24 hours'",
		token, dayStart,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Use map to de-duplicate by normalized local time
	candleMap := make(map[int64]CandleRecord)
	for rows.Next() {
		var t time.Time
		var o, h, l, c float64
		var v int64
		if err := rows.Scan(&t, &o, &h, &l, &c, &v); err != nil {
			continue
		}
		if !IsMarketHoursCandle(t) {
			continue
		}
		normTime := normalizeCandleTime(t)
		normUnix := normTime.Unix()

		if existing, exists := candleMap[normUnix]; !exists || v >= existing.Volume {
			candleMap[normUnix] = CandleRecord{
				Time:   normTime,
				Open:   o,
				High:   h,
				Low:    l,
				Close:  c,
				Volume: v,
			}
		}
	}

	list := make([]CandleRecord, 0, len(candleMap))
	for _, c := range candleMap {
		list = append(list, c)
	}

	// Sort chronologically by normalized time
	sort.Slice(list, func(i, j int) bool {
		return list[i].Time.Before(list[j].Time)
	})

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
		"SELECT placed_at, transaction_type, COALESCE(average_price, 0.0), COALESCE(quantity, 0) FROM orders WHERE symbol = $1 AND status = 'COMPLETE' AND placed_at >= $2 ORDER BY placed_at ASC",
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
	EntryTime       time.Time `json:"entry_time"`
	ExitTime        time.Time `json:"exit_time"`
	CreatedAt       time.Time `json:"created_at"`
	Strategy        string    `json:"strategy"`
	Status          string    `json:"status"`
	ExpiryDate      string    `json:"expiry_date"`
}

// GetAllTradesHistory loads all trades from database
func (d *Database) GetAllTradesHistory(ctx context.Context) ([]TradeHistoryRecord, error) {
	rows, err := d.conn.QueryContext(ctx,
		"SELECT id, symbol, entry_price, exit_price, quantity, pnl, side, COALESCE(time_held_minutes, 0), COALESCE(entry_time, created_at), exit_time, created_at, COALESCE(strategy, 'LOW_VOLUME'), COALESCE(status, 'CLOSED'), COALESCE(expiry_date::text, '') FROM trades ORDER BY created_at DESC",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []TradeHistoryRecord
	for rows.Next() {
		var tr TradeHistoryRecord
		var exitPrice sql.NullFloat64
		var exitTime sql.NullTime
		err := rows.Scan(
			&tr.ID,
			&tr.Symbol,
			&tr.EntryPrice,
			&exitPrice,
			&tr.Quantity,
			&tr.PnL,
			&tr.Side,
			&tr.TimeHeldMinutes,
			&tr.EntryTime,
			&exitTime,
			&tr.CreatedAt,
			&tr.Strategy,
			&tr.Status,
			&tr.ExpiryDate,
		)
		if err != nil {
			continue
		}
		if exitPrice.Valid {
			tr.ExitPrice = exitPrice.Float64
		} else {
			tr.ExitPrice = tr.EntryPrice
		}
		if exitTime.Valid {
			tr.ExitTime = NormalizeToIST(exitTime.Time)
		}
		tr.EntryTime = NormalizeToIST(tr.EntryTime)
		tr.CreatedAt = NormalizeToIST(tr.CreatedAt)
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
func (d *Database) SaveHistoricalCandles(ctx context.Context, token int64, candles []HistoricalData, tableName string) error {
	query := `
		INSERT INTO ` + tableName + ` (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (token, time) DO UPDATE SET
			open = EXCLUDED.open,
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
		// Strictly ignore saving any candle before 09:15 AM and at/after 03:30 PM (15:30) IST
		if !IsMarketHoursCandle(c.Date) {
			continue
		}

		color := "DOJI"
		if c.Close > c.Open {
			color = "GREEN"
		} else if c.Close < c.Open {
			color = "RED"
		}
		vwap := (c.Open + c.High + c.Low + c.Close) / 4.0
		localTime := NormalizeToIST(c.Date)
		utcTime := localTime.UTC()

		// Bid, Ask, TickCount are not provided by historical data, we default them
		_, err = stmt.ExecContext(ctx, token, utcTime, c.Open, c.High, c.Low, c.Close, int64(c.Volume), vwap, c.Low, c.High, 100, color)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}
