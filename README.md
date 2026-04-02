# wave

Code intelligence tool that combines SCIP graphs, Tree-sitter and vector embeddings.

`wave` indexes your codebase, then gives fast symbol + semantic + graph retrieval from the terminal.

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
wave search "handleRequest"
wave def MyClass
wave refs processData
wave context "authentication flow"
```

## Core Commands

- `wave index` — build/update index
- `wave status` — show index freshness
- `wave search <query>` — hybrid retrieval
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

- Python: `scip-python` (plus `python3` and `node`)
- TypeScript: `scip-typescript` (plus `node`)

On first `wave index`, `wave` detects workspace languages and auto-installs any missing SCIP indexers (`scip-python` / `scip-typescript`) via `npm` for detected languages only.

Embeddings use ONNX (`all-MiniLM-L6-v2`). `wave` bootstraps a local Python env for embedding dependencies when needed.

## Notes

- Index data is stored at `.wave/wave.db`.
- Large refactors can trigger automatic re-index before query commands.

## License

See [LICENSE](LICENSE).
