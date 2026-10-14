package agent

import (
	"bytes"
	"flag"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "regenerate finding.schema.json from the ModelResponse struct")

const schemaPath = "finding.schema.json"

// TestSchemaMatchesStruct guards finding.schema.json against drift: the
// committed file must equal what GenerateSchema() produces from the struct
// right now. Run with -update to regenerate it after a deliberate change.
func TestSchemaMatchesStruct(t *testing.T) {
	got, err := GenerateSchema()
	if err != nil {
		t.Fatalf("GenerateSchema() error: %v", err)
	}

	if *update {
		if err := os.WriteFile(schemaPath, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", schemaPath, err)
		}
		return
	}

	want, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read %s: %v (run `go test ./internal/agent -run TestSchemaMatchesStruct -update`)", schemaPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is out of date with the ModelResponse struct; run `go test ./internal/agent -run TestSchemaMatchesStruct -update`", schemaPath)
	}
}
