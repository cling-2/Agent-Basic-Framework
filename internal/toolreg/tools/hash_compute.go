package tools

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ---------- HashCompute Tool ----------

type HashComputeInput struct {
	Text      string `json:"text" jsonschema:"description=要计算哈希值的文本"`
	Algorithm string `json:"algorithm" jsonschema:"description=哈希算法，可选 sha256 或 md5，默认 sha256"`
}

type HashComputeOutput struct {
	Algorithm string `json:"algorithm"`
	Hash      string `json:"hash"`
	Summary   string `json:"summary"`
}

// NewHashComputeTool 创建哈希计算工具（纯本地计算，管理员类）
func NewHashComputeTool() (tool.InvokableTool, error) {
	return utils.InferTool[HashComputeInput, HashComputeOutput](
		"hash_compute",
		"计算文本的哈希值，支持 SHA256 和 MD5 算法",
		func(ctx context.Context, input HashComputeInput) (HashComputeOutput, error) {
			algo := strings.ToLower(strings.TrimSpace(input.Algorithm))
			if algo == "" {
				algo = "sha256"
			}

			var hashStr string
			switch algo {
			case "md5":
				h := md5.Sum([]byte(input.Text))
				hashStr = hex.EncodeToString(h[:])
			case "sha256":
				h := sha256.Sum256([]byte(input.Text))
				hashStr = hex.EncodeToString(h[:])
			default:
				return HashComputeOutput{}, fmt.Errorf("不支持的算法: %s，请使用 sha256 或 md5", algo)
			}

			// 生成人类可读摘要
			// 截断过长的文本用于展示
			displayText := input.Text
			if len(displayText) > 50 {
				displayText = displayText[:50] + "..."
			}
			summary := fmt.Sprintf("文本「%s」的 %s 哈希值为: %s", displayText, strings.ToUpper(algo), hashStr)

			return HashComputeOutput{
				Algorithm: algo,
				Hash:     hashStr,
				Summary:  summary,
			}, nil
		},
	)
}
