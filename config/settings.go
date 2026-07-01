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
	InitialCapital        float64
	MaxDailyLossAmount    float64
	MaxTradesPerDay       int
	MaxLossStreaks        int
	MaxHoldingTimeMin     int
	SLBufferPct           float64
	VBSLBufferPct         float64
	WatchlistMaxPctChange float64
	MaxCapitalPerTrade    float64

	TradeStartTime  string
	TradeEndTime    string
	StockSelectTime string
	WatchlistSize   int

	// Market Hours
	MarketOpenTime  time.Time
	MarketCloseTime time.Time

	// Strategy
	StrategyType        string
	ActiveStrategies    string
	ActiveSelectors     string
	StrategySelectorMap string
	RiskRewardType      string
	RiskRewardRatio     float64
	SectorMaxBuyPct     float64
	SectorMaxSellPct    float64
	StockMaxBuyPct      float64
	StockMaxSellPct     float64
	VBMasterMaxPct      float64
	VBConfirmMaxPct     float64
	VBTradeStartTime    string
	VBTradeEndTime      string
	CandleIntervalSec   int
	VWAPWindow          int
	ATRPeriod           int
	OBIWindow           int
	DefaultOrderType    string

	// Monitoring
	LogLevel              string
	PrometheusAddr        string
	HealthCheckInterval   time.Duration
	MarginCheckInterval   time.Duration
	PositionCheckInterval time.Duration

	// Live Trading mode
	LiveTrading           bool
	SquareOffOnShutdown   bool
	LVUseBrokerSL         bool
	VBUseBrokerSL         bool
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
		InitialCapital:        getEnvOrDefaultFloat("INITIAL_CAPITAL", 500000),
		MaxDailyLossAmount:    getEnvOrDefaultFloat("MAX_DAILY_LOSS_AMOUNT", 0),
		MaxTradesPerDay:       getEnvOrDefaultInt("MAX_TRADES_PER_DAY", 20),
		MaxLossStreaks:        getEnvOrDefaultInt("MAX_LOSS_STREAKS", 3),
		MaxHoldingTimeMin:     getEnvOrDefaultInt("MAX_HOLDING_TIME_MIN", 30),
		SLBufferPct:           getEnvOrDefaultFloat("LV_SL_BUFFER_PCT", 0.0),
		VBSLBufferPct:         getEnvOrDefaultFloat("VB_SL_BUFFER_PCT", 0.0),
		WatchlistMaxPctChange: getEnvOrDefaultFloat("LV_WATCHLIST_MAX_PCT_CHANGE", 100.0),
		MaxCapitalPerTrade:    getEnvOrDefaultFloat("MAX_CAPITAL_PER_TRADE", 20000.0),
		TradeStartTime:        getEnvOrDefault("LV_TRADE_START_TIME", "09:30"),
		TradeEndTime:          getEnvOrDefault("LV_TRADE_END_TIME", "10:45"),
		StockSelectTime:       getEnvOrDefault("LV_STOCK_SELECT_TIME", "09:30"),
		WatchlistSize:         getEnvOrDefaultInt("LV_WATCHLIST_SIZE", 10),

		// Market hours (9:15 AM - 3:30 PM IST)
		MarketOpenTime:  time.Date(2020, 1, 1, 9, 15, 0, 0, time.UTC),
		MarketCloseTime: time.Date(2020, 1, 1, 15, 30, 0, 0, time.UTC),

		// Strategy
		StrategyType:        getEnvOrDefault("STRATEGY_TYPE", "VWAP_RSI"),
		ActiveStrategies:    getEnvOrDefault("ACTIVE_STRATEGIES", "LOW_VOLUME"),
		ActiveSelectors:     getEnvOrDefault("ACTIVE_SELECTORS", "SECURITIES_FO"),
		StrategySelectorMap: getEnvOrDefault("STRATEGY_SELECTOR_MAP", "LOW_VOLUME:SECURITIES_FO,VANDE_BHARAT:SECTORAL"),
		RiskRewardType:      getEnvOrDefault("RISK_REWARD_TYPE", "STANDARD"),
		RiskRewardRatio:     getEnvOrDefaultFloat("RISK_REWARD_RATIO", 2.0),
		SectorMaxBuyPct:     getEnvOrDefaultFloat("VB_SECTOR_MAX_BUY_PCT", 2.5),
		SectorMaxSellPct:    getEnvOrDefaultFloat("VB_SECTOR_MAX_SELL_PCT", -3.0),
		StockMaxBuyPct:      getEnvOrDefaultFloat("VB_STOCK_MAX_BUY_PCT", 2.5),
		StockMaxSellPct:     getEnvOrDefaultFloat("VB_STOCK_MAX_SELL_PCT", -2.5),
		VBMasterMaxPct:      getEnvOrDefaultFloat("VB_MASTER_MAX_PCT", 3.0),
		VBConfirmMaxPct:     getEnvOrDefaultFloat("VB_CONFIRM_MAX_PCT", 1.0),
		VBTradeStartTime:    getEnvOrDefault("VB_TRADE_START_TIME", "09:26"),
		VBTradeEndTime:      getEnvOrDefault("VB_TRADE_END_TIME", "11:00"),
		CandleIntervalSec:   300, // 5 minutes
		VWAPWindow:          50,  // 50 candles
		ATRPeriod:           14,  // Standard ATR
		OBIWindow:           5,   // 5 ticks
		DefaultOrderType:    getEnvOrDefault("DEFAULT_ORDER_TYPE", "MARKET"),

		// Monitoring
		LogLevel:              getEnvOrDefault("LOG_LEVEL", "info"),
		PrometheusAddr:        getEnvOrDefault("PROMETHEUS_ADDR", ":8888"),
		HealthCheckInterval:   10 * time.Second,
		MarginCheckInterval:   5 * time.Minute,
		PositionCheckInterval: 2 * time.Second,

		// Live Trading mode
		LiveTrading:           getEnvOrDefaultBool("LIVE_TRADING", false),
		SquareOffOnShutdown:   getEnvOrDefaultBool("SQUARE_OFF_ON_SHUTDOWN", true),
		LVUseBrokerSL:         getEnvOrDefaultBool("LV_USE_BROKER_SL", false),
		VBUseBrokerSL:         getEnvOrDefaultBool("VB_USE_BROKER_SL", false),
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

func getEnvOrDefaultBool(key string, defaultVal bool) bool {
	if val := os.Getenv(key); val != "" {
		if boolVal, err := strconv.ParseBool(val); err == nil {
			return boolVal
		}
	}
	return defaultVal
}

