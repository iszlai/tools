package diff

import "github.com/iszlai/tools/flowreview/internal/model"

// LineAt finds the hunk and line index of the line at the given file line
// number on the given side (old or new). It is a linear scan: later slices
// that need this often can build an index once, but a parsed file's hunks
// are small enough that this is fine to call directly.
func LineAt(file model.ChangedFile, side model.Side, line int) (hunkIdx, lineIdx int, ok bool) {
	for hi, h := range file.Hunks {
		for li, l := range h.Lines {
			n := l.OldNo
			if side == model.SideNew {
				n = l.NewNo
			}
			if n != 0 && n == line {
				return hi, li, true
			}
		}
	}
	return 0, 0, false
}

// PositionFor returns the GitHub diff position (see the package doc) of the
// line at the given file line number on the given side.
func PositionFor(file model.ChangedFile, side model.Side, line int) (int, bool) {
	hi, li, ok := LineAt(file, side, line)
	if !ok {
		return 0, false
	}
	return file.Hunks[hi].Lines[li].Position, true
}
