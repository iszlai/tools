// Package eventpkg covers FR-16's custom .flowreview.yaml patterns — see
// ../.flowreview.yaml.
package eventpkg

import "context"

// Event is the config's example signature pattern's parameter type.
type Event struct{}

// OnEvent matches ../.flowreview.yaml's func_regex pattern "^OnEvent$".
func OnEvent() {}

// HandleEvent matches ../.flowreview.yaml's signature pattern
// "func(context.Context, *Event) error".
func HandleEvent(ctx context.Context, e *Event) error { return nil }
