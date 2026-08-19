---
name: risk-check
description: Validate risk parameters and check current exposure
---
# Risk Check Skill

Analyzes current risk exposure and validates trading parameters across equity and options strategies.

## Usage
- Ask the agent to check risk or validate risk parameters.

## Implementation Steps for Agent
1. Compare current positions vs risk limits.
2. Check Equity Risk Controls:
   - Daily P&L vs max loss threshold (`MAX_DAILY_LOSS_AMOUNT`)
   - Position concentration per symbol
   - Total exposure vs capital
   - Open trade count vs daily limit
   - High-Water Mark Trailing SL stages (+0.8% -> +0.2%, +1.4% -> +0.7%, +2.0% -> +1.0% & 60% partial exit, >+2.5% -> peak-1.0%)
    - 45-minute time decay profit lock (+0.4% gain held > 45m -> +0.2% locked)
3. Check Options Trading Risk Controls (`OPTIONS_SUPERTREND`):
   - 100% Real Live Zerodha NFO Market Quotes (`GetQuote`) enforced for all entries, exits, SL tracking, and P&L accounting (zero static fallbacks)
   - Base Lot Sizing: `OPTIONS_BASE_LOT_SIZE=65` (1x Lot = 65 Qty)
   - Reversal Lot Scaling: 1x Initial (65 Qty) -> 2x Reversal (130 Qty) (Max Cap: `MAX_QUANTITY_MULTIPLIER=3`)
   - Daily Multiplier & State Reset: Multipliers and trend state reset back to 1x and NEUTRAL on day change (`ResetDailyState`). Initial trade entry strictly requires a completed 5m candle close flip above/below SuperTrend on current session (no carried-over 09:15 AM market open trades)
   - Initial Stop-Loss: 50% premium increase (`OPTIONS_SL_PCT=50.0`)
   - 20% 5-Minute Candle Close Trailing Stop-Loss: Candidate SL = `CurrentPremium * 1.20` (`OPTIONS_TRAIL_SL_PCT=20.0`). Monotonically ratchets down when premium decays; remains strictly constant on adverse bounces
   - Real-Time Tick Breach: 1-second ticks checked continuously against `SLPrice` via `CheckTick(ltp)`
   - Dynamic Holding Time Calculation: Exact duration in minutes persisted to `trades` DB table (`time_held_minutes = int(exitTime.Sub(entryTime).Minutes())`)
   - Intraday EOD Auto Square-Off: `OPTIONS_AUTO_SQUARE_OFF_TIME=15:14` IST
   - Zerodha API Order Protection: Aggressive Limit Orders (5% below LTP for SELL, 5% above LTP for BUY) to prevent market order API rejections
4. Validate configuration:
   - Risk parameters are sensible
   - Stop-loss logic is consistent
   - Position sizing math is correct
5. Mandatory Centralized Time & Performance Architecture:
   - Centralized Time: All timestamps, trade logs, and candle times MUST use `data.NormalizeToIST(t)` or `data.*` time utilities from `data/time_utils.go` (`window.ISTTime` on frontend).
   - High Performance: $O(N)$ linear pass indicator calculations, LightweightCharts DOM canvas reuse, and $O(1)$ map lookups.
   - Run empirical runtime verification (query API endpoints or DB) after any code edit to validate zero 5.5-hour timezone shifts before declaring completion.
