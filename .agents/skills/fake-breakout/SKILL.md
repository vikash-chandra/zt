---
name: fake-breakout
description: Analyze, explain, configure, and trade the Fake Breakout (exhausted opening gap trap) strategy on 5m candles
---

# Fake Breakout Strategy (FAKE_BREAKOUT) Skill & Reference Guide

This skill defines the operational mechanics, opening gap exhaustion rules, parameter roles, and tuning guidelines for the **Fake Breakout Strategy**.

---

## 1. Visual Lifecycle Diagram (Gap-Up Short Trap Setup)

```
[Phase 1: Market Open - Excessive Opening Gap]
        │  Stock opens with an aggressive gap-up compared to previous close:
        │  • gap_up_min_pct: Gap >= 4.0%
        │  • gap_up_max_pct: Gap <= 8.0%
        ▼
[Phase 2: Candle 1 - Counter-Color Exhaustion Bar (09:15 - 09:20 IST)]
        │  Instead of continuation, buyers are exhausted. Candle 1 closes RED:
        │  • Close < Open (RED candle on a massive gap-up)
        │  • master_max_wick_pct: Total Wicks <= 60.0%
        ▼
[Phase 3: Candle 2 - Breakdown Confirmation (09:20 - 09:25 IST)]
        │  • Breaks below Candle 1 Low (Low < Candle 1 Low)
        │  • max_confirmation_pct: Range <= 1.0%
        │  • Trigger Level = Candle 2 Low
        │  • SL Anchor = Candle 1 High
        ▼
[Phase 4: Live Breakdown Trigger & Execution]
           SELL Trigger: Live LTP < Candle 2 Low
           │  • sl_buffer_pct: Places SL at Candle 1 High + 20.0% buffer
           │  • trade_end_time: Hard entry cutoff (e.g. 09:45:00 IST)
           ▼
[Phase 5: Position & Risk Management]
        Target 1: 1:2 Risk-Reward (or Dynamic Trailing SL)
```

---

## 2. Parameter Master Reference

| Parameter Key | UI Display Name | Default | Phase & Role in Setup Lifecycle | Tuning Guidance |
| :--- | :--- | :---: | :--- | :--- |
| `gap_up_min_pct` | Gap Up Min (%) | `4.0%` | Minimum opening gap percentage to trigger gap exhaustion logic | Ensures the gap is sufficiently large to attract profit taking |
| `gap_up_max_pct` | Gap Up Max (%) | `8.0%` | Maximum allowable opening gap percentage | Caps out freak circuit-breaker stocks (e.g. > 10% upper circuit) |
| `gap_down_min_pct` | Gap Down Min (%) | `1.0%` | Minimum opening gap down percentage for gap-down bounce traps | Filters minor overnight variations |
| `gap_down_max_pct` | Gap Down Max (%) | `3.0%` | Maximum opening gap down percentage | Limits excessive panic gaps |
| `max_confirmation_pct` | Max Confirmation Range (%) | `1.0%` | Maximum range percentage on Candle 2 | Prevents entering when Candle 2 has already made a massive extension |
| `master_max_wick_pct` | Master Candle Max Wick (%) | `60.0%` | Maximum total wicks on Candle 1 | Ensures strong counter-directional body closing opposite the gap |
| `sl_buffer_pct` | Strategy SL Buffer (%) | `20.0%` | Percentage buffer placed beyond the opening high/low for broker SL | Gives buffer on large opening gap candles |
| `trade_end_time` | Trade Cutoff Time (IST) | `09:45:00` | Intraday clock time after which setups expire | Gap exhaustion moves resolve fast; 09:45 cutoff avoids afternoon traps |
| `candle_time_frame` | Candle Timeframe | `5m` | Aggregation interval (`5m`) | 5m candles provide true opening bar closure |
| `require_ifp_validation` | Require IFP Validation | `true` | Requires institutional order flow confirmation | Validates that large players are aggressively dumping into the gap |
| `use_broker_sl` | Place Broker SL | `true` | Automatically places broker SL-M order on Kite Connect | Mandatory exchange risk protection |
