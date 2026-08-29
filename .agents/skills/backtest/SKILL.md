---
name: backtest
description: Run backtesting analysis on trading strategies using historical data
---
# Backtest Skill

Analyzes trading strategy performance, simulates trade lifecycles, and computes institutional risk-adjusted metrics using historical candle data from TimescaleDB / PostgreSQL.

## Available Backtest & Simulation Scripts

| Script | Purpose | Command |
| :--- | :--- | :--- |
| **Options Multi-Day Paper Trade Runner** | Runs full Multi-Index options Triple SuperTrend simulations across historical dates | `go run scripts/run_options_paper_trades/main.go` |
| **5-Day Procedural Simulation** | Generates 5-day continuous backtest data across equity and options strategies | `go run scripts/simulate_5days/main.go` |
| **Single-Strategy Backtest Runner** | Evaluates equity breakouts (`LOW_VOLUME`, `VANDE_BHARAT`) on historical candles | `go run scripts/backtest/main.go` |
| **Historical Data Seeding** | Seeds historical 5m & 1m candles for backtesting from Zerodha API / procedural fallback | `go run scripts/seed/main.go` |

---

## Strategy Simulation Specifications

### 1. Vande Bharat Setup (`VANDE_BHARAT`)
- **Timeframe**: Configurable **`1m` (Default)** or **`5m`** candles.
- **Position Sizing**: Dynamically calculates `Quantity = min( floor(RiskPerTrade / SL_Distance), floor(Capital / Margin) )`.
- **09:15 AM Master Candle**:
  - Minimum 2.0% opening gap relative to **Yesterday's Close price**:
    $$\text{BUY Gap \%} = \frac{\text{Open} - \text{YesterdayClose}}{\text{YesterdayClose}} \times 100 \ge 2.0\%$$
    $$\text{SELL Gap \%} = \frac{\text{YesterdayClose} - \text{Open}}{\text{YesterdayClose}} \times 100 \ge 2.0\%$$
  - Master Candle range $\le 1.8\%$ of stock price (`masterMaxPct`) and total wicks $\le 40\%$ of range.
  - BUY requires `Close > PDH` (Green body); SELL requires `Close < PDL` (Red body).
- **2nd Candle (09:16 AM for 1m / 09:20 AM for 5m) SL Anchor**:
  - Range % `(High - Low) / Close * 100` must be strictly in $[0.5\%, 1.0\%]$ (`slMinPct` to `slMaxPct`).
  - Low (BUY) or High (SELL) of 2nd candle is locked as Stop-Loss anchor.
- **Intermediate Consolidation**:
  - All candles before confirmation must remain strictly within `[Master.Low, Master.High]`. Breach of opposite boundary invalidates setup.
- **Any-Color Confirmation Candle**:
  - 1st candle breaking Day High (BUY) or Day Low (SELL) qualifies as Confirmation (Green, Red, or Doji).
- **Entry Day Move Constraint**:
  - At trade entry (LTP), price move relative to reference level must be $\le 1.8\%$:
    - BUY: $\frac{\text{LTP} - \text{PDH}}{\text{PDH}} \times 100 \le 1.8\%$
    - SELL: $\frac{\text{PDL} - \text{LTP}}{\text{PDL}} \times 100 \le 1.8\%$

### 2. Low Volume Breakout (`LOW_VOLUME`)
- **Timeframe**: Configurable **`5m` (Default)** or **`1m`** candles.
- **Position Sizing**: Dynamically calculates `Quantity = min( floor(RiskPerTrade / SL_Distance), floor(Capital / Margin) )`.
- **09:15 AM Qualification**: 1st candle must close above PDH (BUY) or below PDL (SELL).
- **Lowest Volume Setup (Option A)**:
  - Setup candle is the single lowest-volume candle of the session since 09:15 AM.
  - BUY requires RED setup (`Close < Open`); SELL requires GREEN setup (`Close > Open`).
  - Breakout is valid **ONLY on the single immediate next candle**. If no breakout, setup expires.
- **Operational Window**: Active after initial ignore window (default ignores first 3 candles).

### 3. Triple SuperTrend Options Strategy (`OPTIONS_SUPERTREND`)
- **Indicators**: Single forward pass $O(N)$ computation of ST1 (10, 4.0), ST2 (7, 3.0), ST3 (7, 2.0).
- **Candle Close Alignment**: Trend evaluation on completed 5m candle close (`trend != lastTrend`).
- **Trailing Stop-Loss**:
  - Initial SL: 50% premium increase (`sl_pct = 50.0`).
  - Monotonic 20% candle close Trailing SL: $\text{Candidate SL} = \text{CurrentPremium} \times 1.20$.
  - SL tightens if candidate < current; never loosens on adverse bounces.
- **Dynamic Lot Sizing**: Base Lot (e.g. 65 Qty for NIFTY) $\rightarrow$ 2x Lot (130 Qty) on trend reversals up to `max_multiplier`.
- **EOD Square-Off**: Auto square-off at `15:14:00 IST`.

---

## Performance Metrics Calculation

When running backtest reports, compute and output:
1. **Total Trades & Win Rate**: $\text{Win Rate \%} = \frac{\text{Winning Trades}}{\text{Total Trades}} \times 100$
2. **Profit Factor**: $\text{Profit Factor} = \frac{\text{Gross Profits}}{\text{Gross Losses}}$
3. **Average Reward : Risk Ratio**: $\text{Avg R:R} = \frac{\text{Average Win Amount}}{\text{Average Loss Amount}}$
4. **Max Drawdown (MDD)**: Peak-to-trough decline in portfolio value.
5. **Holding Duration**: Exact time held in minutes calculated dynamically (`time_held_minutes`).

---

## Mandatory Execution Guidelines
- **Single Pass $O(N)$ Indicators**: Always use `CalculateTripleSuperTrendSeries` to prevent quadratic $O(N^2)$ slowdowns.
- **IST Time Normalization**: Format all candle timestamps and trade logs with `data.NormalizeToIST(t)` (`Asia/Kolkata`).
- **Dynamic Report Pathing**: Write output markdown reports to the active conversation's artifact directory.
