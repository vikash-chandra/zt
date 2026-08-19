package config

import (
	"os"
	"strconv"
	"strings"
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
	TokenPrefix string

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

	LVTradeEndTime        string
	StockSelectTime       string
	EVGStockSelectTime    string
	AutoSquareOffTime     string
	StrategyWatchlistSize int

	// Market Hours
	MarketOpenTime  time.Time
	MarketCloseTime time.Time

	// Strategy
	ActiveStrategies    string
	ActiveSelectors     string
	StrategySelectorMap string
	RiskRewardType      string
	RiskRewardRatio     float64
	SectorMaxBuyPct     float64
	SectorMaxSellPct    float64
	StockMaxBuyPct      float64
	StockMaxSellPct     float64
	VBMasterMaxPct         float64
	VBConfirmMinPct        float64
	VBConfirmMaxPct        float64
	VBMasterMaxWickPct     float64
	VBStockMaxDayChangePct float64
	VBTradeEndTime         string
	CandleIntervalSec   int
	VWAPWindow          int
	ATRPeriod           int
	OBIWindow           int
	DefaultOrderType    string

	// EMA Indicators
	EMAFastPeriod         int
	EMASlowPeriod         int
	EMACandleInterval     string
	EMAEnabledForTrading bool

	// Monitoring
	LogLevel              string
	HealthCheckInterval   time.Duration
	MarginCheckInterval   time.Duration
	PositionCheckInterval time.Duration

	// Live Trading mode
	LiveTrading          bool
	SquareOffOnShutdown  bool
	LVUseBrokerSL        bool
	VBUseBrokerSL        bool
	LVMinCandlesToIgnore int
	VBMinCandlesToIgnore int
	AWSHostIP            string
	BroadSubscribe       bool
	MorningBroadAggStart string
	MorningBroadAggEnd   string

	// Options Strategy Config
	Options OptionsConfig

	// Quant Stock Scanner Config
	Scanner ScannerConfig
}

type OptionsConfig struct {
	LiveTrading           bool
	PaperBalance          float64
	IndexSymbol           string
	BaseLotSize           int
	StrikeOffsetPoints    float64
	TargetEntryPremium    float64
	ExpiryType            string
	NextMonthDays         int
	SuperTrendST1Period   int
	SuperTrendST1Factor   float64
	SuperTrendST2Period   int
	SuperTrendST2Factor   float64
	SuperTrendST3Period   int
	SuperTrendST3Factor   float64
	OptionsSLPct          float64
	MaxQuantityMultiplier int
	ExpiryCutoffTime      string
	MaxBidAskSpreadPct    float64
	TradeMode             string
	AutoSquareOffTime     string
	LastNewTradeTime      string
	SuperTrendCutoffTime  string
	TrailSLEnabled        bool
	TrailSLPct            float64
}

type ScannerConfig struct {
	Enabled       bool
	ExecutionTime string
	MomentumDays  int
	NewsEnabled   bool
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
		TokenPrefix: strings.TrimSpace(getEnvOrDefault("KITE_TOKEN_PREFIX", "vcj:zt-token:")),

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
		LVTradeEndTime:        getEnvOrDefault("LV_TRADE_END_TIME", "10:45"),
		StockSelectTime:       getEnvOrDefault("STOCK_SELECT_TIME", "09:25"),
		EVGStockSelectTime:    getEnvOrDefault("EVG_STOCK_SELECT_TIME", "09:07"),
		AutoSquareOffTime:     getEnvOrDefault("AUTO_SQUARE_OFF_TIME", "15:20"),
		StrategyWatchlistSize: getEnvOrDefaultInt("STRATEGY_WATCHLIST_SIZE", 10),

		// Market hours (9:15 AM - 3:30 PM IST)
		MarketOpenTime:  time.Date(2020, 1, 1, 9, 15, 0, 0, time.UTC),
		MarketCloseTime: time.Date(2020, 1, 1, 15, 30, 0, 0, time.UTC),

		// Strategy
		ActiveStrategies:    getEnvOrDefault("ACTIVE_STRATEGIES", "LOW_VOLUME"),
		ActiveSelectors:     getEnvOrDefault("ACTIVE_SELECTORS", "SECURITIES_FO"),
		StrategySelectorMap: getEnvOrDefault("STRATEGY_SELECTOR_MAP", "LOW_VOLUME:SECURITIES_FO,VANDE_BHARAT:SECTORAL"),
		RiskRewardType:      getEnvOrDefault("RISK_REWARD_TYPE", "STANDARD"),
		RiskRewardRatio:     getEnvOrDefaultFloat("RISK_REWARD_RATIO", 2.0),
		SectorMaxBuyPct:     getEnvOrDefaultFloat("VB_SECTOR_MAX_BUY_PCT", 2.5),
		SectorMaxSellPct:    getEnvOrDefaultFloat("VB_SECTOR_MAX_SELL_PCT", -3.0),
		StockMaxBuyPct:      getEnvOrDefaultFloat("VB_STOCK_MAX_BUY_PCT", 2.5),
		StockMaxSellPct:     getEnvOrDefaultFloat("VB_STOCK_MAX_SELL_PCT", -2.5),
		VBMasterMaxPct:         getEnvOrDefaultFloat("VB_MASTER_MAX_PCT", 3.0),
		VBConfirmMinPct:        getEnvOrDefaultFloat("VB_CONFIRM_MIN_PCT", 0.5),
		VBConfirmMaxPct:        getEnvOrDefaultFloat("VB_CONFIRM_MAX_PCT", 1.0),
		VBMasterMaxWickPct:     getEnvOrDefaultFloat("VB_MASTER_MAX_WICK_PCT", 40.0),
		VBStockMaxDayChangePct: getEnvOrDefaultFloat("VB_STOCK_MAX_DAY_CHANGE_PCT", 3.0),
		VBTradeEndTime:         getEnvOrDefault("VB_TRADE_END_TIME", "11:00"),
		CandleIntervalSec:   300, // 5 minutes
		VWAPWindow:          50,  // 50 candles
		ATRPeriod:           14,  // Standard ATR
		OBIWindow:           5,   // 5 ticks
		DefaultOrderType:    getEnvOrDefault("DEFAULT_ORDER_TYPE", "MARKET"),

		// Monitoring
		LogLevel:              getEnvOrDefault("LOG_LEVEL", "info"),
		HealthCheckInterval:   10 * time.Second,
		MarginCheckInterval:   5 * time.Minute,
		PositionCheckInterval: 2 * time.Second,

		// Live Trading mode
		LiveTrading:          getEnvOrDefaultBool("LIVE_TRADING", false),
		SquareOffOnShutdown:  getEnvOrDefaultBool("SQUARE_OFF_ON_SHUTDOWN", true),
		LVUseBrokerSL:        getEnvOrDefaultBool("LV_USE_BROKER_SL", false),
		VBUseBrokerSL:        getEnvOrDefaultBool("VB_USE_BROKER_SL", false),
		LVMinCandlesToIgnore: getEnvOrDefaultInt("LV_MIN_CANDLES_TO_IGNORE", 3),
		VBMinCandlesToIgnore: getEnvOrDefaultInt("VB_MIN_CANDLES_TO_IGNORE", 2),
		AWSHostIP:            getEnvOrDefault("AWS_HOST_IP", "3.7.29.3"),
		BroadSubscribe:       getEnvOrDefaultBool("BROAD_SUBSCRIBE", true),
		MorningBroadAggStart: getEnvOrDefault("MORNING_BROAD_AGG_START", "09:15"),
		MorningBroadAggEnd:   getEnvOrDefault("MORNING_BROAD_AGG_END", "09:35"),

		EMAFastPeriod:         getEnvOrDefaultInt("EMA_FAST_PERIOD", 10),
		EMASlowPeriod:         getEnvOrDefaultInt("EMA_SLOW_PERIOD", 20),
		EMACandleInterval:     getEnvOrDefault("EMA_CANDLE_INTERVAL", "5minute"),
		EMAEnabledForTrading: getEnvOrDefaultBool("EMA_ENABLED_FOR_TRADING", false),

		Options: OptionsConfig{
			LiveTrading:           getEnvOrDefaultBool("OPTIONS_LIVE_TRADING", false),
			PaperBalance:          getEnvOrDefaultFloat("OPTIONS_PAPER_BALANCE", 1000000.0),
			IndexSymbol:           getEnvOrDefault("INDEX_SYMBOL", "NIFTY 50"),
			BaseLotSize:           getEnvOrDefaultInt("OPTIONS_BASE_LOT_SIZE", 65),
			StrikeOffsetPoints:    getEnvOrDefaultFloat("STRIKE_OFFSET_POINTS", 300.0),
			TargetEntryPremium:    getEnvOrDefaultFloat("OPTIONS_TARGET_ENTRY_PREMIUM", 100.0),
			ExpiryType:            getEnvOrDefault("OPTIONS_EXPIRY_TYPE", "MONTHLY"),
			NextMonthDays:         getEnvOrDefaultInt("OPTIONS_NEXT_MONTH_DAYS", 7),
			SuperTrendST1Period:   getEnvOrDefaultInt("SUPERTREND_ST1_PERIOD", 10),
			SuperTrendST1Factor:   getEnvOrDefaultFloat("SUPERTREND_ST1_FACTOR", 4.0),
			SuperTrendST2Period:   getEnvOrDefaultInt("SUPERTREND_ST2_PERIOD", 7),
			SuperTrendST2Factor:   getEnvOrDefaultFloat("SUPERTREND_ST2_FACTOR", 3.0),
			SuperTrendST3Period:   getEnvOrDefaultInt("SUPERTREND_ST3_PERIOD", 7),
			SuperTrendST3Factor:   getEnvOrDefaultFloat("SUPERTREND_ST3_FACTOR", 2.0),
			OptionsSLPct:          getEnvOrDefaultFloat("OPTIONS_SL_PCT", 50.0),
			MaxQuantityMultiplier: getEnvOrDefaultInt("MAX_QUANTITY_MULTIPLIER", 3),
			ExpiryCutoffTime:      getEnvOrDefault("EXPIRY_CUTOFF_TIME", "15:15"),
			MaxBidAskSpreadPct:    getEnvOrDefaultFloat("MAX_BID_ASK_SPREAD_PCT", 10.0),
			TradeMode:             getEnvOrDefault("OPTIONS_TRADE_MODE", "INTRADAY"),
			AutoSquareOffTime:     getEnvOrDefault("OPTIONS_AUTO_SQUARE_OFF_TIME", "15:15"),
			LastNewTradeTime:      getEnvOrDefault("OPTIONS_LAST_NEW_TRADE_TIME", "15:00"),
			SuperTrendCutoffTime:  getEnvOrDefault("SUPER_TREND_CUTOFF_TIME", "15:10"),
			TrailSLEnabled:        getEnvOrDefaultBool("OPTIONS_TRAIL_SL_ENABLED", true),
			TrailSLPct:            getEnvOrDefaultFloat("OPTIONS_TRAIL_SL_PCT", 20.0),
		},
		Scanner: ScannerConfig{
			Enabled:       getEnvOrDefaultBool("SCANNER_ENABLED", true),
			ExecutionTime: getEnvOrDefault("SCANNER_EXECUTION_TIME", "08:30"),
			MomentumDays:  getEnvOrDefaultInt("SCANNER_MOMENTUM_DAYS", 3),
			NewsEnabled:   getEnvOrDefaultBool("SCANNER_NEWS_ENABLED", true),
		},
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

// SaveAccessTokenToEnv writes or updates the KITE_ACCESS_TOKEN inside the .env file
func SaveAccessTokenToEnv(filePath string, token string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		// File does not exist, create new
		return os.WriteFile(filePath, []byte("KITE_ACCESS_TOKEN="+token+"\n"), 0644)
	}

	lines := strings.Split(string(content), "\n")
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "KITE_ACCESS_TOKEN=") {
			lines[i] = "KITE_ACCESS_TOKEN=" + token
			found = true
			break
		}
	}

	if !found {
		lines = append(lines, "KITE_ACCESS_TOKEN="+token)
	}

	output := strings.Join(lines, "\n")
	return os.WriteFile(filePath, []byte(output), 0644)
}
