---
name: risk-check
description: Validate risk parameters and check current exposure
---
# Risk Check Skill

Validates intraday risk exposure, capital limits, stop-loss synchronization, and strategy invalidation rules across Equity and Options trading engines.

## 1. Equity Risk Framework

| Risk Parameter | Default Value | Purpose |
| :--- | :--- | :--- |
| `CAPITAL_INR` | `₹1,00,000` | Account baseline allocated capital pool |
| `RISK_PER_TRADE_INR` | `₹500.0` | Maximum currency loss allocated per single trade |
| `LV_CANDLE_TIMEFRAME` | `5m` (1m / 5m) | Configurable candle timeframe for Low Volume Breakout |
| `VB_CANDLE_TIMEFRAME` | `1m` (1m / 5m) | Configurable candle timeframe for Vande Bharat Momentum |
| `FB_CANDLE_TIMEFRAME` | `1m` (1m / 5m) | Configurable candle timeframe for Fake Breakout Trap Reversal |
| `VBT_CANDLE_TIMEFRAME` | `1m` (1m / 5m) | Configurable candle timeframe for Vande Bharat Trap |
| `ES5_CANDLE_TIMEFRAME` | `1m` (1m / 5m) | Configurable candle timeframe for EMA S5 Breakout |
| `MAX_OPEN_POSITIONS` | `3` | Maximum concurrent open positions active simultaneously in Equity |
| `MAX_DAILY_LOSS_AMOUNT` | `₹10,000` | Daily circuit breaker stop trading limit |
| `MAX_TRADES_PER_DAY` | `20` | Max Equity order executions allowed in a single session (Options excluded) |
| `AUTO_SQUARE_OFF_TIME` | `15:20:00 IST` | Intraday MIS mandatory square-off time |

### Risk Per Trade Position Sizing Formula
$$\text{Quantity} = \min\left( \left\lfloor \frac{\text{Risk Per Trade}}{R_{\text{distance}}} \right\rfloor, \left\lfloor \frac{\text{Capital Pool}}{\text{Margin Per Share}} \right\rfloor \right)$$
* **$R_{\text{distance}}$**: Per-share risk distance $|P_{\text{entry}} - P_{\text{SL}}|$ (derived from setup/2nd candle bounds and SL buffer).
* **$\text{Margin Per Share}$**: $P_{\text{entry}} / \text{Leverage}$ (5x for MIS intraday).
* **$\text{Max Loss}$**: $\text{Quantity} \times R_{\text{distance}} \le \text{Risk Per Trade}$ (strictly guarantees loss does not exceed user-configured risk).

### High-Water Mark Trailing Stop-Loss Stages
1. **Stage 1 ($\ge +0.8\%$ gain)**: SL trails to $+0.2\%$ (No-loss buffer).
2. **Stage 2 ($\ge +1.4\%$ gain)**: SL trails to $+0.7\%$ (Locks early profits).
3. **Stage 3 ($\ge +2.0\%$ gain / Target 1)**: Exits 60% partial quantity and trails remaining SL to $+1.0\%$.
4. **Stage 4 ($\ge +2.5\%$ gain)**: Dynamic step-trailing at $(\text{Peak High} - 1.0\%)$.
5. **Time Decay Guard**: Positions held $> 45\text{ mins}$ with $\ge +0.4\%$ gain automatically trail SL to $+0.2\%$.

### Equity Strategy Invalidation & Uniform Cutoff Guards
- **Strict Uniform Trade Cutoff Times**: All stocks (whether automated scanner picks or manual watchlist items) strictly obey their strategy's configured Trade Cutoff Time (`LVTradeEndTime` 10:45, `VBTradeEndTime` 11:00, `FBTradeEndTime` 11:00, `VBTTradeEndTime` 11:00, `ES5TradeEndTime` 11:00 IST). Zero manual bypass is permitted.
- **Configurable Timeframes**: Low Volume (`5m` default), Vande Bharat (`1m` default), Fake Breakout (`1m` default), Vande Bharat Trap (`1m` default), and EMA S5 Breakout (`1m` default) route candles from matching aggregators.
- **Strategy Session History Integrity**: All stock strategies (`LOW_VOLUME`, `VANDE_BHARAT`, `FAKE_BREAKOUT`, `VANDE_BHARAT_TRAP`, `EMAS5_BREAKOUT`) maintain history integrity.
- **Low Volume (Option A)**: Lowest volume candle since 09:15 AM is Setup. Breakout valid **only on the immediate next candle** before `10:45:00 IST`.
- **Vande Bharat 5 Rules**:
  1. 1st Master Candle (09:15 AM) $\ge 2\%$ gap from Yesterday's Close (`(Open - PrevClose)/PrevClose * 100 >= 2%`).
  2. 2nd Candle (09:16 AM for 1m / 09:20 AM for 5m) SL Range Control in $[0.5\%, 1.0\%]$ (`(High - Low)/Close * 100`).
  3. Intermediate consolidation strictly within `[Master.Low, Master.High]`.
  4. Any-color confirmation candle breaking Day High (BUY) / Day Low (SELL).
  5. Entry day move from PDH/PDL $\le 1.8\%$ at live trigger time before `11:00:00 IST`.
- **Fake Breakout Trap Rules**:
  1. Opening gap between $4.0\%$ and $8.0\%$ (Gap Up for SELL, Gap Down for BUY).
  2. Master Candle (09:15 AM): RED for SELL, GREEN for BUY, wicks $\le 40\%$.
  3. Confirmation Candle (2nd Candle): RED for SELL, GREEN for BUY, breaks Master extreme, range $\le 1.0\%$.
  4. Trade execution allowed starting from 3rd candle onward until `FBTradeEndTime` (11:00:00 IST). Stop-Loss fixed at 2nd Candle High (SELL) or Low (BUY).
- **Vande Bharat Trap Rules**:
  1. 1st Fake Master Candle (09:15 AM): Closes above PDH with RED body for BUY, or below PDL with GREEN body for SELL. Range $\le 3.0\%$.
  2. Master Candle Formation: Subsequent candle breaking Fake Master High (BUY) or Low (SELL) forms Master candle (Range $\le 1.8\%$, Wicks $\le 40\%$).
  3. 2nd Candle SL Anchor: Immediate next candle range in $[0.5\%, 1.0\%]$. Low (BUY) or High (SELL) locked as SL.
  4. Intermediate consolidation inside Master range and any-color confirmation candle breaking Day High/Low.
  5. Live breakout before `VBTTradeEndTime` (11:00:00 IST) with move from PDH/PDL $\le 1.8\%$.
- **EMA S5 Breakout Rules**:
  1. 100-candle rolling buffer for smooth EMA 10 & EMA 20 computation.
  2. Rally sequence $\ge 5$ continuous candles forming U-Shape (BUY) or Inverted U-Shape (SELL).
  3. Upward/downward oval curve move $\ge 0.5\%$ from sequence extreme to Master close.
  4. Master Candle touches EMA 10/20 zone, closes beyond all 3 levels, range $\le 2.0\%$, wicks $\le 72\%$.
  5. Breaching Master Low (BUY) or Master High (SELL) immediately invalidates setup.
  6. Max 1 inside candle allowed before Confirmation candle (breaks Master extreme, range $\le 1.0\%$, strict color match).
  7. Live breakout before `ES5TradeEndTime` (11:00:00 IST), SL anchored at Confirmation Low/High, max 2 trades per stock per day.

---

## 2. Options Trading Risk Framework (`OPTIONS_SUPERTREND`)

| Risk Parameter | Default Value | Purpose |
| :--- | :--- | :--- |
| `max_trades_per_day` | `10` | Independent daily options trade limit per index (decoupled from Equity) |
| `sl_pct` | `50.0%` | Initial Stop-Loss premium increase limit |
| `trail_sl_enabled` | `true` | Enables monotonic candle-close trailing SL |
| `trail_sl_buffer_pct` | `5.0%` | 5-minute candle-close option price SuperTrend buffer |
| `multiplier_on_reversal` | `true` | Multiplier lot scaling on trend reversal (1x $\rightarrow$ 2x $\rightarrow$ 3x) |
| `max_multiplier` | `4` | Maximum reversal multiplier cap |
| `last_new_trade_time` | `14:30:00 IST` | No new entries allowed after this time |
| `auto_square_off_time` | `15:15:00 IST` | Intraday mandatory option square-off |

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
