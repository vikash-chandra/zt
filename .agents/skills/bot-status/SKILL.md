---
name: bot-status
description: Check current trading bot status and health
---
# Bot Status Skill

Provides real-time status of the trading bot components.

## Usage
- Ask the agent to check the status or health of the trading bot.

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
