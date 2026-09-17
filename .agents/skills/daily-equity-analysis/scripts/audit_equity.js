#!/usr/bin/env node
/**
 * Daily Equity Stock Deep Audit & Missed-Trade Analyzer
 * 
 * Usage: node audit_equity.js [YYYY-MM-DD]
 * Example: node audit_equity.js 2026-09-17
 */

const { execSync } = require('child_process');
const path = require('path');

const targetDate = process.argv[2] || new Date().toISOString().split('T')[0];
const pemPath = path.resolve(__dirname, '../../../../up-trade-vikash.pem');

function runBash(cmdStr) {
    const b64 = Buffer.from(cmdStr).toString('base64');
    const cmd = `ssh -i "${pemPath}" -o StrictHostKeyChecking=no ubuntu@3.7.29.3 "echo ${b64} | base64 -d | bash"`;
    try {
        return execSync(cmd, { encoding: 'utf-8', maxBuffer: 30 * 1024 * 1024 });
    } catch (e) {
        return `Error: ${e.message}\nOutput: ${e.stdout}\nStderr: ${e.stderr}`;
    }
}

function runSql(sql) {
    const b64 = Buffer.from(sql).toString('base64');
    return runBash(`echo ${b64} | base64 -d | docker exec -i zt-postgres-1 psql -U postgres -d zerodha_trading -A -t`);
}

console.log(`========================================================================`);
console.log(`🔍 DAILY EQUITY DEEP AUDIT & MISSED TRADE REPORT: ${targetDate}`);
console.log(`========================================================================\n`);

// 1. Executed Equity Trades
console.log(`📊 1. EXECUTED EQUITY TRADES (${targetDate})`);
console.log(`------------------------------------------------------------------------`);
const tradesSql = `
    SELECT id, symbol, strategy, side, quantity, entry_price, exit_price, pnl, status, time_held_minutes, entry_time, exit_time
    FROM trades 
    WHERE (entry_time >= '${targetDate} 00:00:00+05:30' OR exit_time >= '${targetDate} 00:00:00+05:30')
      AND strategy != 'OPTIONS_SUPERTREND'
    ORDER BY entry_time ASC;
`;
const tradeRows = runSql(tradesSql).trim().split('\n').filter(r => r.length > 0);
if (tradeRows.length === 0) {
    console.log(`No equity trades executed today.\n`);
} else {
    tradeRows.forEach(r => {
        const p = r.split('|');
        const pnl = parseFloat(p[7]);
        const pnlStr = pnl >= 0 ? `+₹${pnl.toFixed(2)}` : `-₹${Math.abs(pnl).toFixed(2)}`;
        console.log(`• [${p[2]}] ${p[1]} | ${p[3]} ${p[4]} shares @ ₹${parseFloat(p[5]).toFixed(2)} -> Exit: ₹${parseFloat(p[6]).toFixed(2)} | P&L: ${pnlStr} | Status: ${p[8]} (${p[9]} mins)`);
    });
    console.log('');
}

// 2. Watchlist Candidates
console.log(`📋 2. WATCHLIST CANDIDATES AUDITED (${targetDate})`);
console.log(`------------------------------------------------------------------------`);
const wlSql = `
    SELECT DISTINCT symbol, token, selectors FROM (
        SELECT symbol, token, selectors FROM daily_watchlists WHERE date = '${targetDate}'
        UNION
        SELECT symbol, token, selectors FROM daily_watchlists WHERE date >= '${targetDate}'::date - INTERVAL '7 days'
    ) s ORDER BY symbol ASC;
`;
const wlRows = runSql(wlSql).trim().split('\n').filter(r => r.length > 0);
const candidates = [];
wlRows.forEach(r => {
    const p = r.split('|');
    if (p[0] && p[1] && p[1] !== '0') {
        candidates.push({ symbol: p[0], token: parseInt(p[1]), selectors: p[2] || '' });
    }
});
console.log(`Total candidate symbols with historical tracking: ${candidates.length}\n`);

// 3. Candle-by-Candle Strategy Evaluation
console.log(`🔬 3. STRATEGY AUDIT MATRIX`);
console.log(`------------------------------------------------------------------------`);
console.log(`${"SYMBOL".padEnd(12)} | ${"PDH".padEnd(8)} | ${"C1 CLOSE".padEnd(9)} | ${"C1 QUAL".padEnd(9)} | ${"C2 CONF".padEnd(9)} | ${"TRIGGER".padEnd(9)} | ${"VERDICT / REASON"}`);
console.log(`------------------------------------------------------------------------`);

candidates.forEach(cand => {
    const token = cand.token;
    const symbol = cand.symbol;

    // Get PDH/PDL/PDC
    const pdSql = `
        SELECT high, low, close FROM candles_1d 
        WHERE token = ${token} AND time < '${targetDate} 00:00:00+05:30' 
        ORDER BY time DESC LIMIT 1;
    `;
    const pdRow = runSql(pdSql).trim();
    let pdh = 0, pdl = 0, pdc = 0;
    if (pdRow) {
        const parts = pdRow.split('|');
        pdh = parseFloat(parts[0]);
        pdl = parseFloat(parts[1]);
        pdc = parseFloat(parts[2]);
    }

    // Get first 3 candles of the day
    const candlesSql = `
        SELECT time, open, high, low, close, volume 
        FROM candles_5m 
        WHERE token = ${token} AND time >= '${targetDate} 09:15:00+05:30' AND time <= '${targetDate} 09:30:00+05:30'
        ORDER BY time ASC LIMIT 3;
    `;
    const cRows = runSql(candlesSql).trim().split('\n').filter(r => r.length > 0);
    if (cRows.length < 2) return;

    const candles = cRows.map(r => {
        const p = r.split('|');
        return {
            time: p[0],
            open: parseFloat(p[1]),
            high: parseFloat(p[2]),
            low: parseFloat(p[3]),
            close: parseFloat(p[4]),
            volume: parseInt(p[5])
        };
    });

    const c1 = candles[0];
    const c2 = candles[1];
    const c3 = candles.length >= 3 ? candles[2] : null;

    const c1Green = c1.close > c1.open;
    const c1Red = c1.close < c1.open;
    const c1Range = ((c1.high - c1.low) / c1.open) * 100;
    const c1Body = Math.abs(c1.close - c1.open);
    const c1TotalRange = c1.high - c1.low;
    const c1WickPct = c1TotalRange > 0 ? ((c1TotalRange - c1Body) / c1TotalRange) * 100 : 0;

    let c1Qual = false;
    let qualDir = "";
    if (c1Green && c1.close > pdh && c1Range <= 3.0 && c1WickPct <= 60.0) {
        c1Qual = true;
        qualDir = "BUY";
    } else if (c1Red && c1.close < pdl && c1Range <= 3.0 && c1WickPct <= 60.0) {
        c1Qual = true;
        qualDir = "SELL";
    }

    let c2Conf = false;
    let trigPrice = 0;
    if (c1Qual && qualDir === "BUY") {
        if (c2.high > c1.high && c2.close > c2.open) {
            c2Conf = true;
            trigPrice = c2.high;
        }
    } else if (c1Qual && qualDir === "SELL") {
        if (c2.low < c1.low && c2.close < c2.open) {
            c2Conf = true;
            trigPrice = c2.low;
        }
    }

    let triggered = false;
    if (c2Conf && c3) {
        if (qualDir === "BUY" && c3.high > trigPrice) triggered = true;
        if (qualDir === "SELL" && c3.low < trigPrice) triggered = true;
    }

    let verdict = "";
    if (triggered) {
        verdict = "✅ TRIGGERED & FILLED";
    } else if (c2Conf) {
        verdict = "ARMED (No C3 Trigger)";
    } else if (c1Qual) {
        verdict = `C2 Failed (${c2.close <= c2.open ? 'Wrong Color' : 'No High Break'})`;
    } else if (c1.close <= pdh && c1.close >= pdl) {
        verdict = "Disqualified: C1 Inside PDH/PDL";
    } else if (c1WickPct > 60.0) {
        verdict = `Disqualified: Excess Wick (${c1WickPct.toFixed(1)}% > 60%)`;
    } else if (c1Range > 3.0) {
        verdict = `Disqualified: Range Exceeded (${c1Range.toFixed(2)}% > 3%)`;
    } else {
        verdict = "Disqualified: Criteria Not Met";
    }

    console.log(`${symbol.padEnd(12)} | ${pdh.toFixed(2).padEnd(8)} | ${c1.close.toFixed(2).padEnd(9)} | ${(c1Qual ? qualDir : 'NO').padEnd(9)} | ${(c2Conf ? 'YES' : 'NO').padEnd(9)} | ${(triggered ? 'YES' : 'NO').padEnd(9)} | ${verdict}`);
});

console.log(`\n========================================================================\n`);
