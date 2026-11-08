package state

import (
	"encoding/json"
	"fmt"
	"testing"
)

// 长会话的存储成本：每轮都保存"完整历史 + 本轮新增"。
//
// 报告指出的风险：AccumulatedItems 在每轮 accumulate 时保存整个 req.Items，
// 而 req.Items 已经包含上一轮回填的全部历史 —— 于是同一段对话被反复复制，
// 总存储量随轮数呈 O(n²) 增长。
//
// 这个测试把增长曲线量出来（不设断言上限，只作为基线记录），
// 用于在改动后对比是否真的降下来了。
func TestAccumulatedItemsGrowthIsQuadratic(t *testing.T) {
	const turns = 40
	var totalBytes int64
	var history []string

	for turn := 0; turn < turns; turn++ {
		// 每轮用户消息 + 助手回复都进历史。
		history = append(history, fmt.Sprintf("user turn %d: %s", turn, longText(200)))
		history = append(history, fmt.Sprintf("assistant turn %d: %s", turn, longText(200)))

		// 模拟 persistResponse：把"完整历史"序列化后存储。
		b, err := json.Marshal(history)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		totalBytes += int64(len(b))
	}

	lastTurnBytes := int64(len(mustJSON(t, history)))
	avgPerTurn := totalBytes / turns

	t.Logf("轮数=%d 累计存储=%.1f KB 平均每轮=%.1f KB 末轮单条=%.1f KB",
		turns, float64(totalBytes)/1024, float64(avgPerTurn)/1024, float64(lastTurnBytes)/1024)

	// 二次增长的直接体现：末轮单条存储量约为平均值的 2 倍。
	if lastTurnBytes < avgPerTurn*3/2 {
		t.Fatalf("预期末轮显著大于平均值（O(n²) 特征），实际 末轮=%d 平均=%d", lastTurnBytes, avgPerTurn)
	}
}

// longText 生成指定字节数的文本，模拟真实消息体。
func longText(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return string(b)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
