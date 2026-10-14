package github

import (
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func TestParsePRRef(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    model.PRRef
		wantErr bool
	}{
		{
			name: "https URL",
			in:   "https://github.com/o/r/pull/123",
			want: model.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 123, URL: "https://github.com/o/r/pull/123"},
		},
		{
			name: "https URL with trailing slash and segment",
			in:   "https://github.com/o/r/pull/123/files",
			want: model.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 123, URL: "https://github.com/o/r/pull/123/files"},
		},
		{
			name: "http URL, enterprise host",
			in:   "http://github.example.com/acme/widgets/pull/7",
			want: model.PRRef{Host: "github.example.com", Owner: "acme", Repo: "widgets", Number: 7, URL: "http://github.example.com/acme/widgets/pull/7"},
		},
		{
			name: "shorthand owner/repo#n",
			in:   "o/r#123",
			want: model.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 123},
		},
		{
			name: "shorthand with dots and dashes",
			in:   "my-org/my.repo#42",
			want: model.PRRef{Host: "github.com", Owner: "my-org", Repo: "my.repo", Number: 42},
		},
		{
			name:    "bare number needs ParsePRRefWithRepo",
			in:      "123",
			wantErr: true,
		},
		{
			name:    "bare number with hash needs ParsePRRefWithRepo",
			in:      "#123",
			wantErr: true,
		},
		{
			name:    "garbage",
			in:      "not a pr reference",
			wantErr: true,
		},
		{
			name:    "empty",
			in:      "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePRRef(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParsePRRef(%q) = %+v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePRRef(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParsePRRef(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParsePRRefWithRepo(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		originURL string
		want      model.PRRef
		wantErr   bool
	}{
		{
			name:      "bare number, https origin",
			in:        "123",
			originURL: "https://github.com/o/r.git",
			want:      model.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 123},
		},
		{
			name:      "bare number with hash, ssh origin",
			in:        "#123",
			originURL: "git@github.com:o/r.git",
			want:      model.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 123},
		},
		{
			name:      "bare number, ssh:// origin",
			in:        "123",
			originURL: "ssh://git@github.com/o/r.git",
			want:      model.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 123},
		},
		{
			name:      "full URL ignores originURL",
			in:        "https://github.com/x/y/pull/9",
			originURL: "git@github.com:o/r.git",
			want:      model.PRRef{Host: "github.com", Owner: "x", Repo: "y", Number: 9, URL: "https://github.com/x/y/pull/9"},
		},
		{
			name:      "shorthand ignores originURL",
			in:        "x/y#9",
			originURL: "git@github.com:o/r.git",
			want:      model.PRRef{Host: "github.com", Owner: "x", Repo: "y", Number: 9},
		},
		{
			name:      "unparseable origin",
			in:        "123",
			originURL: "not a remote url",
			wantErr:   true,
		},
		{
			name:    "garbage input, not a bare number either",
			in:      "garbage",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePRRefWithRepo(tt.in, tt.originURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParsePRRefWithRepo(%q, %q) = %+v, want error", tt.in, tt.originURL, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePRRefWithRepo(%q, %q): %v", tt.in, tt.originURL, err)
			}
			if got != tt.want {
				t.Errorf("ParsePRRefWithRepo(%q, %q) = %+v, want %+v", tt.in, tt.originURL, got, tt.want)
			}
		})
	}
}

func TestOwnerRepoFromRemote(t *testing.T) {
	tests := []struct {
		remote    string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{remote: "https://github.com/o/r.git", wantOwner: "o", wantRepo: "r"},
		{remote: "https://github.com/o/r", wantOwner: "o", wantRepo: "r"},
		{remote: "git@github.com:o/r.git", wantOwner: "o", wantRepo: "r"},
		{remote: "ssh://git@github.com/o/r.git", wantOwner: "o", wantRepo: "r"},
		{remote: "ssh://git@github.com/o/r", wantOwner: "o", wantRepo: "r"},
		{remote: "not-a-url", wantErr: true},
		{remote: "https://github.com/onlyowner", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.remote, func(t *testing.T) {
			owner, repo, err := ownerRepoFromRemote(tt.remote)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ownerRepoFromRemote(%q) = (%q, %q), want error", tt.remote, owner, repo)
				}
				return
			}
			if err != nil {
				t.Fatalf("ownerRepoFromRemote(%q): %v", tt.remote, err)
			}
			if owner != tt.wantOwner || repo != tt.wantRepo {
				t.Errorf("ownerRepoFromRemote(%q) = (%q, %q), want (%q, %q)", tt.remote, owner, repo, tt.wantOwner, tt.wantRepo)
			}
		})
	}
}
