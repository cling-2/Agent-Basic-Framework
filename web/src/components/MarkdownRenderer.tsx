import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { Components } from 'react-markdown'

interface MarkdownRendererProps {
  content: string
}

// 自定义组件映射：为每种 Markdown 元素指定 className
const components: Components = {
  table: ({ children, ...props }) => (
    <div className="md-table-wrapper">
      <table className="md-table" {...props}>{children}</table>
    </div>
  ),
  thead: ({ children, ...props }) => <thead {...props}>{children}</thead>,
  tbody: ({ children, ...props }) => <tbody {...props}>{children}</tbody>,
  tr: ({ children, ...props }) => <tr {...props}>{children}</tr>,
  th: ({ children, ...props }) => <th {...props}>{children}</th>,
  td: ({ children, ...props }) => <td {...props}>{children}</td>,
  blockquote: ({ children, ...props }) => (
    <blockquote className="md-blockquote" {...props}>{children}</blockquote>
  ),
  pre: ({ children, ...props }) => <pre className="md-code-block" {...props}>{children}</pre>,
  code: ({ className, children, ...props }) => {
    // 行内代码 vs 代码块：代码块由 <pre> 包裹，有 className（语言标识）
    const isBlock = className != null
    if (isBlock) {
      return <code className={className} {...props}>{children}</code>
    }
    return <code className="md-inline-code" {...props}>{children}</code>
  },
  p: ({ children, ...props }) => <p className="md-paragraph" {...props}>{children}</p>,
  ul: ({ children, ...props }) => <ul className="md-list" {...props}>{children}</ul>,
  ol: ({ children, ...props }) => <ol className="md-list" {...props}>{children}</ol>,
  li: ({ children, ...props }) => <li {...props}>{children}</li>,
  h1: ({ children, ...props }) => <h1 className="md-heading md-h1" {...props}>{children}</h1>,
  h2: ({ children, ...props }) => <h2 className="md-heading md-h2" {...props}>{children}</h2>,
  h3: ({ children, ...props }) => <h3 className="md-heading md-h3" {...props}>{children}</h3>,
  h4: ({ children, ...props }) => <h4 className="md-heading md-h4" {...props}>{children}</h4>,
  h5: ({ children, ...props }) => <h5 className="md-heading md-h5" {...props}>{children}</h5>,
  h6: ({ children, ...props }) => <h6 className="md-heading md-h6" {...props}>{children}</h6>,
  a: ({ children, ...props }) => <a className="md-link" target="_blank" rel="noopener noreferrer" {...props}>{children}</a>,
  hr: ({ ...props }) => <hr className="md-hr" {...props} />,
  strong: ({ children, ...props }) => <strong {...props}>{children}</strong>,
  em: ({ children, ...props }) => <em {...props}>{children}</em>,
}

export default function MarkdownRenderer({ content }: MarkdownRendererProps) {
  return (
    <div className="markdown-body">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {content}
      </ReactMarkdown>
    </div>
  )
}
