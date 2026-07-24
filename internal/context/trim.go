package context

import (
	"log"

	"github.com/cloudwego/eino/schema"
)

// TrimByToken 按 Token 数裁剪：保留最近 N 个 token 的消息
// 核心规则：
// 1. system 消息永久保留，不计入裁剪范围
// 2. ToolCall/ToolOutput 配对不可拆散——配对组要么整组保留，要么整组丢弃
// 3. 从尾部向前累加 Token，保留不超过 maxTokens 的消息组
// 4. 首个保留组即使超 maxTokens 也不拆散（宁可多保留不可拆散）
func TrimByToken(messages []*schema.Message, maxTokens int, counter TokenCounter) []*schema.Message {
	if maxTokens <= 0 {
		return messages
	}

	// 分离 system 消息和非 system 消息
	systemMsgs, nonSystemMsgs := separateSystemMessages(messages)

	// 将非 system 消息划分为消息组
	groups := groupMessages(nonSystemMsgs)

	// 从尾部向前累加 Token，保留不超过 maxTokens 的消息组
	retainedGroups := make([]*messageGroup, 0)
	totalTokens := 0

	for i := len(groups) - 1; i >= 0; i-- {
		groupTokens := groups[i].tokenCount(counter)
		if totalTokens + groupTokens <= maxTokens {
			retainedGroups = append([]*messageGroup{groups[i]}, retainedGroups...)
			totalTokens += groupTokens
		} else if totalTokens == 0 {
			// 首个保留组超出 maxTokens，宁可多保留不可拆散
			retainedGroups = append([]*messageGroup{groups[i]}, retainedGroups...)
			totalTokens += groupTokens
			break
		} else {
			break
		}
	}

	// 合并：system 消息 + 保留的消息组
	result := make([]*schema.Message, 0, len(systemMsgs)+len(nonSystemMsgs))
	result = append(result, systemMsgs...)
	for _, g := range retainedGroups {
		result = append(result, g.messages...)
	}

	return result
}

// aggressiveDropOldest 强制丢弃最旧的非 system 消息对（FINAL_GUARD 使用）
// 从头部找到第一个可丢弃的消息组（非 system），整组移除
// 这是防止 LLM API 返回 400 错误的最后防线
func aggressiveDropOldest(messages []*schema.Message) []*schema.Message {
	systemMsgs, nonSystemMsgs := separateSystemMessages(messages)
	if len(nonSystemMsgs) == 0 {
		return messages // 没有可丢弃的非 system 消息
	}

	groups := groupMessages(nonSystemMsgs)
	if len(groups) <= 1 {
		return messages // 至少保留一个组
	}

	// 丢弃第一个组（最旧的）
	remainingNonSystem := make([]*schema.Message, 0, len(nonSystemMsgs)-groups[0].count())
	for _, g := range groups[1:] {
		remainingNonSystem = append(remainingNonSystem, g.messages...)
	}

	// 合并：system + 剩余非 system
	result := make([]*schema.Message, 0, len(systemMsgs)+len(remainingNonSystem))
	result = append(result, systemMsgs...)
	result = append(result, remainingNonSystem...)

	log.Printf("[Context] aggressiveDropOldest: 丢弃最旧消息组(%d条)，剩余%d条非system消息",
		groups[0].count(), len(remainingNonSystem))

	return result
}
