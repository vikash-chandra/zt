---
name: vande-bharat-trap
description: Analyze, explain, configure, and trade the Vande Bharat Trap (reverse breakout / liquidity sweep trap) strategy
---

# Vande Bharat Trap Strategy (VANDE_BHARAT_TRAP) Skill & Reference Guide

This skill defines the operational mechanics, reversal trap geometry, parameter roles, and tuning guidelines for the **Vande Bharat Trap Strategy**.

---

## 1. Visual Lifecycle Diagram (BUY Trap Setup)

```
[Phase 1: Candle 1 - Fake Master (09:15 - 09:20 IST)]
        │  Price breaks below PDL and closes RED (trapping early breakout short sellers)
        │  • fake_master_max_pct: Range <= 3.00%
        │  • master_max_wick_pct: Total Wicks <= 75.0%
        ▼
[Phase 2: Candle 2 - Reversal Master Candle (09:20 - 09:25 IST)]
        │  Price rejects lower prices, sharply reverses, and surges back ABOVE PDL
        │  • Closes GREEN inside/above the previous day's range
        │  • master_max_pct: Range <= 1.80%
        │  • master_max_wick_pct: Total Wicks <= 75.0%
        ▼
[Phase 3: Candle 3 - Confirmation Candle (09:25 - 09:30 IST)]
        │  Confirms bullish reversal by breaking and closing above Candle 2 High
        │  • sl_min_pct: Minimum risk distance >= 0.50%
        │  • sl_max_pct: Maximum risk distance <= 1.00%
        │  • Trigger Level = Candle 3 High
        │  • SL Anchor = Candle 2 Low
        ▼
[Phase 4: Live Trap Breakout Execution]
           BUY Trigger: Live LTP > Candle 3 High
           │  • sl_buffer_pct: Places SL-M order at Candle 2 Low - 0.10% buffer
           │  • trade_end_time: Hard entry cutoff (e.g. 11:00:00 IST)
           ▼
[Phase 5: Position & Risk Management]
        Target 1: 1:2 Risk-Reward (or Dynamic Trailing SL)
```

---

## 2. Parameter Master Reference

| Parameter Key | UI Display Name | Default | Phase & Role in Setup Lifecycle | Tuning Guidance |
| :--- | :--- | :---: | :--- | :--- |
| `fake_master_max_pct` | Fake Master Max Range (%) | `3.00%` | Maximum range percentage allowed on Candle 1 (the trap bar) | Caps oversized opening panics; keep `<= 3.0%` |
| `master_max_pct` | Real Master Max Range (%) | `1.80%` | Maximum range percentage allowed on Candle 2 (the reversal bar) | Limits risk size on the reversal candle |
| `master_max_wick_pct` | Master Candle Max Wick (%) | `75.0%` | Maximum allowable upper + lower wicks on reversal bars | `75%` accommodates long rejection tails (spring wicks) |
| `sl_min_pct` | Min Stop-Loss (%) | `0.50%` | Minimum distance between entry and SL | Prevents ultra-tight stops prone to noise |
| `sl_max_pct` | Max Stop-Loss (%) | `1.00%` | Maximum risk distance allowed | Discards setups with oversized SL risk |
| `sl_buffer_pct` | SL Buffer (%) | `0.10%` | Buffer added beyond the sweep pivot for broker SL | Protects against secondary retest wicks |
| `trade_end_time` | Trade Cutoff Time (IST) | `11:00:00` | Intraday clock time after which setups expire | Traps occur early; 11:00 AM cutoff avoids late chop |
| `candle_time_frame` | Candle Timeframe | `5m` | Aggregation interval (`5m` recommended) | `5m` allows time for the trap to spring cleanly |
| `require_ifp_validation` | Require IFP Validation | `true` | Gatekeeper requiring institutional CVD absorption flow | High win-rate filter for confirming institutional traps |
| `use_broker_sl` | Place Broker SL | `true` | Automatically places broker SL-M order on Kite Connect | Mandatory exchange risk protection |
