package main

import (
	"fmt"
	"time"

	"zerodha-trading/data"
	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewDevelopment()

	// Instantiate CandleAggregator with a nil Database reference (safely bypassed by our check)
	agg := data.NewCandleAggregator(nil, logger, 300, 10, "candles_5m")

	token := int64(9999)
	now := time.Now().Truncate(300 * time.Second).Unix()

	// Simulating ticks for Candle 1 (Cumulative volume starts at 50,000, adds 10,000 -> 60,000)
	ticks1 := []*data.Tick{
		{Token: token, LTP: 100.0, Volume: 50000, Timestamp: float64(now)},
		{Token: token, LTP: 101.0, Volume: 55000, Timestamp: float64(now + 10)},
		{Token: token, LTP: 100.5, Volume: 60000, Timestamp: float64(now + 20)},
	}

	// Simulating ticks for Candle 2 (Cumulative volume adds 8,000 -> 68,000)
	ticks2 := []*data.Tick{
		{Token: token, LTP: 102.0, Volume: 62000, Timestamp: float64(now + 300)},
		{Token: token, LTP: 101.5, Volume: 65000, Timestamp: float64(now + 310)},
		{Token: token, LTP: 103.0, Volume: 68000, Timestamp: float64(now + 320)},
	}

	// Simulating ticks for Candle 3 (Cumulative volume adds 120,000 -> 188,000)
	ticks3 := []*data.Tick{
		{Token: token, LTP: 104.0, Volume: 78000, Timestamp: float64(now + 600)},
		{Token: token, LTP: 105.0, Volume: 120000, Timestamp: float64(now + 610)},
		{Token: token, LTP: 106.0, Volume: 188000, Timestamp: float64(now + 620)},
	}

	// final tick to close Candle 3
	finalTick := &data.Tick{Token: token, LTP: 106.0, Volume: 188000, Timestamp: float64(now + 900)}

	fmt.Println("\n--- STARTING INTERVAL VOLUME SIMULATION ---")

	fmt.Println("Processing Candle 1 Ticks (Cumulative volume: 50k -> 60k)...")
	for _, t := range ticks1 {
		agg.ProcessTick(t)
	}

	fmt.Println("Processing Candle 2 Ticks (Cumulative volume: 60k -> 68k)...")
	c1 := agg.ProcessTick(ticks2[0])
	if c1 != nil {
		fmt.Printf("\n[OUTPUT] ✓ Candle 1 Finalized! Volume: %d (Expected: 10,000)\n\n", c1.Volume)
	}
	for i := 1; i < len(ticks2); i++ {
		agg.ProcessTick(ticks2[i])
	}

	fmt.Println("Processing Candle 3 Ticks (Cumulative volume: 68k -> 188k)...")
	c2 := agg.ProcessTick(ticks3[0])
	if c2 != nil {
		fmt.Printf("\n[OUTPUT] ✓ Candle 2 Finalized! Volume: %d (Expected: 8,000)\n\n", c2.Volume)
	}
	for i := 1; i < len(ticks3); i++ {
		agg.ProcessTick(ticks3[i])
	}

	fmt.Println("Finalizing Candle 3...")
	c3 := agg.ProcessTick(finalTick)
	if c3 != nil {
		fmt.Printf("\n[OUTPUT] ✓ Candle 3 Finalized! Volume: %d (Expected: 120,000)\n\n", c3.Volume)
	}
	fmt.Println("--- SIMULATION COMPLETE ---")
}
