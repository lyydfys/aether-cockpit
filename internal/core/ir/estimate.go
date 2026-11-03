package ir

// EstimateTokens 粗估一次请求的输入 token 数。
//
// 只用来判断"上游是不是明显少算了"，不参与计费与裁剪决策。
// 口径：CJK 等非 ASCII 字符按 1 token，ASCII 按 4 字符 1 token。
// 实测与上游 usage 的偏差在 ±30% 以内，判断量级（差 2 倍以上）足够。
func EstimateTokens(req *UnifiedChatRequest) int64 {
	if req == nil {
		return 0
	}
	var cjk, ascii int64
	count := func(s string) {
		for _, r := range s {
			if r < 128 {
				ascii++
			} else {
				cjk++
			}
		}
	}
	count(req.Instructions)
	for _, it := range req.Items {
		count(it.Text)
		count(it.Output)
		count(it.Arguments)
		count(it.Name)
	}
	return cjk + ascii/4
}
