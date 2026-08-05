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
   - 100% Real Live Zerodha NFO Market Quotes (`GetQuote`) enforced for all entries, exits, 50% SL tracking, and P&L accounting (zero static fallbacks)
   - Base Lot Sizing: `OPTIONS_BASE_LOT_SIZE=65` (1x Lot = 65 Qty)
   - Reversal Lot Scaling: 1x Initial (65 Qty) -> 2x Reversal (130 Qty) (Max Cap: `OPTIONS_MAX_QUANTITY_MULTIPLIER=4`)
   - Daily Multiplier Reset: Multipliers reset back to 1x on day change (`ResetDailyMultiplier`)
   - Option Premium Stop-Loss: 50% premium increase (`OPTIONS_SL_PCT=0.50`) checked dynamically every second against real Zerodha market quote
   - Dynamic Holding Time Calculation: Exact duration in minutes persisted to `trades` DB table (`time_held_minutes = int(exitTime.Sub(entryTime).Minutes())`)
   - Intraday EOD Auto Square-Off: `OPTIONS_AUTO_SQUARE_OFF_TIME=15:15` IST
   - Zerodha API Order Protection: Aggressive Limit Orders (5% below LTP for SELL, 5% above LTP for BUY) to prevent market order API rejections
4. Validate configuration:
   - Risk parameters are sensible
   - Stop-loss logic is consistent
   - Position sizing math is correct
5. Report any violations or warnings.
