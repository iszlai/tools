package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

//go:embed skill/taskhound/SKILL.md
var skillDoc string

// version is stamped by the release build; a local build just says "dev".
var version = "dev"

const usageText = `taskhound — issues in a file you can commit

usage: th <command> [flags]

  init                     create .taskhound.yaml here
  add <title>              add an issue
  list                     list issues
  next                     issues that can be started now (no open blockers)
  show <id>                one issue in full
  deps <id>                everything <id> transitively waits on
  dependents <id>          everything that transitively waits on <id>
  update <id>              change title, description, status, blockers, labels
  comment <id> <body>      append a comment
  archive                  move long-finished issues into the done log
  sync                     push the board to GitHub Issues via the gh CLI
  ui                       serve the kanban board on localhost
  agent-guide              print the usage guide written for LLM agents
  version                  print the version

Every command takes -f <file> to point at a specific board; otherwise th walks
up from the working directory looking for .taskhound.yaml, and falls back to
$TASKHOUND_FILE.

Run th <command> -h for the flags of one command.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	var err error
	switch cmd {
	case "init":
		err = cmdInit(args)
	case "add":
		err = cmdAdd(args)
	case "list", "ls":
		err = cmdList(args)
	case "next":
		err = cmdNext(args)
	case "show":
		err = cmdShow(args)
	case "deps":
		err = cmdEdges(args, "deps")
	case "dependents":
		err = cmdEdges(args, "dependents")
	case "update":
		err = cmdUpdate(args)
	case "comment":
		err = cmdComment(args)
	case "archive":
		err = cmdArchive(args)
	case "sync":
		err = cmdSync(args)
	case "ui":
		err = cmdUI(args)
	case "agent-guide":
		fmt.Print(stripFrontMatter(skillDoc))
	case "version", "--version":
		fmt.Println("taskhound " + version)
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		err = fmt.Errorf("unknown command %q (try `th help`)", cmd)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "th: "+err.Error())
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// flag plumbing
// ---------------------------------------------------------------------------

// stringList collects a flag that may be repeated and may also carry a
// comma-separated list, so --blocked-by TH-1,TH-2 and --blocked-by TH-1
// --blocked-by TH-2 mean the same thing.
type stringList struct {
	vals []string
	set  bool
}

func (s *stringList) String() string { return strings.Join(s.vals, ",") }
func (s *stringList) Set(v string) error {
	s.set = true
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			s.vals = append(s.vals, p)
		}
	}
	return nil
}

// newFS builds a subcommand flag set that always understands -f.
func newFS(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	file := fs.String("f", "", "board file to use (default: nearest .taskhound.yaml)")
	return fs, file
}

// parse runs fs over args and returns the positional arguments. The stdlib
// flag package stops at the first non-flag word, so `th show TH-1 --json`
// would silently drop --json; this pulls the positionals out one at a time and
// keeps parsing what is left.
func parse(fs *flag.FlagSet, args []string) []string {
	var positional []string
	for {
		fs.Parse(args)
		if fs.NArg() == 0 {
			return positional
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func openStore(file string) (*Store, error) {
	if file == "" {
		file = os.Getenv("TASKHOUND_FILE")
	}
	if file != "" {
		abs, err := filepath.Abs(file)
		if err != nil {
			return nil, err
		}
		return &Store{Path: abs}, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	path, err := FindStore(cwd)
	if err != nil {
		return nil, err
	}
	return &Store{Path: path}, nil
}

// readText returns the flag value, or stdin when the value is "-", so long
// descriptions can be piped in.
func readText(v string) (string, error) {
	if v != "-" {
		return v, nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n"), nil
}

// ---------------------------------------------------------------------------
// views
// ---------------------------------------------------------------------------

// issueView is an Issue plus the facts derived from the graph, so JSON
// consumers never have to recompute them.
type issueView struct {
	*Issue
	// Shadows Issue.Priority so JSON consumers always see a concrete value,
	// even though the file leaves the default unwritten.
	Priority     string   `json:"priority"`
	Blocks       []string `json:"blocks"`
	OpenBlockers []string `json:"open_blockers"`
	Ready        bool     `json:"ready"`
	// Leverage: open issues transitively waiting on this one, and how many of
	// those are must or high. This is what orders `th next`.
	Unblocks       int `json:"unblocks"`
	UnblocksUrgent int `json:"unblocks_urgent"`
	// Urgency is the priority the issue actually has: its own, or that of the
	// most urgent open issue waiting on it, whichever is higher. Read this
	// rather than Priority when you want to know how urgent something is.
	Urgency     string `json:"urgency"`
	UrgencyFrom string `json:"urgency_from,omitempty"`
	// Set only when this issue is being offered on a board where nothing is
	// genuinely startable, so a caller can tell a pick from a free choice.
	Forced       bool   `json:"forced,omitempty"`
	ForcedReason string `json:"forced_reason,omitempty"`
}

func view(b *Board, is *Issue) issueView {
	total, urgent := b.Unblocks(is.ID)
	urgency, raisedBy := b.Urgency(is.ID)
	v := issueView{
		Issue:          is,
		Priority:       effectivePriority(is.Priority),
		Blocks:         b.Blocks(is.ID),
		OpenBlockers:   b.OpenBlockers(is),
		Ready:          b.Ready(is),
		Unblocks:       total,
		UnblocksUrgent: urgent,
		Urgency:        urgency,
		UrgencyFrom:    raisedBy,
	}
	if v.Blocks == nil {
		v.Blocks = []string{}
	}
	if v.OpenBlockers == nil {
		v.OpenBlockers = []string{}
	}
	if v.Issue.BlockedBy == nil {
		v.Issue.BlockedBy = []string{}
	}
	return v
}

func views(b *Board, issues []*Issue) []issueView {
	out := make([]issueView, 0, len(issues))
	for _, is := range issues {
		out = append(out, view(b, is))
	}
	return out
}

// priCell is the PRI column. It shows the urgency rather than the stored
// priority, because the urgency is what actually orders the board, with an
// arrow when it was inherited from something waiting on the issue.
func priCell(b *Board, is *Issue) string {
	urgency, from := b.Urgency(is.ID)
	if from != "" {
		return urgency + "\u2191"
	}
	return urgency
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printTable(b *Board, issues []*Issue) {
	if len(issues) == 0 {
		fmt.Println("(no issues)")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPRI\tSTATUS\tBLOCKED BY\tTITLE")
	for _, is := range issues {
		blockers := strings.Join(b.OpenBlockers(is), ",")
		if blockers == "" {
			blockers = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", is.ID, priCell(b, is), is.Status, blockers, is.Title)
	}
	w.Flush()
}

// ---------------------------------------------------------------------------
// commands
// ---------------------------------------------------------------------------

func cmdInit(args []string) error {
	fs, file := newFS("init")
	prefix := fs.String("prefix", "TH", "issue id prefix")
	parse(fs, args)

	path := *file
	if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		path = filepath.Join(cwd, StoreName)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	s := &Store{Path: abs}
	if err := s.Create(strings.ToUpper(*prefix)); err != nil {
		return err
	}
	fmt.Println(abs)
	return nil
}

func cmdAdd(args []string) error {
	fs, file := newFS("add")
	desc := fs.String("d", "", "description (use - to read stdin)")
	status := fs.String("status", StatusTodo, "todo|doing|done")
	priority := fs.String("priority", PriorityNormal, "must|high|normal|low")
	var blockedBy, blocks, labels stringList
	fs.Var(&blockedBy, "blocked-by", "ids this issue waits on (repeatable, comma ok)")
	fs.Var(&blocks, "blocks", "ids that should wait on this issue")
	fs.Var(&labels, "label", "label (repeatable)")
	asJSON := fs.Bool("json", false, "print the created issue as JSON")
	title := strings.TrimSpace(strings.Join(parse(fs, args), " "))
	if title == "" {
		return fmt.Errorf("add needs a title")
	}
	if !validStatus(*status) {
		return fmt.Errorf("bad status %q (want %s)", *status, strings.Join(Statuses, "|"))
	}
	if !validPriority(*priority) {
		return fmt.Errorf("bad priority %q (want %s)", *priority, strings.Join(Priorities, "|"))
	}
	body, err := readText(*desc)
	if err != nil {
		return err
	}
	s, err := openStore(*file)
	if err != nil {
		return err
	}

	var created *Issue
	var board *Board
	err = s.Update(func(b *Board) error {
		is := b.Add(title, body, *status, *priority, labels.vals)
		if err := b.SetBlockedBy(is, blockedBy.vals); err != nil {
			return err
		}
		for _, ref := range blocks.vals {
			other, err := b.Get(ref)
			if err != nil {
				return err
			}
			if err := b.SetBlockedBy(other, append(append([]string{}, other.BlockedBy...), is.ID)); err != nil {
				return err
			}
			other.UpdatedAt = is.UpdatedAt
		}
		created, board = is, b
		return nil
	})
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(view(board, created))
	}
	fmt.Println(created.ID)
	return nil
}

func cmdList(args []string) error {
	fs, file := newFS("list")
	status := fs.String("status", "", "only this status")
	label := fs.String("label", "", "only issues carrying this label")
	priority := fs.String("priority", "", "only this priority")
	ready := fs.Bool("ready", false, "only issues with no open blockers")
	blocked := fs.Bool("blocked", false, "only issues with open blockers")
	asJSON := fs.Bool("json", false, "print JSON")
	parse(fs, args)

	s, err := openStore(*file)
	if err != nil {
		return err
	}
	b, err := s.Read()
	if err != nil {
		return err
	}

	var out []*Issue
	for _, is := range b.Issues {
		if *status != "" && is.Status != *status {
			continue
		}
		if *label != "" && !hasString(is.Labels, *label) {
			continue
		}
		if *priority != "" {
			if urgency, _ := b.Urgency(is.ID); urgency != *priority {
				continue
			}
		}
		open := len(b.OpenBlockers(is)) > 0
		if *ready && (open || is.Status == StatusDone) {
			continue
		}
		if *blocked && !open {
			continue
		}
		out = append(out, is)
	}
	if *asJSON {
		return printJSON(views(b, out))
	}
	printTable(b, out)
	return nil
}

func cmdNext(args []string) error {
	fs, file := newFS("next")
	asJSON := fs.Bool("json", false, "print JSON")
	limit := fs.Int("n", 0, "show at most n issues")
	parse(fs, args)

	s, err := openStore(*file)
	if err != nil {
		return err
	}
	b, err := s.Read()
	if err != nil {
		return err
	}

	var ready []*Issue
	for _, is := range b.Issues {
		if b.Ready(is) {
			ready = append(ready, is)
		}
	}
	b.sortNext(ready)
	if *limit > 0 && len(ready) > *limit {
		ready = ready[:*limit]
	}
	diag := b.Diagnose(len(ready) > 0)

	// The diagnosis goes to stderr in both modes: it keeps stdout a clean array
	// for `jq`, and it is the kind of thing a human needs to see even when a
	// script is reading the output.
	if diag != nil {
		diag.report(os.Stderr)
	}

	if *asJSON {
		// Still a bare array, so `th next --json | jq -r '.[0].id'` keeps
		// working. A forced pick takes the place of the empty queue and says so
		// on the issue, which makes .[0].id give you something to start even on
		// a jammed board.
		out := views(b, ready)
		if len(out) == 0 && diag != nil && diag.Forced != nil {
			forced := view(b, diag.Forced)
			forced.Forced = true
			forced.ForcedReason = diag.Reason
			out = []issueView{forced}
		}
		return printJSON(out)
	}

	if len(ready) == 0 {
		// A finished board and a jammed one look identical from an empty queue,
		// and telling a finished board it is deadlocked makes the tool feel
		// broken. Say which it is, and if it is jammed, break the tie.
		open := 0
		for _, is := range b.Issues {
			if is.Status != StatusDone {
				open++
			}
		}
		switch {
		case len(b.Issues) == 0:
			fmt.Println("the board is empty")
		case open == 0:
			fmt.Println("everything on the board is done")
		default:
			diag.forcedPick(os.Stdout)
		}
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPRI\tSTATUS\tUNBLOCKS\tTITLE")
	for _, is := range ready {
		total, urgent := b.Unblocks(is.ID)
		// The urgent count is why the row is where it is, so show it rather than
		// leaving the order looking arbitrary.
		leverage := strconv.Itoa(total)
		if urgent > 0 {
			leverage += fmt.Sprintf(" (%d urgent)", urgent)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", is.ID, priCell(b, is), is.Status, leverage, is.Title)
	}
	w.Flush()
	return nil
}

func cmdShow(args []string) error {
	fs, file := newFS("show")
	asJSON := fs.Bool("json", false, "print JSON")
	rest := parse(fs, args)
	if len(rest) != 1 {
		return fmt.Errorf("show needs one issue id")
	}
	s, err := openStore(*file)
	if err != nil {
		return err
	}
	b, err := s.Read()
	if err != nil {
		return err
	}
	is, err := b.Get(rest[0])
	if err != nil {
		// An id that has left the board is not a typo — look in the done log
		// before telling the user it does not exist.
		a, aerr := s.ReadArchive()
		if aerr != nil {
			return err
		}
		for _, old := range a.Issues {
			if old.ID == b.NormalizeID(rest[0]) {
				is = old
				break
			}
		}
		if is == nil {
			return fmt.Errorf("%w (not in the done log either)", err)
		}
	}
	if *asJSON {
		return printJSON(view(b, is))
	}

	fmt.Printf("%s  %s\n", is.ID, is.Title)
	state := is.Status
	if open := b.OpenBlockers(is); len(open) > 0 && is.Status != StatusDone {
		state += " (blocked)"
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
	fmt.Fprintf(w, "status:\t%s\n", state)
	priority := effectivePriority(is.Priority)
	if urgency, from := b.Urgency(is.ID); from != "" {
		priority = fmt.Sprintf("%s (raised to %s by %s, which waits on this)", priority, urgency, from)
	}
	fmt.Fprintf(w, "priority:\t%s\n", priority)
	fmt.Fprintf(w, "blocked by:\t%s\n", withStatus(b, is.BlockedBy))
	fmt.Fprintf(w, "blocks:\t%s\n", withStatus(b, b.Blocks(is.ID)))
	if len(is.Labels) > 0 {
		fmt.Fprintf(w, "labels:\t%s\n", strings.Join(is.Labels, ", "))
	}
	fmt.Fprintf(w, "created:\t%s\n", is.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "updated:\t%s\n", is.UpdatedAt.Format(time.RFC3339))
	if !is.ArchivedAt.IsZero() {
		fmt.Fprintf(w, "archived:\t%s (in the done log, not on the board)\n", is.ArchivedAt.Format(time.RFC3339))
	}
	w.Flush()

	if is.Description != "" {
		fmt.Printf("\n%s\n", is.Description)
	}
	if len(is.Comments) > 0 {
		fmt.Println("\ncomments:")
		for _, c := range is.Comments {
			who := c.Author
			if who == "" {
				who = "anon"
			}
			fmt.Printf("  %s  %s\n", c.At.Format(time.RFC3339), who)
			for _, line := range strings.Split(c.Body, "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	}
	return nil
}

func withStatus(b *Board, ids []string) string {
	if len(ids) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if dep, err := b.Get(id); err == nil {
			parts = append(parts, fmt.Sprintf("%s (%s)", id, dep.Status))
		} else {
			parts = append(parts, id+" (missing)")
		}
	}
	return strings.Join(parts, ", ")
}

func cmdEdges(args []string, dir string) error {
	fs, file := newFS(dir)
	asJSON := fs.Bool("json", false, "print JSON")
	rest := parse(fs, args)
	if len(rest) != 1 {
		return fmt.Errorf("%s needs one issue id", dir)
	}
	s, err := openStore(*file)
	if err != nil {
		return err
	}
	b, err := s.Read()
	if err != nil {
		return err
	}
	is, err := b.Get(rest[0])
	if err != nil {
		return err
	}

	var ids []string
	if dir == "deps" {
		ids = b.Deps(is.ID)
	} else {
		ids = b.Dependents(is.ID)
	}
	var out []*Issue
	for _, id := range ids {
		if dep, err := b.Get(id); err == nil {
			out = append(out, dep)
		}
	}
	if *asJSON {
		return printJSON(views(b, out))
	}
	if len(out) == 0 {
		if dir == "deps" {
			fmt.Printf("%s waits on nothing\n", is.ID)
		} else {
			fmt.Printf("nothing waits on %s\n", is.ID)
		}
		return nil
	}
	printTable(b, out)
	return nil
}

// parseAge understands 30d and 2w as well as everything time.ParseDuration
// takes, because a done log is measured in days, not hours.
func parseAge(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "0" {
		return 0, nil
	}
	units := map[byte]time.Duration{'d': 24 * time.Hour, 'w': 7 * 24 * time.Hour}
	if unit, ok := units[v[len(v)-1]]; ok {
		n, err := strconv.Atoi(v[:len(v)-1])
		if err != nil {
			return 0, fmt.Errorf("bad age %q", v)
		}
		return time.Duration(n) * unit, nil
	}
	return time.ParseDuration(v)
}

// cmdArchive keeps the board about the work that is left. Issues finished a
// while ago move to .taskhound-done.yaml, which stays committed beside it.
func cmdArchive(args []string) error {
	fs, file := newFS("archive")
	age := fs.String("older-than", "14d", "archive issues done at least this long ago (30d, 2w, 48h, 0 for all)")
	dryRun := fs.Bool("dry-run", false, "report what would move, change nothing")
	showLog := fs.Bool("list", false, "print the done log instead of archiving")
	asJSON := fs.Bool("json", false, "print JSON")
	parse(fs, args)

	s, err := openStore(*file)
	if err != nil {
		return err
	}

	if *showLog {
		a, err := s.ReadArchive()
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(a.Issues)
		}
		if len(a.Issues) == 0 {
			fmt.Printf("the done log is empty (%s)\n", s.ArchivePath())
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tARCHIVED\tTITLE")
		for _, is := range a.Issues {
			fmt.Fprintf(w, "%s\t%s\t%s\n", is.ID, is.ArchivedAt.Format("2006-01-02"), is.Title)
		}
		return w.Flush()
	}

	d, err := parseAge(*age)
	if err != nil {
		return err
	}
	res, err := s.ArchiveDone(time.Now().UTC().Add(-d), *dryRun)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(res.Moved)
	}
	if len(res.Moved) == 0 {
		fmt.Printf("nothing done longer than %s ago\n", *age)
		return nil
	}
	verb := "archived"
	if *dryRun {
		verb = "would archive"
	}
	for _, is := range res.Moved {
		fmt.Printf("%s  %s\n", is.ID, is.Title)
	}
	fmt.Printf("%s %d issue(s) to %s\n", verb, len(res.Moved), s.ArchivePath())
	if res.Dropped > 0 {
		fmt.Printf("dropped %d reference(s) to them from issues still on the board\n", res.Dropped)
	}
	return nil
}

func cmdUpdate(args []string) error {
	fs, file := newFS("update")
	title := fs.String("title", "", "new title")
	desc := fs.String("d", "", "new description (use - to read stdin)")
	status := fs.String("status", "", "todo|doing|done")
	priority := fs.String("priority", "", "must|high|normal|low")
	var blockedBy, addBlockedBy, rmBlockedBy, blocks, labels, unlabels stringList
	fs.Var(&blockedBy, "blocked-by", "replace the blocker list")
	fs.Var(&addBlockedBy, "add-blocked-by", "add a blocker")
	fs.Var(&rmBlockedBy, "remove-blocked-by", "drop a blocker")
	fs.Var(&blocks, "blocks", "make these issues wait on this one")
	fs.Var(&labels, "label", "add a label")
	fs.Var(&unlabels, "unlabel", "remove a label")
	asJSON := fs.Bool("json", false, "print the updated issue as JSON")
	rest := parse(fs, args)

	if len(rest) != 1 {
		return fmt.Errorf("update needs one issue id")
	}
	if *status != "" && !validStatus(*status) {
		return fmt.Errorf("bad status %q (want %s)", *status, strings.Join(Statuses, "|"))
	}
	if *priority != "" && !validPriority(*priority) {
		return fmt.Errorf("bad priority %q (want %s)", *priority, strings.Join(Priorities, "|"))
	}
	body, err := readText(*desc)
	if err != nil {
		return err
	}
	s, err := openStore(*file)
	if err != nil {
		return err
	}

	var updated *Issue
	var board *Board
	err = s.Update(func(b *Board) error {
		is, err := b.Get(rest[0])
		if err != nil {
			return err
		}
		if *title != "" {
			is.Title = *title
		}
		if *desc != "" {
			is.Description = body
		}
		if *status != "" {
			is.Status = *status
		}
		if *priority != "" {
			is.Priority = storedPriority(*priority)
		}

		next := append([]string{}, is.BlockedBy...)
		if blockedBy.set {
			next = blockedBy.vals
		}
		next = append(next, addBlockedBy.vals...)
		for _, ref := range rmBlockedBy.vals {
			next = removeString(next, b.NormalizeID(ref))
		}
		if err := b.SetBlockedBy(is, next); err != nil {
			return err
		}
		for _, ref := range blocks.vals {
			other, err := b.Get(ref)
			if err != nil {
				return err
			}
			if err := b.SetBlockedBy(other, append(append([]string{}, other.BlockedBy...), is.ID)); err != nil {
				return err
			}
			other.UpdatedAt = now()
		}

		for _, l := range labels.vals {
			if !hasString(is.Labels, l) {
				is.Labels = append(is.Labels, l)
			}
		}
		for _, l := range unlabels.vals {
			is.Labels = removeString(is.Labels, l)
		}

		is.UpdatedAt = now()
		updated, board = is, b
		return nil
	})
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(view(board, updated))
	}
	fmt.Printf("%s updated\n", updated.ID)
	return nil
}

func cmdComment(args []string) error {
	fs, file := newFS("comment")
	author := fs.String("author", "", "comment author (default: $USER)")
	rest := parse(fs, args)
	if len(rest) < 2 {
		return fmt.Errorf("comment needs an issue id and a body")
	}
	body, err := readText(strings.Join(rest[1:], " "))
	if err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("comment body is empty")
	}
	who := *author
	if who == "" {
		who = currentUser()
	}
	s, err := openStore(*file)
	if err != nil {
		return err
	}
	return s.Update(func(b *Board) error {
		is, err := b.Get(rest[0])
		if err != nil {
			return err
		}
		now := now()
		is.Comments = append(is.Comments, Comment{At: now, Author: who, Body: body})
		is.UpdatedAt = now
		fmt.Printf("commented on %s\n", is.ID)
		return nil
	})
}

func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

func hasString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func removeString(list []string, v string) []string {
	out := list[:0]
	for _, s := range list {
		if s != v {
			out = append(out, s)
		}
	}
	return append([]string{}, out...)
}

func stripFrontMatter(doc string) string {
	if !strings.HasPrefix(doc, "---\n") {
		return doc
	}
	if i := strings.Index(doc[4:], "\n---\n"); i >= 0 {
		return strings.TrimLeft(doc[4+i+5:], "\n")
	}
	return doc
}
