#!/bin/bash

echo "============================================="
echo "  Executing Automated Network Fix & Cleanup  "
echo "============================================="
echo ""

# 1. Force Clean Public DNS Servers (Solves the 302 Interception error)
echo "Resetting System DNS Resolvers to Google Secure DNS..."
sudo mkdir -p /etc/resolved.conf.d/
echo -e "nameserver 8.8.8.8\nnameserver 8.8.4.4" | sudo tee /etc/resolv.conf > /dev/null
echo "✅ DNS Overrides applied."

# 2. Block IPv6 Globally at System Kernel Level
echo "Forcing System Kernel to Drop IPv6 Rules..."
sudo sed -i '/disable_ipv6/d' /etc/sysctl.conf
echo "net.ipv6.conf.all.disable_ipv6 = 1" | sudo tee -a /etc/sysctl.conf > /dev/null
echo "net.ipv6.conf.default.disable_ipv6 = 1" | sudo tee -a /etc/sysctl.conf > /dev/null
echo "net.ipv6.conf.lo.disable_ipv6 = 1" | sudo tee -a /etc/sysctl.conf > /dev/null
sudo sysctl -p > /dev/null
echo "✅ Kernel IPv6 block completed."

# 3. Secure Docker Configuration Layout
echo "Configuring Docker Engine Core Settings..."
sudo mkdir -p /etc/docker
echo -e '{\n  "ipv6": false\n}' | sudo tee /etc/docker/daemon.json > /dev/null
echo "✅ Docker daemon constraints written."

# 4. Cycle Global Network and Engine Processes
echo "Restarting Docker Service Engines..."
sudo systemctl restart docker
echo "✅ Services recycled."

echo ""
echo "============================================="
echo "             Testing Live Fixes              "
echo "============================================="

# 5. Check Pure Public Routing IPs Using Alternative Non-Interceptible APIs
echo "Testing Direct Plaintext Public IP Address Allocation..."
RAW_IPV4=$(curl -s --max-time 5 https://ifconfig.me)
echo "Current Real Outbound IP: $RAW_IPV4"
echo "============================================="

