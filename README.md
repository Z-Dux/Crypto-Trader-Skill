# Crypto Trader Skill

Institutional crypto microstructure trading and macroeconomic intelligence skills for AI agents (Antigravity, Claude Code, Cursor, Codex).

## Install via npx

```bash
# Auto-install to your agent environment
npx crypto-trader-skill

# Or install from GitHub repository
npx github:<owner>/Crypto-Trader-Skill
```

Or using the standard skills CLI:
```bash
npx skills add <owner>/Crypto-Trader-Skill
```

## Structure

```text
├── skills/           # Modular skills for AI agents
│   ├── news-analysis/
│   ├── news-weekly/
│   └── news-monthly/
├── agents/           # Specialized subagent definitions
│   ├── crypto-trader.md
│   └── macro-analyst.md
├── cmd/
│   ├── bot/          # Market snapshot & real-time WebSocket tape
│   └── forex/        # Macroeconomic news calendar CLI
└── internal/
    ├── exchange/     # CCXT, indicator math, WebSocket listener
    └── macro/        # Forex Factory calendar engine
```

## Slash Commands

- `/news-analysis` : Today's economic news analysis (strictly excludes past days)
- `/news-weekly`   : Whole week's upcoming news sorted by importance (High > Med > Low)
- `/news-monthly`  : Full month's macro mapping and danger date schedule

## CLI Engines

```bash
# Market snapshot & 60s live tape
go run ./cmd/bot [SYMBOL]

# Macro calendar engine
go run ./cmd/forex -mode=[analysis|weekly|monthly]
```

## Disclaimer

For informational and research purposes only. Not financial advice.
