// Package diff parses git-style unified diffs — "diff --git" patches as
// `git diff`/`git show` print them, and plain `---`/`+++` patches that lack
// the git extended header — into model.ChangedFile/model.DiffHunk/
// model.DiffLine, and re-emits them byte for byte.
//
// GitHub comment position. Each model.DiffLine carries the position GitHub's
// pull request review API expects: 1 is the first line right after a file's
// first "@@" hunk header; the count then keeps climbing through the rest of
// that file's diff text exactly as GitHub describes it ("the position in the
// diff continues to increase through lines of whitespace and additional
// hunks until the beginning of a new file") — meaning every line of the
// file's hunk body, every later "@@" header line, and every
// "\ No newline at end of file" marker each consume one more position, and
// the count resets to 1 at the next file's first hunk. See parseHunk.
package diff

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/lang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

var (
	hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)
	indexLineRe  = regexp.MustCompile(`^index ([0-9a-fA-F]+)\.\.([0-9a-fA-F]+)(?: (\d+))?$`)
)

// Parse reads one or more file diffs out of b. It is strict: any line it
// cannot place into a file's header or hunk body is an error, so that a
// round trip through Emit never silently drops bytes.
func Parse(b []byte) ([]model.ChangedFile, error) {
	lines := splitLines(b)
	var files []model.ChangedFile
	i := 0
	for i < len(lines) {
		if !strings.HasPrefix(lines[i], "diff --git ") && !strings.HasPrefix(lines[i], "--- ") {
			return nil, fmt.Errorf("diff: unexpected line %d: %q", i+1, lines[i])
		}
		f, next, err := parseFile(lines, i)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
		i = next
	}
	return files, nil
}

// splitLines splits b on "\n" into lines without their terminators. Parse
// and Emit assume the input ends with a trailing newline, as every
// git-generated diff and ordinary patch file does; Emit always terminates
// the last line it writes, so that assumption is what makes the round trip
// exact.
func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	lines := strings.Split(string(b), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// parseFile parses one file's diff starting at lines[i] and returns it plus
// the index of the first line belonging to whatever follows it.
func parseFile(lines []string, i int) (model.ChangedFile, int, error) {
	var f model.ChangedFile
	var header []string
	var oldTokRaw, newTokRaw string
	haveGitLine := false

	if strings.HasPrefix(lines[i], "diff --git ") {
		oldTok, newTok, ok := parseDiffGitLine(lines[i])
		if !ok {
			return model.ChangedFile{}, 0, fmt.Errorf("diff: cannot parse line %d: %q", i+1, lines[i])
		}
		haveGitLine = true
		oldTokRaw, newTokRaw = oldTok, newTok
		header = append(header, lines[i])
		i++
	}

	var renameFrom, renameTo, copyFrom, copyTo, indexMode string
	isRename, isCopy, isAdded, isDeleted := false, false, false, false

extHeader:
	for i < len(lines) {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, "old mode "):
			f.OldMode = strings.TrimPrefix(l, "old mode ")
		case strings.HasPrefix(l, "new mode "):
			f.NewMode = strings.TrimPrefix(l, "new mode ")
		case strings.HasPrefix(l, "deleted file mode "):
			f.OldMode = strings.TrimPrefix(l, "deleted file mode ")
			isDeleted = true
		case strings.HasPrefix(l, "new file mode "):
			f.NewMode = strings.TrimPrefix(l, "new file mode ")
			isAdded = true
		case strings.HasPrefix(l, "similarity index "):
			n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(l, "similarity index "), "%"))
			f.Similarity = n
		case strings.HasPrefix(l, "dissimilarity index "):
			// No structured field; kept in Header for round-trip only.
		case strings.HasPrefix(l, "rename from "):
			renameFrom = unquoteGitPath(strings.TrimPrefix(l, "rename from "))
			isRename = true
		case strings.HasPrefix(l, "rename to "):
			renameTo = unquoteGitPath(strings.TrimPrefix(l, "rename to "))
		case strings.HasPrefix(l, "copy from "):
			copyFrom = unquoteGitPath(strings.TrimPrefix(l, "copy from "))
			isCopy = true
		case strings.HasPrefix(l, "copy to "):
			copyTo = unquoteGitPath(strings.TrimPrefix(l, "copy to "))
		case strings.HasPrefix(l, "index "):
			if m := indexLineRe.FindStringSubmatch(l); m != nil {
				indexMode = m[3]
			}
		default:
			break extHeader
		}
		header = append(header, l)
		i++
	}
	// A bare "index a..b mode" line (no separate old/new mode lines) means
	// the mode did not change; it is the only place that mode appears.
	if f.OldMode == "" && f.NewMode == "" && indexMode != "" {
		f.OldMode, f.NewMode = indexMode, indexMode
	}

	var oldPath, newPath string
	haveOldNew := false
	pos := 0

	switch {
	case i < len(lines) && strings.HasPrefix(lines[i], "--- "):
		oldLine := lines[i]
		i++
		if i >= len(lines) || !strings.HasPrefix(lines[i], "+++ ") {
			return model.ChangedFile{}, 0, fmt.Errorf("diff: %q is not followed by a +++ line", oldLine)
		}
		newLine := lines[i]
		i++
		header = append(header, oldLine, newLine)
		haveOldNew = true

		op := parseFileHeaderPath(oldLine, "--- ")
		if op == "/dev/null" {
			isAdded = true
		} else {
			oldPath = stripABPrefix(op)
		}
		np := parseFileHeaderPath(newLine, "+++ ")
		if np == "/dev/null" {
			isDeleted = true
		} else {
			newPath = stripABPrefix(np)
		}

		for i < len(lines) && strings.HasPrefix(lines[i], "@@ ") {
			h, next, err := parseHunk(lines, i, &pos, len(f.Hunks) == 0)
			if err != nil {
				return model.ChangedFile{}, 0, err
			}
			f.Hunks = append(f.Hunks, h)
			i = next
		}

	case i < len(lines) && strings.HasPrefix(lines[i], "Binary files "):
		f.Binary = true
		header = append(header, lines[i])
		i++

	case i < len(lines) && lines[i] == `GIT binary patch`:
		f.Binary = true
		for i < len(lines) && !strings.HasPrefix(lines[i], "diff --git ") {
			header = append(header, lines[i])
			i++
		}
	}

	fallbackOld, fallbackNew := "", ""
	if haveGitLine {
		fallbackOld, fallbackNew = stripABPrefix(oldTokRaw), stripABPrefix(newTokRaw)
	}

	switch {
	case isRename:
		f.Status = model.FileRenamed
		f.OldPath, f.NewPath = renameFrom, renameTo
	case isCopy:
		f.Status = model.FileCopied
		f.OldPath, f.NewPath = copyFrom, copyTo
	case isAdded:
		f.Status = model.FileAdded
		if haveOldNew {
			f.NewPath = newPath
		} else {
			f.NewPath = fallbackNew
		}
	case isDeleted:
		f.Status = model.FileDeleted
		if haveOldNew {
			f.OldPath = oldPath
		} else {
			f.OldPath = fallbackOld
		}
	default:
		f.Status = model.FileModified
		if haveOldNew {
			f.OldPath, f.NewPath = oldPath, newPath
		} else {
			f.OldPath, f.NewPath = fallbackOld, fallbackNew
		}
	}

	f.Language = lang.Detect(f.Path())
	f.Header = header
	for hi := range f.Hunks {
		for _, l := range f.Hunks[hi].Lines {
			switch l.Kind {
			case model.LineAdd:
				f.Additions++
			case model.LineDel:
				f.Deletions++
			}
		}
		f.Hunks[hi].ID = model.HunkID(f.Path(), f.Hunks[hi].Lines)
	}
	f.ID = model.FileID(f.OldPath, f.NewPath)
	return f, i, nil
}
