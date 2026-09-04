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
	RiskPerTrade          float64
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
	SectorScannerEnabled  bool
	SectorScannerTopN     int
	SectorScannerWeight   float64

	// Market Hours
	MarketOpenTime  time.Time
	MarketCloseTime time.Time

	// Strategy
	ActiveStrategies       string
	ActiveSelectors        string
	StrategySelectorMap    string
	RiskRewardType         string
	RiskRewardRatio        float64
	SectorMaxBuyPct        float64
	SectorMaxSellPct       float64
	StockMaxBuyPct         float64
	StockMaxSellPct        float64
	VBMasterMaxPct         float64
	VBSLMinPct             float64
	VBSLMaxPct             float64
	VBMinGapPct            float64
	VBConfirmMinPct        float64
	VBConfirmMaxPct        float64
	VBMasterMaxWickPct     float64
	VBStockMaxDayChangePct float64
	VBTradeEndTime         string
	FBGapUpMinPct          float64
	FBGapUpMaxPct          float64
	FBGapDownMinPct        float64
	FBGapDownMaxPct        float64
	FBMaxConfirmationPct   float64
	FBMasterMaxWickPct     float64
	FBTradeEndTime         string
	FBSLBufferPct          float64
	FBCandleTimeframe      string
	FBUseBrokerSL          bool
	FBMinCandlesToIgnore   int
	VBTFakeMasterMaxPct    float64
	VBTMasterMaxPct        float64
	VBTSLMinPct            float64
	VBTSLMaxPct            float64
	VBTMasterMaxWickPct    float64
	VBTTradeEndTime        string
	VBTSLBufferPct         float64
	VBTCandleTimeframe     string
	VBTUseBrokerSL         bool
	VBTMinCandlesToIgnore  int
	ES5MaxTradesPerStock   int
	ES5RallyCandles        int
	ES5MinReboundPct       float64
	ES5MasterMaxPct        float64
	ES5MaxInsideCandles    int
	ES5ConfirmMaxPct       float64
	ES5TradeEndTime        string
	ES5SLBufferPct         float64
	ES5CandleTimeframe     string
	ES5UseBrokerSL         bool
	ES5MinCandlesToIgnore  int
	ES5EMATouchBufferPct   float64
	ES5MasterMaxWickPct    float64
	CandleIntervalSec      int
	VWAPWindow             int
	ATRPeriod              int
	OBIWindow              int
	DefaultOrderType       string

	// EMA Indicators
	EMAFastPeriod        int
	EMASlowPeriod        int
	EMACandleInterval    string
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
	LVCandleTimeframe    string
	VBCandleTimeframe    string
	AWSHostIP            string
	BroadSubscribe       bool
	MorningBroadAggStart string
	MorningBroadAggEnd   string

	// Restart Time Gate Controls
	RestartAllowedBefore string
	RestartAllowedAfter  string

	// Manual Trading Sync & Risk-Reward Configuration
	ManualTradeSyncEnabled        bool
	ManualTradePollMinutes        int
	ManualTradeAttachedRRStrategy string
	ManualTradeRRRatio            float64
	ManualTradePartialExitPct     float64
	ManualTradeDefaultSLPct       float64
	ManualTradeMoveSLToCost       bool
	ManualTradeCostBufferPct      float64
	ManualTradeUseBrokerSL        bool

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
	TrailSLBufferPct      float64
	LimitBufferPct        float64
	MaxTradesPerDay       int
	ActiveIndices         []string
	LiveIndices           []string
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
		InitialCapital:        getEnvOrDefaultFloat("INITIAL_CAPITAL", 100000.0),
		RiskPerTrade:          getEnvOrDefaultFloat("RISK_PER_TRADE", getEnvOrDefaultFloat("RISK_PER_TRADE_INR", 500.0)),
		MaxDailyLossAmount:    getEnvOrDefaultFloat("MAX_DAILY_LOSS_AMOUNT", 0),
		MaxTradesPerDay:       getEnvOrDefaultInt("MAX_TRADES_PER_DAY", 20),
		MaxLossStreaks:        getEnvOrDefaultInt("MAX_LOSS_STREAKS", 3),
		MaxHoldingTimeMin:     getEnvOrDefaultInt("MAX_HOLDING_TIME_MIN", 30),
		SLBufferPct:           getEnvOrDefaultFloat("LV_SL_BUFFER_PCT", 0.1),
		VBSLBufferPct:         getEnvOrDefaultFloat("VB_SL_BUFFER_PCT", 0.1),
		WatchlistMaxPctChange: getEnvOrDefaultFloat("LV_WATCHLIST_MAX_PCT_CHANGE", 100.0),
		MaxCapitalPerTrade:    getEnvOrDefaultFloat("MAX_CAPITAL_PER_TRADE", 20000.0),
		LVTradeEndTime:        getEnvOrDefault("LV_TRADE_END_TIME", "10:45:00"),
		StockSelectTime:       getEnvOrDefault("STOCK_SELECT_TIME", "09:00:00"),
		EVGStockSelectTime:    getEnvOrDefault("EVG_STOCK_SELECT_TIME", "09:00:00"),
		AutoSquareOffTime:     getEnvOrDefault("AUTO_SQUARE_OFF_TIME", "15:20:00"),
		StrategyWatchlistSize: getEnvOrDefaultInt("STRATEGY_WATCHLIST_SIZE", 10),
		SectorScannerEnabled:  getEnvOrDefaultBool("SECTOR_SCANNER_ENABLED", true),
		SectorScannerTopN:     getEnvOrDefaultInt("SECTOR_SCANNER_TOP_N", 3),
		SectorScannerWeight:   getEnvOrDefaultFloat("SECTOR_SCANNER_WEIGHT", 0.40),

		// Market hours (9:15 AM - 3:30 PM IST)
		MarketOpenTime:  time.Date(2020, 1, 1, 9, 15, 0, 0, time.UTC),
		MarketCloseTime: time.Date(2020, 1, 1, 15, 30, 0, 0, time.UTC),

		// Strategy
		ActiveStrategies:       getEnvOrDefault("ACTIVE_STRATEGIES", "LOW_VOLUME,VANDE_BHARAT,VANDE_BHARAT_TRAP,EMAS5_BREAKOUT"),
		ActiveSelectors:        getEnvOrDefault("ACTIVE_SELECTORS", "FO,SECTOR,PDH_PDL,52WH_52WL"),
		StrategySelectorMap:    getEnvOrDefault("STRATEGY_SELECTOR_MAP", "LOW_VOLUME:PDH_PDL,VANDE_BHARAT:FO,VANDE_BHARAT_TRAP:FO,EMAS5_BREAKOUT:FO"),
		RiskRewardType:         getEnvOrDefault("RISK_REWARD_TYPE", "DYNAMIC_TRAILING"),
		RiskRewardRatio:        getEnvOrDefaultFloat("RISK_REWARD_RATIO", 2.0),
		SectorMaxBuyPct:        getEnvOrDefaultFloat("VB_SECTOR_MAX_BUY_PCT", 2.5),
		SectorMaxSellPct:       getEnvOrDefaultFloat("VB_SECTOR_MAX_SELL_PCT", -3.0),
		StockMaxBuyPct:         getEnvOrDefaultFloat("VB_STOCK_MAX_BUY_PCT", 2.5),
		StockMaxSellPct:        getEnvOrDefaultFloat("VB_STOCK_MAX_SELL_PCT", -2.5),
		VBMasterMaxPct:         getEnvOrDefaultFloat("VB_MASTER_MAX_PCT", 1.8),
		VBSLMinPct:             getEnvOrDefaultFloat("VB_SL_MIN_PCT", getEnvOrDefaultFloat("VB_CONFIRM_MIN_PCT", 0.5)),
		VBSLMaxPct:             getEnvOrDefaultFloat("VB_SL_MAX_PCT", getEnvOrDefaultFloat("VB_CONFIRM_MAX_PCT", 1.0)),
		VBMinGapPct:            getEnvOrDefaultFloat("VB_MIN_GAP_PCT", 2.0),
		VBConfirmMinPct:        getEnvOrDefaultFloat("VB_CONFIRM_MIN_PCT", 0.5),
		VBConfirmMaxPct:        getEnvOrDefaultFloat("VB_CONFIRM_MAX_PCT", 1.0),
		VBMasterMaxWickPct:     getEnvOrDefaultFloat("VB_MASTER_MAX_WICK_PCT", 40.0),
		VBStockMaxDayChangePct: getEnvOrDefaultFloat("VB_STOCK_MAX_DAY_CHANGE_PCT", 3.0),
		VBTradeEndTime:         getEnvOrDefault("VB_TRADE_END_TIME", "11:00:00"),
		FBGapUpMinPct:          getEnvOrDefaultFloat("FB_GAP_UP_MIN_PCT", 4.0),
		FBGapUpMaxPct:          getEnvOrDefaultFloat("FB_GAP_UP_MAX_PCT", 8.0),
		FBGapDownMinPct:        getEnvOrDefaultFloat("FB_GAP_DOWN_MIN_PCT", 4.0),
		FBGapDownMaxPct:        getEnvOrDefaultFloat("FB_GAP_DOWN_MAX_PCT", 8.0),
		FBMaxConfirmationPct:   getEnvOrDefaultFloat("FB_MAX_CONFIRMATION_PCT", 1.0),
		FBMasterMaxWickPct:     getEnvOrDefaultFloat("FB_MASTER_MAX_WICK_PCT", 40.0),
		FBTradeEndTime:         getEnvOrDefault("FB_TRADE_END_TIME", "11:00:00"),
		FBSLBufferPct:          getEnvOrDefaultFloat("FB_SL_BUFFER_PCT", 0.1),
		FBCandleTimeframe:      getEnvOrDefault("FB_CANDLE_TIMEFRAME", "1m"),
		FBUseBrokerSL:          getEnvOrDefaultBool("FB_USE_BROKER_SL", false),
		FBMinCandlesToIgnore:   getEnvOrDefaultInt("FB_MIN_CANDLES_TO_IGNORE", 0),
		VBTFakeMasterMaxPct:    getEnvOrDefaultFloat("VBT_FAKE_MASTER_MAX_PCT", 3.0),
		VBTMasterMaxPct:        getEnvOrDefaultFloat("VBT_MASTER_MAX_PCT", 1.8),
		VBTSLMinPct:            getEnvOrDefaultFloat("VBT_SL_MIN_PCT", 0.5),
		VBTSLMaxPct:            getEnvOrDefaultFloat("VBT_SL_MAX_PCT", 1.0),
		VBTMasterMaxWickPct:    getEnvOrDefaultFloat("VBT_MASTER_MAX_WICK_PCT", 40.0),
		VBTTradeEndTime:        getEnvOrDefault("VBT_TRADE_END_TIME", "11:00:00"),
		VBTSLBufferPct:         getEnvOrDefaultFloat("VBT_SL_BUFFER_PCT", 0.1),
		VBTCandleTimeframe:     getEnvOrDefault("VBT_CANDLE_TIMEFRAME", "1m"),
		VBTUseBrokerSL:         getEnvOrDefaultBool("VBT_USE_BROKER_SL", false),
		VBTMinCandlesToIgnore:  getEnvOrDefaultInt("VBT_MIN_CANDLES_TO_IGNORE", 0),
		ES5MaxTradesPerStock:   getEnvOrDefaultInt("ES5_MAX_TRADES_PER_STOCK", 2),
		ES5RallyCandles:        getEnvOrDefaultInt("ES5_RALLY_CANDLES", 5),
		ES5MinReboundPct:       getEnvOrDefaultFloat("ES5_MIN_REBOUND_PCT", 0.5),
		ES5MasterMaxPct:        getEnvOrDefaultFloat("ES5_MASTER_MAX_PCT", 2.0),
		ES5MaxInsideCandles:    getEnvOrDefaultInt("ES5_MAX_INSIDE_CANDLES", 1),
		ES5ConfirmMaxPct:       getEnvOrDefaultFloat("ES5_CONFIRM_MAX_PCT", 1.0),
		ES5TradeEndTime:        getEnvOrDefault("ES5_TRADE_END_TIME", "11:00:00"),
		ES5SLBufferPct:         getEnvOrDefaultFloat("ES5_SL_BUFFER_PCT", 0.1),
		ES5CandleTimeframe:     getEnvOrDefault("ES5_CANDLE_TIMEFRAME", "1m"),
		ES5UseBrokerSL:         getEnvOrDefaultBool("ES5_USE_BROKER_SL", false),
		ES5MinCandlesToIgnore:  getEnvOrDefaultInt("ES5_MIN_CANDLES_TO_IGNORE", 0),
		ES5EMATouchBufferPct:   getEnvOrDefaultFloat("ES5_EMA_TOUCH_BUFFER_PCT", 0.10),
		ES5MasterMaxWickPct:    getEnvOrDefaultFloat("ES5_MASTER_MAX_WICK_PCT", 40.0),
		CandleIntervalSec:      300, // 5 minutes
		VWAPWindow:             50,  // 50 candles
		ATRPeriod:              14,  // Standard ATR
		OBIWindow:              5,   // 5 ticks
		DefaultOrderType:       getEnvOrDefault("DEFAULT_ORDER_TYPE", "MARKET"),

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
		LVCandleTimeframe:    getEnvOrDefault("LV_CANDLE_TIMEFRAME", "5m"),
		VBCandleTimeframe:    getEnvOrDefault("VB_CANDLE_TIMEFRAME", "1m"),
		AWSHostIP:            getEnvOrDefault("AWS_HOST_IP", "3.7.29.3"),
		BroadSubscribe:       getEnvOrDefaultBool("BROAD_SUBSCRIBE", true),
		MorningBroadAggStart: getEnvOrDefault("MORNING_BROAD_AGG_START", "09:15:00"),
		MorningBroadAggEnd:   getEnvOrDefault("MORNING_BROAD_AGG_END", "09:45:00"),

		EMAFastPeriod:        getEnvOrDefaultInt("EMA_FAST_PERIOD", 10),
		EMASlowPeriod:        getEnvOrDefaultInt("EMA_SLOW_PERIOD", 20),
		EMACandleInterval:    getEnvOrDefault("EMA_CANDLE_INTERVAL", "5minute"),
		EMAEnabledForTrading: getEnvOrDefaultBool("EMA_ENABLED_FOR_TRADING", false),

		RestartAllowedBefore: getEnvOrDefault("BOT_RESTART_ALLOWED_BEFORE", "09:15:00"),
		RestartAllowedAfter:  getEnvOrDefault("BOT_RESTART_ALLOWED_AFTER", "15:45:00"),

		// Manual Trading Sync & Strategy
		ManualTradeSyncEnabled:        getEnvOrDefaultBool("MANUAL_TRADE_SYNC_ENABLED", true),
		ManualTradePollMinutes:        getEnvOrDefaultInt("MANUAL_TRADE_POLL_MINUTES", 5),
		ManualTradeAttachedRRStrategy: getEnvOrDefault("MANUAL_TRADE_ATTACHED_RR_STRATEGY", "PARTIAL_BOOK_COST_SL"),
		ManualTradeRRRatio:            getEnvOrDefaultFloat("MANUAL_TRADE_RR_RATIO", 2.0),
		ManualTradePartialExitPct:     getEnvOrDefaultFloat("MANUAL_TRADE_PARTIAL_EXIT_PCT", 50.0),
		ManualTradeDefaultSLPct:       getEnvOrDefaultFloat("MANUAL_TRADE_DEFAULT_SL_PCT", 1.5),
		ManualTradeMoveSLToCost:       getEnvOrDefaultBool("MANUAL_TRADE_MOVE_SL_TO_COST", true),
		ManualTradeCostBufferPct:      getEnvOrDefaultFloat("MANUAL_TRADE_COST_BUFFER_PCT", 0.05),
		ManualTradeUseBrokerSL:        getEnvOrDefaultBool("MANUAL_TRADE_USE_BROKER_SL", true),

		Options: OptionsConfig{
			LiveTrading:           getEnvOrDefaultBool("OPTIONS_LIVE_TRADING", false),
			PaperBalance:          getEnvOrDefaultFloat("OPTIONS_PAPER_BALANCE", 1000000.0),
			IndexSymbol:           getEnvOrDefault("INDEX_SYMBOL", "NIFTY 50"),
			BaseLotSize:           getEnvOrDefaultInt("OPTIONS_BASE_LOT_SIZE", 65),
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
			MaxQuantityMultiplier: getEnvOrDefaultInt("MAX_QUANTITY_MULTIPLIER", 4),
			ExpiryCutoffTime:      getEnvOrDefault("EXPIRY_CUTOFF_TIME", "15:15:00"),
			MaxBidAskSpreadPct:    getEnvOrDefaultFloat("MAX_BID_ASK_SPREAD_PCT", 10.0),
			TradeMode:             getEnvOrDefault("OPTIONS_TRADE_MODE", "INTRADAY"),
			AutoSquareOffTime:     getEnvOrDefault("OPTIONS_AUTO_SQUARE_OFF_TIME", "15:13:00"),
			LastNewTradeTime:      getEnvOrDefault("OPTIONS_LAST_NEW_TRADE_TIME", "14:32:00"),
			SuperTrendCutoffTime:  getEnvOrDefault("SUPER_TREND_CUTOFF_TIME", "15:15:00"),
			TrailSLEnabled:        getEnvOrDefaultBool("OPTIONS_TRAIL_SL_ENABLED", true),
			TrailSLBufferPct:      getEnvOrDefaultFloat("OPTIONS_TRAIL_SL_BUFFER_PCT", 20.0),
			LimitBufferPct:        getEnvOrDefaultFloat("OPTIONS_LIMIT_BUFFER_PCT", 5.0),
			MaxTradesPerDay:       getEnvOrDefaultInt("OPTIONS_MAX_TRADES_PER_DAY", 10),
			ActiveIndices:         parseActiveIndices(getEnvOrDefault("OPTIONS_ACTIVE_INDICES", getEnvOrDefault("INDEX_SYMBOL", "NIFTY 50"))),
			LiveIndices:           parseStringList(os.Getenv("OPTIONS_LIVE_INDICES")),
		},
		Scanner: ScannerConfig{
			Enabled:       getEnvOrDefaultBool("SCANNER_ENABLED", true),
			ExecutionTime: getEnvOrDefault("SCANNER_EXECUTION_TIME", "15:45:00"),
			MomentumDays:  getEnvOrDefaultInt("SCANNER_MOMENTUM_DAYS", 3),
			NewsEnabled:   getEnvOrDefaultBool("SCANNER_NEWS_ENABLED", true),
		},
	}, nil
}

// IsIndexLiveTrading returns whether a specific index is configured for real live broker trading
func (o *OptionsConfig) IsIndexLiveTrading(indexName string) bool {
	// If global live trading is explicitly disabled and LiveIndices is empty, return false
	if !o.LiveTrading && len(o.LiveIndices) == 0 {
		return false
	}
	// If global live trading is enabled and LiveIndices is empty, ALL active indices are live
	if o.LiveTrading && len(o.LiveIndices) == 0 {
		return true
	}
	target := strings.ToUpper(strings.TrimSpace(indexName))
	for _, liveIdx := range o.LiveIndices {
		l := strings.ToUpper(strings.TrimSpace(liveIdx))
		if l == "ALL" || l == "*" {
			return true
		}
		if l == "NONE" {
			return false
		}
		if l == target {
			return true
		}
		// Match prefixes/aliases (e.g. NIFTY, BANKNIFTY, SENSEX, FINNIFTY, MIDCPNIFTY)
		if (strings.Contains(target, "NIFTY 50") || target == "NIFTY") && (strings.Contains(l, "NIFTY 50") || l == "NIFTY") {
			return true
		}
		if (strings.Contains(target, "BANK") || target == "BANKNIFTY") && (strings.Contains(l, "BANK") || l == "BANKNIFTY") {
			return true
		}
		if strings.Contains(target, "SENSEX") && strings.Contains(l, "SENSEX") {
			return true
		}
		if strings.Contains(target, "FINNIFTY") && strings.Contains(l, "FINNIFTY") {
			return true
		}
		if strings.Contains(target, "MIDCP") && strings.Contains(l, "MIDCP") {
			return true
		}
	}
	return false
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

func parseActiveIndices(raw string) []string {
	var result []string
	if raw == "" {
		return []string{"NIFTY 50"}
	}
	for _, part := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return []string{"NIFTY 50"}
	}
	return result
}

func parseStringList(raw string) []string {
	var result []string
	if raw == "" {
		return result
	}
	for _, part := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
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
