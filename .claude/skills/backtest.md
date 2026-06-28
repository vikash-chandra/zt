---
name: backtest
description: Run backtesting analysis on trading strategies using historical data
usage: /backtest [symbol] [days]
---

# Backtest Skill

Analyzes trading strategy performance using historical candle data from PostgreSQL.

## Usage

```
/backtest TOKEN_NSE 30
/backtest all 90
/backtest BTC 7
```

## What it does

1. Fetches historical 5-minute candles from TimescaleDB
2. Simulates strategy signals (VWAP + RSI) on historical data
3. Calculates performance metrics:
   - Total trades, win rate, avg win/loss
   - Max drawdown, Sharpe ratio
   - P&L and return percentages
4. Outputs trade log and summary statistics

## Requirements

- PostgreSQL with timescaledb extension
- Historical candle data in `candles_5m` table
- `.env` configured with database credentials