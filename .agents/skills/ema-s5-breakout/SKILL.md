---
name: ema-s5-breakout
description: Analyze, explain, and backtest EMA S5 Breakout Strategy setups with sequential U-shape and Inverted U-shape geometry
---

# EMA S5 Breakout Strategy (`EMAS5_BREAKOUT`) Skill & Reference Guide

This skill defines the mechanical rules, geometric market structure, and standard explanation framework for the **EMA S5 Breakout Strategy**.

---

## 1. Geometric Market Structure Overview

The EMA S5 Breakout Strategy identifies high-probability momentum breakouts following an **Oval ('U'-Shape)** or **Inverted Oval (Inverted 'U'-Shape)** consolidation curve against dynamic Exponential Moving Averages (**EMA 10** and **EMA 20**) and Previous Day High/Low (**PDH/PDL**) levels.

```
       BULLISH 'U'-SHAPE (BUY SETUP)                      BEARISH INVERTED 'U'-SHAPE (SELL SETUP)
       =============================                      =======================================
[1. Starting Peak: High of Left Rim] ⭐                [1. Starting Trough: Low of Left Rim] ⭐
               \                                                      /
                \  (Pullback Phase >= 5 candles)                     /  (Rally Phase >= 5 candles)
                 ▼                                                  ▼
[2. Trough Low: Bottom of the 'U'] 🎯                  [2. Peak High: Top of Inverted 'U'] 🎯
                 │                                                  │
                 │  (Right Rim: Upward Turnaround)                  │  (Right Rim: Downward Turnaround)
                 ▼                                                  ▼
[3. Master Candle (GREEN, Close > EMAs/PDH)]           [3. Master Candle (RED, Close < EMAs/PDL)]
                 │                                                  │
                 ▼                                                  ▼
[4. Confirmation Candle (MUST be GREEN)]               [4. Confirmation Candle (MUST be RED)]
                 │                                                  │
                 ▼                                                  ▼
[5. 🚨 BUY Breakout Trigger (LTP >= Confirm High)]     [5. 🚨 SELL Breakdown Trigger (LTP <= Confirm Low)]
```

---

## 2. Standard Step-by-Step Explanation Framework

When analyzing or explaining any EMA S5 Breakout setup to users or in backtest reports, ALWAYS provide these exact 5 sequential anchor points:

### 🟢 For BUY Setups ('U'-Shape):
1. **Starting Peak High (Left Rim Top)**:
   - Identify the morning high of the day (e.g. TCS 09:35 AM High: ₹2335.00).
2. **Trough Low (Bottom of the 'U')**:
   - Identify the lowest swing bottom formed *after* the Starting Peak (e.g. TCS 11:40 AM Low: ₹2321.00).
   - Verify distance: Total candles from Peak to Trough / Master candidate must be $\ge \text{RallyCandlesCount}$ (Default: $\ge 5$ candles).
3. **Master Candle (Right Rim Rising)**:
   - Must be **GREEN** (`Close > Open`).
   - Rebound from Trough Low: $\frac{\text{Master.Close} - \text{TroughLow}}{\text{TroughLow}} \times 100 \ge \mathbf{0.40\%}$ (configurable `ES5_MIN_REBOUND_PCT`).
   - **Levels Interaction**: Must touch dynamic **EMA 10 or EMA 20** (`Low <= EMA && High >= EMA`), and **close strictly above EMA 10 and EMA 20** (and above PDH if interacting with PDH).
   - Range Filter: $\frac{\text{High} - \text{Low}}{\text{Close}} \times 100 \le \mathbf{2.0\%}$ (`ES5_MASTER_MAX_PCT`).
4. **Inside Consolidation Guard**:
   - Inside check occurs **strictly in between the Master candle and Confirmation candle**.
   - Allows maximum $1$ inside candle (`ES5_MAX_INSIDE_CANDLES`). A 2nd consecutive inside candle invalidates the setup.
   - If any subsequent candle breaches Master Low (`Low < Master.Low`), the setup is **immediately invalidated**.
5. **Confirmation Candle & Color Guard**:
   - Must break Master High (`High > Master.High`).
   - **Color Guard Mandate**: MUST close **GREEN** (`Close > Open`). If it closes RED or DOJI, it is rejected as a bull-trap and **invalidates the setup immediately**.
   - Range Filter: Range $\le \mathbf{1.0\%}$ (`ES5_CONFIRM_MAX_PCT`).
6. **Live Breakout Trigger**:
   - Live tick crosses Confirmation High ($\text{LTP} \ge \text{Confirmation.High}$).
   - Stop-Loss: Anchored at Confirmation Low with buffer ($\text{Confirmation.Low} \times 0.999$).
   - Target 1: 1:2 Risk-Reward ($\text{Entry} + (\text{Entry} - \text{SL}) \times 2$).

---

### 🔴 For SELL Setups (Inverted 'U'-Shape):
1. **Starting Trough Low (Left Rim Bottom)**:
   - Identify the morning low of the day (e.g. NBCC 09:15 AM Low / Day High start).
2. **Peak High (Top of the Inverted 'U')**:
   - Identify the highest swing high formed *after* the Starting Trough (e.g. NBCC 09:15 AM High: ₹89.28).
   - Verify distance: Total candles from Peak to Master candidate must be $\ge \text{RallyCandlesCount}$ (Default: $\ge 5$ candles).
3. **Master Candle (Right Rim Falling)**:
   - Must be **RED** (`Close < Open`).
   - Drop from Peak High: $\frac{\text{PeakHigh} - \text{Master.Close}}{\text{PeakHigh}} \times 100 \ge \mathbf{0.40\%}$ (`ES5_MIN_REBOUND_PCT`).
   - **Levels Interaction**: Must touch dynamic **EMA 10 or EMA 20** (`Low <= EMA && High >= EMA`), and **close strictly below EMA 10 and EMA 20** (and below PDL if interacting with PDL).
   - Range Filter: Range $\le \mathbf{2.0\%}$ (`ES5_MASTER_MAX_PCT`).
4. **Inside Consolidation Guard**:
   - Inside check occurs **strictly in between the Master candle and Confirmation candle**.
   - Allows maximum $1$ inside candle. A 2nd consecutive inside candle invalidates setup.
   - If any subsequent candle breaches Master High (`High > Master.High`), the setup is **immediately invalidated**.
5. **Confirmation Candle & Color Guard**:
   - Must break Master Low (`Low < Master.Low`).
   - **Color Guard Mandate**: MUST close **RED** (`Close < Open`). If it closes GREEN or DOJI, it is rejected as a bear-trap and **invalidates the setup immediately**.
   - Range Filter: Range $\le \mathbf{1.0\%}$ (`ES5_CONFIRM_MAX_PCT`).
6. **Live Breakdown Trigger**:
   - Live tick crosses Confirmation Low ($\text{LTP} \le \text{Confirmation.Low}$).
   - Stop-Loss: Anchored at Confirmation High with buffer ($\text{Confirmation.High} \times 1.001$).
   - Target 1: 1:2 Risk-Reward ($\text{Entry} - (\text{SL} - \text{Entry}) \times 2$).

---

## 3. Real-World Case Studies (Reference Benchmarks)

### Case 1: TCS (28-Aug-2026, 5m Timeframe — BUY Setup)
- **Starting High (Left Rim)**: **09:35 AM** at **₹2335.00** (`Index 4`).
- **Trough Low (Bottom of 'U')**: **11:40 AM** at **₹2321.00** (`Index 29`, 25 candles pullback).
- **Master Candle**: **11:55 AM** (Open: ₹2329.60, High: ₹2331.70, Low: ₹2328.10, Close: **₹2331.00** GREEN).
  - Rebound: $+0.43\%$ from ₹2321.00 ($\ge 0.40\%$). Closed above EMA 10 (2328.95) and EMA 20 (2327.31).
- **Confirmation Candle**: **12:00 PM** (High: ₹2332.00, Close: ₹2331.80 GREEN, broke Master High).
- **Breakout Entry**: **12:05 PM** at **₹2332.00** $\rightarrow$ Surged straight to **₹2340.00+**.

### Case 2: NBCC (28-Aug-2026, 5m Timeframe — SELL Setup)
- **Starting High (Left Rim)**: **09:15 AM** at **₹89.28** (`Index 0`).
- **Master Candle #1**: **10:30 AM** (Open: ₹88.42, High: ₹88.44, Low: ₹88.29, Close: **₹88.31** RED).
  - Drop: $-1.09\%$ from ₹89.28 (15 candles distance). Closed below EMA 10 (88.37), EMA 20 (88.40), PDL (88.42).
- **Confirmation Candle**: **10:35 AM** (High: ₹88.31, Low: ₹88.20, Close: ₹88.21 RED, broke Master Low).
- **Breakdown Entry**: **10:40 AM** at **₹88.20** $\rightarrow$ Target 1: ₹87.80 hit.

---

## 4. Key Configuration Parameters

| Parameter | Default Value | Description |
| :--- | :---: | :--- |
| `ES5_MAX_TRADES_PER_STOCK` | `2` | Max daily trades per symbol |
| `ES5_RALLY_CANDLES_COUNT` | `5` | Min candle distance from Peak/Trough |
| `ES5_MIN_REBOUND_PCT` | `0.40%` | Min rebound/drop % from swing extreme |
| `ES5_MASTER_MAX_PCT` | `2.0%` | Max permissible range % of Master candle |
| `ES5_MAX_INSIDE_CANDLES` | `1` | Max inside candles before invalidation |
| `ES5_CONFIRM_MAX_PCT` | `1.0%` | Max permissible range % of Confirmation candle |
| `ES5_TRADE_END_TIME` | `11:00:00` | Intraday entry cutoff time (IST) |
| `CANDLE_TIMEFRAME` | `1m` / `5m` | Supported candle aggregation timeframe |
