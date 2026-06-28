---
name: backtest
description: Run backtesting analysis on trading strategies using historical data
---
# Backtest Skill

Analyzes trading strategy performance using historical candle data from PostgreSQL.

## Usage
- Ask the agent to run a backtest on a specific token symbol or all symbols.

## Implementation Steps for Agent
1. Fetch historical 5-minute candles from TimescaleDB (`candles_5m` table).
2. Simulate strategy signals (VWAP + RSI) on the historical data.
3. Calculate performance metrics:
   - Total trades, win rate, avg win/loss
   - Max drawdown, Sharpe ratio
   - P&L and return percentages
4. Output the trade log and summary statistics.
