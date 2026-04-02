package embed

import "context"

type Document struct {
	OwnerType string
	OwnerKey  string
	Text      string
}

type Vector struct {
	OwnerType string
	OwnerKey  string
	Values    []float32
}

type Stats struct {
	Provider       string       `json:"provider"`
	RequestedBatch int          `json:"requested_batch"`
	SelectedBatch  int          `json:"selected_batch"`
	BatchCount     int          `json:"batch_count"`
	OOMRetries     int          `json:"oom_retries"`
	Documents      int          `json:"documents"`
	Dimensions     int          `json:"dimensions"`
	RequestMS      float64      `json:"request_ms"`
	PreloadMS      float64      `json:"preload_ms"`
	SessionMS      float64      `json:"session_ms"`
	TokenizeMS     float64      `json:"tokenize_ms"`
	InferMS        float64      `json:"infer_ms"`
	NormalizeMS    float64      `json:"normalize_ms"`
	SerializeMS    float64      `json:"serialize_ms"`
	DecodeMS       float64      `json:"decode_ms"`
	TotalMS        float64      `json:"total_ms"`
	BatchStats     []BatchStats `json:"batch_stats,omitempty"`
}

type BatchStats struct {
	Index        int     `json:"index"`
	Size         int     `json:"size"`
	Processed    int     `json:"processed"`
	TokenizeMS   float64 `json:"tokenize_ms"`
	InferMS      float64 `json:"infer_ms"`
	NormalizeMS  float64 `json:"normalize_ms"`
	RetryCount   int     `json:"retry_count"`
	SettledBatch int     `json:"settled_batch"`
}

type Provider interface {
	Name() string
	Embed(context.Context, []Document) ([]Vector, error)
}

type DiagnosticsProvider interface {
	Provider
	LastStats() Stats
}

type NoopProvider struct{}

func (NoopProvider) Name() string { return "noop" }

func (NoopProvider) Embed(_ context.Context, _ []Document) ([]Vector, error) {
	return nil, nil
}
