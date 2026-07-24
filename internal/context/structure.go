package context

import (
	stdctx "context"

	"github.com/cloudwego/eino/schema"
)

// messageGroup 消息分组单元（不可拆散）
// 配对组：assistant+ToolCalls + 其后连续的 tool 消息，作为一个整体
// 独立组：不含 ToolCalls 的 assistant 消息、user 消息，各自独立
type messageGroup struct {
	messages []*schema.Message
	isPaired bool // 是否为 assistant+tool 配对组
}

// count 返回组内消息数
func (g *messageGroup) count() int {
	return len(g.messages)
}

// tokenCount 返回组内消息的总 Token 数
func (g *messageGroup) tokenCount(counter TokenCounter) int {
	total := 0
	for _, msg := range g.messages {
		count, _ := counter.CountMessage(stdctx.Background(), msg)
		total += count
	}
	return total
}

// messageStructure 消息结构分析结果（STRUCTURAL_LOCK 输出）
// 标记 system 消息位置和 ToolCall/ToolOutput 配对边界
type messageStructure struct {
	systemIndices   []int           // system 消息在原始列表中的位置（永久保留）
	nonSystemGroups []*messageGroup // 非 system 消息分组（可操作区间）
	pairBoundaries  map[int]int     // ToolCall 消息索引 → 对应最后一个 ToolOutput 消息索引
}

// analyzeMessageStructure 分析消息结构，标记保护边界（STRUCTURAL_LOCK）
// 在所有裁剪操作前调用，确保 system 消息不被裁、ToolCall/ToolOutput 配对不被拆散
func analyzeMessageStructure(messages []*schema.Message) *messageStructure {
	structure := &messageStructure{
		pairBoundaries: make(map[int]int),
	}

	var nonSystem []*schema.Message
	var currentPairStart int = -1
	var currentPairEnd int = -1

	for i, msg := range messages {
		if msg.Role == schema.System {
			structure.systemIndices = append(structure.systemIndices, i)
			// 如果之前有未关闭的配对组，先保存
			if currentPairStart >= 0 {
				structure.pairBoundaries[currentPairStart] = currentPairEnd
				currentPairStart = -1
			}
		} else {
			nonSystem = append(nonSystem, msg)

			if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
				// 新配对组起始
				if currentPairStart >= 0 {
					structure.pairBoundaries[currentPairStart] = currentPairEnd
				}
				currentPairStart = i
				currentPairEnd = i
			} else if msg.Role == schema.Tool && currentPairStart >= 0 {
				// 配对组成员
				currentPairEnd = i
			} else {
				// 独立消息 → 关闭前一个配对组（如有）
				if currentPairStart >= 0 {
					structure.pairBoundaries[currentPairStart] = currentPairEnd
					currentPairStart = -1
				}
			}
		}
	}

	// 处理最后一个配对组
	if currentPairStart >= 0 {
		structure.pairBoundaries[currentPairStart] = currentPairEnd
	}

	// 构建非 system 消息分组
	structure.nonSystemGroups = groupMessages(nonSystem)

	return structure
}

// groupMessages 将非 system 消息划分为消息组
// 分组规则：
// 1. assistant 消息含 ToolCalls → 新配对组起始
// 2. tool 消息（Role == schema.Tool）→ 归入当前配对组
// 3. 其他消息 → 独立组，并关闭前一个配对组
func groupMessages(messages []*schema.Message) []*messageGroup {
	var groups []*messageGroup
	var currentPair *messageGroup // 当前正在构建的配对组

	for _, msg := range messages {
		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			// assistant 消息含 ToolCalls → 新配对组起始
			if currentPair != nil {
				groups = append(groups, currentPair)
			}
			currentPair = &messageGroup{
				messages: []*schema.Message{msg},
				isPaired: true,
			}
		} else if msg.Role == schema.Tool && currentPair != nil {
			// tool 消息 → 归入当前配对组
			currentPair.messages = append(currentPair.messages, msg)
		} else {
			// 其他消息 → 独立组
			if currentPair != nil {
				groups = append(groups, currentPair)
				currentPair = nil
			}
			groups = append(groups, &messageGroup{
				messages: []*schema.Message{msg},
				isPaired: false,
			})
		}
	}

	// 处理最后一个配对组
	if currentPair != nil {
		groups = append(groups, currentPair)
	}

	return groups
}

// separateSystemMessages 分离 system 消息和非 system 消息
func separateSystemMessages(messages []*schema.Message) (systemMsgs, nonSystemMsgs []*schema.Message) {
	for _, msg := range messages {
		if msg.Role == schema.System {
			systemMsgs = append(systemMsgs, msg)
		} else {
			nonSystemMsgs = append(nonSystemMsgs, msg)
		}
	}
	return
}
