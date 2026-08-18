package strategy

import (
	"testing"
	"time"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

func TestLowVolumeEnginePDHPDL(t *testing.T) {
	logger := zap.NewNop()
	engine := NewLowVolumeEngine(logger)
	symbol := "SBIN"

	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)
	baseTime := time.Date(2026, 8, 18, 9, 15, 0, 0, data.ISTLocation)

	// 1. 1st Candle (09:15 AM): Closes above PDH 100.0 (Open 99.0, Close 101.0 > 100.0) -> BUY Qualified
	candle1 := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   99.0,
		High:   101.5,
		Low:    99.0,
		Close:  101.0,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1, symbol)

	// 2. 2nd Candle (09:20 AM): Lowest Volume Red Setup Candle (Open 101.5, Close 101.0 < Open 101.5, Volume 500)
	candle2 := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   101.5,
		High:   101.8,
		Low:    100.8,
		Close:  101.0,
		Volume: 500,
	}
	engine.OnCandleClose(candle2, symbol)

	// 3. 3rd Candle (09:25 AM): Candle 2 is the immediately previous completed candle
	// CheckBreakout with LTP 102.0 > Setup High 101.8 -> BUY Trigger!
	sig := engine.CheckBreakout(symbol, 102.0, "BUY_ONLY")
	if sig == nil || sig.Action != "BUY" {
		t.Fatalf("expected BUY signal when 1st candle closed > PDH, got: %+v", sig)
	}

	// Reset for Unqualified 1st Candle Test
	engine.Reset()
	engine.SetPreviousDayHighLow(symbol, 100.0, 90.0)

	// 1st Candle closes between PDH (100) and PDL (90) -> Close 95.0
	candle1Neutral := &data.Candle{
		Token:  123,
		Time:   baseTime,
		Open:   94.0,
		High:   96.0,
		Low:    94.0,
		Close:  95.0,
		Volume: 1000,
	}
	engine.OnCandleClose(candle1Neutral, symbol)

	candle2Neutral := &data.Candle{
		Token:  123,
		Time:   baseTime.Add(5 * time.Minute),
		Open:   95.5,
		High:   96.0,
		Low:    94.5,
		Close:  95.0,
		Volume: 500,
	}
	engine.OnCandleClose(candle2Neutral, symbol)

	sigNeutral := engine.CheckBreakout(symbol, 97.0, "BUY_ONLY")
	if sigNeutral != nil {
		t.Fatalf("expected NO signal when 1st candle closed between PDH and PDL, got: %+v", sigNeutral)
	}
}
