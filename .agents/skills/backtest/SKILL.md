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
   - Options: `OPTIONS_SUPERTREND` (5-minute Triple SuperTrend ST1: 10,4.0; ST2: 7,3.0; ST3: 7,2.0) with 20% 5-minute candle close Trailing Stop-Loss (`OPTIONS_TRAIL_SL_PCT=20.0`)
3. Calculate performance metrics:
   - Total trades, win rate, avg win/loss, profit factor
   - Max drawdown, Sharpe ratio
   - P&L and return percentages
   - Options lot scaling (Base Lot = 65, Reversal Multiplier = 130), 20% trailed SL exits, and 15:14 IST auto square-off
4. Output the trade log with exact IST timestamps (`HH:MM:SS`) and summary statistics.
5. Mandatory Time & Performance Verification:
   - Ensure all indicator loops execute in a single forward pass $O(N)$ (`CalculateTripleSuperTrendSeries`) to prevent quadratic slowdowns.
   - Ensure all backtested candle timestamps and trade execution logs use `data.NormalizeToIST(t)` or centralized time utilities in `data/time_utils.go`.
   - Run empirical runtime verification on backtest output reports after code edits.
