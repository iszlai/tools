// Package model holds the types every other package speaks. They are frozen:
// later slices add fields, they do not rename or remove them, because sessions
// on disk and the HTTP API both carry these shapes as JSON.
package model

import "time"

// Side says which half of a diff a line belongs to.
type Side string

const (
	SideOld Side = "old"
	SideNew Side = "new"
)

// Anchor pins a comment or a reviewed mark to one line of one diff. HeadSHA is
// the head the anchor was taken against; publishing checks it still maps.
type Anchor struct {
	Path     string `json:"path"`
	Side     Side   `json:"side"`
	Line     int    `json:"line"`
	HunkID   string `json:"hunk_id"`
	HeadSHA  string `json:"head_sha,omitempty"`
	Position int    `json:"position,omitempty"` // GitHub diff position, kept for verification
}

// LineKind is the role of a line inside a hunk.
type LineKind string

const (
	LineContext LineKind = "context"
	LineAdd     LineKind = "add"
	LineDel     LineKind = "del"
)

// DiffLine is one line of a hunk. OldNo/NewNo are 0 when the line does not
// exist on that side. Position is the GitHub diff position within the file:
// 1 is the first line after the first @@ header, and it keeps counting across
// hunks (each later @@ header takes a position too).
type DiffLine struct {
	Kind      LineKind `json:"kind"`
	OldNo     int      `json:"old_no,omitempty"`
	NewNo     int      `json:"new_no,omitempty"`
	Text      string   `json:"text"` // without the leading +, - or space
	Position  int      `json:"position"`
	NoNewline bool     `json:"no_newline,omitempty"` // followed by "\ No newline at end of file"
}

// DiffHunk is one @@ block.
type DiffHunk struct {
	ID       string     `json:"id"`
	OldStart int        `json:"old_start"`
	OldLines int        `json:"old_lines"`
	NewStart int        `json:"new_start"`
	NewLines int        `json:"new_lines"`
	Section  string     `json:"section,omitempty"` // text after the closing @@, usually a function name
	Lines    []DiffLine `json:"lines"`
}

// FileStatus is what happened to a file in the diff.
type FileStatus string

const (
	FileAdded    FileStatus = "added"
	FileModified FileStatus = "modified"
	FileDeleted  FileStatus = "deleted"
	FileRenamed  FileStatus = "renamed"
	FileCopied   FileStatus = "copied"
)

// ChangedFile is one file of a diff. Header holds the raw extended header
// lines ("diff --git", "index", mode and rename lines, "---", "+++") so the
// parser can re-emit the exact text it read.
type ChangedFile struct {
	ID         string     `json:"id"`
	OldPath    string     `json:"old_path,omitempty"`
	NewPath    string     `json:"new_path,omitempty"`
	Status     FileStatus `json:"status"`
	Similarity int        `json:"similarity,omitempty"` // rename/copy similarity, percent
	Binary     bool       `json:"binary,omitempty"`
	OldMode    string     `json:"old_mode,omitempty"`
	NewMode    string     `json:"new_mode,omitempty"`
	Language   string     `json:"language,omitempty"`
	Additions  int        `json:"additions"`
	Deletions  int        `json:"deletions"`
	Header     []string   `json:"header,omitempty"`
	Hunks      []DiffHunk `json:"hunks"`
}

// Path is the path a reader knows the file by: the new path, or the old one
// for a deletion.
func (f ChangedFile) Path() string {
	if f.NewPath != "" && f.Status != FileDeleted {
		return f.NewPath
	}
	return f.OldPath
}

// SourceKind says what a session reviews.
type SourceKind string

const (
	SourceWorktree SourceKind = "worktree" // uncommitted changes against HEAD
	SourceRange    SourceKind = "range"    // A..B or A...B
	SourceCommit   SourceKind = "commit"   // one commit against its parent
	SourcePatch    SourceKind = "patch"    // a patch file
	SourcePR       SourceKind = "pr"       // a pull request on a host
)

// PRRef names a pull request on a host.
type PRRef struct {
	Host   string `json:"host,omitempty"` // "github.com"
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
}

// Source is where the diff of a session comes from.
type Source struct {
	Kind      SourceKind `json:"kind"`
	RepoPath  string     `json:"repo_path,omitempty"` // absolute path of the local clone
	Ref       string     `json:"ref,omitempty"`       // as typed: "main...feature", a sha, …
	BaseSHA   string     `json:"base_sha,omitempty"`
	HeadSHA   string     `json:"head_sha,omitempty"`
	PatchPath string     `json:"patch_path,omitempty"`
	PR        *PRRef     `json:"pr,omitempty"`
}

// HunkStatus is the review status of one hunk.
type HunkStatus string

const (
	HunkUnreviewed    HunkStatus = "unreviewed"
	HunkReviewed      HunkStatus = "reviewed"
	HunkStale         HunkStatus = "stale"           // the hunk id disappeared from the diff
	HunkNeedsReReview HunkStatus = "needs-re-review" // same place, different content
)

// ReviewState is the reviewed mark on one hunk. Body is the hunk body at the
// time it was marked, so a later version can be diffed against what was read.
type ReviewState struct {
	HunkID       string     `json:"hunk_id"`
	Path         string     `json:"path"`
	Status       HunkStatus `json:"status"`
	Body         string     `json:"body,omitempty"`
	ReviewedAt   time.Time  `json:"reviewed_at,omitzero"`
	ReviewedHead string     `json:"reviewed_head,omitempty"`
}

// CommentStatus is where a draft is on its way to the host.
type CommentStatus string

const (
	CommentDraftStatus CommentStatus = "draft"
	CommentPublished   CommentStatus = "published"
	CommentFailed      CommentStatus = "failed"
	CommentStale       CommentStatus = "stale" // its anchor no longer maps
)

// CommentDraft is a comment the reviewer is writing. A nil Anchor makes it a
// general comment on the whole review.
type CommentDraft struct {
	ID          string        `json:"id"`
	Anchor      *Anchor       `json:"anchor,omitempty"`
	Body        string        `json:"body"`
	Status      CommentStatus `json:"status"`
	Publishable bool          `json:"publishable"` // false for lines outside the host's diff
	AIAssisted  bool          `json:"ai_assisted,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	RemoteID    int64         `json:"remote_id,omitempty"`
	RemoteURL   string        `json:"remote_url,omitempty"`
	Error       string        `json:"error,omitempty"`
}

// ReviewSession is one review of one diff, persisted between runs.
type ReviewSession struct {
	ID        string                  `json:"id"`
	RepoKey   string                  `json:"repo_key"`
	Title     string                  `json:"title"`
	Source    Source                  `json:"source"`
	CreatedAt time.Time               `json:"created_at"`
	UpdatedAt time.Time               `json:"updated_at"`
	States    map[string]*ReviewState `json:"states,omitempty"` // by hunk id
	Comments  []CommentDraft          `json:"comments,omitempty"`
}

// Range is a span of lines (1-based, inclusive). Columns are optional.
type Range struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
	StartCol  int `json:"start_col,omitempty"`
	EndCol    int `json:"end_col,omitempty"`
}

// Location is a point in a file.
type Location struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Col  int    `json:"col,omitempty"`
}

// SymbolKind classifies a CodeSymbol.
type SymbolKind string

const (
	SymFunc      SymbolKind = "func"
	SymMethod    SymbolKind = "method"
	SymType      SymbolKind = "type"
	SymInterface SymbolKind = "interface"
	SymClass     SymbolKind = "class"
	SymScenario  SymbolKind = "scenario"
	SymStep      SymbolKind = "step"
	SymStepDef   SymbolKind = "stepdef"
)

// CodeSymbol is a named piece of code. ID is unique within a repo: for Go
// "pkgpath.Name" or "pkgpath.(Recv).Name"; for the structural tier
// "path#Name" or "path#Class.Name".
type CodeSymbol struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Kind      SymbolKind `json:"kind"`
	Language  string     `json:"language"`
	Package   string     `json:"package,omitempty"`
	Receiver  string     `json:"receiver,omitempty"`
	Path      string     `json:"path"`
	Range     Range      `json:"range"`
	Signature string     `json:"signature,omitempty"`
	Exported  bool       `json:"exported,omitempty"`
	Changed   bool       `json:"changed,omitempty"`
}

// EdgeKind classifies a NavigationEdge.
type EdgeKind string

const (
	EdgeCalls      EdgeKind = "calls"
	EdgeImplements EdgeKind = "implements"
	EdgeReferences EdgeKind = "references"
	EdgeStepDef    EdgeKind = "step-def" // a Gherkin step to the code that defines it
)

// Confidence says how far an edge can be trusted.
type Confidence string

const (
	ConfStatic            Confidence = "static"
	ConfInterfaceDispatch Confidence = "interface-dispatch"
	ConfHeuristic         Confidence = "heuristic"
)

// NavigationEdge links two symbols. Source names what produced it ("vta",
// "cha", "types", "treesitter", "name-search", "gherkin").
type NavigationEdge struct {
	Kind       EdgeKind   `json:"kind"`
	From       string     `json:"from"`
	To         string     `json:"to"`
	Confidence Confidence `json:"confidence"`
	Source     string     `json:"source"`
	Site       *Location  `json:"site,omitempty"`
}

// Evidence is a quoted line backing a finding. It is verified against the
// file before the finding is shown.
type Evidence struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Excerpt string `json:"excerpt"`
}

// AgentFinding is one issue raised by an agentic reviewer.
type AgentFinding struct {
	ID               string     `json:"id"`
	Severity         string     `json:"severity"` // "info" | "low" | "medium" | "high"
	Title            string     `json:"title"`
	Explanation      string     `json:"explanation"`
	Evidence         []Evidence `json:"evidence"`
	Confidence       float64    `json:"confidence"`
	Verification     string     `json:"verification,omitempty"` // how the evidence was checked
	SuggestedComment string     `json:"suggested_comment,omitempty"`
	Anchor           *Anchor    `json:"anchor,omitempty"`
	Provider         string     `json:"provider,omitempty"`
}

// SnippetSummary is the structured summary of one hunk.
type SnippetSummary struct {
	Key           string    `json:"key"`
	HunkID        string    `json:"hunk_id"`
	ModelID       string    `json:"model_id"`
	PromptVersion string    `json:"prompt_version"`
	Kind          string    `json:"kind"` // behavior | refactor | rename | test | config
	What          string    `json:"what"`
	InferredWhy   string    `json:"inferred_why,omitempty"`
	Impact        string    `json:"impact,omitempty"`
	RiskFlags     []string  `json:"risk_flags,omitempty"`
	Confidence    float64   `json:"confidence"`
	Fallback      bool      `json:"fallback,omitempty"` // deterministic text, not from a model
	CreatedAt     time.Time `json:"created_at"`
}

// ModelConfig is the user's model choice.
type ModelConfig struct {
	Enabled  bool   `json:"enabled"`
	Model    string `json:"model,omitempty"` // catalog name
	Path     string `json:"path,omitempty"`  // gguf on disk
	Tier     string `json:"tier,omitempty"`
	CtxSize  int    `json:"ctx_size,omitempty"`
	Parallel int    `json:"parallel,omitempty"`
}
