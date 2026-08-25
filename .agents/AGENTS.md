# Zerodha Trading Bot Workspace Memory & Rules

This file provides rules, architectural overview, and coding guidelines for the Zerodha Trading Bot codebase, customized for the Antigravity agent.

## Project Overview
A production-grade Go algorithmic trading bot interfacing with the Zerodha Kite Connect API. It processes real-time market data ticks, aggregates them into 1-minute and 5-minute candles, generates signals using technical indicators (VWAP, ATR, RSI), and executes trades with rigorous pre-trade and post-trade risk management.

### Directory Structure & Layers
- [main.go](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/main.go): Main entry point and lifecycle orchestrator running 4 concurrent loops.
- [config/settings.go](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/config/settings.go): Configuration manager loading settings from `.env`.
- [data/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/data): Handles WebSocket/mock ticker, instrument master (SecurityMaster), 1-minute and 5-minute candle aggregation, and TimescaleDB storage.
- [strategy/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/strategy): Computes technical indicators and generates buy/sell/hold signals.
- [selection/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/selection): Handles modular stock selection algorithms and selectors.
- [execution/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/execution): Handles order execution, status polling/tracking, and resilient API call retries.
- [risk/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/risk): Enforces risk management, tracks open positions, runs pluggable risk-reward calculators, and implements the circuit breaker.
- [monitoring/](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/monitoring): Structured JSON logging (via Zap) and Prometheus metric exporting.

---

## Coding Guidelines

### 1. Concurrency & Safety
- **State Protection**: Always use `sync.RWMutex` or `sync.Mutex` when accessing shared fields in strategy engines, ticker states, risk managers, or order/position maps. Do not allow race conditions.
- **Context Cancellation**: Ensure all goroutines monitor `ctx.Done()` to exit gracefully upon shutdown.

### 2. Error Handling & Wrapping
- **Wrap Errors**: Wrap errors using the `%w` verb when propagating them up (e.g., `fmt.Errorf("failed to perform action: %w", err)`).
- **Log Errors**: Use the zap logger to log error contexts rather than printing directly to stdout/stderr.

### 3. Database Operations
- **TimescaleDB Compatibility**: The `candles_1m` and `candles_5m` tables are structured for time-series data. Query with time bounds when fetching history to ensure quick execution. Both tables contain a `color` VARCHAR column (`GREEN`, `RED`, or `DOJI`).
- **Resource Cleanup**: Always close `sql.Rows` handles immediately after scanning.
- **On Conflict Handling**: When upserting candles, handle conflicts on `(token, time)` using `ON CONFLICT DO UPDATE`.
- **Decoupled Queries Pattern (Repository)**: All raw database SQL queries MUST be isolated within the `data` package (specifically encapsulated in methods on `data.Database` in [queries.go](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/data/queries.go)). Domain logic, handlers, schedulers, and executors MUST NOT execute raw query strings directly or manage database connection contexts; instead, they must invoke helper methods on the `*data.Database` (or `*Database` in package `data`) instances.
- **Unified Database Migrations**: All database tables, columns, indexes, and schema modifications MUST be declared and initialized inside the main application schema setup in [data/database.go](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/data/database.go) to ensure they are created automatically on bot startup. Do NOT rely on standalone scripts or tools (e.g. `pre-selection/main.go`) to initialize their own tables, as this causes failures on remote or fresh instances (such as AWS) when run by the automated scheduler.

### 4. Logging Standards
- **Structured Fields**: Use Uber's `zap` structured logging. Avoid unstructured logging. Provide context keys (e.g., `zap.String("symbol", s)`, `zap.Error(err)`).

### 5. Environment Configuration Rules
- **Keep Env Files in Sync**: Whenever you add, modify, or delete environment variables in `.env`, you must immediately make matching changes to:
  1. `.env.example` (to keep the template in sync).
  2. `config/settings.go` (to expose the config property in Go).
  3. `docker-compose.yml` (under the `environment` section of the `app` service, to ensure the variable is forwarded into the running Docker container).
- **Keep Documentation and Frontend Dynamic**: When any parameter or environment configuration changes inside `.env`, ensure matching default value updates are propagated to `README.md` (Risk Framework table), and verify that any corresponding frontend files (such as `index.html`) display these configurations dynamically rather than using hardcoded labels.

---

## Developer Commands

- **Build**: `go build -o trading-bot`
- **Run**: `./trading-bot`
- **Dev / Hot reload**: `go run .`
- **Run Tests**: `go test ./...`
- **Format Code**: `go fmt ./...`
- **Lint Code**: `golangci-lint run ./...`
- **Infrastructure**: `docker-compose up -d`
- **Seeding Historical Data**: `go run scripts/seed/main.go`

### 6. Backtesting & Report Rules
- **Timezone Normalization**: Historical database timestamps may differ between seeded UTC-named times (Hour >= 9) and live UTC times (Hour < 9). Always normalize them accordingly (e.g. converting UTC times to local time using `t.In(loc)` if Hour < 9, or constructing a local time directly if Hour >= 9) to prevent 5.5-hour timezone offsets in backtests and frontend display API endpoints (like `/api/candles`).
- **Volume Normalization**: Live candle data can contain cumulative tick volumes instead of interval volumes. Always check if database volumes are monotonically increasing and normalize them (`current - prev`) before running strategy simulations.
- **Dynamic Report Pathing**: Always write generated reports (e.g., `backtest_report.md`) to the dynamically provided current active conversation's artifact folder instead of any hardcoded conversation ID folders.

### 7. Position & Order Re-attachment (AWS & Startup Recovery)
- **No Emergency Startup Square-offs**: Open MIS positions are never squared off on startup before 3:15 PM. The bot must recover them.
- **State Reconstruction**: Active positions on Zerodha are matched against completed entry orders today to reconstruct local in-memory states (`EntryPrice`, `Side`, `Quantity`, `Strategy`).
- **Stop-Loss Recovery**: Active stop-loss orders (`SL`/`SL-M`) are reconciled on startup: if already active on Zerodha, they are recovered and tracked; if missing, they are recalculated (1.5% default fallback) and placed.
- **Database Position Persistence**: All open positions and their active broker SL order IDs must be synced with the `positions` database table (created with unique indexes on `order_id` in `database.go`) on entry, update, and close.
- **Trigger State Recovery**: On startup, today's completed trades must be scanned from the `trades` database table and loaded into the strategy engines' `triggeredTrades` memory maps to prevent duplicate entries for previously traded symbols after a reboot.
- **API Cache Protection**: Historical candles fetched during morning catch-up API fallbacks are saved in the database `candles_5m` table to protect Kite Connect API limits on subsequent restarts.

### 8. Live Streaming & Subscription Robustness
- **Dynamic Subscription Re-connection Recovery**: The `RobustKiteTicker` must maintain a synchronized internal cache of all active subscriptions (`subscribedTokens`). Upon WebSocket auto-reconnection, the `OnConnect` callback must re-subscribe to the *entire cached list* (initial + dynamic additions) to prevent losing dynamic watchlist or manual additions.
- **Tick-by-Tick Volume Aggregation**: Raw Zerodha WebSocket ticks report cumulative daily volume (`VolumeTraded`). The `CandleAggregator` must track the last seen cumulative volume for each token and compute the tick-by-tick interval volume (`current - prev`). Increment candle volume and VWAP sums using this interval volume to prevent severe volume inflation and VWAP distortion.
- **Catch-up Candle Validation**: When catch-up queries run, calculate the expected number of 5m candles since 09:15 AM IST (capped at 15:30 PM IST). Only bypass the Zerodha `/historical` API fallback if the local DB contains **at least** the expected candle count, ensuring the strategies do not run with incomplete morning data due to connection drops.

### 9. Broker API Decoupling (Pure Domain Model Isolation)
- **Zero Direct SDK Dependencies**: No core logic package (e.g. `execution`, `selection`, `strategy`, `risk`), server file (`handlers.go`), database script (`queries.go`, `database.go`), or entry point file (`main.go`, `scheduler.go`) should directly import `"github.com/zerodha/gokiteconnect/v4"`.
- **Use BrokerClient & Generic Models**: All files must use the `data.BrokerClient` interface and its vendor-agnostic models defined in [data/broker_models.go](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/data/broker_models.go).
- **Isolate Adaptations**: All vendor-specific calls, parameter structures, and mappings to/from Zerodha SDK models MUST reside strictly inside `data/broker.go` within `ZerodhaBrokerAdapter`.

### 10. Remote AWS Deployment Rules
- **No scp for Source Code**: Always push local changes to GitHub first, then run `git pull` on the remote AWS server to update the code. Do not copy source files directly using `scp`.

### 11. High-Water Mark Multi-Tier Trailing SL & Profit Protection
- **Multi-Stage SL Trailing**: The `RiskManager` evaluates peak high/low (`HighestPrice`) on every tick:
  - Stage 1 ($\ge +0.8\%$ gain): SL trails to $+0.2\%$ (No-loss buffer).
  - Stage 2 ($\ge +1.4\%$ gain): SL trails to $+0.7\%$ (Locks early gains).
  - Stage 3 ($\ge +2.0\%$ gain / Target 1): Exits 60% partial quantity and trails remaining SL to $+1.0\%$ (Locks solid profit).
  - Stage 4 ($\ge +2.5\%$ gain): SL step-trails dynamically at $(\text{Peak High} - 1.0\%)$.
- **45-Minute Time-Decay Guard**: Positions held $> 45$ minutes with $\ge +0.4\%$ gain automatically trail SL to $+0.2\%$ to prevent mid-day decay from eroding profits.
- **Live Broker SL Synchronization**: When `action == "SL_TRAILED"`, `engine.go` updates the broker-side SL order on Zerodha exchange (`replaceBrokerSLOnPartialExit`) with the new trailed trigger price.

### 12. Options Paper Trade Seeding & Multi-Day Date Matching Rules
- **Exact Holding Duration Calculation**: When inserting simulated or backtested option paper trades into the `trades` database table, always calculate and store the exact holding duration in `time_held_minutes` (`int(exitTime.Sub(entryTime).Minutes())`) rather than hardcoding static 45-minute fallbacks.
- **Entry & Exit Date Range Matching**: When filtering trades by date in UI handlers (`fetchOptionsTradesLog`), compute `entryTime = exitTime - (time_held_minutes * 60)` so that overnight trades match both their Entry Date and Exit Date.
- **Timezone Normalization**: When serving trades via `/api/trades/all`, format timestamps using explicit IST time location (`time.Date(..., loc)`) if `Hour >= 9` to prevent +5.5 hour double-offset shifts (e.g. converting 14:05 IST to 19:35 IST).

### 13. SuperTrend Parameter Synchronization & Chart Marker Rules
- **Strict Parameter Propagation Across 5 Files**: Whenever any SuperTrend parameter (e.g. `SUPERTREND_ST1_FACTOR`) or environment variable in `.env` changes:
  1. `.env`: Update target value.
  2. `.env.example`: Update template value immediately.
  3. `config/settings.go`: Update default fallback value in Go struct.
  4. `docker-compose.yml`: Forward the environment variable under `app` service (`- SUPERTREND_ST1_FACTOR=${SUPERTREND_ST1_FACTOR:-4.0}`).
  5. `README.md`: Update Risk Framework documentation table and strategy description.
- **Dynamic Frontend Legends**: No static text labels like `ST1 (10, 4)` in HTML (`index.html`). Chart header legends and series titles must load dynamically from `/api/options/state` (`st1_params`).
- **Single-Candle Reversal Trade Markers**:
  - When a 5-minute candle closes confirming a trend reversal across all 3 SuperTrends, trade execution happens at **candle close confirmation time** (e.g. 14:30:00 IST).
  - Both the Exit of the old position (`EXIT_PROFIT` / `EXIT_SL`) AND the Entry of the new position (`SELL_PE` / `SELL_CE`) MUST be recorded in PostgreSQL with the **exact same `created_at` timestamp** (`14:30:00 IST`).
  - Chart signal markers MUST be built strictly from executed trade records when trades exist in DB to prevent offset duplicate arrows on consecutive candles.

### 14. Database Repository Layer Mandatory IST Normalization Guard
- **Automatic Normalization on DB Write & Read**: All candle SQL persistence methods (`InsertCandle`) and candle query methods (`GetLastNCandles`) in [`data/queries.go`](file:///C:/Users/Dell/OneDrive/Desktop/cz/zt/data/queries.go) MUST pass timestamps through `data.NormalizeToIST(t)` prior to executing SQL statements and prior to returning scanned candle structs to callers. This guarantees that unnormalized UTC/wall-clock timestamp variations can never enter PostgreSQL or pollute UI chart time scales.

### 15. Standardized IST Wall-Clock Time Anchoring (`NormalizeToIST`)
- **Wall-Clock Anchoring**: `data.NormalizeToIST(t)` MUST anchor wall-clock time components (`Year`, `Month`, `Day`, `Hour`, `Minute`, `Second`) directly into `Asia/Kolkata` location using `time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), ISTLocation)`. This prevents double-offset shifts (+5.5 hours) when timestamps scanned from PostgreSQL or Zerodha historical API have explicit UTC timezone attributes.
- **Trade Marker Unix Timestamp Alignment**: In `handlers.go` (`handleOptionsSuperTrends`), trade entry times computed via `NormalizeToIST(tr.CreatedAt).Add(-time.Duration(tr.TimeHeldMinutes)*time.Minute)` and 5m candle timestamps `NormalizeToIST(last.Time)` MUST use identical `flooredUnix = (t.Unix() / 300) * 300` calculations to ensure 100% exact alignment between chart candles and signal markers (`SELL PE`, `SELL CE`, `EXIT`).

### 16. Multi-Day Continuity Chart Scoping & LightweightCharts Marker De-duplication
- **Multi-Day Continuity Scoping**: `/api/options/supertrends?date=YYYY-MM-DD` MUST return **Previous Trading Day + Target Date** (e.g. Friday 31/07 + Monday 03/08, 150 candles) when `date` query parameter is provided. This ensures that the UI chart canvas provides seamless technical indicator continuity across day boundaries while displaying all entry/exit signal markers for both days.
- **Multi-Signal Marker Combination**: In `index.html` (`renderOptionsSuperTrendChart`), when a single candle contains both `EXIT` and `ENTRY` signals (e.g. reversal candles with `"ENTRY_SELL_CE,EXIT_PROFIT"`), they MUST be combined into a single marker object (e.g. `EXIT & SELL CE`) to prevent LightweightCharts from discarding duplicate timestamp markers.
- **Simulation Trade State Synchronization**: In paper trading/backtest scripts (e.g. `scripts/run_options_paper_trades/main.go`), when an initial trade is opened, `posMgr.OnTradeOpened(...)` MUST be invoked to sync in-memory position state. Otherwise, `EvaluateSignal()` will falsely see `activePosition == nil` on subsequent candles, triggering unwanted mid-day trade exits.

### 18. Dynamic In-Memory WebSocket Reconnection Guard
- **Immediate Reconnection on Token Update**: Whenever `SetAccessToken(token)` is invoked (such as when exchanging a request token via UI handler `handleConfigAccessToken`), the ticker manager (`RobustKiteTicker`) MUST close the old WebSocket instance (`oldTicker.Close()`), re-instantiate `ticker.New(apiKey, token)`, and trigger an immediate background reconnection (`Connect(ctx, subscribedTokens)`). Never rely solely on mutating internal string fields without re-establishing the live WebSocket ticker client.

### 19. Zerodha ISO Timestamp & Database Insertion Formatting Guard
- **Explicit Offset Formatting**: Raw ISO timestamp strings returned by the Zerodha historical REST API (`2026-08-04T09:15:00+0530`) MUST be formatted with space separator and colon in offset (`2026-08-04 09:15:00+05:30`) or parsed via `time.Parse` prior to executing raw PostgreSQL SQL insert statements. This guarantees that `psql` CLI and raw database queries parse timezone offsets cleanly without silent row rejection.

### 20. 100% Real Live Zerodha NFO Market Quotes for Option Trading (Paper & Live)
- **Zero Static Price Fallbacks**: All option trade entry prices, exit prices, 50% SL tracking, and P&L calculations MUST fetch real-time Zerodha market quotes (`tb.kiteClient.GetQuote("NFO:" + symbol)`). Hardcoded or static fallback values (`120.0`, `65.0`) MUST NOT be used in paper or live mode.

### 21. Instant Candle Close Execution Sync
- **Immediate Candle Sync at Candle Close**: The options evaluation loop (`runOptionsBotLoop`) MUST sync NIFTY 50 5-minute candles from Zerodha API immediately at candle close (`Second() < 10` on 5m boundaries) so that SuperTrend reversals are evaluated and executed within 5 seconds at the start of the next candle (e.g., at `12:30:05 IST`).

### 22. Dynamic Holding Duration Calculation
- **Exact Duration Persistence**: When persisting closed option trades to the `trades` database table, `time_held_minutes` MUST be calculated dynamically (`int(nowIST.Sub(optPos.CreatedAt).Minutes())`) to ensure exact alignment between Entry Time and Exit Time in UI trade logs.

### 18. Dynamic In-Memory WebSocket Reconnection Guard
- **Immediate Reconnection on Token Update**: Whenever `SetAccessToken(token)` is invoked (such as when exchanging a request token via UI handler `handleConfigAccessToken`), the ticker manager (`RobustKiteTicker`) MUST close the old WebSocket instance (`oldTicker.Close()`), re-instantiate `ticker.New(apiKey, token)`, and trigger an immediate background reconnection (`Connect(ctx, subscribedTokens)`). Never rely solely on mutating internal string fields without re-establishing the live WebSocket ticker client.

### 19. Zerodha ISO Timestamp & Database Insertion Formatting Guard
- **Explicit Offset Formatting**: Raw ISO timestamp strings returned by the Zerodha historical REST API (`2026-08-04T09:15:00+0530`) MUST be formatted with space separator and colon in offset (`2026-08-04 09:15:00+05:30`) or parsed via `time.Parse` prior to executing raw PostgreSQL SQL insert statements. This guarantees that `psql` CLI and raw database queries parse timezone offsets cleanly without silent row rejection.

### 20. 100% Real Live Zerodha NFO Market Quotes for Option Trading (Paper & Live)
- **Zero Static Price Fallbacks**: All option trade entry prices, exit prices, 50% SL tracking, and P&L calculations MUST fetch real-time Zerodha market quotes (`tb.kiteClient.GetQuote("NFO:" + symbol)`). Hardcoded or static fallback values (`120.0`, `65.0`) MUST NOT be used in paper or live mode.

### 21. Instant Candle Close Execution Sync
- **Immediate Candle Sync at Candle Close**: The options evaluation loop (`runOptionsBotLoop`) MUST sync NIFTY 50 5-minute candles from Zerodha API immediately at candle close (`Second() < 10` on 5m boundaries) so that SuperTrend reversals are evaluated and executed within 5 seconds at the start of the next candle (e.g., at `12:30:05 IST`).

### 22. Dynamic Holding Duration Calculation
- **Exact Duration Persistence**: When persisting closed option trades to the `trades` database table, `time_held_minutes` MUST be calculated dynamically (`int(nowIST.Sub(optPos.CreatedAt).Minutes())`) to ensure exact alignment between Entry Time and Exit Time in UI trade logs.

### 23. Mandatory Post-Edit Time & Timezone Empirical Verification Guard
- **Mandatory Centralized Time Utility**: Any code written or modified that handles timestamps, candle times, trade logs, or API responses MUST use `data.NormalizeToIST(t)` or `t.In(data.ISTLocation)`.
- **Mandatory Post-Edit Verification**: After writing or modifying ANY code that reads, writes, formats, or serves time or timestamps (in `handlers.go`, `main.go`, `queries.go`, `database.go`, or `index.html`), the agent MUST run empirical runtime verification (e.g. querying API endpoints via script or inspecting DB rows) to validate that timestamps display 100% accurately in IST (`Asia/Kolkata`) without any 5.5-hour UTC shifts or wall-clock offsets before declaring completion.

### 24. Intraday Expected Move & Option Delta Sensitivity Rules
- **Pure Engine Isolation (`data/expected_move.go`)**: `CalculateExpectedMove` calculates VIX daily expected move ($\text{VIX} / \sqrt{365}$), intraday decay target ($\sqrt{H_{\text{remaining}} / 6.25}$), ATM Straddle market maker bounds ($0.85 \times \text{Straddle}$), and Black-Scholes Delta ($\Delta$) & Theta ($\Theta$) projected option premiums.
- **Strict Real Market & DB Data Sourcing (Zero Static Price Assumptions)**: Spot price, India VIX index, ATM Straddle quotes, and Option LTP MUST be fetched live from Zerodha API (`tb.kiteClient.GetQuote`), or if unquoted, queried from PostgreSQL database table `candles_5m`. Hardcoded or assumed static numbers (`24570.0`, `14.50`, `205.0`, `30.0`) must never be substituted.
- **Isolated Console Tab**: The Expected Move suite resides in its dedicated UI tab pane (`#console-expected-move-content`) under the button **`🎯 Expected Move`**, decoupled from the 5m SuperTrend chart pane.

### 25. Completed Closed Candle Strategy Evaluation Mandate
- **Strict Completed Candle Filtering**: The options SuperTrend strategy evaluation loop (`runOptionsBotLoop`) MUST filter candle datasets to include **ONLY fully completed closed 5-minute candles** (`cTime <= nowFloored - 5m`). Incomplete mid-candles currently forming in real-time MUST be excluded prior to invoking `CalculateTripleSuperTrend` to prevent false mid-candle entries or signals.

### 26. Strict Trade-Only Options Chart Signal Markers
- **Trade Activity Marker Filtering**: SuperTrend option chart signal markers MUST ONLY be rendered on candles where an actual trade entry or exit occurred (or combined single-candle reversal `EXIT & SELL PE/CE`). Raw indicator arrows MUST NOT be rendered on candles where no trade was taken.

### 27. Database PostgreSQL Server Clock Timestamp Recording (`NOW()`)
- **PostgreSQL IST Server Clock**: All trade entry, exit, and position database operations MUST record timestamps using PostgreSQL server clock (`NOW() AT TIME ZONE 'Asia/Kolkata'`).
- **Database Connection Timezone Configuration**: Database initialization (`InitDB` in `data/database.go`) MUST execute `SET timezone = 'Asia/Kolkata';` and `ALTER DATABASE zerodha_trading SET timezone TO 'Asia/Kolkata';`.
- **Docker Compose Timezone Enforcment**: `docker-compose.yml` MUST set `TZ: Asia/Kolkata` and `PGTZ: Asia/Kolkata` under environment for `postgres` service to guarantee that PostgreSQL runs natively in Indian Standard Time (`+05:30`).

### 28. Permanent 1-Day Daily Candle Stock Scanner Architecture
- **Daily Candle Storage (`candles_1d`)**: Stock scanner range lookback calculations (52W High/Low, Monthly High/Low, Weekly High/Low) MUST evaluate 1-day daily candles (`candles_1d` table in PostgreSQL), populated via 252 daily candles fetched from Zerodha REST API (`interval = "day"`), supplemented by `buildTodayLiveDailyCandle` during live market hours and `aggregate5mToDaily` fallback off-hours.
- **Lookback Window History Sufficiency Guard**: `getHighLow(candles, lookback)` MUST verify that `len(candles)` meets minimum history bounds (`>= 200` for ATH/ATL, `>= 150` for 52W High/Low, `>= 15` for Monthly, `>= 4` for Weekly). If history is shorter, `getHighLow` returns `0.0, 0.0` so that short-history stocks (e.g. 15 daily candles) are NEVER falsely flagged as All-Time High or 52-Week High breakouts.

### 29. SuperTrend Current Session Candle Flip & Ignored Candles Rules
- **No Yesterday Trend Carryover**: On a new trading day, `optionsPosMgr.ResetDailyState()` resets `lastTrend = "NEUTRAL"`. Yesterday's candles are used ONLY for indicator line calculation. No trade is taken at 09:15:01 AM market open on carried-over trends.
- **Current Session Candle Close Flip Entry**: Initial trade entry (`OPEN_INITIAL`) requires a completed 5-minute candle of the current session to close confirming an explicit trend flip above/below SuperTrend (`trend != lastTrend`, e.g. at 09:20 AM IST after the 09:15-09:20 candle closes).
- **Equity Ignored Candles Enforcement**: `LV_MIN_CANDLES_TO_IGNORE` (3 candles = 09:30 AM start) and `VB_MIN_CANDLES_TO_IGNORE` (2 candles = 09:25 AM start) are strictly checked before evaluating breakout trades for Low Volume and Vande Bharat equity strategies.

### 30. Vande Bharat 5-Rule Specification Guard
- **Rule 1 (1st Candle Master Only)**: Master candle MUST be the 1st 5m candle of the day (09:15 AM IST) closing above PDH (Buy) or below PDL (Sell). If 1st candle fails, no Master candle is set today.
- **Rule 2 (Stock Day % Change Filter)**: At trade trigger entry time, overall stock day % change (`|LTP - Open| / Open * 100`) MUST be < 3.0%.
- **Rule 3 (Confirmation Candle Range Bounds & Strict Intermediate Consolidation)**:
  - All intermediate candles between Master Candle and Confirmation Candle MUST stay strictly **INSIDE** the Master Candle range (`Low >= Master Low` and `High <= Master High`).
  - If ANY intermediate candle breaches the Master range before the confirmation candle, or if an attempted breakout fails confirmation (wrong color or range `< 0.5%` or `> 1.0%`), the entire Master setup is immediately **INVALIDATED**.
  - Confirmation candle range % (`(High - Low) / Close * 100`) MUST be strictly between 0.5% and 1.0% of stock price.
- **Rule 4 (Master Candle Max 40% Wick)**: Master candle body must account for at least 60% of total range (total upper+lower wicks <= 40% of range).
- **Rule 5 (2nd Candle SL Anchor)**: Stop-loss anchor level is set to the 2nd 5m candle of the day (09:20 AM IST candle Low for BUY, High for SELL).

### 31. LOW VOLUME Strategy 1st Candle PDH/PDL Qualification Guard
- **BUY Setup Qualified**: Only if the 1st 5m candle of the day (09:15 AM IST) closes **above PDH** (`1st_Candle.Close > PDH`).
- **SELL Setup Qualified**: Only if the 1st 5m candle of the day (09:15 AM IST) closes **below PDL** (`1st_Candle.Close < PDL`).
- **Disqualification**: If the 1st 5m candle closes inside the previous day range (`PDL <= Close <= PDH`), no LOW VOLUME trades trigger for that stock today.
- **Lowest-Volume Setup Candle**: Lowest volume 5m candle since 09:15 AM IST acts as Setup Candle. BUY requires RED setup candle (`Close < Open`), SELL requires GREEN setup candle (`Close > Open`).
- **Execution Window**: Starts strictly after 3 ignored morning 5m candles (`09:30:01 AM IST`).

### 32. Pure Backend API Trade Status & Single Source of Truth Guard
- **Zero Client-Side Time Rules**: Frontend (`index.html`) MUST NOT contain client-side time checks or conditional status overrides (e.g. `if (dExitObj.getHours() === 15 ...)`).
- **Backend Persistence**: All trade status labels (`EOD SQUARE-OFF`, `PROFIT EXIT`, `REVERSAL EXIT`, `50% SL HIT`) MUST be determined by backend execution routines and persisted directly in PostgreSQL (`trades` table).
- **Direct API Display**: The UI renders `tr.status` directly from backend REST API responses (`/api/trades/all`).

### 33. Unbiased Setup-Driven Strategy Execution Guard
- **Setup-Driven Signal Checks**: Vande Bharat and Low Volume strategy breakout checks execute setup-driven signals based strictly on candle pattern confirmation without enforcing global market bias restrictions.

### 34. Centralized Time Module & Single Source of Truth (`data/time_utils.go`)
- **Centralized Time Functions**: All time manipulation, parsing, formatting, IST normalization (`data.NormalizeToIST`, `data.NowIST`, `data.FormatIST`, `data.FormatDate`, `data.GetEffectiveTradingDate`, `data.GetPreviousTradingDay`, `data.GetUpcomingOptionExpiry`), and trading calendar checks (`data.IsTradingDay`, `data.IsMarketOpen`) MUST strictly utilize `data/time_utils.go` on the backend and `window.ISTTime` on the frontend (`index.html`).
- **No Ad-Hoc Time Helpers**: Never create local ad-hoc time parsing functions or timezone conversions in subpackages (`risk`, `strategy`, `execution`, `selection`, etc.). Always import and invoke the centralized `data.*` time helpers.

### 35. High-Performance Algorithm & UI Responsiveness Standards
- **Single Linear Pass ($O(N)$) Technical Indicators**: All series-wide technical indicator computations (SuperTrends, ATR, TR, EMA) MUST be computed in a single forward pass across candles (`CalculateTripleSuperTrendSeries` in `strategy/supertrend_options_engine.go`). Never nest full-history indicator calculations inside loops ($O(N^2)$), as this causes 10–20 second UI stalls on multi-day historical data.
- **LightweightCharts DOM Canvas Instance Reuse**: Never destroy and recreate `createChart` canvas instances in the browser DOM on tab clicks or date changes. Retain persistent chart and series instances, updating series data via `series.setData(...)` and fitting content via `chart.timeScale().fitContent()`.
- **Fast $O(1)$ Key-Value Lookups**: Groupings and trade lookups (e.g. `groupTrades()`) MUST use JavaScript `Map` or hash maps rather than nested linear array searches ($O(N)$ inside loops).
- **Parallel Tab Resource Fetching**: UI tab switching must execute backend API calls in parallel using `Promise.allSettled()` rather than serial blocking `await` chains to guarantee instant sub-second response times.

### 36. Options 20% 5-Minute Candle Close Trailing Stop-Loss & Live NFO Resolution
- **Monotonic 20% Downward Ratchet**: On every 5-minute candle close, candidate Trailed SL is calculated as $\text{CurrentPremium} \times (1 + \text{OPTIONS\_TRAIL\_SL\_PCT}/100)$ (default 20% buffer above current premium). The SL tightens ($\text{New SL} = \text{Candidate SL}$) if candidate SL < current SL. If premium bounces adversely or stays flat, SL remains strictly constant—it NEVER increases or widens.
- **Continuous 1-Second Tick SL Breach**: If live tick LTP reaches or exceeds `SLPrice` (`CheckTick(ltp) == true`), position is closed immediately at market and multiplier resets to 1.
- **Live NFO Instrument Token Resolution**: `SecurityMaster.GetInstrumentToken` automatically resolves and caches active NFO option contract tokens (`ResolveNFOSymbol`) to enable real-time WebSocket tick streaming in addition to REST quote fallbacks.
- **Aggressive Limit Order Compliance**: Live option orders submit aggressive limit orders (5% below LTP for SELL, 5% above LTP for BUY) to guarantee instant fills while complying with exchange and Zerodha API protection policies.

### 37. Database-Driven Configuration Engine & UI Settings Safety Lock
- **PostgreSQL Configuration Storage**: All trading strategy parameters, per-index option settings (`options_index_configs`), equity risk parameters (`app_system_configs`), stock selection settings, and quant scanner triggers are persisted in PostgreSQL. They are loaded exclusively on bot boot and are edited dynamically through the dashboard Settings modal.
- **Strict Pre/Post-Market Bot Restart Lock**:
  - The UI Bot Restart endpoint (`POST /api/system/restart`) enforces a strict time safety gate.
  - Allowed **ONLY** before `09:15 AM IST` (`BOT_RESTART_ALLOWED_BEFORE`) and after `03:45 PM IST` (`BOT_RESTART_ALLOWED_AFTER`).
  - During live market hours (`09:15` to `15:45` IST), restart requests are **rejected immediately with HTTP 403 Forbidden** to protect active intraday positions and order lifecycles.
- **Zero Stale Env Overwrites**: `.env`, `.env.example`, and `docker-compose.yml` contain only system infrastructure credentials and database connectivity variables. All strategy parameters are managed directly via the UI Settings modal and PostgreSQL.

### 38. Pre-Market 09:00 AM Stock Trading, Manual Strategy Binding, & Force Recalculation
- **09:00:00 AM Pre-Market Consideration**: Manual stocks added via browser (`POST /api/watchlist/add`) or pre-configured in `daily_watchlists` with assigned strategies (`SYMBOL:STRATEGY`, e.g. `AMBER:NEWS`) are immediately registered, subscribed, and considered for trading starting from `09:00:00 AM IST`.
- **Cache-Bypassing Force Recalculation**: Scheduled stock selection (`09:00:00` / `09:25:00` IST) and user manual triggers (`POST /api/watchlist/recalculate`) execute with `force=true` to bypass cached database watchlist reconstruction and dynamically screen top gainers/losers and sector momentum across the entire market universe.
- **Manual Strategy Preservation**: Manual stock assignments (`MANUAL:NEWS`, `MANUAL:PDH_PDL`) are strictly preserved in `daily_watchlists` and highlighted with amber badges in the UI sidebar (`FO+SEC+NEWS` / `MA`).
- **Candle Gap Auto-Fill**: Catch-up historical candle sync checks `len(dbCandles) >= expectedCount` (`(now - 09:15) / 5 min`) to query missing candles from Zerodha and save them to `candles_5m`.

### 39. Single-Run Sector Selection Cleanup & Real-Time IST Timestamping
- **Active Sector Reset**: Whenever the Sector Scanner runs (`SectoralSelector.SelectStocks`), prior sector selections for today are cleared (`ClearSelectedSectors`) so that only the latest top $N$ active sectors (default: 2) are preserved in PostgreSQL `selected_sectors`.
- **Exact IST Timestamping**: Selected sectors are saved with exact `time.Now().In(data.ISTLocation)` timestamps and served via `/api/watchlist` to render live sector badges and `Selected at HH:MM:SS IST` in the UI widget.

### 40. Dynamic User-Managed Sector Baskets & Database-Driven Sectoral Selection
- **Zero Hardcoded Sector Maps in Backend Code**: Sector baskets and their constituent stock lists are stored in and dynamically loaded from the PostgreSQL `sector_definitions` table (`GetSectorDefinitions`, `GetSectorConstituentsMap`) rather than hardcoded in Go source code.
- **Full UI Sector Management Lifecycle**: Users can add new sector baskets (`POST /api/sectors`), edit constituent stock symbols, toggle sectors active/inactive, delete sectors (`DELETE /api/sectors`), or restore the default 9 sectoral categories (`POST /api/sectors/reset`) via the **"🏢 Sector Management"** tab in the Settings modal.
- **Dynamic Screening Execution**: `SectoralSelector.SelectStocks` loads active sectors from `sector_definitions` on every run, batches OHLC queries for all mapped stocks, computes live sector averages, filters by threshold, and saves top performing sectors to `selected_sectors`.


