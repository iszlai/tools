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

func TestParseArgsFlagsBeforeOrAfterPositional(t *testing.T) {
	cases := [][]string{
		{"--no-open", "diff", "main..feature"},
		{"diff", "main..feature", "--no-open"},
		{"diff", "--no-open", "main..feature"},
	}
	for _, args := range cases {
		opts, err := parseArgs(args)
		if err != nil {
			t.Fatalf("parseArgs(%v): %v", args, err)
		}
		if opts.source.Kind != model.SourceRange || opts.source.Ref != "main..feature" || !opts.noOpen {
			t.Errorf("parseArgs(%v) = %+v, want Range main..feature with noOpen", args, opts)
		}
	}
}

func TestParseArgsPRSubcommand(t *testing.T) {
	opts, err := parseArgs([]string{"pr", "owner/repo#42"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if opts.prRaw != "owner/repo#42" {
		t.Errorf("prRaw = %q, want %q", opts.prRaw, "owner/repo#42")
	}
	if opts.source.Kind != "" {
		t.Errorf("source.Kind = %q, want empty until run() resolves prRaw", opts.source.Kind)
	}
}

func TestParseArgsPRSubcommandMissingArg(t *testing.T) {
	if _, err := parseArgs([]string{"pr"}); err == nil {
		t.Fatal("want an error for 'pr' with no reference")
	}
}

func TestParseArgsPatchSubcommandMatchesFlagForm(t *testing.T) {
	bySubcommand, err := parseArgs([]string{"patch", "change.diff"})
	if err != nil {
		t.Fatalf("parseArgs(patch subcommand): %v", err)
	}
	byFlag, err := parseArgs([]string{"--patch", "change.diff"})
	if err != nil {
		t.Fatalf("parseArgs(--patch flag): %v", err)
	}
	if bySubcommand.source != byFlag.source {
		t.Errorf("source via subcommand = %+v, via flag = %+v, want equal", bySubcommand.source, byFlag.source)
	}
}

func TestParseArgsUnknownFlag(t *testing.T) {
	if _, err := parseArgs([]string{"--bogus"}); err == nil {
		t.Fatal("want an error for an unrecognized flag")
	}
}

func TestParseArgsPortEqualsForm(t *testing.T) {
	opts, err := parseArgs([]string{"--port=4242"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if opts.port != 4242 {
		t.Errorf("port = %d, want 4242", opts.port)
	}
}

func TestParseArgsBrowserFlag(t *testing.T) {
	opts, err := parseArgs([]string{"--browser"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !opts.browser {
		t.Error("browser = false, want true")
	}
}
