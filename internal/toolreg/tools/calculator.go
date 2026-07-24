package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ---------- Calculator Tool ----------

type CalculatorInput struct {
	Expression string `json:"expression" jsonschema:"description=数学表达式，支持加减乘除，如 2+3*4"`
}

type CalculatorOutput struct {
	Result  string `json:"result"`
	Summary string `json:"summary"`
}

// NewCalculatorTool 创建计算器工具
func NewCalculatorTool() (tool.InvokableTool, error) {
	return utils.InferTool[CalculatorInput, CalculatorOutput](
		"calculator",
		"执行数学计算，支持加减乘除等基本运算",
		func(ctx context.Context, input CalculatorInput) (CalculatorOutput, error) {
			result, err := evalSimpleExpr(input.Expression)
			if err != nil {
				return CalculatorOutput{
					Result:  "计算错误: " + err.Error(),
					Summary: "计算出错: " + err.Error(),
				}, nil
			}
			return CalculatorOutput{
				Result:  result,
				Summary: fmt.Sprintf("计算结果: %s = %s", input.Expression, result),
			}, nil
		},
	)
}

// evalSimpleExpr 简易算术表达式求值（仅支持整数加减乘除，不依赖第三方库）
func evalSimpleExpr(expr string) (string, error) {
	expr = strings.ReplaceAll(expr, " ", "")
	if expr == "" {
		return "", nil
	}

	// 按运算符优先级：先处理加减，再处理乘除
	result, err := evalAddSub(expr)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(result), nil
}

func evalAddSub(expr string) (int, error) {
	// 找到最外层的 + 或 -（不在括号内的）
	depth := 0
	for i := len(expr) - 1; i > 0; i-- {
		ch := expr[i]
		if ch == ')' {
			depth++
		} else if ch == '(' {
			depth--
		} else if depth == 0 && (ch == '+' || ch == '-') {
			left, err := evalAddSub(expr[:i])
			if err != nil {
				return 0, err
			}
			right, err := evalMulDiv(expr[i+1:])
			if err != nil {
				return 0, err
			}
			if ch == '+' {
				return left + right, nil
			}
			return left - right, nil
		}
	}
	return evalMulDiv(expr)
}

func evalMulDiv(expr string) (int, error) {
	depth := 0
	for i := len(expr) - 1; i > 0; i-- {
		ch := expr[i]
		if ch == ')' {
			depth++
		} else if ch == '(' {
			depth--
		} else if depth == 0 && (ch == '*' || ch == '/') {
			left, err := evalMulDiv(expr[:i])
			if err != nil {
				return 0, err
			}
			right, err := evalAtom(expr[i+1:])
			if err != nil {
				return 0, err
			}
			if ch == '*' {
				return left * right, nil
			}
			if right == 0 {
				return 0, nil // 除零返回 0
			}
			return left / right, nil
		}
	}
	return evalAtom(expr)
}

func evalAtom(expr string) (int, error) {
	expr = strings.TrimSpace(expr)
	if len(expr) >= 2 && expr[0] == '(' && expr[len(expr)-1] == ')' {
		return evalAddSub(expr[1 : len(expr)-1])
	}
	return strconv.Atoi(expr)
}
