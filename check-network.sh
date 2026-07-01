#!/bin/bash

# Clear terminal screen
clear

echo "============================================="
echo "   Zerodha Trading Bot Network Health Check   "
echo "============================================="
echo ""

# 1. Check System Level IPv4 Routing Address
echo "Checking Outbound IPv4 Routing..."
CURRENT_IP=$(curl -s --max-time 5 https://ipify.org)

if [ -z "$CURRENT_IP" ]; then
    echo "❌ Error: Could not reach the IPv4 verification endpoint."
else
    echo "✅ Success! System Outbound IPv4 is: $CURRENT_IP"
    if [ "$CURRENT_IP" == "3.7.29.3" ]; then
        echo "👉 [MATCH] This matches your primary AWS whitelisted IP."
    else
        echo "⚠️  [ALERT] This does not match 3.7.29.3. Double check your Elastic IP."
    fi
fi
echo "---------------------------------------------"

# 2. Check for Leaking IPv6 Pathways
echo "Checking for Active IPv6 Pathways..."
IPV6_LEAK=$(curl -s --max-time 3 https://ipify.org 2>/dev/null)

if [ -n "$IPV6_LEAK" ]; then
    echo "❌ CRITICAL WARNING: IPv6 leakage detected!"
    echo "   Leaked Address: $IPV6_LEAK"
    echo "   Zerodha will reject your orders. Please run the disable-ipv6 steps."
else
    echo "✅ Success! IPv6 is completely disabled at system level."
fi
echo "---------------------------------------------"

# 3. Check Docker Engine Engine Status
echo "Checking Docker Daemon Network Restrictions..."
if [ -f /etc/docker/daemon.json ]; then
    if grep -q '"ipv6": false' /etc/docker/daemon.json; then
        echo "✅ Success! Docker daemon config forces IPv4."
    else
        echo "⚠️  Warning: Docker config found but 'ipv6: false' is missing."
    fi
else
    echo "❌ Error: /etc/docker/daemon.json file is completely missing."
fi
echo "---------------------------------------------"

# 4. Check Active Docker Infrastructure Containers
echo "Checking Active App Container Instances..."
if command -v docker &> /dev/null; then
    RUNNING_CONTAINERS=$(docker ps --format "{{.Names}} ({{.Status}})")
    if [ -z "$RUNNING_CONTAINERS" ]; then
        echo "⚠️  Alert: No containers are currently running. Run your 'cup' shortcut."
    else
        echo "🏃 Active Containers:"
        echo "$RUNNING_CONTAINERS"
    fi
else
    echo "❌ Error: Docker engine daemon is not installed properly."
fi

echo "============================================="

