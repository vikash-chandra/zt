---
name: vande-bharat
description: Analyze, explain, configure, and trade the Vande Bharat Momentum breakout strategy across 1m and 5m timeframes with manual watchlist integration
---

# Vande Bharat Momentum Strategy (VANDE_BHARAT) Skill & Reference Guide

This skill defines the operational mechanics, timeframe configurations, risk anchoring rules, parameter roles, and tuning guidelines for the **Vande Bharat Momentum Strategy** and its Daily Manual Watchlist integration.

---

## 1. Visual Lifecycle Diagram

```
[Phase 1: Market Open at 09:15 IST]
        │
        │  Opening price opens above PDH (BUY) or below PDL (SELL)
        ▼
[Phase 2: Candle 1 - Master Candle (09:15 - 09:20 IST)]
        │  • BUY: Closes GREEN above PDH (Close > PDH)
        │  • SELL: Closes RED below PDL (Close < PDL)
        │  • master_max_pct: Range <= 1.90%
        │  • master_max_wick_pct: Total Wicks <= 75.0%
        │  • stock_max_day_change_pct: Day change <= 1.90%
        ▼
[Phase 3: Candle 2 - SL Anchor & Confirmation (09:20 - 09:25 IST)]
        │  • Rule 1 (Breakout): High > Master.High -> Trigger = Candle 2 High, SL = Candle 2 Low
        │  • Rule 2 (Inside): High <= Master.High -> Trigger = Master High, SL = Candle 2 Low
        │  • confirm_min_pct: Range >= 0.50%
        │  • confirm_max_pct: Range <= 1.00%
        │  • sl_min_pct: SL Risk >= 0.50% (5m) or 0.05% (1m)
        │  • sl_max_pct: SL Risk <= 1.00%
        ▼
[Phase 4: Candle 3+ - Live Breakout Execution]
           BUY Trigger: Live LTP > Trigger Level
           │  • min_candles_to_ignore: Waits N candles before arming trigger
           │  • sl_buffer_pct: Places SL-M order at SL Anchor - 0.10% buffer
           │  • Same-Breakout-Candle Mandate: Must enter on breaching candle or expire
           │  • trade_end_time: Hard entry cutoff (e.g. 11:00:00 IST)
           ▼
[Phase 5: Position & Risk Management]
        Target 1: 1:2 Risk-Reward (or Dynamic Trailing SL)
```

---

## 2. Complete Parameter Master Reference

| Parameter Key | UI Display Name | Default | Phase & Role in Setup Lifecycle | Tuning Guidance |
| :--- | :--- | :---: | :--- | :--- |
| `master_max_pct` | Master Candle Max Range (%) | `1.90%` | Maximum range percentage allowed for Candle 1 | Keep `<= 1.8%` to avoid large risk; raise to `2.5%` for volatile days |
| `master_max_wick_pct` | Master Candle Max Wick (%) | `75.0%` | Maximum allowable upper + lower wick proportion on Candle 1 | `75%` permits bottom rejection tails; lower to `40%` for pure full bodies |
| `confirm_min_pct` | Confirm Candle Min Range (%) | `0.50%` | Minimum range required on Candle 2 to ensure momentum | Lower to `0.20%` in `1m` mode; keep `0.50%` in `5m` mode |
| `confirm_max_pct` | Confirm Candle Max Range (%) | `1.00%` | Maximum range allowed on Candle 2 | Prevents entering after an exhausted Candle 2 run |
| `sl_min_pct` | Min Stop-Loss (%) | `0.50%` | Minimum risk distance required between Entry and SL | Automatically adapts to `0.05%` in `1m` mode |
| `sl_max_pct` | Max Stop-Loss (%) | `1.00%` | Maximum allowable risk distance | Discards trades where SL would exceed 1.0% of stock price |
| `sl_buffer_pct` | SL Buffer (%) | `0.10%` | Buffer subtracted below Candle 2 Low for broker SL order | Protects against market-maker wick spikes at key levels |
| `min_candles_to_ignore`| Min Candles to Ignore | `2` | Initial candles to observe before evaluating triggers | Set to `2` for standard 09:25 AM entry; set to `0` for instant Candle 2 scalp |
| `stock_max_day_change_pct` | Stock Max Day Change (%) | `1.90%` | Maximum percentage gain from previous close allowed at open | Filters gap-up stocks that have already moved too far before entry |
| `trade_end_time` | Trade Cutoff Time (IST) | `11:00:00` | Intraday clock time after which setups expire | 11:00:00 IST restricts trading to high-momentum morning window |
| `candle_time_frame` | Candle Timeframe | `5m` | Aggregation interval (`1m` vs `5m`) | `5m` standard equity; `1m` fast scalp entries |
| `require_ifp_validation`| Require IFP Validation | `true` | Gatekeeper requiring 10x block flow or CVD absorption | Enable for institutional verification; disable for pure price action |
| `use_broker_sl` | Place Broker SL | `true` | Automatically places exchange SL-M order on Kite Connect | Mandatory for automated risk protection |

---

## 3. Timeframe Execution Modes (1m vs 5m)

The engine operates on either 1-minute or 5-minute candles:

| Parameter / Milestone | 1-Minute Mode (1m) | 5-Minute Mode (5m, Default) |
| :--- | :--- | :--- |
| Master Candle (Candle 1) | 09:15:00 - 09:16:00 IST | 09:15:00 - 09:20:00 IST |
| SL Anchor / Confirm (Candle 2) | 09:16:00 - 09:17:00 IST | 09:20:00 - 09:25:00 IST |
| Earliest Execution (Candle 3) | 09:17:01 IST onwards | 09:25:01 IST onwards |
| Dynamic Min SL Adaptation | Automatically lowers to 0.05% | 0.50% standard equity minimum |
| Max SL Cap | 1.00% | 1.00% |
| Trade Cutoff | 11:00:00 IST | 11:00:00 IST |

---

## 4. Daily Manual Watchlist Integration

Stocks selected before 09:15 AM in the Daily Watchlist tab receive these guarantees:
1. **Pre-Market Persistence**: Retained in database and tagged as provenance `MANUAL`.
2. **WebSocket Registration**: Streamed real-time on `RobustKiteTicker`.
3. **Directional Bias Immunity**: Automated screener stocks inherit strict LONG_ONLY or SHORT_ONLY tags, but manual stocks can trade in either direction based purely on price action relative to PDH/PDL.
