package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// SetBlockedBy refuses to create a cycle, so a board built entirely through th
// cannot deadlock: follow the blockers back from any open issue and a DAG must
// end at something with none, which is ready by definition.
//
// The file is not built entirely through th, though. People edit it, merges
// resolve it, and until v0.4.0 an older binary could rewrite it. So the graph on
// disk can hold a cycle the API would have rejected, or a blocker naming an
// issue that no longer exists — which blocks forever, since a blocker that
// cannot be found is not done.
//
// A stuck board reporting an empty queue looks like a finished one. It should
// say what is wrong and still hand back something to work on.
//
// The same routes put the same id on two issues, and that one is quieter: Get
// answers with the first match, so the second issue is not missing from the
// board, only unreachable from it -- it lists, and nothing else about it works.
// Minting refuses to make a new one (see mintID), and the two below are for the
// ones already in the file: Duplicates finds them, Renumber moves them.

// Deadlock is why nothing is startable, and what to do about it anyway.
type Deadlock struct {
	Cycles     [][]string          `json:"cycles,omitempty"`
	Dangling   map[string][]string `json:"dangling,omitempty"` // issue id -> blockers that do not exist
	Duplicates []Duplicate         `json:"duplicates,omitempty"`
	Forced     *Issue              `json:"forced,omitempty"` // pick this to break the impasse
	Reason     string              `json:"reason,omitempty"`
}

func (d *Deadlock) empty() bool {
	return d == nil || (len(d.Cycles) == 0 && len(d.Dangling) == 0 && len(d.Duplicates) == 0)
}

// Duplicate is one id that names more than one issue.
type Duplicate struct {
	ID      string   `json:"id"`
	Titles  []string `json:"titles"`            // of the issues on the board, in file order
	Retired bool     `json:"retired,omitempty"` // the done log holds this id too
}

// Duplicates finds every id the board has handed out more than once, and every
// id it shares with the done log. A duplicate does not stop anything working,
// which is the problem with it: the board lists two TH-7s and every other
// command silently means the first.
func (b *Board) Duplicates() []Duplicate {
	titles := map[string][]string{}
	var order []string
	for _, is := range b.Issues {
		if _, seen := titles[is.ID]; !seen {
			order = append(order, is.ID)
		}
		titles[is.ID] = append(titles[is.ID], is.Title)
	}
	var out []Duplicate
	for _, id := range order {
		if len(titles[id]) > 1 || b.retired[id] {
			out = append(out, Duplicate{ID: id, Titles: titles[id], Retired: b.retired[id]})
		}
	}
	return out
}

// Renumbered records one issue moved off a taken id.
type Renumbered struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Title string `json:"title"`
}

// Renumber moves every issue holding an id something else already has onto a
// fresh one, and returns what it moved.
//
// The first holder keeps the id, which is the whole point. Get already resolves
// that id to the first issue, so every blocked_by, every log entry and every
// habit that names it goes on meaning exactly what it meant this morning; the
// issue that moves is the one nothing could reach anyway. Which of the two the
// author of a blocker had in mind is not knowable from the file, and guessing
// at it would be the one way to make this worse.
//
// An id shared with the done log is the exception, and falls out of the same
// rule: the archived copy is not on the board, so the board's copy is what
// every reference to it resolves to and the references have to follow it. So
// what gets rewritten is a blocker naming an id that is no longer on the board
// at all -- which is none of them in the ordinary case.
func (b *Board) Renumber() []Renumbered {
	held := map[string]bool{}
	for id := range b.retired {
		held[id] = true
	}
	var moved []Renumbered
	for _, is := range b.Issues {
		if !held[is.ID] {
			held[is.ID] = true
			continue
		}
		from := is.ID
		is.ID = b.mintID()
		is.UpdatedAt = now()
		held[is.ID] = true
		moved = append(moved, Renumbered{From: from, To: is.ID, Title: is.Title})
	}
	if len(moved) == 0 {
		return nil
	}
	onBoard := map[string]bool{}
	for _, is := range b.Issues {
		onBoard[is.ID] = true
	}
	for _, mv := range moved {
		if onBoard[mv.From] {
			continue
		}
		for _, is := range b.Issues {
			for i, dep := range is.BlockedBy {
				if dep == mv.From {
					is.BlockedBy[i] = mv.To
				}
			}
		}
	}
	return moved
}

// Cycles returns each group of open issues that transitively block each other.
// Done issues are excluded: a closed cycle blocks nothing, so it is history
// rather than a problem.
func (b *Board) Cycles() [][]string {
	open := map[string]*Issue{}
	for _, is := range b.Issues {
		if is.Status != StatusDone {
			open[is.ID] = is
		}
	}

	// Iterative Tarjan would be tidier; with boards this size a coloured DFS
	// that records the path is easier to read and gives the cycle directly.
	const (
		white = 0 // unvisited
		grey  = 1 // on the current path
		black = 2 // finished
	)
	colour := map[string]int{}
	var path []string
	var found [][]string
	seen := map[string]bool{}

	var visit func(id string)
	visit = func(id string) {
		colour[id] = grey
		path = append(path, id)
		for _, dep := range open[id].BlockedBy {
			if _, ok := open[dep]; !ok {
				continue // missing or done: not part of a live cycle
			}
			switch colour[dep] {
			case white:
				visit(dep)
			case grey:
				// dep is on the path, so path[from:] is the loop.
				for i, p := range path {
					if p == dep {
						cycle := append([]string{}, path[i:]...)
						if key := cycleKey(cycle); !seen[key] {
							seen[key] = true
							found = append(found, cycle)
						}
						break
					}
				}
			}
		}
		path = path[:len(path)-1]
		colour[id] = black
	}

	ids := make([]string, 0, len(open))
	for id := range open {
		ids = append(ids, id)
	}
	sort.Sort(byIDNum{ids, b.Prefix})
	for _, id := range ids {
		if colour[id] == white {
			visit(id)
		}
	}
	return found
}

// cycleKey names a cycle independently of where the walk entered it, so the
// same loop is not reported once per member.
func cycleKey(cycle []string) string {
	rotated := append([]string{}, cycle...)
	sort.Strings(rotated)
	return strings.Join(rotated, ">")
}

// Dangling finds blockers that name an issue the board does not have. They
// block forever, because a blocker that cannot be found is never done.
func (b *Board) Dangling() map[string][]string {
	out := map[string][]string{}
	for _, is := range b.Issues {
		if is.Status == StatusDone {
			continue
		}
		for _, dep := range is.BlockedBy {
			if _, err := b.Get(dep); err != nil {
				out[is.ID] = append(out[is.ID], dep)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Diagnose explains a board with open work but nothing ready, and picks
// something to do about it. Called with ready work available it still reports
// cycles, because a loop that is not blocking you today is still corrupt.
func (b *Board) Diagnose(anyReady bool) *Deadlock {
	d := &Deadlock{Cycles: b.Cycles(), Dangling: b.Dangling(), Duplicates: b.Duplicates()}
	if anyReady {
		if d.empty() {
			return nil
		}
		return d
	}

	// Nothing is ready. Force a pick: the issue whose blockers are the problem
	// is the one worth starting, since finishing it is what breaks the loop.
	var candidates []*Issue
	for _, is := range b.Issues {
		if is.Status != StatusDone {
			candidates = append(candidates, is)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	// Priority first, not leverage: inside a loop every member unblocks every
	// other, so the leverage that orders th next says nothing here.
	sort.SliceStable(candidates, func(i, j int) bool {
		a, c := candidates[i], candidates[j]
		if ra, rc := priorityRank(a.Priority), priorityRank(c.Priority); ra != rc {
			return ra < rc
		}
		if na, nc := len(b.Dependents(a.ID)), len(b.Dependents(c.ID)); na != nc {
			return na > nc
		}
		ia, _ := idNum(a.ID)
		ic, _ := idNum(c.ID)
		return ia < ic
	})
	d.Forced = candidates[0]

	switch {
	case len(d.Cycles) > 0:
		d.Reason = fmt.Sprintf("every open issue is waiting on another, in a loop (%s)",
			strings.Join(d.Cycles[0], " → "))
	case len(d.Dangling) > 0:
		d.Reason = "open issues are blocked by ids that are not on the board"
	default:
		d.Reason = "every open issue is waiting on a blocker that is not done"
	}
	return d
}

// report prints a diagnosis in the order it is useful: what is wrong, then what
// to do about it.
func (d *Deadlock) report(w interface{ Write([]byte) (int, error) }) {
	for _, cycle := range d.Cycles {
		fmt.Fprintf(w, "loop: %s → %s\n", strings.Join(cycle, " → "), cycle[0])
	}
	ids := make([]string, 0, len(d.Dangling))
	for id := range d.Dangling {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(w, "missing: %s is blocked by %s, which is not on the board\n",
			id, strings.Join(d.Dangling[id], ", "))
	}
	for _, dup := range d.Duplicates {
		fmt.Fprintf(w, "duplicate: %s %s\n", dup.ID, dup.where())
	}
	if len(d.Duplicates) > 0 {
		fmt.Fprintln(w, "th answers with the first of them; `th doctor --fix` moves the rest onto free ids")
	}
}

// where says which way an id is doubled up, and quotes the titles so you can
// tell at a glance which issue you have been talking to all along.
func (d Duplicate) where() string {
	titles := make([]string, 0, len(d.Titles))
	for _, t := range d.Titles {
		titles = append(titles, fmt.Sprintf("%q", t))
	}
	list := strings.Join(titles, ", ")
	switch {
	case len(d.Titles) > 1 && d.Retired:
		return fmt.Sprintf("names %d issues on the board (%s) and one in the done log", len(d.Titles), list)
	case len(d.Titles) > 1:
		return fmt.Sprintf("names %d issues (%s)", len(d.Titles), list)
	default:
		return fmt.Sprintf("is on the board (%s) and in the done log", list)
	}
}

// forcedPick is what to do about it, printed where the queue would have been.
func (d *Deadlock) forcedPick(w interface{ Write([]byte) (int, error) }) {
	if d == nil || d.Forced == nil {
		return
	}
	fmt.Fprintf(w, "nothing is startable: %s\n", d.Reason)
	fmt.Fprintf(w, "forced pick: %s  %s\n", d.Forced.ID, d.Forced.Title)
	if len(d.Forced.BlockedBy) > 0 {
		fmt.Fprintf(w, "start it anyway, or cut the edge: th update %s --remove-blocked-by %s\n",
			d.Forced.ID, strings.Join(d.Forced.BlockedBy, ","))
	}
}

// ---------------------------------------------------------------------------
// th doctor
// ---------------------------------------------------------------------------

// th doctor is the same three checks th next runs, read off a board you have
// not asked a question of -- and the one place that repairs what it finds.
//
// Only the duplicates are repairable. A loop and a dangling blocker are a
// statement about the work that a person has to make: which edge was wrong.
// An id held twice is not a statement about anything, so th can put it right
// on its own, and should: the alternative is editing the YAML by hand, which
// is how at least one of these boards came to have a duplicate in the first
// place.
type doctorReport struct {
	Board      string              `json:"board"`
	Duplicates []Duplicate         `json:"duplicates,omitempty"`
	Cycles     [][]string          `json:"cycles,omitempty"`
	Dangling   map[string][]string `json:"dangling,omitempty"`
	Renumbered []Renumbered        `json:"renumbered,omitempty"`
	Healthy    bool                `json:"healthy"`
}

func cmdDoctor(args []string) error {
	fs, file := newFS("doctor")
	fix := fs.Bool("fix", false, "move issues holding a taken id onto free ones")
	asJSON := fs.Bool("json", false, "print JSON")
	parse(fs, args)

	s, err := openStore(*file)
	if err != nil {
		return err
	}

	rep := doctorReport{Board: s.Path}
	look := func(b *Board) {
		rep.Duplicates, rep.Cycles, rep.Dangling = b.Duplicates(), b.Cycles(), b.Dangling()
	}
	if *fix {
		// The scan happens after the repair and inside the same lock, so what
		// is reported is the board as it now stands rather than as it was.
		err = s.Update(func(b *Board) error {
			rep.Renumbered = b.Renumber()
			look(b)
			return nil
		})
	} else {
		var b *Board
		if b, err = s.Read(); err == nil {
			look(b)
		}
	}
	if err != nil {
		return err
	}
	left := len(rep.Duplicates) + len(rep.Cycles) + len(rep.Dangling)
	rep.Healthy = left == 0

	if *asJSON {
		return printJSON(rep)
	}
	for _, mv := range rep.Renumbered {
		fmt.Printf("moved: %s is now %s  %s\n", mv.From, mv.To, mv.Title)
	}
	if len(rep.Renumbered) > 0 {
		fmt.Println("the id each one had stays with the issue that had it first, so nothing that " +
			"named it has changed meaning — check the captain's log if it discussed the ones that moved")
	}
	(&Deadlock{Duplicates: rep.Duplicates, Cycles: rep.Cycles, Dangling: rep.Dangling}).report(os.Stdout)
	if rep.Healthy {
		if len(rep.Renumbered) == 0 {
			fmt.Println("no problems found")
		}
		return nil
	}
	return fmt.Errorf("%d problem%s left on the board", left, plural(left))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
