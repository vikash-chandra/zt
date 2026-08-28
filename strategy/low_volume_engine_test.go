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

func TestLowVolumeEngineStrictAbsoluteLowestOfDay(t *testing.T) {
	logger := zap.NewNop()
	engine := NewLowVolumeEngine(logger)
	symbol := "SAIL"

	engine.SetPreviousDayHighLow(symbol, 190.0, 180.0)
	baseTime := time.Date(2026, 8, 28, 9, 15, 0, 0, data.ISTLocation)

	// 1. 09:15 AM Candle: Closes > PDH 190.0 (Open 191, Close 195 > 190, Vol 2M) -> BUY Qualified
	engine.OnCandleClose(&data.Candle{
		Token: 123, Time: baseTime, Open: 191.0, High: 196.0, Low: 191.0, Close: 195.0, Volume: 2000000,
	}, symbol)

	// 2. 10:20 AM Candle: Lowest volume of the day (57.18K volume, Red, High 196.08, Low 195.83)
	c1020 := &data.Candle{
		Token: 123, Time: baseTime.Add(65 * time.Minute), Open: 196.01, High: 196.08, Low: 195.83, Close: 195.85, Volume: 57181,
	}
	engine.OnCandleClose(c1020, symbol)

	// 3. 10:25 AM Candle (during formation): LTP 196.00 < Setup High 196.08 -> NO Breakout
	sig1 := engine.CheckBreakout(symbol, 196.00, "BUY_ONLY")
	if sig1 != nil {
		t.Fatalf("expected NO signal when LTP 196.00 < 196.08, got: %+v", sig1)
	}

	// 4. 10:25 AM Candle Closes: Volume 261K (Red)
	c1025 := &data.Candle{
		Token: 123, Time: baseTime.Add(70 * time.Minute), Open: 195.91, High: 196.00, Low: 195.75, Close: 195.76, Volume: 261805,
	}
	engine.OnCandleClose(c1025, symbol)

	// 5. 10:30 AM Candle (during formation): Since 10:25 is NOT the lowest volume candle (57k < 261k), no breakout allowed
	sig2 := engine.CheckBreakout(symbol, 197.00, "BUY_ONLY")
	if sig2 != nil {
		t.Fatalf("expected NO signal on 10:25 candle when setup was 10:20, got: %+v", sig2)
	}

	// 6. 10:30 AM Candle Closes: Volume 91.47K (Higher than 10:20's 57.18K)
	c1030 := &data.Candle{
		Token: 123, Time: baseTime.Add(75 * time.Minute), Open: 195.76, High: 195.79, Low: 195.47, Close: 195.47, Volume: 91473,
	}
	engine.OnCandleClose(c1030, symbol)

	// 7. 10:35 AM Candle (during formation): Price surges to 197.00.
	// Since 10:30 volume (91.47K) > 10:20 volume (57.18K), 10:30 is NOT the lowest volume of the day!
	// Option A strictly rejects this breakout!
	sig3 := engine.CheckBreakout(symbol, 197.00, "BUY_ONLY")
	if sig3 != nil {
		t.Fatalf("expected NO signal on 10:30 candle because 10:30 (91.47K) was not the day's lowest volume (10:20 had 57.18K), got: %+v", sig3)
	}

	// 8. 10:40 AM Candle Closes with a NEW lowest volume of the day (40K < 57.18K, Red, High 196.50)
	c1040 := &data.Candle{
		Token: 123, Time: baseTime.Add(85 * time.Minute), Open: 196.40, High: 196.50, Low: 196.10, Close: 196.20, Volume: 40000,
	}
	engine.OnCandleClose(c1040, symbol)

	// 9. 10:45 AM Candle: Price breaks above 196.50 -> Now BUY is strictly allowed on the new lowest volume setup!
	sig4 := engine.CheckBreakout(symbol, 196.60, "BUY_ONLY")
	if sig4 == nil || sig4.Action != "BUY" {
		t.Fatalf("expected BUY signal after new lowest volume setup (40k < 57k), got: %+v", sig4)
	}
}
