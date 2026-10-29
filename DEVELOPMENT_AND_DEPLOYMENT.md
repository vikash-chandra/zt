# Multi-System Development & Dual-Server Deployment Guide (AWS & VPS)

This guide documents everything you or an AI agent needs to develop, test, and deploy features from **any system or laptop** onto both remote production instances:
1. **Primary AWS Instance**: `3.7.29.3` (`ubuntu`, `up-trade-vikash.pem`, CLI `myaws.ps1`)
2. **Secondary VPS Instance**: `160.250.204.29` (`root`, `vps-vikash.pem`, CLI `myvps.ps1`)

---

## 1. Quickstart: Setting Up on a New Machine / Laptop

When switching to a new computer or logging in from another device:

### Step 1: Clone Repository
```powershell
git clone https://github.com/vikash-chandra/zt.git
cd zt
```

### Step 2: Copy SSH Private Keys into Project Root
Ensure the two authorized private keys are placed in the root directory:
- `up-trade-vikash.pem` (Primary AWS key)
- `vps-vikash.pem` (Secondary VPS key)

*(Note: Both `.pem` files and `myvps.ps1` are protected by `.gitignore` to prevent secret leakage).*

### Step 3: Enforce Windows NTFS File Permissions (Crucial)
Windows OpenSSH will reject keys if other users or processes have access permissions. Run this once in PowerShell:
```powershell
# AWS Key Permissions
icacls .\up-trade-vikash.pem /inheritance:r 2>$null
icacls .\up-trade-vikash.pem /remove:g Everyone "Authenticated Users" Users 2>$null
icacls .\up-trade-vikash.pem /grant:r "$($env:USERNAME):(R)" 2>$null

# VPS Key Permissions
icacls .\vps-vikash.pem /inheritance:r 2>$null
icacls .\vps-vikash.pem /remove:g Everyone "Authenticated Users" Users 2>$null
icacls .\vps-vikash.pem /grant:r "$($env:USERNAME):(R)" 2>$null
```
*(On Linux or macOS, run: `chmod 400 up-trade-vikash.pem vps-vikash.pem`)*.

### Step 4: Verify Connectivity to Both Servers
```powershell
# Check AWS
powershell -ExecutionPolicy Bypass -File .\myaws.ps1 status

# Check VPS
powershell -ExecutionPolicy Bypass -File .\myvps.ps1 status
```
If both return container lists (`zt-app-1`, `zt-postgres-1`) and resource usage, your machine is fully configured!

---

## 2. Feature Implementation Guidelines

When adding a new strategy, risk parameter, indicator, or UI capability:

### A. Clean Decoupled Architecture
1. **Core Strategy Logic**: Pure indicator computations belong in `strategy/` (e.g. `vande_bharat_engine.go`, `emas5_breakout_engine.go`, `indicators.go`).
2. **Stock Selection Logic**: Ranking, candidate filtering, and level-shifting belong in `selection/` (`selector.go`, `selection.go`).
3. **Database Repository Isolation**: NEVER execute raw SQL strings inside handlers or engine loops. All SQL must reside as isolated methods on `*data.Database` in [`data/queries.go`](data/queries.go).
4. **Broker Independence**: Core engines must use `data.BrokerClient` and vendor-agnostic models in [`data/broker_models.go`](data/broker_models.go). Direct Zerodha SDK imports are forbidden outside `data/broker.go`.
5. **Sub-100ms Latency Budget**: HTTP endpoints (e.g. `/api/config/save`, `/api/daily-watchlist/update-strategy`) must NEVER block on external Zerodha network calls. Use non-blocking lookups (`getQuickPreviousDayLevels`) and dispatch any missing-data fetches to background goroutines.

### B. Strict 5-File Configuration Synchronization Rule
Whenever a new parameter, setting, or environment variable is added or renamed:
1. [`.env`](.env): Update active target value.
2. [`.env.example`](.env.example): Update template default value.
3. [`config/settings.go`](config/settings.go): Add Go struct field and default fallback value.
4. [`docker-compose.yml`](docker-compose.yml): Forward variable under `app` service (`- MY_NEW_VAR=${MY_NEW_VAR:-default}`).
5. [`README.md`](README.md): Document in Risk Framework or Strategy Parameters table.

---

## 3. Local Testing & Quality Gates

Before committing any feature:

### 1. Run Unit & Edge-Case Tests
Always cover positive, negative, zero values, and edge conditions:
```powershell
go test -v ./strategy/... ./selection/... ./data/... ./config/... ./risk/... ./execution/...
```

### 2. Verify Clean Local Compilation
```powershell
go build -o trading-bot.exe .
Remove-Item .\trading-bot.exe -Force
```

---

## 4. Dual Deployment Procedure (AWS + VPS)

Deploying to production requires a synchronized 3-step sequence:

### Step 1: Push Code to GitHub
```powershell
git add .
git commit -m "feat(<scope>): <description of change>"
git push origin main
```

### Step 2: Deploy to Primary AWS (`3.7.29.3`)
```powershell
# 1. Pull latest code and rebuild app container
powershell -ExecutionPolicy Bypass -File .\myaws.ps1 deploy

# 2. Mandatory Go Race Detector Guard (MUST have zero race conditions)
powershell -ExecutionPolicy Bypass -File .\myaws.ps1 race

# 3. Check health and resource usage
powershell -ExecutionPolicy Bypass -File .\myaws.ps1 status
```

### Step 3: Deploy to Secondary VPS (`160.250.204.29`)
```powershell
# 1. Pull latest code and rebuild app container
powershell -ExecutionPolicy Bypass -File .\myvps.ps1 deploy

# 2. Check health and resource usage
powershell -ExecutionPolicy Bypass -File .\myvps.ps1 status
```

---

## 5. Operations & Troubleshooting Reference

Both PowerShell utilities (`myaws.ps1` and `myvps.ps1`) share identical commands:

| Task | Primary AWS (`3.7.29.3`) | Secondary VPS (`160.250.204.29`) |
| :--- | :--- | :--- |
| **Check Health & RAM** | `powershell -ExecutionPolicy Bypass -File .\myaws.ps1 status` | `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 status` |
| **Deploy Latest Code** | `powershell -ExecutionPolicy Bypass -File .\myaws.ps1 deploy` | `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 deploy` |
| **View Last 50 Logs** | `powershell -ExecutionPolicy Bypass -File .\myaws.ps1 logs` | `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 logs` |
| **Stream Live Logs** | `powershell -ExecutionPolicy Bypass -File .\myaws.ps1 logs -Follow` | `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 logs -Follow` |
| **Fast Restart** | `powershell -ExecutionPolicy Bypass -File .\myaws.ps1 restart` | `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 restart` |
| **Run Race Detector** | `powershell -ExecutionPolicy Bypass -File .\myaws.ps1 race` | `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 race` |
| **Check Database Rows**| `powershell -ExecutionPolicy Bypass -File .\myaws.ps1 db` | `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 db` |
| **Open SSH Tunnel** | `powershell -ExecutionPolicy Bypass -File .\myaws.ps1 tunnel` | `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 tunnel` |

### Common Gotchas & Fixes
- **SSH Hanging / Freezing**: Always ensure the `-n` flag is passed in SSH commands (`ssh -n -i ...`) to detach standard input on Windows.
- **Permission Denied (publickey)**: Run the NTFS permissions script in Section 1 on the corresponding `.pem` file.
- **Docker Build Failure on Remote**: Both servers have swap space configured. If a build ever exhausts RAM on the 512MB VPS, `docker builder prune -f` frees space.
