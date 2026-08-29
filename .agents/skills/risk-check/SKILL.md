---
name: risk-check
description: Validate risk parameters and check current exposure
---
# Risk Check Skill

Validates intraday risk exposure, capital limits, stop-loss synchronization, and strategy invalidation rules across Equity and Options trading engines.

## 1. Equity Risk Framework

| Risk Parameter | Default Value | Purpose |
| :--- | :--- | :--- |
| `CAPITAL_INR` | `₹1,00,000` | Account baseline allocated capital |
| `RISK_PER_TRADE_INR` | `₹500` / `₹5,000` | Maximum currency loss allocated per single trade |
| `MAX_OPEN_POSITIONS` | `3` (Equity) / `2` (Bot) | Maximum concurrent open positions |
| `MAX_DAILY_LOSS_AMOUNT` | `₹10,000` | Daily circuit breaker stop trading limit |
| `MAX_TRADES_PER_DAY` | `20` | Max order executions allowed in a single session |
| `AUTO_SQUARE_OFF_TIME` | `15:20:00 IST` | Intraday MIS mandatory square-off time |

### High-Water Mark Trailing Stop-Loss Stages
1. **Stage 1 ($\ge +0.8\%$ gain)**: SL trails to $+0.2\%$ (No-loss buffer).
2. **Stage 2 ($\ge +1.4\%$ gain)**: SL trails to $+0.7\%$ (Locks early profits).
3. **Stage 3 ($\ge +2.0\%$ gain / Target 1)**: Exits 60% partial quantity and trails remaining SL to $+1.0\%$.
4. **Stage 4 ($\ge +2.5\%$ gain)**: Dynamic step-trailing at $(\text{Peak High} - 1.0\%)$.
5. **Time Decay Guard**: Positions held $> 45\text{ mins}$ with $\ge +0.4\%$ gain automatically trail SL to $+0.2\%$.

### Equity Strategy Invalidation Guards
- **Strategy Session History Integrity**: All stock strategies (`LOW_VOLUME`, `VANDE_BHARAT`) MUST anchor `firstCandles` strictly to the `09:15 AM IST` candle. If missing, trade triggers are strictly blocked.
- **Low Volume (Option A)**: Lowest volume 5m candle since 09:15 AM is Setup. Breakout valid **only on the immediate next 5m candle**.
- **Vande Bharat 5 Rules**:
  1. 1st Candle (09:15 AM) $\ge 2\%$ gap from Yesterday's Close (`(Open - PrevClose)/PrevClose * 100 >= 2%`).
  2. 2nd Candle (09:20 AM) SL Range Control in $[0.5\%, 1.0\%]$ (`(High - Low)/Close * 100`).
  3. Intermediate consolidation strictly within `[Master.Low, Master.High]`.
  4. Any-color confirmation candle breaking Day High (BUY) / Day Low (SELL).
  5. Entry day move from PDH/PDL $\le 1.8\%$ at live trigger time.

---

## 2. Options Trading Risk Framework (`OPTIONS_SUPERTREND`)

| Risk Parameter | Default Value | Purpose |
| :--- | :--- | :--- |
| `sl_pct` | `50.0%` | Initial Stop-Loss premium increase limit |
| `trail_sl_enabled` | `true` | Enables monotonic candle-close trailing SL |
| `trail_sl_pct` | `20.0%` | 5-minute candle-close trailing buffer |
| `multiplier_on_reversal` | `true` | Multiplier lot scaling on trend reversal (1x $\rightarrow$ 2x $\rightarrow$ 3x) |
| `max_multiplier` | `4` | Maximum reversal multiplier cap |
| `last_new_trade_time` | `14:32:00 IST` | No new entries allowed after this time |
| `auto_square_off_time` | `15:13:00 IST` | Intraday mandatory option square-off |

### Trailing Stop-Loss & Tick Breach Mechanics
- **Monotonic 20% Downward Ratchet**: On 5m candle close, $\text{Candidate SL} = \text{CurrentPremium} \times 1.20$.
  - SL tightens if candidate < current SL; remains strictly constant on bounces (never increases).
- **1-Second Real-Time Tick SL Breach**: Continuous monitoring via `CheckTick(ltp)`. If $\text{Tick LTP} \ge \text{SLPrice}$, executes market exit instantly and resets multiplier to 1.
- **100% Real Zerodha NFO Market Quotes**: Live `GetQuote` quotes enforced for all entries, exits, SL tracking, and P&L calculations (zero static assumptions).

---

## 3. System & Infrastructure Safety
- **Market Hours Bot Restart Lock**: Restart (`POST /api/system/restart`) allowed **ONLY** before `09:15 AM` and after `15:45 PM IST`. Forbidden (HTTP 403) during market hours.
- **Aggressive Limit Orders**: Live option orders submit aggressive limit orders (5% buffer) to avoid Zerodha market order rejections.
- **Centralized Time Normalization**: All timestamps and logs pass through `data.NormalizeToIST(t)` (`Asia/Kolkata`).
