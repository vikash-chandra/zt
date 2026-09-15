---
name: vande-bharat
description: Analyze, explain, configure, and trade the Vande Bharat Momentum breakout strategy across 1m and 5m timeframes with manual watchlist integration
---

# Vande Bharat Momentum Strategy (`VANDE_BHARAT`) Skill & Reference Guide

This skill defines the operational mechanics, timeframe configurations, risk anchoring rules, and standard explanation framework for the **Vande Bharat Momentum Strategy** and its Daily Manual Watchlist integration.

---

## 1. Overview & Core Philosophy

The **Refined Vande Bharat** strategy is an institutional opening-momentum breakout model designed to capture sharp directional expansion right after market open. It anchors setups against Previous Day High/Low (**PDH/PDL**) levels, defines risk on the initial opening range, and executes live tick-level breakouts with zero latency.

```
       BULLISH BUY SETUP                                  BEARISH SELL SETUP
       =================                                  ==================
[09:15 AM Open > PDH]                              [09:15 AM Open < PDL]
        │                                                  │
        ▼                                                  ▼
[Candle 1: Master Candle]                          [Candle 1: Master Candle]
(Close > PDH, GREEN body, Range <= 1.8%)           (Close < PDL, RED body, Range <= 1.8%)
        │                                                  │
        ▼                                                  ▼
[Candle 2: SL Anchor & Confirmation]               [Candle 2: SL Anchor & Confirmation]
• Rule 1: High > Master.High -> Confirm High       • Rule 1: Low < Master.Low -> Confirm Low
  SL = Candle 2 Low                                  SL = Candle 2 High
• Rule 2: High <= Master.High -> Master High       • Rule 2: Low >= Master.Low -> Master Low
  SL = Candle 2 Low                                  SL = Candle 2 High
        │                                                  │
        ▼                                                  ▼
[Candle 3+: Live Tick Breakout Execution]          [Candle 3+: Live Tick Breakdown Execution]
• Buy Trigger: LTP > Trigger Level                 • Sell Trigger: LTP < Trigger Level
• SL: Broker SL-M Order Placed Instantly           • SL: Broker SL-M Order Placed Instantly
• Target: 1:2 Risk-Reward (or Dynamic Trailing)    • Target: 1:2 Risk-Reward (or Dynamic Trailing)
```

---

## 2. Timeframe Execution Modes (`1m` vs `5m`)

The engine seamlessly operates on either **1-minute** or **5-minute** candles based on the `candle_time_frame` setting in System Settings:

| Parameter / Milestone | 1-Minute Timeframe (`1m`) | 5-Minute Timeframe (`5m`, Default) |
| :--- | :--- | :--- |
| **Master Candle (Candle 1)** | 09:15:00 – 09:16:00 IST | 09:15:00 – 09:20:00 IST |
| **SL Anchor / Confirm (Candle 2)** | 09:16:00 – 09:17:00 IST | 09:20:00 – 09:25:00 IST |
| **Earliest Live Execution (Candle 3)** | **09:17:01 IST** onwards | **09:25:01 IST** onwards |
| **Minimum SL Threshold (`minSL`)** | **`0.05%`** (dynamically adapted) | **`0.50%`** (standard equity volatility) |
| **Maximum SL Threshold (`maxSL`)** | `1.00%` (configurable) | `1.00%` (configurable) |
| **Master Candle Max Range** | $\le 1.80\%$ of close | $\le 1.80\%$ of close |
| **Master Candle Max Wick** | $\le 40.0\%$ total wick proportion | $\le 40.0\%$ total wick proportion |
| **Trade Window Cutoff** | `11:00:00 IST` | `11:00:00 IST` |

### ⚡ Dynamic 1-Minute MinSL Adaptation
* Standard 5-minute candles typically have price ranges between $0.50\% - 1.20\%$, making `sl_min_pct = 0.50%` appropriate.
* 1-minute candles for liquid equities (e.g. Reliance, SBIN, TCS) frequently range between $0.05\% - 0.35\%$.
* In `1m` mode, the strategy engine dynamically lowers the SL minimum threshold to **$0.05\%$** if unconfigured or default:
  ```go
  minSL := e.slMinPct
  if e.candleTimeFrame == "1m" && (minSL == 0 || minSL >= 0.30) {
      minSL = 0.05 // dynamically adapt for 1m volatility
  }
  ```
* This prevents valid 1-minute setups from being erroneously rejected while preserving the full $0.50\%$ threshold for $5\text{m}$ mode.

---

## 3. The 4 Core Step-by-Step Execution Rules

### Rule 1: Candle 2 Breaks Master Extreme (Confirmation Breakout)
1. **Master Candle Qualification**:
   - **BUY**: Candle 1 closes **GREEN** with `Close > PDH`. Range $\le 1.8\%$, Wicks $\le 40\%$.
   - **SELL**: Candle 1 closes **RED** with `Close < PDL`. Range $\le 1.8\%$, Wicks $\le 40\%$.
2. **Candle 2 Confirmation**:
   - **BUY**: If Candle 2 breaks Master High (`Candle2.High > Master.High`) and closes **GREEN**:
     - `TriggerLevel = Candle2.High` (Confirmation High).
     - `StopLoss = Candle2.Low` (Candle 2 Low is the SL anchor).
   - **SELL**: If Candle 2 breaks Master Low (`Candle2.Low < Master.Low`) and closes **RED**:
     - `TriggerLevel = Candle2.Low` (Confirmation Low).
     - `StopLoss = Candle2.High` (Candle 2 High is the SL anchor).
3. **Execution**: Trade is initiated on the very first live tick crossing `TriggerLevel` during or after Candle 3.

---

### Rule 2: Candle 2 Inside Master Range (Master Breakout Fallback)
1. **Candle 2 Inside Master Range**:
   - **BUY**: If Candle 2 does **NOT break Master High** (`Candle2.High <= Master.High`):
     - Candle 2 serves as the **Stop-Loss Anchor ONLY** (`StopLoss = Candle2.Low`).
     - `TriggerLevel = Master.High` (Master High remains the breakout trigger level).
   - **SELL**: If Candle 2 does **NOT break Master Low** (`Candle2.Low >= Master.Low`):
     - Candle 2 serves as the **Stop-Loss Anchor ONLY** (`StopLoss = Candle2.High`).
     - `TriggerLevel = Master.Low` (Master Low remains the breakdown trigger level).
2. **Execution**: The trade triggers immediately when live price breaks Master High / Master Low.

---

### Rule 3: Wait for Breakout & Same-Breakout-Candle Execution Guard
1. **Consolidation Waiting**:
   - If subsequent candles remain inside the range without crossing the trigger level, the bot continues to wait.
2. **Same-Breakout-Candle Execution Mandate**:
   - When a breakout candle breaches the trigger level, the trade **MUST be initiated on that breakout candle**.
   - If a breakout candle breaches the trigger level and closes without trade execution, the setup is **expired and cancelled immediately** at candle close. This prevents chasing late runaway entries after the initial momentum thrust.

---

### Rule 4: Vice-Versa for SELL / Breakdown
* Exact mirror symmetry is strictly applied to all SELL setups:
  - Master Candle must close RED below PDL.
  - Stop-Loss is anchored to Candle 2 High (`SL = Candle2.High * (1 + slBufferPct)`).
  - Breakdown triggered at Confirmation Low (Rule 1) or Master Low (Rule 2).

---

## 4. Daily Manual Watchlist Integration

The strategy supports trading handpicked stocks selected before 09:15 AM through the **Daily Watchlist tab**:

### 🛡️ Key Architectural Guarantees:
1. **Pre-Market Persistence**: Stocks entered into `daily_manual_watchlist` prior to 09:15 AM are retained when the automated pre-selection screener runs at 09:15:00 IST. They are tagged with provenance `MANUAL`.
2. **Dynamic Strategy Enrollment**: Manual stocks are automatically enrolled into active trading strategies (including `VANDE_BHARAT`).
3. **WebSocket Subscription**: Manual stock tokens are immediately registered with `RobustKiteTicker` for real-time tick streaming and 1m/5m candle aggregation.
4. **Directional Bias Immunity (`hasDir && !isManual`)**:
   - Automated screener stocks inherit a strict directional bias (`LONG_ONLY` or `SHORT_ONLY`) from sectoral or news screening.
   - Handpicked manual stocks are **exempt from directional bias**:
     ```go
     // In engine.go
     if hasDir && !isManual {
         if posType == "LONG" && dir != "BUY" && dir != "LONG" && dir != "BOTH" {
             // Blocked for screener stocks
             return
         }
     }
     ```
   - This ensures manual stocks can cleanly trigger either BUY or SELL Vande Bharat setups depending purely on price action relative to PDH/PDL.

---

## 5. Concrete Walkthrough Scenarios

### Scenario 1: 1-Minute Execution (`1m` Mode — e.g. COFORGE)
* **Reference Levels**: PDH = ₹1,850.00, PDL = ₹1,800.00, Previous Close = ₹1,830.00
* **Candle 1 (09:15:00 – 09:16:00 IST)**:
  - Open = ₹1,855.00, High = ₹1,868.00, Low = ₹1,852.00, Close = ₹1,865.00 (**GREEN**, above PDH).
  - Master Candle Established (`Master.High = 1868.00`, `Master.Low = 1852.00`).
* **Candle 2 (09:16:00 – 09:17:00 IST)**:
  - Open = ₹1,865.00, High = ₹1,870.00, Low = ₹1,864.00, Close = ₹1,869.00 (**GREEN**).
  - High ₹1,870.00 > Master High ₹1,868.00 $\rightarrow$ Rule 1 Triggered!
  - `TriggerLevel = ₹1,870.00` (Confirmation High).
  - `StopLoss = ₹1,864.00` (Candle 2 Low).
  - Risk Distance = ₹6.00 ($0.32\% \ge 0.05\%$ dynamic 1m minSL threshold $\rightarrow$ **VALID**).
* **Candle 3 (09:17:05 IST Live Tick)**:
  - LTP hits ₹1,870.50 $\rightarrow$ Bot initiates BUY order immediately.
  - Broker SL order placed at ₹1,864.00 with target at ₹1,882.50 ($1:2$ RR).

---

### Scenario 2: 5-Minute Execution (`5m` Mode — e.g. SBIN)
* **Reference Levels**: PDH = ₹812.00, PDL = ₹795.00, Previous Close = ₹800.00
* **Candle 1 (09:15:00 – 09:20:00 IST)**:
  - Open = ₹817.00, High = ₹824.00, Low = ₹815.00, Close = ₹822.00 (**GREEN**, above PDH).
  - Master Candle Established.
* **Candle 2 (09:20:00 – 09:25:00 IST)**:
  - Open = ₹822.00, High = ₹823.50, Low = ₹819.00, Close = ₹821.00.
  - High ₹823.50 $\le$ Master High ₹824.00 $\rightarrow$ Rule 2 Triggered (Inside Master Range)!
  - `TriggerLevel = ₹824.00` (Master High).
  - `StopLoss = ₹819.00` (Candle 2 Low).
  - Risk Distance = ₹5.00 ($0.61\% \in [0.50\%, 1.00\%]$ standard SL range $\rightarrow$ **VALID**).
* **Candle 3 (09:25:12 IST Live Tick)**:
  - LTP hits ₹824.20 $\rightarrow$ Bot initiates BUY order on Master High breakout.
  - Broker SL order placed at ₹819.00 with target at ₹834.00 ($1:2$ RR).

---

## 6. Verification & Troubleshooting Commands

```bash
# Run 1m and 5m Vande Bharat Engine Unit Tests
go test -v -run TestVandeBharatEngine strategy/vande_bharat_engine_test.go strategy/vande_bharat_engine.go strategy/models.go

# Verify Full System Configurations & Engine Mappings
go run scripts/verify_configs/main.go

# Audit Live Logs for Vande Bharat Candle Formations
# Container logs on AWS:
ssh -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker logs zt-app-1 --tail 300 | grep -i vande"
```
