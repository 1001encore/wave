package embed

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ONNXProvider struct {
	PythonPath string
	ScriptPath string
	ModelDir   string
	ModelName  string
	Device     string
	lastStats  Stats
}

type onnxRequest struct {
	ModelDir  string   `json:"model_dir"`
	Texts     []string `json:"texts"`
	BatchSize int      `json:"batch_size"`
	Device    string   `json:"device,omitempty"`
}

type onnxResponseHeader struct {
	Count          int     `json:"count"`
	Dim            int     `json:"dim"`
	Provider       string  `json:"provider"`
	RequestedBatch int     `json:"requested_batch"`
	SelectedBatch  int     `json:"selected_batch"`
	BatchCount     int     `json:"batch_count"`
	OOMRetries     int     `json:"oom_retries"`
	RequestMS      float64 `json:"request_ms"`
	PreloadMS      float64 `json:"preload_ms"`
	SessionMS      float64 `json:"session_ms"`
	TokenizeMS     float64 `json:"tokenize_ms"`
	InferMS        float64 `json:"infer_ms"`
	NormalizeMS    float64 `json:"normalize_ms"`
	SerializeMS    float64 `json:"serialize_ms"`
	TotalMS        float64 `json:"total_ms"`
}

type onnxBatchStat struct {
	Index        int     `json:"index"`
	Size         int     `json:"size"`
	Processed    int     `json:"processed"`
	TokenizeMS   float64 `json:"tokenize_ms"`
	InferMS      float64 `json:"infer_ms"`
	NormalizeMS  float64 `json:"normalize_ms"`
	RetryCount   int     `json:"retry_count"`
	SettledBatch int     `json:"settled_batch"`
}

const (
	embedModelName  = "all-MiniLM-L6-v2-code-search-512"
	embedModelRepo  = "isuruwijesiri/all-MiniLM-L6-v2-code-search-512"
	embedModelEnv   = "WAVE_EMBED_MODEL_DIR"
	embedCacheEnv   = "WAVE_EMBED_CACHE_DIR"
	embedPythonEnv  = "WAVE_EMBED_PYTHON"
	embedBatchEnv   = "WAVE_EMBED_BATCH_SIZE"
	embedScriptName = "embed_onnx.py"
)

var embedModelRequiredFiles = []string{
	"config.json",
	"tokenizer.json",
	"tokenizer_config.json",
	"special_tokens_map.json",
	"vocab.txt",
	"README.md",
}

var embedModelDownloadFiles = []string{
	"config.json",
	"tokenizer.json",
	"tokenizer_config.json",
	"special_tokens_map.json",
	"vocab.txt",
	"README.md",
	"onnx/model.onnx",
}

var embedModelONNXCandidates = []string{
	"model.onnx",
	"onnx/model.onnx",
}

//go:embed assets/embed_onnx.py
var embeddedONNXScript []byte

func (p *ONNXProvider) Name() string {
	if p.ModelName != "" {
		return p.ModelName
	}
	return "onnx"
}

func (p *ONNXProvider) Embed(ctx context.Context, docs []Document) ([]Vector, error) {
	if len(docs) == 0 {
		p.lastStats = Stats{}
		return nil, nil
	}

	requestedBatch := embedBatchSize()
	texts := make([]string, 0, len(docs))
	for _, doc := range docs {
		texts = append(texts, doc.Text)
	}
	payload, err := json.Marshal(onnxRequest{
		ModelDir:  p.ModelDir,
		Texts:     texts,
		BatchSize: requestedBatch,
		Device:    p.Device,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	cmd := exec.CommandContext(ctx, p.PythonPath, p.ScriptPath)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = appendNvidiaLibPath(os.Environ(), p.PythonPath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create onnx stderr pipe: %w", err)
	}
	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start onnx embedder: %w", err)
	}

	var stderrWG sync.WaitGroup
	stderrWG.Add(1)
	go func() {
		defer stderrWG.Done()
		readONNXStderr(stderrPipe, &stderr)
	}()

	if err := cmd.Wait(); err != nil {
		stderrWG.Wait()
		_, cleanStderr := parseONNXStderr(stderr.Bytes())
		return nil, fmt.Errorf("run onnx embedder: %w\n%s", err, cleanStderr)
	}
	stderrWG.Wait()
	decodeStartedAt := time.Now()
	batchStats, _ := parseONNXStderr(stderr.Bytes())

	header, payloadBytes, err := decodeONNXResponse(stdout.Bytes())
	if err != nil {
		return nil, err
	}
	if header.Provider == "CPUExecutionProvider" {
		if isCUDADevice(p.Device) {
			fmt.Fprintf(os.Stderr, "warning: CUDA was requested but embeddings fell back to CPU; install CUDA 12 + cuDNN 9 runtime libraries or run: %s -m pip install nvidia-cublas-cu12 nvidia-cudnn-cu12 nvidia-cuda-runtime-cu12 nvidia-cufft-cu12 nvidia-curand-cu12\n", p.PythonPath)
		} else if strings.TrimSpace(strings.ToLower(p.Device)) != "cpu" {
			fmt.Fprintln(os.Stderr, "info: embeddings are running on CPUExecutionProvider")
		}
	}
	if header.Count != len(docs) {
		return nil, fmt.Errorf("embedding count mismatch: got %d want %d", header.Count, len(docs))
	}
	if header.Dim <= 0 {
		return nil, fmt.Errorf("invalid embedding dimension: %d", header.Dim)
	}
	expectedBytes := header.Count * header.Dim * 4
	if len(payloadBytes) != expectedBytes {
		return nil, fmt.Errorf("embedding payload size mismatch: got %d want %d", len(payloadBytes), expectedBytes)
	}

	flat := make([]float32, header.Count*header.Dim)
	for i := range flat {
		offset := i * 4
		flat[i] = math.Float32frombits(binary.LittleEndian.Uint32(payloadBytes[offset : offset+4]))
	}

	vectors := make([]Vector, 0, len(docs))
	for i, doc := range docs {
		start := i * header.Dim
		end := start + header.Dim
		vectors = append(vectors, Vector{
			OwnerType: doc.OwnerType,
			OwnerKey:  doc.OwnerKey,
			Values:    flat[start:end],
		})
	}
	p.lastStats = Stats{
		Provider:       header.Provider,
		RequestedBatch: requestedBatch,
		SelectedBatch:  header.SelectedBatch,
		BatchCount:     header.BatchCount,
		OOMRetries:     header.OOMRetries,
		Documents:      header.Count,
		Dimensions:     header.Dim,
		RequestMS:      header.RequestMS,
		PreloadMS:      header.PreloadMS,
		SessionMS:      header.SessionMS,
		TokenizeMS:     header.TokenizeMS,
		InferMS:        header.InferMS,
		NormalizeMS:    header.NormalizeMS,
		SerializeMS:    header.SerializeMS,
		DecodeMS:       float64(time.Since(decodeStartedAt)) / float64(time.Millisecond),
		TotalMS:        maxFloat64(header.TotalMS, float64(time.Since(startedAt))/float64(time.Millisecond)),
		BatchStats:     batchStats,
	}
	return vectors, nil
}

func (p *ONNXProvider) LastStats() Stats {
	return p.lastStats
}

func readONNXStderr(r io.Reader, dst *bytes.Buffer) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		dst.WriteString(line)
		dst.WriteByte('\n')
	}
}

func decodeONNXResponse(stdout []byte) (onnxResponseHeader, []byte, error) {
	newline := bytes.IndexByte(stdout, '\n')
	if newline < 0 {
		return onnxResponseHeader{}, nil, fmt.Errorf("decode embedding response: missing header")
	}

	var header onnxResponseHeader
	if err := json.Unmarshal(stdout[:newline], &header); err != nil {
		return onnxResponseHeader{}, nil, fmt.Errorf("decode embedding response header: %w", err)
	}
	return header, stdout[newline+1:], nil
}

func embedBatchSize() int {
	if value := strings.TrimSpace(os.Getenv(embedBatchEnv)); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil {
			return 0
		}
		if n > 0 {
			return n
		}
	}
	return 0
}

func parseONNXStderr(stderr []byte) ([]BatchStats, string) {
	lines := strings.Split(strings.ReplaceAll(string(stderr), "\r\n", "\n"), "\n")
	batches := make([]BatchStats, 0, len(lines))
	other := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if item, ok := parseONNXBatchLine(line); ok {
			batches = append(batches, item)
			continue
		}
		other = append(other, line)
	}
	return batches, strings.Join(other, "\n")
}

func parseONNXBatchLine(line string) (BatchStats, bool) {
	const prefix = "WAVE_EMBED_BATCH "
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, prefix) {
		return BatchStats{}, false
	}
	var item onnxBatchStat
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &item); err != nil {
		return BatchStats{}, false
	}
	return BatchStats{
		Index:        item.Index,
		Size:         item.Size,
		Processed:    item.Processed,
		TokenizeMS:   item.TokenizeMS,
		InferMS:      item.InferMS,
		NormalizeMS:  item.NormalizeMS,
		RetryCount:   item.RetryCount,
		SettledBatch: item.SettledBatch,
	}, true
}

func ResolveONNXProvider(rootDir string, device string) (Provider, error) {
	searchRoots := candidateRoots(rootDir)
	modelDir, err := resolveModelDir(searchRoots)
	if err != nil {
		return nil, err
	}

	pythonPath, err := resolvePythonPath(searchRoots, device)
	if err != nil {
		return nil, err
	}

	scriptPath, err := ensureEmbeddedScript()
	if err != nil {
		return nil, err
	}

	return &ONNXProvider{
		PythonPath: pythonPath,
		ScriptPath: scriptPath,
		ModelDir:   modelDir,
		ModelName:  embedModelName,
		Device:     device,
	}, nil
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

func resolveModelDir(searchRoots []string) (string, error) {
	if override := strings.TrimSpace(os.Getenv(embedModelEnv)); override != "" {
		if !dirExists(override) {
			return "", fmt.Errorf("%s is set but directory does not exist: %s", embedModelEnv, override)
		}
		if err := ensureModelFiles(filepath.Clean(override)); err != nil {
			return "", err
		}
		return filepath.Clean(override), nil
	}

	candidates := append(
		rootedPath(searchRoots, ".wave", "models", embedModelName),
		defaultModelDir(),
	)
	for _, candidate := range candidates {
		if candidate == "" || !dirExists(candidate) {
			continue
		}
		if err := ensureModelFiles(candidate); err != nil {
			return "", err
		}
		return candidate, nil
	}

	target := defaultModelDir()
	if target == "" {
		return "", fmt.Errorf("cannot resolve model cache directory")
	}
	fmt.Fprintf(os.Stderr, "info: embedding model not found locally; installing into %s\n", target)
	if err := downloadModel(target); err != nil {
		return "", err
	}
	return target, nil
}

func resolvePythonPath(searchRoots []string, device string) (string, error) {
	if override := strings.TrimSpace(os.Getenv(embedPythonEnv)); override != "" {
		if !fileExists(override) {
			return "", fmt.Errorf("%s is set but file does not exist: %s", embedPythonEnv, override)
		}
		if err := ensurePythonDeps(override); err != nil {
			return "", err
		}
		return filepath.Clean(override), nil
	}

	basePython, err := resolveBasePython(searchRoots)
	if err != nil {
		return "", err
	}
	return ensureEmbeddedRuntime(basePython, device)
}

func resolveBasePython(searchRoots []string) (string, error) {
	if path := firstExistingFile(rootedPath(searchRoots, ".venv", "bin", "python")...); path != "" {
		return path, nil
	}
	if path, err := exec.LookPath("python3"); err == nil {
		return path, nil
	}
	if path, err := exec.LookPath("python"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("python runtime not found; expected project .venv/bin/python or python3 on PATH")
}

func ensureEmbeddedScript() (string, error) {
	cacheDir, err := userCacheDir()
	if err != nil {
		return "", err
	}
	scriptDir := filepath.Join(cacheDir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		return "", fmt.Errorf("create embed script directory: %w", err)
	}

	scriptPath := filepath.Join(scriptDir, embedScriptName)
	if existing, err := os.ReadFile(scriptPath); err == nil {
		if sha256.Sum256(existing) == sha256.Sum256(embeddedONNXScript) {
			return scriptPath, nil
		}
	}

	tmpPath := scriptPath + ".tmp"
	fmt.Fprintf(os.Stderr, "info: installing embedded onnx script to %s\n", scriptPath)
	if err := os.WriteFile(tmpPath, embeddedONNXScript, 0o755); err != nil {
		return "", fmt.Errorf("write embedded onnx script: %w", err)
	}
	if err := os.Rename(tmpPath, scriptPath); err != nil {
		return "", fmt.Errorf("install embedded onnx script: %w", err)
	}
	return scriptPath, nil
}

func ensureModelFiles(modelDir string) error {
	for _, rel := range embedModelRequiredFiles {
		path := filepath.Join(modelDir, rel)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return fmt.Errorf("embedding model is incomplete; missing %s", path)
		}
	}
	for _, rel := range embedModelONNXCandidates {
		path := filepath.Join(modelDir, rel)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return nil
		}
	}
	return fmt.Errorf("embedding model is incomplete; missing %s or %s", filepath.Join(modelDir, "model.onnx"), filepath.Join(modelDir, "onnx", "model.onnx"))
}

func downloadModel(modelDir string) error {
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return fmt.Errorf("create model directory: %w", err)
	}

	client := &http.Client{}
	for _, rel := range embedModelDownloadFiles {
		targetPath := filepath.Join(modelDir, rel)
		if info, err := os.Stat(targetPath); err == nil && !info.IsDir() {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("create model subdirectory for %s: %w", rel, err)
		}
		fmt.Fprintf(os.Stderr, "info: downloading embedding model file %s\n", rel)
		if err := downloadFile(client, modelURL(rel), targetPath); err != nil {
			return fmt.Errorf("download %s: %w", rel, err)
		}
	}
	return ensureModelFiles(modelDir)
}

func downloadFile(client *http.Client, url string, targetPath string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	tmpPath := targetPath + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, targetPath)
}

func modelURL(rel string) string {
	return fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s?download=1", embedModelRepo, rel)
}

func defaultModelDir() string {
	cacheDir, err := userCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cacheDir, "models", embedModelName)
}

func userCacheDir() (string, error) {
	if override := strings.TrimSpace(os.Getenv(embedCacheEnv)); override != "" {
		return filepath.Clean(override), nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	return filepath.Join(dir, "wave"), nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func maxFloat64(a float64, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func isCUDADevice(device string) bool {
	return device == "cuda" || device == "gpu"
}

// appendNvidiaLibPath detects pip-installed nvidia CUDA libraries under the
// Python runtime's site-packages and adds their lib dirs to LD_LIBRARY_PATH
// so onnxruntime can find them at session-creation time.
func appendNvidiaLibPath(environ []string, pythonPath string) []string {
	cmd := exec.Command(pythonPath, "-c", `
import pathlib, site, sys
try:
    sp = site.getsitepackages()
except Exception:
    sys.exit(0)
for s in sp:
    nvidia = pathlib.Path(s) / "nvidia"
    if nvidia.is_dir():
        dirs = sorted(set(str(p.parent) for p in nvidia.rglob("*.so*") if p.is_file()))
        if dirs:
            print(":".join(dirs))
            break
`)
	out, err := cmd.Output()
	if err != nil {
		return environ
	}
	extra := strings.TrimSpace(string(out))
	if extra == "" {
		return environ
	}

	for i, env := range environ {
		if strings.HasPrefix(env, "LD_LIBRARY_PATH=") {
			environ[i] = "LD_LIBRARY_PATH=" + extra + ":" + env[len("LD_LIBRARY_PATH="):]
			return environ
		}
	}
	return append(environ, "LD_LIBRARY_PATH="+extra)
}

func ensureNvidiaCUDALibs(pythonPath string) {
	packages := []string{
		"nvidia-cublas-cu12",
		"nvidia-cudnn-cu12",
		"nvidia-cuda-runtime-cu12",
		"nvidia-cufft-cu12",
		"nvidia-curand-cu12",
	}
	fmt.Fprintf(os.Stderr, "info: ensuring NVIDIA CUDA runtime libraries in %s\n", pythonPath)
	if err := installRuntimePackagesWithUV(pythonPath, packages...); err != nil {
		fmt.Fprintf(
			os.Stderr,
			"warning: failed to install NVIDIA CUDA runtime libraries for %s: %v\n",
			pythonPath,
			err,
		)
		return
	}
	fmt.Fprintln(os.Stderr, "info: NVIDIA CUDA runtime libraries are installed")
}

func ensurePythonDeps(pythonPath string) error {
	cmd := exec.Command(pythonPath, "-c", "import numpy, onnxruntime, tokenizers")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("python runtime %s is missing embedding dependencies: %s", pythonPath, strings.TrimSpace(string(output)))
	}
	return nil
}

func ensureEmbeddedRuntime(basePython string, device string) (string, error) {
	cacheDir, err := userCacheDir()
	if err != nil {
		return "", err
	}
	runtimeDir := filepath.Join(cacheDir, "runtime")
	runtimePython := filepath.Join(runtimeDir, "bin", "python")

	if fileExists(runtimePython) {
		if err := ensurePythonDeps(runtimePython); err == nil {
			return runtimePython, nil
		}
		fmt.Fprintf(os.Stderr, "info: existing embedding runtime at %s is missing dependencies; reinstalling\n", runtimePython)
	}

	if !dirExists(runtimeDir) {
		fmt.Fprintf(os.Stderr, "info: creating isolated embedding runtime at %s\n", runtimeDir)
		cmd := exec.Command(basePython, "-m", "venv", runtimeDir)
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("create embedding runtime: %w\n%s", err, strings.TrimSpace(string(output)))
		}
	}

	packages := []string{"onnxruntime"}
	if isCUDADevice(device) {
		packages = []string{"onnxruntime-gpu", "onnxruntime"}
	}
	var installErr error
	for i, onnxPackage := range packages {
		fmt.Fprintf(os.Stderr, "info: installing embedding dependencies with uv (%s) in %s\n", onnxPackage, runtimePython)
		if err := installRuntimePackagesWithUV(runtimePython, "numpy", onnxPackage, "tokenizers"); err != nil {
			if i+1 < len(packages) {
				fmt.Fprintf(os.Stderr, "warning: failed to install %s; falling back to %s\n", onnxPackage, packages[i+1])
			}
			installErr = fmt.Errorf("install embedding runtime dependencies (%s): %w", onnxPackage, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "info: embedding dependencies installed with %s\n", onnxPackage)
		installErr = nil
		break
	}
	if installErr != nil {
		return "", installErr
	}
	if isCUDADevice(device) {
		ensureNvidiaCUDALibs(runtimePython)
	}
	if err := ensurePythonDeps(runtimePython); err != nil {
		return "", err
	}
	return runtimePython, nil
}

func installRuntimePackagesWithUV(runtimePython string, packages ...string) error {
	if err := ensureRuntimeUV(runtimePython); err != nil {
		return err
	}
	args := append([]string{"-m", "uv", "pip", "install", "--python", runtimePython}, packages...)
	cmd := exec.Command(runtimePython, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install packages with uv: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func ensureRuntimeUV(runtimePython string) error {
	checkCmd := exec.Command(runtimePython, "-m", "uv", "--version")
	if err := checkCmd.Run(); err == nil {
		return nil
	}

	fmt.Fprintf(os.Stderr, "info: uv not found in embedding runtime; installing uv in %s\n", runtimePython)
	installCmd := exec.Command(runtimePython, "-m", "pip", "install", "--disable-pip-version-check", "uv")
	output, err := installCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install uv in embedding runtime: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
