package diff

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// noNewlineMarker is the line git (and GNU diff) writes immediately after a
// content line whose original file has no trailing newline.
const noNewlineMarker = `\ No newline at end of file`

// parseHunk reads one "@@ ... @@" header and its body starting at lines[i].
// pos is the file's running GitHub position counter (see the package doc);
// first says whether this is the file's first hunk, since only later hunks'
// own header lines consume a position.
func parseHunk(lines []string, i int, pos *int, first bool) (model.DiffHunk, int, error) {
	oldStart, oldLines, newStart, newLines, section, ok := parseHunkHeader(lines[i])
	if !ok {
		return model.DiffHunk{}, 0, fmt.Errorf("diff: bad hunk header %q", lines[i])
	}
	if !first {
		*pos++
	}
	i++

	h := model.DiffHunk{
		OldStart: oldStart,
		OldLines: oldLines,
		NewStart: newStart,
		NewLines: newLines,
		Section:  section,
	}

	oldNo, newNo := oldStart, newStart
	remOld, remNew := oldLines, newLines
	for remOld > 0 || remNew > 0 {
		if i >= len(lines) {
			return model.DiffHunk{}, 0, fmt.Errorf("diff: hunk truncated before its declared line count")
		}
		l := lines[i]
		marker := byte(' ')
		text := ""
		if len(l) > 0 {
			marker, text = l[0], l[1:]
		}

		var dl model.DiffLine
		switch marker {
		case ' ':
			dl.Kind = model.LineContext
			dl.OldNo, dl.NewNo = oldNo, newNo
			oldNo++
			newNo++
			remOld--
			remNew--
		case '-':
			dl.Kind = model.LineDel
			dl.OldNo = oldNo
			oldNo++
			remOld--
		case '+':
			dl.Kind = model.LineAdd
			dl.NewNo = newNo
			newNo++
			remNew--
		default:
			return model.DiffHunk{}, 0, fmt.Errorf("diff: unexpected hunk line %q", l)
		}
		dl.Text = text
		i++
		*pos++
		dl.Position = *pos

		if i < len(lines) && lines[i] == noNewlineMarker {
			dl.NoNewline = true
			i++
			*pos++
		}
		h.Lines = append(h.Lines, dl)
	}
	return h, i, nil
}

// parseHunkHeader reads "@@ -oldStart[,oldLines] +newStart[,newLines] @@
// [section]"; a missing ",count" means count is 1, the unified-diff
// convention for a single-line range.
func parseHunkHeader(line string) (oldStart, oldLines, newStart, newLines int, section string, ok bool) {
	m := hunkHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, 0, 0, "", false
	}
	oldStart, _ = strconv.Atoi(m[1])
	oldLines = 1
	if m[2] != "" {
		oldLines, _ = strconv.Atoi(m[2])
	}
	newStart, _ = strconv.Atoi(m[3])
	newLines = 1
	if m[4] != "" {
		newLines, _ = strconv.Atoi(m[4])
	}
	section = strings.TrimPrefix(m[5], " ")
	return oldStart, oldLines, newStart, newLines, section, true
}

// formatHunkHeader is the exact inverse of parseHunkHeader.
func formatHunkHeader(h model.DiffHunk) string {
	old := formatRange(h.OldStart, h.OldLines)
	nw := formatRange(h.NewStart, h.NewLines)
	if h.Section != "" {
		return fmt.Sprintf("@@ -%s +%s @@ %s", old, nw, h.Section)
	}
	return fmt.Sprintf("@@ -%s +%s @@", old, nw)
}

// formatRange omits ",count" exactly when parseHunkHeader would have
// defaulted it: when count is 1.
func formatRange(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}
