package ir

// EstimateTokens 估算一次请求的输入 token 数。
//
// 口径：CJK 等非 ASCII 字符按 1 token，ASCII 按 4 字符 1 token
// （与主流分词器在这两类文本上的平均表现接近）。
//
// 这个值现在有两个用途，准确性比过去更重要：
//  1. 判断上游是不是明显少算了输入（静默截断）；
//  2. 上游不报 usage 时，用它回填账本的输入 token（否则用量与成本恒为 0）。
//
// 因此除了正文，还要把**工具定义**和**每条消息的结构开销**算进去——
// agent 类请求里工具 schema 往往占输入的一大半，漏掉会让估算偏低一半以上。
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
		count(it.EncryptedContent)
		// 附件（图片/文件）不按字符算：一张图通常折算成固定的视觉 token。
		for range it.Attachments {
			cjk += estimateImageTokens
		}
	}

	// 工具定义：名字 + 描述 + JSON Schema。agent 请求里这部分很可观。
	for _, t := range req.Tools {
		count(t.Name)
		count(t.Description)
		count(string(t.Parameters))
	}

	// 每条消息有固定的角色/分隔符开销（各家分词器 3~5 个 token 不等）。
	msgs := int64(len(req.Items))
	if msgs == 0 {
		msgs = 1
	}
	overhead := msgs * perMessageOverheadTokens

	return cjk + ascii/4 + overhead
}

const (
	// estimateImageTokens 是单张图片的粗略折算（视觉模型常见的低分辨率档）。
	estimateImageTokens = 256
	// perMessageOverheadTokens 是每条消息的结构开销。
	perMessageOverheadTokens = 4
)
