---
name: news-monthly
description: >-
  Retrieves the entire month's upcoming Forex Factory news and maps out important macroeconomic dates and volatility catalysts through month-end.
  Strictly excludes older past dates. Triggered by /news-monthly or when asking for monthly macro news and calendar mapping.
---

# /news-monthly

Retrieves economic calendar news for the full month from the current date through the end of the month, strictly excluding any past dates, and generates a structured calendar map of key risk dates and major catalysts.

## Workflow

1. Execute the monthly mapping command:
   ```bash
   go run ./cmd/forex -mode=monthly -detailed
   ```
   *To retrieve raw data as JSON:*
   ```bash
   go run ./cmd/forex -mode=monthly -json
   ```

2. Date Mapping & Risk Classification:
   - Dates before today are strictly excluded.
   - Each upcoming date in the month is classified:
     - 🔴 **HIGH RISK**: Days featuring High-Impact releases (CPI, PCE, Fed/ECB/BOE speeches, GDP, Monthly Crypto Options Expiry)
     - 🟠 **MODERATE RISK**: Days with Medium-Impact releases (PMIs, Regional Surveys, Durable Goods)
     - 🟢 **LOW RISK**: Routine or minor release days

3. Present the monthly map:
   - **Horizon Overview**: Total upcoming active trading days in the month.
   - **Chronological Date Map**: Detailed list of upcoming dates, day of the week, risk category, and key scheduled events.
   - **Strategic Danger Clusters**: Critical multi-day volatility zones (e.g. CPI week, FOMC minutes, Monthly Crypto Options Expiry settlement on the last Friday of the month).
