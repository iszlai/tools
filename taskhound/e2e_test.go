package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// thBin is the compiled CLI. The end-to-end tests drive the real binary as a
// subprocess, because the things most likely to break — file locking between
// processes, flag parsing, the HTTP surface — cannot be observed in-process.
var thBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "taskhound-bin-")
	if err != nil {
		panic(err)
	}
	thBin = filepath.Join(dir, "th")
	build := exec.Command("go", "build", "-o", thBin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type cli struct {
	t   *testing.T
	dir string
}

func newCLI(t *testing.T) *cli {
	t.Helper()
	c := &cli{t: t, dir: t.TempDir()}
	c.run("init")
	return c
}

func (c *cli) try(args ...string) (string, error) {
	c.t.Helper()
	cmd := exec.Command(thBin, args...)
	cmd.Dir = c.dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("th %s: %w: %s", strings.Join(args, " "), err, errb.String())
	}
	return out.String(), nil
}

func (c *cli) run(args ...string) string {
	c.t.Helper()
	out, err := c.try(args...)
	if err != nil {
		c.t.Fatal(err)
	}
	return out
}

func (c *cli) add(title string, extra ...string) string {
	c.t.Helper()
	return strings.TrimSpace(c.run(append([]string{"add", title}, extra...)...))
}

func (c *cli) json(args ...string) []issueView {
	c.t.Helper()
	var out []issueView
	if err := json.Unmarshal([]byte(c.run(args...)), &out); err != nil {
		c.t.Fatalf("%v: %v", args, err)
	}
	return out
}

func ids(vs []issueView) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return out
}

// TestFullFlow walks the whole life of a small dependency chain: file it,
// discover what is startable, work it, comment on it, close it, and watch the
// frontier move.
func TestFullFlow(t *testing.T) {
	c := newCLI(t)

	schema := c.add("Add the ledger schema", "-d", "Tables and indexes.")
	api := c.add("Expose the ledger API", "--blocked-by", schema)
	ui := c.add("Ledger screen", "--blocked-by", api)
	if schema != "TH-1" || api != "TH-2" || ui != "TH-3" {
		t.Fatalf("unexpected ids: %s %s %s", schema, api, ui)
	}

	// Only the head of the chain can be started.
	if got := ids(c.json("next", "--json")); len(got) != 1 || got[0] != schema {
		t.Fatalf("next = %v, want [%s]", got, schema)
	}

	// Both directions of the question the user asked for.
	if got := ids(c.json("dependents", schema, "--json")); strings.Join(got, ",") != "TH-2,TH-3" {
		t.Fatalf("dependents(TH-1) = %v", got)
	}
	if got := ids(c.json("deps", ui, "--json")); strings.Join(got, ",") != "TH-2,TH-1" {
		t.Fatalf("deps(TH-3) = %v", got)
	}

	c.run("update", schema, "--status", "doing")
	c.run("comment", schema, "migration applied on staging")
	c.run("update", schema, "--status", "done")

	// Closing the head hands the frontier to the next slice.
	if got := ids(c.json("next", "--json")); len(got) != 1 || got[0] != api {
		t.Fatalf("after closing TH-1, next = %v, want [%s]", got, api)
	}

	var shown issueView
	if err := json.Unmarshal([]byte(c.run("show", schema, "--json")), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.Status != "done" || len(shown.Comments) != 1 {
		t.Fatalf("show TH-1 = %+v", shown)
	}
	if shown.Comments[0].Body != "migration applied on staging" {
		t.Fatalf("comment body = %q", shown.Comments[0].Body)
	}

	// Filters
	if got := ids(c.json("list", "--blocked", "--json")); strings.Join(got, ",") != "TH-3" {
		t.Fatalf("list --blocked = %v, want [TH-3]", got)
	}
	if got := ids(c.json("list", "--status", "done", "--json")); strings.Join(got, ",") != "TH-1" {
		t.Fatalf("list --status done = %v", got)
	}
}

func TestBlocksSugarAndCycleRefusal(t *testing.T) {
	c := newCLI(t)
	a := c.add("A")
	b := c.add("B", "--blocks", a) // B blocks A, i.e. A waits on B

	shown := c.json("list", "--json")
	var gotA issueView
	for _, v := range shown {
		if v.ID == a {
			gotA = v
		}
	}
	if strings.Join(gotA.BlockedBy, ",") != b {
		t.Fatalf("--blocks did not write the edge onto %s: %+v", a, gotA.BlockedBy)
	}
	if _, err := c.try("update", b, "--add-blocked-by", a); err == nil {
		t.Fatal("closing the cycle should have failed")
	}
	// The refused write must not have changed anything.
	var stillB issueView
	json.Unmarshal([]byte(c.run("show", b, "--json")), &stillB)
	if len(stillB.BlockedBy) != 0 {
		t.Fatalf("%s kept a blocker from a refused write: %v", b, stillB.BlockedBy)
	}
}

func TestDescriptionFromStdin(t *testing.T) {
	c := newCLI(t)
	cmd := exec.Command(thBin, "add", "Piped", "-d", "-")
	cmd.Dir = c.dir
	cmd.Stdin = strings.NewReader("line one\nline two\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(string(out))
	var shown issueView
	json.Unmarshal([]byte(c.run("show", id, "--json")), &shown)
	if shown.Description != "line one\nline two" {
		t.Fatalf("description = %q", shown.Description)
	}
}

// TestConcurrentAdds is the point of the locking: many instances writing the
// same file at once must all land, with distinct ids and a file that still
// parses.
func TestConcurrentAdds(t *testing.T) {
	c := newCLI(t)
	const n = 24

	var wg sync.WaitGroup
	errs := make(chan error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if _, err := c.try("add", fmt.Sprintf("concurrent %d", i)); err != nil {
				errs <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	all := c.json("list", "--json")
	if len(all) != n {
		t.Fatalf("got %d issues, want %d — a write was lost", len(all), n)
	}
	seen := map[string]bool{}
	for _, v := range all {
		if seen[v.ID] {
			t.Fatalf("duplicate id %s", v.ID)
		}
		seen[v.ID] = true
	}
}

// TestConcurrentCommentsOnOneIssue exercises read-modify-write on the same
// record, where a lost update would silently drop a comment.
func TestConcurrentCommentsOnOneIssue(t *testing.T) {
	c := newCLI(t)
	id := c.add("Busy issue")
	const n = 20

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := c.try("comment", id, fmt.Sprintf("note %d", i)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	var shown issueView
	json.Unmarshal([]byte(c.run("show", id, "--json")), &shown)
	if len(shown.Comments) != n {
		t.Fatalf("got %d comments, want %d", len(shown.Comments), n)
	}
}

// ---------------------------------------------------------------------------
// UI
// ---------------------------------------------------------------------------

type server struct {
	url  string
	stop func()
}

var urlRE = regexp.MustCompile(`http://127\.0\.0\.1:\d+`)

func startUI(t *testing.T, c *cli) *server {
	t.Helper()
	cmd := exec.Command(thBin, "ui", "--port", "0")
	cmd.Dir = c.dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	url := urlRE.FindString(line)
	if url == "" {
		t.Fatalf("could not find the listen address in %q", line)
	}
	go io.Copy(io.Discard, stdout)
	return &server{url: url, stop: func() { cmd.Process.Kill(); cmd.Wait() }}
}

func (s *server) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, s.url+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// TestUIMatchesTheCLI drives the HTTP surface the board uses, then checks the
// CLI sees the same facts — the two must be one tool, not two.
func TestUIMatchesTheCLI(t *testing.T) {
	c := newCLI(t)
	first := c.add("Filed from the CLI")

	srv := startUI(t, c)
	defer srv.stop()

	// The page itself.
	res, err := http.Get(srv.url + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Contains(page, []byte("taskhound")) {
		t.Fatalf("GET / = %d", res.StatusCode)
	}

	// Create through the API, blocked by the CLI-filed issue.
	code, created := srv.do(t, "POST", "/api/issues", map[string]any{
		"title": "Filed from the UI", "description": "body", "blocked_by": []string{first},
	})
	if code != 201 {
		t.Fatalf("POST /api/issues = %d %v", code, created)
	}
	second, _ := created["id"].(string)
	if second == "" {
		t.Fatalf("no id in %v", created)
	}
	if ready, _ := created["ready"].(bool); ready {
		t.Error("a freshly blocked issue must not report ready")
	}

	// Drag to Doing, edit the text, comment.
	if code, out := srv.do(t, "PATCH", "/api/issues/"+second, map[string]any{"status": "doing"}); code != 200 {
		t.Fatalf("PATCH status = %d %v", code, out)
	}
	if code, out := srv.do(t, "PATCH", "/api/issues/"+second, map[string]any{"title": "Renamed in the UI"}); code != 200 {
		t.Fatalf("PATCH title = %d %v", code, out)
	}
	if code, out := srv.do(t, "POST", "/api/issues/"+second+"/comments", map[string]any{"body": "from the board"}); code != 201 {
		t.Fatalf("POST comment = %d %v", code, out)
	}

	// Bad input is refused rather than written.
	if code, _ := srv.do(t, "PATCH", "/api/issues/"+second, map[string]any{"status": "nope"}); code != 400 {
		t.Errorf("bad status returned %d, want 400", code)
	}
	if code, _ := srv.do(t, "PATCH", "/api/issues/"+first, map[string]any{"blocked_by": []string{second}}); code != 400 {
		t.Errorf("cycle through the API returned %d, want 400", code)
	}

	// Everything the API did is visible to the CLI.
	var shown issueView
	json.Unmarshal([]byte(c.run("show", second, "--json")), &shown)
	if shown.Title != "Renamed in the UI" || shown.Status != "doing" || len(shown.Comments) != 1 {
		t.Fatalf("CLI does not see the UI's writes: %+v", shown)
	}
	if strings.Join(shown.BlockedBy, ",") != first {
		t.Fatalf("blocked_by = %v", shown.BlockedBy)
	}

	// ...and the other way round.
	c.run("comment", second, "from the CLI")
	code, board := srv.do(t, "GET", "/api/board", nil)
	if code != 200 {
		t.Fatalf("GET /api/board = %d", code)
	}
	issues, _ := board["issues"].([]any)
	if len(issues) != 2 {
		t.Fatalf("board has %d issues, want 2", len(issues))
	}
}

func TestAgentGuideIsPrintable(t *testing.T) {
	c := newCLI(t)
	out := c.run("agent-guide")
	if strings.HasPrefix(out, "---") {
		t.Error("agent-guide should strip the skill front matter")
	}
	for _, want := range []string{"th next --json", "th dependents", "blocked_by"} {
		if !strings.Contains(out, want) {
			t.Errorf("agent-guide is missing %q", want)
		}
	}
}

func TestNoBoardIsAClearError(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(thBin, "list")
	cmd.Dir = dir
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err == nil {
		t.Fatal("list without a board should fail")
	}
	if !strings.Contains(errb.String(), "th init") {
		t.Errorf("error should point at `th init`, got %q", errb.String())
	}
}
