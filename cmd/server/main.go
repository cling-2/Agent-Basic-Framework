package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"kingsoft-agent/api"
	"kingsoft-agent/internal/agent"
	"kingsoft-agent/internal/auth"
	ctxmgr "kingsoft-agent/internal/context"
	"kingsoft-agent/internal/hitl"
	"kingsoft-agent/internal/settings"
	"kingsoft-agent/internal/toolreg"
	"kingsoft-agent/internal/toolreg/tools"
	pkgmodel "kingsoft-agent/pkg/model"

	einomodel "github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/gin-gonic/gin"
)

func main() {
	ctx := context.Background()

	// 1. 初始化存储
	userStore := auth.NewMemoryUserStore()
	sessionStore := auth.NewMemorySessionStore()
	defer sessionStore.Close()
	aclChecker := auth.NewMemoryACLChecker()

	// 2. 初始化工具注册中心
	registry := toolreg.NewToolRegistry()
	if err := registerTools(registry); err != nil {
		log.Fatalf("failed to register tools: %v", err)
	}

	// 3. 种子数据
	if err := seedData(userStore, aclChecker); err != nil {
		log.Fatalf("failed to seed data: %v", err)
	}

	// 4. 初始化配置存储
	settingsStore := settings.NewSettingsStore("data/settings.json")
	if err := settingsStore.Load(); err != nil {
		log.Printf("warning: failed to load settings: %v", err)
	}

	// 5. 创建 ChatModel（优先使用持久化配置，其次环境变量，最后回退Mock）
	savedSettings := settingsStore.Get()
	llmCfg := &agent.LLMConfig{
		APIKey:          firstNonEmpty(savedSettings.APIKey, os.Getenv("LLM_API_KEY")),
		BaseURL:         firstNonEmpty(savedSettings.BaseURL, os.Getenv("LLM_BASE_URL")),
		Model:           firstNonEmpty(savedSettings.Model, os.Getenv("LLM_MODEL")),
		HeaderName:      "ksyun-code-type",
		HeaderValue:     "kingsoft-agent",
		MaxRetries:      5,
		InitialBackoff:  1 * time.Second,
		MaxBackoff:      30 * time.Second,
	}
	chatModel, err := agent.NewChatModel(ctx, llmCfg)
	if err != nil {
		log.Fatalf("failed to create chat model: %v", err)
	}

	// 6. 创建 ACL 中间件
	aclMiddleware := toolreg.ACLToolMiddleware(aclChecker)

	// 6.5 创建 HITL 中断-恢复组件
	riskChecker := hitl.NewMemoryRiskChecker()
	riskChecker.Add("send_email", "发送邮件属于高风险操作，需要人工审批")
	approvalStore := hitl.NewApprovalStore()
	defer approvalStore.Close()
	hitlMiddleware := hitl.HumanApprovalMiddleware(riskChecker, approvalStore)

	// 7. 创建专家 Agent 定义
	specialistDefs := []*agent.SpecialistDef{
		{
			Name:         "MathAgent",
			IntendedUse:  "处理数学计算、算术运算等任务",
			SystemPrompt: "你是一个数学计算助手。你必须使用 calculator 工具完成计算任务，不要仅用文字描述计算过程。\n\n规则：\n- 收到计算请求时，必须调用 calculator 工具\n- 调用工具后，将工具返回的具体数值完整呈现在回复中\n- 绝对不要只回复\"我来计算\"而不给出结果\n\n示例：\n用户：计算 2+3*4\n正确操作：调用 calculator(expression=\"2+3*4\")，然后回复\"2+3*4 的计算结果是 14\"\n错误操作：回复\"我来帮你计算\"（❌ 没有调用工具，没有给出结果）",
			ToolNames:    []string{"calculator"},
		},
		{
			Name:         "SearchAgent",
			IntendedUse:  "处理文件内容搜索、模式匹配等任务",
			SystemPrompt: "你是一个文件搜索助手。你必须使用 grep_files 工具搜索文件内容。\n\n规则：\n- 收到搜索请求时，必须调用 grep_files 工具\n- 调用工具后，将搜索到的匹配结果完整呈现\n- 不要仅回复\"我来搜索\"而不给出结果",
			ToolNames:    []string{"grep_files"},
		},
		{
			Name:         "AdminAgent",
			IntendedUse:  "处理哈希计算、邮件发送等管理员工具任务",
			SystemPrompt: "你是一个管理员工具助手。你拥有 hash_compute 和 send_email 两个工具。\n\n重要规则：\n- 你必须通过调用工具完成任务，不要仅用文字描述操作\n- 当用户要求发送邮件时，必须调用 send_email 工具（传入 to、subject、body 参数），系统自动处理审批\n- 当用户要求计算哈希时，必须调用 hash_compute 工具\n- 调用工具后，将工具返回的具体结果完整呈现在回复中（如哈希值、邮件状态等）\n- 绝对不要只回复\"我来发送邮件\"或\"我来计算哈希\"而不实际调用工具\n\n示例：\n用户：发送邮件给 alice@example.com，主题：项目进展\n正确操作：调用 send_email(to=\"alice@example.com\", subject=\"项目进展\", body=\"...\")\n错误操作：回复\"好的，我来为您发送邮件\"（❌ 没有调用工具）\n\n用户：计算 hello 的 SHA256\n正确操作：调用 hash_compute(text=\"hello\", algorithm=\"sha256\")，然后回复\"hello 的 SHA256 哈希值为 2cf24dba5fb0a30e26e83b2ac5b9e29e...\"\n错误操作：回复\"我来帮你计算哈希\"（❌ 没有调用工具，没有给出哈希值）",
			ToolNames:    []string{"hash_compute", "send_email"},
		},
		{
			Name:         "GeneralAgent",
			IntendedUse:  "处理日常对话、知识问答等通用任务",
			SystemPrompt: "你是一个通用问答助手。直接回答用户问题，不需要使用工具。",
			ToolNames:    []string{},
		},
	}

	// 8. 构建专家 Agent 和 Supervisor
	toolLookup := func(names []string) []einotool.BaseTool {
		return registry.Filter(names)
	}

	_, specialists, err := agent.BuildSpecialists(ctx, chatModel, specialistDefs, toolLookup, aclMiddleware, hitlMiddleware)
	if err != nil {
		log.Fatalf("failed to build specialists: %v", err)
	}

	supervisor, err := agent.CreateSupervisor(ctx, chatModel, specialists)
	if err != nil {
		log.Fatalf("failed to create supervisor: %v", err)
	}

	// 9. 创建意图风险兜底检查器（确保高风险操作100%需要审批）
	intentChecker := agent.NewMemoryIntentRiskChecker()
	intentChecker.AddPattern(
		[]string{"发送邮件", "发邮件", "发一封邮件", "send email", "email", "邮件"},
		"send_email",
		"发送邮件属于高风险操作，需要人工审批",
	)

	// 10. 创建上下文管理组件
	messageStore := ctxmgr.NewMemoryMessageStore()

	// ContextManager 使用同一个 chatModel（ToolCallingChatModel 满足 BaseChatModel 接口）
	var baseChatModel einomodel.BaseChatModel = chatModel
	contextConfig := ctxmgr.ContextManagerConfig{
		MaxMessages:      20,
		MaxTokens:        8000,
		SummaryThreshold: 0.8,
		ChatModel:        baseChatModel,
	}
	contextManager := ctxmgr.NewContextManager(contextConfig)
	tokenCounter := &ctxmgr.DefaultTokenCounter{}
	contextHandler := ctxmgr.NewContextHandler(messageStore, contextManager, tokenCounter)

	// 10. 创建 Handler 和中间件
	authHandler := auth.NewAuthHandler(userStore, sessionStore, aclChecker)
	authMiddlewareGin := auth.AuthMiddleware(sessionStore, userStore, aclChecker)
	agentHandler := agent.NewAgentHandler(supervisor, registry, aclChecker, specialistDefs, approvalStore, intentChecker, messageStore, contextManager)

	// 11. 创建配置 Handler（带重建回调）
	settingsHandler := settings.NewSettingsHandler(settingsStore, func(s settings.LLMSettings) error {
		newCfg := &agent.LLMConfig{
			APIKey:          s.APIKey,
			BaseURL:         s.BaseURL,
			Model:           s.Model,
			HeaderName:      "ksyun-code-type",
			HeaderValue:     "kingsoft-agent",
			MaxRetries:      5,
			InitialBackoff:  1 * time.Second,
			MaxBackoff:      30 * time.Second,
		}
		newChatModel, err := agent.NewChatModel(ctx, newCfg)
		if err != nil {
			return fmt.Errorf("创建ChatModel失败: %w", err)
		}

		// 重建 Supervisor
		if err := agentHandler.RebuildSupervisor(ctx, newChatModel, toolLookup, aclMiddleware, hitlMiddleware); err != nil {
			return err
		}

		// 同步更新上下文管理器的 ChatModel（用于摘要压缩）
		var newBaseChatModel einomodel.BaseChatModel = newChatModel
		newContextConfig := contextConfig
		newContextConfig.ChatModel = newBaseChatModel
		contextManager.SetConfig(newContextConfig)

		return nil
	})

	// 11. 启动 HTTP 服务
	r := gin.Default()

	// 静态前端页面（Vite 构建产物）
	r.Static("/assets", "./web/dist/assets")
	r.StaticFile("/", "./web/dist/index.html")
	r.StaticFile("/index.html", "./web/dist/index.html")
	r.NoRoute(func(c *gin.Context) {
		c.File("./web/dist/index.html")
	})

	api.SetupRoutes(r, authHandler, authMiddlewareGin, agentHandler, settingsHandler, contextHandler)

	addr := ":8080"
	fmt.Printf("Kingsoft Agent Framework starting on %s\n", addr)
	fmt.Println("Preset accounts: admin/admin123, visitor/visitor123")
	fmt.Printf("Registered tools: %v\n", registry.Names())
	fmt.Printf("Specialist agents: %v\n", agentDefNames(specialistDefs))
	fmt.Printf("LLM configured: %v\n", settingsStore.IsConfigured())
	if err := r.Run(addr); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}

// registerTools 注册所有内置工具
func registerTools(registry *toolreg.ToolRegistry) error {
	type toolCreator struct {
		name string
		fn   func() (einotool.InvokableTool, error)
	}

	creators := []toolCreator{
		{"calculator", tools.NewCalculatorTool},
		{"grep_files", tools.NewGrepFilesTool},
		{"hash_compute", tools.NewHashComputeTool},
		{"send_email", tools.NewSendEmailTool},
	}

	for _, c := range creators {
		t, err := c.fn()
		if err != nil {
			return fmt.Errorf("create tool %s: %w", c.name, err)
		}
		if err := registry.Register(t); err != nil {
			return fmt.Errorf("register tool %s: %w", c.name, err)
		}
	}

	return nil
}

// seedData 初始化预置角色、用户和权限数据
func seedData(userStore *auth.MemoryUserStore, aclChecker *auth.MemoryACLChecker) error {
	// 预置角色
	roles := []*pkgmodel.Role{
		{ID: 1, Name: pkgmodel.RoleAdmin, Description: "管理员，可调用所有工具"},
		{ID: 2, Name: pkgmodel.RoleVisitor, Description: "访客，仅可调用基础查询工具"},
	}

	// 预置用户
	users := []struct {
		Username string
		Password string
		RoleID   int64
	}{
		{Username: "admin", Password: "admin123", RoleID: 1},
		{Username: "visitor", Password: "visitor123", RoleID: 2},
	}

	if err := userStore.Seed(roles, users); err != nil {
		return fmt.Errorf("seed users: %w", err)
	}

	// admin 为超级角色，无需逐条授权
	aclChecker.AddSuperRole(pkgmodel.RoleAdmin)

	// visitor 可调用的工具权限（DOC-02 查询类工具 + DOC-01 遗留权限）
	visitorPerms := []pkgmodel.Permission{
		// 查询类
		{ID: 1, ToolName: "calculator", Action: "execute", Description: "数学计算"},
		{ID: 2, ToolName: "grep_files", Action: "execute", Description: "搜索文件"},
		// hash_compute 为管理员专属，不授予 visitor
	}
	for _, p := range visitorPerms {
		aclChecker.Grant(pkgmodel.RoleVisitor, p)
	}

	return nil
}

// agentDefNames 获取 Agent 定义名称列表
func agentDefNames(defs []*agent.SpecialistDef) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}

// firstNonEmpty 返回第一个非空字符串
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
