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

type Provider interface {
	Name() string
	Embed(context.Context, []Document) ([]Vector, error)
}

type NoopProvider struct{}

func (NoopProvider) Name() string { return "noop" }

func (NoopProvider) Embed(_ context.Context, _ []Document) ([]Vector, error) {
	return nil, nil
}
