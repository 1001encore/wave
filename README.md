# wave

Code intelligence CLI powered by [SCIP](https://sourcegraph.com/docs/code-intelligence/references/scip) graphs, [tree-sitter](https://tree-sitter.github.io/) syntax, and vector embeddings.

Index a project once, then search symbols, jump to definitions, list references, and pull contextual bundles — all from the terminal.

## Install

### Prebuilt binaries

Download the latest release from [GitHub Releases](https://github.com/1001encore/wave/releases):

| Platform | Archive |
|---|---|
| Linux x86_64 | `wave_linux_amd64.tar.gz` |
| Linux arm64 | `wave_linux_arm64.tar.gz` |
| macOS x86_64 | `wave_darwin_amd64.tar.gz` |
| macOS Apple Silicon | `wave_darwin_arm64.tar.gz` |
| Windows x86_64 | `wave_windows_amd64.zip` |

#### One-line install (Windows PowerShell)

```powershell
gh api repos/1001encore/wave/contents/scripts/install.ps1?ref=main --jq .content | % { [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($_)) } | iex
```

#### One-line install (Linux Bash)

```bash
gh api repos/1001encore/wave/contents/scripts/install.sh?ref=main --jq .content | base64 -d | sh
```

Scripts used by the commands above:

- `scripts/install.ps1`
- `scripts/install.sh`

Note: raw.githubusercontent.com links work only for public repos. This repo is private, so use the `gh api` commands above.

```bash
# Example: Linux amd64
curl -LO https://github.com/1001encore/wave/releases/latest/download/wave_linux_amd64.tar.gz
tar xzf wave_linux_amd64.tar.gz
sudo mv wave /usr/local/bin/
```

### From source

```bash
go install github.com/1001encore/wave/cmd/wave@latest
```

Or build locally:

```bash
git clone https://github.com/1001encore/wave.git
cd wave
go build -o wave ./cmd/wave
```

## Requirements

`wave` shells out to language-specific SCIP indexers at index time:

- **Python** — `scip-python` (also needs `python3` and `node`)
- **TypeScript** — `scip-typescript` (needs `node`)

Embeddings require a Python environment with `numpy`, `onnxruntime`, and `tokenizers`. If none is found, `wave` creates a local venv and installs them automatically.

## Quick start

```bash
cd your-project/

# Index the project (detects language from manifest)
wave index

# Search by name or natural language
wave search "handleRequest"
wave search "function that parses config files"

# Jump to a definition
wave def MyClass

# List references
wave refs processData

# Get a contextual bundle around a query
wave context "authentication flow"
```

## Commands

| Command | Description |
|---|---|
| `index` | Index the project with SCIP + tree-sitter, generate embeddings |
| `status` | Show indexed project status and freshness |
| `search` | Search symbols and code chunks |
| `def` | Resolve a symbol's definition location |
| `refs` | List all references to a symbol |
| `context` | Build a contextual bundle (definition + neighbors + graph + refs) |

## Flags

| Flag | Default | Description |
|---|---|---|
| `--root` | cwd | Project root path |
| `--json` | false | Emit JSON output |
| `--limit` | 10 | Max results |
| `--explain` | false | Show query routing details |
| `--mode` | auto | Query mode: `auto`, `hybrid`, `symbol`, `semantic`, `graph` |
| `--device` | auto | Embedding device: `cpu`, `cuda` |
| `--db` | `.wave/wave.db` | Override database path |

## How it works

1. **SCIP indexing** — calls `scip-python` or `scip-typescript` to produce a symbol graph with definitions, references, and relationships.
2. **Tree-sitter chunking** — parses source into semantic chunks (functions, classes, imports) with parent-child relationships.
3. **Edge derivation** — combines SCIP occurrences with tree-sitter AST walks to emit `calls`, `reads`, `writes`, `uses`, `imports`, and `contains` edges.
4. **Vector embeddings** — embeds each chunk via ONNX (`all-MiniLM-L6-v2`) for semantic search. Model is auto-downloaded on first run.
5. **Hybrid retrieval** — queries fuse exact symbol lookup, lexical search, semantic similarity, and graph traversal, ranked and re-ranked by kind and span.

All data is stored in a single SQLite database (`.wave/wave.db`) using [sqlite-vec](https://github.com/asg017/sqlite-vec) for vector search.

## License

See [LICENSE](LICENSE).
