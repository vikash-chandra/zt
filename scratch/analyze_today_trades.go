package main

import (
	"database/sql"
	"fmt"
	"log"
	"math"
	"time"

	_ "github.com/lib/pq"
)

type Trade struct {
	ID        int
	Symbol    string
	Entry     float64
	Exit      float64
	Quantity  int
	PnL       float64
	CreatedAt time.Time
	Strategy  string
	EntryTime sql.NullTime
}

func main() {
	dsn := "host=127.0.0.1 port=5432 user=postgres password=trading_password dbname=zerodha_trading sslmode=disable"
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Query trades joined with their corresponding entry order to get entry timestamps
	rows, err := db.Query(`
		SELECT t.id, t.symbol, t.entry_price, t.exit_price, t.quantity, t.pnl, t.created_at, t.strategy, o.placed_at
		FROM trades t
		LEFT JOIN LATERAL (
			SELECT placed_at 
			FROM orders 
			WHERE symbol = t.symbol 
			  AND quantity = t.quantity 
			  AND transaction_type = t.side 
			  AND status = 'COMPLETE' 
			  AND placed_at < t.created_at
			ORDER BY placed_at DESC
			LIMIT 1
		) o ON TRUE
		WHERE t.created_at >= '2026-07-06 00:00:00+00'
		ORDER BY t.id ASC
	`)
	if err != nil {
		log.Fatalf("Failed to query trades: %v", err)
	}
	defer rows.Close()

	var trades []Trade
	var totalPnL float64
	var wins, losses, breakevens int
	var grossProfits, grossLosses float64

	strategyStats := make(map[string]struct {
		Count int
		PnL   float64
		Wins  int
	})

	symbolPnL := make(map[string]float64)

	fmt.Println("--- TODAY'S COMPLETED TRADES WITH ENTRY/EXIT TIMESTAMPS (2026-07-06) ---")
	fmt.Printf("%-3s %-12s %-10s %-10s %-6s %-10s %-12s %-12s %-12s\n", 
		"ID", "Symbol", "Entry Px", "Exit Px", "Qty", "PnL", "Strategy", "Entry Time", "Exit Time")
	fmt.Println(string(make([]byte, 95)))

	for rows.Next() {
		var t Trade
		err := rows.Scan(&t.ID, &t.Symbol, &t.Entry, &t.Exit, &t.Quantity, &t.PnL, &t.CreatedAt, &t.Strategy, &t.EntryTime)
		if err != nil {
			log.Printf("Scan error: %v", err)
			continue
		}
		trades = append(trades, t)
		totalPnL += t.PnL
		symbolPnL[t.Symbol] += t.PnL

		// Strategy stats
		stats := strategyStats[t.Strategy]
		stats.Count++
		stats.PnL += t.PnL
		if t.PnL > 0 {
			stats.Wins++
		}
		strategyStats[t.Strategy] = stats

		// Wins/losses
		if t.PnL > 0 {
			wins++
			grossProfits += t.PnL
		} else if t.PnL < 0 {
			losses++
			grossLosses += math.Abs(t.PnL)
		} else {
			breakevens++
		}

		entryTimeStr := "N/A"
		if t.EntryTime.Valid {
			entryTimeStr = t.EntryTime.Time.Local().Format("15:04:05")
		}
		exitTimeStr := t.CreatedAt.Local().Format("15:04:05")

		fmt.Printf("%-3d %-12s %-10.2f %-10.2f %-6d %-10.2f %-12s %-12s %-12s\n",
			t.ID, t.Symbol, t.Entry, t.Exit, t.Quantity, t.PnL, t.Strategy, entryTimeStr, exitTimeStr)
	}

	fmt.Println(string(make([]byte, 95)))
	fmt.Printf("Total Trades: %d (Wins: %d, Losses: %d, Breakeven: %d)\n", len(trades), wins, losses, breakevens)
	fmt.Printf("Net Realized PnL: INR %.2f\n", totalPnL)

	// Win Rate
	var winRate float64
	if (wins + losses) > 0 {
		winRate = (float64(wins) / float64(wins+losses)) * 100.0
	}
	fmt.Printf("Win Rate: %.2f%%\n", winRate)

	// Profit Factor
	profitFactor := 0.0
	if grossLosses > 0 {
		profitFactor = grossProfits / grossLosses
	} else if grossProfits > 0 {
		profitFactor = 99.9 // Infinity fallback
	}
	fmt.Printf("Profit Factor: %.2f\n", profitFactor)
}
