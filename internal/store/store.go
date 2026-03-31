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
	chunkEmbeddingDimensions  = 768
	symbolEmbeddingDimensions = 768
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
}

type FileData struct {
	RelativePath string
	AbsPath      string
	Language     string
	ContentHash  string
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

type IndexPayload struct {
	Project     ProjectData
	Files       []FileData
	Symbols     []SymbolData
	Occurrences []OccurrenceData
	Chunks      []ChunkData
	Edges       []EdgeData
	Embeddings  []EmbeddingData
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
}

type RelatedChunk struct {
	RelationKind string  `json:"relation_kind"`
	Direction    string  `json:"direction"`
	ChunkID      int64   `json:"chunk_id"`
	FileID       int64   `json:"file_id"`
	SymbolID     int64   `json:"symbol_id"`
	Path         string  `json:"path"`
	StartLine    int     `json:"start_line"`
	EndLine      int     `json:"end_line"`
	Kind         string  `json:"kind"`
	Name         string  `json:"name"`
	HeaderText   string  `json:"header_text"`
	Text         string  `json:"text"`
	Score        float64 `json:"score"`
}

type SearchHit struct {
	ChunkID         int64   `json:"chunk_id"`
	FileID          int64   `json:"file_id"`
	PrimarySymbolID int64   `json:"primary_symbol_id"`
	Path            string  `json:"path"`
	StartLine       int     `json:"start_line"`
	EndLine         int     `json:"end_line"`
	Kind            string  `json:"kind"`
	Name            string  `json:"name"`
	HeaderText      string  `json:"header_text"`
	Text            string  `json:"text"`
	Score           float64 `json:"score"`
}

type SymbolSearchHit struct {
	SymbolID    int64   `json:"symbol_id"`
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
	SymbolID    int64  `json:"symbol_id"`
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
	SymbolID    int64  `json:"symbol_id"`
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
			root_path TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			language TEXT NOT NULL,
			manifest_path TEXT NOT NULL,
			environment_source TEXT NOT NULL,
			adapter_id TEXT NOT NULL DEFAULT '',
			scip_artifact_path TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			tool_version TEXT NOT NULL,
			last_indexed_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			relative_path TEXT NOT NULL,
			abs_path TEXT NOT NULL,
			language TEXT NOT NULL,
			content_hash TEXT NOT NULL,
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
	if _, err := s.db.Exec(fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vec_chunks USING vec0(
		embedding float[%d]
	)`, chunkEmbeddingDimensions)); err != nil {
		return fmt.Errorf("create vector table: %w", err)
	}
	if _, err := s.db.Exec(fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vec_symbols USING vec0(
		embedding float[%d]
	)`, symbolEmbeddingDimensions)); err != nil {
		return fmt.Errorf("create symbol vector table: %w", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE projects ADD COLUMN adapter_id TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate adapter_id column: %w", err)
	}
	return nil
}

func (s *Store) ReplaceProjectIndex(ctx context.Context, payload IndexPayload) error {
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
	oldErr := tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE root_path = ?`, payload.Project.RootPath).Scan(&oldProjectID)
	if oldErr == nil {
		if _, err = tx.ExecContext(ctx, `DELETE FROM vec_chunks WHERE rowid IN (SELECT id FROM chunks WHERE project_id = ?)`, oldProjectID); err != nil {
			return fmt.Errorf("delete old vector rows: %w", err)
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM vec_symbols WHERE rowid IN (SELECT id FROM symbols WHERE project_id = ?)`, oldProjectID); err != nil {
			return fmt.Errorf("delete old symbol vector rows: %w", err)
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
			tool_name, tool_version, last_indexed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		payload.Project.RootPath,
		payload.Project.Name,
		payload.Project.Language,
		payload.Project.ManifestPath,
		payload.Project.EnvironmentSource,
		payload.Project.AdapterID,
		payload.Project.ScipArtifactPath,
		payload.Project.ToolName,
		payload.Project.ToolVersion,
		now,
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
			`INSERT INTO files (project_id, relative_path, abs_path, language, content_hash)
			 VALUES (?, ?, ?, ?, ?)`,
			projectID,
			file.RelativePath,
			file.AbsPath,
			file.Language,
			file.ContentHash,
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
		case "symbol":
			symbolID, ok := symbolIDs[embedding.OwnerKey]
			if !ok {
				continue
			}
			if _, execErr := tx.ExecContext(
				ctx,
				`INSERT INTO vec_symbols (rowid, embedding) VALUES (?, ?)`,
				symbolID,
				blob,
			); execErr != nil {
				return fmt.Errorf("insert symbol vector for %s: %w", embedding.OwnerKey, execErr)
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
	query += ` ORDER BY p.root_path`

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

func (s *Store) IndexedFiles(ctx context.Context, rootPath string) (map[string]IndexedFileRow, error) {
	projectID, err := s.projectID(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT relative_path, abs_path, content_hash FROM files WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, fmt.Errorf("query indexed files: %w", err)
	}
	defer rows.Close()

	result := map[string]IndexedFileRow{}
	for rows.Next() {
		var row IndexedFileRow
		if err := rows.Scan(&row.RelativePath, &row.AbsPath, &row.ContentHash); err != nil {
			return nil, fmt.Errorf("scan indexed file: %w", err)
		}
		result[row.RelativePath] = row
	}
	return result, rows.Err()
}

func (s *Store) SearchChunks(ctx context.Context, rootPath string, query string, limit int) ([]SearchHit, error) {
	projectID, err := s.projectID(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("empty search query")
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			c.id,
			c.file_id,
			COALESCE(c.primary_symbol_id, 0),
			f.abs_path,
			c.start_line,
			c.end_line,
			c.kind,
			c.name,
			c.header_text,
			c.text,
			0.0
		FROM chunks c
		JOIN files f ON f.id = c.file_id
		WHERE c.project_id = ? AND c.retrieval_text LIKE ?
		ORDER BY c.start_line
		LIMIT ?`,
		projectID,
		"%"+query+"%",
		limit,
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
	projectID, err := s.projectID(ctx, rootPath)
	if err != nil {
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
			c.header_text,
			c.text,
			knn.distance
		FROM knn
		JOIN chunks c ON c.id = knn.chunk_id
		JOIN files f ON f.id = c.file_id
		WHERE c.project_id = ?
		ORDER BY knn.distance, c.start_line
		LIMIT ?`,
		queryBlob,
		limit,
		projectID,
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
	projectID, err := s.projectID(ctx, rootPath)
	if err != nil {
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
		WHERE s.project_id = ? AND (
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
		projectID,
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
	projectID, err := s.projectID(ctx, rootPath)
	if err != nil {
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
		WHERE s.project_id = ?
		ORDER BY knn.distance, s.display_name
		LIMIT ?`,
		queryBlob,
		limit,
		projectID,
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
	projectID, err := s.projectID(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	row := s.db.QueryRowContext(
		ctx,
		`SELECT
			s.id, s.scip_symbol, s.display_name, s.kind,
			COALESCE(f.abs_path, ''), s.def_start_line, s.def_start_col, s.def_end_line, s.def_end_col,
			s.doc_summary, s.signature, s.enclosing_symbol
		FROM symbols s
		LEFT JOIN files f ON f.id = s.file_id
		WHERE s.project_id = ? AND (
			s.display_name = ? OR s.scip_symbol = ? OR s.display_name LIKE ? OR s.scip_symbol LIKE ?
		)
		ORDER BY
			CASE
				WHEN s.display_name = ? THEN 0
				WHEN s.scip_symbol = ? THEN 1
				WHEN s.display_name LIKE ? THEN 2
				ELSE 3
			END,
			LENGTH(s.display_name)
		LIMIT 1`,
		projectID,
		symbol,
		symbol,
		"%"+symbol+"%",
		"%"+symbol+"%",
		symbol,
		symbol,
		"%"+symbol+"%",
	)

	var result DefinitionResult
	if err := row.Scan(
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
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("find definition: %w", err)
	}
	return &result, nil
}

func (s *Store) ListReferences(ctx context.Context, rootPath string, symbol string, limit int) ([]ReferenceResult, error) {
	def, err := s.FindDefinition(ctx, rootPath, symbol)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, nil
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
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
		WHERE o.symbol_id = ? AND o.is_definition = 0
		ORDER BY f.abs_path, o.start_line, o.start_col
		LIMIT ?`,
		def.SymbolID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list references: %w", err)
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

func (s *Store) DefinitionChunk(ctx context.Context, rootPath string, symbolID int64) (*SearchHit, error) {
	projectID, err := s.projectID(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	row := s.db.QueryRowContext(
		ctx,
		`SELECT
			c.id, c.file_id, COALESCE(c.primary_symbol_id, 0), f.abs_path,
			c.start_line, c.end_line, c.kind, c.name, c.header_text, c.text, 0.0
		FROM chunks c
		JOIN files f ON f.id = c.file_id
		WHERE c.project_id = ? AND c.primary_symbol_id = ?
		ORDER BY c.start_line
		LIMIT 1`,
		projectID,
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

func (s *Store) NeighborChunks(ctx context.Context, rootPath string, fileID int64, excludeChunkID int64, limit int) ([]SearchHit, error) {
	projectID, err := s.projectID(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			c.id, c.file_id, COALESCE(c.primary_symbol_id, 0), f.abs_path,
			c.start_line, c.end_line, c.kind, c.name, c.header_text, c.text, 0.0
		FROM chunks c
		JOIN files f ON f.id = c.file_id
		WHERE c.project_id = ? AND c.file_id = ? AND c.id != ?
		ORDER BY ABS(c.start_line - (
			SELECT start_line FROM chunks WHERE id = ?
		)), c.start_line
		LIMIT ?`,
		projectID,
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
	projectID, err := s.projectID(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT relation_kind, direction, chunk_id, file_id, symbol_id, path, start_line, end_line, kind, name, header_text, text, score
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
				c.header_text AS header_text,
				c.text AS text,
				0.0 AS score
			FROM edges e
			JOIN chunks c ON c.primary_symbol_id = e.dst_symbol_id
			JOIN files f ON f.id = c.file_id
			WHERE e.project_id = ? AND e.src_symbol_id = ? AND c.project_id = ?
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
				c.header_text AS header_text,
				c.text AS text,
				0.0 AS score
			FROM edges e
			JOIN chunks c ON c.primary_symbol_id = e.src_symbol_id
			JOIN files f ON f.id = c.file_id
			WHERE e.project_id = ? AND e.dst_symbol_id = ? AND c.project_id = ?
		)
		ORDER BY relation_kind, direction, path, start_line
		LIMIT ?`,
		projectID, symbolID, projectID,
		projectID, symbolID, projectID,
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

func (s *Store) projectID(ctx context.Context, rootPath string) (int64, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id FROM projects WHERE root_path = ?`, filepath.Clean(rootPath))
	var projectID int64
	if err := row.Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("project %s is not indexed", rootPath)
		}
		return 0, fmt.Errorf("lookup project: %w", err)
	}
	return projectID, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
