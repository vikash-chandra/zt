---
name: ema-s5-breakout
description: Analyze, explain, and backtest EMA S5 Breakout Strategy setups with sequential U-shape, Inverted U-shape, and V-shape geometry
---

# EMA S5 Breakout Strategy (EMAS5_BREAKOUT) Skill & Reference Guide

This skill defines the mechanical rules, geometric market structure, parameter roles, and tuning guidelines for the **EMA S5 Breakout Strategy** across U-Shape, Inverted U-Shape, and V-Shape momentum turns.

---

## 1. Visual Lifecycle Diagram (BUY Setup)

```
[Phase 1: Starting Peak High] (Left Rim of Arc)
           \
            \  <--- rally_candles (>= 5 candles distance; set to 2 for V-shape)
             \      min_pdh_pdl_retrace_pct (>= 0.5% extension above PDH; set 0.0% to disable)
              ▼
[Phase 2: Swing Trough Low] (Bottom of Arc / Reversal Pivot)
              │
              │  <--- min_rebound_pct (>= 0.45% rebound from Trough)
              │  <--- arc_bounce_tolerance_pct (<= 0.30% retest tolerance)
              ▼
[Phase 3: Master Candle] (Right Rim - GREEN)
              │  <--- ema_touch_buffer_pct (within 0.10% of EMA 10/20 or PDH)
              │  <--- master_max_pct (Candle Range <= 2.0%)
              │  <--- master_max_wick_pct (Total Wicks <= 40%)
              │  (Closes strictly above EMA 10, EMA 20, and PDH)
              ▼
[Phase 4: Consolidation & Re-anchoring]
              │  <--- max_inside_candles (<= 1 inside bar)
              │  (If new candle meets Master rules -> Universal Re-anchor)
              ▼
[Phase 5: Confirmation Candle] (MUST be GREEN)
              │  <--- confirm_max_pct (Range <= 1.0%)
              │  <--- confirm_master_multiplier (Size <= 1.5x Master)
              │  (Breaks Master High and closes GREEN above Master Low)
              ▼
[Phase 6: Live Breakout Execution]
                 BUY Trigger: LTP > Confirmation High
                 │  <--- max_entry_distance_pct (LTP <= Confirm High + 0.35%)
                 │  <--- max_setup_wait_candles (Must trigger within 6 candles)
                 │  <--- sl_buffer_pct (SL = Confirmation Low - 0.10%)
                 │  <--- trade_end_time (Hard entry cutoff e.g. 14:30:30 IST)
                 │  <--- max_trades_per_stock (Max 2 trades/stock/day)
```

---

## 2. Geometric Shapes: U-Shape vs. V-Shape Setup Models

```
    CLASSIC U-SHAPE (Rounded Arc)                 SHARP V-SHAPE (Immediate Spike)
    =============================                 ===============================
Peak High                                     Peak High
  \                                             \
   \   (Pullback Phase >= 5 candles)             \   (Quick Drop: 1-2 candles)
    \                                             \
     \_                                            \
       \__                                          ▼
          \____                                 Trough Low (Pivot)
               \                                    /
                ▼                                  /  (Sharp Rebound: 1-2 candles)
          Trough Low                              /
              /                                  ▼
             /  (Right Rim Turnaround)      Master Candle (Closes above EMAs)
            /
           ▼
     Master Candle (Closes above EMAs)
```

### Key Differences Between U-Shape and V-Shape:
* **U-Shape**: Represents institutional absorption over multiple candles (5 to 10 candles). Price rounds out smoothly, retests moving averages, and builds base support.
* **V-Shape**: Represents an aggressive liquidity sweep and instant rejection (1 to 2 candles). Price spikes down to a key level and violently rebounds in the very next candle.

---

## 3. How to Allow V-Shape Trading in EMA S5

To enable the bot to trade sharp V-shape reversals in addition to classic U-shapes, adjust these 3 key parameters in the UI dashboard or PostgreSQL database:

### 1. Lower `rally_candles` from 5 to 2
* **Current U-Shape Requirement**: Requires at least 5 completed candles between the peak high and master candle candidate.
* **V-Shape Adjustment**: Set `rally_candles` to **`2`** (or `1`).
* **Result**: Allows the Master candle to form just 1 to 2 candles after the swing trough, immediately catching V-spikes.

### 2. Set `min_pdh_pdl_retrace_pct` to 0.0%
* **Current U-Shape Requirement**: Requires price to first expand at least 0.50% above PDH before the retrace curve begins.
* **V-Shape Adjustment**: Set `min_pdh_pdl_retrace_pct` to **`0.0%`**.
* **Result**: Removes the requirement for a prior breakout above PDH/PDL, allowing V-bottoms bouncing directly off dynamic EMA 10 or EMA 20 support to qualify.

### 3. Adjust `min_rebound_pct` to 0.35% - 0.40%
* **Current Requirement**: Requires at least 0.45% rebound from the swing bottom.
* **V-Shape Adjustment**: Keep at **`0.35%`** for large-caps or **`0.45%`** for high-beta stocks.
* **Result**: Ensures the V-bounce has sufficient momentum to break moving averages while not demanding an excessively large single bar.

---

## 4. Complete Parameter Master Reference

| Parameter Key | UI Display Name | Default | Phase & Role in Setup Lifecycle | Tuning Guidance |
| :--- | :--- | :---: | :--- | :--- |
| `rally_candles` | Pre-Setup Rally Candles | `5` | Min candles between peak/trough and Master candle | Set to `2` for V-Shapes; `5` to `7` for classical U-Shapes |
| `min_rebound_pct` | Min Bounce from Trough / Drop from Peak (%) | `0.45%` | Min percentage bounce from trough (BUY) or drop from peak (SELL) to Master close | Set to `0.10%` to allow Master candle right at trough pivot; `0.45%` for strong momentum |
| `min_pdh_pdl_retrace_pct` | Min Morning Rally Above PDH / Drop Below PDL (%) | `0.50%` | Requires morning move to push above PDH (BUY) or below PDL (SELL) before pullback | Set to `0.20%` for solid morning expansion; `0.0%` to trade pure EMA bounces anywhere |
| `arc_bounce_tolerance_pct` | Arc Pullback / Bounce Tolerance (%) | `0.30%` | Depth of pullback dip required from peak to trough | Set to `0.20%` - `0.30%` to confirm a real dip occurred |
| `ema_touch_buffer_pct` | Level Touch Buffer (%) | `0.10%` | Proximity threshold for candle extreme to touch EMA 10/20 | Accommodates front-running by algorithmic traders |
| `master_max_pct` | Master Candle Max Range (%) | `2.0%` | Caps total range percentage of Master candle | Keep `<= 1.5%` to control initial stop-loss risk |
| `master_max_wick_pct` | Master Candle Max Wick (%) | `40.0%` | Caps total upper + lower wicks on Master candle | Set to `70% - 85%` if you want to allow hammer pinbars |
| `max_inside_candles` | Max Inside Candles | `1` | Max consolidation bars allowed inside Master range | `1` demands quick momentum; `2 - 3` allows base consolidation |
| `confirm_max_pct` | Confirmation Max Range (%) | `1.0%` | Caps range percentage on Confirmation candle | Prevents entering on exhausted runaway breakout bars |
| `confirm_master_multiplier` | Confirm vs Master Size Multiplier | `1.5x` | Max ratio of Confirmation size to Master size | If `> 1.5x`, bar is promoted to candidate NEW Master candle |
| `max_entry_distance_pct` | Max Entry Distance (%) | `0.35%` | Anti-chasing ceiling above Confirmation High | Discards runaway fills if market gaps beyond this limit |
| `max_setup_wait_candles` | Max Setup Wait (Candles) | `6` | Window to wait for breakout trigger before expiry | `6` candles = 30 min on 5m, 6 min on 1m. Prevents stale entries |
| `sl_buffer_pct` | Strategy SL Buffer (%) | `0.10%` | Buffer added beyond Confirmation Low for stop-loss | Protects against market-maker tick hunting of exact extremes |
| `trade_end_time` | Trade Cutoff Time (IST) | `14:30:30` | Daily clock cutoff after which zero entries occur | Set to `11:00:00` to avoid midday sideways chop |
| `max_trades_per_stock` | Max Trades Per Stock | `2` | Daily execution cap per symbol | Prevents churn and whipsaw in range-bound stocks |
| `candle_time_frame` | Candle Timeframe | `5m` | Aggregation interval (`1m` vs `5m`) | `5m` offers high accuracy; `1m` provides rapid morning scalps |
| `require_ifp_validation` | Require IFP Validation | `false` | Institutional Footprint gatekeeper (10x block flow) | Set to `true` for highest institutional win rate |
