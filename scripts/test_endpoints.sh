#!/bin/bash
endpoints=(
    "watchlist"
    "candles?token=256265"
    "trades?date=2026-10-06"
    "trades/all"
    "manual-watchlist"
    "daily-watchlists"
    "config/all"
    "config/runtime-audit"
    "positions"
    "options/state"
    "options/indices"
    "options/supertrends"
    "options/expected-move"
    "scanner/dates"
    "scanner/results"
    "seeder/status"
    "sectors"
    "strategy/events"
    "manual-trades/status"
    "footprints/recent"
)

echo "=== TESTING ALL REAL BOT REST API ENDPOINTS ==="
pass_count=0
fail_count=0

for ep in "${endpoints[@]}"; do
    res=$(curl -s -w "%{http_code}|%{size_download}|%{time_total}" -o /tmp/resp.json "http://localhost:8080/api/$ep")
    code=$(echo "$res" | cut -d'|' -f1)
    size=$(echo "$res" | cut -d'|' -f2)
    time=$(echo "$res" | cut -d'|' -f3)
    if [ "$code" == "200" ]; then
        echo -e "✅ PASS: /api/$ep -> HTTP $code | $size bytes | ${time}s"
        ((pass_count++))
    else
        echo -e "❌ FAIL: /api/$ep -> HTTP $code | $size bytes | ${time}s"
        ((fail_count++))
    fi
done

echo ""
echo "=== SUMMARY: $pass_count PASSED, $fail_count FAILED ==="
