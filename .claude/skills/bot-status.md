---
name: bot-status
description: Check current trading bot status and health
usage: /bot-status
---

# Bot Status Skill

Provides real-time status of the trading bot components.

## Usage

```
/bot-status
```

## What it does

1. Checks component health:
   - Database connection
   - Redis connection
   - WebSocket ticker status
   - Strategy engine state
2. Reports metrics:
   - Current positions and P&L
   - Orders placed today
   - Last candle timestamp
   - Connection latencies
3. Shows system state:
   - Running status
   - Circuit breaker state
   - Market hours status
   - Any errors/warnings

## Output

- Component status table
- Current exposure and P&L
- Recent order activity
- Any alerts or warnings