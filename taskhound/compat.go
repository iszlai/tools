package main

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the board format this build understands. A file claiming a
// higher one was written by a newer th, which may hold fields this build cannot
// round-trip safely, so writing it is refused rather than risked.
const SchemaVersion = 1

// Keeping a board readable by more than one version of th is the whole point of
// committing it: agents and people pick up whatever binary they have. Marshaling
// through a struct silently discards every key the struct has no field for, so
// an older binary writing a newer board used to delete the newer fields with no
// error at all.
//
// So saving re-attaches, from the file as it was read, every key this build does
// not know about. "Does not know about" is decided from the struct tags, not
// from what happens to be present: a field this build does know and chose to
// omit — a label that was removed, a description that was cleared — must stay
// omitted, or deleting anything would be impossible.

// yamlKeys returns the yaml key names a struct can represent.
func yamlKeys(v any) map[string]bool {
	keys := map[string]bool{}
	t := reflect.TypeOf(v)
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		if name := strings.Split(tag, ",")[0]; name != "" {
			keys[name] = true
		}
	}
	return keys
}

var (
	boardKeys = yamlKeys(Board{})
	issueKeys = yamlKeys(Issue{})
)

// mapValue reads a scalar out of a mapping node, or "" if it is not there.
func mapValue(node *yaml.Node, key string) string {
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1].Value
		}
	}
	return ""
}

func mapEntry(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func hasKey(node *yaml.Node, key string) bool {
	return mapEntry(node, key) != nil
}

// copyUnknownKeys appends every key of src that this build has no field for and
// that dst does not already carry.
func copyUnknownKeys(dst, src *yaml.Node, known map[string]bool) {
	if dst == nil || src == nil || dst.Kind != yaml.MappingNode || src.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(src.Content); i += 2 {
		key := src.Content[i]
		if known[key.Value] || hasKey(dst, key.Value) {
			continue
		}
		dst.Content = append(dst.Content, key, src.Content[i+1])
	}
}

// documentRoot unwraps a parsed document down to its top-level mapping.
func documentRoot(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

// preserveUnknown re-attaches the unknown keys of raw onto the freshly encoded
// board, at the two levels a schema actually grows at: the board itself and each
// issue, matched by id. An issue that no longer exists is simply not matched —
// archiving is a deletion and should stay one.
func preserveUnknown(encoded, raw *yaml.Node) {
	dst, src := documentRoot(encoded), documentRoot(raw)
	if dst == nil || src == nil {
		return
	}
	copyUnknownKeys(dst, src, boardKeys)

	dstIssues, srcIssues := mapEntry(dst, "issues"), mapEntry(src, "issues")
	if dstIssues == nil || srcIssues == nil ||
		dstIssues.Kind != yaml.SequenceNode || srcIssues.Kind != yaml.SequenceNode {
		return
	}
	byID := map[string]*yaml.Node{}
	for _, item := range srcIssues.Content {
		if id := mapValue(item, "id"); id != "" {
			byID[id] = item
		}
	}
	for _, item := range dstIssues.Content {
		if old, ok := byID[mapValue(item, "id")]; ok {
			copyUnknownKeys(item, old, issueKeys)
		}
	}
}

// marshalBoard renders a board, carrying over whatever the file held that this
// build does not model.
func marshalBoard(b *Board) ([]byte, error) {
	if b.Version > SchemaVersion {
		return nil, fmt.Errorf(
			"this board is schema version %d and this th only understands %d — "+
				"upgrade th rather than let it write a file it cannot represent",
			b.Version, SchemaVersion)
	}
	var encoded yaml.Node
	if err := encoded.Encode(b); err != nil {
		return nil, err
	}
	if b.raw != nil {
		preserveUnknown(&encoded, b.raw)
	}
	return yaml.Marshal(&encoded)
}
