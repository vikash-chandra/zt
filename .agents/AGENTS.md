# Zerodha Trading Bot Workspace Memory & Rules

This file provides rules, architectural overview, and coding guidelines for the Zerodha Trading Bot codebase, customized for the Antigravity agent.

## Project Overview
A production-grade Go algorithmic trading bot interfacing with the Zerodha Kite Connect API. It processes real-time market data ticks, aggregates them into 1-minute and 5-minute candles, generates signals using technical indicators (VWAP, ATR, RSI), and executes trades with rigorous pre-trade and post-trade risk management.

### Directory Structure & Layers
- [main.go](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/main.go): Main entry point and lifecycle orchestrator running 4 concurrent loops.
- [config/settings.go](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/config/settings.go): Configuration manager loading settings from `.env`.
- [data/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/data): Handles WebSocket/mock ticker, instrument master (SecurityMaster), 1-minute and 5-minute candle aggregation, and TimescaleDB storage.
- [strategy/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/strategy): Computes technical indicators and generates buy/sell/hold signals.
- [selection/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/selection): Handles modular stock selection algorithms and selectors.
- [execution/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/execution): Handles order execution, status polling/tracking, and resilient API call retries.
- [risk/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/risk): Enforces risk management, tracks open positions, runs pluggable risk-reward calculators, and implements the circuit breaker.
- [monitoring/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/monitoring): Structured JSON logging (via Zap) and Prometheus metric exporting.

---

## Coding Guidelines

### 1. Concurrency & Safety
- **State Protection**: Always use `sync.RWMutex` or `sync.Mutex` when accessing shared fields in strategy engines, ticker states, risk managers, or order/position maps. Do not allow race conditions.
- **Context Cancellation**: Ensure all goroutines monitor `ctx.Done()` to exit gracefully upon shutdown.

### 2. Error Handling & Wrapping
- **Wrap Errors**: Wrap errors using the `%w` verb when propagating them up (e.g., `fmt.Errorf("failed to perform action: %w", err)`).
- **Log Errors**: Use the zap logger to log error contexts rather than printing directly to stdout/stderr.

### 3. Database Operations
- **TimescaleDB Compatibility**: The `candles_1m` and `candles_5m` tables are structured for time-series data. Query with time bounds when fetching history to ensure quick execution. Both tables contain a `color` VARCHAR column (`GREEN`, `RED`, or `DOJI`).
- **Resource Cleanup**: Always close `sql.Rows` handles immediately after scanning.
- **On Conflict Handling**: When upserting candles, handle conflicts on `(token, time)` using `ON CONFLICT DO UPDATE`.
- **Decoupled Queries Pattern (Repository)**: All raw database SQL queries MUST be isolated within the `data` package (specifically encapsulated in methods on `data.Database` in [queries.go](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/data/queries.go)). Domain logic, handlers, schedulers, and executors MUST NOT execute raw query strings directly or manage database connection contexts; instead, they must invoke helper methods on the `*data.Database` (or `*Database` in package `data`) instances.
- **Unified Database Migrations**: All database tables, columns, indexes, and schema modifications MUST be declared and initialized inside the main application schema setup in [data/database.go](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/data/database.go) to ensure they are created automatically on bot startup. Do NOT rely on standalone scripts or tools (e.g. `pre-selection/main.go`) to initialize their own tables, as this causes failures on remote or fresh instances (such as AWS) when run by the automated scheduler.

### 4. Logging Standards
- **Structured Fields**: Use Uber's `zap` structured logging. Avoid unstructured logging. Provide context keys (e.g., `zap.String("symbol", s)`, `zap.Error(err)`).

### 5. Environment Configuration Rules
- **Keep Env Files in Sync**: Whenever you add, modify, or delete environment variables in `.env`, you must immediately make matching changes to:
  1. `.env.example` (to keep the template in sync).
  2. `config/settings.go` (to expose the config property in Go).
  3. `docker-compose.yml` (under the `environment` section of the `app` service, to ensure the variable is forwarded into the running Docker container).
- **Keep Documentation and Frontend Dynamic**: When any parameter or environment configuration changes inside `.env`, ensure matching default value updates are propagated to `README.md` (Risk Framework table), and verify that any corresponding frontend files (such as `index.html`) display these configurations dynamically rather than using hardcoded labels.

---

## Developer Commands

- **Build**: `go build -o trading-bot`
- **Run**: `./trading-bot`
- **Dev / Hot reload**: `go run .`
- **Run Tests**: `go test ./...`
- **Format Code**: `go fmt ./...`
- **Lint Code**: `golangci-lint run ./...`
- **Infrastructure**: `docker-compose up -d`
- **Seeding Historical Data**: `go run scripts/seed/main.go`

### 6. Backtesting & Report Rules
- **Timezone Normalization**: Historical database timestamps may differ between seeded UTC-named times (Hour >= 9) and live UTC times (Hour < 9). Always normalize them accordingly (e.g. converting UTC times to local time using `t.In(loc)` if Hour < 9, or constructing a local time directly if Hour >= 9) to prevent 5.5-hour timezone offsets in backtests and frontend display API endpoints (like `/api/candles`).
- **Volume Normalization**: Live candle data can contain cumulative tick volumes instead of interval volumes. Always check if database volumes are monotonically increasing and normalize them (`current - prev`) before running strategy simulations.
- **Dynamic Report Pathing**: Always write generated reports (e.g., `backtest_report.md`) to the dynamically provided current active conversation's artifact folder instead of any hardcoded conversation ID folders.

### 7. Position & Order Re-attachment (AWS & Startup Recovery)
- **No Emergency Startup Square-offs**: Open MIS positions are never squared off on startup before 3:15 PM. The bot must recover them.
- **State Reconstruction**: Active positions on Zerodha are matched against completed entry orders today to reconstruct local in-memory states (`EntryPrice`, `Side`, `Quantity`, `Strategy`).
- **Stop-Loss Recovery**: Active stop-loss orders (`SL`/`SL-M`) are reconciled on startup: if already active on Zerodha, they are recovered and tracked; if missing, they are recalculated (1.5% default fallback) and placed.
- **Database Position Persistence**: All open positions and their active broker SL order IDs must be synced with the `positions` database table (created with unique indexes on `order_id` in `database.go`) on entry, update, and close.
- **Trigger State Recovery**: On startup, today's completed trades must be scanned from the `trades` database table and loaded into the strategy engines' `triggeredTrades` memory maps to prevent duplicate entries for previously traded symbols after a reboot.
- **API Cache Protection**: Historical candles fetched during morning catch-up API fallbacks are saved in the database `candles_5m` table to protect Kite Connect API limits on subsequent restarts.

### 8. Live Streaming & Subscription Robustness
- **Dynamic Subscription Re-connection Recovery**: The `RobustKiteTicker` must maintain a synchronized internal cache of all active subscriptions (`subscribedTokens`). Upon WebSocket auto-reconnection, the `OnConnect` callback must re-subscribe to the *entire cached list* (initial + dynamic additions) to prevent losing dynamic watchlist or manual additions.
- **Tick-by-Tick Volume Aggregation**: Raw Zerodha WebSocket ticks report cumulative daily volume (`VolumeTraded`). The `CandleAggregator` must track the last seen cumulative volume for each token and compute the tick-by-tick interval volume (`current - prev`). Increment candle volume and VWAP sums using this interval volume to prevent severe volume inflation and VWAP distortion.
- **Catch-up Candle Validation**: When catch-up queries run, calculate the expected number of 5m candles since 09:15 AM IST (capped at 15:30 PM IST). Only bypass the Zerodha `/historical` API fallback if the local DB contains **at least** the expected candle count, ensuring the strategies do not run with incomplete morning data due to connection drops.

### 9. Broker API Decoupling (Pure Domain Model Isolation)
- **Zero Direct SDK Dependencies**: No core logic package (e.g. `execution`, `selection`, `strategy`, `risk`), server file (`handlers.go`), database script (`queries.go`, `database.go`), or entry point file (`main.go`, `scheduler.go`) should directly import `"github.com/zerodha/gokiteconnect/v4"`.
- **Use BrokerClient & Generic Models**: All files must use the `data.BrokerClient` interface and its vendor-agnostic models defined in [data/broker_models.go](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/data/broker_models.go).
- **Isolate Adaptations**: All vendor-specific calls, parameter structures, and mappings to/from Zerodha SDK models MUST reside strictly inside `data/broker.go` within `ZerodhaBrokerAdapter`.

### 10. Remote AWS Deployment Rules
- **No scp for Source Code**: Always push local changes to GitHub first, then run `git pull` on the remote AWS server to update the code. Do not copy source files directly using `scp`.
