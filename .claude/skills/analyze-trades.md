---
name: analyze-trades
description: Analyze trade performance and generate insights
usage: /analyze-trades [period]
---

# Trade Analysis Skill

Analyzes executed trades from the database to provide performance insights.

## Usage

```
/analyze-trades today
/analyze-trades 7d
/analyze-trades 30d
/analyze-trades all
```

## What it does

1. Queries PostgreSQL `trades` table for completed trades
2. Calculates:
   - Win rate and profit factor
   - Average reward:risk ratio
   - Best/worst performing symbols
   - Time-based performance (hourly/daily patterns)
3. Identifies:
   - Winning vs losing strategy conditions
   - Common exit reasons
   - Position size effectiveness
4. Generates actionable insights for strategy improvement

## Output

- Trade summary table
- Performance heatmap by time
- Strategy effectiveness breakdown
- Recommendations for parameter tuning