package main

import (
	"os"
	"path/filepath"

	"github.com/iszlai/tools/flowreview/internal/llm"
	"github.com/iszlai/tools/flowreview/internal/llm/models"
	"github.com/iszlai/tools/flowreview/internal/llm/sidecar"
	"github.com/iszlai/tools/flowreview/internal/llm/summary"
	"github.com/iszlai/tools/flowreview/internal/model"
	"github.com/iszlai/tools/flowreview/third_party/llama"
)

// buildSummaryPipeline wires FR-28's summary pipeline from the user's model
// config (dataRoot/config.json, FR-26): a sidecar.Lazy provider over the
// user's chosen model when summaries are enabled, that model is installed on
// disk, and this binary was built with the real llama-server embedded
// (-tags llama_embed); a nil provider (fallback-only summaries, see
// summary.Pipeline.run) in every other case, including a plain build without
// the archive. The sidecar itself is not started here -- Lazy starts it on
// the first summary request (PLAN.md §3), never at app launch.
//
// Model choice: the user's own pick (config.json's Model/Path) if set,
// otherwise the catalog's models.RecommendedFor this machine's RAM tier, if
// that entry happens to be installed. Nothing is auto-installed. With no
// config.json at all, summaries are on when the recommended model is
// installed (installing it was the user's opt-in); an explicit
// "enabled": false in config.json always wins.
// The returned stop func stops the sidecar if Lazy ever started one; it is
// always safe to call, including when no provider was wired up at all.
func buildSummaryPipeline(dataRoot string) (*summary.Pipeline, func()) {
	cache := summary.NewCache(filepath.Join(dataRoot, "cache", "summaries"))
	noop := func() {}

	cfg, err := models.LoadConfig(dataRoot)
	if err == nil && !configExists(dataRoot) {
		cfg.Enabled = true
	}
	if err != nil || !cfg.Enabled || !llama.Available() {
		return summary.NewPipeline(cache, nil, "", 0), noop
	}

	modelID, path := resolveModel(dataRoot, cfg)
	if path == "" {
		return summary.NewPipeline(cache, nil, "", 0), noop
	}
	if _, statErr := os.Stat(path); statErr != nil {
		return summary.NewPipeline(cache, nil, "", 0), noop
	}

	lazy := sidecar.NewLazy(sidecar.Options{
		DataRoot:  dataRoot,
		ModelPath: path,
		CtxSize:   cfg.CtxSize,
		Parallel:  cfg.Parallel,
	}, modelID)

	var provider llm.ModelProvider = lazy
	return summary.NewPipeline(cache, provider, modelID, 0), func() { lazy.Stop() }
}

// resolveModel picks the model to serve summaries with: the user's own
// choice if config.json names one, otherwise the catalog's recommendation
// for this machine's physical RAM, if that model is actually installed.
// Returns "", "" when neither resolves to a path.
func resolveModel(dataRoot string, cfg model.ModelConfig) (modelID, path string) {
	if cfg.Model != "" || cfg.Path != "" {
		modelID = cfg.Model
		path = cfg.Path
		if path == "" {
			path = filepath.Join(dataRoot, "models", cfg.Model+".gguf")
		}
		if modelID == "" {
			modelID = filepath.Base(path)
		}
		return modelID, path
	}

	ram, err := models.PhysicalRAMBytes()
	if err != nil {
		return "", ""
	}
	entry, ok := models.RecommendedFor(ram)
	if !ok {
		return "", ""
	}
	candidate := filepath.Join(dataRoot, "models", entry.Name+".gguf")
	if _, statErr := os.Stat(candidate); statErr != nil {
		return "", ""
	}
	return entry.Name, candidate
}

func configExists(dataRoot string) bool {
	_, err := os.Stat(filepath.Join(dataRoot, "config.json"))
	return err == nil
}
