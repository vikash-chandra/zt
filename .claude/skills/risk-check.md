---
name: risk-check
description: Validate risk parameters and check current exposure
usage: /risk-check
---

# Risk Check Skill

Analyzes current risk exposure and validates trading parameters.

## Usage

```
/risk-check
```

## What it does

1. Compares current positions vs risk limits
2. Checks:
   - Daily P&L vs max loss threshold
   - Position concentration per symbol
   - Total exposure vs capital
   - Open trade count vs daily limit
3. Validates configuration:
   - Risk parameters are sensible
   - Stop-loss logic is consistent
   - Position sizing math is correct
4. Reports any violations or warnings

## Output

- Current exposure summary
- Risk limit status (green/yellow/red)
- Any active circuit breakers
- Recommendations for adjustments