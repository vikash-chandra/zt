---
name: config-check
description: Audit, validate, and test configuration parameter wiring across database, settings, runtime engines, and UI dashboard
---
# Configuration Audit & 8-Point Wiring Skill

Validates and verifies the end-to-end integration and runtime propagation of all configuration parameters across Database, Go Settings structs, Docker environment, In-Memory Strategy Engines, Risk Calculators, and Web Dashboard UI Forms.

---

## 1. The 8-Point Configuration Lifecycle

Whenever a new setting or configuration variable is added or modified in the bot, it MUST be wired across all 8 mandatory integration points:

| Step | Layer / File | Responsibility |
| :--- | :--- | :--- |
| **1** | **Database Default Seeds**<br>([`data/database.go`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/data/database.go)) | Seed default value into `defaultSysConfigs` (for `app_system_configs`) or `defaultOptConfigs` (for `options_index_configs`) for automatic DB migration on boot. |
| **2** | **DB Repository Queries**<br>([`data/queries.go`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/data/queries.go)) | Encapsulate queries (`GetAllSystemConfigs`, `UpsertSystemConfig`, `GetOptionsIndexConfigs`) to read/write settings in PostgreSQL. |
| **3** | **Go Struct Definitions**<br>([`config/settings.go`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/config/settings.go)) | Define field on `Settings`, `OptionsConfig`, or `ScannerConfig` with proper data type. |
| **4** | **Environment Fallbacks**<br>([`config/settings.go`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/config/settings.go)) | Parse fallback environment variables in `Load()` (e.g. `getEnvAsFloat`, `getEnvAsInt`, `getEnvAsSlice`). |
| **5** | **Docker Forwarding**<br>([`docker-compose.yml`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/docker-compose.yml)) | Forward the environment variable under `services.app.environment` with default fallback. |
| **6** | **Backend Sync & Engine Mutation**<br>([`main.go`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/main.go)) | 1. Parse DB config map and mutate `tb.cfg` in `applySystemConfigsToSettings()`.<br>2. Update active in-memory strategy and risk engines in `loadModularStrategyConfigs()`. |
| **7** | **UI Dashboard Controls & Serialization**<br>([`index.html`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/index.html)) | 1. Render input field in `render...Settings()`.<br>2. Read and serialize value in `collect...Settings()` before `POST /api/settings/system`. |
| **8** | **Automated Verification Assertions**<br>([`config_validation_test.go`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/config_validation_test.go) & [`scripts/verify_configs/main.go`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/scripts/verify_configs/main.go)) | Add assert test verifying the parameter is parsed from DB JSON/strings and mutates the Go config and strategy engines correctly. |

---

## 2. Configuration Verification Commands

Run the automated verification suite to audit configuration wiring:

### Run Standalone Configuration Audit Tool
```bash
go run scripts/verify_configs/main.go
```
* Audits all 14 configuration scopes (Equity, 5 Strategies, 2 RR profiles, Manual Trading, 12 Stock Selection strategies, Quant Scanner, System, Options).
* Outputs a clear summary table of wired parameters.

### Run Automated Unit Test Assertions
```bash
go test -v -run "TestAllUIConfigurationsWiredAndApplied|TestOptionsIndexConfigWiring"
```
* Verifies zero type mismatch errors.
* Verifies that JSON serialization and deserialization across all categories match in-memory engines.

---

## 3. Configuration Categories & Key Fields

### 1. `EQUITY_STRATEGY` (Global Equity Risk & Buffers)
- `risk_per_trade_inr`
- `capital_inr`
- `max_open_positions`
- `max_daily_loss_amount`
- `max_trades_per_day`
- `max_holding_time_min`
- `enable_live_trading`
- `default_order_type`
- `limit_buffer_pct`
- `auto_square_off_time`
- `lv_sl_buffer_pct`, `vb_sl_buffer_pct`, `fb_sl_buffer_pct`, `vbt_sl_buffer_pct`, `es5_sl_buffer_pct`

### 2. `TRADING_STRATEGY` (Modular Intraday Strategies)
- **`LOW_VOLUME`**: `enabled`, `candle_time_frame`, `attached_risk_reward`, `attached_stock_selections`, `trade_end_time`, `min_candles_to_ignore`, `sl_buffer_pct`, `use_broker_sl`
- **`VANDE_BHARAT`**: `enabled`, `candle_time_frame`, `attached_risk_reward`, `attached_stock_selections`, `trade_end_time`, `min_candles_to_ignore`, `sl_buffer_pct`, `sl_min_pct`, `sl_max_pct`, `min_gap_pct`, `master_max_pct`, `master_max_wick_pct`, `use_broker_sl`
- **`FAKE_BREAKOUT`**: `enabled`, `candle_time_frame`, `attached_risk_reward`, `attached_stock_selections`, `trade_end_time`, `min_candles_to_ignore`, `gap_up_min_pct`, `gap_up_max_pct`, `gap_down_min_pct`, `gap_down_max_pct`, `max_confirmation_pct`, `master_max_wick_pct`, `sl_buffer_pct`, `use_broker_sl`
- **`VANDE_BHARAT_TRAP`**: `enabled`, `candle_time_frame`, `attached_risk_reward`, `attached_stock_selections`, `trade_end_time`, `min_candles_to_ignore`, `fake_master_max_pct`, `master_max_pct`, `sl_min_pct`, `sl_max_pct`, `master_max_wick_pct`, `sl_buffer_pct`, `use_broker_sl`
- **`EMAS5_BREAKOUT`**: `enabled`, `candle_time_frame`, `attached_risk_reward`, `attached_stock_selections`, `trade_end_time`, `min_candles_to_ignore`, `max_trades_per_stock`, `rally_candles`, `min_rebound_pct`, `master_max_pct`, `master_max_wick_pct`, `max_inside_candles`, `confirm_max_pct`, `ema_touch_buffer_pct`, `sl_buffer_pct`, `max_entry_distance_pct`, `max_setup_wait_candles`, `use_broker_sl`

### 3. `RR_STRATEGY` (Risk-Reward Profiles)
- **`PARTIAL_BOOK_COST_SL`**: `risk_reward_ratio`, `partial_exit_qty_pct`, `move_sl_to_cost`, `cost_sl_buffer_pct`, `initial_sl_mode`, `fixed_sl_pct`, `sl_buffer_pct`
- **`DYNAMIC_TRAILING_SL`**: `stage1_trigger_gain_pct`, `stage1_trail_sl_pct`, `stage2_trigger_gain_pct`, `stage2_trail_sl_pct`, `stage3_trigger_gain_pct`, `stage3_trail_sl_pct`, `stage4_trigger_gain_pct`, `stage4_exit_pct`, `stage4_trail_sl_pct`, `stage5_trigger_gain_pct`, `stage5_step_offset_pct`, `time_decay_min`, `time_decay_trigger_pct`, `time_decay_trail_sl_pct`

### 4. `MANUAL_TRADING`
- `manual_trade_sync_enabled`, `manual_trade_poll_minutes`, `manual_trade_attached_rr_strategy`, `manual_trade_rr_ratio`, `manual_trade_partial_exit_pct`, `manual_trade_default_sl_pct`, `manual_trade_move_sl_to_cost`, `manual_trade_cost_buffer_pct`, `manual_trade_use_broker_sl`

### 5. `OPTIONS_INDEX_CONFIGS` (Per-Index Multipliers & Parameters)
- `index_symbol`, `is_active`, `is_live`, `base_lot_size`, `max_multiplier`, `multiplier_on_reversal`, `target_entry_premium`, `expiry_type`, `next_month_days`, `sl_pct`, `trail_sl_enabled`, `trail_sl_buffer_pct`, `st1_period`, `st1_multiplier`, `st2_period`, `st2_multiplier`, `st3_period`, `st3_multiplier`, `last_new_trade_time`, `auto_square_off_time`, `supertrend_cutoff_time`, `max_trades_per_day`

---

## 4. Troubleshooting & Debugging Checklist

When investigating why a setting change did not take effect:
1. **Did the UI Save write to PostgreSQL?** Check `app_system_configs` or `options_index_configs` in the DB.
2. **Did `applySystemConfigsToSettings` execute?** Verify `loadModularStrategyConfigs()` was invoked upon saving settings or starting up.
3. **Was the Engine Updated?** Verify the strategy struct has the corresponding setter (e.g. `engine.SetSLBufferPct(val)`) and that `loadModularStrategyConfigs()` called it.
4. **Is there an Env Collision?** Check if a variable like `LimitBufferPct` was incorrectly reading `sl_buffer_pct`.
5. **Run the Audit Script**: Execute `go run scripts/verify_configs/main.go` to instantly isolate any missing wiring points.
