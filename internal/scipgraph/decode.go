package scipgraph

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	scip "github.com/1001encore/wave/internal/gen/scippb"

	"google.golang.org/protobuf/proto"
)

type Range struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
}

type RoleFlags struct {
	Definition bool
	Import     bool
	Read       bool
	Write      bool
}

func LoadIndex(path string) (*scip.Index, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scip artifact: %w", err)
	}

	var index scip.Index
	if err := proto.Unmarshal(payload, &index); err != nil {
		return nil, fmt.Errorf("decode scip artifact: %w", err)
	}
	return &index, nil
}

func ParseRange(values []int32) (Range, error) {
	switch len(values) {
	case 3:
		return Range{
			StartLine: int(values[0]),
			StartCol:  int(values[1]),
			EndLine:   int(values[0]),
			EndCol:    int(values[2]),
		}, nil
	case 4:
		return Range{
			StartLine: int(values[0]),
			StartCol:  int(values[1]),
			EndLine:   int(values[2]),
			EndCol:    int(values[3]),
		}, nil
	default:
		return Range{}, fmt.Errorf("invalid SCIP range length %d", len(values))
	}
}

func DecodeRoles(bits int32) RoleFlags {
	return RoleFlags{
		Definition: bits&int32(scip.SymbolRole_Definition) != 0,
		Import:     bits&int32(scip.SymbolRole_Import) != 0,
		Read:       bits&int32(scip.SymbolRole_ReadAccess) != 0,
		Write:      bits&int32(scip.SymbolRole_WriteAccess) != 0,
	}
}

func DocumentationSummary(lines []string) string {
	joined := strings.TrimSpace(strings.Join(lines, "\n"))
	if len(joined) > 400 {
		return joined[:400]
	}
	return joined
}

var camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func NormalizeSymbolKind(kind scip.SymbolInformation_Kind) string {
	if kind == scip.SymbolInformation_UnspecifiedKind {
		return ""
	}
	return normalizeEnumLabel(kind.String(), "Kind")
}

func NormalizeSyntaxKind(kind scip.SyntaxKind) string {
	if kind == scip.SyntaxKind_UnspecifiedSyntaxKind {
		return ""
	}
	return normalizeEnumLabel(kind.String(), "SyntaxKind")
}

func normalizeEnumLabel(raw string, suffix string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "SymbolInformation_")
	raw = strings.TrimPrefix(raw, "SyntaxKind_")
	raw = strings.TrimSuffix(raw, suffix)
	raw = camelBoundary.ReplaceAllString(raw, "${1}_${2}")
	raw = strings.ToLower(raw)
	raw = strings.Trim(raw, "_")
	return raw
}
