---
name: bot-status
description: Check current trading bot status and health
---
# Bot Status Skill

Provides real-time status of the trading bot components.

## Usage
- Ask the agent to check the status or health of the trading bot.
- Use the local `myaws` utility script to fetch remote Docker, database, and system metrics.

## Implementation Steps for Agent
1. Check component health:
   - Database connection status
   - WebSocket ticker status
   - Strategy engine state
2. Report metrics:
   - Current positions and P&L
   - Orders placed today
   - Last candle timestamp
   - Connection latencies
3. Show system state:
   - Running status
   - Circuit breaker state
   - Market hours status
   - Any errors or warnings
4. AWS Server Monitoring (`myaws` Integration):
   - Run `.\myaws.ps1` to view remote docker container status and system memory usage.
   - Run `.\myaws.ps1 logs` or `.\myaws.ps1 logs -Follow` to view the running bot application log stream.
   - Run `.\myaws.ps1 db` to query the remote database and count `candles_5m` and `candles_1m` tables.
   - Run `.\myaws.ps1 tunnel` to forward the remote TimescaleDB port (`5432`) and Web dashboard port (`8080`) to localhost for local inspection.

