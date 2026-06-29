package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"text/tabwriter"
	"time"

	"zerodha-trading/config"

	_ "github.com/lib/pq"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Query row counts
	counts := make(map[string]int)
	tables := []string{"candles_1m", "candles_5m", "orders", "positions", "trades", "market_breadth_logs"}
	for _, t := range tables {
		var cnt int
		err = db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", t)).Scan(&cnt)
		if err != nil {
			log.Fatalf("Failed to count rows in %s: %v", t, err)
		}
		counts[t] = cnt
	}

	fmt.Println("==================================================================")
	fmt.Println("             ZERODHA TRADING BOT DATABASE REPORT                 ")
	fmt.Println("==================================================================")
	fmt.Println()
	fmt.Println("--- Database Row Counts ---")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "Table Name\tRow Count")
	fmt.Fprintln(w, "----------\t---------")
	for _, t := range tables {
		fmt.Fprintf(w, "%s\t%d\n", t, counts[t])
	}
	w.Flush()
	fmt.Println()

	// Query Trades Detail
	fmt.Println("--- Executed Trades & P&L Summary ---")
	rows, err := db.Query("SELECT symbol, entry_price, exit_price, quantity, pnl, side, time_held_minutes, created_at FROM trades ORDER BY created_at DESC")
	if err != nil {
		log.Fatalf("Failed to query trades: %v", err)
	}
	defer rows.Close()

	type Trade struct {
		Symbol          string
		EntryPrice      float64
		ExitPrice       float64
		Quantity        int
		PnL             float64
		Side            string
		TimeHeldMinutes int
		CreatedAt       time.Time
	}

	var trades []Trade
	var totalPnL float64 = 0.0

	for rows.Next() {
		var t Trade
		err := rows.Scan(&t.Symbol, &t.EntryPrice, &t.ExitPrice, &t.Quantity, &t.PnL, &t.Side, &t.TimeHeldMinutes, &t.CreatedAt)
		if err != nil {
			log.Fatalf("Failed to scan trade: %v", err)
		}
		trades = append(trades, t)
		totalPnL += t.PnL
	}

	if len(trades) == 0 {
		fmt.Println("No completed trades recorded in the database yet.")
	} else {
		w2 := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w2, "Time\tSymbol\tSide\tQty\tEntry\tExit\tMin Held\tP&L (INR)")
		fmt.Fprintln(w2, "----\t------\t----\t---\t-----\t----\t--------\t---------")
		for _, t := range trades {
			fmt.Fprintf(w2, "%s\t%s\t%s\t%d\t%.2f\t%.2f\t%d\t%.2f\n",
				t.CreatedAt.Format("2006-01-02 15:04:02"), t.Symbol, t.Side, t.Quantity, t.EntryPrice, t.ExitPrice, t.TimeHeldMinutes, t.PnL)
		}
		w2.Flush()
	}
	fmt.Println()
	fmt.Printf("Total Completed Trades: %d\n", len(trades))
	fmt.Printf("Total P&L: INR %.2f\n", totalPnL)
	fmt.Println()

	// Query Open Positions
	fmt.Println("--- Current Open Positions ---")
	pRows, err := db.Query("SELECT symbol, quantity, entry_price, current_price, side, sl_price, created_at FROM positions WHERE closed_at IS NULL")
	if err != nil {
		log.Fatalf("Failed to query positions: %v", err)
	}
	defer pRows.Close()

	type Position struct {
		Symbol       string
		Quantity     int
		EntryPrice   float64
		CurrentPrice float64
		Side         string
		SLPrice      float64
		CreatedAt    time.Time
	}

	var positions []Position
	for pRows.Next() {
		var p Position
		var sl sql.NullFloat64
		var cur sql.NullFloat64
		err := pRows.Scan(&p.Symbol, &p.Quantity, &p.EntryPrice, &cur, &p.Side, &sl, &p.CreatedAt)
		if err != nil {
			log.Fatalf("Failed to scan position: %v", err)
		}
		if sl.Valid {
			p.SLPrice = sl.Float64
		}
		if cur.Valid {
			p.CurrentPrice = cur.Float64
		}
		positions = append(positions)
	}

	if len(positions) == 0 {
		fmt.Println("No active open positions.")
	} else {
		w3 := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w3, "Opened At\tSymbol\tSide\tQty\tEntry\tCurrent LTP\tSL Price")
		fmt.Fprintln(w3, "---------\t------\t----\t---\t-----\t-----------\t--------")
		for _, p := range positions {
			fmt.Fprintf(w3, "%s\t%s\t%s\t%d\t%.2f\t%.2f\t%.2f\n",
				p.CreatedAt.Format("2006-01-02 15:04:02"), p.Symbol, p.Side, p.Quantity, p.EntryPrice, p.CurrentPrice, p.SLPrice)
		}
		w3.Flush()
	}
	fmt.Println("==================================================================")
}
