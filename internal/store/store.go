package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

const (
	chunkEmbeddingDimensions = 384
)

var sqliteVecAuto sync.Once

type Store struct {
	db *sql.DB
}

type ProjectData struct {
	RootPath          string
	Name              string
	Language          string
	ManifestPath      string
	EnvironmentSource string
	AdapterID         string
	ScipArtifactPath  string
	ToolName          string
	ToolVersion       string
	GitCommitHash     string
}

type FileData struct {
	RelativePath string
	AbsPath      string
	Language     string
	ContentHash  string
	LineCount    int
}

type SymbolData struct {
	ScipSymbol      string
	DisplayName     string
	Kind            string
	FilePath        string
	DefStartLine    int
	DefStartCol     int
	DefEndLine      int
	DefEndCol       int
	Signature       string
	DocSummary      string
	EnclosingSymbol string
}

type OccurrenceData struct {
	FilePath           string
	Symbol             string
	StartLine          int
	StartCol           int
	EndLine            int
	EndCol             int
	EnclosingStartLine int
	EnclosingStartCol  int
	EnclosingEndLine   int
	EnclosingEndCol    int
	RoleBits           int
	SyntaxKind         string
	IsDefinition       bool
	IsImport           bool
	IsRead             bool
	IsWrite            bool
}

type ChunkData struct {
	Key           string
	FilePath      string
	Kind          string
	Name          string
	ParentKey     string
	StartByte     int
	EndByte       int
	StartLine     int
	StartCol      int
	EndLine       int
	EndCol        int
	Text          string
	HeaderText    string
	RetrievalText string
	PrimarySymbol string
}

type EdgeData struct {
	SrcSymbol  string
	DstSymbol  string
	Kind       string
	Provenance string
}

type ChunkSymbolLinkData struct {
	ChunkKey string
	Symbol   string
	Role     string
	Weight   float64
}

type IndexPayload struct {
	Project      ProjectData
	Files        []FileData
	Symbols      []SymbolData
	Occurrences  []OccurrenceData
	Chunks       []ChunkData
	ChunkSymbols []ChunkSymbolLinkData
	Edges        []EdgeData
	Embeddings   []EmbeddingData
}

type EmbeddingData struct {
	OwnerType string
	OwnerKey  string
	Model     string
	TextHash  string
	Text      string
	Vector    []float32
}

type StatusRow struct {
	Name         string    `json:"name"`
	RootPath     string    `json:"root_path"`
	Language     string    `json:"language"`
	ManifestPath string    `json:"manifest_path"`
	AdapterID    string    `json:"adapter_id"`
	IndexedAt    time.Time `json:"indexed_at"`
	FileCount    int       `json:"file_count"`
	SymbolCount  int       `json:"symbol_count"`
	ChunkCount   int       `json:"chunk_count"`
	EdgeCount    int       `json:"edge_count"`
}

type IndexedFileRow struct {
	RelativePath string
	AbsPath      string
	ContentHash  string
	LineCount    int
}

type RelatedChunk struct {
	RelationKind string  `json:"relation_kind"`
	Direction    string  `json:"direction"`
	ChunkID      int64   `json:"-"`
	FileID       int64   `json:"-"`
	SymbolID     int64   `json:"-"`
	Path         string  `json:"path"`
	StartLine    int     `json:"start_line"`
	EndLine      int     `json:"end_line"`
	Kind         string  `json:"kind"`
	Name         string  `json:"name"`
	DisplayName  string  `json:"display_name"`
	Signature    string  `json:"signature"`
	HeaderText   string  `json:"header_text"`
	Text         string  `json:"text"`
	Score        float64 `json:"score"`
}

type ChunkSymbolLink struct {
	ChunkID  int64   `json:"-"`
	SymbolID int64   `json:"-"`
	Role     string  `json:"role"`
	Weight   float64 `json:"weight"`
}

type SearchHit struct {
	ChunkID            int64   `json:"-"`
	FileID             int64   `json:"-"`
	PrimarySymbolID    int64   `json:"-"`
	Path               string  `json:"path"`
	StartLine          int     `json:"start_line"`
	EndLine            int     `json:"end_line"`
	Kind               string  `json:"kind"`
	Name               string  `json:"name"`
	DisplayName        string  `json:"display_name"`
	Signature          string  `json:"signature"`
	HeaderText         string  `json:"header_text"`
	Text               string  `json:"text"`
	Score              float64 `json:"score"`
	SoftmaxProbability float64 `json:"softmax_probability,omitempty"`
}

type SymbolSearchHit struct {
	SymbolID    int64   `json:"-"`
	ScipSymbol  string  `json:"scip_symbol"`
	DisplayName string  `json:"display_name"`
	Kind        string  `json:"kind"`
	Path        string  `json:"path"`
	StartLine   int     `json:"start_line"`
	StartCol    int     `json:"start_col"`
	EndLine     int     `json:"end_line"`
	EndCol      int     `json:"end_col"`
	DocSummary  string  `json:"doc_summary"`
	Signature   string  `json:"signature"`
	Enclosing   string  `json:"enclosing_symbol"`
	Score       float64 `json:"score"`
}

type DefinitionResult struct {
	SymbolID    int64  `json:"-"`
	ScipSymbol  string `json:"scip_symbol"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"`
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	StartCol    int    `json:"start_col"`
	EndLine     int    `json:"end_line"`
	EndCol      int    `json:"end_col"`
	DocSummary  string `json:"doc_summary"`
	Signature   string `json:"signature"`
	Enclosing   string `json:"enclosing_symbol"`
}

type ReferenceResult struct {
	SymbolID    int64  `json:"-"`
	DisplayName string `json:"display_name"`
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	StartCol    int    `json:"start_col"`
	EndLine     int    `json:"end_line"`
	EndCol      int    `json:"end_col"`
	SyntaxKind  string `json:"syntax_kind"`
}

func Open(path string) (*Store, error) {
	sqliteVecAuto.Do(vec.Auto)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	stmts := []string{
		`PRAGMA foreign_keys = ON;`,
		`CREATE TABLE IF NOT EXISTS projects (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				root_path TEXT NOT NULL,
				name TEXT NOT NULL,
				language TEXT NOT NULL,
			manifest_path TEXT NOT NULL,
			environment_source TEXT NOT NULL,
			adapter_id TEXT NOT NULL DEFAULT '',
				scip_artifact_path TEXT NOT NULL,
				tool_name TEXT NOT NULL,
				tool_version TEXT NOT NULL,
				last_indexed_at TEXT NOT NULL,
				git_commit_hash TEXT NOT NULL DEFAULT '',
				UNIQUE(root_path, adapter_id)
			);`,
		`CREATE TABLE IF NOT EXISTS files (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
				relative_path TEXT NOT NULL,
				abs_path TEXT NOT NULL,
				language TEXT NOT NULL,
				content_hash TEXT NOT NULL,
				line_count INTEGER NOT NULL DEFAULT 0,
				UNIQUE(project_id, relative_path)
			);`,
		`CREATE TABLE IF NOT EXISTS symbols (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			scip_symbol TEXT NOT NULL,
			display_name TEXT NOT NULL,
			kind TEXT NOT NULL,
			file_id INTEGER REFERENCES files(id) ON DELETE SET NULL,
			def_start_line INTEGER NOT NULL,
			def_start_col INTEGER NOT NULL,
			def_end_line INTEGER NOT NULL,
			def_end_col INTEGER NOT NULL,
			signature TEXT NOT NULL,
			doc_summary TEXT NOT NULL,
			enclosing_symbol TEXT NOT NULL,
			UNIQUE(project_id, scip_symbol)
		);`,
		`CREATE TABLE IF NOT EXISTS occurrences (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			symbol_id INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
			file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
			start_line INTEGER NOT NULL,
			start_col INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			end_col INTEGER NOT NULL,
			enclosing_start_line INTEGER NOT NULL,
			enclosing_start_col INTEGER NOT NULL,
			enclosing_end_line INTEGER NOT NULL,
			enclosing_end_col INTEGER NOT NULL,
			role_bits INTEGER NOT NULL,
			syntax_kind TEXT NOT NULL,
			is_definition INTEGER NOT NULL,
			is_import INTEGER NOT NULL,
			is_read INTEGER NOT NULL,
			is_write INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
			primary_symbol_id INTEGER REFERENCES symbols(id) ON DELETE SET NULL,
			parent_chunk_id INTEGER REFERENCES chunks(id) ON DELETE SET NULL,
			chunk_key TEXT NOT NULL,
			kind TEXT NOT NULL,
			name TEXT NOT NULL,
			start_byte INTEGER NOT NULL,
			end_byte INTEGER NOT NULL,
			start_line INTEGER NOT NULL,
			start_col INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			end_col INTEGER NOT NULL,
			header_text TEXT NOT NULL,
			text TEXT NOT NULL,
			retrieval_text TEXT NOT NULL,
			UNIQUE(project_id, chunk_key)
		);`,
		`CREATE TABLE IF NOT EXISTS chunk_symbols (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			chunk_id INTEGER NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
			symbol_id INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			weight REAL NOT NULL,
			UNIQUE(project_id, chunk_id, symbol_id, role)
		);`,
		`CREATE TABLE IF NOT EXISTS edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			src_symbol_id INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
			dst_symbol_id INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
			kind TEXT NOT NULL,
			provenance TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS index_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			started_at TEXT NOT NULL,
			completed_at TEXT NOT NULL,
			status TEXT NOT NULL,
			files_indexed INTEGER NOT NULL,
			symbols_indexed INTEGER NOT NULL,
			chunks_indexed INTEGER NOT NULL,
			error_text TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_files_project ON files(project_id);`,
		`CREATE INDEX IF NOT EXISTS idx_symbols_project_display ON symbols(project_id, display_name);`,
		`CREATE INDEX IF NOT EXISTS idx_occurrences_symbol ON occurrences(symbol_id, is_definition);`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_primary_symbol ON chunks(primary_symbol_id);`,
		`CREATE INDEX IF NOT EXISTS idx_chunk_symbols_chunk ON chunk_symbols(chunk_id, weight DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_chunk_symbols_symbol ON chunk_symbols(symbol_id);`,
		`CREATE INDEX IF NOT EXISTS idx_edges_src ON edges(src_symbol_id, kind);`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema: %w", err)
		}
	}
	if _, err := s.db.Exec(`DROP TABLE IF EXISTS embedding_docs`); err != nil {
		return fmt.Errorf("drop legacy embedding table: %w", err)
	}
	if err := s.ensureVecTable("vec_chunks", chunkEmbeddingDimensions); err != nil {
		return fmt.Errorf("ensure chunk vector table: %w", err)
	}
	if err := s.ensureVecTable("vec_symbols", chunkEmbeddingDimensions); err != nil {
		return fmt.Errorf("ensure symbol vector table: %w", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE projects ADD COLUMN adapter_id TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate adapter_id column: %w", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE projects ADD COLUMN git_commit_hash TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate git_commit_hash column: %w", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE files ADD COLUMN line_count INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate line_count column: %w", err)
	}
	if err := s.migrateLegacyProjectUniqueness(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureVecTable(name string, dimensions int) error {
	var existingSQL string
	err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&existingSQL)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	expected := fmt.Sprintf("float[%d]", dimensions)
	if existingSQL != "" && !strings.Contains(existingSQL, expected) {
		if _, err := s.db.Exec(`DROP TABLE IF EXISTS ` + name); err != nil {
			return err
		}
	}
	_, err = s.db.Exec(fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS %s USING vec0(
		embedding float[%d]
	)`, name, dimensions))
	return err
}

func (s *Store) migrateLegacyProjectUniqueness() error {
	var sqlText string
	err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'projects'`).Scan(&sqlText)
	if err != nil {
		return fmt.Errorf("inspect projects schema: %w", err)
	}
	lower := strings.ToLower(sqlText)
	if !strings.Contains(lower, "root_path text not null unique") {
		return nil
	}

	if _, err := s.db.Exec(`PRAGMA foreign_keys = OFF;`); err != nil {
		return fmt.Errorf("disable foreign keys: %w", err)
	}
	defer func() {
		_, _ = s.db.Exec(`PRAGMA foreign_keys = ON;`)
	}()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin projects migration tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmts := []string{
		`CREATE TABLE projects_new (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				root_path TEXT NOT NULL,
				name TEXT NOT NULL,
				language TEXT NOT NULL,
			manifest_path TEXT NOT NULL,
			environment_source TEXT NOT NULL,
			adapter_id TEXT NOT NULL DEFAULT '',
				scip_artifact_path TEXT NOT NULL,
				tool_name TEXT NOT NULL,
				tool_version TEXT NOT NULL,
				last_indexed_at TEXT NOT NULL,
				git_commit_hash TEXT NOT NULL DEFAULT '',
				UNIQUE(root_path, adapter_id)
			);`,
		`INSERT INTO projects_new (
				id, root_path, name, language, manifest_path, environment_source, adapter_id,
				scip_artifact_path, tool_name, tool_version, last_indexed_at, git_commit_hash
			)
			SELECT
				id, root_path, name, language, manifest_path, environment_source, adapter_id,
				scip_artifact_path, tool_name, tool_version, last_indexed_at, COALESCE(git_commit_hash, '')
			FROM projects;`,
		`DROP TABLE projects;`,
		`ALTER TABLE projects_new RENAME TO projects;`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate projects uniqueness: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit projects migration: %w", err)
	}
	return nil
}

func (s *Store) ReplaceProjectIndex(ctx context.Context, payload IndexPayload) error {
	cleanRoot := filepath.Clean(payload.Project.RootPath)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var oldProjectID int64
	oldErr := tx.QueryRowContext(
		ctx,
		`SELECT id FROM projects WHERE root_path = ? AND adapter_id = ?`,
		cleanRoot,
		payload.Project.AdapterID,
	).Scan(&oldProjectID)
	if oldErr == nil {
		if _, err = tx.ExecContext(ctx, `DELETE FROM vec_chunks WHERE rowid IN (SELECT id FROM chunks WHERE project_id = ?)`, oldProjectID); err != nil {
			return fmt.Errorf("delete old vector rows: %w", err)
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, oldProjectID); err != nil {
			return fmt.Errorf("delete old project: %w", err)
		}
	} else if !errors.Is(oldErr, sql.ErrNoRows) {
		return fmt.Errorf("lookup existing project: %w", oldErr)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(
		ctx,
		`INSERT INTO projects (
			root_path, name, language, manifest_path, environment_source, adapter_id, scip_artifact_path,
			tool_name, tool_version, last_indexed_at, git_commit_hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cleanRoot,
		payload.Project.Name,
		payload.Project.Language,
		payload.Project.ManifestPath,
		payload.Project.EnvironmentSource,
		payload.Project.AdapterID,
		payload.Project.ScipArtifactPath,
		payload.Project.ToolName,
		payload.Project.ToolVersion,
		now,
		payload.Project.GitCommitHash,
	)
	if err != nil {
		return fmt.Errorf("insert project: %w", err)
	}
	projectID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("get project id: %w", err)
	}

	fileIDs := make(map[string]int64, len(payload.Files))
	for _, file := range payload.Files {
		res, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO files (project_id, relative_path, abs_path, language, content_hash, line_count)
				 VALUES (?, ?, ?, ?, ?, ?)`,
			projectID,
			file.RelativePath,
			file.AbsPath,
			file.Language,
			file.ContentHash,
			file.LineCount,
		)
		if execErr != nil {
			return fmt.Errorf("insert file %s: %w", file.RelativePath, execErr)
		}
		id, idErr := res.LastInsertId()
		if idErr != nil {
			return fmt.Errorf("get file id for %s: %w", file.RelativePath, idErr)
		}
		fileIDs[file.RelativePath] = id
	}

	symbolIDs := make(map[string]int64, len(payload.Symbols))
	for _, symbol := range payload.Symbols {
		var fileID any
		if symbol.FilePath != "" {
			id, ok := fileIDs[symbol.FilePath]
			if ok {
				fileID = id
			}
		}

		res, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO symbols (
				project_id, scip_symbol, display_name, kind, file_id,
				def_start_line, def_start_col, def_end_line, def_end_col,
				signature, doc_summary, enclosing_symbol
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			projectID,
			symbol.ScipSymbol,
			symbol.DisplayName,
			symbol.Kind,
			fileID,
			symbol.DefStartLine,
			symbol.DefStartCol,
			symbol.DefEndLine,
			symbol.DefEndCol,
			symbol.Signature,
			symbol.DocSummary,
			symbol.EnclosingSymbol,
		)
		if execErr != nil {
			return fmt.Errorf("insert symbol %s: %w", symbol.ScipSymbol, execErr)
		}
		id, idErr := res.LastInsertId()
		if idErr != nil {
			return fmt.Errorf("get symbol id for %s: %w", symbol.ScipSymbol, idErr)
		}
		symbolIDs[symbol.ScipSymbol] = id
	}

	for _, occ := range payload.Occurrences {
		fileID, ok := fileIDs[occ.FilePath]
		if !ok {
			return fmt.Errorf("occurrence file not indexed: %s", occ.FilePath)
		}
		symbolID, ok := symbolIDs[occ.Symbol]
		if !ok {
			return fmt.Errorf("occurrence symbol not indexed: %s", occ.Symbol)
		}

		if _, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO occurrences (
				project_id, symbol_id, file_id, start_line, start_col, end_line, end_col,
				enclosing_start_line, enclosing_start_col, enclosing_end_line, enclosing_end_col,
				role_bits, syntax_kind, is_definition, is_import, is_read, is_write
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			projectID,
			symbolID,
			fileID,
			occ.StartLine,
			occ.StartCol,
			occ.EndLine,
			occ.EndCol,
			occ.EnclosingStartLine,
			occ.EnclosingStartCol,
			occ.EnclosingEndLine,
			occ.EnclosingEndCol,
			occ.RoleBits,
			occ.SyntaxKind,
			boolToInt(occ.IsDefinition),
			boolToInt(occ.IsImport),
			boolToInt(occ.IsRead),
			boolToInt(occ.IsWrite),
		); execErr != nil {
			return fmt.Errorf("insert occurrence for %s: %w", occ.Symbol, execErr)
		}
	}

	chunkIDs := make(map[string]int64, len(payload.Chunks))
	for _, chunk := range payload.Chunks {
		fileID, ok := fileIDs[chunk.FilePath]
		if !ok {
			return fmt.Errorf("chunk file not indexed: %s", chunk.FilePath)
		}

		var primarySymbolID any
		if chunk.PrimarySymbol != "" {
			if id, exists := symbolIDs[chunk.PrimarySymbol]; exists {
				primarySymbolID = id
			}
		}

		res, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO chunks (
				project_id, file_id, primary_symbol_id, parent_chunk_id, chunk_key, kind, name,
				start_byte, end_byte, start_line, start_col, end_line, end_col,
				header_text, text, retrieval_text
			) VALUES (?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			projectID,
			fileID,
			primarySymbolID,
			chunk.Key,
			chunk.Kind,
			chunk.Name,
			chunk.StartByte,
			chunk.EndByte,
			chunk.StartLine,
			chunk.StartCol,
			chunk.EndLine,
			chunk.EndCol,
			chunk.HeaderText,
			chunk.Text,
			chunk.RetrievalText,
		)
		if execErr != nil {
			return fmt.Errorf("insert chunk %s: %w", chunk.Key, execErr)
		}
		id, idErr := res.LastInsertId()
		if idErr != nil {
			return fmt.Errorf("get chunk id for %s: %w", chunk.Key, idErr)
		}
		chunkIDs[chunk.Key] = id
	}

	for _, chunk := range payload.Chunks {
		if chunk.ParentKey == "" {
			continue
		}
		chunkID, ok := chunkIDs[chunk.Key]
		if !ok {
			continue
		}
		parentID, ok := chunkIDs[chunk.ParentKey]
		if !ok {
			continue
		}
		if _, execErr := tx.ExecContext(ctx, `UPDATE chunks SET parent_chunk_id = ? WHERE id = ?`, parentID, chunkID); execErr != nil {
			return fmt.Errorf("link chunk parent for %s: %w", chunk.Key, execErr)
		}
	}

	for _, link := range payload.ChunkSymbols {
		chunkID, ok := chunkIDs[link.ChunkKey]
		if !ok {
			continue
		}
		symbolID, ok := symbolIDs[link.Symbol]
		if !ok {
			continue
		}
		if _, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO chunk_symbols (project_id, chunk_id, symbol_id, role, weight)
			 VALUES (?, ?, ?, ?, ?)`,
			projectID,
			chunkID,
			symbolID,
			link.Role,
			link.Weight,
		); execErr != nil {
			return fmt.Errorf("insert chunk symbol link %s -> %s: %w", link.ChunkKey, link.Symbol, execErr)
		}
	}

	for _, edge := range payload.Edges {
		srcID, ok := symbolIDs[edge.SrcSymbol]
		if !ok {
			continue
		}
		dstID, ok := symbolIDs[edge.DstSymbol]
		if !ok {
			continue
		}
		if _, execErr := tx.ExecContext(
			ctx,
			`INSERT INTO edges (project_id, src_symbol_id, dst_symbol_id, kind, provenance)
			 VALUES (?, ?, ?, ?, ?)`,
			projectID,
			srcID,
			dstID,
			edge.Kind,
			edge.Provenance,
		); execErr != nil {
			return fmt.Errorf("insert edge %s -> %s: %w", edge.SrcSymbol, edge.DstSymbol, execErr)
		}
	}

	for _, embedding := range payload.Embeddings {
		blob, blobErr := vec.SerializeFloat32(embedding.Vector)
		if blobErr != nil {
			return fmt.Errorf("serialize embedding for %s: %w", embedding.OwnerKey, blobErr)
		}
		switch embedding.OwnerType {
		case "chunk":
			chunkID, ok := chunkIDs[embedding.OwnerKey]
			if !ok {
				continue
			}
			if _, execErr := tx.ExecContext(
				ctx,
				`INSERT INTO vec_chunks (rowid, embedding) VALUES (?, ?)`,
				chunkID,
				blob,
			); execErr != nil {
				return fmt.Errorf("insert chunk vector for %s: %w", embedding.OwnerKey, execErr)
			}
		}
	}

	if _, err = tx.ExecContext(
		ctx,
		`INSERT INTO index_runs (
			project_id, started_at, completed_at, status, files_indexed, symbols_indexed, chunks_indexed, error_text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		projectID,
		now,
		now,
		"ok",
		len(payload.Files),
		len(payload.Symbols),
		len(payload.Chunks),
		"",
	); err != nil {
		return fmt.Errorf("insert index run: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (s *Store) DeleteProjectsExceptAdapters(ctx context.Context, rootPath string, adapterIDs []string) error {
	cleanRoot := filepath.Clean(rootPath)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var (
		filter strings.Builder
		args   []any
	)
	filter.WriteString(`root_path = ?`)
	args = append(args, cleanRoot)
	if len(adapterIDs) > 0 {
		filter.WriteString(` AND adapter_id NOT IN (`)
		for i, adapterID := range adapterIDs {
			if i > 0 {
				filter.WriteString(",")
			}
			filter.WriteString("?")
			args = append(args, adapterID)
		}
		filter.WriteString(")")
	}

	deleteVecChunksSQL := fmt.Sprintf(
		`DELETE FROM vec_chunks WHERE rowid IN (
			SELECT id FROM chunks WHERE project_id IN (SELECT id FROM projects WHERE %s)
		)`,
		filter.String(),
	)
	if _, err = tx.ExecContext(ctx, deleteVecChunksSQL, args...); err != nil {
		return fmt.Errorf("delete stale chunk vectors: %w", err)
	}

	deleteVecSymbolsSQL := fmt.Sprintf(
		`DELETE FROM vec_symbols WHERE rowid IN (
			SELECT id FROM symbols WHERE project_id IN (SELECT id FROM projects WHERE %s)
		)`,
		filter.String(),
	)
	if _, err = tx.ExecContext(ctx, deleteVecSymbolsSQL, args...); err != nil {
		return fmt.Errorf("delete stale symbol vectors: %w", err)
	}

	deleteProjectsSQL := fmt.Sprintf(`DELETE FROM projects WHERE %s`, filter.String())
	if _, err = tx.ExecContext(ctx, deleteProjectsSQL, args...); err != nil {
		return fmt.Errorf("delete stale projects: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit stale project delete: %w", err)
	}
	return nil
}

func (s *Store) Status(ctx context.Context, rootPath string) ([]StatusRow, error) {
	query := `
		SELECT
			p.name,
			p.root_path,
			p.language,
			p.manifest_path,
			p.adapter_id,
			p.last_indexed_at,
			(SELECT COUNT(*) FROM files f WHERE f.project_id = p.id),
			(SELECT COUNT(*) FROM symbols sy WHERE sy.project_id = p.id),
			(SELECT COUNT(*) FROM chunks c WHERE c.project_id = p.id),
			(SELECT COUNT(*) FROM edges e WHERE e.project_id = p.id)
		FROM projects p
	`
	args := []any{}
	if rootPath != "" {
		query += ` WHERE p.root_path = ?`
		args = append(args, filepath.Clean(rootPath))
	}
	query += ` ORDER BY p.root_path, p.adapter_id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query status: %w", err)
	}
	defer rows.Close()

	var result []StatusRow
	for rows.Next() {
		var row StatusRow
		var ts string
		if err := rows.Scan(
			&row.Name,
			&row.RootPath,
			&row.Language,
			&row.ManifestPath,
			&row.AdapterID,
			&ts,
			&row.FileCount,
			&row.SymbolCount,
			&row.ChunkCount,
			&row.EdgeCount,
		); err != nil {
			return nil, fmt.Errorf("scan status row: %w", err)
		}
		row.IndexedAt, _ = time.Parse(time.RFC3339, ts)
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *Store) GitCommitHash(ctx context.Context, rootPath string, adapterID string) (string, error) {
	cleanRoot := filepath.Clean(rootPath)
	if err := s.ensureProjectsExist(ctx, cleanRoot); err != nil {
		return "", err
	}
	query := `SELECT COALESCE(git_commit_hash, '') FROM projects WHERE root_path = ?`
	args := []any{cleanRoot}
	if strings.TrimSpace(adapterID) != "" {
		query += ` AND adapter_id = ?`
		args = append(args, adapterID)
	}
	query += ` LIMIT 1`
	var hash string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("query git commit hash: %w", err)
	}
	return hash, nil
}

func (s *Store) IndexedFiles(ctx context.Context, rootPath string, adapterID string) (map[string]IndexedFileRow, error) {
	cleanRoot := filepath.Clean(rootPath)
	if err := s.ensureProjectsExist(ctx, cleanRoot); err != nil {
		return nil, err
	}

	query := `SELECT f.relative_path, f.abs_path, f.content_hash, f.line_count
		FROM files f
		JOIN projects p ON p.id = f.project_id
		WHERE p.root_path = ?`
	args := []any{cleanRoot}
	if strings.TrimSpace(adapterID) != "" {
		query += ` AND p.adapter_id = ?`
		args = append(args, adapterID)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query indexed files: %w", err)
	}
	defer rows.Close()

	result := map[string]IndexedFileRow{}
	for rows.Next() {
		var row IndexedFileRow
		if err := rows.Scan(&row.RelativePath, &row.AbsPath, &row.ContentHash, &row.LineCount); err != nil {
			return nil, fmt.Errorf("scan indexed file: %w", err)
		}
		result[row.RelativePath] = row
	}
	return result, rows.Err()
}

func (s *Store) SearchChunks(ctx context.Context, rootPath string, query string, limit int) ([]SearchHit, error) {
	cleanRoot := filepath.Clean(rootPath)
	if err := s.ensureProjectsExist(ctx, cleanRoot); err != nil {
		return nil, err
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("empty search query")
	}

	terms := tokenizeSearchTerms(query)
	args := []any{query, query + "%", "%" + query + "%", "%" + query + "%"}

	var scoreExpr strings.Builder
	scoreExpr.WriteString(`CASE WHEN c.retrieval_text LIKE '%' || ? || '%' THEN 8.0 ELSE 0.0 END`)
	scoreExpr.WriteString(` + CASE WHEN c.name LIKE ? THEN 4.0 ELSE 0.0 END`)
	scoreExpr.WriteString(` + CASE WHEN c.header_text LIKE ? THEN 3.0 ELSE 0.0 END`)
	scoreExpr.WriteString(` + CASE WHEN c.text LIKE ? THEN 2.0 ELSE 0.0 END`)

	var where strings.Builder
	where.WriteString(`c.project_id IN (SELECT id FROM projects WHERE root_path = ?) AND (`)
	where.WriteString(`c.retrieval_text LIKE '%' || ? || '%' OR c.name LIKE ? OR c.header_text LIKE ? OR c.text LIKE ?`)
	whereArgs := []any{cleanRoot, query, query + "%", "%" + query + "%", "%" + query + "%"}
	for _, term := range terms {
		scoreExpr.WriteString(` + CASE WHEN c.retrieval_text LIKE '%' || ? || '%' THEN 1.25 ELSE 0.0 END`)
		scoreExpr.WriteString(` + CASE WHEN c.header_text LIKE '%' || ? || '%' THEN 0.75 ELSE 0.0 END`)
		args = append(args, term, term)
		where.WriteString(` OR c.retrieval_text LIKE '%' || ? || '%'`)
		whereArgs = append(whereArgs, term)
	}
	where.WriteString(`)`)
	args = append(args, whereArgs...)
	args = append(args, limit)

	rows, err := s.db.QueryContext(
		ctx,
		fmt.Sprintf(`SELECT
			c.id,
			c.file_id,
			COALESCE(c.primary_symbol_id, 0),
			f.abs_path,
			c.start_line,
			c.end_line,
			c.kind,
			c.name,
			COALESCE(s.display_name, ''),
			COALESCE(s.signature, ''),
			c.header_text,
			c.text,
			(%s) AS score
		FROM chunks c
		JOIN files f ON f.id = c.file_id
		LEFT JOIN symbols s ON s.id = c.primary_symbol_id
		WHERE %s
		ORDER BY score DESC, c.start_line
		LIMIT ?`, scoreExpr.String(), where.String()),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("search chunks: %w", err)
	}
	defer rows.Close()

	var hits []SearchHit
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(
			&hit.ChunkID,
			&hit.FileID,
			&hit.PrimarySymbolID,
			&hit.Path,
			&hit.StartLine,
			&hit.EndLine,
			&hit.Kind,
			&hit.Name,
			&hit.DisplayName,
			&hit.Signature,
			&hit.HeaderText,
			&hit.Text,
			&hit.Score,
		); err != nil {
			return nil, fmt.Errorf("scan search hit: %w", err)
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func (s *Store) SemanticSearchChunks(ctx context.Context, rootPath string, vector []float32, limit int) ([]SearchHit, error) {
	cleanRoot := filepath.Clean(rootPath)
	if err := s.ensureProjectsExist(ctx, cleanRoot); err != nil {
		return nil, err
	}

	queryBlob, err := vec.SerializeFloat32(vector)
	if err != nil {
		return nil, fmt.Errorf("serialize query vector: %w", err)
	}

	rows, err := s.db.QueryContext(
		ctx,
		`WITH knn AS (
			SELECT
				rowid AS chunk_id,
				distance
			FROM vec_chunks
			WHERE embedding MATCH ?
			ORDER BY distance
			LIMIT ?
		)
		SELECT
			c.id,
			c.file_id,
			COALESCE(c.primary_symbol_id, 0),
			f.abs_path,
			c.start_line,
			c.end_line,
			c.kind,
			c.name,
			COALESCE(s.display_name, ''),
			COALESCE(s.signature, ''),
			c.header_text,
			c.text,
			knn.distance
		FROM knn
		JOIN chunks c ON c.id = knn.chunk_id
		JOIN files f ON f.id = c.file_id
		LEFT JOIN symbols s ON s.id = c.primary_symbol_id
		WHERE c.project_id IN (SELECT id FROM projects WHERE root_path = ?)
		ORDER BY knn.distance, c.start_line
		LIMIT ?`,
		queryBlob,
		limit,
		cleanRoot,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("semantic chunk search: %w", err)
	}
	defer rows.Close()

	var hits []SearchHit
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(
			&hit.ChunkID,
			&hit.FileID,
			&hit.PrimarySymbolID,
			&hit.Path,
			&hit.StartLine,
			&hit.EndLine,
			&hit.Kind,
			&hit.Name,
			&hit.DisplayName,
			&hit.Signature,
			&hit.HeaderText,
			&hit.Text,
			&hit.Score,
		); err != nil {
			return nil, fmt.Errorf("scan semantic hit: %w", err)
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func (s *Store) SearchSymbols(ctx context.Context, rootPath string, query string, limit int) ([]SymbolSearchHit, error) {
	cleanRoot := filepath.Clean(rootPath)
	if err := s.ensureProjectsExist(ctx, cleanRoot); err != nil {
		return nil, err
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("empty symbol query")
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			s.id,
			s.scip_symbol,
			s.display_name,
			s.kind,
			COALESCE(f.abs_path, ''),
			s.def_start_line,
			s.def_start_col,
			s.def_end_line,
			s.def_end_col,
			s.doc_summary,
			s.signature,
			s.enclosing_symbol,
			CASE
				WHEN s.display_name = ? THEN 1.0
				WHEN s.scip_symbol = ? THEN 0.98
				WHEN s.display_name LIKE ? THEN 0.86
				WHEN s.scip_symbol LIKE ? THEN 0.82
				WHEN s.doc_summary LIKE ? THEN 0.55
				WHEN s.signature LIKE ? THEN 0.45
				ELSE 0.30
			END AS score
		FROM symbols s
		LEFT JOIN files f ON f.id = s.file_id
		WHERE s.project_id IN (SELECT id FROM projects WHERE root_path = ?) AND (
			s.display_name = ? OR
			s.scip_symbol = ? OR
			s.display_name LIKE ? OR
			s.scip_symbol LIKE ? OR
			s.doc_summary LIKE ? OR
			s.signature LIKE ?
		)
		ORDER BY score DESC, LENGTH(s.display_name), s.display_name
		LIMIT ?`,
		query,
		query,
		query+"%",
		"%"+query+"%",
		"%"+query+"%",
		"%"+query+"%",
		cleanRoot,
		query,
		query,
		query+"%",
		"%"+query+"%",
		"%"+query+"%",
		"%"+query+"%",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search symbols: %w", err)
	}
	defer rows.Close()

	var hits []SymbolSearchHit
	for rows.Next() {
		var hit SymbolSearchHit
		if err := rows.Scan(
			&hit.SymbolID,
			&hit.ScipSymbol,
			&hit.DisplayName,
			&hit.Kind,
			&hit.Path,
			&hit.StartLine,
			&hit.StartCol,
			&hit.EndLine,
			&hit.EndCol,
			&hit.DocSummary,
			&hit.Signature,
			&hit.Enclosing,
			&hit.Score,
		); err != nil {
			return nil, fmt.Errorf("scan symbol hit: %w", err)
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func (s *Store) SemanticSearchSymbols(ctx context.Context, rootPath string, vector []float32, limit int) ([]SymbolSearchHit, error) {
	cleanRoot := filepath.Clean(rootPath)
	if err := s.ensureProjectsExist(ctx, cleanRoot); err != nil {
		return nil, err
	}

	queryBlob, err := vec.SerializeFloat32(vector)
	if err != nil {
		return nil, fmt.Errorf("serialize symbol query vector: %w", err)
	}

	rows, err := s.db.QueryContext(
		ctx,
		`WITH knn AS (
			SELECT
				rowid AS symbol_id,
				distance
			FROM vec_symbols
			WHERE embedding MATCH ?
			ORDER BY distance
			LIMIT ?
		)
		SELECT
			s.id,
			s.scip_symbol,
			s.display_name,
			s.kind,
			COALESCE(f.abs_path, ''),
			s.def_start_line,
			s.def_start_col,
			s.def_end_line,
			s.def_end_col,
			s.doc_summary,
			s.signature,
			s.enclosing_symbol,
			knn.distance
		FROM knn
		JOIN symbols s ON s.id = knn.symbol_id
		LEFT JOIN files f ON f.id = s.file_id
		WHERE s.project_id IN (SELECT id FROM projects WHERE root_path = ?)
		ORDER BY knn.distance, s.display_name
		LIMIT ?`,
		queryBlob,
		limit,
		cleanRoot,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("semantic symbol search: %w", err)
	}
	defer rows.Close()

	var hits []SymbolSearchHit
	for rows.Next() {
		var hit SymbolSearchHit
		if err := rows.Scan(
			&hit.SymbolID,
			&hit.ScipSymbol,
			&hit.DisplayName,
			&hit.Kind,
			&hit.Path,
			&hit.StartLine,
			&hit.StartCol,
			&hit.EndLine,
			&hit.EndCol,
			&hit.DocSummary,
			&hit.Signature,
			&hit.Enclosing,
			&hit.Score,
		); err != nil {
			return nil, fmt.Errorf("scan semantic symbol hit: %w", err)
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func (s *Store) FindDefinition(ctx context.Context, rootPath string, symbol string) (*DefinitionResult, error) {
	results, err := s.FindDefinitions(ctx, rootPath, symbol, 1)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return &results[0], nil
}

func (s *Store) FindDefinitions(ctx context.Context, rootPath string, symbol string, limit int) ([]DefinitionResult, error) {
	cleanRoot := filepath.Clean(rootPath)
	if err := s.ensureProjectsExist(ctx, cleanRoot); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			s.id, s.scip_symbol, s.display_name, s.kind,
			COALESCE(f.abs_path, ''), s.def_start_line, s.def_start_col, s.def_end_line, s.def_end_col,
			s.doc_summary, s.signature, s.enclosing_symbol
		FROM symbols s
		LEFT JOIN files f ON f.id = s.file_id
		WHERE s.project_id IN (SELECT id FROM projects WHERE root_path = ?) AND (
			s.display_name = ? OR s.scip_symbol = ? OR s.display_name LIKE ? OR s.scip_symbol LIKE ?
		)
		ORDER BY
			CASE
				WHEN s.display_name = ? THEN 0
				WHEN s.scip_symbol = ? THEN 1
				WHEN s.display_name LIKE ? THEN 2
				ELSE 3
			END,
			CASE WHEN COALESCE(f.abs_path, '') LIKE '%/tests/%' THEN 1 ELSE 0 END,
			LENGTH(s.display_name),
			COALESCE(f.abs_path, ''),
			s.def_start_line
		LIMIT ?`,
		cleanRoot,
		symbol,
		symbol,
		"%"+symbol+"%",
		"%"+symbol+"%",
		symbol,
		symbol,
		symbol+"%",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("find definitions: %w", err)
	}
	defer rows.Close()

	var results []DefinitionResult
	for rows.Next() {
		var result DefinitionResult
		if err := rows.Scan(
			&result.SymbolID,
			&result.ScipSymbol,
			&result.DisplayName,
			&result.Kind,
			&result.Path,
			&result.StartLine,
			&result.StartCol,
			&result.EndLine,
			&result.EndCol,
			&result.DocSummary,
			&result.Signature,
			&result.Enclosing,
		); err != nil {
			return nil, fmt.Errorf("scan definition candidate: %w", err)
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (s *Store) ListReferences(ctx context.Context, rootPath string, symbol string, limit int) ([]ReferenceResult, error) {
	def, err := s.FindDefinition(ctx, rootPath, symbol)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, nil
	}
	return s.ListReferencesBySymbolID(ctx, rootPath, def.SymbolID, limit)
}

func (s *Store) ListReferencesBySymbolIDs(ctx context.Context, rootPath string, symbolIDs []int64, limit int) ([]ReferenceResult, error) {
	cleanRoot := filepath.Clean(rootPath)
	if err := s.ensureProjectsExist(ctx, cleanRoot); err != nil {
		return nil, err
	}
	if len(symbolIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}

	seedPlaceholders := make([]string, 0, len(symbolIDs))
	args := make([]any, 0, len(symbolIDs)+3)
	for _, symbolID := range symbolIDs {
		seedPlaceholders = append(seedPlaceholders, "(?)")
		args = append(args, symbolID)
	}
	args = append(args, cleanRoot, cleanRoot, limit)

	query := fmt.Sprintf(`WITH seed(symbol_id) AS (VALUES %s),
		reference_family(symbol_id) AS (
			SELECT symbol_id FROM seed
			UNION
			SELECT e.dst_symbol_id
			FROM edges e
			WHERE e.project_id IN (SELECT id FROM projects WHERE root_path = ?) AND e.src_symbol_id IN (SELECT symbol_id FROM seed) AND e.kind = 'reference'
			UNION
			SELECT e.src_symbol_id
			FROM edges e
			WHERE e.project_id IN (SELECT id FROM projects WHERE root_path = ?) AND e.dst_symbol_id IN (SELECT symbol_id FROM seed) AND e.kind = 'reference'
		)
		SELECT
			o.symbol_id,
			s.display_name,
			f.abs_path,
			o.start_line,
			o.start_col,
			o.end_line,
			o.end_col,
			o.syntax_kind
		FROM occurrences o
		JOIN symbols s ON s.id = o.symbol_id
		JOIN files f ON f.id = o.file_id
		WHERE o.symbol_id IN (SELECT symbol_id FROM reference_family) AND o.is_definition = 0
		ORDER BY f.abs_path, o.start_line, o.start_col
		LIMIT ?`, strings.Join(seedPlaceholders, ", "))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list references by symbol IDs: %w", err)
	}
	defer rows.Close()

	var refs []ReferenceResult
	for rows.Next() {
		var ref ReferenceResult
		if err := rows.Scan(
			&ref.SymbolID,
			&ref.DisplayName,
			&ref.Path,
			&ref.StartLine,
			&ref.StartCol,
			&ref.EndLine,
			&ref.EndCol,
			&ref.SyntaxKind,
		); err != nil {
			return nil, fmt.Errorf("scan reference: %w", err)
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (s *Store) ListReferencesBySymbolID(ctx context.Context, rootPath string, symbolID int64, limit int) ([]ReferenceResult, error) {
	return s.ListReferencesBySymbolIDs(ctx, rootPath, []int64{symbolID}, limit)
}

func tokenizeSearchTerms(query string) []string {
	fields := strings.Fields(strings.ToLower(query))
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		term := strings.Trim(field, " \t\r\n.,:;!?()[]{}<>\"'`")
		if len(term) < 3 {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, term)
	}
	return out
}

func (s *Store) DefinitionChunk(ctx context.Context, rootPath string, symbolID int64) (*SearchHit, error) {
	cleanRoot := filepath.Clean(rootPath)
	if err := s.ensureProjectsExist(ctx, cleanRoot); err != nil {
		return nil, err
	}

	row := s.db.QueryRowContext(
		ctx,
		`SELECT
			c.id, c.file_id, COALESCE(c.primary_symbol_id, 0), f.abs_path,
			c.start_line, c.end_line, c.kind, c.name, COALESCE(s.display_name, ''), COALESCE(s.signature, ''), c.header_text, c.text, 0.0
		FROM chunks c
		JOIN files f ON f.id = c.file_id
		LEFT JOIN symbols s ON s.id = c.primary_symbol_id
		WHERE c.project_id IN (SELECT id FROM projects WHERE root_path = ?) AND c.primary_symbol_id = ?
		ORDER BY c.start_line
		LIMIT 1`,
		cleanRoot,
		symbolID,
	)

	var hit SearchHit
	if err := row.Scan(
		&hit.ChunkID,
		&hit.FileID,
		&hit.PrimarySymbolID,
		&hit.Path,
		&hit.StartLine,
		&hit.EndLine,
		&hit.Kind,
		&hit.Name,
		&hit.DisplayName,
		&hit.Signature,
		&hit.HeaderText,
		&hit.Text,
		&hit.Score,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("definition chunk: %w", err)
	}
	return &hit, nil
}

func (s *Store) LinkedSymbolsForChunks(ctx context.Context, rootPath string, chunkIDs []int64) ([]ChunkSymbolLink, error) {
	cleanRoot := filepath.Clean(rootPath)
	if err := s.ensureProjectsExist(ctx, cleanRoot); err != nil {
		return nil, err
	}
	if len(chunkIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, 0, len(chunkIDs))
	args := make([]any, 0, len(chunkIDs)+1)
	args = append(args, cleanRoot)
	for _, chunkID := range chunkIDs {
		placeholders = append(placeholders, "?")
		args = append(args, chunkID)
	}

	rows, err := s.db.QueryContext(
		ctx,
		fmt.Sprintf(`SELECT chunk_id, symbol_id, role, weight
		FROM chunk_symbols
		WHERE project_id IN (SELECT id FROM projects WHERE root_path = ?) AND chunk_id IN (%s)
		ORDER BY chunk_id, weight DESC, symbol_id`, strings.Join(placeholders, ",")),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("linked chunk symbols: %w", err)
	}
	defer rows.Close()

	var out []ChunkSymbolLink
	for rows.Next() {
		var link ChunkSymbolLink
		if err := rows.Scan(&link.ChunkID, &link.SymbolID, &link.Role, &link.Weight); err != nil {
			return nil, fmt.Errorf("scan linked chunk symbol: %w", err)
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

func (s *Store) NeighborChunks(ctx context.Context, rootPath string, fileID int64, excludeChunkID int64, limit int) ([]SearchHit, error) {
	cleanRoot := filepath.Clean(rootPath)
	if err := s.ensureProjectsExist(ctx, cleanRoot); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			c.id, c.file_id, COALESCE(c.primary_symbol_id, 0), f.abs_path,
			c.start_line, c.end_line, c.kind, c.name, COALESCE(s.display_name, ''), COALESCE(s.signature, ''), c.header_text, c.text, 0.0
		FROM chunks c
		JOIN files f ON f.id = c.file_id
		LEFT JOIN symbols s ON s.id = c.primary_symbol_id
		WHERE c.project_id IN (SELECT id FROM projects WHERE root_path = ?) AND c.file_id = ? AND c.id != ?
		ORDER BY ABS(c.start_line - (
			SELECT start_line FROM chunks WHERE id = ?
		)), c.start_line
		LIMIT ?`,
		cleanRoot,
		fileID,
		excludeChunkID,
		excludeChunkID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("neighbor chunks: %w", err)
	}
	defer rows.Close()

	var hits []SearchHit
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(
			&hit.ChunkID,
			&hit.FileID,
			&hit.PrimarySymbolID,
			&hit.Path,
			&hit.StartLine,
			&hit.EndLine,
			&hit.Kind,
			&hit.Name,
			&hit.DisplayName,
			&hit.Signature,
			&hit.HeaderText,
			&hit.Text,
			&hit.Score,
		); err != nil {
			return nil, fmt.Errorf("scan neighbor chunk: %w", err)
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func (s *Store) RelatedChunks(ctx context.Context, rootPath string, symbolID int64, limit int) ([]RelatedChunk, error) {
	cleanRoot := filepath.Clean(rootPath)
	if err := s.ensureProjectsExist(ctx, cleanRoot); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT relation_kind, direction, chunk_id, file_id, symbol_id, path, start_line, end_line, kind, name, COALESCE(display_name, ''), COALESCE(signature, ''), header_text, text, score
		FROM (
			SELECT
				e.kind AS relation_kind,
				'outgoing' AS direction,
				c.id AS chunk_id,
				c.file_id AS file_id,
				c.primary_symbol_id AS symbol_id,
				f.abs_path AS path,
				c.start_line AS start_line,
				c.end_line AS end_line,
				c.kind AS kind,
				c.name AS name,
				s.display_name AS display_name,
				s.signature AS signature,
				c.header_text AS header_text,
				c.text AS text,
				0.0 AS score
			FROM edges e
			JOIN chunks c ON c.primary_symbol_id = e.dst_symbol_id
			JOIN files f ON f.id = c.file_id
			LEFT JOIN symbols s ON s.id = c.primary_symbol_id
			WHERE e.project_id IN (SELECT id FROM projects WHERE root_path = ?) AND e.src_symbol_id = ? AND c.project_id IN (SELECT id FROM projects WHERE root_path = ?)
			UNION ALL
			SELECT
				e.kind AS relation_kind,
				'incoming' AS direction,
				c.id AS chunk_id,
				c.file_id AS file_id,
				c.primary_symbol_id AS symbol_id,
				f.abs_path AS path,
				c.start_line AS start_line,
				c.end_line AS end_line,
				c.kind AS kind,
				c.name AS name,
				s.display_name AS display_name,
				s.signature AS signature,
				c.header_text AS header_text,
				c.text AS text,
				0.0 AS score
			FROM edges e
			JOIN chunks c ON c.primary_symbol_id = e.src_symbol_id
			JOIN files f ON f.id = c.file_id
			LEFT JOIN symbols s ON s.id = c.primary_symbol_id
			WHERE e.project_id IN (SELECT id FROM projects WHERE root_path = ?) AND e.dst_symbol_id = ? AND c.project_id IN (SELECT id FROM projects WHERE root_path = ?)
		)
		ORDER BY relation_kind, direction, path, start_line
		LIMIT ?`,
		cleanRoot, symbolID, cleanRoot,
		cleanRoot, symbolID, cleanRoot,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("related chunks: %w", err)
	}
	defer rows.Close()

	var result []RelatedChunk
	seen := map[string]struct{}{}
	for rows.Next() {
		var item RelatedChunk
		if err := rows.Scan(
			&item.RelationKind,
			&item.Direction,
			&item.ChunkID,
			&item.FileID,
			&item.SymbolID,
			&item.Path,
			&item.StartLine,
			&item.EndLine,
			&item.Kind,
			&item.Name,
			&item.DisplayName,
			&item.Signature,
			&item.HeaderText,
			&item.Text,
			&item.Score,
		); err != nil {
			return nil, fmt.Errorf("scan related chunk: %w", err)
		}
		key := fmt.Sprintf("%s\x00%s\x00%d", item.RelationKind, item.Direction, item.ChunkID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ensureProjectsExist(ctx context.Context, rootPath string) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM projects WHERE root_path = ?`, filepath.Clean(rootPath)).Scan(&count); err != nil {
		return fmt.Errorf("lookup project: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("project %s is not indexed", rootPath)
	}
	return nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
