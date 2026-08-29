---
name: expected-move
description: Calculate Intraday Expected Move, VIX Range, Market Maker ATM Straddle Bounds, and Black-Scholes Option Delta Sensitivity
---
# Intraday Expected Move & Option Delta Sensitivity Skill

Calculates mathematical expected range targets and option contract premium sensitivity strictly from live Zerodha market quotes (`GetQuote`) or PostgreSQL database historical candles (`candles_5m`).

## Mathematical Models & Formulations

### 1. India VIX Daily Volatility Range
- **Daily Volatility %**:
  $$\text{DailyPct} = \frac{\text{VIX}}{\sqrt{365}} \times \frac{1}{100}$$
- **Full-Day Points Range**:
  $$\text{DailyPoints} = \text{Spot} \times \text{DailyPct}$$
- **Intraday Time-Decay Target**:
  $$\text{RemainingPoints} = \text{DailyPoints} \times \sqrt{\frac{\text{HoursRemaining}}{6.25}}$$
- **VIX Intraday Range**: $[\text{Spot} - \text{DailyPoints}, \ \text{Spot} + \text{DailyPoints}]$

### 2. ATM Straddle Market Maker Bounds
- **ATM Strike**: $\text{ATM} = \text{Round}\left(\frac{\text{Spot}}{50}\right) \times 50$
- **Expected Market Move**:
  $$\text{ExpectedPoints} = 0.85 \times (\text{ATM CE LTP} + \text{ATM PE LTP})$$
- **Straddle Bounds**: $[\text{Spot} - \text{ExpectedPoints}, \ \text{Spot} + \text{ExpectedPoints}]$

### 3. Option Contract Sensitivity (Black-Scholes Delta $\Delta$ & Theta $\Theta$)
- **Delta ($\Delta$)**: Computed dynamically from strike moneyness ($0.50$ ATM, $0.12 - 0.88$ for OTM/ITM).
- **Theta ($\Theta$)**: Time-decay erosion per hour:
  $$\Theta = -\frac{\text{LTP} \times 0.04}{\text{HoursRemaining}}$$
- **Projected Option Premiums**: Contract LTP projections for $+50\text{ Pts}$, $+100\text{ Pts}$, $-50\text{ Pts}$, and $-100\text{ Pts}$ index moves.

---

## Implementation Architecture
- **Pure Calculation Engine**: [`data/expected_move.go`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/data/expected_move.go) (`CalculateExpectedMove` pure domain function).
- **API Endpoint**: `GET /api/options/expected-move` (handled by `tb.handleOptionsExpectedMove` in [`handlers.go`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/handlers.go)).
- **Dedicated UI Tab**: Dashboard tab **`🎯 Expected Move`** (`#console-expected-move-content` in [`index.html`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/index.html)).

---

## Strict Data & Time Invariants
- **Zero Static Price Assumptions**: Spot, India VIX, ATM Straddle quotes, and Option LTP MUST be fetched dynamically from Zerodha API (`tb.kiteClient.GetQuote`) or PostgreSQL database table `candles_5m`.
- **IST Time Normalization**: Server timestamp calculations MUST anchor wall-clock time using `data.NormalizeToIST(now)`.
