# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A production-grade Go algorithmic trading bot for Zerodha Kite Connect API. Implements real-time market data processing, technical analysis, order execution, and risk management.

## Build & Run Commands

```bash
# Build
go build -o trading-bot

# Run
./trading-bot

# Development with hot reload
go run .

# Run tests
go test ./...

# Run with coverage
go test -cover ./...

# Format code
go fmt ./...

# Vet
go vet ./...
```

## Architecture

**Entry Point**: `main.go` - TradingBot orchestrator with 5 concurrent loops

### Layer Structure

| Layer | Package | Responsibility |
|-------|---------|----------------|
| Data | `data/` | WebSocket ticker, OHLCV candle aggregation, PostgreSQL persistence |
| Selection | `selection/` | Watchlist builders (SecuritiesFOSelector, SectoralSelector) |
| Strategy | `strategy/` | Technical indicators, Strategy Engines (LowVolumeEngine, VandeBharatEngine) |
| Execution | `execution/` | Order placement, status tracking, resilient API wrapper |
| Risk | `risk/` | Capital preservation, position tracking, circuit breakers, risk reward calculators |
| Monitoring | `monitoring/` | Structured JSON logging, Prometheus metrics |
| Config | `config/` | Environment-based configuration loading |

### Data Flow

```
Kite WebSocket → Tick → CandleAggregator → StrategyEngine / Breakout Engine → Signal → RiskManager → ExecutionManager
                                                                                        ↓
                                                                                 StatusTracker (monitoring)
```

### Key Components

- **TradingBot** (`main.go`): Orchestrates 4 goroutines - tick processing, strategy loop, order management, monitoring
- **CandleAggregator**: Converts raw ticks to 5-minute OHLCV candles, persists to TimescaleDB
- **Strategy Engines**: Executes Low Volume and Refined Vande Bharat breakout signals, maintains rolling candle buffer per token
- **RiskManager**: Tracks positions, checks for duplicate open positions, enforces daily loss limits, trails SL, circuit breaker
- **ExecutionManager**: Places/cancels orders via Zerodha API, tracks order status

## Configuration

Configuration loaded from `.env` file via `config.Load()`:

| Section | Key Variables |
|---------|---------------|
| Zerodha API | `KITE_API_KEY`, `KITE_API_SECRET`, `KITE_USER_ID`, `KITE_ACCESS_TOKEN` |
| Database | `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME` |
| Trading | `INITIAL_CAPITAL`, `MAX_TRADES_PER_DAY`, `MAX_LOSS_STREAKS`, `MAX_HOLDING_TIME_MIN`, `MAX_CAPITAL_PER_TRADE`, `MAX_DAILY_LOSS_AMOUNT` |
| Strategy | `ACTIVE_STRATEGIES`, `ACTIVE_SELECTORS`, `STRATEGY_SELECTOR_MAP`, `LV_TRADE_END_TIME`, `SECTOR_MAX_BUY_PCT`, `SECTOR_MAX_SELL_PCT`, `STOCK_MAX_BUY_PCT`, `STOCK_MAX_SELL_PCT`, `VB_MASTER_MAX_PCT`, `VB_CONFIRM_MAX_PCT`, `VB_TRADE_END_TIME` |
| Monitoring | `LOG_LEVEL` |

## Dependencies

```
github.com/gorilla/websocket    - WebSocket client
github.com/lib/pq               - PostgreSQL driver
github.com/joho/godotenv        - Environment loading
github.com/prometheus/client_golang - Metrics
go.uber.org/zap                 - Structured logging
```

## Infrastructure

Docker Compose provides PostgreSQL (TimescaleDB) and the Go application:

```bash
docker-compose up -d
```

## Key Patterns

- **Goroutine-per-loop**: Each component runs independently with channel-based communication
- **Context cancellation**: Graceful shutdown via context.WithCancel
- **Retry with backoff**: `ResilientExecutor` for API calls
- **Circuit breaker**: Auto-shutdown on excessive losses
- **JSON logging**: Structured logs via zap with custom fields (InfoTrade, ErrorTrade, etc.)