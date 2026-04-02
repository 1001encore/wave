# Agent Guide for wave

Use `wave` as a fast code-intelligence layer over an indexed project.

## Default Workflow

1. Ensure the project is indexed:

```bash
wave index
```

2. Run one of the core commands:

```bash
wave search "query"
wave def SymbolName
wave refs SymbolName
wave context "query"
```

## Command Intent

- `search` — best for discovery (identifier or natural language)
- `def` — precise symbol definition lookup
- `refs` — symbol usage map
- `context` — compact local neighborhood (seed + neighbors + graph + refs)
- `status` — index freshness and counts

## Modes and JSON

For automation, always use JSON:

```bash
wave search --json "handleRequest"
wave context --json "auth middleware"
```

Use explicit retrieval modes when needed:

```bash
wave search --mode symbol "Router"
wave search --mode semantic "retry with exponential backoff"
wave search --mode graph "PaymentService"
```

## Practical Tips

- Prefer `wave context --json` when you need a single, rich payload.
- Use `wave search --json --explain` to inspect routing decisions.
- Use `--root <path>` when running outside the target project directory.
- Use `--device cuda` when GPU is available and embedding latency matters.

## Freshness

- `wave` stores index state in `.wave/wave.db`.
- Normal query commands may auto re-index after large refactors.
- Run `wave status` if results look stale.
