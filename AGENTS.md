# Agent Guidelines for wave

`wave` is a code intelligence CLI. Use it to search, navigate, and understand
codebases that have been indexed.

## Indexing a project

Before querying, index the project once from its root:

```bash
wave index
```

This auto-detects the language (Python or TypeScript) from the project manifest,
runs the SCIP indexer, extracts tree-sitter chunks, derives call/reference edges,
and generates vector embeddings. Re-run after significant code changes.

Use `wave status` to check freshness before querying.

## Searching

```bash
# Symbol or identifier lookup
wave search "ClassName"

# Natural language
wave search "retry logic for HTTP requests"

# Force a specific retrieval mode
wave search --mode symbol "parse"
wave search --mode semantic "error handling"
wave search --mode graph "Router"
```

Prefer `--json` for structured output you can parse:

```bash
wave search --json "handleRequest"
```

## Definitions and references

```bash
wave def SymbolName          # jump to definition
wave refs SymbolName         # list all references
```

When the symbol is ambiguous, `wave` returns the best-ranked candidate and
includes a `candidates` array. Use the SCIP symbol string for exact matches.

## Context bundles

```bash
wave context "functionName"
```

Returns the seed chunk, file neighbors, graph-connected chunks (callers/callees),
and references — everything needed to understand a symbol in context.

## Flags

| Flag | Description |
|---|---|
| `--json` | Structured JSON output (always use this for programmatic access) |
| `--limit N` | Cap result count (default 10) |
| `--explain` | Show query routing plan and freshness |
| `--mode M` | `auto`, `hybrid`, `symbol`, `semantic`, `graph` |
| `--device D` | Embedding device: `cpu`, `cuda` |
| `--root PATH` | Override project root |

## Tips

- Use `wave search --json --explain` to see which retrieval path was taken.
- Pipe `wave def --json Symbol | jq .path` to get a file path for further reads.
- `wave context --json` gives the richest result — definition, neighbors, graph
  neighbors, and references in one call.
- The index lives in `.wave/wave.db`; delete it to force a full re-index.
