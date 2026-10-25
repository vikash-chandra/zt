# VPS Deployment & Operations Guide

This guide documents the complete, step-by-step procedure used to provision, harden, configure, and successfully deploy the **Zerodha Algorithmic Trading Bot & Dashboard** onto the secondary Ubuntu VPS server (`160.250.204.29`).

Every command is tested and verified to ensure a reproducible setup on fresh Ubuntu 20.04/22.04/24.04 nodes without manual intervention or credential leakage.

---

## 1. Server Architecture & Specifications

| Property | Value |
| :--- | :--- |
| **Server IP** | `160.250.204.29` |
| **Default User** | `root` |
| **Operating System** | Ubuntu 20.04.6 LTS (Focal Fossa) |
| **CPU** | 2 vCPU Cores (Intel Xeon E5-2680 v4 @ 2.40GHz) |
| **Physical RAM** | 512 MB (477 MiB usable) |
| **Configured Swap** | 2.0 GB (Total Virtual Memory: ~2.5 GB) |
| **Disk Storage** | 20 GB SSD (/dev/vda1, ~11 GB free after deployment) |
| **Container Runtime**| Docker Engine 28.1.1 + Docker Compose v2.35.1 |
| **Web UI Port** | `8080` (HTTP) |
| **PostgreSQL Port** | `5432` (Internal Docker network / local loopback) |

---

## 2. Step-by-Step Deployment Procedure

### Step 1: Dedicated SSH Key Generation & Permission Hardening (Local Machine)

To guarantee 100% security and eliminate password storage in scripts, a dedicated 4096-bit RSA key pair is created exclusively for this VPS (completely isolated from the AWS instance key `up-trade-vikash.pem`).

#### On Local Windows PowerShell:
```powershell
# 1. Generate dedicated 4096-bit RSA SSH key pair in project root
ssh-keygen -t rsa -b 4096 -f .\vps-vikash.pem -N '""'

# 2. Enforce strict Windows NTFS permissions (Required by OpenSSH)
icacls .\vps-vikash.pem /inheritance:r
icacls .\vps-vikash.pem /remove:g Everyone "Authenticated Users" Users
icacls .\vps-vikash.pem /grant:r "$($env:USERNAME):(R)"

# 3. Verify .gitignore protects the private key
# Ensure vps-vikash.pem and myvps.ps1 are in .gitignore
```

---

### Step 2: Bootstrap SSH Key onto Remote VPS (One-Time Setup)

Install the public key (`vps-vikash.pem.pub`) into the server's `authorized_keys`. Once installed, all future access is cryptographically secure and passwordless.

```bash
# On Remote Server (performed via temporary SSH bootstrap):
mkdir -p /root/.ssh
chmod 700 /root/.ssh

# Append your local vps-vikash.pem.pub content to authorized_keys
cat << 'EOF' >> /root/.ssh/authorized_keys
<YOUR_VPS_VIKASH_PEM_PUB_CONTENT>
EOF

chmod 600 /root/.ssh/authorized_keys
```

#### Test Passwordless SSH Access from Local Machine:
```powershell
ssh -n -i .\vps-vikash.pem -o StrictHostKeyChecking=no root@160.250.204.29 "whoami"
# Expected output: root
```

> **Note:** The `-n` flag detaches `stdin` on Windows PowerShell, preventing SSH sessions from hanging indefinitely.

---

### Step 3: Configure 2.0 GB Swap Memory (Critical for 512 MB RAM Nodes)

With only 477 MB of physical RAM, compiling Go binaries and running TimescaleDB + Web Dashboard simultaneously will trigger Out-Of-Memory (OOM) process termination without swap space. Adding a 2 GB swap file expands total virtual memory to ~2.5 GB.

#### Commands Executed on VPS:
```bash
# 1. Allocate 2 GB swapfile
fallocate -l 2G /swapfile || dd if=/dev/zero of=/swapfile bs=1M count=2048

# 2. Lock down file permissions
chmod 600 /swapfile

# 3. Format and activate swap
mkswap /swapfile
swapon /swapfile

# 4. Make swap permanent across system reboots
echo '/swapfile none swap sw 0 0' >> /etc/fstab

# 5. Tune swappiness for optimal performance (conserves physical RAM)
sysctl vm.swappiness=20
echo 'vm.swappiness=20' >> /etc/sysctl.conf

# 6. Verify active memory and swap
free -h
```

---

### Step 4: Install Docker Engine & Docker Compose Plugin

Install the official Docker CE and Compose plugin for Ubuntu 20.04 (Focal).

#### Commands Executed on VPS:
```bash
# 1. Update APT repository and install prerequisites
apt-get update
apt-get install -y ca-certificates curl gnupg lsb-release git ufw

# 2. Add Docker official GPG key
mkdir -p /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg

# 3. Configure official Docker APT repository
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null

# 4. Install Docker CE packages
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin docker-buildx-plugin

# 5. Enable and start Docker daemon
systemctl enable docker
systemctl start docker

# 6. Verify Docker installation
docker --version
docker compose version
```

---

### Step 5: Configure UFW Firewall

Harden server network security by restricting incoming traffic strictly to SSH (`22`) and Trading Dashboard (`8080`).

#### Commands Executed on VPS:
```bash
# 1. Set default policies
ufw default deny incoming
ufw default allow outgoing

# 2. Allow SSH (port 22) and Web Application (port 8080)
ufw allow 22/tcp
ufw allow 8080/tcp

# 3. Enable firewall
ufw --force enable

# 4. Verify status
ufw status verbose
```

---

### Step 6: Clone Application Repository

Clone the trading bot repository to `/root/zt`.

#### Commands Executed on VPS:
```bash
# 1. Clone repository from GitHub
git clone https://github.com/vikash-chandra/zt.git /root/zt

# 2. Navigate to project root
cd /root/zt
```

---

### Step 7: Configure Environment Variables (`.env`)

Copy and configure your trading bot environment configuration file.

#### Commands Executed:
```bash
# Set host IP to the VPS public IP
# On local machine, pipe .env securely via SSH:
Get-Content .env | ssh -i .\vps-vikash.pem -o StrictHostKeyChecking=no root@160.250.204.29 "cat > /root/zt/.env"

# On remote server, set secure permissions:
chmod 600 /root/zt/.env

# Update host IP in .env on the server:
sed -i 's/^AWS_HOST_IP=.*/AWS_HOST_IP=160.250.204.29/' /root/zt/.env
```

---

### Step 8: Build and Launch Containers

Build the Go trading bot container and start TimescaleDB.

#### Commands Executed on VPS:
```bash
cd /root/zt

# 1. Build and start containers in detached mode
docker compose up -d --build

# 2. Verify container status
docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'

# Expected output:
# NAMES           STATUS                    PORTS
# zt-app-1        Up About a minute         0.0.0.0:8080->8080/tcp, [::]:8080->8080/tcp
# zt-postgres-1   Up About a minute (healthy) 0.0.0.0:5432->5432/tcp, [::]:5432->5432/tcp
```

---

### Step 9: Verify Live Application & Database

Verify HTTP service and database tables:

```bash
# 1. Verify HTTP login redirect
curl -s -I http://localhost:8080/
# Expected: HTTP/1.1 302 Found, Location: /login

# 2. Verify TimescaleDB tables are initialized
docker exec zt-postgres-1 psql -U postgres -d zerodha_trading -c "\dt"

# 3. External check from local machine:
curl.exe -s -I http://160.250.204.29:8080/
```

---

## 3. Daily Operations & Automation: `myvps.ps1`

A dedicated PowerShell utility [`myvps.ps1`](myvps.ps1) is provided for daily management. It mirrors [`myaws.ps1`](myaws.ps1) while targeting the VPS.

### Available Commands:

| Command | Action |
| :--- | :--- |
| `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 status` | Display running containers, CPU/RAM/Swap usage, and disk space |
| `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 logs` | View the last 50 lines of application logs |
| `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 logs -Follow` | Stream real-time live trading engine logs |
| `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 deploy` | Pull latest code from GitHub and rebuild/restart the app container |
| `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 restart` | Restart the trading bot container without rebuilding |
| `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 race` | Run Go Race Detector inside Docker on the VPS |
| `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 db` | Query candle record counts in the remote TimescaleDB |
| `powershell -ExecutionPolicy Bypass -File .\myvps.ps1 tunnel` | Open encrypted SSH tunnel for local port forwarding (`8081` -> Web, `5433` -> DB) |

---

## 4. Key Differences: AWS Instance vs VPS Instance

| Feature | AWS Instance (`3.7.29.3`) | VPS Instance (`160.250.204.29`) |
| :--- | :--- | :--- |
| **SSH Key** | [`up-trade-vikash.pem`](up-trade-vikash.pem) | [`vps-vikash.pem`](vps-vikash.pem) |
| **Default User** | `ubuntu` | `root` |
| **Application Path**| `/home/ubuntu/zt` | `/root/zt` |
| **Management CLI** | [`.\myaws.ps1`](myaws.ps1) | [`.\myvps.ps1`](myvps.ps1) |
| **Memory Config** | 1.0 GB RAM + 2.0 GB Swap | 512 MB RAM + 2.0 GB Swap |
| **Public Dashboard**| `http://3.7.29.3:8080/` | `http://160.250.204.29:8080/` |

---

## 5. Security & Isolation Summary

1. **Zero Credential Exposure**: Passwords are never committed to GitHub or hardcoded into scripts.
2. **Key Isolation**: The AWS private key and the VPS private key are separate and non-interchangeable.
3. **Repository Protection**: [`.gitignore`](.gitignore) excludes `vps-vikash.pem`, `vps-vikash.pem.pub`, and `myvps.ps1`.
4. **Firewall Protection**: Only ports 22 and 8080 are exposed to the public internet. Database port 5432 is restricted.
