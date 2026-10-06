package main

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	db, err := sql.Open("postgres", "postgres://postgres:postgres@3.7.29.3:5432/zerodha_trading?sslmode=disable")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	loc, _ := time.LoadLocation("Asia/Kolkata")

	rows, err := db.Query(`SELECT order_id, symbol, strategy, transaction_type, order_type, price, trigger_price, status, placed_at 
		FROM orders WHERE symbol IN ('VOLTAS', 'UNIMECH') ORDER BY placed_at ASC`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	fmt.Println("=== ORDERS FOR VOLTAS & UNIMECH ===")
	for rows.Next() {
		var oid, sym, strat, txn, otype, status string
		var price, trigger sql.NullFloat64
		var placed time.Time
		if err := rows.Scan(&oid, &sym, &strat, &txn, &otype, &price, &trigger, &status, &placed); err != nil {
			panic(err)
		}
		fmt.Printf("%s | %s | %s | %s | %s | P:%.2f | Trig:%.2f | %s | %s\n",
			placed.In(loc).Format("2006-01-02 15:04:05"), sym, strat, txn, otype, price.Float64, trigger.Float64, status, oid)
	}
}
