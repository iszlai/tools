package model

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// HunkBody is the normalized text of a hunk: one line per diff line, prefixed
// with its marker, without the @@ header and so without line numbers. A
// trailing "\r" is dropped so CRLF checkouts hash like LF ones; everything
// else, whitespace included, is content.
func HunkBody(lines []DiffLine) string {
	var b strings.Builder
	for _, l := range lines {
		switch l.Kind {
		case LineAdd:
			b.WriteByte('+')
		case LineDel:
			b.WriteByte('-')
		default:
			b.WriteByte(' ')
		}
		b.WriteString(strings.TrimSuffix(l.Text, "\r"))
		b.WriteByte('\n')
		if l.NoNewline {
			b.WriteString("\\ No newline at end of file\n")
		}
	}
	return b.String()
}

// HunkID identifies a hunk by its path and content: sha1(path + "\n" + body).
// A hunk that only moved keeps its id; any content change, or a rename, gives a
// new one.
func HunkID(path string, lines []DiffLine) string {
	h := sha1.New()
	h.Write([]byte(path))
	h.Write([]byte{'\n'})
	h.Write([]byte(HunkBody(lines)))
	return hex.EncodeToString(h.Sum(nil))
}

// FileID identifies a changed file within a diff by its old and new path.
func FileID(oldPath, newPath string) string {
	sum := sha1.Sum([]byte(oldPath + "\x00" + newPath))
	return hex.EncodeToString(sum[:])[:12]
}

// SummaryKey identifies a cached summary. Each part is length-prefixed so no
// two different inputs concatenate to the same bytes.
func SummaryKey(modelID, promptVersion, hunkBody, enclosingFunc, contextSlice string) string {
	h := sha256.New()
	for _, part := range []string{modelID, promptVersion, hunkBody, enclosingFunc, contextSlice} {
		fmt.Fprintf(h, "%d:", len(part))
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
