---
name: news-weekly
description: >-
  Fetches the whole week's upcoming Forex Factory economic events, sorts them strictly on the basis of importance
  (High Red, Medium Orange, Low Yellow), and presents a clear weekly trading danger schedule.
  Strictly excludes older and already-passed news. Triggered by /news-weekly or when asking for upcoming weekly economic news.
---

# /news-weekly

Fetches all upcoming economic calendar releases for the remainder of the current trading week from Forex Factory, strictly excluding past events, and presents them prioritized by impact level.

## Workflow

1. Execute the weekly upcoming command:
   ```bash
   go run ./cmd/forex -mode=weekly -detailed
   ```
   *To retrieve raw data as JSON:*
   ```bash
   go run ./cmd/forex -mode=weekly -json
   ```

2. Sorting & Filtering Rules:
   - Older events (prior days or already passed in time) are strictly filtered out.
   - Events are sorted by importance tier:
     - 🔴 **High Impact (Red)**: Major market catalysts (CPI, Fed, NFP, GDP, Rate Decisions)
     - 🟠 **Medium Impact (Orange)**: Secondary macro indicators
     - 🟡 **Low Impact (Yellow)**: Routine and minor releases

3. Present the weekly schedule:
   - **Tier 1 (High Importance)**: Full event details, forecast, prior numbers, and expected market volatility.
   - **Tier 2 (Medium Importance)**: Highlight key secondary releases.
   - **Tier 3 (Low Importance)**: Concise list to avoid clutter.
   - **Trading Session Danger Windows**: Chronological breakdown by day (Monday to Friday) with exact time slots representing high slippage and liquidity risk for leveraged positions.
