package main

import (
	"errors"
	"os"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/host/github"
)

// codedError pairs an error with the process exit code it should produce.
// README.md documents the codes: 0 ok, 1 runtime error, 2 usage error, 3 not
// a git repo / source not found, 4 auth or host error.
type codedError struct {
	code int
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

func usageErr(err error) error    { return &codedError{2, err} }
func notFoundErr(err error) error { return &codedError{3, err} }
func hostErr(err error) error     { return &codedError{4, err} }

// exitCode maps run's returned error to a process exit code: a *codedError
// carries its own, anything else is an unclassified runtime error (1), and
// nil is success.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ce *codedError
	if errors.As(err, &ce) {
		return ce.code
	}
	return 1
}

// classifyOpenErr turns an error from review.Open into a codedError where the
// underlying cause is specific enough to pick a code better than the default
// runtime-error 1: a GitHub 404 or a missing git repository mean the source
// named on the command line was not found (3); a GitHub 401/403 or any other
// github package error (every one of which prefixes its message with
// "github:") means the request never got a straight answer from the host (4).
func classifyOpenErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, github.ErrNotFound):
		return notFoundErr(err)
	case errors.Is(err, github.ErrUnauthorized), errors.Is(err, github.ErrForbidden):
		return hostErr(err)
	case errors.Is(err, review.ErrPRNotSupported):
		return hostErr(err)
	case errors.Is(err, os.ErrNotExist):
		return notFoundErr(err)
	case strings.Contains(err.Error(), "not a git repository"):
		return notFoundErr(err)
	case strings.Contains(err.Error(), "github:"):
		return hostErr(err)
	default:
		return err
	}
}
