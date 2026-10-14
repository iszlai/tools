package gherkin

import (
	"regexp"
	"strings"
)

// exampleTable is a Scenario Outline's Examples table: a header row naming
// each "<column>" and the data rows substituted into it.
type exampleTable struct {
	header []string
	rows   [][]string
}

// parseExamplesTable looks for an "Examples:" line within [startLine,
// endLine] of lines (1-based, inclusive) and parses the pipe-delimited table
// that follows it. Returns nil when there is no Examples section.
func parseExamplesTable(lines []string, startLine, endLine int) *exampleTable {
	for i := startLine - 1; i < endLine && i < len(lines); i++ {
		if i < 0 {
			continue
		}
		if strings.TrimSpace(lines[i]) != "Examples:" {
			continue
		}
		var header []string
		var rows [][]string
		for j := i + 1; j < endLine && j < len(lines); j++ {
			row := splitTableRow(lines[j])
			if row == nil {
				break
			}
			if header == nil {
				header = row
			} else {
				rows = append(rows, row)
			}
		}
		if header == nil {
			return nil
		}
		return &exampleTable{header: header, rows: rows}
	}
	return nil
}

func splitTableRow(line string) []string {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "|") || !strings.HasSuffix(t, "|") || len(t) < 2 {
		return nil
	}
	parts := strings.Split(t[1:len(t)-1], "|")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

var placeholderRe = regexp.MustCompile(`<([^>]+)>`)

// substituteExample replaces every "<name>" in text with the value from
// ex's first data row at the "name" column, succeeding only when every
// placeholder has a matching column — the "if easy" case the slice asks
// for. Multiple rows would need multiple edges per step for no real gain:
// Scenario Outline rows normally all exercise the same step definition.
func substituteExample(text string, ex *exampleTable) (string, bool) {
	if ex == nil || len(ex.rows) == 0 {
		return "", false
	}
	row := ex.rows[0]
	col := make(map[string]int, len(ex.header))
	for i, h := range ex.header {
		col[h] = i
	}
	ok := true
	out := placeholderRe.ReplaceAllStringFunc(text, func(m string) string {
		name := m[1 : len(m)-1]
		i, found := col[name]
		if !found || i >= len(row) {
			ok = false
			return m
		}
		return row[i]
	})
	if !ok {
		return "", false
	}
	return out, true
}

// wildcardSubstitute replaces every "<name>" with a generic value. It works
// because a placeholder usually sits inside the step's own literal
// delimiters already (e.g. a user named "<name>" -> a user named "x"),
// which still lines up with a stepdef pattern expecting a quoted value.
func wildcardSubstitute(text string) string {
	return placeholderRe.ReplaceAllString(text, "x")
}
