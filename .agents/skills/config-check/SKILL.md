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
| **1** | **Database Default Seeds**<br>([`data/database.go`](data/database.go)) | Seed default value into `defaultSysConfigs` (for `app_system_configs`) or `defaultOptConfigs` (for `options_index_configs`) for automatic DB migration on boot. |
| **2** | **DB Repository Queries**<br>([`data/queries.go`](data/queries.go)) | Encapsulate queries (`GetAllSystemConfigs`, `UpsertSystemConfig`, `GetOptionsIndexConfigs`) to read/write settings in PostgreSQL. |
| **3** | **Go Struct Definitions**<br>([`config/settings.go`](config/settings.go)) | Define field on `Settings`, `OptionsConfig`, or `ScannerConfig` with proper data type. |
| **4** | **Environment Fallbacks**<br>([`config/settings.go`](config/settings.go)) | Parse fallback environment variables in `Load()` (e.g. `getEnvAsFloat`, `getEnvAsInt`, `getEnvAsSlice`). |
| **5** | **Docker Forwarding**<br>([`docker-compose.yml`](docker-compose.yml)) | Forward the environment variable under `services.app.environment` with default fallback. |
| **6** | **Backend Sync & Engine Mutation**<br>([`main.go`](main.go)) | 1. Parse DB config map and mutate `tb.cfg` in `applySystemConfigsToSettings()`.<br>2. Update active in-memory strategy and risk engines in `loadModularStrategyConfigs()`. |
| **7** | **UI Dashboard Controls & Serialization**<br>([`index.html`](index.html)) | 1. Render input field in `render...Settings()`.<br>2. Read and serialize value in `collect...Settings()` before `POST /api/settings/system`. |
| **8** | **Automated Verification Assertions**<br>([`config_validation_test.go`](config_validation_test.go) & [`scripts/verify_configs/main.go`](scripts/verify_configs/main.go)) | Add assert test verifying the parameter is parsed from DB JSON/strings and mutates the Go config and strategy engines correctly. |

---

## 2. Configuration Verification Commands

Run the automated verification suite to audit configuration wiring:

### Run Standalone Configuration Audit Tool (Local / Synthetic)
```bash
go run scripts/verify_configs/main.go
```
* Audits all 14 configuration scopes (Equity, 5 Strategies, 2 RR profiles, Manual Trading, 12 Stock Selection strategies, Quant Scanner, System, Options).
* Outputs a clear summary table of wired parameters.

### Run Live Post-Deployment Verification Tool (AWS / Localhost)
```bash
go run scripts/verify_configs/main.go --live http://3.7.29.3:8080
```
* **Phase 1 (Passive Runtime Audit)**: Queries `GET /api/config/runtime-audit` to assert that 100% of database configuration rows match the in-memory engine states with zero slippage.
* **Phase 2 (Active Mutation Test)**: Mutates a live setting via `POST /api/config/save`, validates instant in-memory propagation across running engines without requiring a restart, and cleanly rolls back to original values.
* **Phase 3 (Calculation Integrity Test)**: Asserts that calculations execute cleanly with zero hardcoded overrides.

### Run Automated Unit Test & Concurrency Assertions
```bash
$env:GOMEMLIMIT="512MiB"; go test -v -run "TestConfigE2EWiring|TestConfigConcurrencySafety|TestAllUIConfigurationsWiredAndApplied|TestOptionsIndexConfigWiring" .
```
* `TestConfigE2EWiring`: Audits all 70 parameters for `PERFECT_SYNC` and verifies intentional mismatch detection.
* `TestConfigConcurrencySafety`: Stress-tests 30 concurrent workers performing simultaneous mutations and reads over 100 iterations with 0 race conditions and 0 deadlocks.
* **Go Race Detector Command**: `powershell -ExecutionPolicy Bypass -File .\myaws.ps1 race` (runs `go test -race` across all production packages).

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

### 5. `BROKERAGE_CHARGES` (Zerodha Regulatory Levies & Fee Schedule)
- **Equity Intraday (MIS)**: `equity_intraday_brokerage_pct` (0.03%), `equity_intraday_brokerage_max` (20.0), `equity_intraday_stt_sell_pct` (0.025%), `equity_intraday_exchange_pct` (0.00307%), `equity_intraday_stamp_buy_pct` (0.003%)
- **Options (NFO)**: `options_brokerage_flat_per_order` (20.0), `options_stt_sell_pct` (0.10%), `options_exchange_pct` (0.03553%), `options_stamp_buy_pct` (0.003%)
- **Statutory Levies & GST**: `sebi_turnover_per_crore` (10.0), `gst_rate_pct` (18.0), `calculate_charges_enabled` (true)

### 6. `OPTIONS_INDEX_CONFIGS` (Per-Index Multipliers & Parameters)
- `index_symbol`, `is_active`, `is_live`, `base_lot_size`, `max_multiplier`, `multiplier_on_reversal`, `target_entry_premium`, `expiry_type`, `next_month_days`, `sl_pct`, `trail_sl_enabled`, `trail_sl_buffer_pct`, `st1_period`, `st1_multiplier`, `st2_period`, `st2_multiplier`, `st3_period`, `st3_multiplier`, `last_new_trade_time`, `auto_square_off_time`, `supertrend_cutoff_time`, `max_trades_per_day`

---

## 4. UI Settings Layout & Anti-Clutter Rules

When adding new settings or tools to the web dashboard, follow these strict UI layout and aesthetics rules:

1. **Header Sanctity (Zero Clutter in `.header-right`)**:
   - Never inject ad-hoc action buttons (like "Brokerage & Taxes" or "Settings") into the main top header navigation.
   - `.header-right` must remain clean and uncluttered with only brand and the circular `UT` user avatar icon.
   - Never pollute the user profile dropdown menu with individual configuration shortcuts. The dropdown menu must contain only top-level navigation: `Settings` (which opens the full Settings modal) and `Switch Theme`.

2. **Settings Modal Containment**:
   - All system and strategy settings belong inside the Settings Modal as a dedicated sidebar tab (`id="settings-tab-btn-..."`) with an accompanying pane (`id="settings-pane-..."`).
   - If a setting category also warrants a dashboard overview, add it as a horizontal console tab (e.g. `id="tab-btn-brokerage"` / `id="console-brokerage-content"`), never as a header button.

3. **App-Standard CSS & Theming Tokens (No Arbitrary Overrides)**:
   - **Never use hardcoded RGB/dark grey backgrounds**: Avoid ad-hoc inline styles like `background: rgba(30, 34, 45, 0.4);` or custom border colors that break theme consistency.
   - **Always inherit native card classes**: Use `.kite-section-card`, which automatically adapts to both dark theme (`background: rgba(255, 255, 255, 0.02); border: 1px solid var(--border-color);`) and light theme (`background: #FFFFFF; border-color: #E2E8F0;`).
   - **Standard Form Elements**: Use `.kite-input`, `.kite-select`, `.kite-switch`, and `.kite-form-row` with standard widths (`width: 90px;` or `width: 100%;`).
   - **Financial Figures & Monospace Typography**: All currency amounts, percentages, points, and numeric results must use `font-family: 'JetBrains Mono', monospace;`.
   - **Structured KPI Mini-Cards**: Group numeric outputs into discrete mini-cards with crisp uppercase labels (`font-size: 0.65rem; font-weight: 700; text-transform: uppercase;`) and semantic colors (`var(--color-up)`, `var(--color-down)`, `var(--color-accent)`).

---

## 5. Calculation Precision & Testing Protocols

When adding financial calculations, fee schedules, or risk metrics:

1. **Exact Precision Down to the Paisa**:
   - Test against official broker contract notes down to 2 decimal places.
   - **Float Epsilon Guard**: Use IEEE 754 epsilon guard (`1e-9`) in `round2()` to prevent float truncation artifacts (e.g. `0.315 * 100 = 31.499999999999996` rounding to 31 instead of 32):
     ```go
     func round2(val float64) float64 {
         return math.Round((val+1e-9)*100.0) / 100.0
     }
     ```
   - **Ceiling Rounding for Breakeven Cost SL**: Tick rounding for BUY cost SL must use `math.Ceil` so the SL strictly covers 100% of roundtrip charges at the tick boundary.

2. **Decoupled Architecture**:
   - Keep calculation logic pure and vendor-agnostic in `risk/charges.go`.
   - Attach dynamically to risk-reward strategies (`risk/calculator.go`) without circular dependencies or hardcoded fallbacks.

3. **Mandatory Concurrency & Unit Testing**:
   - Add unit tests validating edge cases (zero turnover, small lots, large quantities, max fee cap).
   - Verify zero data races across the entire codebase using `powershell -ExecutionPolicy Bypass -File .\myaws.ps1 race` (`go test -race ./...`).

4. **Remote Deployment & Client Cache Verification**:
   - Push to GitHub and build/restart the container on AWS.
   - Verify container health via `docker compose logs --tail=20 app` and remote API curls.
   - Remind the user to perform a hard refresh (`Ctrl + F5` / `Cmd + Shift + R`) to prevent stale browser cache from masking UI updates.

---

## 6. Troubleshooting & Debugging Checklist

When investigating why a setting change did not take effect:
1. **Did the UI Save write to PostgreSQL?** Check `app_system_configs` or `options_index_configs` in the DB.
2. **Did `applySystemConfigsToSettings` execute?** Verify `loadModularStrategyConfigs()` was invoked upon saving settings or starting up.
3. **Was the Engine Updated?** Verify the strategy struct has the corresponding setter (e.g. `engine.SetSLBufferPct(val)`) and that `loadModularStrategyConfigs()` called it.
4. **Is there an Env Collision?** Check if a variable like `LimitBufferPct` was incorrectly reading `sl_buffer_pct`.
5. **Run the Audit Script**: Execute `go run scripts/verify_configs/main.go` to instantly isolate any missing wiring points.

