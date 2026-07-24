package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ---------- SendEmail Tool ----------

type SendEmailInput struct {
	To      string `json:"to"      jsonschema:"description=收件人邮箱地址"`
	Subject string `json:"subject" jsonschema:"description=邮件主题"`
	Body    string `json:"body"    jsonschema:"description=邮件正文"`
}

type SendEmailOutput struct {
	Status string `json:"status"`
	To     string `json:"to"`
	Summary string `json:"summary"`
}

func NewSendEmailTool() (tool.InvokableTool, error) {
	return utils.InferTool[SendEmailInput, SendEmailOutput](
		"send_email",
		"发送邮件到指定邮箱地址（模拟，需人工审批）",
		func(ctx context.Context, input SendEmailInput) (SendEmailOutput, error) {
			// 模拟发送：仅记录，不实际发送
			summary := fmt.Sprintf("邮件已发送至 %s，主题：%s", input.To, input.Subject)
			return SendEmailOutput{
				Status:  "sent",
				To:      input.To,
				Summary: summary,
			}, nil
		},
	)
}
