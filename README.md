# wave

Code intelligence tool that combines SCIP graphs, Tree-sitter, and vector embeddings.

`wave` indexes your codebase, then gives fast symbol + semantic + graph retrieval from the terminal.

## Supported Languages

| Language | Adapter ID | SCIP indexer | Auto-install path |
| --- | --- | --- | --- |
| Go | `go-scip` | `scip-go` | `go install` |
| Java | `java-scip` | `scip-java` | `coursier` (`cs`/`coursier`) |
| Python | `python-scip` | `scip-python` | `npm` |
| Rust | `rust-scip` | `rust-analyzer` (`scip`) | `rustup component add rust-analyzer` |
| TypeScript / JavaScript | `typescript-scip` | `scip-typescript` | `npm` |

## Install

### One-line install (Linux/macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/1001encore/wave/main/scripts/install.sh | sh
```

### One-line install (Windows PowerShell)

```powershell
irm https://raw.githubusercontent.com/1001encore/wave/main/scripts/install.ps1 | iex
```

### Other distributions

Use GitHub Releases for manual binaries and archives:

- https://github.com/1001encore/wave/releases

## Capabilities

- `wave index` — build or update the index.
- `wave status` — report index freshness and per-language counts.
- `wave search <query>` — discovery across identifier-style and natural-language queries.
- `wave context <query>` — compact context bundle (seed + neighbors + graph + refs).
- `wave def <symbol>` — exact symbol definition lookup.
- `wave refs <symbol>` — symbol usage map for the resolved symbol.
- `wave update` — self-update from GitHub Releases.
- `wave version` — print the current CLI version.

`--mode` is used with `wave search` and `wave context`:

- `auto` (default): routes identifier-like queries to symbol-heavy retrieval, and natural-language queries to semantic+symbol hybrid retrieval.
- `hybrid`: combines symbol, semantic, and graph-style signals.
- `symbol`: exact/fuzzy symbol-oriented retrieval.
- `semantic`: embedding-based natural-language retrieval.
- `graph`: relationship-driven retrieval from symbol/edge structure.

## Quick Start

```bash
cd your-project
wave index
wave search --mode auto "handleRequest"
wave search --mode hybrid "auth middleware and retries"
wave context --mode semantic "retry with exponential backoff"
wave search --mode graph "PaymentService"
wave def MyClass
wave refs processData
wave update --check
```

## Common Flags

- `--root <path>` — project root (or path inside project)
- `--json` — structured output
- `--mode auto|hybrid|symbol|semantic|graph` (for `search` and `context`)
- `--limit <n>`
- `--explain`
- `--device cpu|cuda` (default: `cpu` for query/status commands, `cuda` for `index`)
- `wave search --show-score` — include raw rerank score per hit
- `wave search --show-softmax` — include softmax probability per hit (relative within returned hits)
- `wave search --preview` — show a short code preview per hit
- `wave search --preview-chars <n>` — cap preview length in characters
- `wave search --signature` — show SCIP signature per hit

## Requirements

`wave` auto-detects languages in your workspace and installs missing indexers on first `wave index` using each language's installer path shown in the Supported Languages table.

If a required installer toolchain is missing (for example `go`, `rustup`, or `coursier`), `wave` returns a clear error describing what to install.

- Python 3 is required for embedding-backed workflows (`index`, `search`, `context`, and auto-reindex triggered before query commands).
- Embeddings use ONNX with `all-MiniLM-L6-v2-code-search-512` (size of ~90MB, downloaded first time only).
- `wave` uses a Wave-managed runtime (`~/.cache/wave/runtime`) by default for embedding dependencies (independent of project virtual environments).
- Package installation inside that Wave-managed runtime uses `uv` (`uv pip`).

## Notes

- Index data is stored at `.wave/wave.db`.
- Large refactors can trigger automatic re-index before query commands.
- Indexing defaults to GPU (`--device cuda`) when available; query/status commands default to CPU for lower interactive overhead. You can override either per command with `--device`.
- Update checks are run at most once a day on the first command use, resulting on a single line update notice printed once at the next command use. You can set `WAVE_UPDATE_REMINDER=never` to disable update notices.

## License

MIT
