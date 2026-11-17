---
name: news-analysis
description: >-
  Get today's Forex Factory macroeconomic news events and analyze their immediate impact on crypto, FX, and global liquidity.
  Strictly excludes older news from prior days. Triggered by /news-analysis or when asking for today's economic news analysis.
---

# /news-analysis

Retrieves today's economic calendar events using the Forex Factory engine, strictly excluding past days, and performs a comprehensive macroeconomic and crypto market impact analysis.

## Workflow

1. Execute the analysis command:
   ```bash
   go run ./cmd/forex -mode=analysis -detailed
   ```
   *To retrieve raw data as JSON:*
   ```bash
   go run ./cmd/forex -mode=analysis -json
   ```

2. Parse and evaluate the results:
   - Identify High Impact (🔴 Red) events as primary catalysts.
   - Compare Actual vs Forecast vs Previous for any released numbers.
   - Note pending high-impact releases (e.g. Fed speeches, CPI, rate minutes).

3. Present the analysis:
   - **Market Overview**: Total events today and impact breakdown.
   - **Catalyst Analysis**: Detailed examination of each high-impact release, its economic significance, and market direction bias (hawkish vs dovish).
   - **Crypto Market Implications**: Expected volatility spikes on BTC, ETH, and altcoins; funding rate/liquidity risks; key trading session hours to observe.
