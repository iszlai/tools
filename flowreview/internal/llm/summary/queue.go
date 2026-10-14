// Pipeline is FR-28's worker: a priority queue of hunks waiting on a
// summary, drained by a small, fixed pool of workers so at most that many
// requests are ever in flight against the embedded model at once. See
// pipeline.go for Pipeline itself; this file is just the heap underneath it.
package summary

import "container/heap"

// jobState tracks one job through the queue, so a hunk already running (or
// already done) is never pushed back onto the heap by a second Request for
// it.
type jobState int

const (
	stateQueued jobState = iota
	stateRunning
)

// job is one queued or running summary request.
type job struct {
	hunkID   string
	key      string
	input    Input
	priority int // lower runs sooner
	// bgPriority is the priority Prefetch gave this job (0: never
	// prefetched). A client Request can raise priority above it; when the
	// client stops wanting the hunk, the job drops back to bgPriority
	// instead of being cancelled.
	bgPriority int
	seq        int // tie-breaker: insertion order, for a stable sort among equal priorities
	state      jobState
	index      int // heap.Interface bookkeeping; -1 when not in the heap
}

// jobQueue is a container/heap min-heap of *job, ordered by priority then by
// insertion order.
type jobQueue []*job

func (q jobQueue) Len() int { return len(q) }
func (q jobQueue) Less(i, j int) bool {
	if q[i].priority != q[j].priority {
		return q[i].priority < q[j].priority
	}
	return q[i].seq < q[j].seq
}
func (q jobQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index, q[j].index = i, j
}

func (q *jobQueue) Push(x any) {
	j := x.(*job)
	j.index = len(*q)
	*q = append(*q, j)
}

func (q *jobQueue) Pop() any {
	old := *q
	n := len(old)
	j := old[n-1]
	old[n-1] = nil
	j.index = -1
	*q = old[:n-1]
	return j
}

var _ heap.Interface = (*jobQueue)(nil)
