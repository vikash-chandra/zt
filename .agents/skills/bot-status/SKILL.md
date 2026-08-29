---
name: bot-status
description: Check current trading bot status and health
---
# Bot Status Skill

Inspects real-time engine states, open positions, live P&L, WebSocket connectivity, and AWS server metrics across Equity and Options trading systems.

## Diagnostic Commands & Endpoints

| Resource | Scope | Endpoint / Command |
| :--- | :--- | :--- |
| **System & Engine State** | REST API | `GET /api/config/all` |
| **Options Engine & Position State** | REST API | `GET /api/options/state` |
| **Active Watchlist & Sectors** | REST API | `GET /api/watchlist` |
| **Open Positions & P&L** | REST API | `GET /api/positions` |
| **Completed Trade History** | REST API | `GET /api/trades/all` |
| **AWS Remote Container Status** | PowerShell CLI | `.\myaws.ps1 status` |
| **AWS Remote Docker Logs** | PowerShell CLI | `.\myaws.ps1 logs app 50` |
| **AWS SSH Direct Log Tail** | SSH Command | `ssh -i .\up-trade-vikash.pem ubuntu@3.7.29.3 "docker logs --tail 50 -f zt-app-1"` |

---

## Health Check Checklist

### 1. Options Strategy Health (`OPTIONS_SUPERTREND`)
- **Active Index Configuration**: Verified from PostgreSQL `options_index_configs` (`NIFTY 50`, `BANKNIFTY`, `SENSEX`, `FINNIFTY`, `MIDCPNIFTY`).
- **Live Quotes**: Verified that entries, exits, and SL tracking use live Zerodha quotes (`GetQuote`) with zero static assumptions.
- **20% Trailing SL**: Monotonic tightening verified on 5-minute candle closes (`trail_sl_pct = 20.0`).
- **Triple SuperTrend Alignment**: ST1 (10, 4.0), ST2 (7, 3.0), ST3 (7, 2.0) computed via $O(N)$ engine.

### 2. Equity Watchlist & Selection Health
- **Daily Watchlist**: Active stocks and assigned strategies (`FO`, `SECTOR`, `PDH_PDL`, `52WH_52WL`, `NEWS`).
- **09:00 AM Pre-Market Manual Stocks**: Custom manual stocks tagged with `MA` badges.
- **Top Active Sectors**: Real-time sector calculation timestamp in `selected_sectors`.

### 3. System Connectivity & Risk Controls
- **WebSocket Subscriptions**: Robust ticker re-connection cache maintained across auto-reconnects.
- **Market Hours Restart Gate**: Pre/post-market restart window verified (`< 09:15 AM` or `\ge 15:45 PM IST`).
- **Auto Square-Off Triggers**: Configured for `15:13 IST` (Options) and `15:20 IST` (Equity).

---

## Mandatory Time & Timezone Guard
- All server responses, logs, and metrics MUST report timestamps natively in Indian Standard Time (`Asia/Kolkata`, `+05:30`).
- Verify zero UTC double-offsets (+5.5 hours) on all API outputs.
