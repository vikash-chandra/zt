---
name: expected-move
description: Calculate Intraday Expected Move, VIX Range, Market Maker ATM Straddle Bounds, and Black-Scholes Option Delta Sensitivity
---
# Intraday Expected Move & Option Delta Sensitivity Skill

Calculates mathematical expected range targets and option contract premium sensitivity strictly from live Zerodha quotes or PostgreSQL database historical candles (`candles_5m`).

## Key Mathematical Models

1. **India VIX Daily Volatility Range**:
   - Daily Percentage Move: `DailyPct = (VIX / sqrt(365.0)) / 100.0`
   - Daily Points Move: `DailyPoints = Spot * DailyPct`
   - Intraday Time Decay Target: `RemainingPoints = DailyPoints * sqrt(HoursRemaining / 6.25)`
   - VIX Upper Bound: `Spot + DailyPoints`
   - VIX Lower Bound: `Spot - DailyPoints`

2. **ATM Straddle Market Maker Bounds**:
   - ATM Strike: `round(Spot / 50.0) * 50.0`
   - Expected Market Move: `ExpectedPoints = 0.85 * ATM_Straddle_Price`
   - Straddle Upper Bound: `Spot + ExpectedPoints`
   - Straddle Lower Bound: `Spot - ExpectedPoints`

3. **Option Contract Sensitivity (Black-Scholes Delta & Theta)**:
   - Delta (`Δ`): Calculated dynamically based on strike moneyness (`0.50` for ATM, `0.12` to `0.88` for OTM/ITM).
   - Theta (`Θ`): Time-decay loss per hour `-(LTP * 0.04) / HoursRemaining`.
   - Premium Projections: Projected contract LTP for `+50 Pts`, `+100 Pts`, `-50 Pts`, and `-100 Pts` index shifts.

## Implementation Architecture
- **Pure Engine**: `data/expected_move.go` (`CalculateExpectedMove` pure domain function).
- **API Endpoint**: GET `/api/options/expected-move` registered in `main.go` and handled by `tb.handleOptionsExpectedMove` in `handlers.go`.
- **UI View**: Dedicated dashboard tab `🎯 Expected Move` (`#console-expected-move-content` pane in `index.html`).

## Strict Rules
- **No Static Price Assumptions**: All prices (Spot, VIX, ATM Straddle CE+PE, Option LTP) MUST be fetched dynamically from Zerodha API (`tb.kiteClient.GetQuote`) or PostgreSQL database table `candles_5m`.
- **IST Time Normalization**: Server timestamp calculation MUST anchor wall-clock time using `data.NormalizeToIST(now)`.
