package hitl

// RiskChecker 工具风险评估器接口
type RiskChecker interface {
	// IsHighRisk 判断工具是否需要人工审批
	IsHighRisk(toolName string) bool

	// RiskReason 返回工具的高风险原因描述
	RiskReason(toolName string) string
}

// MemoryRiskChecker 基于内存配置的风险检查器
type MemoryRiskChecker struct {
	highRiskTools map[string]string // toolName -> risk reason
}

// NewMemoryRiskChecker 创建内存风险检查器
func NewMemoryRiskChecker() *MemoryRiskChecker {
	return &MemoryRiskChecker{
		highRiskTools: make(map[string]string),
	}
}

// Add 添加高风险工具及其原因
func (r *MemoryRiskChecker) Add(toolName, reason string) {
	r.highRiskTools[toolName] = reason
}

// IsHighRisk 判断工具是否需要人工审批
func (r *MemoryRiskChecker) IsHighRisk(toolName string) bool {
	_, ok := r.highRiskTools[toolName]
	return ok
}

// RiskReason 返回工具的高风险原因描述
func (r *MemoryRiskChecker) RiskReason(toolName string) string {
	if reason, ok := r.highRiskTools[toolName]; ok {
		return reason
	}
	return ""
}
