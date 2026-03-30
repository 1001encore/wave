package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type ONNXProvider struct {
	PythonPath string
	ScriptPath string
	ModelDir   string
	ModelName  string
}

type onnxRequest struct {
	ModelDir string   `json:"model_dir"`
	Texts    []string `json:"texts"`
}

type onnxResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (p ONNXProvider) Name() string {
	if p.ModelName != "" {
		return p.ModelName
	}
	return "onnx"
}

func (p ONNXProvider) Embed(ctx context.Context, docs []Document) ([]Vector, error) {
	if len(docs) == 0 {
		return nil, nil
	}

	texts := make([]string, 0, len(docs))
	for _, doc := range docs {
		texts = append(texts, doc.Text)
	}
	payload, err := json.Marshal(onnxRequest{
		ModelDir: p.ModelDir,
		Texts:    texts,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	cmd := exec.CommandContext(ctx, p.PythonPath, p.ScriptPath)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run onnx embedder: %w\n%s", err, stderr.String())
	}

	var response onnxResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(response.Embeddings) != len(docs) {
		return nil, fmt.Errorf("embedding count mismatch: got %d want %d", len(response.Embeddings), len(docs))
	}

	vectors := make([]Vector, 0, len(docs))
	for i, doc := range docs {
		vectors = append(vectors, Vector{
			OwnerType: doc.OwnerType,
			OwnerKey:  doc.OwnerKey,
			Values:    response.Embeddings[i],
		})
	}
	return vectors, nil
}

func ResolveONNXProvider(rootDir string) Provider {
	searchRoots := candidateRoots(rootDir)
	modelDir := firstExistingDir(
		append([]string{os.Getenv("WAVE_EMBED_MODEL_DIR")}, rootedPath(searchRoots, ".wave", "models", "CodeRankEmbed-onnx-int8")...)...,
	)
	if modelDir == "" {
		return NoopProvider{}
	}

	pythonPath := firstExistingFile(
		rootedPath(searchRoots, ".venv", "bin", "python")...,
	)
	if pythonPath == "" {
		return NoopProvider{}
	}

	scriptPath := firstExistingFile(
		rootedPath(searchRoots, "scripts", "embed_onnx.py")...,
	)
	if scriptPath == "" {
		return NoopProvider{}
	}

	return ONNXProvider{
		PythonPath: pythonPath,
		ScriptPath: scriptPath,
		ModelDir:   modelDir,
		ModelName:  "CodeRankEmbed-onnx-int8",
	}
}

func candidateRoots(rootDir string) []string {
	seen := map[string]struct{}{}
	roots := make([]string, 0, 6)
	add := func(path string) {
		if path == "" {
			return
		}
		clean := filepath.Clean(path)
		if _, ok := seen[clean]; ok {
			return
		}
		seen[clean] = struct{}{}
		roots = append(roots, clean)
	}

	add(rootDir)
	if cwd, err := os.Getwd(); err == nil {
		add(cwd)
	}
	if exe, err := os.Executable(); err == nil {
		add(filepath.Dir(exe))
		add(filepath.Dir(filepath.Dir(exe)))
	}
	return roots
}

func rootedPath(roots []string, parts ...string) []string {
	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		paths = append(paths, filepath.Join(append([]string{root}, parts...)...))
	}
	return paths
}

func firstExistingDir(paths ...string) string {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
	}
	return ""
}

func firstExistingFile(paths ...string) string {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
