// FR-16's config: extra entry-point patterns an .flowreview.yaml at the repo
// root can declare, on top of the built-in heuristics in entrypoints.go.
package golang

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// configFileName is the repo-root file LoadConfig reads.
const configFileName = ".flowreview.yaml"

// EntryPointPattern is one user-declared pattern. FuncRegex matches a
// func/method's bare name; Signature matches its rendered shape (see
// signatureShape) exactly. A pattern with both must match both; a pattern
// with neither never matches anything.
type EntryPointPattern struct {
	FuncRegex string `yaml:"func_regex,omitempty"`
	Signature string `yaml:"signature,omitempty"`
	Reason    string `yaml:"reason"`

	compiled *regexp.Regexp // compiled from FuncRegex by LoadConfig
}

// EntryPointsConfig is the "entrypoints:" section of .flowreview.yaml.
type EntryPointsConfig struct {
	Patterns []EntryPointPattern `yaml:"patterns,omitempty"`
}

// Config is this package's view of .flowreview.yaml. FR-35 will add a
// "checks:" section to the same file later; yaml.v3's Unmarshal ignores keys
// a struct does not declare, so Config only needs to describe the keys FR-16
// itself reads and tolerates whatever else is there, now or later.
//
// HasCallers is not part of the YAML file: it is wired in by the caller
// (FR-17, once FR-15's call graph exists) to enable the "exported, no
// in-module callers" heuristic in EntryPoints. nil skips that heuristic.
type Config struct {
	EntryPoints EntryPointsConfig          `yaml:"entrypoints,omitempty"`
	HasCallers  func(symbolID string) bool `yaml:"-"`
}

// LoadConfig reads root/.flowreview.yaml. A missing file is not an error: it
// returns the zero Config, which has no patterns — EntryPoints then runs
// only its built-in heuristics.
func LoadConfig(root string) (Config, error) {
	path := filepath.Join(root, configFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("golang: %s is not valid: %w", path, err)
	}
	for i, pat := range cfg.EntryPoints.Patterns {
		if pat.FuncRegex == "" {
			continue
		}
		re, err := regexp.Compile(pat.FuncRegex)
		if err != nil {
			return Config{}, fmt.Errorf("golang: %s: pattern %d: func_regex %q: %w", path, i, pat.FuncRegex, err)
		}
		cfg.EntryPoints.Patterns[i].compiled = re
	}
	return cfg, nil
}
