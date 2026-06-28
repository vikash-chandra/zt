package main

import (
	"context"
	"log"
	"math/rand"
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
)

type tick1m struct {
	open   float64
	high   float64
	low    float64
	close  float64
	volume int64
}

func main() {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create logger
	logger, err := monitoring.NewLogger(cfg.LogLevel)
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}

	// Connect to database
	db, err := data.NewDatabase(
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		logger.Logger,
	)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	// Initialize schema if not already initialized
	if err := db.InitSchema(); err != nil {
		log.Fatalf("Schema initialization failed: %v", err)
	}

	// Create Kite Client
	kiteClient := kiteconnect.New(cfg.APIKey)
	kiteClient.SetAccessToken(cfg.AccessToken)

	// Create security master (uses DB context and Kite client)
	ctx := context.Background()
	securityMaster := data.NewSecurityMaster(db.WithContext(ctx), kiteClient, logger.Logger)

	// Fetch Nifty 50 constituents
	watchlist, err := securityMaster.GetNifty50Constituents(ctx)
	if err != nil {
		log.Fatalf("Failed to fetch Nifty 50 constituents: %v", err)
	}

	log.Printf("Seeding 1 week of historical data for %d Nifty 50 instruments...", len(watchlist))

	// Define time bounds (last 7 calendar days)
	now := time.Now().UTC()
	startDate := now.AddDate(0, 0, -7)

	total1mInserted := 0
	total5mInserted := 0

	tx, err := db.WithContext(ctx).BeginTx(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to begin transaction: %v", err)
	}

	stmt1m, err := tx.PrepareContext(ctx, `
		INSERT INTO candles_1m (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (token, time) DO NOTHING
	`)
	if err != nil {
		tx.Rollback()
		log.Fatalf("Failed to prepare statement 1m: %v", err)
	}
	defer stmt1m.Close()

	stmt5m, err := tx.PrepareContext(ctx, `
		INSERT INTO candles_5m (token, time, open, high, low, close, volume, vwap, bid, ask, tick_count, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (token, time) DO NOTHING
	`)
	if err != nil {
		tx.Rollback()
		log.Fatalf("Failed to prepare statement 5m: %v", err)
	}
	defer stmt5m.Close()

	// Try fetching from real Zerodha API first
	var liveSeeded = false
	if cfg.AccessToken != "" && cfg.AccessToken != "your_access_token_here" {
		log.Printf("Active KITE_ACCESS_TOKEN detected. Attempting to fetch real historical data from Zerodha...")

		for symbol, token := range watchlist {
			log.Printf("Fetching real historical candles for %s (Token: %d)...", symbol, token)

			// Fetch 1m candles
			candles1m, err := kiteClient.GetHistoricalData(int(token), "minute", startDate, now, false, false)
			if err != nil {
				log.Printf("Warning: Failed to fetch 1m historical data for %s: %v. Switching to simulation mode.", symbol, err)
				liveSeeded = false
				break
			}

			// Fetch 5m candles
			candles5m, err := kiteClient.GetHistoricalData(int(token), "5minute", startDate, now, false, false)
			if err != nil {
				log.Printf("Warning: Failed to fetch 5m historical data for %s: %v. Switching to simulation mode.", symbol, err)
				liveSeeded = false
				break
			}

			// Insert 1m candles
			for _, c := range candles1m {
				color := "DOJI"
				if c.Close > c.Open {
					color = "GREEN"
				} else if c.Close < c.Open {
					color = "RED"
				}
				vwap := (c.Open + c.High + c.Low + c.Close) / 4.0

				_, err = stmt1m.ExecContext(ctx, token, c.Date.Time, c.Open, c.High, c.Low, c.Close, int64(c.Volume), vwap, c.Low, c.High, 100, color)
				if err != nil {
					tx.Rollback()
					log.Fatalf("Failed to insert live 1m candle: %v", err)
				}
				total1mInserted++
			}

			// Insert 5m candles
			for _, c := range candles5m {
				color := "DOJI"
				if c.Close > c.Open {
					color = "GREEN"
				} else if c.Close < c.Open {
					color = "RED"
				}
				vwap := (c.Open + c.High + c.Low + c.Close) / 4.0

				_, err = stmt5m.ExecContext(ctx, token, c.Date.Time, c.Open, c.High, c.Low, c.Close, int64(c.Volume), vwap, c.Low, c.High, 500, color)
				if err != nil {
					tx.Rollback()
					log.Fatalf("Failed to insert live 5m candle: %v", err)
				}
				total5mInserted++
			}

			liveSeeded = true

			// Sleep to respect Zerodha API rate limits (3 requests per second limit)
			time.Sleep(350 * time.Millisecond)
		}
	}

	// Fallback to simulation if live data seeding was not possible or failed
	if !liveSeeded {
		log.Printf("Starting procedural simulation to generate historical data...")
		// Clear counts
		total1mInserted = 0
		total5mInserted = 0

		r := rand.New(rand.NewSource(time.Now().UnixNano()))

		for _, token := range watchlist {
			basePrice := 100.0 + r.Float64()*2900.0
			currentPrice := basePrice

			for d := 0; d <= 7; d++ {
				day := startDate.AddDate(0, 0, d)
				if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
					continue
				}

				marketOpen := time.Date(day.Year(), day.Month(), day.Day(), 9, 15, 0, 0, time.UTC)
				marketClose := time.Date(day.Year(), day.Month(), day.Day(), 15, 30, 0, 0, time.UTC)

				var cur5mCandles []tick1m
				var cur5mStart time.Time

				for t := marketOpen; t.Before(marketClose); t = t.Add(1 * time.Minute) {
					if t.After(now) {
						break
					}

					if len(cur5mCandles) == 0 {
						cur5mStart = t
					}

					pctChange := (r.Float64() - 0.495) * 0.0016
					open1m := currentPrice
					close1m := open1m * (1.0 + pctChange)

					var high1m, low1m float64
					if close1m > open1m {
						high1m = close1m * (1.0 + r.Float64()*0.0006)
						low1m = open1m * (1.0 - r.Float64()*0.0006)
					} else {
						high1m = open1m * (1.0 + r.Float64()*0.0006)
						low1m = close1m * (1.0 - r.Float64()*0.0006)
					}

					volume1m := int64(1000 + r.Intn(9000))
					vwap1m := (open1m + high1m + low1m + close1m) / 4.0
					color1m := "DOJI"
					if close1m > open1m {
						color1m = "GREEN"
					} else if close1m < open1m {
						color1m = "RED"
					}
					tickCount1m := 20 + r.Intn(180)

					_, err = stmt1m.ExecContext(ctx, token, t, open1m, high1m, low1m, close1m, volume1m, vwap1m, low1m, high1m, tickCount1m, color1m)
					if err != nil {
						tx.Rollback()
						log.Fatalf("Failed to execute insert 1m: %v", err)
					}
					total1mInserted++

					cur5mCandles = append(cur5mCandles, tick1m{
						open:   open1m,
						high:   high1m,
						low:    low1m,
						close:  close1m,
						volume: volume1m,
					})

					if len(cur5mCandles) == 5 {
						open5m := cur5mCandles[0].open
						close5m := cur5mCandles[len(cur5mCandles)-1].close
						high5m := cur5mCandles[0].high
						low5m := cur5mCandles[0].low
						volume5m := int64(0)

						for _, c := range cur5mCandles {
							if c.high > high5m {
								high5m = c.high
							}
							if c.low < low5m {
								low5m = c.low
							}
							volume5m += c.volume
						}

						vwap5m := (open5m + high5m + low5m + close5m) / 4.0
						color5m := "DOJI"
						if close5m > open5m {
							color5m = "GREEN"
						} else if close5m < open5m {
							color5m = "RED"
						}
						tickCount5m := 100 + r.Intn(900)

						_, err = stmt5m.ExecContext(ctx, token, cur5mStart, open5m, high5m, low5m, close5m, volume5m, vwap5m, low5m, high5m, tickCount5m, color5m)
						if err != nil {
							tx.Rollback()
							log.Fatalf("Failed to execute insert 5m: %v", err)
						}
						total5mInserted++

						cur5mCandles = nil
					}

					currentPrice = close1m
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		log.Fatalf("Failed to commit transaction: %v", err)
	}

	if liveSeeded {
		log.Printf("Successfully fetched and seeded %d 1-minute and %d 5-minute REAL historical candles from Zerodha API!", total1mInserted, total5mInserted)
	} else {
		log.Printf("Successfully simulated and seeded %d 1-minute and %d 5-minute historical candles in database!", total1mInserted, total5mInserted)
	}
}
