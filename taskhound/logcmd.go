package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

const logUsage = `th log — the captain's log: decisions, in a file you can commit

usage: th log <command> [flags]

  add <title>         append an entry, body from stdin or -d
  tail [n]            the last n entries (default 3)
  since <date>        every entry from a date onwards
  ls                  one line per entry
  grep <pattern>      matches grouped under the entry they belong to
  issue <id>          every entry naming an issue, headings first
  amendments [doc]    entries that record a change to a document
  check               the log and the board agree

The log is append-only: th reads it and adds to the end, and never rewrites or
reorders it. Status lives on the board; this file is decisions only.

Every command takes --log <file> to point at a specific log; otherwise th walks
up from the working directory looking for captains-log.md, falls back to
$TASKHOUND_LOG, and then to the log beside the board.
`

// newLogFS is newFS plus --log, because a log command may need to reach the
// board too: th log check reads it, and th log issue normalises an id with it.
func newLogFS(name string) (*flag.FlagSet, *string, *string) {
	fs, file := newFS("log " + name)
	logFile := fs.String("log", "", "log file to use (default: nearest "+LogName+")")
	return fs, file, logFile
}

func openLog(logFile, boardFile string) (*Log, error) {
	path, err := FindLog(logFile, boardFile)
	if err != nil {
		return nil, err
	}
	return ReadLog(path)
}

func cmdLog(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, logUsage)
		return fmt.Errorf("log needs a command")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return cmdLogAdd(rest)
	case "tail":
		return cmdLogTail(rest)
	case "since":
		return cmdLogSince(rest)
	case "ls", "list":
		return cmdLogList(rest)
	case "grep", "find":
		return cmdLogGrep(rest)
	case "issue":
		return cmdLogIssue(rest)
	case "amendments", "amend":
		return cmdLogAmendments(rest)
	case "check":
		return cmdLogCheck(rest)
	case "help", "-h", "--help":
		fmt.Print(logUsage)
		return nil
	}
	return fmt.Errorf("unknown log command %q (try `th log help`)", sub)
}

// ---------------------------------------------------------------------------
// add
// ---------------------------------------------------------------------------

func cmdLogAdd(args []string) error {
	fs, file, logFile := newLogFS("add")
	desc := fs.String("d", "", "entry body (use - to read stdin; stdin is the default when piped)")
	sub := fs.Bool("sub", false, "write a ### sub-entry under the last entry")
	date := fs.String("date", "", "date for the heading (default: today, locally)")
	id := fs.String("id", "", "issue this entry is about; goes at the front of the title")
	title := strings.TrimSpace(strings.Join(parse(fs, args), " "))
	if title == "" {
		return fmt.Errorf("add needs a title")
	}

	body, err := readEntryBody(*desc)
	if err != nil {
		return err
	}
	l, err := openLog(*logFile, *file)
	if err != nil {
		return err
	}
	when := *date
	if when == "" {
		when = Today()
	}
	if !*sub && !logDatePattern.MatchString(when) {
		return fmt.Errorf("bad date %q (want YYYY-MM-DD)", when)
	}
	if *sub && len(l.Entries) == 0 {
		return fmt.Errorf("--sub writes under the last entry and the log has none yet")
	}

	// The id belongs in the heading rather than only in the prose: that is what
	// puts the entry at the top of `th log issue`, which is the question a cold
	// session asks first and the one the board cannot answer.
	heading := l.Heading(when, TitleWithID(normalizeLogID(*id, *file), title), *sub)
	if err := l.Append(heading, body); err != nil {
		return err
	}
	fmt.Println(heading)
	return nil
}

// readEntryBody takes the body from -d, or from stdin when -d is "-" or when
// nothing was passed and stdin is a pipe. An entry body is a heredoc far more
// often than it is a flag, so piping needs no ceremony.
func readEntryBody(v string) (string, error) {
	if v != "" && v != "-" {
		return v, nil
	}
	if v == "" {
		info, err := os.Stdin.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice != 0 {
			return "", nil
		}
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n"), nil
}

// ---------------------------------------------------------------------------
// reading whole entries
// ---------------------------------------------------------------------------

func cmdLogTail(args []string) error {
	fs, file, logFile := newLogFS("tail")
	asJSON := fs.Bool("json", false, "print JSON")
	rest := parse(fs, args)

	n := 3
	if len(rest) > 0 {
		v, err := strconv.Atoi(rest[0])
		if err != nil || v < 1 {
			return fmt.Errorf("tail wants a count, not %q", rest[0])
		}
		n = v
	}
	l, err := openLog(*logFile, *file)
	if err != nil {
		return err
	}
	return printEntries(l.Tail(n), *asJSON)
}

func cmdLogSince(args []string) error {
	fs, file, logFile := newLogFS("since")
	asJSON := fs.Bool("json", false, "print JSON")
	rest := parse(fs, args)
	if len(rest) != 1 {
		return fmt.Errorf("since needs a date (YYYY-MM-DD)")
	}
	if !logDatePattern.MatchString(rest[0]) {
		return fmt.Errorf("bad date %q (want YYYY-MM-DD)", rest[0])
	}
	l, err := openLog(*logFile, *file)
	if err != nil {
		return err
	}
	return printEntries(l.Since(rest[0]), *asJSON)
}

func cmdLogList(args []string) error {
	fs, file, logFile := newLogFS("ls")
	asJSON := fs.Bool("json", false, "print JSON")
	parse(fs, args)

	l, err := openLog(*logFile, *file)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(entryHeads(l.Entries))
	}
	if len(l.Entries) == 0 {
		fmt.Printf("%s has no entries yet\n", l.Path)
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "LINE\tDATE\tTITLE")
	for _, e := range l.Entries {
		date := e.Date
		if date == "" {
			date = "-"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\n", e.Line, date, e.Title)
	}
	return w.Flush()
}

// printEntries prints entries whole, one after another, the way the file has
// them. Entry by entry rather than by line number is the whole point: a range
// of lines is something a reader has to reassemble into a decision.
func printEntries(entries []*LogEntry, asJSON bool) error {
	if asJSON {
		return printJSON(entries)
	}
	if len(entries) == 0 {
		fmt.Println("(no entries)")
		return nil
	}
	for i, e := range entries {
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(e.Text())
	}
	return nil
}

// entryHead is an entry without its body, for th log ls --json.
type entryHead struct {
	Date      string `json:"date,omitempty"`
	Effective string `json:"effective_date,omitempty"`
	Title     string `json:"title"`
	Line      int    `json:"line"`
}

func entryHeads(entries []*LogEntry) []entryHead {
	out := make([]entryHead, 0, len(entries))
	for _, e := range entries {
		out = append(out, entryHead{e.Date, e.Effective, e.Title, e.Line})
	}
	return out
}

// ---------------------------------------------------------------------------
// searching
// ---------------------------------------------------------------------------

// logMatch is a set of hits and the entry they belong to.
type logMatch struct {
	Entry *entryHead `json:"entry"`
	Hits  []LogHit   `json:"hits"`
	Text  string     `json:"text,omitempty"`
}

func cmdLogGrep(args []string) error {
	fs, file, logFile := newLogFS("grep")
	asJSON := fs.Bool("json", false, "print JSON")
	full := fs.Bool("full", false, "print the whole of every matching entry")
	fixed := fs.Bool("fixed", false, "match the pattern literally, not as a regexp")
	matchCase := fs.Bool("match-case", false, "case-sensitive (default: insensitive)")
	rest := parse(fs, args)
	if len(rest) != 1 {
		return fmt.Errorf("grep needs one pattern")
	}
	re, err := logPattern(rest[0], *fixed, *matchCase)
	if err != nil {
		return err
	}
	l, err := openLog(*logFile, *file)
	if err != nil {
		return err
	}
	return printMatches(l, searchLog(l.Entries, re), *asJSON, *full, rest[0])
}

func logPattern(pattern string, fixed, matchCase bool) (*regexp.Regexp, error) {
	if fixed {
		pattern = regexp.QuoteMeta(pattern)
	}
	if !matchCase {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}

func searchLog(entries []*LogEntry, re *regexp.Regexp) []logMatch {
	var out []logMatch
	for _, e := range entries {
		if hits := e.Search(re); len(hits) > 0 {
			head := entryHeads([]*LogEntry{e})[0]
			out = append(out, logMatch{Entry: &head, Hits: hits, Text: e.Text()})
		}
	}
	return out
}

// printMatches groups hits under the entry that holds them. A hit that says
// which decision it belongs to is the difference between reading the answer and
// spawning something to reconstruct it.
func printMatches(l *Log, matches []logMatch, asJSON, full bool, what string) error {
	if asJSON {
		if !full {
			for i := range matches {
				matches[i].Text = ""
			}
		}
		return printJSON(matches)
	}
	if len(matches) == 0 {
		fmt.Printf("nothing in the log matches %s\n", what)
		return nil
	}
	// path:line, so an editor can be pointed at the entry rather than at a
	// line number somebody has to go and find.
	where := filepath.Base(l.Path)
	hits := 0
	for i, m := range matches {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("%s:%d  %s\n", where, m.Entry.Line, entryLabel(m.Entry))
		if full {
			fmt.Println()
			fmt.Println(m.Text)
			hits += len(m.Hits)
			continue
		}
		for _, h := range m.Hits {
			hits++
			if h.Head {
				// The label above already carries the heading; printing it
				// again says nothing except that the match was in the title.
				fmt.Printf("  %6d  (in the title)\n", h.Line)
				continue
			}
			fmt.Printf("  %6d  %s\n", h.Line, strings.TrimSpace(h.Text))
		}
	}
	fmt.Printf("\n%d hit(s) in %d entry(ies)\n", hits, len(matches))
	return nil
}

func entryLabel(h *entryHead) string {
	date := h.Date
	if date == "" {
		date = "(undated)"
	}
	return date + "  " + h.Title
}

// ---------------------------------------------------------------------------
// issue
// ---------------------------------------------------------------------------

func cmdLogIssue(args []string) error {
	fs, file, logFile := newLogFS("issue")
	asJSON := fs.Bool("json", false, "print JSON")
	full := fs.Bool("full", false, "print the whole of every matching entry")
	rest := parse(fs, args)
	if len(rest) != 1 {
		return fmt.Errorf("issue needs one issue id")
	}
	l, err := openLog(*logFile, *file)
	if err != nil {
		return err
	}
	id := normalizeLogID(rest[0], *file)
	re, err := regexp.Compile(`\b` + regexp.QuoteMeta(id) + `\b`)
	if err != nil {
		return err
	}

	matches := searchLog(l.Entries, re)
	sortHeadingsFirst(matches)
	if *asJSON {
		return printMatches(l, matches, true, *full, id)
	}
	if len(matches) == 0 {
		fmt.Printf("no entry in the log names %s\n", id)
		return nil
	}
	titled := 0
	for _, m := range matches {
		if matchInHeading(m) {
			titled++
		}
	}
	fmt.Printf("%s: %d entry(ies), %d of them about it\n\n", id, len(matches), titled)
	return printMatches(l, matches, false, *full, id)
}

// sortHeadingsFirst puts the entries an id is the subject of ahead of the ones
// that merely reference it. An id in a title is what the entry is about; an id
// in the prose is a decision that touched it. This is the question `th show`
// cannot answer and the one a cold session needs most, so the ordering is the
// answer rather than a presentation detail.
func sortHeadingsFirst(matches []logMatch) {
	sort.SliceStable(matches, func(i, j int) bool {
		return matchInHeading(matches[i]) && !matchInHeading(matches[j])
	})
}

func matchInHeading(m logMatch) bool {
	for _, h := range m.Hits {
		if h.Head {
			return true
		}
	}
	return false
}

// normalizeLogID puts a reference through the board's own id rules when a board
// is reachable, so `th log issue 7` finds the same issue `th show 7` does.
func normalizeLogID(ref, boardFile string) string {
	if s, err := openStore(boardFile); err == nil {
		if b, err := s.Read(); err == nil {
			return b.NormalizeID(ref)
		}
	}
	return strings.ToUpper(strings.TrimSpace(ref))
}

// ---------------------------------------------------------------------------
// amendments
// ---------------------------------------------------------------------------

func cmdLogAmendments(args []string) error {
	fs, file, logFile := newLogFS("amendments")
	asJSON := fs.Bool("json", false, "print JSON")
	full := fs.Bool("full", false, "print the whole of every matching entry")
	rest := parse(fs, args)

	l, err := openLog(*logFile, *file)
	if err != nil {
		return err
	}
	doc := ""
	if len(rest) > 0 {
		doc = rest[0]
	}

	var matches []logMatch
	loose := 0
	for _, e := range l.Entries {
		marked := amendmentLines(e)
		if len(marked) == 0 {
			// The tool can only see the convention. An entry that says it
			// amended something in prose is invisible here, and saying how many
			// is what makes adopting the marker worth it.
			if mentionsAmendment(e) {
				loose++
			}
			continue
		}
		if doc != "" && !linesMention(marked, doc) {
			continue
		}
		head := entryHeads([]*LogEntry{e})[0]
		matches = append(matches, logMatch{Entry: &head, Hits: marked, Text: e.Text()})
	}

	if loose > 0 {
		fmt.Fprintf(os.Stderr,
			"note: %d entry(ies) mention an amendment with no **Amended:** line, so this cannot see them\n", loose)
	}
	what := "an **Amended:** line"
	if doc != "" {
		what = "an **Amended:** line naming " + doc
	}
	return printMatches(l, matches, *asJSON, *full, what)
}

func amendmentLines(e *LogEntry) []LogHit {
	var out []LogHit
	body := e.Line + len(e.heading)
	for i, line := range e.Body {
		if logAmendPattern.MatchString(line) {
			out = append(out, LogHit{Line: body + i, Text: line})
		}
	}
	return out
}

func mentionsAmendment(e *LogEntry) bool {
	for _, line := range e.Body {
		if logAmendProse.MatchString(line) {
			return true
		}
	}
	return false
}

func linesMention(hits []LogHit, what string) bool {
	needle := strings.ToLower(what)
	for _, h := range hits {
		if strings.Contains(strings.ToLower(h.Text), needle) {
			return true
		}
	}
	return false
}
