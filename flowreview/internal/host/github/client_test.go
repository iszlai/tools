package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNew_tokenPrecedence(t *testing.T) {
	// gh must never be consulted when any other source provides a token.
	t.Run("Options.Token wins over everything", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "env-github-token")
		t.Setenv("GH_TOKEN", "env-gh-token")
		stubGHFailure(t)

		c, err := New(Options{Token: "explicit-token"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if c.token != "explicit-token" {
			t.Errorf("token = %q, want %q", c.token, "explicit-token")
		}
	})

	t.Run("GITHUB_TOKEN wins over GH_TOKEN and gh", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "env-github-token")
		t.Setenv("GH_TOKEN", "env-gh-token")
		stubGHFailure(t)

		c, err := New(Options{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if c.token != "env-github-token" {
			t.Errorf("token = %q, want %q", c.token, "env-github-token")
		}
	})

	t.Run("GH_TOKEN used when GITHUB_TOKEN is unset", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "")
		t.Setenv("GH_TOKEN", "env-gh-token")
		stubGHFailure(t)

		c, err := New(Options{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if c.token != "env-gh-token" {
			t.Errorf("token = %q, want %q", c.token, "env-gh-token")
		}
	})

	t.Run("falls back to gh auth token", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "")
		t.Setenv("GH_TOKEN", "")
		restore := lookPath
		lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
		t.Cleanup(func() { lookPath = restore })
		restoreRun := runGHAuthToken
		runGHAuthToken = func(ctx context.Context) (string, error) { return "gh-cli-token", nil }
		t.Cleanup(func() { runGHAuthToken = restoreRun })

		c, err := New(Options{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if c.token != "gh-cli-token" {
			t.Errorf("token = %q, want %q", c.token, "gh-cli-token")
		}
	})
}

func TestNew_ghMissing(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	restore := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = restore })

	_, err := New(Options{})
	if err == nil {
		t.Fatal("New: want error when gh is missing and no token is set")
	}
	if !strings.Contains(err.Error(), "install") || !strings.Contains(err.Error(), "cli.github.com") {
		t.Errorf("error %q should hint at installing gh", err.Error())
	}
}

func TestNew_ghNotLoggedIn(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	restore := lookPath
	lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	t.Cleanup(func() { lookPath = restore })
	restoreRun := runGHAuthToken
	runGHAuthToken = func(ctx context.Context) (string, error) {
		return "", errors.New("gh auth token: To get started with GitHub CLI, please run: gh auth login")
	}
	t.Cleanup(func() { runGHAuthToken = restoreRun })

	_, err := New(Options{})
	if err == nil {
		t.Fatal("New: want error when gh is not logged in")
	}
	if !strings.Contains(err.Error(), "gh auth login") {
		t.Errorf("error %q should hint at `gh auth login`", err.Error())
	}
}

func TestNew_defaults(t *testing.T) {
	c, err := New(Options{Token: "t"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, defaultBaseURL)
	}
	if c.http != http.DefaultClient {
		t.Error("http client should default to http.DefaultClient")
	}
}

func TestNew_baseURLFromEnv(t *testing.T) {
	t.Setenv("FLOWREVIEW_GITHUB_API", "https://example.invalid/api")

	c, err := New(Options{Token: "t"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.baseURL != "https://example.invalid/api" {
		t.Errorf("baseURL = %q, want the FLOWREVIEW_GITHUB_API value", c.baseURL)
	}
}

func TestNew_explicitBaseURLWinsOverEnv(t *testing.T) {
	t.Setenv("FLOWREVIEW_GITHUB_API", "https://example.invalid/api")

	c, err := New(Options{Token: "t", BaseURL: "https://explicit.invalid"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.baseURL != "https://explicit.invalid" {
		t.Errorf("baseURL = %q, want the explicit Options.BaseURL", c.baseURL)
	}
}

func TestNew_customBaseURLAndHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	custom := &http.Client{}
	c, err := New(Options{Token: "t", BaseURL: srv.URL + "/", HTTP: custom})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.baseURL != srv.URL {
		t.Errorf("baseURL = %q, want trailing slash trimmed to %q", c.baseURL, srv.URL)
	}
	if c.http != custom {
		t.Error("http client should be the one passed in Options")
	}
}

func stubGHFailure(t *testing.T) {
	t.Helper()
	restore := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("gh must not be consulted here") }
	t.Cleanup(func() { lookPath = restore })
}
