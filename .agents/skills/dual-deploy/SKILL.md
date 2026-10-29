---
name: dual-deploy
description: Turnkey multi-machine developer workflow to implement features, enforce 5-file config sync, test with edge cases and race detection, and deploy simultaneously to primary AWS (3.7.29.3) and secondary VPS (160.250.204.29).
---

# Dual-Server Feature Development & Deployment Skill

This skill provides turnkey instructions for implementing new features, testing them against edge cases, and deploying them across **both remote production instances**:
1. **Primary AWS Instance**: `3.7.29.3` (`ubuntu`, `up-trade-vikash.pem`, `.\myaws.ps1`)
2. **Secondary VPS Instance**: `160.250.204.29` (`root`, `vps-vikash.pem`, `.\myvps.ps1`)

---

## 1. Setting Up on a New / Fresh System

When switching to another laptop, workstation, or environment:

### A. Clone Repository & Install Prerequisites
1. Clone the repo:
   ```bash
   git clone https://github.com/vikash-chandra/zt.git
   cd zt
   ```
2. Verify Go 1.24+ and Docker are installed:
   ```bash
   go version
   docker --version
   ```

### B. Place SSH Private Keys in Project Root
Copy the authorized private keys into the project root directory:
- `up-trade-vikash.pem` (for AWS `3.7.29.3`)
- `vps-vikash.pem` (for VPS `160.250.204.29`)

### C. Enforce Strict Windows NTFS File Permissions
Windows SSH fails if keys are readable by other users. Run this command once:
```powershell
# AWS Key
icacls .\up-trade-vikash.pem /inheritance:r 2>$null
icacls .\up-trade-vikash.pem /remove:g Everyone "Authenticated Users" Users 2>$null
icacls .\up-trade-vikash.pem /grant:r "$($env:USERNAME):(R)" 2>$null

# VPS Key
icacls .\vps-vikash.pem /inheritance:r 2>$null
icacls .\vps-vikash.pem /remove:g Everyone "Authenticated Users" Users 2>$null
icacls .\vps-vikash.pem /grant:r "$($env:USERNAME):(R)" 2>$null
```
*(On Linux/macOS, simply run: `chmod 400 up-trade-vikash.pem vps-vikash.pem`)*.

### D. Verify Connectivity
Test passwordless SSH connection to both instances:
```powershell
powershell -ExecutionPolicy Bypass -File .\myaws.ps1 status
powershell -ExecutionPolicy Bypass -File .\myvps.ps1 status
```

---

## 2. Feature Implementation Rules

When implementing any strategy rule, stock selection logic, risk check, or UI component:

### Rule 1: Layered Decoupling
- **Strategy Logic**: Keep pure technical indicator computations inside `strategy/` (e.g. `vande_bharat_engine.go`, `emas5_breakout_engine.go`).
- **Stock Selection**: Keep ranking and filtering algorithms inside `selection/`.
- **Decoupled Database Repository**: Raw SQL queries MUST NOT appear in domain logic or HTTP handlers. All queries belong strictly in `data/queries.go` methods on `*data.Database`.
- **Broker Independence**: Use `data.BrokerClient` and vendor-agnostic models in `data/broker_models.go`. Never directly import Zerodha SDK in core engine packages.
- **Sub-100ms Latency Budget**: HTTP endpoints must never perform blocking external API calls synchronously. Cache in-memory and dispatch any external catch-up fetches in background goroutines.

### Rule 2: Strict 5-File Configuration Synchronization
Whenever a parameter, threshold, or environment variable is added or changed in `.env`:
1. `.env`: Target value.
2. `.env.example`: Template value.
3. `config/settings.go`: Go struct field and default fallback.
4. `docker-compose.yml`: Forwarded environment variable under `app` service.
5. `README.md`: Risk framework documentation table.

---

## 3. Testing & Verification Gateways

Before committing any feature:

### A. Run Unit & Edge-Case Tests
Always write tests covering zero/negative prices, boundary limits, and typical trading sessions:
```powershell
go test -v ./strategy/... ./selection/... ./data/... ./config/... ./risk/... ./execution/...
```

### B. Verify Clean Local Build
```powershell
go build -o trading-bot.exe .
Remove-Item .\trading-bot.exe -Force
```

---

## 4. Dual Deployment Procedure (Step-by-Step)

### Step 1: Git Commit & Push
```powershell
git add .
git commit -m "feat(<scope>): <concise summary of changes>"
git push origin main
```

### Step 2: Deploy to Primary AWS (`3.7.29.3`)
```powershell
# Pull latest code and rebuild docker container
powershell -ExecutionPolicy Bypass -File .\myaws.ps1 deploy

# Mandatory Go race detector guard (MUST verify 0 race warnings)
powershell -ExecutionPolicy Bypass -File .\myaws.ps1 race

# Confirm container status
powershell -ExecutionPolicy Bypass -File .\myaws.ps1 status
```

### Step 3: Deploy to Secondary VPS (`160.250.204.29`)
```powershell
# Pull latest code and rebuild docker container
powershell -ExecutionPolicy Bypass -File .\myvps.ps1 deploy

# Confirm container status
powershell -ExecutionPolicy Bypass -File .\myvps.ps1 status
```

---

## 5. Operations & CLI Reference

| Action | Primary AWS (`3.7.29.3`) | Secondary VPS (`160.250.204.29`) |
| :--- | :--- | :--- |
| **Check Status** | `.\myaws.ps1 status` | `.\myvps.ps1 status` |
| **Deploy Latest Code** | `.\myaws.ps1 deploy` | `.\myvps.ps1 deploy` |
| **View Recent Logs** | `.\myaws.ps1 logs` | `.\myvps.ps1 logs` |
| **Stream Live Logs** | `.\myaws.ps1 logs -Follow` | `.\myvps.ps1 logs -Follow` |
| **Fast Restart** | `.\myaws.ps1 restart` | `.\myvps.ps1 restart` |
| **Run Race Detector** | `.\myaws.ps1 race` | `.\myvps.ps1 race` |
| **Database Row Counts**| `.\myaws.ps1 db` | `.\myvps.ps1 db` |
| **Open Local Tunnel** | `.\myaws.ps1 tunnel` (`:8080`, `:5432`)| `.\myvps.ps1 tunnel` (`:8081`, `:5433`)|
