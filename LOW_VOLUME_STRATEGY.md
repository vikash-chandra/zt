# LOW_VOLUME Breakout Trading Strategy: Complete Specifications

This document outlines the core architecture, operational rules, mathematical formulas, and scenario walkthroughs for the **LOW_VOLUME** breakout trading strategy.

---

## 1. Core Rules & Parameter Configuration

### A. Pre-Market Bias Selection (09:29:00 AM IST)
The daily market direction is determined by scanning the price changes of Nifty 50 constituents relative to their opens:
* **Advances (A)**: Stock LTP > Open Price.
* **Declines (D)**: Stock LTP < Open Price.
* **Bias Rules**:
  * If $Advances > Declines$, Bias = **`BUY_ONLY`** (only take Long positions).
  * Otherwise, Bias = **`SELL_ONLY`** (only take Short positions; there are no idle/no-trade days).

### B. Watchlist Filtering (09:30:00 AM IST)
The bot scans all active F&O underlying stocks (typically 180+ liquid tickers) and ranks them to select a **Top 10 watchlist**:
* If `BUY_ONLY`, it selects the top gainers since open.
* If `SELL_ONLY`, it selects the top losers since open.
* **Watchlist Limit**: Stocks are only eligible if their absolute percentage change since open is **$\le 2.5\%$** (customizable via `WATCHLIST_MAX_PCT_CHANGE` in `.env`). This prevents chasing overextended stocks.

### C. Setup Candle Identification
The Setup Candle is determined dynamically at the close of every 5-minute candle since 09:15 AM today:
* It is the completed 5-minute candle with the **absolute lowest trading volume** since 09:15 AM.
* The bot records the Setup Candle's **High**, **Low**, and **Color** (RED if Close < Open, GREEN if Close > Open).

### D. Next-Candle Breakout Constraint (Trigger Window)
Breakout entries are **only** valid during the **single 5-minute candle immediately following** the Setup Candle's completion:
* Let the Setup Candle start at $T_{start}$ and end at $T_{close}$ ($T_{start} + 5\text{ minutes}$).
* The breakout can **only** trigger during the window $[T_{close}, T_{close} + 5\text{ minutes})$.
* If the price does not break out during this next 5-minute period, the setup becomes invalid. This ensures trades are only taken on immediate momentum.
* > [!IMPORTANT]
  > **Trading starts strictly after 09:30:00 AM IST**. Watchlist filtering and live tick subscriptions run at 09:30:00 AM. Any breakouts that occur prior to 09:30:00 AM (for example, on setup candles completed at 09:20 AM or 09:25 AM) are completely ignored since the system is not yet active.

### E. Dynamic Position Sizing
Quantity is computed dynamically to utilize the maximum broker leverage allowed for the stock on Zerodha. It queries Zerodha's live `GetOrderMargins` API for 1 share of the target stock (MIS product type) to retrieve the exact margin requirement (`marginPerShare`):
$$\text{Quantity} = \lfloor \frac{\text{MAX\_CAPITAL\_PER\_TRADE}}{\text{marginPerShare}} \rfloor$$
* **Live Leverage**: Calculated dynamically as $\text{Price} / \text{marginPerShare}$. For example, if a stock price is 1,000 Rs and Zerodha requires 200 Rs margin (5x leverage), a 20,000 Rs allocation will buy $\lfloor 20000 / 200 \rfloor = 100$ shares (exposure of 100,000 Rs).
* **Fallback**: If the margins API call fails or is running offline, it defaults to a standard 5x leverage multiplier.
* If $\text{Quantity} = 0$, the trade is skipped.

### F. Target, Stop-Loss, and Trailing Rules
Once entered at the breakout price (Setup High for Long, Setup Low for Short):
* **Original Risk** = $| \text{Entry Price} - \text{Setup Candle Opposite Bound} |$
* **Buffered Risk** = $\text{Original Risk} \times 1.2$ (adds a 20% buffer to prevent premature stops on market noise).
* **Stop-Loss (SL)**:
  * For Long: $Entry - Buffered\ Risk$
  * For Short: $Entry + Buffered\ Risk$
* **Target 1**:
  * For Long: $Entry + (Buffered\ Risk \times 2)$ (1:2 Risk-to-Reward ratio)
  * For Short: $Entry - (Buffered\ Risk \times 2)$
* **Target 1 Execution**:
  * When price touches Target 1, **50% of the shares** are immediately closed at market price.
  * The Stop-Loss for the remaining 50% shares is immediately trailed to the **Entry Price** (breakeven cost-to-cost).

---

## 2. Walkthrough Scenarios

### Scenario 1: Long Entry Triggered & Target 1 Hit
* **Market Bias**: `BUY_ONLY` (Advances: 32, Declines: 18)
* **Symbol**: `MOTILALOFS` (Price: 1,000 Rs, % change: +1.50% at 09:30 AM - Eligible).
* **Setup Candle**: Formed from 09:25 AM to 09:30 AM. It is the lowest volume candle of the day so far (Volume: 5,000). It is a **RED** candle (Open: 1001, Close: 999, High: 1002, Low: 998).
* **Constraint Check**: Next-candle period is 09:30 AM to 09:35 AM (valid since the bot is active).
* **Price Action**: At 09:32 AM, live LTP hits **1,002.50** (crossing above Setup High of 1002.00).
* **Execution**:
  * **marginPerShare**: $1002.50 / 5.0 = 200.50$ Rs (based on 5x leverage).
  * **Quantity**: $\lfloor 20000 / 200.50 \rfloor = \mathbf{99\text{ shares}}$ (instead of 19 shares without leverage).
  * **Original Risk**: $1002.50 - 998 = 4.50$ Rs.
  * **Buffered Risk**: $4.50 \times 1.2 = 5.40$ Rs.
  * **Stop-Loss (SL)**: $1002.50 - 5.40 = \mathbf{997.10\text{ Rs}}$.
  * **Target 1**: $1002.50 + (5.40 \times 2) = \mathbf{1013.30\text{ Rs}}$.
* **Outcome**: Price rises to 1013.50 at 09:34 AM. Bot immediately sells **49 shares** (50% of 99) at 1013.30 locking in **+529.20 Rs** profit ($49 \times 10.80$ Rs gain) and trails stop-loss for the remaining 50 shares to **1002.50** (risk-free).

---

### Scenario 2: Short Entry Triggered & Hit SL
* **Market Bias**: `SELL_ONLY` (Advances: 15, Declines: 35)
* **Symbol**: `VEDL` (Price: 300 Rs, % change: -1.20% at 09:30 AM - Eligible).
* **Setup Candle**: Formed from 09:25 AM to 09:30 AM. Lowest volume of the morning (Volume: 20,000). It is a **GREEN** candle (Open: 298, Close: 300, High: 301, Low: 297).
* **Constraint Check**: Next-candle period is 09:30 AM to 09:35 AM.
* **Price Action**: At 09:31 AM, live LTP drops to **296.80** (crossing below Setup Low of 297.00).
* **Execution**:
  * **marginPerShare**: $296.80 / 5.0 = 59.36$ Rs (based on 5x leverage).
  * **Quantity**: $\lfloor 20000 / 59.36 \rfloor = \mathbf{336\text{ shares}}$ (instead of 67 shares without leverage).
  * **Original Risk**: $301 - 296.80 = 4.20$ Rs.
  * **Buffered Risk**: $4.20 \times 1.2 = 5.04$ Rs.
  * **Stop-Loss (SL)**: $296.80 + 5.04 = \mathbf{301.84\text{ Rs}}$.
  * **Target 1**: $296.80 - (5.04 \times 2) = \mathbf{286.72\text{ Rs}}$.
* **Outcome**: Market rebounds, price hits 302.00 at 09:34 AM. Stop-loss triggers, closing all 336 shares for a loss of **-1693.44 Rs** ($336 \times -5.04$ Rs).

---

### Scenario 3: Ignored due to Next-Candle Rule (Time Exceeded)
* **Market Bias**: `BUY_ONLY`
* **Symbol**: `LUPIN`
* **Setup Candle**: Formed at 09:15 AM–09:20 AM (RED candle, High: 1500, Low: 1480, lowest volume).
* **Constraint Check**: Breakout must occur during the next candle (09:20 AM to 09:25 AM).
* **Price Action**: 
  * From 09:20 AM to 09:25 AM, price consolidates between 1485 and 1495. No breakout triggers.
  * At 09:30 AM, price breaks above **1501.00**.
* **Outcome**: **IGNORED**. Even though price crossed the Setup High, the breakout occurred during the 09:25–09:30 candle, which is more than 5 minutes after the Setup Candle ended. The setup is expired.

---

### Scenario 4: Ignored due to Setup Color Mismatch
* **Market Bias**: `BUY_ONLY`
* **Symbol**: `TRENT`
* **Setup Candle**: Formed at 09:20 AM–09:25 AM. Volume is the lowest of the morning. However, the candle closed **GREEN** (Open: 3200, Close: 3205).
* **Price Action**: At 09:26 AM, price breaks above the Setup High (`3206.00`).
* **Outcome**: **IGNORED**. In a `BUY_ONLY` market bias, the Setup Candle must be **RED** (signaling volume exhaustion on selling pressure before a bullish reversal). A breakout above a GREEN setup candle is bypassed.

---

### Scenario 5: Ignored due to Watchlist Filter (% Change Limit)
* **Market Bias**: `BUY_ONLY`
* **Symbol**: `DELHIVERY`
* **Price Action**: At 09:30 AM, `DELHIVERY` has surged by **+2.85%** since market open.
* **Setup Candle**: Formed at 09:20 AM–09:25 AM (RED candle, lowest volume).
* **Price Action**: At 09:26 AM, price breaks above the Setup High.
* **Outcome**: **IGNORED**. Because the stock's morning gain (+2.85%) exceeded the maximum filter of **2.50%**, it was excluded from the Top 10 watchlist at 09:30:00. The bot is not subscribed to its ticks, so the breakout is ignored.

---

### Scenario 6: Ignored due to Sizing Limit (Price > Capital)
* **Market Bias**: `BUY_ONLY`
* **Symbol**: `MRF`
* **Price**: 120,000 Rs per share.
* **Setup Candle**: Formed at 09:20 AM–09:25 AM (RED candle, lowest volume, High: 120,100).
* **Price Action**: At 09:26 AM, price breaks above 120,150.
* **Outcome**: **IGNORED**. The dynamic quantity formula calculates:
  Since the cost of a single share exceeds the maximum capital allocation per trade (20,000 Rs), the bot calculates a quantity of 0 and bypasses the entry.

---

### Scenario 7: Sequence of Ignores (8 Candles Ignored + 1 Mismatch Ignored, then Trade Executed)
* **Market Bias**: `BUY_ONLY`
* **Symbol**: `MOTILALOFS` (Price: 1,000 Rs)
* **Setup Candle**: The initial lowest-volume candle of the day is the `09:25–09:30` candle (RED, Volume: 10,000).
* **The 8-Candle Ignore Sequence (09:30 AM to 10:10 AM)**:
  1. **Candle 1 (09:30–09:35)**: Volume is 12,000. No new lowest-volume setup is formed. Price consolidates within the setup bounds. The setup expires at 09:35. (**Ignored 1**)
  2. **Candle 2 (09:35–09:40)**: Volume is 15,000. No new setup. The `09:25-09:30` candle remains the lowest volume, but its next-candle breakout window has already passed. (**Ignored 2**)
  3. **Candle 3 (09:40–09:45)**: Volume is 14,000. Setup is expired, no trade is possible. (**Ignored 3**)
  4. **Candle 4 (09:45–09:50)**: Volume is 11,000. Setup is expired, no trade is possible. (**Ignored 4**)
  5. **Candle 5 (09:50–09:55)**: Volume is 18,000. Setup is expired, no trade is possible. (**Ignored 5**)
  6. **Candle 6 (09:55–10:00)**: Volume is 13,000. Setup is expired, no trade is possible. (**Ignored 6**)
  7. **Candle 7 (10:00–10:05)**: Volume is 12,500. Setup is expired, no trade is possible. (**Ignored 7**)
  8. **Candle 8 (10:05–10:10)**: Volume is 16,000. Setup is expired, no trade is possible. (**Ignored 8**)
* **The Color Mismatch Ignore (10:10 AM to 10:15 AM)**:
  9. **Candle 9 (10:10–10:15)**: Closes with **Volume: 8,000**. Since 8,000 < 10,000, this forms a **new lowest-volume Setup Candle**! However, it is a **GREEN** candle. Because the market bias is `BUY_ONLY` (requires a RED setup candle), this setup cannot trigger a long entry. (**Ignored 9 - "again ignore 1 candle"**)
* **The Valid Setup & Trade Execution (10:15 AM to 10:25 AM)**:
  10. **Candle 10 (10:15–10:20)**: Closes with **Volume: 6,000**. Since 6,000 < 8,000, this becomes the **new lowest-volume Setup Candle**! It is a **RED** candle (High: 1,002.00, Low: 998.00).
  11. **Trade Entry (10:20–10:25)**: The next-candle trigger window is now active. At 10:22 AM, the live price surges to **1,002.50**, crossing above the Setup High.
  12. **Outcome**: The bot instantly places a BUY order for 99 shares (20,000 Rs capital / 200.50 Rs margin per share) and successfully enters the trade!
