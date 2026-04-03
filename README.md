# wave

Code intelligence tool that combines SCIP graphs, Tree-sitter and vector embeddings.

`wave` indexes your codebase, then gives fast symbol + semantic + graph retrieval from the terminal.
The roadmap is to support all languages that has a high quality SCIP indexer.

## Supported Languages

| Language | Adapter ID | SCIP indexer | Auto-install path |
| --- | --- | --- | --- |
| Go | `go-scip` | `scip-go` | `go install` |
| Java | `java-scip` | `scip-java` | `coursier` (`cs`/`coursier`) |
| Python | `python-scip` | `scip-python` | `npm` |
| Rust | `rust-scip` | `rust-analyzer` (`scip`) | `rustup component add rust-analyzer` |
| TypeScript / JavaScript | `typescript-scip` | `scip-typescript` | `npm` |

## Capabilities

- `wave search <query>` supports both identifier queries and natural-language semantic queries.
- `wave def <symbol>` does exact symbol definition resolution.
- `wave refs <symbol>` lists references for the resolved symbol.
- `wave context <query>` builds a compact context bundle: seed + neighbors + graph + refs.
- `wave status` reports index freshness and per-language counts.

### Search/Context Modes

- `auto` (default): routes identifier-like queries to symbol-heavy retrieval, and natural-language queries to semantic+symbol hybrid retrieval.
- `hybrid`: combine symbol, semantic, and graph-style signals.
- `symbol`: exact/fuzzy symbol-oriented retrieval.
- `semantic`: embedding-based natural-language retrieval.
- `graph`: relationship-driven retrieval from symbol/edge structure.

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

## Quick Start

```bash
cd your-project
wave index
wave search --mode auto "handleRequest"
wave search --mode hybrid "auth middleware and retries"
wave search --mode symbol "Router"
wave search --mode semantic "retry with exponential backoff"
wave search --mode graph "PaymentService"
wave def MyClass
wave refs processData
```

## Core Commands

- `wave index` — build/update index
- `wave status` — show index freshness
- `wave search <query>` — discovery for identifiers and natural-language queries
- `wave def <symbol>` — jump to definition
- `wave refs <symbol>` — list references
- `wave context <query>` — definition + neighbors + graph + refs

## Common Flags

- `--root <path>` — project root (or path inside project)
- `--json` — structured output
- `--mode auto|hybrid|symbol|semantic|graph`
- `--limit <n>`
- `--explain`
- `--device cpu|cuda` (default: `cuda`)

## Requirements

Indexing shells out to SCIP indexers:

- Go: `scip-go` (plus `go`)
- Java: `scip-java`
- Python: `scip-python` (plus `python3` and `node`)
- Rust: `rust-analyzer` (with `scip` support, plus `cargo`)
- TypeScript: `scip-typescript` (plus `node`)

On first `wave index`, `wave` detects workspace languages and auto-installs missing indexers for detected languages:

- `scip-python`, `scip-typescript` via `npm`
- `scip-go` via `go install`
- `rust-analyzer` via `rustup component add`
- `scip-java` via `coursier` (`cs`/`coursier`)

If the required installer toolchain for a language is missing (for example `go`, `rustup`, or `coursier`), `wave` reports a clear error describing what to install.

Embeddings use ONNX (`all-MiniLM-L6-v2`). `wave` bootstraps a local Python env for embedding dependencies when needed.

## Notes

- Index data is stored at `.wave/wave.db`.
- Large refactors can trigger automatic re-index before query commands.

## License

See [LICENSE](LICENSE).
