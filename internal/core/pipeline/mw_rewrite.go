package pipeline

import (
	"context"
	"fmt"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/mw"
	"aether-cockpit/internal/provider"
)

func init() {
	Register("rewrite", func(deps Deps) Middleware { return Rewrite(deps) })
}

// Rewrite 应用声明式的请求改写规则（参数覆盖 / 模型映射 / 关键词拦截 / 系统提示）。
//
// 位置：在 redact 之后、spec_overrides 之前。
//   - 在 redact 之后：脱敏先跑，改写规则看到的是已脱敏的内容，
//     这样关键词拦截不会因为误判明文而漏拦。
//   - 在 spec_overrides 之前：spec 注入的系统提示词也要经过改写规则检查。
func Rewrite(deps Deps) Middleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
			eng := deps.Rewrite
			if eng == nil || req == nil {
				return next(ctx, env, req)
			}

			// 规则引擎用指针表达"用户有没有显式指定"，
			// 而 IR 里这两个字段本身就是指针，正好对上。
			var temp, topP float64
			if req.Temperature != nil {
				temp = *req.Temperature
			}
			if req.TopP != nil {
				topP = *req.TopP
			}
			var maxTok int
			if req.MaxOutputTokens != nil {
				maxTok = *req.MaxOutputTokens
			}
			instr := req.Instructions

			mctx := &mw.Context{Provider: req.ProviderID, Model: req.Model}
			if blocked := eng.Apply(mctx, &temp, &topP, &maxTok, &instr); blocked != "" {
				// 拦截：给下游一个明确的错误，而不是静默丢弃
				return nil, fmt.Errorf("请求被改写规则拦截: %s", blocked)
			}

			// 回写
			if temp != 0 {
				req.Temperature = &temp
			}
			if topP != 0 {
				req.TopP = &topP
			}
			if maxTok != 0 {
				req.MaxOutputTokens = &maxTok
			}
			req.Instructions = instr
			if mctx.Model != "" && mctx.Model != req.Model {
				req.Model = mctx.Model
				// 渠道真正读取的是 UpstreamModel（路由层已回填）；只改
				// req.Model 会让"模型映射"看起来生效、实际仍发旧模型。
				if req.UpstreamModel != "" {
					req.UpstreamModel = strings.TrimPrefix(mctx.Model, req.ProviderID+"/")
				}
			}

			return next(ctx, env, req)
		}
	}
}
