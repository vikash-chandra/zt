# 🛠️ CLI Guide: Running Backtests & Seeding Trades

This guide contains the commands to run the strategy backtest simulation and seed the resulting trades into the database manually from the PowerShell command line.

---

## 📊 1. Run the Backtest Simulation
This will simulate both `LOW_VOLUME` and `VANDE_BHARAT` strategies over the last 6 trading days (including today) using cached candles, applying 5x leverage, and reading the daily bias directly from the database.

```powershell
# Set the directory where the backtest markdown report will be saved
$env:ARTIFACT_DIR="C:\Users\Dell\.gemini\antigravity-cli\brain\03b85694-13f2-4638-8194-90d614327607"

# Execute the simulation
go run scripts/backtest/main.go
```

The resulting markdown report will be generated at:
`C:\Users\Dell\.gemini\antigravity-cli\brain\03b85694-13f2-4638-8194-90d614327607\backtest_report.md`

---

## 🗄️ 2. Seed Simulated Trades into the Database
This clears any existing trades for today in the database and seeds today's backtest trades so they populate the web dashboard metrics and log.

```powershell
# Execute the database seeding script
go run C:\Users\Dell\.gemini\antigravity-cli\brain\03b85694-13f2-4638-8194-90d614327607\scratch\seed_today_trades.go
```
