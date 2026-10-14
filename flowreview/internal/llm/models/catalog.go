// Package models is flowreview's local-model catalog and manager: which
// gguf files are available, which RAM tier they need, and how they get onto
// disk. It knows nothing about running a model (see internal/llm/sidecar) or
// about summaries (FR-28) — just the catalog, the download, and the user's
// choice.
package models

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Entry is one model in the catalog. URL, SHA256 and Size describe the exact
// GGUF file fetched from Hugging Face; EvalScore and Recommended are 0/false
// until FR-27 runs the eval harness and records a verdict — there is no
// silent default (PLAN.md §8).
type Entry struct {
	Name        string  `json:"name"` // catalog key; also the installed filename without ".gguf"
	Display     string  `json:"display"`
	URL         string  `json:"url"`    // Hugging Face resolve URL of the gguf
	SHA256      string  `json:"sha256"` // lower-hex, of the exact file at URL
	Size        int64   `json:"size"`   // bytes
	Licence     string  `json:"licence"`
	LicenceURL  string  `json:"licence_url"`
	Params      string  `json:"params"` // "7B", "1.5B", ...
	MinRAMGB    int     `json:"min_ram_gb"`
	Tier        string  `json:"tier"`        // "large" | "medium" | "small"
	EvalScore   float64 `json:"eval_score"`  // 0 = not yet evaluated (FR-27)
	Recommended bool    `json:"recommended"` // false until FR-27 picks one per tier
}

// RAM tiers, matching PLAN.md §3: large models need 24 GB+ physical RAM,
// medium 16 GB, small 8 GB. Below 16 GB, summaries default to off.
const (
	TierLarge  = "large"
	TierMedium = "medium"
	TierSmall  = "small"

	gb = 1 << 30
)

// Catalog is every model flowreview knows how to fetch. It is a var, not a
// const, so tests can swap in a fixture pointing at an httptest server
// instead of Hugging Face.
var Catalog = []Entry{
	{
		Name:       "qwen2.5-coder-7b-instruct-q4_k_m",
		Display:    "Qwen2.5 Coder 7B Instruct (Q4_K_M)",
		URL:        "https://huggingface.co/Qwen/Qwen2.5-Coder-7B-Instruct-GGUF/resolve/main/qwen2.5-coder-7b-instruct-q4_k_m.gguf",
		SHA256:     "509287f78cb4d4cf6b3843734733b914b2c158e43e22a7f4bf5e963800894d3c",
		Size:       4683073536,
		Licence:    "Apache-2.0",
		LicenceURL: "https://huggingface.co/Qwen/Qwen2.5-Coder-7B-Instruct-GGUF/blob/main/LICENSE",
		Params:     "7B",
		MinRAMGB:   24,
		Tier:       TierLarge,
		// FR-27, eval/reports/2026-10-14.md (43 cases): score 0.739, kind
		// accuracy 42%, hallucination rate 7.0%, 3.06s mean latency, 14.5
		// tok/s, 5627 MB peak RSS on this Mac.
		EvalScore: 0.739,
	},
	{
		Name:       "qwen3-8b-q4_k_m",
		Display:    "Qwen3 8B (Q4_K_M)",
		URL:        "https://huggingface.co/Qwen/Qwen3-8B-GGUF/resolve/main/Qwen3-8B-Q4_K_M.gguf",
		SHA256:     "d98cdcbd03e17ce47681435b5150e34c1417f50b5c0019dd560e4882c5745785",
		Size:       5027783488,
		Licence:    "Apache-2.0",
		LicenceURL: "https://huggingface.co/Qwen/Qwen3-8B-GGUF/blob/main/LICENSE",
		Params:     "8B",
		MinRAMGB:   24,
		Tier:       TierLarge,
	},
	{
		Name:       "llama-3.1-8b-instruct-q4_k_m",
		Display:    "Llama 3.1 8B Instruct (Q4_K_M)",
		URL:        "https://huggingface.co/lmstudio-community/Meta-Llama-3.1-8B-Instruct-GGUF/resolve/main/Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf",
		SHA256:     "f2be3e1a239c12c9f3f01a962b11fb2807f8032fdb63b0a5502ea42ddef55e44",
		Size:       4920739168,
		Licence:    "Llama 3.1 Community License",
		LicenceURL: "https://www.llama.com/llama3_1/license/",
		Params:     "8B",
		MinRAMGB:   24,
		Tier:       TierLarge,
	},
	{
		Name:       "gemma-3-12b-it-q4_k_m",
		Display:    "Gemma 3 12B it (Q4_K_M)",
		URL:        "https://huggingface.co/bartowski/google_gemma-3-12b-it-GGUF/resolve/main/google_gemma-3-12b-it-Q4_K_M.gguf",
		SHA256:     "fc57f67efa46d711c346e587cbef7d049e95f3df8db2eb2271153343ef0acc7b",
		Size:       7300575264,
		Licence:    "Gemma Terms of Use",
		LicenceURL: "https://ai.google.dev/gemma/terms",
		Params:     "12B",
		MinRAMGB:   32,
		Tier:       TierLarge,
	},
	{
		Name:       "qwen2.5-coder-3b-instruct-q4_k_m",
		Display:    "Qwen2.5 Coder 3B Instruct (Q4_K_M)",
		URL:        "https://huggingface.co/Qwen/Qwen2.5-Coder-3B-Instruct-GGUF/resolve/main/qwen2.5-coder-3b-instruct-q4_k_m.gguf",
		SHA256:     "724fb256bec1ff062b2f65e4569e871ad2e95ab2a3989723d1769c54294730b7",
		Size:       2104932800,
		Licence:    "Qwen Research",
		LicenceURL: "https://huggingface.co/Qwen/Qwen2.5-Coder-3B-Instruct-GGUF/blob/main/LICENSE",
		Params:     "3B",
		MinRAMGB:   16,
		Tier:       TierMedium,
		// FR-27, eval/reports/2026-10-14.md (43 cases): score 0.673, kind
		// accuracy 30%, hallucination rate 16.3%, 1.46s mean latency, 33.8
		// tok/s, 2969 MB peak RSS on this Mac.
		EvalScore: 0.673,
	},
	{
		Name:       "qwen2.5-coder-1.5b-instruct-q4_k_m",
		Display:    "Qwen2.5 Coder 1.5B Instruct (Q4_K_M)",
		URL:        "https://huggingface.co/Qwen/Qwen2.5-Coder-1.5B-Instruct-GGUF/resolve/main/qwen2.5-coder-1.5b-instruct-q4_k_m.gguf",
		SHA256:     "cc324af070c2ecbfd324a30884d2f951a7ff756aba85cb811a6ec436933bb046",
		Size:       1117320768,
		Licence:    "Apache-2.0",
		LicenceURL: "https://huggingface.co/Qwen/Qwen2.5-Coder-1.5B-Instruct-GGUF/blob/main/LICENSE",
		Params:     "1.5B",
		MinRAMGB:   8,
		Tier:       TierSmall,
		// FR-27, eval/reports/2026-10-14.md (43 cases): score 0.701, kind
		// accuracy 33%, hallucination rate 11.5%, 922ms mean latency, 63.8
		// tok/s, 1854 MB peak RSS on this Mac.
		EvalScore: 0.701,
	},
	// Qwen3.5 (2026): added for FR-27 to compare against Qwen2.5-Coder at
	// similar sizes. Unlike the Qwen2.5-Coder line these ship one chat-ready
	// checkpoint per size (no separate "-Instruct" suffix; "-Base" names the
	// raw pretrained one, which this catalog does not use) and are
	// vision-capable multimodal models under the hood — the GGUF repos also
	// ship mmproj-*.gguf files this catalog ignores, using text chat only.
	// URL/SHA256/Size/Licence came from Hugging Face's git-lfs pointer files
	// and model cards (unsloth's GGUF quantizations), not yet independently
	// re-verified by a build in this environment (see the final report: a
	// sandbox classifier blocked running go build/go test/go run against this
	// file after it was edited with fetched web content). EvalScore is 0 for
	// all four — they were never run through the harness, so whether b11609
	// can even load Qwen3.5's architecture is unconfirmed.
	{
		Name:       "qwen3.5-9b-q4_k_m",
		Display:    "Qwen3.5 9B (Q4_K_M)",
		URL:        "https://huggingface.co/unsloth/Qwen3.5-9B-GGUF/resolve/main/Qwen3.5-9B-Q4_K_M.gguf",
		SHA256:     "03b74727a860a56338e042c4420bb3f04b2fec5734175f4cb9fa853daf52b7e8",
		Size:       5680522464,
		Licence:    "Apache-2.0",
		LicenceURL: "https://huggingface.co/Qwen/Qwen3.5-9B/blob/main/LICENSE",
		Params:     "9B",
		MinRAMGB:   24,
		Tier:       TierLarge,
	},
	{
		Name:       "qwen3.5-4b-q4_k_m",
		Display:    "Qwen3.5 4B (Q4_K_M)",
		URL:        "https://huggingface.co/unsloth/Qwen3.5-4B-GGUF/resolve/main/Qwen3.5-4B-Q4_K_M.gguf",
		SHA256:     "00fe7986ff5f6b463e62455821146049db6f9313603938a70800d1fb69ef11a4",
		Size:       2740937888,
		Licence:    "Apache-2.0",
		LicenceURL: "https://huggingface.co/Qwen/Qwen3.5-4B/blob/main/LICENSE",
		Params:     "4B",
		MinRAMGB:   16,
		Tier:       TierMedium,
	},
	{
		Name:       "qwen3.5-2b-q4_k_m",
		Display:    "Qwen3.5 2B (Q4_K_M)",
		URL:        "https://huggingface.co/unsloth/Qwen3.5-2B-GGUF/resolve/main/Qwen3.5-2B-Q4_K_M.gguf",
		SHA256:     "aaf42c8b7c3cab2bf3d69c355048d4a0ee9973d48f16c731c0520ee914699223",
		Size:       1280835840,
		Licence:    "Apache-2.0",
		LicenceURL: "https://huggingface.co/Qwen/Qwen3.5-2B/blob/main/LICENSE",
		Params:     "2B",
		MinRAMGB:   8,
		Tier:       TierSmall,
	},
	{
		Name:       "qwen3.5-0.8b-q4_k_m",
		Display:    "Qwen3.5 0.8B (Q4_K_M)",
		URL:        "https://huggingface.co/unsloth/Qwen3.5-0.8B-GGUF/resolve/main/Qwen3.5-0.8B-Q4_K_M.gguf",
		SHA256:     "bd258782e35f7f458f8aced1adc053e6e92e89bc735ba3be89d38a06121dc517",
		Size:       532517120,
		Licence:    "Apache-2.0",
		LicenceURL: "https://huggingface.co/Qwen/Qwen3.5-0.8B/blob/main/LICENSE",
		Params:     "0.8B",
		MinRAMGB:   8,
		Tier:       TierSmall,
	},
}

// Lookup finds a catalog entry by name.
func Lookup(name string) (Entry, bool) {
	for _, e := range Catalog {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Filter returns the catalog entries for one RAM tier, in catalog order.
func Filter(tier string) []Entry {
	var out []Entry
	for _, e := range Catalog {
		if e.Tier == tier {
			out = append(out, e)
		}
	}
	return out
}

// TierFor classifies physical RAM into the buckets PLAN.md §3 defines:
// 24 GB+ is "large", 16 GB+ is "medium", anything less is "small". There is
// no tier below "small" — a machine with less than 8 GB still sees the small
// catalog, it just also gets SummariesOffByDefault.
func TierFor(ramBytes uint64) string {
	switch {
	case ramBytes >= 24*gb:
		return TierLarge
	case ramBytes >= 16*gb:
		return TierMedium
	default:
		return TierSmall
	}
}

// SummariesOffByDefault reports whether embedded summaries should default to
// off for this much physical RAM: true below 16 GB (PLAN.md §8, "Low RAM").
// The user can still opt in to the small tier explicitly.
func SummariesOffByDefault(ramBytes uint64) bool {
	return ramBytes < 16*gb
}

// SummariesOffReason explains SummariesOffByDefault in a sentence the UI or
// CLI can show as-is.
func SummariesOffReason(ramBytes uint64) string {
	return fmt.Sprintf(
		"Only about %.0f GB of RAM detected; embedded summaries need at least 16 GB, so they start off. You can turn them on and pick the small-tier model anyway.",
		float64(ramBytes)/gb,
	)
}

// PhysicalRAMBytes reads the machine's physical RAM via `sysctl -n
// hw.memsize` (macOS).
func PhysicalRAMBytes() (uint64, error) {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0, fmt.Errorf("models: sysctl hw.memsize: %w", err)
	}
	val, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("models: parse hw.memsize output %q: %w", out, err)
	}
	return val, nil
}
