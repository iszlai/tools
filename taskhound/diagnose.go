package main

import (
	"fmt"
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

// Deadlock is why nothing is startable, and what to do about it anyway.
type Deadlock struct {
	Cycles   [][]string          `json:"cycles,omitempty"`
	Dangling map[string][]string `json:"dangling,omitempty"` // issue id -> blockers that do not exist
	Forced   *Issue              `json:"forced,omitempty"`   // pick this to break the impasse
	Reason   string              `json:"reason,omitempty"`
}

func (d *Deadlock) empty() bool {
	return d == nil || (len(d.Cycles) == 0 && len(d.Dangling) == 0)
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
	d := &Deadlock{Cycles: b.Cycles(), Dangling: b.Dangling()}
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
