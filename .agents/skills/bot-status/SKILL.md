---
name: bot-status
description: Check current trading bot status and health
---
# Bot Status Skill

Provides real-time status of the trading bot components across Equity and Options strategies.

## Usage
- Ask the agent to check the status or health of the trading bot.
- Use the local `myaws` utility script to fetch remote Docker, database, and system metrics.

## Implementation Steps for Agent
1. Check component health:
   - Database connection status (TimescaleDB / PostgreSQL)
   - WebSocket ticker status & subscribed instrument tokens (209+ instruments)
   - Strategy engine states (`LOW_VOLUME`, `VANDE_BHARAT`, `OPTIONS_SUPERTREND`)
2. Check Options Bot Status (`OPTIONS_SUPERTREND`):
   - Current active option position (`NIFTY24800CE` / `NIFTY24300PE`)
   - 100% Real Live Zerodha NFO Market Quotes (`GetQuote`) for entry, exit, SL, and live P&L tracking
   - Triple SuperTrend indicator alignment (**ST1: 10,4.0; ST2: 7,3.0; ST3: 7,2.0**)
   - Instant 5-minute candle sync at candle close (`Second() < 10` on 5m boundaries)
3. Report metrics:
   - Current positions and P&L (Unrealized & Realized)
   - Orders placed today and executed trade history
   - Last NIFTY 50 5m candle timestamp and spot price
   - Ticker packet loss and connection latencies
4. Show system state:
   - Running status & active trade mode (`OPTIONS_LIVE_TRADING=true/false`)
   - Circuit breaker state & daily portfolio loss cap
   - Market hours status (09:15 AM – 15:30 PM IST)
   - Any errors or warnings
5. AWS Server Monitoring (`myaws` Integration):
   - Run `.\myaws.ps1` to view remote docker container status and system memory usage.
   - Run `.\myaws.ps1 logs` or `.\myaws.ps1 logs -Follow` to view the running bot application log stream.
   - Run `.\myaws.ps1 db` to query the remote database and count `candles_5m` and `candles_1m` tables.
   - Run `.\myaws.ps1 tunnel` to forward the remote TimescaleDB port (`5432`) and Web dashboard port (`8080`) to localhost for local inspection.


