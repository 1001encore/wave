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
$dest=Join-Path $env:LOCALAPPDATA "wave"; $zip=Join-Path $env:TEMP "wave_windows_amd64.zip"; Invoke-WebRequest -Uri "https://github.com/1001encore/wave/releases/latest/download/wave_windows_amd64.zip" -OutFile $zip; New-Item -ItemType Directory -Force -Path $dest | Out-Null; Expand-Archive -Path $zip -DestinationPath $dest -Force; Remove-Item $zip -Force; $userPath=[Environment]::GetEnvironmentVariable("Path","User"); if ([string]::IsNullOrWhiteSpace($userPath)) { $userPath=$dest } elseif (-not (($userPath -split ";") -contains $dest)) { $userPath="$userPath;$dest" }; [Environment]::SetEnvironmentVariable("Path",$userPath,"User"); $env:Path="$env:Path;$dest"; wave --help
```

Open a new terminal after running this so updated `PATH` is picked up everywhere.

#### One-line install (Linux Bash)

```bash
ARCH="$(uname -m)"; case "$ARCH" in x86_64|amd64) A=amd64;; aarch64|arm64) A=arm64;; *) echo "Unsupported architecture: $ARCH" >&2; exit 1;; esac; TMP="$(mktemp -d)"; curl -fsSL -o "$TMP/wave.tar.gz" "https://github.com/1001encore/wave/releases/latest/download/wave_linux_${A}.tar.gz"; tar -xzf "$TMP/wave.tar.gz" -C "$TMP"; mkdir -p "$HOME/.local/bin"; install -m 0755 "$TMP/wave" "$HOME/.local/bin/wave"; rm -rf "$TMP"; command -v wave >/dev/null 2>&1 || { echo 'Add ~/.local/bin to PATH (for bash: echo '\''export PATH="$HOME/.local/bin:$PATH"'\'' >> ~/.bashrc && source ~/.bashrc)'; }; wave --help
```

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
