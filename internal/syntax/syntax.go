package syntax

import "context"

type Chunk struct {
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
	PrimarySymbol string
}

type Extractor interface {
	Extract(context.Context, string, []byte) ([]Chunk, error)
}
