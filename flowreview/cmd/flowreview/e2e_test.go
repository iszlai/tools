package main

// End-to-end tests for FR-12: they build the real flowreview binary and run
// it as a subprocess against fixture git repositories, exercising the CLI
// surface the way a user actually invokes it (not by calling Go functions in
// process). No test here touches a real network or a real GitHub repo; the
// PR path talks to an httptest fake and a local bare "origin".

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var binPath string

func TestMain(m *testing.M) {
	os.Exit(runE2ETests(m))
}

// runE2ETests builds the binary once into a scratch temp dir (not t.TempDir:
// TestMain has no *testing.T) and removes it on the way out, win or lose.
func runE2ETests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "flowreview-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)

	binPath = filepath.Join(dir, "flowreview")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build flowreview: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

func runCLI(t *testing.T, dir string, env []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	err := cmd.Run()
	if err == nil {
		return outBuf.String(), errBuf.String(), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return outBuf.String(), errBuf.String(), exitErr.ExitCode()
	}
	t.Fatalf("run %v: %v", args, err)
	return "", "", -1
}

// startNoOpen launches the binary with --no-open appended, reads the URL it
// prints on its first stdout line, and arranges for the process to be killed
// at test cleanup. It returns the base URL with no trailing slash.
func startNoOpen(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	fullArgs := append(append([]string{}, args...), "--no-open")
	cmd := exec.Command(binPath, fullArgs...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
	})

	line, err := readLineWithTimeout(bufio.NewReader(stdout), 10*time.Second)
	if err != nil {
		t.Fatalf("read URL line for %v: %v (stderr: %s)", args, err, stderrBuf.String())
	}
	return strings.TrimRight(strings.TrimSpace(line), "/")
}

func readLineWithTimeout(r *bufio.Reader, timeout time.Duration) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := r.ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			return "", res.err
		}
		return res.line, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("timed out after %s", timeout)
	}
}

func httpGetJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("GET %s: decode: %v", url, err)
		}
	}
}

func runGitE2E(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeFileE2E(t *testing.T, dir, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitE2E(t *testing.T, dir, when, msg string) string {
	t.Helper()
	env := []string{
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com", "GIT_AUTHOR_DATE=" + when,
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com", "GIT_COMMITTER_DATE=" + when,
	}
	runGitE2E(t, dir, env, "-c", "commit.gpgsign=false", "commit", "-q", "-m", msg)
	return strings.TrimSpace(runGitE2E(t, dir, nil, "rev-parse", "HEAD"))
}

// newTwoCommitFixture builds a repo with two commits on main, for `diff
// A..B`.
func newTwoCommitFixture(t *testing.T) (dir, rootSHA, modifySHA string) {
	t.Helper()
	dir = t.TempDir()
	runGitE2E(t, dir, nil, "init", "-q", "-b", "main", ".")
	runGitE2E(t, dir, nil, "config", "user.name", "Fixture")
	runGitE2E(t, dir, nil, "config", "user.email", "fixture@example.com")

	writeFileE2E(t, dir, "a.txt", []byte("line one\nline two\n"))
	runGitE2E(t, dir, nil, "add", "a.txt")
	rootSHA = commitE2E(t, dir, "2020-01-01T00:00:00Z", "root: add a.txt")

	writeFileE2E(t, dir, "a.txt", []byte("line one\nline TWO\n"))
	runGitE2E(t, dir, nil, "add", "a.txt")
	modifySHA = commitE2E(t, dir, "2020-01-01T00:01:00Z", "modify: edit a.txt")
	return dir, rootSHA, modifySHA
}

// newWorktreeFixture is a repo with one commit and a dirty working tree, so a
// worktree review has exactly one hunk to show.
func newWorktreeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitE2E(t, dir, nil, "init", "-q", "-b", "main", ".")
	runGitE2E(t, dir, nil, "config", "user.name", "Fixture")
	runGitE2E(t, dir, nil, "config", "user.email", "fixture@example.com")

	writeFileE2E(t, dir, "a.txt", []byte("line one\nline two\n"))
	runGitE2E(t, dir, nil, "add", "a.txt")
	commitE2E(t, dir, "2020-01-01T00:00:00Z", "root: add a.txt")

	writeFileE2E(t, dir, "a.txt", []byte("line one\nline THREE\n"))
	return dir
}

func TestE2E_Version(t *testing.T) {
	stdout, _, code := runCLI(t, t.TempDir(), nil, "version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), "flowreview ") {
		t.Errorf("stdout = %q, want it to start with %q", stdout, "flowreview ")
	}
}

func TestE2E_Help(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}} {
		stdout, _, code := runCLI(t, t.TempDir(), nil, args...)
		if code != 0 {
			t.Fatalf("flowreview %v: exit code = %d, want 0", args, code)
		}
		if !strings.Contains(stdout, "exit codes:") {
			t.Errorf("flowreview %v: stdout = %q, want the exit codes section", args, stdout)
		}
	}
}

func TestE2E_UsageErrorsExitTwo(t *testing.T) {
	cases := [][]string{
		{"diff"},         // missing ref
		{"--bogus-flag"}, // unknown flag
		{"pr"},           // missing PR reference
	}
	for _, args := range cases {
		_, stderr, code := runCLI(t, t.TempDir(), []string{"FLOWREVIEW_HOME=" + t.TempDir()}, args...)
		if code != 2 {
			t.Errorf("flowreview %v: exit code = %d, want 2; stderr: %s", args, code, stderr)
		}
	}
}

func TestE2E_NotAGitRepoExitsThree(t *testing.T) {
	notARepo := t.TempDir()
	_, stderr, code := runCLI(t, notARepo, []string{"FLOWREVIEW_HOME=" + t.TempDir()})
	if code != 3 {
		t.Fatalf("exit code = %d, want 3; stderr: %s", code, stderr)
	}
}

func TestE2E_WorktreeOpenAndSessions(t *testing.T) {
	dir := newWorktreeFixture(t)
	home := t.TempDir()
	env := []string{"FLOWREVIEW_HOME=" + home}

	url := startNoOpen(t, dir, env)

	var health map[string]bool
	httpGetJSON(t, url+"/api/health", &health)
	if !health["ok"] {
		t.Fatalf("health = %+v, want ok:true", health)
	}

	var sess struct {
		ID     string `json:"id"`
		Source struct {
			Kind string `json:"kind"`
		} `json:"source"`
	}
	httpGetJSON(t, url+"/api/session", &sess)
	if sess.ID != "worktree" || sess.Source.Kind != "worktree" {
		t.Fatalf("session = %+v, want the worktree session", sess)
	}

	stdout, stderr, code := runCLI(t, dir, env, "sessions", "--json")
	if code != 0 {
		t.Fatalf("sessions --json: exit = %d, stderr: %s", code, stderr)
	}
	var views []sessionListView
	if err := json.Unmarshal([]byte(stdout), &views); err != nil {
		t.Fatalf("decode sessions --json: %v\noutput: %s", err, stdout)
	}
	if len(views) != 1 || views[0].ID != "worktree" {
		t.Fatalf("views = %+v, want exactly the worktree session", views)
	}
	if views[0].TotalHunks == nil || *views[0].TotalHunks != 1 {
		t.Errorf("TotalHunks = %v, want a known total of 1", views[0].TotalHunks)
	}
}

func TestE2E_DiffRange(t *testing.T) {
	dir, rootSHA, modifySHA := newTwoCommitFixture(t)
	env := []string{"FLOWREVIEW_HOME=" + t.TempDir()}

	url := startNoOpen(t, dir, env, "diff", rootSHA+".."+modifySHA)

	var sess struct {
		Source struct {
			Kind string `json:"kind"`
			Ref  string `json:"ref"`
		} `json:"source"`
	}
	httpGetJSON(t, url+"/api/session", &sess)
	if sess.Source.Kind != "range" || sess.Source.Ref != rootSHA+".."+modifySHA {
		t.Fatalf("session.source = %+v, want the range just opened", sess.Source)
	}

	var files []struct {
		Path string `json:"path"`
	}
	httpGetJSON(t, url+"/api/files", &files)
	if len(files) != 1 || files[0].Path != "a.txt" {
		t.Fatalf("files = %+v, want one changed file a.txt", files)
	}
}

func TestE2E_ModelAndToolchainSubcommands(t *testing.T) {
	env := []string{"FLOWREVIEW_HOME=" + t.TempDir()}

	if _, stderr, code := runCLI(t, t.TempDir(), env, "model", "list"); code != 0 {
		t.Errorf("model list: exit = %d, stderr: %s", code, stderr)
	}

	stdout, stderr, code := runCLI(t, t.TempDir(), env, "toolchain", "list")
	if code != 0 {
		t.Fatalf("toolchain list: exit = %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "no toolchains installed") {
		t.Errorf("toolchain list stdout = %q, want %q", stdout, "no toolchains installed")
	}

	if _, _, code := runCLI(t, t.TempDir(), env, "toolchain", "rm", "9.9.9"); code != 1 {
		t.Errorf("toolchain rm (nonexistent): exit = %d, want 1", code)
	}
}

// newPRFixture builds a small repo whose "origin" remote is a `file://` URL
// pointing at a local bare repository, laid out under a "o/r" directory pair
// so the URL's last two path segments are literally "o" and "r" -- exactly
// what ownerRepoFromRemote needs to recognize this as owning PRs for
// "o/r", letting ResolveClone's local-origin check reuse this repo instead of
// shelling out to `gh repo clone`. (`git remote get-url` always expands
// `url.<x>.insteadOf` rewriting, so that approach cannot be used to keep a
// GitHub-shaped URL while secretly fetching from a local path; a file://
// remote avoids the problem by being both the literal and the real URL.) The
// real `git fetch` flowreview runs against this origin therefore works
// completely offline, exactly as it would against the real GitHub remote.
func newPRFixture(t *testing.T) (dir string, baseSHA, headSHA string) {
	t.Helper()
	bareDir := filepath.Join(t.TempDir(), "o", "r")
	if err := os.MkdirAll(bareDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitE2E(t, bareDir, nil, "init", "-q", "--bare", "-b", "main", ".")

	dir = t.TempDir()
	runGitE2E(t, dir, nil, "init", "-q", "-b", "main", ".")
	runGitE2E(t, dir, nil, "config", "user.name", "Fixture")
	runGitE2E(t, dir, nil, "config", "user.email", "fixture@example.com")
	runGitE2E(t, dir, nil, "remote", "add", "origin", "file://"+bareDir)

	writeFileE2E(t, dir, "a.txt", []byte("line one\nline two\n"))
	runGitE2E(t, dir, nil, "add", "a.txt")
	baseSHA = commitE2E(t, dir, "2021-01-01T00:00:00Z", "root: add a.txt")
	runGitE2E(t, dir, nil, "push", "-q", "origin", "main")

	runGitE2E(t, dir, nil, "checkout", "-q", "-b", "pr-branch")
	writeFileE2E(t, dir, "a.txt", []byte("line one\nline TWO\n"))
	runGitE2E(t, dir, nil, "add", "a.txt")
	headSHA = commitE2E(t, dir, "2021-01-01T00:01:00Z", "pr: edit a.txt")
	runGitE2E(t, dir, nil, "push", "-q", "origin", "pr-branch:refs/pull/1/head")
	runGitE2E(t, dir, nil, "checkout", "-q", "main")

	return dir, baseSHA, headSHA
}

func TestE2E_PRPath(t *testing.T) {
	dir, baseSHA, headSHA := newPRFixture(t)
	diffBytes := []byte(runGitE2E(t, dir, nil, "diff", baseSHA, headSHA))

	prJSON, err := json.Marshal(map[string]any{
		"number": 1, "title": "Fixture PR title", "body": "", "state": "open",
		"merged": false, "draft": false, "html_url": "https://github.com/o/r/pull/1",
		"user": map[string]any{"login": "fixture"},
		"base": map[string]any{"ref": "main", "sha": baseSHA},
		"head": map[string]any{
			"ref": "pr-branch", "sha": headSHA,
			"repo": map[string]any{"clone_url": "https://github.com/o/r.git"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "application/vnd.github.v3.diff" {
			w.Write(diffBytes)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(prJSON)
	})
	mux.HandleFunc("/repos/o/r/pulls/1/comments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/repos/o/r/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	env := []string{
		"FLOWREVIEW_HOME=" + t.TempDir(),
		"FLOWREVIEW_GITHUB_API=" + ts.URL,
		"GITHUB_TOKEN=test-token",
	}

	url := startNoOpen(t, dir, env, "pr", "o/r#1")

	var sess struct {
		Title  string `json:"title"`
		Source struct {
			Kind string `json:"kind"`
		} `json:"source"`
	}
	httpGetJSON(t, url+"/api/session", &sess)
	if sess.Title != "Fixture PR title" {
		t.Errorf("session title = %q, want the PR title from the fake host", sess.Title)
	}
	if sess.Source.Kind != "pr" {
		t.Errorf("session.source.kind = %q, want pr", sess.Source.Kind)
	}

	var remote []map[string]any
	httpGetJSON(t, url+"/api/remote-comments", &remote)
	if len(remote) != 0 {
		t.Errorf("remote comments = %+v, want none (the fake host has none)", remote)
	}

	var files []struct {
		Path string `json:"path"`
	}
	httpGetJSON(t, url+"/api/files", &files)
	if len(files) != 1 || files[0].Path != "a.txt" {
		t.Fatalf("files = %+v, want one changed file a.txt", files)
	}
}
