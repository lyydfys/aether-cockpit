package pipeline

import (
	"context"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

func init() {
	Register("spec_overrides", func(deps Deps) Middleware { return SpecOverrides() })
}

// SpecOverrides 把 spec 里两类"渠道级补偿参数"套到请求上：
//
//  1. system_prompt_inject：把指定系统提示词追加到 Instructions 末尾。
//     某些逆向上游必须带特定系统提示才肯工作（固定的人格、格式或安全声明），
//     这类约束是"喂给机制的参数"而非机制本身，放 spec 里改完即生效。
//  2. request_overrides：max_output_tokens / temperature 的缺省值。
//     只在客户端没显式给该参数时生效 —— 覆盖用户意图是大忌，缺省才叫补偿。
//
// 为什么做成中间件而不是塞进各 provider：
// 这两件事与"某个上游怎么发请求"无关，与"这个渠道缺什么"有关。
// 塞进 provider 意味着 N 个渠道各抄一份相同的 if-else，每改一句话都要重新编译；
// 做成中间件后，渠道在 spec JSON 的 middlewares 数组里声明 "spec_overrides"
// 即启用（网关也可在 handlerFor 统一接线），参数本体就在同一个 spec 文件里热更新，
// 改 JSON 存盘即生效，不用重启。
//
// spec JSON 示例：
//
//	{
//	  "provider_id": "demo",
//	  "middlewares": ["spec_overrides"],
//	  "system_prompt_inject": "你必须始终用中文回答。",
//	  "request_overrides": {"max_output_tokens": 8192, "temperature": 0.7}
//	}
func SpecOverrides() Middleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
			if req == nil || req.ProviderID == "" {
				return next(ctx, env, req)
			}
			sp := spec.Load(req.ProviderID)
			if !hasSpecOverrides(sp) {
				return next(ctx, env, req)
			}
			// 与 system_role_merge / state_suture 同一惯例：改前先克隆，
			// 调用方手里的原请求不被本中间件污染（dispatch 层每轮重试各自持有副本）。
			work := req.Clone()
			applySpecOverrides(work, sp)
			return next(ctx, env, work)
		}
	}
}

// hasSpecOverrides 判断这条 spec 是否声明了需要本中间件落地的内容。
// 没有就直接放行，避免为大多数未配置的渠道白白付出一次 Clone。
func hasSpecOverrides(sp *spec.Spec) bool {
	if sp == nil {
		return false
	}
	if strings.TrimSpace(sp.SystemPromptInject) != "" {
		return true
	}
	ro := sp.RequestOverrides
	return ro != nil && (ro.Temperature != nil || ro.MaxOutputTokens != nil)
}

// applySpecOverrides 就地改写请求（调用方必须已经 Clone 过）。
func applySpecOverrides(req *ir.UnifiedChatRequest, sp *spec.Spec) {
	if inject := strings.TrimSpace(sp.SystemPromptInject); inject != "" {
		switch {
		case strings.TrimSpace(req.Instructions) == "":
			// Instructions 为空：注入值直接顶上，不引入前导空行。
			req.Instructions = inject
		case strings.HasSuffix(req.Instructions, inject):
			// 已经注入过（渠道被双层包装：网关统一接线 + spec middlewares 数组
			// 同时声明时，本中间件会出现在管道里两次）。跳过防止提示词翻倍。
		default:
			req.Instructions = req.Instructions + "\n\n" + inject
		}
	}

	if ro := sp.RequestOverrides; ro != nil {
		// 只补缺省，永不覆盖客户端显式给出的值。
		if req.Temperature == nil && ro.Temperature != nil {
			v := *ro.Temperature
			req.Temperature = &v // 拷贝一份，避免请求与 spec 存储共享指针
		}
		if req.MaxOutputTokens == nil && ro.MaxOutputTokens != nil {
			v := *ro.MaxOutputTokens
			req.MaxOutputTokens = &v
		}
	}
}
