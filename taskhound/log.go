package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The captain's log is the second file that carries state and the only one
// worked by hand: an append-only markdown file of decisions, committed beside
// the board. The board owns status; the log owns why.
//
// Everything here reads the file and appends to it. Nothing rewrites it and
// nothing reorders it, because the file has to stay readable with `less` on a
// machine that has never heard of th -- which is the same reason the log holds
// no status of its own and `th log check` reads the board without writing it.
//
// There is one format and th log writes it. Deriving it from the last entry was
// the first attempt and it is worse: it perpetuates whatever drift is already in
// the file, and two agents appending on two days get two shapes depending on
// what happened to be last. LogHeading below is the format, in one place.
//
// Reading stays tolerant, because a log that predates the tool has shapes the
// tool would not write -- a title wrapped onto a second ## line, a heading with
// no date -- and the file may never be rewritten to suit its reader. What
// closes the gap is that `th log check` counts the entries that do not match,
// so drift is something you can see rather than something the parser quietly
// absorbs.

// LogName is the file th log looks for, found by walking up from the working
// directory the way the board is.
const LogName = "captains-log.md"

var (
	logDatePattern  = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})`)
	logRulePattern  = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})$`)
	logIDPattern    = regexp.MustCompile(`\b([A-Z][A-Z0-9]*)-([0-9]+[a-z]?)\b`)
	logAmendPattern = regexp.MustCompile(`(?i)^\s*(?:\*\*|__)?amend(?:ed|ment)?s?:(?:\*\*|__)?`)
	logAmendProse   = regexp.MustCompile(`(?i)\bamend(?:ed|ing|ment|ments|s)?\b`)
)

// The format. An entry is a heading, a blank line and prose:
//
//	## 2026-09-08 — What was decided
//
//	Context, the decision, and what was rejected.
//
// No separator rule: the heading already separates. An id goes at the front of
// the title, `V8-60: what was decided`, which is what puts it in the heading
// where `th log issue` offers it first. A sub-entry is `### Title` with no date,
// because it sits inside a dated one.
const (
	LogDash    = "—"
	LogIDSplit = ": "
)

// dateDashes are the punctuation a heading may already use between its date and
// its title, longest first so "--" is not read as "-". Only the reader needs
// this; the writer uses LogDash.
var dateDashes = []string{"—", "–", "--", "-", ":"}

// LogHeading renders the one heading th log writes. Every check of whether an
// entry matches the format goes through this same function, so the format and
// the check cannot drift apart.
func LogHeading(date, title string) string {
	return "## " + date + " " + LogDash + " " + title
}

func LogSubHeading(title string) string { return "### " + title }

// TitleWithID puts an id at the front of a title the way the format asks, and
// leaves a title that already names it alone.
func TitleWithID(id, title string) string {
	if id == "" || strings.HasPrefix(strings.ToUpper(title), strings.ToUpper(id)) {
		return title
	}
	return id + LogIDSplit + title
}

// LogEntry is one decision: a ## heading and everything under it.
type LogEntry struct {
	// Date as the heading wrote it, empty for a heading that carries none.
	Date string `json:"date,omitempty"`
	// Effective is Date, or the date of the nearest dated entry above it, so an
	// undated heading still sorts and filters where it actually sits.
	Effective string   `json:"effective_date,omitempty"`
	Title     string   `json:"title"`
	Line      int      `json:"line"`
	Body      []string `json:"body"`

	heading []string // as written; a long title may wrap onto a second ## line
}

// LogHit is a matching line inside an entry, carrying its line number in the
// file so an editor can be pointed straight at it.
type LogHit struct {
	Line int    `json:"line"`
	Text string `json:"text"`
	Head bool   `json:"in_heading,omitempty"`
}

// Canonical reports whether the entry is shaped the way th log add writes one.
// Entries older than the tool will not be, which is the point of counting them
// rather than refusing to read them.
func (e *LogEntry) Canonical() bool {
	if len(e.heading) != 1 || e.Date == "" || e.Title == "" {
		return false
	}
	return e.heading[0] == LogHeading(e.Date, e.Title)
}

func (e *LogEntry) Label() string {
	date := e.Date
	if date == "" {
		date = "(undated)"
	}
	return date + "  " + e.Title
}

// Text is the entry as it stands in the file, heading included.
func (e *LogEntry) Text() string {
	out := append([]string{}, e.heading...)
	return strings.Join(append(out, e.Body...), "\n")
}

// Search returns every line of the entry the pattern matches. A hit in the
// heading is marked, because th log issue offers those first: an id in a title
// is what the entry is about, and an id in the prose is a reference to it.
func (e *LogEntry) Search(re *regexp.Regexp) []LogHit {
	var hits []LogHit
	for i, line := range e.heading {
		if re.MatchString(line) {
			hits = append(hits, LogHit{Line: e.Line + i, Text: line, Head: true})
		}
	}
	body := e.Line + len(e.heading)
	for i, line := range e.Body {
		if re.MatchString(line) {
			hits = append(hits, LogHit{Line: body + i, Text: line})
		}
	}
	return hits
}

// Log is the whole file: the header nobody edits, then the entries.
type Log struct {
	Path     string
	Preamble []string
	Entries  []*LogEntry

	endsBlank   bool // the file ends with a blank line
	endsNewline bool
	empty       bool
}

// FindLog resolves which log to work on: the flag, then $TASKHOUND_LOG, then
// the nearest one above the working directory, then the one beside the board.
func FindLog(logFile, boardFile string) (string, error) {
	if logFile == "" {
		logFile = os.Getenv("TASKHOUND_LOG")
	}
	if logFile != "" {
		return filepath.Abs(logFile)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if path, err := FindUp(cwd, LogName); err == nil {
		return path, nil
	}
	// Not above the working directory. A log and the board it explains are
	// committed together, so try beside the board before giving up.
	if s, err := openStore(boardFile); err == nil {
		beside := filepath.Join(filepath.Dir(s.Path), LogName)
		if _, err := os.Stat(beside); err == nil {
			return beside, nil
		}
	}
	return "", fmt.Errorf("no %s here or in any parent directory (point at one with --log)", LogName)
}

func ReadLog(path string) (*Log, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	l := ParseLog(data)
	l.Path = path
	return l, nil
}

// ParseLog reads the file into entries in one pass. There is no index and there
// is not going to be one: a 16k-line log is half a megabyte, a full scan is a
// millisecond, and the thing that made grep expensive was never the scan -- it
// was getting back line hits with no idea which decision they belonged to.
func ParseLog(data []byte) *Log {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	l := &Log{
		empty:       strings.TrimSpace(text) == "",
		endsNewline: text == "" || strings.HasSuffix(text, "\n"),
	}
	trimmed := strings.TrimRight(text, "\n")
	l.endsBlank = len(text) > len(trimmed)+1
	lines := strings.Split(trimmed, "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}

	for i, line := range lines {
		if !isLogHeading(line) {
			if n := len(l.Entries); n > 0 {
				e := l.Entries[n-1]
				e.Body = append(e.Body, line)
			} else {
				l.Preamble = append(l.Preamble, line)
			}
			continue
		}
		// A ## line directly under another, carrying no date, is a title that
		// wrapped rather than a new entry. Narrow on purpose: it is the only
		// shape a wrapped heading takes, and anything looser would swallow a
		// genuine undated entry that happens to follow one with no blank line.
		if n := len(l.Entries); n > 0 {
			prev := l.Entries[n-1]
			if len(prev.Body) == 0 && !logDatePattern.MatchString(logHeadingText(line)) {
				prev.heading = append(prev.heading, line)
				prev.Title = strings.TrimSpace(prev.Title + " " + logHeadingText(line))
				continue
			}
		}
		date, title := splitLogHeading(line)
		l.Entries = append(l.Entries, &LogEntry{
			Date: date, Title: title, Line: i + 1, heading: []string{line},
		})
	}

	// A --- between two entries is punctuation, not the last line of the one
	// above. Lift it off, so printing an entry back does not trail a rule that
	// was never part of it.
	l.Preamble, _ = trimLogRule(l.Preamble)
	for _, e := range l.Entries {
		e.Body, _ = trimLogRule(e.Body)
	}

	// An undated heading sits after some dated one, and that is the date it
	// belongs to for sorting and for --since.
	var seen string
	for _, e := range l.Entries {
		if e.Date != "" {
			seen = e.Date
		}
		e.Effective = seen
	}
	return l
}

func isLogHeading(line string) bool { return strings.HasPrefix(line, "## ") }

func logHeadingText(line string) string {
	return strings.TrimSpace(strings.TrimPrefix(line, "##"))
}

func splitLogHeading(line string) (date, title string) {
	text := logHeadingText(line)
	m := logDatePattern.FindString(text)
	if m == "" {
		return "", text
	}
	rest := strings.TrimLeft(text[len(m):], " ")
	for _, d := range dateDashes {
		if strings.HasPrefix(rest, d) {
			return m, strings.TrimSpace(rest[len(d):])
		}
	}
	return m, strings.TrimSpace(rest)
}

// trimLogRule drops trailing blank lines and, if a horizontal rule is left at
// the end, that too -- reporting whether it found one.
func trimLogRule(lines []string) ([]string, bool) {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if end > 0 && logRulePattern.MatchString(strings.TrimSpace(lines[end-1])) {
		return lines[:end-1], true
	}
	return lines[:end], false
}

// ---------------------------------------------------------------------------
// appending
// ---------------------------------------------------------------------------

// Heading renders the line th log add is about to write, which is the format and
// never whatever the last entry happened to do.
func (l *Log) Heading(date, title string, sub bool) string {
	if sub {
		return LogSubHeading(title)
	}
	return LogHeading(date, title)
}

// Append writes one entry to the end of the file and returns the heading it
// wrote. It is the only thing here that touches the file at all.
func (l *Log) Append(heading, body string) error {
	var b strings.Builder
	switch {
	case l.empty:
	case !l.endsNewline:
		b.WriteString("\n\n")
	case !l.endsBlank:
		b.WriteString("\n")
	}
	b.WriteString(heading + "\n")
	if strings.TrimSpace(body) != "" {
		b.WriteString("\n" + strings.TrimRight(body, "\n") + "\n")
	}

	f, err := os.OpenFile(l.Path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	// One write, in append mode. The kernel serialises the seek and the write
	// for a regular file, so two agents logging at the same moment cannot
	// interleave -- which is why this needs no lock file beside a log people
	// also edit by hand.
	if _, err := f.WriteString(b.String()); err != nil {
		return err
	}
	return f.Sync()
}

// Today is the date a new heading carries: the local one, because a heading is
// the day the person writing it is having. The board's timestamps are UTC
// because they are machine time; this is not.
func Today() string { return time.Now().Format("2006-01-02") }

// ---------------------------------------------------------------------------
// queries
// ---------------------------------------------------------------------------

// Tail returns the last n entries.
func (l *Log) Tail(n int) []*LogEntry {
	if n <= 0 || n > len(l.Entries) {
		n = len(l.Entries)
	}
	return l.Entries[len(l.Entries)-n:]
}

// Since returns every entry from a date onwards, undated ones included where
// they sit.
func (l *Log) Since(date string) []*LogEntry {
	var out []*LogEntry
	for _, e := range l.Entries {
		if e.Effective >= date {
			out = append(out, e)
		}
	}
	return out
}

// IDs returns every board-id-shaped token in the log, by prefix, with the line
// each was first seen on. An optional letter suffix is allowed because a log
// outlives its board and people subdivide by hand -- WO-8a, WO-10b, V5-00 are
// all real references. Even so the pattern catches UTF-8 and AES-256, which is
// why the caller only ever checks the prefix its own board uses.
func (l *Log) IDs() map[string]map[string]int {
	out := map[string]map[string]int{}
	note := func(line string, n int) {
		for _, m := range logIDPattern.FindAllStringSubmatch(line, -1) {
			prefix, id := m[1], m[0]
			if out[prefix] == nil {
				out[prefix] = map[string]int{}
			}
			if _, seen := out[prefix][id]; !seen {
				out[prefix][id] = n
			}
		}
	}
	for _, e := range l.Entries {
		for i, h := range e.heading {
			note(h, e.Line+i)
		}
		body := e.Line + len(e.heading)
		for i, line := range e.Body {
			note(line, body+i)
		}
	}
	return out
}

// logIDShape is logIDPattern anchored, with the number's padding split off, so
// an id written by either hand can be reduced to one form.
var logIDShape = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*)-0*([0-9]+)([A-Za-z]?)$`)

// logIDKey reduces an id to the form the board writes it in: prefix uppercased,
// leading zeros off the number, a hand-added letter suffix kept and lowercased.
//
// The log and the board are two files kept by different hands, and a log that
// writes V6-01 where the board writes V6-1 is not naming a different issue --
// it is padding. Every comparison between the two goes through here, so the
// question is about the issue rather than about its spelling. Without it a real
// log reported every finished issue as unlogged and every id it named as
// existing nowhere, which is a gate nobody would keep.
func logIDKey(id string) string {
	id = strings.TrimSpace(id)
	m := logIDShape.FindStringSubmatch(id)
	if m == nil {
		return strings.ToUpper(id)
	}
	return strings.ToUpper(m[1]) + "-" + m[2] + strings.ToLower(m[3])
}

// logIDRegexp matches an id however the log spelled its number, so asking about
// V6-1 finds the entries that wrote V6-01 and asking about V6-01 finds the ones
// that wrote V6-1. A ref that is not id-shaped is matched as written.
func logIDRegexp(id string) (*regexp.Regexp, error) {
	m := logIDShape.FindStringSubmatch(strings.TrimSpace(id))
	if m == nil {
		return regexp.Compile(`\b` + regexp.QuoteMeta(id) + `\b`)
	}
	return regexp.Compile(`(?i)\b` + regexp.QuoteMeta(m[1]) + `-0*` + m[2] + m[3] + `\b`)
}
