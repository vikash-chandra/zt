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
   - Per-index database configuration loaded from `options_index_configs` (`NIFTY 50`, `NIFTY BANK`, `BSE SENSEX`, `FINNIFTY`, `MIDCPNIFTY`)
   - Current active option position (`NIFTY24800CE` / `NIFTY24300PE`) with entry price and live Trailed SL (`sl_price`)
   - 100% Real Live Zerodha NFO Market Quotes (`GetQuote`) for entry, exit, SL, and live P&L tracking
   - Multi-Index 20% 5-minute candle close Trailing Stop-Loss (`trail_sl_enabled=true`, `trail_sl_pct=20.0`)
   - Triple SuperTrend indicator alignment (**ST1: 10,4.0; ST2: 7,3.0; ST3: 7,2.0**) computed via $O(N)$ single-pass engine
   - Instant 5-minute candle sync at candle close (`Second() < 10` on 5m boundaries)
3. Report metrics:
   - Current positions and P&L (Unrealized & Realized)
   - Orders placed today and executed trade history
   - Last NIFTY 50 5m candle timestamp and spot price
   - Ticker packet loss and connection latencies
4. Show system state:
   - Running status & active trade mode (Live vs Paper from `options_index_configs`)
   - Circuit breaker state & daily portfolio loss cap
   - Market hours status (09:15 AM – 15:30 PM IST)
   - Bot Restart Safety Lock status (Allowed pre-market < 09:15 AM and post-market >= 03:45 PM IST; Locked during market hours)
   - Any errors or warnings
5. AWS Server Monitoring (`myaws` Integration):
   - Run `.\myaws.ps1` to view remote docker container status and system memory usage.
## Mandatory Centralized Time & UI Performance Checklist
- **Database Configuration Engine**: Strategy and risk settings are persisted in PostgreSQL (`options_index_configs` & `app_system_configs`) and configured dynamically via the UI Settings modal (`#app-settings-modal`).
- **Centralized Time Functions**: All time-handling logic must pass timestamps through `data.NormalizeToIST(t)` or `data.*` functions in `data/time_utils.go` (`window.ISTTime` on frontend).
- **Post-Edit Verification**: Always run empirical runtime verification (querying API endpoints or DB rows) after code edits to ensure timestamps display 100% accurately in IST (`Asia/Kolkata`) with zero 5.5-hour UTC shifts.
- **UI Performance**: Ensure frontend chart instances are reused without canvas destruction, and data fetches execute in parallel.
