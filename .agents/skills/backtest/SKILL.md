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

### 1. Vande Bharat Momentum Setup (`VANDE_BHARAT`)
- **Timeframe**: Configurable **`1m` (Default)** or **`5m`** candles.
- **Position Sizing**: Dynamically calculates `Quantity = min( floor(RiskPerTrade / SL_Distance), floor(Capital / Margin) )`.
- **4 Core Rules**:
  1. **Rule 1 (Confirmation Breakout)**: Candle 2 breaks Master High (BUY) / Low (SELL) $\rightarrow$ Candle 2 is Confirmation Candle with SL anchored at Candle 2 Low/High. Trade triggers when **Confirmation High / Low** is broken.
  2. **Rule 2 (Master Fallback)**: Candle 2 inside Master range $\rightarrow$ Candle 2 is only SL Anchor (SL at Candle 2 Low/High). Trade triggers when **Master High / Low** is broken directly.
  3. **Rule 3 (Wait for Breakout & Same-Breakout-Candle Execution Guard)**: Bot waits while price consolidates. When breakout candle breaches trigger level, trade MUST be initiated in that breakout candle. If breakout candle closes without trade execution, setup is **cancelled and expired immediately at candle close** (no late chase entries).
  4. **Rule 4 (Mirror Symmetry for SELL)**: Exact mirror rules applied to breakdown SELL setups. Master Range $\le 1.8\%$, Wicks $\le 40\%$, Candle 2 SL Range $0.5\%-1.0\%$, SL Buffer $0.1\%$, Cutoff Time `11:00:00 IST`.

### 2. Vande Bharat Trap Strategy (`VANDE_BHARAT_TRAP`)
- **Timeframe**: Configurable **`1m` (Default)** or **`5m`** candles.
- **Rules**:
  1. 1st Fake Master Candle (09:15 AM): Closes above PDH with RED body for BUY, or below PDL with GREEN body for SELL. Range $\le 3.0\%$.
  2. Genuine Master Formation: Subsequent candle breaking Fake Master High (BUY) or Low (SELL) forms Genuine Master (Range $\le 1.8\%$, Wicks $\le 40\%$).
  3. 2nd Candle SL Anchor & Confirmation / Master Fallback: Candle 2 range in $[0.5\%, 1.0\%]$. Low (BUY) or High (SELL) locked as SL.
  4. Wait for Breakout & Same-Breakout-Candle Execution Guard: Trade must execute in breakout candle; otherwise expires immediately at candle close.
  5. Live breakout before `VBTTradeEndTime` (11:00:00 IST), SL Buffer $0.1\%$.

### 3. EMA S5 Breakout Strategy (`EMAS5_BREAKOUT`)
- **Timeframe**: Configurable **`1m` (Default)** or **`5m`** candles.
- **Rules**:
  1. Rally sequence $\ge 5$ continuous candles forming U-Shape (BUY) or Inverted U-Shape (SELL).
  2. Upward/downward oval curve move $\ge 0.50\%$ from sequence extreme to Master close.
  3. Master Candle touches dynamic EMA 10/20 zone within $0.10\%$ buffer, closes beyond all levels, range $\le 2.0\%$, wicks $\le 40.0\%$.
  4. Max 1 inside candle allowed before Confirmation candle (breaks Master extreme, closes beyond Master extreme, range $\le 1.0\%$, strict color match: Green for BUY, Red for SELL).
  5. Live breakout before `ES5TradeEndTime` (11:00:00 IST), SL anchored at Confirmation Low/High, max 2 trades per stock per day.

### 4. Low Volume Breakout (`LOW_VOLUME`)
- **Timeframe**: Configurable **`5m` (Default)** or **`1m`** candles.
- **Position Sizing**: Dynamically calculates `Quantity = min( floor(RiskPerTrade / SL_Distance), floor(Capital / Margin) )`.
- **09:15 AM Qualification**: 1st candle must close above PDH (BUY) or below PDL (SELL).
- **Lowest Volume Setup (Option A)**:
  - Setup candle is the single lowest-volume candle of the session since 09:15 AM.
  - BUY requires RED setup (`Close < Open`); SELL requires GREEN setup (`Close > Open`).
  - Breakout is valid **ONLY on the single immediate next candle**. If no breakout, setup expires. Cutoff: `10:45:00 IST`.

### 5. Triple SuperTrend Options Strategy (`OPTIONS_SUPERTREND`)
- **Indicators**: Single forward pass $O(N)$ computation of ST1 (10, 4.0), ST2 (7, 3.0), ST3 (7, 2.0).
- **Candle Close Alignment**: Trend evaluation on completed 5m candle close (`trend != lastTrend`).
- **Trailing Stop-Loss**:
  - Initial SL: 50% premium increase (`sl_pct = 50.0`).
  - Monotonic 20% candle close Trailing SL: $\text{Candidate SL} = \text{CurrentPremium} \times 1.20$.
  - SL tightens if candidate < current; never loosens on adverse bounces.
- **Dynamic Lot Sizing**: Base Lot (e.g. 65 Qty for NIFTY) $\rightarrow$ 2x Lot (130 Qty) on trend reversals up to `max_multiplier` (default 4x).
- **Cutoffs**: Last New Trade at `14:32:00 IST`, Auto Square-Off at `15:13:00 IST`.

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
