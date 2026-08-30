package strategy

import (
	"zerodha-trading/config"
	"zerodha-trading/data"

	"go.uber.org/zap"
)

// SetupCandle represents setup parameters (like high, low bounds) used by the risk manager
type SetupCandle struct {
	Candle data.Candle
	High   float64
	Low    float64
	Volume int64
}

// Signal represents a trading signal
type Signal struct {
	Symbol       string
	Action       string  // BUY, SELL, HOLD
	Strength     float64 // 0-1, confidence level
	Reason       string
	Candle       *data.Candle
	StrategyName string // Name of the strategy generating the signal
}

// Strategy interface defines the standard API for trading strategies
type Strategy interface {
	Name() string
	CandleTimeFrame() string
	SetCandleTimeFrame(tf string)
	OnCandleClose(candle *data.Candle, symbol string)
	CheckBreakout(symbol string, ltp float64, bias string) *Signal
	GetSetupCandle(symbol string) *SetupCandle
	Reset()
	RestoreTriggeredTrade(symbol string)
}

// InitializeActiveStrategies registers and returns active strategies based on configuration names
func InitializeActiveStrategies(names []string, logger *zap.Logger, cfg *config.Settings) []Strategy {
	var active []Strategy
	for _, name := range names {
		switch name {
		case "LOW_VOLUME":
			lv := NewLowVolumeEngine(logger)
			lv.MinCandlesToIgnore = cfg.LVMinCandlesToIgnore
			if cfg.LVCandleTimeframe != "" {
				lv.SetCandleTimeFrame(cfg.LVCandleTimeframe)
			}
			active = append(active, lv)
		case "VANDE_BHARAT":
			vb := NewVandeBharatEngine(logger, cfg.VBMasterMaxPct, cfg.VBSLMinPct, cfg.VBSLMaxPct, cfg.VBMasterMaxWickPct, cfg.VBMinGapPct)
			vb.MinCandlesToIgnore = cfg.VBMinCandlesToIgnore
			if cfg.VBCandleTimeframe != "" {
				vb.SetCandleTimeFrame(cfg.VBCandleTimeframe)
			}
			active = append(active, vb)
		case "FAKE_BREAKOUT":
			fb := NewFakeBreakoutEngine(logger, cfg.FBGapUpMinPct, cfg.FBGapUpMaxPct, cfg.FBGapDownMinPct, cfg.FBGapDownMaxPct, cfg.FBMaxConfirmationPct, cfg.FBMasterMaxWickPct)
			fb.TradeEndTime = cfg.FBTradeEndTime
			fb.MinCandlesToIgnore = cfg.FBMinCandlesToIgnore
			if cfg.FBCandleTimeframe != "" {
				fb.SetCandleTimeFrame(cfg.FBCandleTimeframe)
			}
			active = append(active, fb)
		case "VANDE_BHARAT_TRAP":
			vbt := NewVandeBharatTrapEngine(logger, cfg.VBTFakeMasterMaxPct, cfg.VBTMasterMaxPct, cfg.VBTSLMinPct, cfg.VBTSLMaxPct, cfg.VBTMasterMaxWickPct)
			vbt.MinCandlesToIgnore = cfg.VBTMinCandlesToIgnore
			if cfg.VBTCandleTimeframe != "" {
				vbt.SetCandleTimeFrame(cfg.VBTCandleTimeframe)
			}
			active = append(active, vbt)
		case "EMAS5_BREAKOUT":
			es5 := NewEMAS5BreakoutEngine(
				logger,
				cfg.ES5MaxTradesPerStock,
				cfg.ES5RallyCandles,
				cfg.ES5MinReboundPct,
				cfg.ES5MasterMaxPct,
				cfg.ES5MaxInsideCandles,
				cfg.ES5ConfirmMaxPct,
			)
			es5.MinCandlesToIgnore = cfg.ES5MinCandlesToIgnore
			if cfg.ES5CandleTimeframe != "" {
				es5.SetCandleTimeFrame(cfg.ES5CandleTimeframe)
			}
			if cfg.ES5EMATouchBufferPct >= 0 {
				es5.SetEMATouchBufferPct(cfg.ES5EMATouchBufferPct)
			}
			if cfg.ES5MasterMaxWickPct > 0 {
				es5.SetMasterMaxWickPct(cfg.ES5MasterMaxWickPct)
			}
			active = append(active, es5)
		default:
			logger.Warn("Unknown strategy requested in config", zap.String("name", name))
		}
	}
	// Fallback to LOW_VOLUME if no valid strategies were loaded
	if len(active) == 0 {
		logger.Warn("No valid strategies enabled, falling back to LOW_VOLUME")
		lv := NewLowVolumeEngine(logger)
		lv.MinCandlesToIgnore = cfg.LVMinCandlesToIgnore
		if cfg.LVCandleTimeframe != "" {
			lv.SetCandleTimeFrame(cfg.LVCandleTimeframe)
		}
		active = append(active, lv)
	}
	return active
}

// StrategyEngine has been removed as the legacy VWAP_RSI strategy is retired.
// Codebase now strictly runs LOW_VOLUME and VANDE_BHARAT strategies.
