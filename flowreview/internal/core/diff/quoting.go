package diff

import (
	"strconv"
	"strings"
)

// unquoteGitPath decodes a path the way git writes it on "diff --git",
// "rename from/to", "copy from/to" and "---"/"+++" lines: left alone when it
// needs no quoting, or wrapped in double quotes and C-escaped (\\, \", \n,
// \t, \r, \a, \b, \f, \v, and \NNN octal for any other byte that needs
// escaping — by default that includes every non-ASCII byte) when it does. A
// plain space never triggers quoting on its own.
func unquoteGitPath(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	inner := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c != '\\' || i+1 >= len(inner) {
			b.WriteByte(c)
			continue
		}
		i++
		switch inner[i] {
		case '\\':
			b.WriteByte('\\')
		case '"':
			b.WriteByte('"')
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'v':
			b.WriteByte('\v')
		default:
			if n, ok := readOctal(inner, i); ok {
				b.WriteByte(byte(n))
				i += 2
				continue
			}
			b.WriteByte(inner[i])
		}
	}
	return b.String()
}

// readOctal reads the 3-digit octal escape starting at inner[i] (the first
// digit), as used in a \NNN byte escape.
func readOctal(inner string, i int) (uint64, bool) {
	if inner[i] < '0' || inner[i] > '7' || i+2 >= len(inner) {
		return 0, false
	}
	n, err := strconv.ParseUint(inner[i:i+3], 8, 8)
	if err != nil {
		return 0, false
	}
	return n, true
}

// scanQuoted reads one double-quoted, backslash-escaped token from the start
// of s and returns it already unquoted, plus the remainder of s after the
// closing quote.
func scanQuoted(s string) (tok, rest string, ok bool) {
	if len(s) == 0 || s[0] != '"' {
		return "", s, false
	}
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++ // Skip exactly the escaped byte; an octal escape's extra
			// digits are inert ('0'-'7'), so they can never be mistaken
			// for the closing quote this loop is looking for.
		case '"':
			return unquoteGitPath(s[:i+1]), s[i+1:], true
		}
	}
	return "", s, false
}

// parseDiffGitLine extracts the two path tokens from a "diff --git a/X b/Y"
// line, already unquoted. Git quotes the whole "a/X"/"b/Y" token when the
// path needs it; a plain space is not reason enough to quote, so when
// neither token is quoted the only way to split them is to know old and new
// are byte-identical, which is exactly the case git leaves unquoted here
// without a rename or ---/+++ line to disambiguate instead.
func parseDiffGitLine(line string) (oldTok, newTok string, ok bool) {
	const prefix = "diff --git "
	rest := strings.TrimPrefix(line, prefix)
	if rest == line {
		return "", "", false
	}
	if strings.HasPrefix(rest, `"`) {
		tok, remainder, ok2 := scanQuoted(rest)
		if !ok2 {
			return "", "", false
		}
		remainder = strings.TrimPrefix(remainder, " ")
		if strings.HasPrefix(remainder, `"`) {
			tok2, _, ok3 := scanQuoted(remainder)
			if !ok3 {
				return "", "", false
			}
			return tok, tok2, true
		}
		return tok, remainder, true
	}
	if oldTok, newTok, ok := splitEqualGitPaths(rest); ok {
		return oldTok, newTok, true
	}
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		return rest[:i], rest[i+1:], true
	}
	return "", "", false
}

// splitEqualGitPaths splits "a/P b/P" into "a/P" and "b/P" by trusting the
// shape rather than guessing where the separating space is: both tokens
// carry the same P, so the total length fixes len(P) and therefore where the
// separator has to be, even when P itself contains spaces.
func splitEqualGitPaths(rest string) (oldTok, newTok string, ok bool) {
	if len(rest) < 5 {
		return "", "", false
	}
	plen := len(rest) - 5
	if plen < 0 || plen%2 != 0 {
		return "", "", false
	}
	plen /= 2
	sep := 2 + plen // index of the space right after "a/P"
	if rest[sep] != ' ' {
		return "", "", false
	}
	if !strings.HasPrefix(rest, "a/") || !strings.HasPrefix(rest[sep+1:], "b/") {
		return "", "", false
	}
	p1, p2 := rest[2:sep], rest[sep+3:]
	if p1 != p2 {
		return "", "", false
	}
	return rest[:sep], rest[sep+1:], true
}

// parseFileHeaderPath extracts the path from a "--- " or "+++ " line:
// everything after the 4-byte prefix, up to a literal tab if present. Git
// appends one trailing tab when the path contains a space, to mark where it
// ends; a plain `diff -u` instead follows the tab with a timestamp. Either
// way, the path is whatever precedes the first tab.
func parseFileHeaderPath(line, prefix string) string {
	s := strings.TrimPrefix(line, prefix)
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	return unquoteGitPath(s)
}

// stripABPrefix removes the leading "a/" or "b/" git puts on every path
// outside of "rename from/to" and "copy from/to" (which are already bare).
func stripABPrefix(p string) string {
	if len(p) > 2 && (p[:2] == "a/" || p[:2] == "b/") {
		return p[2:]
	}
	return p
}
