package summary

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchemaIsValidJSON(t *testing.T) {
	var v any
	if err := json.Unmarshal(Schema, &v); err != nil {
		t.Fatalf("Schema is not valid JSON: %v", err)
	}
}

func TestMessagesIncludeHunkAndFunction(t *testing.T) {
	msgs := Messages(Input{
		Path:              "internal/core/diff/hunk.go",
		Language:          "go",
		Hunk:              "+\treturn x\n-\treturn y\n",
		EnclosingFunction: "func parseHunk() {}",
	})
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[1].Role != "user" {
		t.Fatalf("want system then user, got %q then %q", msgs[0].Role, msgs[1].Role)
	}
	user := msgs[1].Content
	for _, want := range []string{"internal/core/diff/hunk.go", "go", "return x", "return y", "func parseHunk"} {
		if !strings.Contains(user, want) {
			t.Errorf("user message missing %q:\n%s", want, user)
		}
	}
}

func TestMessagesWithoutEnclosingFunction(t *testing.T) {
	msgs := Messages(Input{Path: "x.yaml", Hunk: "+key: value\n"})
	if strings.Contains(msgs[1].Content, "Enclosing function") {
		t.Error("should not mention an enclosing function section when none was found")
	}
	if !strings.Contains(msgs[1].Content, "unknown") {
		t.Error("want \"unknown\" language when Language is empty")
	}
}
