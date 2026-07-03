# Quick Start Guide

## 1. Prerequisites Installation

### macOS
```bash
brew install postgresql go
brew services start postgresql
```

### Ubuntu/Debian
```bash
sudo apt-get install postgresql postgresql-contrib golang
sudo systemctl start postgresql
```

### Windows
- Download PostgreSQL from https://www.postgresql.org/download/windows/
- Download Go from https://go.dev/dl/

## 2. Setup Project

```bash
# Clone/navigate to project
cd zerodha-trading

# Install Go dependencies
go mod download

# Copy environment template
cp .env.example .env
```

## 3. Configure Environment

Edit `.env`:

```env
# Essential - Get from Zerodha Kite Connect Dashboard
KITE_API_KEY=your_api_key
KITE_API_SECRET=your_api_secret  
KITE_USER_ID=your_user_id
KITE_ACCESS_TOKEN=your_access_token

# Database - Change password!
DB_PASSWORD=your_secure_password

# Trading - Adjust risk parameters
INITIAL_CAPITAL=500000
MAX_DAILY_LOSS_AMOUNT=10000.0
```

## 4. Start Infrastructure

### Option A: Docker (Recommended)
```bash
docker-compose up -d
# Waits for services to be healthy
docker-compose logs -f
```

### Option B: Manual
```bash
# Terminal 1: PostgreSQL
psql postgres

# In psql:
CREATE DATABASE zerodha_trading;
CREATE EXTENSION timescaledb;



# Terminal 3: Prometheus (optional)
prometheus --config.file=prometheus.yml
```

## 5. Run Bot

```bash
# Build
go build -o trading-bot

# Run
./trading-bot

# Or using make
make run
```

## 6. Verify Setup

### Check Logs
```bash
tail -f logs/trading.log | jq .
```

### Check Prometheus Metrics
```bash
curl http://localhost:8888/metrics | grep trading_
```

### Check Database
```bash
psql zerodha_trading
SELECT COUNT(*) FROM candles_5m;
SELECT COUNT(*) FROM trades;
```



## Common Issues

### Database Connection Error
```
Error: database connection failed
```
**Fix**: Ensure PostgreSQL is running and credentials are correct in `.env`

### Redis Connection Warning (OK)
```
Redis connection failed (continuing anyway)
```
**Fix**: Redis is optional for caching. Install/start Redis if needed.

### Module Import Error
```
cannot find module: zerodha-trading/config
```
**Fix**: Ensure you're running from project root directory

### Port Already in Use
```
bind: address already in use
```
**Fix**: Change port in `.env` or kill existing process:
```bash
lsof -i :8888  # Find process
kill -9 <PID>
```

## Architecture Check

The project should have this structure:

```
zerodha-trading/
├── main.go                    ← Entry point
├── handlers.go                ← HTTP dashboard handlers
├── scheduler.go               ← Daily pre-market/market hours scheduler
├── engine.go                  ← Ticker processing router
├── config/settings.go         ← Configuration settings loader
├── data/                      ← Data ingestion and storage layer
│   ├── security_master.go     ← Symbol-token mapping resolver
│   ├── ticker.go              ← WebSocket tick client (live + mock)
│   ├── candle_aggregator.go   ← Tick aggregation into timeframes
│   └── database.go            ← TimescaleDB connection and queries
├── strategy/                  ← Strategy signal layer
│   ├── indicators.go          ← VWAP, ATR, RSI helpers
│   ├── low_volume_engine.go   ← LOW_VOLUME strategy engine
│   └── vande_bharat_engine.go ← VANDE_BHARAT strategy engine
├── execution/                 ← Order placement layer
│   ├── execution_manager.go   ← Order execution manager
│   ├── status_tracker.go      ← Order status poller
│   └── resilient_executor.go  ← Exponential backoff retry handler
├── risk/                      ← Risk parameters layer
│   ├── risk_manager.go        ← Pre-trade checks and circuit breakers
│   └── calculator.go          ← Pluggable risk reward profile calculators
├── monitoring/                ← Observability layer
│   ├── logger.go              ← Uber Zap structured logs writer
│   └── metrics.go             ← Prometheus metrics exporter
├── .env.example               ← Template config environment file
├── go.mod                     ← Go modules manifest
├── docker-compose.yml         ← Container deployment config
└── README.md                  ← Comprehensive documentation guide
```

## Testing

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run specific package
go test -v ./strategy

# Run with coverage
go test -cover ./...
```

## Performance Tuning

### For Development
```env
LOG_LEVEL=debug
MAX_TRADES_PER_DAY=5
INITIAL_CAPITAL=50000
```

### For Production
```env
LOG_LEVEL=info
MAX_TRADES_PER_DAY=20
INITIAL_CAPITAL=500000
DB_POOL_SIZE=25
```

## Next Steps

1. ✅ Get Kite Connect API credentials
2. ✅ Run `docker-compose up -d`
3. ✅ Update `.env` with your API key
4. ✅ Run `go build && ./trading-bot`
5. 📊 Monitor via `http://localhost:9090` (Prometheus)
6. 🔍 Check logs: `tail -f logs/trading.log`
7. 💰 Start with paper trading to validate
8. 📈 Gradually increase capital after profitability

## Support

- Full docs: See [README.md](README.md)
- Issues: Check logs in JSON format
- Metrics: http://localhost:8888/metrics
- Database: Use PostgreSQL client

Good luck! 🚀
