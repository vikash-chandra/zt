# Zerodha Trading Bot Workspace Memory & Rules

This file provides rules, architectural overview, and coding guidelines for the Zerodha Trading Bot codebase, customized for the Antigravity agent.

## Project Overview
A production-grade Go algorithmic trading bot interfacing with the Zerodha Kite Connect API. It processes real-time market data ticks, aggregates them into 5-minute candles, generates signals using technical indicators (VWAP, ATR, RSI), and executes trades with rigorous pre-trade and post-trade risk management.

### Directory Structure & Layers
- [main.go](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/main.go): Main entry point and lifecycle orchestrator running 4 concurrent loops.
- [config/settings.go](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/config/settings.go): Configuration manager loading settings from `.env`.
- [data/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/data): Handles WebSocket/mock ticker, instrument master (SecurityMaster), candle aggregation, and TimescaleDB storage.
- [strategy/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/strategy): Computes technical indicators and generates buy/sell/hold signals.
- [execution/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/execution): Handles order execution, status polling/tracking, and resilient API call retries.
- [risk/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/risk): Enforces risk management, tracks open positions, and implements the circuit breaker.
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
- **TimescaleDB Compatibility**: The `candles_5m` table is structured for time-series data. Query with time bounds when fetching history to ensure quick execution.
- **Resource Cleanup**: Always close `sql.Rows` handles immediately after scanning.
- **On Conflict Handling**: When upserting candles, handle conflicts on `(token, time)` using `ON CONFLICT DO UPDATE`.

### 4. Logging Standards
- **Structured Fields**: Use Uber's `zap` structured logging. Avoid unstructured logging. Provide context keys (e.g., `zap.String("symbol", s)`, `zap.Error(err)`).

---

## Developer Commands

- **Build**: `go build -o trading-bot`
- **Run**: `./trading-bot`
- **Dev / Hot reload**: `go run .`
- **Run Tests**: `go test ./...`
- **Format Code**: `go fmt ./...`
- **Lint Code**: `golangci-lint run ./...`
- **Infrastructure**: `docker-compose up -d`
