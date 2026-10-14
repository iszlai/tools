# flowreview — todo

Goal: one Go binary, own native window, that reviews a local diff or GitHub PR in
code-flow order for Go, Java, JS/TS and Gherkin, with callers/callees, blast radius,
embedded local summaries (llama-server inside the binary), and GitHub publishing.
Full plan: `../PLAN.md`. Board: `th next` in this folder (`.taskhound.yaml`, prefix FR).

## M1 — daily-usable reviewer
- [x] FR-1 Skeleton and frozen types
- [x] FR-2 Git adapter
- [x] FR-3 Unified diff parser and position map
- [x] FR-4 Session store
- [x] FR-5 Server, native WKWebView window and UI shell
- [x] FR-6 Side-by-side diff renderer (strict perf budget in smoke)
- [x] FR-7 Review queue, reviewed state, progress
- [x] FR-8 Enclosing-function expansion
- [x] FR-9 Comment drafts
- [x] FR-10 GitHub host: read
- [x] FR-11 GitHub host: publish
- [x] FR-12 CLI surface M1
- [x] FR-39 Tree-sitter structural provider (all languages)
- [ ] Dogfood gate: review one real PR with the tool

## M2 — flow and risk
- [x] FR-32 On-demand Go toolchain download
- [x] FR-13 Go package loader with cache (spike on ledger first)
- [x] FR-14 Symbols and definitions
- [x] FR-15 Call graph: callers, callees, implementations
- [x] FR-16 Entry points
- [x] FR-17 Flow ordering
- [ ] FR-18 Call-chain explorer UI
- [x] FR-19 Blast radius and signature changes
- [x] FR-20 Co-change companions and hotspots
- [x] FR-21 Test reach
- [ ] FR-22 Delta re-review (with diff against the body you reviewed)
- [ ] FR-23 Commit walkthrough order
- [ ] FR-24 CLI text/JSON surface for agents
- [x] FR-40 Gherkin step linking
- [ ] FR-41 Java and JS/TS structural flow
- [ ] Dogfood gate

## M3 — embedded summaries
- [x] FR-25 Sidecar embedding and lifecycle
- [x] FR-26 Model manager
- [ ] FR-27 Eval harness and catalog ranking (HITL; user picks at first run)
- [ ] FR-28 Summary pipeline
- [ ] FR-29 Summary UI
- [ ] FR-30 Review body draft
- [ ] Dogfood gate

## M4 — agentic review
- [x] FR-31 Finding model and provider interface
- [ ] FR-33 Embedded-model provider (only agentic provider)
- [ ] FR-34 Findings UI and approve-to-comment
- [ ] FR-35 Read-only checks runner

## M5 — distribution
- [x] FR-36 Release and upgrade
- [ ] FR-37 .app bundle polish
- [ ] FR-38 Signing and notarization (HITL)

## Decisions
All grill questions answered 2026-10-14; see PLAN.md §8. Revisit the UI-layer choice (web in WKWebView vs SwiftUI) at the M3 dogfood gate.

## Review
(filled in as milestones land)
