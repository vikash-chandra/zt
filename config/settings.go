package config

import (
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// Settings holds all configuration for the trading bot
type Settings struct {
	// Zerodha Kite API
	APIKey      string
	APISecret   string
	UserID      string
	AccessToken string
	RedirectURL string

	// Database
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string



	// Trading Parameters
	InitialCapital     float64
	MaxDailyLossPct    float64
	MaxLossAmount      float64
	MaxPositionSize    float64
	MaxTradesPerDay    int
	MaxLossStreaks     int
	MaxQtyPerOrder     int
	MinProfitTargetPct float64
	MaxHoldingTimeMin  int
	SLBufferPct        float64
	WatchlistMaxPctChange float64
	MaxCapitalPerTrade float64

	TradeStartTime        string
	TradeEndTime          string

	// Market Hours
	MarketOpenTime  time.Time
	MarketCloseTime time.Time

	// Strategy
	StrategyType      string
	CandleIntervalSec int
	VWAPWindow        int
	ATRPeriod         int
	OBIWindow         int

	// Monitoring
	LogLevel              string
	PrometheusAddr        string
	HealthCheckInterval   time.Duration
	MarginCheckInterval   time.Duration
	PositionCheckInterval time.Duration
}

// Load loads settings from environment variables
func Load() (*Settings, error) {
	// Load .env file if exists
	_ = godotenv.Load()

	return &Settings{
		// Zerodha
		APIKey:      os.Getenv("KITE_API_KEY"),
		APISecret:   os.Getenv("KITE_API_SECRET"),
		UserID:      os.Getenv("KITE_USER_ID"),
		AccessToken: os.Getenv("KITE_ACCESS_TOKEN"),
		RedirectURL: getEnvOrDefault("KITE_REDIRECT_URL", "http://localhost:8080/callback"),

		// Database
		DBHost:     getEnvOrDefault("DB_HOST", "localhost"),
		DBPort:     getEnvOrDefaultInt("DB_PORT", 5432),
		DBUser:     getEnvOrDefault("DB_USER", "postgres"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBName:     getEnvOrDefault("DB_NAME", "zerodha_trading"),
		DBSSLMode:  getEnvOrDefault("DB_SSL_MODE", "disable"),



		// Trading
		InitialCapital:     getEnvOrDefaultFloat("INITIAL_CAPITAL", 500000),
		MaxDailyLossPct:    getEnvOrDefaultFloat("MAX_DAILY_LOSS_PCT", 2.0),
		MaxLossAmount:      getEnvOrDefaultFloat("MAX_LOSS_AMOUNT", 10000),
		MaxPositionSize:    getEnvOrDefaultFloat("MAX_POSITION_SIZE", 100000),
		MaxTradesPerDay:    getEnvOrDefaultInt("MAX_TRADES_PER_DAY", 20),
		MaxLossStreaks:     getEnvOrDefaultInt("MAX_LOSS_STREAKS", 3),
		MaxQtyPerOrder:     getEnvOrDefaultInt("MAX_QTY_PER_ORDER", 5000),
		MinProfitTargetPct: getEnvOrDefaultFloat("MIN_PROFIT_TARGET_PCT", 0.5),
		MaxHoldingTimeMin:  getEnvOrDefaultInt("MAX_HOLDING_TIME_MIN", 30),
		SLBufferPct:        getEnvOrDefaultFloat("SL_BUFFER_PCT", 0.0),
		WatchlistMaxPctChange: getEnvOrDefaultFloat("WATCHLIST_MAX_PCT_CHANGE", 100.0),
		MaxCapitalPerTrade: getEnvOrDefaultFloat("MAX_CAPITAL_PER_TRADE", 20000.0),
		TradeStartTime:     getEnvOrDefault("TRADE_START_TIME", "09:30"),
		TradeEndTime:       getEnvOrDefault("TRADE_END_TIME", "10:45"),

		// Market hours (9:15 AM - 3:30 PM IST)
		MarketOpenTime:  time.Date(2020, 1, 1, 9, 15, 0, 0, time.UTC),
		MarketCloseTime: time.Date(2020, 1, 1, 15, 30, 0, 0, time.UTC),

		// Strategy
		StrategyType:      getEnvOrDefault("STRATEGY_TYPE", "VWAP_RSI"),
		CandleIntervalSec: 300, // 5 minutes
		VWAPWindow:        50,  // 50 candles
		ATRPeriod:         14,  // Standard ATR
		OBIWindow:         5,   // 5 ticks

		// Monitoring
		LogLevel:              getEnvOrDefault("LOG_LEVEL", "info"),
		PrometheusAddr:        getEnvOrDefault("PROMETHEUS_ADDR", ":8888"),
		HealthCheckInterval:   10 * time.Second,
		MarginCheckInterval:   5 * time.Minute,
		PositionCheckInterval: 2 * time.Second,
	}, nil
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvOrDefaultInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultVal
}

func getEnvOrDefaultFloat(key string, defaultVal float64) float64 {
	if val := os.Getenv(key); val != "" {
		if floatVal, err := strconv.ParseFloat(val, 64); err == nil {
			return floatVal
		}
	}
	return defaultVal
}
