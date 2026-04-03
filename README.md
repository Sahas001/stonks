# Stonks

`stonks` is a local Go CLI for pulling NEPSE market data, analyzing recent floorsheet activity, and ranking stocks by a simple risk/reward model.

## What it does

- Collects quote and floorsheet/trade data from configurable HTTP sources.
- Scores each symbol using momentum, liquidity, and floorsheet flow.
- Returns candidate entries with `buy_price`, `stop_loss`, `take_profit`, and `risk_reward`.
- Supports bearer-token, cookie, and custom-header backed sources, so you can plug in auth tokens for sites like Nepse Alpha without hardcoding them in code.
- Can optionally run a local Ollama review layer and maintain a tiny rolling `modelfile` memory for concise lessons.

## Current scope

This first version is intentionally configurable because NEPSE and Nepse Alpha endpoints can change shape or require different headers over time. The engine is stable; the source URLs and tokens are environment-driven.

## Run

```bash
go run ./cmd/stonks picks
```

`stonks` loads config from `~/.config/stonks/.env` by default. It also falls back to a local project `.env` if present.

CLI commands:

- `go run ./cmd/stonks picks`
- `go run ./cmd/stonks top 3`
- `go run ./cmd/stonks debug`
- `go run ./cmd/stonks help`
- `go run ./cmd/stonks buy AKJCL 100 415`
- `go run ./cmd/stonks pnl`

## Configuration

```bash
export STONKS_PORT=8080
export STONKS_SOURCES=nepse,alpha

export STONKS_NEPSE_BASE_URL=https://www.nepalstock.com
export STONKS_NEPSE_QUOTE_PATH=/api/nots/marketwatch
export STONKS_NEPSE_FLOORSHEET_PATH=/api/nots/floorsheet
export STONKS_NEPSE_AUTH_TOKEN=
export STONKS_NEPSE_HEADERS='Cookie=sessionid=abc123;X-Requested-With=XMLHttpRequest'

export STONKS_ALPHA_BASE_URL=https://www.nepsealpha.com
export STONKS_ALPHA_FORCE_URL_KEY_PATH=/force-url-key
export STONKS_ALPHA_FORCE_URL_KEY_FS=161130
export STONKS_ALPHA_QUOTE_PATH='/sastoshare/get_live_market/1?fsk={{fsk}}&fs={{quote_fs}}'
export STONKS_ALPHA_FLOORSHEET_PATH='/floorsheet-live-today/filter?fsk={{fsk}}&page=1&lvs={{lvs}}&contractNumber=&stockSymbol=&buyer=&seller=&itemsPerPage=20'
export STONKS_ALPHA_HISTORY_PATH='/trading/1/history?fsk={{fsk}}&symbol={{symbol}}&resolution={{resolution}}&frame={{frame}}'
export STONKS_HISTORY_RESOLUTION=1D
export STONKS_HISTORY_FRAME=300
export STONKS_ALPHA_TOKEN=your_token_here
export STONKS_ALPHA_HEADERS='Cookie=access_token=your_token_here'
export STONKS_ALPHA_COOKIE_NAME=nepsealpha_session
export STONKS_ALPHA_COOKIE_VALUE='your_session_cookie_value'

export STONKS_TOP_N=5
export STONKS_MIN_RISK_REWARD=1.8
export STONKS_MAX_RISK_PCT=3.0
export STONKS_DEFAULT_TP_RR=2.2
export STONKS_MIN_VOLUME=10000
export STONKS_MIN_TRADE_COUNT=8
export STONKS_CANDLE_SHORTLIST_SIZE=12
export STONKS_INACTIVE_LOOKBACK_DAYS=120
export STONKS_INACTIVE_MIN_TRADE_DAYS=5
export STONKS_JOURNAL_PATH=~/.config/stonks/journal.jsonl
export STONKS_TRADE_LOG_PATH=~/.config/stonks/trades.jsonl
export STONKS_MODELFILE_PATH=~/.config/stonks/modelfile
export STONKS_PERFORMANCE_SUMMARY_PATH=~/.config/stonks/performance_summary.json
export STONKS_JOURNAL_RETENTION_DAYS=14
export STONKS_JOURNAL_MAX_ENTRIES=120
export STONKS_AI_REVIEW_ENABLED=true
export STONKS_AI_REVIEW_COMMAND='ollama run deepseek-r1:1.5b'
export STONKS_AI_REVIEW_TIMEOUT_SECONDS=25
export STONKS_MODELFILE_MAX_ENTRIES=18
export STONKS_MODELFILE_MAX_BYTES=4096
export STONKS_HTTP_TIMEOUT_SECONDS=20
export STONKS_REQUEST_DELAY_MS=1200
export STONKS_HTTP_USER_AGENT='stonks/0.1 (+personal research tool)'
export STONKS_HTTP_REFERER='https://nepsealpha.com/'
export STONKS_INSECURE_SKIP_TLS_VERIFY=false
```

For a local-only setup, keep your main config in `~/.config/stonks/.env`. A project-local `.env` still works as a fallback.

Each analysis run is also logged locally so future runs can compare prior picks against the latest market prices. Old journal entries are pruned by age and max count.

Trade entries are stored separately in `~/.config/stonks/trades.jsonl`, the local AI review layer keeps a small rolling memory in `~/.config/stonks/modelfile`, and all-time pick performance is compacted into `~/.config/stonks/performance_summary.json`.

## Notes on authenticated scraping

If Nepse Alpha or another source expects a bearer token, session cookie, or custom header, set it through environment variables. `STONKS_*_HEADERS` accepts `Key=Value;Key2=Value2`.

Examples:

```bash
export STONKS_ALPHA_HEADERS='Authorization=Token your_token_here'
export STONKS_ALPHA_HEADERS='Cookie=access_token=your_token_here;X-CSRFToken=abc'
export STONKS_ALPHA_COOKIE_NAME=nepsealpha_session
export STONKS_ALPHA_COOKIE_VALUE='your_real_cookie_value'
```

## Nepse Alpha page paths

These URLs are useful discovery points, but they appear to be application pages and may not be the direct JSON endpoints the scraper should call:

- `https://nepsealpha.com/sastoshare/floorsheet/today`
- `https://nepsealpha.com/sastoshare/buy-sell-depth`
- `https://nepsealpha.com/sastoshare/floorsheet/trader`
- `https://nepsealpha.com/sastoshare/floorsheet/history`
- `https://nepsealpha.com/sastoshare/dealers`
- `https://nepsealpha.com/sastoshare/floorsheet/net-accumulation`
- `https://nepsealpha.com/sastoshare/floorsheet/daily`

Use your browser network tab to find the underlying XHR or fetch URLs those pages call, then put those API paths into `STONKS_ALPHA_QUOTE_PATH` and `STONKS_ALPHA_FLOORSHEET_PATH`.

Supported Alpha placeholders:

- `{{fsk}}`: session or page key
- `{{quote_fs}}`: quote date key derived from the live session value
- `{{lvs}}`: live floorsheet session value from `force-url-key`

## TLS note

If a data source has a broken or locally untrusted certificate chain, you can opt out of TLS verification:

```bash
export STONKS_INSECURE_SKIP_TLS_VERIFY=true
```

Use that only when you understand the risk.

## Important limitation

This project ranks setups mechanically. It does not know your capital base, portfolio concentration, tax constraints, or broader market context. Use it as a screening tool, not as an unattended execution system.
