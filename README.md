# Zerodha Automated Intraday Trading Bot

Production-grade Go implementation of an algorithmic intraday trading system for Zerodha Kite Connect API.

## Architecture

### Components

- **Data Layer** (`data/`): WebSocket ticker, OHLCV candle aggregation, security master, time-series database
- **Strategy Layer** (`strategy/`): Technical indicators (VWAP, ATR, RSI), signal generation engine
- **Execution Layer** (`execution/`): Order placement, status tracking, resilient API wrapper
- **Risk Management** (`risk/`): Capital preservation, position tracking, circuit breakers
- **Monitoring** (`monitoring/`): Structured logging, Prometheus metrics

### Key Features

✅ **Resilient**: Automatic retry with exponential backoff, circuit breaker for cascading failures  
✅ **Real-time**: WebSocket ticker, 5-minute candle aggregation, sub-second latency  
✅ **Safe**: Dynamic SL with ATR, capital preservation, daily loss limits, margin monitoring  
✅ **Observable**: Prometheus metrics, structured logging (JSON), order tracking  
✅ **Web Dashboard**: Interactive real-time candlestick charts with execution trade markers and dynamic tooltips
✅ **Modular**: Clean separation of concerns, easy to extend strategies  

## Prerequisites

- Go 1.24+
- PostgreSQL 13+ (for TimescaleDB and caching)
- Zerodha Kite Connect API credentials

## Setup

### 1. Environment Configuration

```bash
cp .env.example .env
```

Edit `.env`:

```env
# Zerodha Kite API
KITE_API_KEY=your_api_key
KITE_API_SECRET=your_api_secret
KITE_USER_ID=your_user_id
KITE_ACCESS_TOKEN=your_access_token

# Database
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=your_password
DB_NAME=zerodha_trading
DB_SSL_MODE=disable


# Trading
INITIAL_CAPITAL=500000
MAX_TRADES_PER_DAY=20
MAX_LOSS_STREAKS=3
MAX_HOLDING_TIME_MIN=30
MAX_CAPITAL_PER_TRADE=20000.0
MAX_DAILY_LOSS_AMOUNT=10000.0

# Monitoring
LOG_LEVEL=info
PROMETHEUS_ADDR=:8888
```

### 2. Database Setup

```bash
# Create database and enable TimescaleDB
createdb zerodha_trading
psql zerodha_trading -c "CREATE EXTENSION IF NOT EXISTS timescaledb;"
```

### 3. Dependencies

```bash
go mod download
go mod tidy
```

### 4. Build & Run

```bash
go build -o trading-bot
./trading-bot
```

### 5. Seeding Historical Data

To seed 1 week of historical 1-minute and 5-minute candles for all Nifty 50 instruments into the database, run:

```bash
go run scripts/seed/main.go
```

* **Live Mode**: If a valid `KITE_ACCESS_TOKEN` is configured in `.env`, the script will query Zerodha's `/historical` API to load real historical candles.
* **Mock Mode**: If no access token is set, it automatically falls back to generating a high-fidelity procedural simulation.

## Interactive Web Dashboard

The application features an embedded, high-performance web dashboard to track trades, monitor watchlist tickers, and visualize live intraday charts.

### 🌐 Accessing the Dashboard

Once the application container starts, open your browser and navigate to:
👉 **`http://localhost:8080/zt`**

*(Note: Navigating to the root `http://localhost:8080/` automatically redirects you to `/zt` via a `301 Moved Permanently` header).*

### 📊 Key Dashboard UI Features

1. **Intraday Candlestick & Volume Chart**:
   - Renders 5-minute OHLCV candles using TradingView's high-performance **Lightweight Charts** library.
   - Restricts transaction volume to the bottom 20% overlay pane.
   - Automatically centers and fits visible candles on symbol load via `fitContent()`.

2. **Trade Markers**:
   - Buy entries are marked with a blue **Up Arrow** below the entry candle.
   - Sell entries are marked with a pink **Down Arrow** above the entry candle.
   - Exact entry and exit prices are displayed on the markers.

3. **Dynamic Watchlist Dropdown**:
   - The dropdown list automatically syncs with the trading engine. It displays only active, subscribed watchlist symbols.

4. **Return Metric Hover Tooltips**:
   - Hovering over the percentages in the **Daily Net P&L** card triggers tooltips detailing:
     - **Margin Return**: Return on leveraged capital locked.
       $$\text{Margin Return \%} = \frac{\text{Net P\&L}}{\text{Total Trade Value} / 5} \times 100$$
     - **Account Growth**: Return on entire portfolio size.
       $$\text{Account Growth \%} = \frac{\text{Net P\&L}}{\text{INITIAL\_CAPITAL}} \times 100$$

## Modular Strategy Architecture

The bot features a modular multi-strategy execution framework. Multiple strategies can run concurrently on incoming live tick feeds and candle closes, configurable dynamically via environment variables. Executed orders and completed trades are saved in the database with their originating strategy name (e.g. `LOW_VOLUME`, `VANDE_BHARAT`) for tracking and analysis.

### Active Strategies Configuration
Set the enabled strategies in your `.env` file using the `ACTIVE_STRATEGIES` key (comma-separated):
```env
ACTIVE_STRATEGIES=LOW_VOLUME,VANDE_BHARAT
```

---

## Strategy 1: Low-Volume Breakout (`LOW_VOLUME`)

The bot executes a high-fidelity **Low-Volume Breakout Strategy** designed to identify intraday consolidation ranges and capitalize on explosive momentum expansions.

### 1. Daily Bias & Watchlist Selection
* **Pre-Market Bias (09:29 AM)**: Automatically scans the Nifty 50 constituents. 
  * If $Advances > Declines$, Bias = **`BUY_ONLY`** (Long positions only).
  * If $Advances \le Declines$, Bias = **`SELL_ONLY`** (Short positions only).
* **Watchlist Selection (09:30 AM)**: Dynamically selects the **Top 10** gainers (for `BUY_ONLY`) or losers (for `SELL_ONLY`) since the market open.
  * **Chasing Limit**: Tickers are excluded if their absolute percentage change since open is **$> 2.5\%$** to avoid chasing overextended moves.

### 2. Trade Setup & Trigger Constraints
* **Setup Candle**: Defined as the completed 5-minute candle with the **absolute lowest trading volume** since 09:15 AM.
* **Breakout Entry**: Triggered when the price crosses the setup candle's High (for Long) or Low (for Short).
* **Next-Candle Constraint**: A breakout is **only** valid if it triggers during the single 5-minute candle immediately following the setup candle. If no breakout occurs during this next candle, the setup is invalidated.
* **Operational Window**: Trading activity starts strictly after **09:30 AM IST**. Any breakouts prior to this time are ignored.

---

## Strategy 2: Refined Vande Bharat Setup (`VANDE_BHARAT`)

The **Refined Vande Bharat** strategy implements a high-performance sector-driven breakout model checking previous day high/low references, master/confirmation candles, and candle color and range restrictions.

### 1. Daily Bias & Watchlist Selection
* **Pre-Market Bias (09:29 AM)**: Scans the Nifty 50 constituents.
  * If $Advances > Declines$, Bias = **`BUY_ONLY`** (Long positions only).
  * If $Advances \le Declines$, Bias = **`SELL_ONLY`** (Short positions only).
* **Sector Filter**: Calculates average performance across F&O sectors.
  * `BUY_ONLY` bias: Filters sectors with change $\le 2.5\%$ (configurable via `SECTOR_MAX_BUY_PCT`).
  * `SELL_ONLY` bias: Filters sectors with change $\le -3.0\%$ (configurable via `SECTOR_MAX_SELL_PCT`, ignoring any sectors with change $> -3.0\%$).
* **Sector Selection**: Selects the top 2 sectors with the largest absolute change matching the bias.
* **Stock Selection**: Selects top 10 stocks in the top 2 sectors with change $\le 2.5\%$ (for Buy, configurable via `STOCK_MAX_BUY_PCT`) or $\ge -2.5\%$ (for Sell, configurable via `STOCK_MAX_SELL_PCT`).

### 2. Strategy Setup & Trigger Constraints
* **Candle Interval**: 5-minute candles.
* **Operational Window**: Trading activity runs strictly from **09:26 AM** to **11:00 AM** (configured via `VB_TRADE_START_TIME` and `VB_TRADE_END_TIME`).
* **Previous Day Reference**: Dynamically queries Previous Day High (PDH) and Low (PDL) from TimescaleDB cache.
* **Setup Requirements**:
  * **Master Candle**:
    * Buy: Close > PDH. Must be **GREEN** (Close > Open). Range (High - Low) $\le 3.0\%$ of Close (configurable via `VB_MASTER_MAX_PCT`).
    * Sell: Close < PDL. Must be **RED** (Close < Open). Range (High - Low) $\le 3.0\%$ of Close.
  * **Confirmation Candle**: The very next candle immediately following the Master Candle:
    * Buy: Close > Master High. Must be **GREEN**. Range $\le 1.0\%$ of Close (configurable via `VB_CONFIRM_MAX_PCT`).
    * Sell: Close < Master Low. Must be **RED**. Range $\le 1.0\%$ of Close.
  * **Trade Entry**: Triggered when the live price breaks above the Confirmation Candle's High (for Buy) or below the Confirmation Candle's Low (for Sell).
  * **Duplicate Position Prevention**: Only one active trade is allowed per symbol. If a breakout triggers on a symbol that already has an open position (from either strategy), the breakout is skipped.

---

## Stop-Loss & Target Management (Both Strategies)
* **Risk Buffer**: The initial trade risk is buffered to prevent stops from triggering on market noise:
  * **Low Volume Breakout**: Uses a 20% risk buffer:
    $$\text{Buffered Risk} = |\text{Entry} - \text{Setup Opposite Bound}| \times 1.20$$
  * **Vande Bharat Breakout**: Uses a 10% risk buffer:
    $$\text{Buffered Risk} = |\text{Entry} - \text{Setup Opposite Bound}| \times 1.10$$
* **Stop-Loss (SL)**: Set at $\text{Entry} - \text{Buffered Risk}$ (for Long) or $\text{Entry} + \text{Buffered Risk}$ (for Short).
* **Target 1 (1:2 R:R)**: Set at $\text{Entry} + (\text{Buffered Risk} \times \text{RISK\_REWARD\_RATIO})$ (for Long) or $\text{Entry} - (\text{Buffered Risk} \times \text{RISK\_REWARD\_RATIO})$ (for Short).
* **Exit Scaling**:
  1. Once **Target 1** is hit, **50% of the position** is closed immediately at market price.
  2. The Stop-Loss for the remaining 50% of the position is moved to the **Entry Price** (breakeven cost-to-cost).
  3. If the remaining position is not stopped out, it is held until the **03:15 PM IST** market-close hard square-off override.

---

## Risk Framework

| Parameter | Default Value | Description |
| :--- | :--- | :--- |
| `ACTIVE_STRATEGIES` | `LOW_VOLUME` | Comma-separated list of active strategies to execute |
| `ACTIVE_SELECTORS` | `SECURITIES_FO,SECTORAL` | Comma-separated list of active stock selection selectors |
| `STRATEGY_SELECTOR_MAP` | `LOW_VOLUME:SECURITIES_FO,VANDE_BHARAT:SECTORAL` | Maps strategy engine name to selection algorithm |
| `RISK_REWARD_TYPE` | `STANDARD` | Pluggable calculator mode (`STANDARD` or `PERCENTAGE`) |
| `RISK_REWARD_RATIO` | `2.0` | Target profit margin multiplier relative to buffered risk |
| `SECTOR_MAX_BUY_PCT` | `2.5%` | Maximum sector gain allowed for bullish sector watchlist |
| `SECTOR_MAX_SELL_PCT` | `-3.0%` | Maximum sector loss threshold for shorting sector watchlist |
| `STOCK_MAX_BUY_PCT` | `2.5%` | Maximum stock gain allowed for long watchlist inclusion |
| `STOCK_MAX_SELL_PCT` | `-2.5%` | Maximum stock loss threshold for shorting watchlist inclusion |
| `VB_MASTER_MAX_PCT` | `3.0%` | Maximum allowed size of Vande Bharat Master Candle |
| `VB_CONFIRM_MAX_PCT` | `1.0%` | Maximum allowed size of Vande Bharat Confirmation Candle |
| `VB_TRADE_START_TIME` | `09:26` | Execution window start time for Vande Bharat |
| `VB_TRADE_END_TIME` | `11:00` | Execution window end time for Vande Bharat |
| `MAX_CAPITAL_PER_TRADE` | ₹20,000 | Max cash allocation per trade setup |
| `INITIAL_CAPITAL` | ₹1,00,000 | Base portfolio size |
| `MAX_DAILY_LOSS_AMOUNT` | ₹10,000 | Max portfolio loss limit (Circuit breaker) |
| `MAX_LOSS_STREAKS` | 3 | Stop trading after N consecutive losses |
| `MAX_HOLDING_TIME_MIN` | 30 | Max holding time minutes for MIS positions |
| `MAX_TRADES_PER_DAY` | 20 | Maximum total executions per session |
| `WATCHLIST_SIZE` | 10 | Target watchlist portfolio size |
| `WATCHLIST_MAX_PCT_CHANGE` | 2.5% | Max percentage change to allow watchlist inclusion |

## API Endpoints

### Monitoring

```
Prometheus metrics: http://localhost:8888/metrics
Health check: GET /health
```

### Trades

```
GET /trades - List all trades
GET /trades/{id} - Trade details
GET /positions - Open positions
POST /orders/manual - Manual order (override)
```

## Error Handling

| Error | Handling |
|-------|----------|
| HTTP 429 (Rate limit) | Exponential backoff |
| HTTP 401 (Auth failed) | Token refresh + retry |
| HTTP 5xx (Server error) | Retry with backoff |
| WebSocket disconnect | Auto-reconnect + fallback to polling |
| Margin call | Reduce position sizes |
| Circuit breaker | Stop trading immediately |

## Monitoring & Debugging

### Checking Logs

The application outputs structured JSON logs. You can view them using Docker:

* **Go App Logs**:
  ```bash
  docker-compose logs -f app
  ```
  *(Or stream and format them using `jq`: `docker-compose logs -f app | jq .`)*
* **All Services Logs (App + DB)**:
  ```bash
  docker-compose logs -f
  ```

### Metrics

Metrics are exported to Prometheus format at `http://localhost:8888/metrics`:

```bash
curl http://localhost:8888/metrics | grep trading_
```

Key metrics:
- `trading_orders_placed_total` - Total orders
- `trading_daily_pnl` - Current day P&L
- `trading_drawdown_percent` - Max drawdown
- `trading_circuit_breaker_active` - CB status

### Database Queries

You can connect to the database via command line directly inside the running container:

```bash
docker exec -it zt-postgres-1 psql -U postgres -d zerodha_trading
```

To connect using external GUI clients (pgAdmin, DBeaver, TablePlus, etc.):
* **Host**: `localhost`
* **Port**: `5432`
* **Database Name**: `zerodha_trading`
* **Username**: `postgres`
* **Password**: `trading_password`

Useful SQL queries inside `psql`:

```sql
-- View instrument metadata cache (which replaced Redis)
SELECT key, updated_at, LEFT(value, 100) AS preview FROM metadata_cache;

-- Recent trades and P&L
SELECT * FROM trades ORDER BY created_at DESC LIMIT 10;

-- Candles for analysis
SELECT * FROM candles_5m WHERE token = 100000 ORDER BY time DESC LIMIT 50;

-- Open positions
SELECT * FROM positions WHERE closed_at IS NULL;
```

## Performance Tuning

### Latency Optimization

- Use PostgreSQL metadata table for instrument master caching
- Connection pooling (25 max conns)
- Async order processing

### Throughput

- 1000+ ticks/second processing
- Sub-100ms candle completion
- Parallel order status polling

## Compliance & Risk

⚠️ **IMPORTANT**: This is a high-frequency trading system. Ensure:

1. You have proper regulatory approval
2. Capital is from dedicated trading account
3. All trades are tracked for tax/audit
4. Broker margin requirements are met
5. Stop-losses are always in place

## Testing

```bash
go test ./...
```

Mock ticker runs on startup for testing without live data.

## Architecture Diagram

```
┌─────────────────────────────────────────┐
│         Zerodha Kite API                 │
│    (WebSocket + REST)                    │
└────────────┬────────────────────────────┘
             │
      ┌──────┴──────┐
      ▼             ▼
┌──────────┐  ┌──────────────┐
│  Ticker  │  │ REST Orders  │
└────┬─────┘  └──────┬───────┘
     │               │
     └───────┬───────┘
             ▼
     ┌───────────────┐
     │ Candle Agg    │ → PostgreSQL (TimescaleDB)
     │ (5-min OHLCV) │
     └───────┬───────┘
             │
             ▼
     ┌───────────────┐
     │ Strategy Eng  │
     │ (Indicators)  │
     └───────┬───────┘
             │ Signal
             ▼
     ┌────────────────┐
     │ Risk Manager   │
     │ Capital Protect│
     └───────┬────────┘
             │
             ▼
     ┌────────────────┐
     │ Execution Mgr  │
     │ Order Mgmt     │
     └─────┬──────────┘
           │
           └──→ PostgreSQL (orders, trades, cache)
```

## License

Proprietary. Use only with explicit permission.

## Support

For issues or questions, contact the development team.
