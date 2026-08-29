---
name: analyze-trades
description: Analyze trade performance and generate insights
---
# Trade Analysis Skill

Queries, inspects, and analyzes executed and simulated trades from PostgreSQL to generate institutional performance scorecards, equity curves, and strategy insights.

## Quick CLI & Query Commands

| Scope | Method | Command |
| :--- | :--- | :--- |
| **Local DB Report Script** | Go CLI | `go run scripts/db_report/main.go` |
| **AWS Remote DB Interactive Shell** | PowerShell Utility | `.\myaws.ps1 db` |
| **AWS Direct PSQL Query** | SSH Execution | `ssh -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -it zt-postgres-1 psql -U postgres -d zerodha_trading -c 'SELECT * FROM trades ORDER BY created_at DESC LIMIT 10;'"` |
| **REST API Full Trade Log** | HTTP JSON | `GET /api/trades/all` |

---

## Database `trades` Table Schema

| Column | Type | Description |
| :--- | :--- | :--- |
| `order_id` | `VARCHAR(100)` | Unique broker or simulated order ID |
| `symbol` | `VARCHAR(50)` | Trading symbol (e.g. `SBIN`, `NIFTY24800CE`) |
| `strategy` | `VARCHAR(50)` | Originating strategy (`LOW_VOLUME`, `VANDE_BHARAT`, `OPTIONS_SUPERTREND`) |
| `side` | `VARCHAR(10)` | Trade action (`BUY`, `SELL`, `SELL_PE`, `SELL_CE`) |
| `quantity` | `INT` | Executed share / lot quantity |
| `entry_price` | `NUMERIC` | Average fill entry price |
| `exit_price` | `NUMERIC` | Average fill exit price |
| `pnl` | `NUMERIC` | Realized Net P&L in ₹ (INR) |
| `status` | `VARCHAR(50)` | Exit classification (`PROFIT EXIT`, `50% SL HIT`, `TRAIL_SL_HIT`, `EOD SQUARE-OFF`, `REVERSAL EXIT`) |
| `time_held_minutes` | `INT` | Dynamic duration in minutes |
| `created_at` | `TIMESTAMPTZ` | Timestamp recorded using PostgreSQL IST server clock |

---

## Key SQL Analysis Queries

### 1. Strategy Performance Summary
```sql
SELECT 
    strategy,
    COUNT(*) AS total_trades,
    COUNT(CASE WHEN pnl > 0 THEN 1 END) AS winning_trades,
    ROUND(COUNT(CASE WHEN pnl > 0 THEN 1 END)::NUMERIC / COUNT(*) * 100, 2) AS win_rate_pct,
    ROUND(SUM(pnl), 2) AS net_pnl,
    ROUND(SUM(CASE WHEN pnl > 0 THEN pnl ELSE 0 END) / NULLIF(ABS(SUM(CASE WHEN pnl < 0 THEN pnl ELSE 0 END)), 0), 2) AS profit_factor,
    ROUND(AVG(pnl), 2) AS avg_trade_pnl,
    ROUND(AVG(time_held_minutes), 1) AS avg_holding_mins
FROM trades
GROUP BY strategy;
```

### 2. Exit Reason Breakdown
```sql
SELECT 
    strategy,
    status AS exit_reason,
    COUNT(*) AS trade_count,
    ROUND(SUM(pnl), 2) AS total_pnl,
    ROUND(AVG(pnl), 2) AS avg_pnl
FROM trades
GROUP BY strategy, status
ORDER BY strategy, trade_count DESC;
```

---

## Mandatory Time & Integrity Guidelines
- **IST Time Normalization**: Format all queried trade timestamps with `data.NormalizeToIST(t)` (`Asia/Kolkata`) to prevent 5.5-hour UTC shifts.
- **Dynamic Holding Time**: Always verify that `time_held_minutes` is calculated dynamically (`int(exitTime.Sub(entryTime).Minutes())`).
