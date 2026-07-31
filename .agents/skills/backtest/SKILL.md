---
name: backtest
description: Run backtesting analysis on trading strategies using historical data
---
# Backtest Skill

Analyzes trading strategy performance using historical candle data from PostgreSQL.

## Usage
- Ask the agent to run a backtest on a specific token symbol or options strategy.

## Implementation Steps for Agent
1. Fetch historical 5-minute candles from TimescaleDB (`candles_5m` table).
2. Simulate strategy signals on the historical data:
   - Equity: `LOW_VOLUME` and `VANDE_BHARAT`
   - Options: `OPTIONS_SUPERTREND` (5-minute Triple SuperTrend ST1: 10,4.0; ST2: 7,3.0; ST3: 7,2.0)
3. Calculate performance metrics:
   - Total trades, win rate, avg win/loss, profit factor
   - Max drawdown, Sharpe ratio
   - P&L and return percentages
   - Options lot scaling (Base Lot = 65, Reversal Multiplier = 130) and 15:15 IST auto square-off
4. Output the trade log with exact timestamps (`HH:MM:SS`) and summary statistics.
