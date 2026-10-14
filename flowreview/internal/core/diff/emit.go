package diff

import (
	"strings"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// Emit re-serializes files to the exact bytes Parse would read back into
// them: each file's Header lines verbatim (that is where "diff --git",
// "index", "---"/"+++" and every other extended-header line already live,
// quoting and all), then each hunk's "@@ ... @@" line rebuilt from
// OldStart/OldLines/NewStart/NewLines/Section, then its lines with their
// marker restored and a "\ No newline at end of file" line after any line
// flagged NoNewline.
func Emit(files []model.ChangedFile) []byte {
	var b strings.Builder
	for _, f := range files {
		for _, h := range f.Header {
			b.WriteString(h)
			b.WriteByte('\n')
		}
		for _, h := range f.Hunks {
			b.WriteString(formatHunkHeader(h))
			b.WriteByte('\n')
			for _, l := range h.Lines {
				switch l.Kind {
				case model.LineAdd:
					b.WriteByte('+')
				case model.LineDel:
					b.WriteByte('-')
				default:
					b.WriteByte(' ')
				}
				b.WriteString(l.Text)
				b.WriteByte('\n')
				if l.NoNewline {
					b.WriteString(noNewlineMarker)
					b.WriteByte('\n')
				}
			}
		}
	}
	return []byte(b.String())
}
