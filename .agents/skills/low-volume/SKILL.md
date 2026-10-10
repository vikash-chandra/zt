---
name: low-volume
description: Analyze, explain, configure, and trade the Low Volume Pullback scalp strategy on 5m candles
---

# Low Volume Scalp Strategy (LOW_VOLUME) Skill & Reference Guide

This skill defines the operational mechanics, volume compression filters, parameter roles, and tuning guidelines for the **Low Volume Scalp Strategy**.

---

## 1. Visual Lifecycle Diagram (BUY Setup)

```
[Phase 1: Market Directional Breadth (09:24 IST)]
        │  Nifty 50 Advance / Decline Breadth:
        │  • Advances > Declines -> BUY_ONLY Bias
        │  • Declines > Advances -> SELL_ONLY Bias
        ▼
[Phase 2: Candle 1 - Opening Range Qualification (09:15 - 09:20 IST)]
        │  • BUY_ONLY: Candle 1 closes ABOVE PDH
        │  • SELL_ONLY: Candle 1 closes BELOW PDL
        ▼
[Phase 3: Observation Baseline (min_candles_to_ignore = 3)]
        │  Waits until 09:30 AM to establish clean volume moving averages
        ▼
[Phase 4: Setup Candle - Lowest Volume Compression Bar]
        │  • Candle with lowest volume traded since 09:15 AM
        │  • Must be the immediately preceding completed candle
        │  • Counter-Color Requirement:
        │    - BUY setup: Setup candle must be RED (healthy low-volume pullback)
        │    - SELL setup: Setup candle must be GREEN (healthy low-volume counter-bounce)
        ▼
[Phase 5: Live Trigger & Execution]
           BUY Trigger: Live LTP > Setup Candle High
           │  • sl_buffer_pct: Places SL at Setup Candle Low - 15.0% buffer
           │  • trade_end_time: Hard entry cutoff (e.g. 10:00:00 IST)
           ▼
[Phase 6: Position & Risk Management]
        Target 1: 1:2 Risk-Reward (or Dynamic Trailing SL)
```

---

## 2. Parameter Master Reference

| Parameter Key | UI Display Name | Default | Phase & Role in Setup Lifecycle | Tuning Guidance |
| :--- | :--- | :---: | :--- | :--- |
| `min_candles_to_ignore` | Min Candles to Ignore | `3` | Wait time (in completed 5m candles) before scanning for lowest volume | 3 candles = 09:30 AM IST. Establishes true morning volume baseline |
| `sl_buffer_pct` | Strategy SL Buffer (%) | `15.0%` | Buffer percentage of candle range placed beyond Setup Candle extreme | Gives room on low-volume micro candles where range is very small |
| `trade_end_time` | Trade Cutoff Time (IST) | `10:00:00` | Intraday clock time after which setups expire | Low-volume pullbacks work best in early momentum (09:30 - 10:00 AM) |
| `candle_time_frame` | Candle Timeframe | `5m` | Aggregation interval (`5m`) | 5m volume profiles eliminate single-tick 1m noise |
| `require_ifp_validation` | Require IFP Validation | `true` | Requires institutional order flow confirmation | Prevents entering false low-volume dead stocks |
| `use_broker_sl` | Place Broker SL | `true` | Automatically places broker SL-M order on Kite Connect | Mandatory exchange risk protection |
