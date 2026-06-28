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
3. Validate configuration:
   - Risk parameters are sensible
   - Stop-loss logic is consistent
   - Position sizing math is correct
4. Report any violations or warnings.
