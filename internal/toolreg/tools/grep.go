package tools

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ---------- GrepFiles Tool ----------

type GrepFilesInput struct {
	Pattern string `json:"pattern" jsonschema:"description=搜索的文本模式或正则表达式"`
	Path    string `json:"path" jsonschema:"description=搜索的目录路径，必填"`
}

type GrepFilesOutput struct {
	Matches []FileMatch `json:"matches"`
	Summary string      `json:"summary"`
}

type FileMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// 常见二进制文件扩展名，搜索时跳过
var binaryExtensions = map[string]bool{
	".exe": true, ".dll": true, ".so": true, ".dylib": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".ico": true, ".webp": true,
	".zip": true, ".tar": true, ".gz": true, ".rar": true, ".7z": true,
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	".mp3": true, ".mp4": true, ".avi": true, ".mov": true, ".wav": true,
	".class": true, ".o": true, ".a": true, ".lib": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
	".sqlite": true, ".db": true,
}

// maxMatches 限制最大返回匹配数，防止输出过多
const maxMatches = 100

// NewGrepFilesTool 创建文件内容搜索工具（真实实现，使用 filepath.WalkDir + regexp）
func NewGrepFilesTool() (tool.InvokableTool, error) {
	return utils.InferTool[GrepFilesInput, GrepFilesOutput](
		"grep_files",
		"在文件中搜索匹配指定模式的文本行，支持正则表达式",
		func(ctx context.Context, input GrepFilesInput) (GrepFilesOutput, error) {
		if input.Path == "" {
				return GrepFilesOutput{
					Matches: nil,
					Summary: "❌ 搜索路径不能为空，请提供要搜索的目录路径。例如：grep搜索 /home/user/project 中的 TODO",
				}, nil
			}

			searchPath := input.Path

			// 编译正则表达式
			re, err := regexp.Compile(input.Pattern)
			if err != nil {
				return GrepFilesOutput{}, fmt.Errorf("正则表达式编译失败: %w", err)
			}

			var matches []FileMatch

			err = filepath.WalkDir(searchPath, func(filePath string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil // 跳过无法访问的文件/目录
				}

				// 跳过 .git 等隐藏目录
				if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}

				// 只处理普通文件
				if d.IsDir() {
					return nil
				}

				// 跳过二进制文件
				ext := strings.ToLower(filepath.Ext(d.Name()))
				if binaryExtensions[ext] {
					return nil
				}

				// 已达到最大匹配数，跳过后续文件
				if len(matches) >= maxMatches {
					return nil
				}

				// 逐行扫描文件
				file, err := os.Open(filePath)
				if err != nil {
					return nil // 跳过无法打开的文件
				}
				defer file.Close()

				scanner := bufio.NewScanner(file)
				lineNum := 0
				for scanner.Scan() {
					lineNum++
					line := scanner.Text()
					if re.MatchString(line) {
						// 截断过长行
						content := line
						if len(content) > 500 {
							content = content[:500] + "..."
						}
						matches = append(matches, FileMatch{
							File:    filePath,
							Line:    lineNum,
							Content: content,
						})
						if len(matches) >= maxMatches {
							break
						}
					}
				}

				return nil
			})

			if err != nil {
				return GrepFilesOutput{}, fmt.Errorf("文件搜索失败: %w", err)
			}

			if matches == nil {
				matches = []FileMatch{}
			}

			// 生成人类可读摘要
			summary := formatGrepSummary(input.Pattern, searchPath, matches)

			return GrepFilesOutput{Matches: matches, Summary: summary}, nil
		},
	)
}

// formatGrepSummary 将搜索结果格式化为人类可读的摘要
func formatGrepSummary(pattern, path string, matches []FileMatch) string {
	if len(matches) == 0 {
		return fmt.Sprintf("在 %s 中搜索模式「%s」，未找到匹配结果。", path, pattern)
	}

	// 统计匹配的文件数
	fileSet := make(map[string]bool)
	for _, m := range matches {
		fileSet[m.File] = true
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("在 %s 中搜索模式「%s」，", path, pattern))
	sb.WriteString(fmt.Sprintf("共找到 %d 处匹配（涉及 %d 个文件）：\n", len(matches), len(fileSet)))

	// 最多显示前10条匹配详情
	limit := len(matches)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i++ {
		m := matches[i]
		sb.WriteString(fmt.Sprintf("  - %s:%d: %s\n", m.File, m.Line, m.Content))
	}
	if len(matches) > 10 {
		sb.WriteString(fmt.Sprintf("  ... 还有 %d 条匹配结果", len(matches)-10))
	}

	return sb.String()
}
