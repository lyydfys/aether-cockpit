package devin

// 模型名 → 上游 chat_model_uid 的解析。
//
// Devin 上游只认 UID（如 swe-2-high / MODEL_PRIVATE_3 / MODEL_GPT_5_2_MEDIUM），
// 且不同模型的思考档位后缀规则不同。规则移植自 RelayProxy
// internal/runtime/executor/helps/devin_models.go；上游动态模型目录未接入，
// 因此对未知模型退化为「原样透传」，对已知模型按静态档位表归一。

import (
	"strings"
)

var devinKnownSuffixes = []string{
	"-none", "-low", "-medium", "-high", "-xhigh", "-max",
	"-fast", "-slow", "-priority", "-low-priority", "-medium-priority",
	"-high-priority", "-xhigh-priority", "-max-priority",
	"-low-fast", "-medium-fast", "-high-fast", "-xhigh-fast", "-max-fast", "-none-fast",
	"-thinking-1m", "-thinking", "-max-1m", "-none-1m",
	"-minimal-lightning", "-low-lightning", "-medium-lightning", "-high-lightning",
	"-xhigh-lightning", "-max-lightning", "-none-lightning",
	"_none", "_minimal", "_low", "_medium", "_high", "_xhigh", "_max", "_thinking",
}

var devinSpecialAliases = map[string]string{
	"claude-haiku-4-5": "MODEL_PRIVATE_11",
	"gpt-4-1":          "MODEL_CHAT_GPT_4_1_2025_04_14",
}

// devinThinkingLevels 是已知支持思考档位的模型（键为点转横线后的规范名）及其可用档位。
var devinThinkingLevels = map[string][]string{
	"swe-2":            {"none", "low", "medium", "high", "xhigh", "max"},
	"gpt-5-2":          {"none", "low", "medium", "high", "xhigh"},
	"gemini-3-flash":   {"minimal", "low", "medium", "high"},
	"gemini-3-8-flash": {"minimal", "low", "medium", "high"},
	"deepseek-v3-2":    {"low", "medium", "high", "max"},
	"glm-5-2":          {"none", "low", "medium", "high", "max"},
	"kimi-k2-5":        {"low", "medium", "high"},
	"grok-4-1-fast":    {"low", "medium", "high"},
}

func hasDevinEffortSuffix(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	for _, s := range devinKnownSuffixes {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	return false
}

// normalizeThinkingLevel 把思考预算/宽松档位词归一成 Devin 档位。
func normalizeThinkingLevel(level string, budgetTokens int) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "minimal", "low", "medium", "high", "xhigh", "max", "fast":
		return strings.ToLower(strings.TrimSpace(level))
	case "none", "off", "disabled":
		return "none"
	case "auto", "adaptive":
		return "high"
	}
	if budgetTokens > 0 {
		switch {
		case budgetTokens <= 4096:
			return "low"
		case budgetTokens <= 16384:
			return "medium"
		case budgetTokens <= 32768:
			return "high"
		default:
			return "max"
		}
	}
	return ""
}

// resolveDevinChatModelUID 把任意模型名解析成上游 UID；effort 未指定时用静态默认档。
func resolveDevinChatModelUID(rawModel, thinkingLevel string, budgetTokens int) string {
	model := strings.TrimSpace(rawModel)
	if model == "" {
		return "swe-2-high"
	}
	clean := model
	if strings.HasPrefix(strings.ToLower(clean), "devin/") {
		clean = clean[6:]
	}
	if hasDevinEffortSuffix(clean) {
		return clean
	}

	base := clean
	if i := strings.LastIndex(clean, ":"); i != -1 {
		base = strings.TrimSpace(clean[:i])
		thinkingLevel = strings.TrimSpace(clean[i+1:])
	}
	effort := normalizeThinkingLevel(thinkingLevel, budgetTokens)
	lowerBase := strings.ToLower(base)
	canonical := strings.ReplaceAll(lowerBase, ".", "-")

	if alias, ok := devinSpecialAliases[canonical]; ok {
		return alias
	}
	if canonical == "claude-sonnet-4-5" || strings.Contains(canonical, "sonnet-4-5") {
		if effort != "" && effort != "none" {
			return "MODEL_PRIVATE_3"
		}
		return "MODEL_PRIVATE_2"
	}
	if canonical == "gemini-3-flash" {
		canonical = "gemini-3-8-flash"
	}

	switch strings.ReplaceAll(canonical, "-", "_") {
	case "model_gpt_5_2":
		return "MODEL_GPT_5_2_" + strings.ToUpper(clampDevinEffort(effort, devinThinkingLevels["gpt-5-2"], "low"))
	case "model_google_gemini_3_0_flash":
		return "MODEL_GOOGLE_GEMINI_3_0_FLASH_" + strings.ToUpper(clampDevinEffort(effort, devinThinkingLevels["gemini-3-flash"], "high"))
	case "model_claude_4_5_opus":
		if effort != "" && effort != "none" {
			return "MODEL_CLAUDE_4_5_OPUS_THINKING"
		}
		return "MODEL_CLAUDE_4_5_OPUS"
	}

	switch canonical {
	case "swe-1-7":
		if effort == "medium" {
			return "swe-1-7-medium"
		}
		return "swe-1-7"
	case "swe-1-6":
		if effort == "fast" {
			return "swe-1-6-fast"
		}
		return "swe-1-6"
	case "glm-5-2":
		if effort == "none" {
			return "glm-5-2-none"
		}
		if effort == "max" {
			return "glm-5-2-max"
		}
		return "glm-5-2"
	case "glm-5-2-1m":
		if effort == "none" {
			return "glm-5-2-none-1m"
		}
		if effort == "max" {
			return "glm-5-2-max-1m"
		}
		return "glm-5-2-1m"
	case "claude-opus-4-6", "claude-sonnet-4-6":
		if effort != "" && effort != "none" {
			return canonical + "-thinking"
		}
		return canonical
	case "claude-opus-4-6-1m":
		if effort != "" && effort != "none" {
			return "claude-opus-4-6-thinking-1m"
		}
		return canonical
	case "claude-sonnet-4-6-1m":
		if effort != "" && effort != "none" {
			return "claude-sonnet-4-6-thinking-1m"
		}
		return canonical
	}

	if strings.HasSuffix(canonical, "-lightning") {
		stem := strings.TrimSuffix(canonical, "-lightning")
		if hasDevinEffortSuffix(stem) {
			return canonical
		}
		levels := devinThinkingLevels[stem]
		if len(levels) == 0 {
			return canonical
		}
		return stem + "-" + clampDevinEffort(effort, levels, selectDefaultDevinEffort(stem, levels)) + "-lightning"
	}

	levels := devinThinkingLevels[canonical]
	if len(levels) == 0 {
		return canonical
	}
	return canonical + "-" + clampDevinEffort(effort, levels, selectDefaultDevinEffort(canonical, levels))
}

func selectDefaultDevinEffort(baseModel string, levels []string) string {
	if strings.Contains(baseModel, "swe-2") {
		return "high"
	}
	has := map[string]bool{}
	for _, l := range levels {
		has[l] = true
	}
	if has["none"] && has["low"] && strings.HasPrefix(baseModel, "gpt-5") {
		return "low"
	}
	if has["high"] && (strings.Contains(baseModel, "gemini") ||
		strings.Contains(baseModel, "grok") ||
		strings.Contains(baseModel, "glm") ||
		strings.Contains(baseModel, "deepseek") ||
		strings.Contains(baseModel, "kimi") ||
		strings.Contains(baseModel, "nemotron")) {
		return "high"
	}
	if has["medium"] {
		return "medium"
	}
	if has["high"] {
		return "high"
	}
	if has["low"] {
		return "low"
	}
	return levels[0]
}

var devinStandardLevelOrder = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

func devinLevelIndex(level string) int {
	lower := strings.ToLower(strings.TrimSpace(level))
	for i, l := range devinStandardLevelOrder {
		if l == lower {
			return i
		}
	}
	return -1
}

func clampDevinEffort(requested string, allowed []string, defaultEffort string) string {
	if requested == "" {
		return defaultEffort
	}
	reqLower := strings.ToLower(strings.TrimSpace(requested))
	for _, a := range allowed {
		if reqLower == strings.ToLower(strings.TrimSpace(a)) {
			return a
		}
	}
	if reqLower == "none" {
		return defaultEffort
	}
	reqIdx := devinLevelIndex(reqLower)
	if reqIdx == -1 {
		return defaultEffort
	}
	bestMatch := defaultEffort
	bestDist := 999
	bestIdx := -1
	for _, a := range allowed {
		aIdx := devinLevelIndex(a)
		if aIdx == -1 {
			continue
		}
		dist := reqIdx - aIdx
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist = dist
			bestMatch = a
			bestIdx = aIdx
		} else if dist == bestDist && aIdx > bestIdx {
			bestMatch = a
			bestIdx = aIdx
		}
	}
	return bestMatch
}
