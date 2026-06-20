# Quick Start Guide

## 1. Prerequisites Installation

### macOS
```bash
brew install postgresql redis go
brew services start postgresql
brew services start redis
```

### Ubuntu/Debian
```bash
sudo apt-get install postgresql postgresql-contrib redis-server golang
sudo systemctl start postgresql
sudo systemctl start redis-server
```

### Windows
- Download PostgreSQL from https://www.postgresql.org/download/windows/
- Download Redis from https://github.com/microsoftarchive/redis/releases
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
MAX_DAILY_LOSS_PCT=2.0
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

# Terminal 2: Redis
redis-server

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

### Check Redis
```bash
redis-cli
> PING
> KEYS *
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
├── config/settings.go         ← Configuration
├── data/                      ← Data layer
│   ├── security_master.go
│   ├── ticker.go
│   ├── candle_aggregator.go
│   └── database.go
├── strategy/                  ← Strategy layer
│   ├── indicators.go
│   └── engine.go
├── execution/                 ← Execution layer
│   ├── order_manager.go
│   ├── status_tracker.go
│   └── resilient_executor.go
├── risk/                      ← Risk management
│   └── risk_manager.go
├── monitoring/                ← Monitoring
│   ├── logger.go
│   └── metrics.go
├── .env.example               ← Config template
├── go.mod                     ← Dependencies
├── docker-compose.yml         ← Docker setup
└── README.md                  ← Full documentation
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
