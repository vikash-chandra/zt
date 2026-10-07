package scanner

import (
	"context"
	"sync"
	"testing"
	"time"

	"zerodha-trading/data"

	"github.com/zerodha/gokiteconnect/v4/models"
	"go.uber.org/zap"
)

func TestRingBuffer50(t *testing.T) {
	rb := &RingBuffer50{}

	// Test initial state
	if rb.SMA() != 0 {
		t.Fatalf("expected initial SMA to be 0, got %f", rb.SMA())
	}

	// Add 10 values of 100
	for i := 0; i < 10; i++ {
		sma := rb.Add(100)
		if sma != 100 {
			t.Fatalf("expected SMA to be 100, got %f at index %d", sma, i)
		}
	}
	if rb.count != 10 {
		t.Fatalf("expected count 10, got %d", rb.count)
	}

	// Fill up to 50 with 100
	for i := 10; i < 50; i++ {
		rb.Add(100)
	}
	if rb.count != 50 || rb.SMA() != 100 {
		t.Fatalf("expected full buffer with SMA 100, got count %d, SMA %f", rb.count, rb.SMA())
	}

	// Add a new value of 200, displacing one 100
	// New sum = (49 * 100) + 200 = 5100 -> SMA = 5100 / 50 = 102.0
	newSMA := rb.Add(200)
	if newSMA != 102.0 {
		t.Fatalf("expected SMA 102.0 after eviction, got %f", newSMA)
	}
}

func TestFootprintScanner_TickTestAndCVD(t *testing.T) {
	logger := zap.NewNop()
	fs := NewFootprintScanner(nil, nil, logger, nil)
	defer fs.Close()

	token := int64(123456)
	symbol := "TEST_STOCK"
	fs.RegisterInstrument(token, symbol)

	// Tick 1: Baseline initialization
	fs.ProcessTick(models.Tick{
		InstrumentToken:    uint32(token),
		LastPrice:          100.0,
		VolumeTraded:       1000,
		LastTradedQuantity: 50,
	})

	cvd, _, price, found := fs.GetTrackerState(token)
	if !found {
		t.Fatalf("instrument should be found")
	}
	if price != 100.0 || cvd != 0 {
		t.Fatalf("expected initial price 100 and CVD 0, got price %f, CVD %d", price, cvd)
	}

	// Tick 2: Price moves UP to 101.0, VolumeTraded 1500 (IncVol = +500).
	// Uptick direction = +1 -> CVD = +500
	fs.ProcessTick(models.Tick{
		InstrumentToken:    uint32(token),
		LastPrice:          101.0,
		VolumeTraded:       1500,
		LastTradedQuantity: 50,
	})

	cvd, _, price, _ = fs.GetTrackerState(token)
	if price != 101.0 || cvd != 500 {
		t.Fatalf("expected price 101.0 and CVD 500, got price %f, CVD %d", price, cvd)
	}

	// Tick 3: Price moves DOWN to 100.5, VolumeTraded 1800 (IncVol = +300).
	// Downtick direction = -1 -> CVD = 500 - 300 = 200
	fs.ProcessTick(models.Tick{
		InstrumentToken:    uint32(token),
		LastPrice:          100.5,
		VolumeTraded:       1800,
		LastTradedQuantity: 50,
	})

	cvd, _, price, _ = fs.GetTrackerState(token)
	if price != 100.5 || cvd != 200 {
		t.Fatalf("expected price 100.5 and CVD 200, got price %f, CVD %d", price, cvd)
	}

	// Tick 4: Price UNCHANGED at 100.5, VolumeTraded 2000 (IncVol = +200).
	// Inherit previous direction (-1) -> CVD = 200 - 200 = 0
	fs.ProcessTick(models.Tick{
		InstrumentToken:    uint32(token),
		LastPrice:          100.5,
		VolumeTraded:       2000,
		LastTradedQuantity: 50,
	})

	cvd, _, price, _ = fs.GetTrackerState(token)
	if price != 100.5 || cvd != 0 {
		t.Fatalf("expected price 100.5 and CVD 0, got price %f, CVD %d", price, cvd)
	}
}

func TestFootprintScanner_BlockTrade10XTrigger(t *testing.T) {
	logger := zap.NewNop()
	var detectedRecord *data.FootprintRecord
	var mu sync.Mutex

	fs := NewFootprintScanner(nil, nil, logger, func(rec *data.FootprintRecord) {
		mu.Lock()
		detectedRecord = rec
		mu.Unlock()
	})
	defer fs.Close()

	token := int64(789012)
	symbol := "RELIANCE"
	fs.RegisterInstrument(token, symbol)

	// Seed 25 retail ticks of ~50 shares each
	vol := uint32(10000)
	price := 2500.0
	for i := 0; i < 25; i++ {
		vol += 50
		price += 0.05
		fs.ProcessTick(models.Tick{
			InstrumentToken:    uint32(token),
			LastPrice:          price,
			VolumeTraded:       vol,
			LastTradedQuantity: 50,
		})
	}

	// Now send a single massive trade of 800 shares (which is 16x the 50-share baseline)
	vol += 800
	price += 0.50
	fs.ProcessTick(models.Tick{
		InstrumentToken:    uint32(token),
		LastPrice:          price,
		VolumeTraded:       vol,
		LastTradedQuantity: 800,
	})

	// Wait briefly for asynchronous callback
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	rec := detectedRecord
	mu.Unlock()

	if rec == nil {
		t.Fatalf("expected 10X block trade trigger to fire, but received nil")
	}
	if rec.TradingSymbol != "RELIANCE" {
		t.Fatalf("expected symbol RELIANCE, got %s", rec.TradingSymbol)
	}
	if rec.Volume != 800 {
		t.Fatalf("expected volume 800, got %d", rec.Volume)
	}
}

func TestFootprintScanner_AbsorptionDivergence(t *testing.T) {
	logger := zap.NewNop()
	var detectedRecord *data.FootprintRecord
	var mu sync.Mutex

	fs := NewFootprintScanner(nil, nil, logger, func(rec *data.FootprintRecord) {
		mu.Lock()
		detectedRecord = rec
		mu.Unlock()
	})
	defer fs.Close()

	token := int64(456789)
	symbol := "TCS"
	fs.RegisterInstrument(token, symbol)

	// Simulate 35 ticks where price stays within 0.05% (flat) but aggressive buy volume pours in (CVD > 50,000)
	vol := uint32(100000)
	basePrice := 3500.0

	// Initial tick
	fs.ProcessTick(models.Tick{
		InstrumentToken:    uint32(token),
		LastPrice:          basePrice,
		VolumeTraded:       vol,
		LastTradedQuantity: 100,
	})

	for i := 0; i < 32; i++ {
		vol += 2000 // Total incremental buy volume = 32 * 2000 = 64,000 shares
		// Price remains flat at 3500.05 (spread = 0.0014% <= 0.15% compression threshold, buyer uptick direction inherited)
		price := basePrice + 0.05
		fs.ProcessTick(models.Tick{
			InstrumentToken:    uint32(token),
			LastPrice:          price,
			VolumeTraded:       vol,
			LastTradedQuantity: 2000,
		})
	}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	rec := detectedRecord
	mu.Unlock()

	if rec == nil {
		t.Fatalf("expected Absorption trigger to fire, but received nil")
	}
	if rec.TradingSymbol != "TCS" {
		t.Fatalf("expected symbol TCS, got %s", rec.TradingSymbol)
	}
}

func TestFootprintScanner_ConcurrentTicks(t *testing.T) {
	logger := zap.NewNop()
	fs := NewFootprintScanner(nil, nil, logger, nil)
	defer fs.Close()

	numInstruments := 20
	ticksPerInstrument := 100

	for i := 1; i <= numInstruments; i++ {
		fs.RegisterInstrument(int64(i), "STOCK")
	}

	var wg sync.WaitGroup
	for i := 1; i <= numInstruments; i++ {
		wg.Add(1)
		go func(token int64) {
			defer wg.Done()
			vol := uint32(1000)
			price := 100.0
			for j := 0; j < ticksPerInstrument; j++ {
				vol += 10
				price += 0.05
				fs.ProcessTick(models.Tick{
					InstrumentToken:    uint32(token),
					LastPrice:          price,
					VolumeTraded:       vol,
					LastTradedQuantity: 10,
				})
			}
		}(int64(i))
	}

	wg.Wait()

	// Verify all instruments have non-zero recorded state
	for i := 1; i <= numInstruments; i++ {
		_, _, price, found := fs.GetTrackerState(int64(i))
		if !found || price == 0 {
			t.Fatalf("expected instrument %d to have valid state", i)
		}
	}
}

func TestFootprintScanner_Recalculate(t *testing.T) {
	logger := zap.NewNop()
	fs := NewFootprintScanner(nil, nil, logger, nil)
	defer fs.Close()

	token := int64(999999)
	fs.RegisterInstrument(token, "RECALC_STOCK")

	// Send a few ticks
	fs.ProcessTick(models.Tick{
		InstrumentToken:    uint32(token),
		LastPrice:          150.0,
		VolumeTraded:       1000,
		LastTradedQuantity: 20,
	})
	fs.ProcessTick(models.Tick{
		InstrumentToken:    uint32(token),
		LastPrice:          151.0,
		VolumeTraded:       1200,
		LastTradedQuantity: 20,
	})

	// Invoke recalculate
	err := fs.RecalculateFootprints(context.Background())
	if err != nil {
		t.Fatalf("recalculate failed: %v", err)
	}
}

func TestFootprintScanner_AnalyticalMetrics(t *testing.T) {
	logger := zap.NewNop()
	var detectedRecord *data.FootprintRecord
	var mu sync.Mutex

	fs := NewFootprintScanner(nil, nil, logger, func(rec *data.FootprintRecord) {
		mu.Lock()
		detectedRecord = rec
		mu.Unlock()
	})
	defer fs.Close()

	token := int64(999888)
	symbol := "INFY"
	fs.RegisterInstrument(token, symbol)

	// Seed 25 ticks with retail LTQ=50
	vol := uint32(10000)
	price := 1500.0
	for i := 0; i < 25; i++ {
		vol += 50
		price += 0.10
		fs.ProcessTick(models.Tick{
			InstrumentToken:    uint32(token),
			LastPrice:          price,
			VolumeTraded:       vol,
			LastTradedQuantity: 50,
			AverageTradePrice:  1495.0,
			OHLC: models.OHLC{
				Open:  1490.0,
				High:  1510.0,
				Low:   1485.0,
				Close: 1490.0,
			},
			OI: 250000,
		})
	}

	// Trigger 10X block deal with 1,000 shares
	vol += 1000
	price += 0.50
	fs.ProcessTick(models.Tick{
		InstrumentToken:    uint32(token),
		LastPrice:          price,
		VolumeTraded:       vol,
		LastTradedQuantity: 1000,
		AverageTradePrice:  1495.0,
		OHLC: models.OHLC{
			Open:  1490.0,
			High:  1510.0,
			Low:   1485.0,
			Close: 1490.0,
		},
		OI: 250000,
	})

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	rec := detectedRecord
	mu.Unlock()

	if rec == nil {
		t.Fatalf("expected footprint record to be generated")
	}

	expectedTradeVal := price * 1000.0
	if rec.TradeValue != expectedTradeVal {
		t.Errorf("expected TradeValue %f, got %f", expectedTradeVal, rec.TradeValue)
	}

	if rec.Side != "BUY" {
		t.Errorf("expected Side BUY, got %s", rec.Side)
	}

	if rec.Multiplier < 10.0 {
		t.Errorf("expected Multiplier >= 10x, got %f", rec.Multiplier)
	}

	if rec.VWAP != 1495.0 {
		t.Errorf("expected VWAP 1495.0, got %f", rec.VWAP)
	}

	if rec.VWAPDiffPct <= 0 {
		t.Errorf("expected positive VWAPDiffPct, got %f", rec.VWAPDiffPct)
	}

	if rec.DayHigh != 1510.0 || rec.DayLow != 1485.0 {
		t.Errorf("expected DayHigh 1510 and DayLow 1485, got %f and %f", rec.DayHigh, rec.DayLow)
	}

	if rec.DayRangePct <= 0 || rec.DayRangePct > 100 {
		t.Errorf("expected DayRangePct between 0 and 100, got %f", rec.DayRangePct)
	}

	if rec.OI != 250000 {
		t.Errorf("expected OI 250000, got %d", rec.OI)
	}
}
