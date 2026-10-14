package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// configFileName is the file LoadConfig/SaveConfig read and write, directly
// under the data root (a sibling of sessions/, cache/ and models/; PLAN.md
// §3).
const configFileName = "config.json"

// LoadConfig reads the user's model choice from dataRoot/config.json. A
// missing file is not an error: it returns the zero ModelConfig, which has
// Enabled == false — "summaries off" is the state before anyone has chosen
// anything (PLAN.md §8: no silent default).
func LoadConfig(dataRoot string) (model.ModelConfig, error) {
	data, err := os.ReadFile(filepath.Join(dataRoot, configFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return model.ModelConfig{}, nil
		}
		return model.ModelConfig{}, err
	}
	var cfg model.ModelConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return model.ModelConfig{}, fmt.Errorf("models: config.json is not valid: %w", err)
	}
	return cfg, nil
}

// SaveConfig writes cfg to dataRoot/config.json, replacing any previous
// choice. The write is atomic (temp file + rename) so a crash mid-write
// never leaves a truncated config behind.
func SaveConfig(dataRoot string, cfg model.ModelConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dataRoot, configFileName)
	tmp, err := os.CreateTemp(dataRoot, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
