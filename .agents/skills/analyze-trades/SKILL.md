---
name: analyze-trades
description: Analyze trade performance and generate insights
---
# Trade Analysis Skill

Analyzes executed trades from the database to provide performance insights.

## Usage
- Ask the agent to analyze trade performance.
- Ask for win rate, profit factor, drawdown, or reward-to-risk ratio.

## Implementation Steps for Agent
1. Query the PostgreSQL `trades` table for completed trades.
   - For remote AWS query: Run `.\myaws.ps1 db` or run raw psql queries inside the `zt-postgres-1` database container via SSH.
2. Calculate:
   - Win rate and profit factor
   - Average reward:risk ratio
   - Best/worst performing symbols
   - Time-based performance (hourly/daily patterns)
3. Identify:
   - Winning vs losing strategy conditions
   - Common exit reasons
   - Position size effectiveness
4. Generate actionable insights for strategy improvement.

