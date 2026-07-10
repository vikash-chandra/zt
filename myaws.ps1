param(
    [string]$Action = "status",
    [switch]$Follow
)

$HostIP = "3.7.29.33" # Default fallback

# Dynamically load AWS_HOST_IP from local .env if available
if (Test-Path ".env") {
    $envContent = Get-Content ".env"
    foreach ($line in $envContent) {
        if ($line -match "^\s*AWS_HOST_IP\s*=\s*(.+)\s*$") {
            $HostIP = $Matches[1].Trim()
            break
        }
    }
}

$User = "ubuntu"
$Key = "up-trade-vikash.pem"
$SSH_CMD = "ssh -i $Key -o StrictHostKeyChecking=no ${User}@${HostIP}"

# Ensure correct key permissions on Windows
& icacls $Key /inheritance:r | Out-Null
& icacls $Key /grant:r "$($env:USERNAME):(R)" | Out-Null

switch ($Action) {
    "status" {
        Write-Host "=== Fetching AWS Server Status ($HostIP) ===" -ForegroundColor Cyan
        Write-Host "1. Running Containers:" -ForegroundColor Yellow
        Invoke-Expression "$SSH_CMD 'docker ps --format ""table {{.Names}}\t{{.Status}}\t{{.Ports}}""'"
        
        Write-Host "`n2. System Resource Usage:" -ForegroundColor Yellow
        Invoke-Expression "$SSH_CMD 'free -h && df -h /'"
    }
    "logs" {
        Write-Host "=== Fetching Application Logs ===" -ForegroundColor Cyan
        $TailCmd = "docker ps --filter name=app --format '{{.Names}}' | xargs -I {} docker logs --tail 50 {}"
        if ($Follow) {
            $TailCmd = "docker ps --filter name=app --format '{{.Names}}' | xargs -I {} docker logs -f --tail 50 {}"
        }
        Invoke-Expression "$SSH_CMD `"$TailCmd`""
    }
    "tunnel" {
        Write-Host "=== Opening Secure SSH Tunnels to AWS ===" -ForegroundColor Green
        Write-Host "  - Local Port 8080 -> Remote App (Dashboard)" -ForegroundColor Gray
        Write-Host "  - Local Port 5432 -> Remote Database (TimescaleDB)" -ForegroundColor Gray
        Write-Host "Press Ctrl+C to close the tunnels." -ForegroundColor Yellow
        Invoke-Expression "ssh -i $Key -N -L 8080:127.0.0.1:8080 -L 5432:127.0.0.1:5432 ${User}@${HostIP}"
    }
    "db" {
        Write-Host "=== Querying Remote Database Candle Count ===" -ForegroundColor Cyan
        $DbQuery = "docker ps --filter name=db --format '{{.Names}}' | xargs -I {} docker exec -t {} psql -U postgres -d zerodha_trading -c 'SELECT COUNT(*) AS candles_5m_count FROM candles_5m; SELECT COUNT(*) AS candles_1m_count FROM candles_1m;'"
        Invoke-Expression "$SSH_CMD `"$DbQuery`""
    }
    "restart" {
        Write-Host "=== Restarting Remote Docker Services ===" -ForegroundColor Red
        # Assumes docker-compose is in ~/zt or runs compose commands in zt folder if found
        $RestartCmd = "if [ -d ~/zt ]; then cd ~/zt && docker compose restart; else docker ps -q | xargs -I {} docker restart {}; fi"
        Invoke-Expression "$SSH_CMD `"$RestartCmd`""
    }
    default {
        Write-Host "Unknown action: $Action" -ForegroundColor Red
        Write-Host "Available actions: status, logs, tunnel, db, restart" -ForegroundColor Yellow
        Write-Host "Usage: .\myaws.ps1 [action] [-Follow]" -ForegroundColor Yellow
    }
}
