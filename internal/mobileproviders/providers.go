// Package mobileproviders registers the channel implementations available to
// the mobile bridges. Keeping the matrix explicit prevents a channel that works
// on desktop from silently missing on a phone.
package mobileproviders

import (
	_ "aether-cockpit/internal/providers/antigravity"
	_ "aether-cockpit/internal/providers/antigravityide"
	_ "aether-cockpit/internal/providers/claude"
	_ "aether-cockpit/internal/providers/codebuddy"
	_ "aether-cockpit/internal/providers/cursor"
	_ "aether-cockpit/internal/providers/devin"
	_ "aether-cockpit/internal/providers/geminicompat"
	_ "aether-cockpit/internal/providers/githubcopilot"
	_ "aether-cockpit/internal/providers/kiro"
	_ "aether-cockpit/internal/providers/minimax"
	_ "aether-cockpit/internal/providers/openaicompat"
	_ "aether-cockpit/internal/providers/qoder"
	_ "aether-cockpit/internal/providers/reverse"
	_ "aether-cockpit/internal/providers/trae"
	_ "aether-cockpit/internal/providers/typesafe"
	_ "aether-cockpit/internal/providers/vertex"
	_ "aether-cockpit/internal/providers/windsurf"
	_ "aether-cockpit/internal/providers/workbuddy"
	_ "aether-cockpit/internal/providers/zcode"
	_ "aether-cockpit/internal/providers/zed"
)
