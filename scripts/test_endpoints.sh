#!/bin/bash
endpoints=(
    "status"
    "positions"
    "trades"
    "trades/all"
    "watchlist"
    "options/state"
    "options/supertrends"
    "options/trade-log"
    "config"
    "config/runtime-audit"
    "system/controls"
    "strategies/performance"
    "expected-move"
    "telemetry/timeline"
)

echo "=== TESTING ALL BOT REST API ENDPOINTS ==="
for ep in "${endpoints[@]}"; do
    res=$(curl -s -w "%{http_code}|%{size_download}|%{time_total}" -o /tmp/resp.json "http://localhost:8080/api/$ep")
    code=$(echo "$res" | cut -d'|' -f1)
    size=$(echo "$res" | cut -d'|' -f2)
    time=$(echo "$res" | cut -d'|' -f3)
    if [ "$code" == "200" ]; then
        echo -e "PASS: /api/$ep -> HTTP $code | $size bytes | ${time}s"
    else
        echo -e "FAIL: /api/$ep -> HTTP $code | $size bytes | ${time}s"
    fi
done
