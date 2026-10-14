package main

import (
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func TestParseArgsDefaultIsWorktree(t *testing.T) {
	opts, err := parseArgs(nil)
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if opts.source.Kind != model.SourceWorktree {
		t.Errorf("source.Kind = %q, want worktree", opts.source.Kind)
	}
}

func TestParseArgsDiffRange(t *testing.T) {
	opts, err := parseArgs([]string{"diff", "main..feature"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if opts.source.Kind != model.SourceRange || opts.source.Ref != "main..feature" {
		t.Errorf("source = %+v, want Range main..feature", opts.source)
	}
}

func TestParseArgsDiffSingleRefIsACommit(t *testing.T) {
	opts, err := parseArgs([]string{"diff", "abc1234"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if opts.source.Kind != model.SourceCommit || opts.source.Ref != "abc1234" {
		t.Errorf("source = %+v, want Commit abc1234", opts.source)
	}
}

func TestParseArgsDiffMissingRef(t *testing.T) {
	if _, err := parseArgs([]string{"diff"}); err == nil {
		t.Fatal("want an error for 'diff' with no ref")
	}
}

func TestParseArgsPatch(t *testing.T) {
	opts, err := parseArgs([]string{"--patch", "change.diff"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if opts.source.Kind != model.SourcePatch || opts.source.PatchPath != "change.diff" {
		t.Errorf("source = %+v, want Patch change.diff", opts.source)
	}
}

func TestParseArgsPatchWithDiffIsAnError(t *testing.T) {
	if _, err := parseArgs([]string{"diff", "main..feature", "--patch", "x.diff"}); err == nil {
		t.Fatal("want an error combining --patch with diff")
	}
}

func TestParseArgsFlags(t *testing.T) {
	opts, err := parseArgs([]string{"--port", "4242", "--no-open"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if opts.port != 4242 || !opts.noOpen || opts.browser {
		t.Errorf("opts = %+v, want port=4242 noOpen=true browser=false", opts)
	}
}

func TestParseArgsRejectsTrailingGarbage(t *testing.T) {
	if _, err := parseArgs([]string{"--no-open", "extra"}); err == nil {
		t.Fatal("want an error for an unexpected trailing argument")
	}
}
