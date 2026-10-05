---
name: database-ops
description: Full autonomous database operational authority, procedures, and runbooks to query, inspect, analyze, repair, and execute commands across local and remote AWS TimescaleDB/PostgreSQL instances.
---

# Database Operations & Autonomous Query Authority (`database-ops`)

This skill defines the operational runbooks, connection profiles, execution standards, and pre-approved query commands for the Zerodha Trading Bot's TimescaleDB/PostgreSQL databases across all environments.

> **User Authorization Mandate**:
> The user has granted the agent **full, unrestricted autonomous permission** to query, inspect, execute administrative commands, analyze performance, apply schema adjustments, and perform maintenance on **any database** (local Docker, native, or remote AWS production TimescaleDB) across the workspace without pausing for routine confirmation.

---

## 1. Connection Profiles & Environment Architectures

### A. Remote AWS Production TimescaleDB (Default)
- **Host**: AWS EC2 `3.7.29.3`
- **SSH Key**: `up-trade-vikash.pem` (User: `ubuntu`)
- **Docker Container**: `zt-postgres-1`
- **Engine**: TimescaleDB (PostgreSQL 15+)
- **Database Name**: `zerodha_trading`
- **Database User**: `postgres`
- **Internal Port**: `5432`

#### Standard Remote Query Command Pattern (Recommended)
Piping SQL into SSH is the most robust method from Windows PowerShell, eliminating all nested quote-escaping issues:
```powershell
"SELECT relname, n_live_tup FROM pg_stat_user_tables WHERE schemaname = 'public' ORDER BY n_live_tup DESC;" | ssh -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading"
```

#### Direct Execution Pattern with Detached Stdin (`-n`)
```powershell
ssh -n -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -c '<SQL_COMMAND>'"
```

#### Multi-Line / Formatted Query Pattern (Expanded Vertical Mode `-x`)
```powershell
"SELECT * FROM trades ORDER BY created_at DESC LIMIT 3;" | ssh -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -x"
```

#### Secure Local Port-Forwarding Tunnel (Optional)
To expose the remote TimescaleDB instance locally on `localhost:5432`:
```powershell
powershell -ExecutionPolicy Bypass -File .\myaws.ps1 tunnel
```
*(Or directly: `ssh -i .\up-trade-vikash.pem -N -L 5432:127.0.0.1:5432 ubuntu@3.7.29.3`)*

---

### B. Local Database (Development / Docker Compose)
- **Host**: `localhost` (or `127.0.0.1`)
- **Port**: `5432`
- **Database Name**: `zerodha_trading`
- **User**: `postgres`
- **Password**: `trading_password`
- **Docker Command**:
  ```powershell
  docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -c "<SQL_COMMAND>"
  ```

---

## 2. Core Database Schema & Key Entities

| Table / Hypertable | Type | Primary Purpose | Key Columns |
| :--- | :--- | :--- | :--- |
| `candles_5m` | Timescale Hypertable | 5-Minute OHLCV technical candles | `token`, `time`, `open`, `high`, `low`, `close`, `volume`, `color` |
| `candles_1m` | Timescale Hypertable | 1-Minute OHLCV technical candles | `token`, `time`, `open`, `high`, `low`, `close`, `volume`, `color` |
| `trades` | Standard Table | Closed executed trades and paper records | `order_id`, `symbol`, `side`, `quantity`, `entry_price`, `exit_price`, `pnl`, `created_at`, `time_held_minutes`, `exit_reason` |
| `positions` | Standard Table | Currently open / active positions | `order_id`, `symbol`, `side`, `quantity`, `entry_price`, `sl_price`, `broker_sl_order_id`, `status` |
| `orders` | Standard Table | Broker order audits & status records | `order_id`, `exchange_order_id`, `symbol`, `status`, `variety`, `order_type`, `price`, `trigger_price` |
| `system_configs` | Standard Table | Dynamic UI & engine runtime parameter overrides | `category`, `config_key`, `config_value`, `data_type`, `updated_at` |
| `market_breadth_logs`| Standard Table | Real-time market breadth advance/decline ratios | `timestamp`, `advances`, `declines`, `ad_ratio`, `breadth_pct` |
| `metadata_cache` | Standard Table | Security Master instrument cache & F&O stocks | `cache_key`, `data`, `updated_at` |

---

## 3. Operational Runbooks & Pre-Approved Query Templates

### Runbook 1: Health Check & Table Row Counts
Quickly inspect database status, candle volume, and trade records:
```powershell
ssh -n -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -c '
SELECT 
    schemaname, 
    relname AS table_name, 
    n_live_tup AS estimated_rows 
FROM pg_stat_user_tables 
ORDER BY n_live_tup DESC;
'"
```
Or use the automated Go report script:
```powershell
go run scripts/db_report/main.go
```

---

### Runbook 2: Inspect Today's Executed Trades & P&L
Query trades executed today in IST timezone with duration, P&L, and exit reason:
```powershell
ssh -n -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -c '
SELECT 
    symbol,
    side,
    quantity,
    entry_price,
    exit_price,
    ROUND(pnl::numeric, 2) AS pnl,
    time_held_minutes AS held_mins,
    exit_reason,
    TO_CHAR(created_at AT TIME ZONE ''Asia/Kolkata'', ''YYYY-MM-DD HH24:MI:SS'') AS executed_at_ist
FROM trades 
WHERE created_at >= (CURRENT_DATE AT TIME ZONE ''Asia/Kolkata'')
ORDER BY created_at DESC;
'"
```

---

### Runbook 3: Inspect Open Positions & Broker Stop-Loss IDs
Verify live positions tracked in PostgreSQL:
```powershell
ssh -n -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -c '
SELECT 
    order_id,
    symbol,
    side,
    quantity,
    entry_price,
    sl_price,
    broker_sl_order_id,
    status,
    TO_CHAR(created_at AT TIME ZONE ''Asia/Kolkata'', ''YYYY-MM-DD HH24:MI:SS'') AS opened_at_ist
FROM positions
WHERE status = ''OPEN''
ORDER BY created_at DESC;
'"
```

---

### Runbook 4: Inspect & Override Runtime System Configurations
List all current strategy parameters stored in `system_configs`:
```powershell
ssh -n -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -c '
SELECT category, config_key, config_value, data_type, updated_at 
FROM system_configs 
ORDER BY category, config_key;
'"
```

Update or insert a strategy config directly:
```powershell
ssh -n -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -c \"
INSERT INTO system_configs (category, config_key, config_value, data_type, updated_at)
VALUES ('EQUITY_STRATEGY', 'es5_confirm_master_multiplier', '1.5', 'FLOAT', NOW())
ON CONFLICT (category, config_key) 
DO UPDATE SET config_value = EXCLUDED.config_value, updated_at = NOW();
\""
```

---

### Runbook 5: Inspect Candle Aggregations & Timestamps
Verify recent candles for a specific token (e.g. NIFTY 50 token `256265`):
```powershell
ssh -n -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -c '
SELECT 
    token,
    TO_CHAR(time AT TIME ZONE ''Asia/Kolkata'', ''YYYY-MM-DD HH24:MI:SS'') AS candle_time_ist,
    open, high, low, close, volume, color
FROM candles_5m
WHERE token = 256265
ORDER BY time DESC
LIMIT 10;
'"
```

---

### Runbook 6: TimescaleDB Maintenance, Diagnostics & Cleanup
Check hypertable chunk storage and compression:
```powershell
ssh -n -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -c '
SELECT hypertable_name, total_chunks, compressed_chunks, pg_size_pretty(table_size) 
FROM timescaledb_information.hypertables;
'"
```

Check active connections and long-running locks:
```powershell
ssh -n -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -c '
SELECT pid, now() - pg_stat_activity.query_start AS duration, query, state
FROM pg_stat_activity
WHERE state != ''idle'' AND query NOT ILIKE ''%pg_stat_activity%''
ORDER BY duration DESC;
'"
```

Vacuum & Analyze tables:
```powershell
ssh -n -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -c 'VACUUM ANALYZE trades; VACUUM ANALYZE positions; VACUUM ANALYZE system_configs;'"
```

---

## 4. Mandatory Query Optimization & Anti-N+1 Performance Standards

Every database query and repository method written or modified MUST be optimized for minimum latency and zero resource waste. The agent must enforce the following strict performance rules:

### A. Anti-Sequential Loop Mandate (Bulk Operations Only)
- **Zero DML in Loops**: NEVER execute single `INSERT`, `UPDATE`, `DELETE`, or `SELECT` queries inside a loop over slices/collections (e.g. 10 to 500 items). Sequential execution multiplies network latency and connection handshakes, causing severe latency degradation (e.g. 231 queries taking >2.3 seconds).
- **PostgreSQL Array Unnesting (`UNNEST`)**:
  When batch upserting or inserting collections, pack columns into typed Go slices (`[]string`, `[]time.Time`, `[]float64`) and use a single bulk query with `UNNEST`:
  ```go
  query := `
      INSERT INTO system_configs (category, key, value, updated_at)
      SELECT * FROM UNNEST($1::text[], $2::text[], $3::text[], $4::timestamptz[])
      ON CONFLICT (category, key) DO UPDATE
      SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at;
  `
  _, err := d.conn.ExecContext(ctx, query, pq.Array(cats), pq.Array(keys), pq.Array(vals), pq.Array(times))
  ```
  This reduces 200+ network roundtrips down to a single 7ms database execution.
- **Single-Transaction Batching (`BeginTx`)**:
  When operations cannot be represented in a single `UNNEST` statement, group them inside a single database transaction (`tx, err := d.conn.BeginTx(ctx, nil)`) to eliminate per-statement transaction overhead and commit once.

### B. Anti-N+1 Query Prevention & Thread-Safe In-Memory Caching
- **Hot-Path Memory Protection**:
  High-frequency loops, symbol reconciliations (`ReconcileStrategyWatchlists`), tick processing, and UI request handlers must NEVER query PostgreSQL repeatedly for daily or static reference data (e.g. `daily_manual_watchlist`, `metadata_cache:fo:stocks`, previous day levels, system configs).
- **Thread-Safe Caching Pattern**:
  Store reference data in memory (`sync.RWMutex`, slice or `map[string]string`) anchored to the trading day date string (`2006-01-02`).
  - Read with read-lock (`RLock`).
  - If cache is empty or date changed, acquire write-lock (`Lock`), populate from DB once, update date anchor, and unlock.
  - Return in-memory map/slice immediately ($O(1)$ lookup, 0ms latency).
- **Mandatory Invalidation Hooks**:
  Whenever an API endpoint or internal workflow creates, updates, or deletes records (e.g. `handleSetManualWatchlist`, `handleDeleteManualStock`, `handleConfigSave`), it MUST immediately invoke the corresponding cache invalidator (e.g. `tb.InvalidateManualWatchlistCache()`) so subsequent reads receive fresh data without requiring server restarts.

### C. TimescaleDB Hypertable Time-Bounding & Chunk Pruning
- **Mandatory Time Filters**:
  Queries against TimescaleDB hypertables (`candles_1m`, `candles_5m`) MUST always include explicit time range filters (`time >= $1 AND time <= $2`) in addition to `token = $3`.
- **Chunk Exclusion**:
  Without time bounds, PostgreSQL is forced to scan every historical chunk partition in the hypertable, degrading query times from 5ms to several seconds.
- **Selective Column Projections**:
  Avoid `SELECT *` in hypertable queries. Always specify the exact required technical columns (`SELECT time, open, high, low, close, volume, color`).

### D. Index Alignment & Pagination Guardrails
- **Covering Index Alignment**:
  Ensure all `WHERE` and `ORDER BY` clauses align with primary keys or composite indexes (e.g. `(token, time DESC)`, `(order_id)`, `(category, key)`, `(date)`).
- **Strict Analytical Query Limits**:
  Always use `LIMIT` or time bounds on analytical, trade log, and event queries to protect application memory from unbounded row allocations.
- **Immediate Resource Cleanup**:
  Always close `sql.Rows` handles immediately via `defer rows.Close()` to prevent connection pool exhaustion.

### E. Latency Budget & Empirical Telemetry Logging
- **Sub-100ms Latency Budget**:
  All new or modified database repository queries and calling HTTP endpoints must execute well under 100ms (bulk operations under 20ms).
- **Telemetry Breakdown Logging**:
  For critical persistence routines (like `handleConfigSave`), log millisecond timing breakdowns (`options_batch_save_ms`, `system_batch_save_ms`, `in_memory_reload_ms`, `total_duration_ms`) using Zap structured logging to immediately detect any performance regressions.

---

## 5. Coding & Architecture Guardrails

Even with full execution authority for queries and operational commands, the agent must adhere to clean architectural principles when modifying application code:

1. **Decoupled Queries Pattern (Rule 3)**:
   All raw SQL queries embedded within Go application code MUST reside inside package `data` (specifically [`data/queries.go`](file:///c:/Users/admin/zt/data/queries.go)). Domain strategies, risk managers, and handlers must invoke `data.Database` helper methods rather than executing raw SQL strings directly.
2. **Unified Database Migrations (Rule 3)**:
   All table creation, column alterations, and index migrations must be declared in [`data/database.go`](file:///c:/Users/admin/zt/data/database.go) to run automatically on bot boot.
3. **IST Timezone Normalization (Rule 14 & 15)**:
   All candle read/write operations must normalize timestamps using `data.NormalizeToIST(t)` or format with explicit `+05:30` offsets to prevent UTC shifts.
