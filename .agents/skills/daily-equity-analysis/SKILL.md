---
name: daily-equity-analysis
description: Deep end-of-day and intraday audit and analysis of equity stocks across all 5 trading strategies (Vande Bharat, Low Volume, EMA S5 Breakout, Vande Bharat Trap, Fake Breakout), verifying executed vs missed trades, candle-by-candle diagnostics, and UI audit/telemetry logs.
---

# Daily Equity Analysis & Stock Audit Skill

A comprehensive methodology and automated tooling suite for performing deep post-market and intraday equity performance reviews, missed-trade audits, candle-by-candle rule verifications, and UI audit trail validation.

---

## 1. Core Workflow & Scope

When an equity analysis or trade audit is requested:
1. **Query Executed Trades & Positions**: Extract today's executed equity trades from PostgreSQL `trades` and `orders` tables (`strategy != 'OPTIONS_SUPERTREND'`).
2. **Retrieve the Universe of Watchlist Candidates**: Fetch both manual and automated stock selections from `daily_watchlists` and `daily_manual_watchlist` for the target trading session.
3. **Trace Candle-by-Candle Lifecycle**: For each candidate stock, fetch the previous trading day's levels (PDH, PDL, PDC) and intraday 5-minute candles to evaluate against the 5 equity strategies.
4. **Disambiguate Executed vs. Missed vs. Disqualified Trades**:
   - **Executed Trades**: Verified with entry time, fill price, broker SL order ID, exit price, and realized P&L.
   - **Disqualified Candidates**: Detailed explanation of the exact failure point (e.g. Candle 1 inside PDH/PDL, excess wicks, Candle 2 wrong color, or cutoff exceeded).
   - **Missed Trades (if any)**: Flag any stock where all algorithmic criteria were satisfied but no fill occurred (e.g. zero-quantity position sizing, risk per trade exceeded, broker API reject, or WebSocket tick lag).
5. **Verify UI Audit & Telemetry Logs**: Verify that `/api/strategy/stock-audit` and `/api/strategy/events` correctly reflect all telemetry stages in the interactive web dashboard.
6. **Strict Database Configuration Supremacy**: Always inspect and apply the actual live configurations from PostgreSQL `app_system_configs` table (`TRADING_STRATEGY`, `EQUITY_STRATEGY`) for cutoffs, thresholds, and selector attachments. Never assume or cite hardcoded code fallback defaults.

---

## 2. The 5 Equity Strategies & Exact Audit Criteria

### A. Vande Bharat Momentum (`VANDE_BHARAT`)
* **Timeframe**: `5m` (Default) or `1m`.
* **Master Candle (Candle 1: 09:15–09:20 IST)**:
  - **BUY Setup**: Candle 1 must close **GREEN** (`Close > Open`) **ABOVE PDH** (`Close > PDH`). Total range $\le 3.00\%$, total wicks $\le 60.0\%$.
  - **SELL Setup**: Candle 1 must close **RED** (`Close < Open`) **BELOW PDL** (`Close < PDL`). Total range $\le 3.00\%$, total wicks $\le 60.0\%$.
* **Confirmation Candle (Candle 2: 09:20–09:25 IST)**:
  - **BUY Setup**: Candle 2 must break Master High (`Candle 2 High > Master High`) AND close **GREEN**. Trigger Level = `Candle 2 High`, SL Anchor = `Candle 2 Low`.
  - **SELL Setup**: Candle 2 must break Master Low (`Candle 2 Low < Master Low`) AND close **RED**. Trigger Level = `Candle 2 Low`, SL Anchor = `Candle 2 High`.
* **Live Execution (Candle 3 onwards, cutoff 10:45 / 11:00 IST)**:
  - BUY order placed immediately when `LTP > Trigger Level`.
  - Immediate broker-side Stop Loss order (`SL` / `SL-M`) placed at SL Anchor.
  - Setup cancelled if trigger level is not broken during the entry window or if reversal occurs.

---

### B. Low Volume Scalp (`LOW_VOLUME`)
* **Timeframe**: `5m`.
* **Daily Global Bias**: Established at `09:24:00 IST` via Nifty 50 advance/decline breadth (`BUY_ONLY` if advances > declines, `SELL_ONLY` if declines > advances).
* **1st Candle (09:15 IST) Qualification**:
  - `BUY_ONLY`: Candle 1 must close **ABOVE PDH** (`Close > PDH`).
  - `SELL_ONLY`: Candle 1 must close **BELOW PDL** (`Close < PDL`).
* **Setup Candle**: The candle with the absolute lowest volume since 09:15 AM:
  - Must be the **immediately preceding candle** to prevent stale triggers.
  - For `BUY_ONLY`: Setup candle must be **RED** (`Close < Open`).
  - For `SELL_ONLY`: Setup candle must be **GREEN** (`Close > Open`).
* **Trigger**: Real-time tick crosses setup candle's high (BUY) or low (SELL).

---

### C. EMA S5 Breakout (`EMAS5_BREAKOUT`)
* **Timeframe**: `5m`.
* **Geometry**: Evaluates sequential U-shape (Bottom-to-Top Oval for BUY) and Inverted U-shape (Top-to-Bottom Oval for SELL) with EMA 5, 10, and 20.
* **Warm-up Requirement**: Requires 100–150 preceding historical 5m candles loaded into memory buffer to compute rolling EMAs accurately.
* **Master Candle**: Formed when price closes outside the EMA band after a minimum rebound/drop ($\ge 0.40\%$) from today's lowest low or highest high.
* **Entry Cutoff**: New setups and entries are strictly restricted after `14:30:30 IST` to prevent holding risk into end-of-day square-off.

---

### D. Vande Bharat Trap (`VANDE_BHARAT_TRAP`)
* **Timeframe**: `5m`.
* **Fake Master (Candle 1: 09:15 IST)**:
  - **BUY Trap**: Candle 1 closes **RED below PDL** (trapping initial short sellers).
  - **SELL Trap**: Candle 1 closes **GREEN above PDH** (trapping initial buyers).
* **Real Master & Confirmation (Candles 2 & 3)**:
  - Price abruptly reverses back inside and breaks through the opposite side of the Fake Master range with a strong counter-directional close.
* **Trigger**: Breakout of confirmation bar high/low before trade end cutoff (configured dynamically in DB, e.g. `10:00:00` or `11:00:00 IST`).

---

### E. Fake Breakout (`FAKE_BREAKOUT`)
* **Timeframe**: `5m`.
* **Opening Gap**: Requires opening gap between **$3.5\%$ and $7.5\%$** (or $4.0\%$ to $8.0\%$).
* **Counter-Color 1st Candle**:
  - Gap Up with a solid **RED** Master Candle.
  - Gap Down with a solid **GREEN** Master Candle.
* **Wicks**: Total wick percentage $\le 60.0\%$.
* **Confirmation**: Candle 2 breaks Master Candle low (for Gap Up) or high (for Gap Down).

---

## 3. Quick CLI Commands

| Action | Execution Command |
| :--- | :--- |
| **Run Daily Equity Audit Script** | `node .agents/skills/daily-equity-analysis/scripts/audit_equity.js [YYYY-MM-DD]` |
| **Query Executed Trades Today** | `ssh -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -c 'SELECT id, symbol, strategy, side, quantity, entry_price, exit_price, pnl, status, time_held_minutes FROM trades WHERE entry_time >= CURRENT_DATE ORDER BY id ASC;'"` |
| **Query Strategy Events Today** | `curl -s 'http://3.7.29.3:8080/api/strategy/events?date=YYYY-MM-DD' \| jq '.count, .events[].title'` |
| **Inspect Stock Strategy Audit** | `curl -s 'http://3.7.29.3:8080/api/strategy/stock-audit?symbol=KAYNES&date=YYYY-MM-DD' \| jq '.day_summary, .applied_config'` |

---

## 4. UI Audit & Telemetry Verification Protocol

Whenever auditing the interactive dashboard UI (`index.html`):
1. **Stock Chart Header "Audit" Button**:
   - Ensure clicking the purple **`Audit`** button ([`#view-stock-audit-btn`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/index.html#L4605)) launches the modal for the currently selected stock.
   - Verify that the **Executive Verdict Banner** renders the correct state (`TRADE_TAKEN`, `ARMED_WAITING`, `INVALIDATED`, `EXPIRED`, or `NO_SETUP`).
   - Verify the **Candle Diagnostics & Rules** table correctly displays all 5m candles with OHLC, EMAs, Range %, Wick %, and specific rejection reasons.
2. **Strategy Telemetry Feed (`#watchlist-subview-telemetry`)**:
   - In the **`🔍 Daily Watchlist & Strategy Logs`** console tab, click **`Telemetry`**.
   - Verify that KPI counters (`Total`, `Masters`, `Armed`, `Orders`, `Skips`) are non-zero when events exist today.
   - Verify that events can be filtered by Strategy, Stage (`MASTER`, `ARMED`, `ORDERS`), and Severity (`SUCCESS`, `WARNING`, `DANGER`).
   - Clicking on any **Symbol Pill** (e.g. `KAYNES`) must filter the stream to that stock instantly.
3. **Watchlist Dropdown Action Buttons**:
   - Verify that clicking the clipboard icon next to any stock item in the watchlist opens `openStockAuditModal(symbol, date, strategy)` directly.

---

## 5. Mandatory Integrity Guards

* **IST Server Time Normalization**: Always format dates and timestamps in Indian Standard Time (`Asia/Kolkata` / `+05:30`) using `data.NormalizeToIST(t)`.
* **Broker SL Re-verification**: For every executed trade, confirm that the broker-side SL order ID (`broker_sl_order_id`) in `positions` matches an active order in `orders`.
* **Zero Static Assumptions**: Never make assumptions about missed trades without inspecting actual 5m OHLC database bars and strategy event logs.
