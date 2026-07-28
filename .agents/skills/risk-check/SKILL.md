---
name: risk-check
description: Validate risk parameters and check current exposure
---
# Risk Check Skill

Analyzes current risk exposure and validates trading parameters.

## Usage
- Ask the agent to check risk or validate risk parameters.

## Implementation Steps for Agent
1. Compare current positions vs risk limits.
2. Check:
   - Daily P&L vs max loss threshold
   - Position concentration per symbol
   - Total exposure vs capital
   - Open trade count vs daily limit
   - High-Water Mark Trailing SL stages (+0.8% -> +0.2%, +1.4% -> +0.7%, +2.0% -> +1.0% & 60% partial exit, >+2.5% -> peak-1.0%)
   - 45-minute time decay profit lock (+0.4% gain held > 45m -> +0.2% locked)
3. Validate configuration:
   - Risk parameters are sensible
   - Stop-loss logic is consistent
   - Position sizing math is correct
4. Report any violations or warnings.
