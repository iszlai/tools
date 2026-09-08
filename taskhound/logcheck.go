package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
)

// th log check is the one command here that reads the board, and it only ever
// reads it: the log explains the board and never owns any of it.
//
// It separates a broken reference from an untidy one, and the reason is a lesson
// this project has already paid for once -- a check that a file is meant to fail
// is a wrong instruction in an authoritative tone, and a permanently red gate
// trains everyone to ignore it. So an id that names nothing and a closed issue
// nobody wrote up are failures, and an undated heading, a date out of order or a
// heading the format would not have written are reported. --strict fails on
// those too, for a log that has caught up with its own format.

type logProblem struct {
	Kind  string `json:"kind"`
	ID    string `json:"id,omitempty"`
	Line  int    `json:"line,omitempty"`
	Note  string `json:"note"`
	Fatal bool   `json:"fatal"`
}

type logReport struct {
	Log      string         `json:"log"`
	Board    string         `json:"board"`
	Entries  int            `json:"entries"`
	Problems []logProblem   `json:"problems"`
	Prefixes map[string]int `json:"other_prefixes,omitempty"`
}

func cmdLogCheck(args []string) error {
	fs, file, logFile := newLogFS("check")
	asJSON := fs.Bool("json", false, "print JSON")
	strict := fs.Bool("strict", false, "fail on undated headings and dates out of order too")
	since := fs.String("since", "", "only ask about issues finished on or after this date")
	parse(fs, args)

	if *since != "" && !logDatePattern.MatchString(*since) {
		return fmt.Errorf("bad date %q (want YYYY-MM-DD)", *since)
	}
	l, err := openLog(*logFile, *file)
	if err != nil {
		return err
	}
	s, err := openStore(*file)
	if err != nil {
		return err
	}
	b, err := s.Read()
	if err != nil {
		return err
	}
	archive, err := s.ReadArchive()
	if err != nil {
		return err
	}

	rep := &logReport{Log: l.Path, Board: s.Path, Entries: len(l.Entries)}
	rep.check(l, b, archive, *since, *strict)
	if *asJSON {
		if err := printJSON(rep); err != nil {
			return err
		}
	} else {
		rep.report(os.Stdout)
	}
	if n := rep.fatal(); n > 0 {
		return fmt.Errorf("log check found %d problem(s)", n)
	}
	return nil
}

func (r *logReport) add(kind, id string, line int, note string, fatal bool) {
	r.Problems = append(r.Problems, logProblem{kind, id, line, note, fatal})
}

func (r *logReport) fatal() int {
	n := 0
	for _, p := range r.Problems {
		if p.Fatal {
			n++
		}
	}
	return n
}

func (r *logReport) check(l *Log, b *Board, archive *Archive, since string, strict bool) {
	// A heading with no date, and a date that goes backwards. The log runs
	// oldest first, so a heading out of order is somebody's copied heading.
	last := ""
	for _, e := range l.Entries {
		// A heading the writer would not have produced: a wrapped title, an odd
		// dash, no date. Counted rather than corrected, because the log is
		// append-only and every entry older than the tool is one of these.
		if !e.Canonical() && e.Date != "" {
			r.add("off-format", "", e.Line, e.Title, strict)
		}
		if e.Date == "" {
			r.add("undated", "", e.Line, e.Title, strict)
			continue
		}
		if last != "" && e.Date < last {
			r.add("out-of-order", "", e.Line,
				fmt.Sprintf("%s follows %s", e.Date, last), strict)
		}
		last = e.Date
	}

	// Every id of this board's prefix that the log names has to exist, on the
	// board or in the done log. Only this prefix: a log that has outlived a
	// prefix change is full of ids that are correctly not here any more, and
	// reporting all of them would bury the one that is a typo.
	ids := l.IDs()
	known := map[string]bool{}
	for _, is := range b.Issues {
		known[is.ID] = true
	}
	for _, is := range archive.Issues {
		known[is.ID] = true
	}
	for id, line := range ids[b.Prefix] {
		if !known[id] {
			r.add("no such issue", id, line, "named in the log, on neither the board nor the done log", true)
		}
	}

	// Every finished issue should have an entry naming it: a decision nobody
	// wrote up is the thing this whole file exists to stop.
	named := ids[b.Prefix]
	for _, is := range finishedIssues(b, archive) {
		if _, ok := named[is.ID]; ok {
			continue
		}
		if since != "" && is.UpdatedAt.Format("2006-01-02") < since {
			continue
		}
		r.add("unlogged", is.ID, 0, is.Title, true)
	}

	// Everything else that looks like an id, counted rather than judged.
	r.Prefixes = map[string]int{}
	for prefix, seen := range ids {
		if prefix == b.Prefix {
			continue
		}
		r.Prefixes[prefix] = len(seen)
	}
}

func finishedIssues(b *Board, archive *Archive) []*Issue {
	var out []*Issue
	for _, is := range b.Issues {
		if is.Status == StatusDone {
			out = append(out, is)
		}
	}
	out = append(out, archive.Issues...)
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := idNum(out[i].ID)
		c, _ := idNum(out[j].ID)
		return a < c
	})
	return out
}

func (r *logReport) report(w *os.File) {
	fmt.Fprintf(w, "%s — %d entries, against %s\n", r.Log, r.Entries, r.Board)

	for _, group := range []struct {
		kind, heading string
	}{
		{"no such issue", "named in the log and on neither the board nor the done log"},
		{"unlogged", "finished with no entry naming them"},
		{"undated", "heading(s) carry no date"},
		{"out-of-order", "date(s) go backwards"},
		{"off-format", "heading(s) th log add would have written differently"},
	} {
		var found []logProblem
		for _, p := range r.Problems {
			if p.Kind == group.kind {
				found = append(found, p)
			}
		}
		if len(found) == 0 {
			continue
		}
		mark := "note"
		if found[0].Fatal {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "\n%s  %d %s\n", mark, len(found), group.heading)
		t := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, p := range found {
			where := "-"
			if p.Line > 0 {
				where = "line " + fmt.Sprint(p.Line)
			}
			fmt.Fprintf(t, "  %s\t%s\t%s\n", p.ID, where, p.Note)
		}
		t.Flush()
	}

	if len(r.Prefixes) > 0 {
		prefixes := make([]string, 0, len(r.Prefixes))
		for p := range r.Prefixes {
			prefixes = append(prefixes, p)
		}
		sort.Slice(prefixes, func(i, j int) bool {
			if r.Prefixes[prefixes[i]] != r.Prefixes[prefixes[j]] {
				return r.Prefixes[prefixes[i]] > r.Prefixes[prefixes[j]]
			}
			return prefixes[i] < prefixes[j]
		})
		parts := make([]string, 0, len(prefixes))
		for _, p := range prefixes {
			parts = append(parts, fmt.Sprintf("%s (%d)", p, r.Prefixes[p]))
		}
		fmt.Fprintf(w, "\nother id prefixes in the log, not checked: %s\n", strings.Join(parts, ", "))
	}

	if r.fatal() == 0 {
		fmt.Fprintln(w, "\nthe log and the board agree")
	}
}
