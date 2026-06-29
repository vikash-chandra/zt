package data

import (
	"context"
	"database/sql"
	"fmt"

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

	logger.Info("Database connected", zap.String("host", host))

	return &Database{conn: conn, logger: logger}, nil
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
	`

	if _, err := d.conn.Exec(schema); err != nil {
		return err
	}

	// Migrations: ensure strategy columns exist for backward compatibility with active DB instances
	_, _ = d.conn.Exec("ALTER TABLE orders ADD COLUMN IF NOT EXISTS strategy VARCHAR(50) DEFAULT 'LOW_VOLUME'")
	_, _ = d.conn.Exec("ALTER TABLE positions ADD COLUMN IF NOT EXISTS strategy VARCHAR(50) DEFAULT 'LOW_VOLUME'")
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
