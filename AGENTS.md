# Agent Guide for wave

Use `wave` as a fast code-intelligence layer over an indexed project.

## Supported Languages

| Language | Adapter ID | SCIP indexer | Auto-install path |
| --- | --- | --- | --- |
| Go | `go-scip` | `scip-go` | `go install` |
| Java | `java-scip` | `scip-java` | `coursier` (`cs`/`coursier`) |
| Python | `python-scip` | `scip-python` | `npm` |
| Rust | `rust-scip` | `rust-analyzer` (`scip`) | `rustup component add rust-analyzer` |
| TypeScript / JavaScript | `typescript-scip` | `scip-typescript` | `npm` |

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

- `search` — discovery across identifiers and natural-language semantic intent.
- `def` — precise definition lookup from symbol identity.
- `refs` — symbol usage map for the resolved symbol.
- `context` — compact local neighborhood (seed + neighbors + graph + refs).
- `status` — index freshness and counts.

## Retrieval Modes

`search` and `context` support `--mode auto|hybrid|symbol|semantic|graph`:

- `auto` (default): picks symbol-heavy routing for identifier-like queries, and semantic+symbol hybrid for natural-language queries.
- `hybrid`: mixes symbol/semantic/graph signals.
- `symbol`: symbol-oriented matching.
- `semantic`: embedding-based natural-language retrieval.
- `graph`: relationship/edge-oriented retrieval.

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
