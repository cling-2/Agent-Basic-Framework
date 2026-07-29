package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ========== 长期记忆注入 ==========

// keyLabelMap key 后缀到人类可读标签的映射
var keyLabelMap = map[string]string{
	"language":            "编程语言",
	"response_style":      "回答风格",
	"role":                "角色",
	"team":                "团队",
	"framework":           "框架",
	"editor":              "编辑器",
	"operating_system":    "操作系统",
	"communication":       "沟通方式",
	"detail_level":        "详细程度",
	"output_format":       "输出格式",
	"no_email_forwarding": "禁止邮件转发",
	"name":                "姓名",
	"email":               "邮箱",
	"location":            "所在地",
	"hometown":            "家乡",
	"surname":             "姓氏",
	"age":                 "年龄",
	"birthday":            "生日",
	"company":             "公司",
	"project":             "项目",
}

// humanizeKey 将 key 转为人类可读形式
// "preference_language" → "编程语言"
// "fact_role" → "角色"
func humanizeKey(key string) string {
	parts := strings.SplitN(key, "_", 2)
	if len(parts) < 2 {
		return key
	}
	suffix := parts[1]
	if label, ok := keyLabelMap[suffix]; ok {
		return label
	}
	return suffix
}

// BuildMemoryInjection 构造长期记忆注入消息
// 返回 nil 表示无需注入（无记忆条目）
func BuildMemoryInjection(entries []*MemoryEntry) *schema.Message {
	if len(entries) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.WriteString("关于此用户的已知信息：\n")
	for _, e := range entries {
		switch e.Category {
		case "preference":
			sb.WriteString(fmt.Sprintf("- 偏好%s：%s\n", humanizeKey(e.Key), e.Value))
		case "fact":
			sb.WriteString(fmt.Sprintf("- %s：%s\n", humanizeKey(e.Key), e.Value))
		case "rule":
			sb.WriteString(fmt.Sprintf("- 规则：%s（%s）\n", e.Value, humanizeKey(e.Key)))
		default:
			sb.WriteString(fmt.Sprintf("- %s：%s\n", e.Key, e.Value))
		}
	}

	return schema.SystemMessage(sb.String())
}

// ========== 显式触发写入策略 ==========

// ShouldSaveMemory 检测用户消息是否包含需要保存长期记忆的意图
func ShouldSaveMemory(userMessage string) bool {
	triggers := []string{
		"请记住", "记住我", "我偏好", "我喜欢", "我是",
		"以后都用", "默认用", "每次都", "我习惯",
		"我叫", "我的名字", "记住", "别忘了",
		"我姓", "我来自", "我住在", "我的邮箱",
		"记住这", "以后记得", "我的是", "我的电话",
		"我擅长", "我会", "我能", "我用", "我负责",
		"我的工作", "我的职业", "我的公司", "我的项目",
		"我正在", "我之前", "我平时", "我通常",
	}
	lower := strings.ToLower(userMessage)
	for _, t := range triggers {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

// ShouldTryLLMExtraction 轻量预筛：判断是否值得调用 LLM 进行记忆提取
// 比规则触发宽松——只要包含第一人称陈述或信息量足够就有可能含可提取信息
// 纯问句、极短消息等跳过，避免浪费 LLM 调用
func ShouldTryLLMExtraction(userMessage string) bool {
	// 极短消息不太可能含可提取信息
	if len([]rune(userMessage)) < 4 {
		return false
	}
	// 规则触发词直接通过
	if ShouldSaveMemory(userMessage) {
		return true
	}
	// 包含第一人称陈述标记（"我" + 动词/形容词）
	firstPersonPatterns := []string{"我是", "我叫", "我姓", "我有", "我会", "我能",
		"我擅长", "我喜欢", "我偏好", "我习惯", "我负责", "我用",
		"我住", "我来自", "我的", "我之前", "我正在", "我平时",
		"我通常", "我经常", "我总是", "我从不", "我不要"}
	lower := strings.ToLower(userMessage)
	for _, p := range firstPersonPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// ExtractMemoryFromConversation 从对话中提取长期记忆条目（规则匹配兜底）
func ExtractMemoryFromConversation(userMsg string) []*MemoryEntry {
	var entries []*MemoryEntry

	// 编程语言偏好
	if lang := extractLanguagePreference(userMsg); lang != "" {
		entries = append(entries, &MemoryEntry{
			Key:      "preference_language",
			Value:    lang,
			Category: "preference",
		})
	}

	// 回答风格偏好
	if style := extractResponseStyle(userMsg); style != "" {
		entries = append(entries, &MemoryEntry{
			Key:      "preference_response_style",
			Value:    style,
			Category: "preference",
		})
	}

	// 角色/身份事实
	if role := extractRoleFact(userMsg); role != "" {
		entries = append(entries, &MemoryEntry{
			Key:      "fact_role",
			Value:    role,
			Category: "fact",
		})
	}

	return entries
}

// ========== 偏好提取辅助函数 ==========

// languageKeywords 编程语言关键词映射
var languageKeywords = map[string]string{
	"python":     "Python",
	"java":       "Java",
	"go":         "Go",
	"golang":     "Go",
	"javascript": "JavaScript",
	"js":         "JavaScript",
	"typescript": "TypeScript",
	"ts":         "TypeScript",
	"c++":        "C++",
	"cpp":        "C++",
	"c#":         "C#",
	"csharp":     "C#",
	"rust":       "Rust",
	"ruby":       "Ruby",
	"php":        "PHP",
	"swift":      "Swift",
	"kotlin":     "Kotlin",
	"scala":      "Scala",
}

// extractLanguagePreference 提取编程语言偏好
func extractLanguagePreference(msg string) string {
	lower := strings.ToLower(msg)
	// 仅在包含偏好触发词 + 语言关键词时提取
	hasTrigger := strings.Contains(lower, "喜欢") ||
		strings.Contains(lower, "偏好") ||
		strings.Contains(lower, "用") ||
		strings.Contains(lower, "写代码") ||
		strings.Contains(lower, "编程") ||
		strings.Contains(lower, "开发")

	if !hasTrigger {
		return ""
	}

	for kw, lang := range languageKeywords {
		if strings.Contains(lower, kw) {
			return lang
		}
	}
	return ""
}

// styleKeywords 回答风格关键词映射
var styleKeywords = map[string]string{
	"简洁":   "简洁",
	"简短":   "简洁",
	"详细":   "详细",
	"详尽":   "详细",
	"精简":   "简洁",
	"简单":   "简洁",
	"深入":   "详细",
	"全面":   "详细",
	"代码为主": "代码为主",
	"解释为主": "解释为主",
	"多举例":  "举例为主",
}

// extractResponseStyle 提取回答风格偏好
func extractResponseStyle(msg string) string {
	lower := strings.ToLower(msg)
	for kw, style := range styleKeywords {
		if strings.Contains(lower, kw) {
			return style
		}
	}
	return ""
}

// roleKeywords 角色/身份关键词映射
var roleKeywords = map[string]string{
	"后端开发":      "后端开发者",
	"后端":        "后端开发者",
	"前端开发":      "前端开发者",
	"前端":        "前端开发者",
	"全栈":        "全栈开发者",
	"运维":        "运维工程师",
	"测试":        "测试工程师",
	"产品经理":      "产品经理",
	"数据分析师":     "数据分析师",
	"架构师":       "架构师",
	"devops":    "DevOps工程师",
	"sre":       "SRE工程师",
	"java开发者":   "Java开发者",
	"python开发者": "Python开发者",
	"go开发者":     "Go开发者",
	"学生":        "学生",
	"实习生":       "实习生",
}

// extractRoleFact 提取角色/身份事实
func extractRoleFact(msg string) string {
	lower := strings.ToLower(msg)
	for kw, role := range roleKeywords {
		if strings.Contains(lower, kw) {
			return role
		}
	}
	return ""
}

// ========== LLM 驱动的记忆提取 ==========

// MemoryExtractor 记忆提取器接口
type MemoryExtractor interface {
	Extract(ctx context.Context, userMsg string) []*MemoryEntry
}

// LLMMemoryExtractor 使用 LLM 提取用户长期记忆
type LLMMemoryExtractor struct {
	chatModel einomodel.BaseChatModel
}

// NewLLMMemoryExtractor 创建 LLM 记忆提取器
func NewLLMMemoryExtractor(chatModel einomodel.BaseChatModel) *LLMMemoryExtractor {
	return &LLMMemoryExtractor{chatModel: chatModel}
}

const memoryExtractionSystemPrompt = `你是一个用户画像提取助手。分析用户消息，提取所有值得长期记住的用户信息。

提取规则：
1. 只提取明确的、事实性的信息（偏好、身份、规则、个人事实）
2. 不要提取模糊或不确定的信息
3. key 使用 "category_detail" 格式，如 "preference_language"、"fact_name"、"rule_no_email_forwarding"
4. category 只能是 "preference"（偏好）、"fact"（事实）或 "rule"（规则）
5. 如果没有值得记住的信息，返回空数组

返回格式（纯JSON，不要markdown代码块）：
[{"key":"category_detail","value":"具体值","category":"preference|fact|rule"}]`

// memoryEntryJSON LLM 提取结果的 JSON 结构
type memoryEntryJSON struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Category string `json:"category"`
}

// markdownCodeBlockRe 匹配 LLM 可能添加的 markdown 代码块包裹
var markdownCodeBlockRe = regexp.MustCompile("(?s)```(?:json)?\\s*\n?(.*?)\n?```")

// Extract 使用 LLM 从用户消息中提取记忆条目
func (e *LLMMemoryExtractor) Extract(ctx context.Context, userMsg string) []*MemoryEntry {
	prompt := []*schema.Message{
		schema.SystemMessage(memoryExtractionSystemPrompt),
		schema.UserMessage(userMsg),
	}

	result, err := e.chatModel.Generate(ctx, prompt)
	if err != nil {
		log.Printf("[Memory/LLM] 提取调用失败: %v，降级到规则提取", err)
		return nil
	}

	content := strings.TrimSpace(result.Content)
	if content == "" {
		return nil
	}

	// 去掉 LLM 可能添加的 markdown 代码块包裹
	if m := markdownCodeBlockRe.FindStringSubmatch(content); len(m) >= 2 {
		content = strings.TrimSpace(m[1])
	}

	var jsonEntries []memoryEntryJSON
	if err := json.Unmarshal([]byte(content), &jsonEntries); err != nil {
		maxLen := 200
		if len(content) < maxLen {
			maxLen = len(content)
		}
		log.Printf("[Memory/LLM] JSON 解析失败: %v，原始内容: %s，降级到规则提取", err, content[:maxLen])
		return nil
	}

	var entries []*MemoryEntry
	for _, je := range jsonEntries {
		if je.Key == "" || je.Value == "" {
			continue
		}
		category := je.Category
		if category != "preference" && category != "fact" && category != "rule" {
			category = "fact" // 默认归类为事实
		}
		entries = append(entries, &MemoryEntry{
			Key:      je.Key,
			Value:    je.Value,
			Category: category,
		})
	}

	return entries
}
