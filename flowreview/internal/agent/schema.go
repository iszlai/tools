package agent

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// ModelResponse is the JSON object an AgentProvider's model must return for
// one review turn: a run is zero or more of these.
type ModelResponse struct {
	Findings []ModelFinding `json:"findings"`
}

// ModelFinding is the shape a model fills in for one finding. The Runner
// adds ID, Verification, Anchor and Provider itself (see model.AgentFinding)
// — the model is never asked for them and must not invent them.
type ModelFinding struct {
	Severity         string          `json:"severity" jsonschema:"enum=info|low|medium|high"`
	Title            string          `json:"title"`
	Explanation      string          `json:"explanation"`
	Evidence         []ModelEvidence `json:"evidence"`
	Confidence       float64         `json:"confidence" jsonschema:"minimum=0,maximum=1"`
	SuggestedComment string          `json:"suggested_comment,omitempty"`
}

// ModelEvidence is one quoted source line backing a finding, as the model
// cites it. Verify checks it against the real file before display.
type ModelEvidence struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Excerpt string `json:"excerpt"`
}

//go:generate go test -run TestSchemaMatchesStruct -update

// FindingSchemaJSON is the committed JSON Schema for ModelResponse, embedded
// so callers (the sidecar's response_format, a future CLI provider) can ship
// it without a file path. TestSchemaMatchesStruct guards it against drift.
//
//go:embed finding.schema.json
var FindingSchemaJSON []byte

// jsonSchema is the tiny subset of JSON Schema (draft-07) this package
// emits — enough to constrain structured model output, not a general-purpose
// schema library.
type jsonSchema struct {
	Schema     string                 `json:"$schema,omitempty"`
	Type       string                 `json:"type,omitempty"`
	Properties map[string]*jsonSchema `json:"properties,omitempty"`
	Items      *jsonSchema            `json:"items,omitempty"`
	Required   []string               `json:"required,omitempty"`
	Enum       []string               `json:"enum,omitempty"`
	Minimum    *float64               `json:"minimum,omitempty"`
	Maximum    *float64               `json:"maximum,omitempty"`
}

// GenerateSchema builds the JSON Schema for ModelResponse by reflecting over
// its fields: the json tag names each property, a missing "omitempty" makes
// it required, and an optional `jsonschema:"..."` tag adds an enum or a
// numeric range. Reflection-based generation means the schema can never
// drift from the struct it describes — regenerate the committed file with
// `go generate ./internal/agent` (or `go test ./internal/agent -run
// TestSchemaMatchesStruct -update`).
func GenerateSchema() ([]byte, error) {
	s := schemaForType(reflect.TypeOf(ModelResponse{}))
	s.Schema = "http://json-schema.org/draft-07/schema#"
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func schemaForType(t reflect.Type) *jsonSchema {
	switch t.Kind() {
	case reflect.Struct:
		s := &jsonSchema{Type: "object", Properties: map[string]*jsonSchema{}}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, omitempty := jsonFieldName(f)
			if name == "" {
				continue // untagged or explicitly "-"
			}
			fs := schemaForType(f.Type)
			applySchemaTag(fs, f.Tag.Get("jsonschema"))
			s.Properties[name] = fs
			if !omitempty {
				s.Required = append(s.Required, name)
			}
		}
		sort.Strings(s.Required)
		return s
	case reflect.Slice, reflect.Array:
		return &jsonSchema{Type: "array", Items: schemaForType(t.Elem())}
	case reflect.String:
		return &jsonSchema{Type: "string"}
	case reflect.Float32, reflect.Float64:
		return &jsonSchema{Type: "number"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &jsonSchema{Type: "integer"}
	case reflect.Bool:
		return &jsonSchema{Type: "boolean"}
	default:
		panic(fmt.Sprintf("agent: GenerateSchema: unsupported field kind %s", t.Kind()))
	}
}

// jsonFieldName reads the name and the "omitempty" option off a field's json
// tag, falling back to the Go field name when the tag has none.
func jsonFieldName(f reflect.StructField) (name string, omitempty bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty
}

// applySchemaTag parses a `jsonschema:"enum=a|b|c,minimum=0,maximum=1"` tag.
func applySchemaTag(s *jsonSchema, tag string) {
	if tag == "" {
		return
	}
	for _, kv := range strings.Split(tag, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch k {
		case "enum":
			s.Enum = strings.Split(v, "|")
		case "minimum":
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				s.Minimum = &f
			}
		case "maximum":
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				s.Maximum = &f
			}
		}
	}
}
