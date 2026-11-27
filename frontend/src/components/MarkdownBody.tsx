import { isValidElement, useRef, useState, type ReactNode } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import { Check, Copy } from 'lucide-react'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import remarkGemoji from 'remark-gemoji'
import rehypeRaw from 'rehype-raw'
import rehypeSanitize, { defaultSchema } from 'rehype-sanitize'
import rehypeHighlight from 'rehype-highlight'
import rehypeKatex from 'rehype-katex'
import MermaidDiagram from './MermaidDiagram'
import { copyText } from '../lib/clipboard'

// rehype-sanitize 默认会清掉 remark-math 生成的 class，
// 这里放行 math 标记，KaTeX 再把它转换成公式 HTML。
const sanitizeSchema = {
  ...defaultSchema,
  attributes: {
    ...defaultSchema.attributes,
    span: [
      ...(defaultSchema.attributes?.span ?? []),
      ['className', 'math', 'math-inline', 'math-display'],
    ],
    div: [
      ...(defaultSchema.attributes?.div ?? []),
      ['className', 'math', 'math-inline', 'math-display'],
    ],
  },
}

function CodeBlock({ children, ...props }: React.HTMLAttributes<HTMLPreElement>) {
  const ref = useRef<HTMLPreElement>(null)
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    const text = ref.current?.innerText ?? ''
    if (!(await copyText(text))) return
    setCopied(true)
    setTimeout(() => setCopied(false), 1400)
  }

  return (
    <div className="code-block">
      <pre ref={ref} {...props}>{children}</pre>
      <button
        type="button"
        className="code-copy"
        aria-label={copied ? '已复制' : '复制代码'}
        title={copied ? '已复制' : '复制代码'}
        onClick={copy}
      >
        {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      </button>
    </div>
  )
}

function nodeText(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(nodeText).join('')
  if (isValidElement<{ children?: ReactNode }>(node)) return nodeText(node.props.children)
  return ''
}

const components: Components = {
  pre: ({ node, children, ...props }) => {
    void node
    const child = Array.isArray(children) ? children[0] : children
    if (isValidElement<{ className?: string; children?: ReactNode }>(child)) {
      const cls = child.props.className ?? ''
      if (/(^|\s)language-mermaid(\s|$)/.test(cls)) {
        return <MermaidDiagram chart={nodeText(child.props.children).trim()} />
      }
    }
    return <CodeBlock {...props}>{children}</CodeBlock>
  },
  a: ({ node, href, children, ...props }) => {
    void node
    const external = !!href && /^https?:\/\//i.test(href)
    return (
      <a
        href={href}
        {...props}
        {...(external ? { target: '_blank', rel: 'noreferrer noopener' } : {})}
      >
        {children}
      </a>
    )
  },
}

export default function MarkdownBody({ content }: { content: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm, remarkMath, remarkGemoji]}
      rehypePlugins={[
        rehypeRaw,
        [rehypeSanitize, sanitizeSchema],
        [rehypeHighlight, { detect: false, ignoreMissing: true }],
        [rehypeKatex, { throwOnError: false, errorColor: '#f85149' }],
      ]}
      components={components}
    >
      {content}
    </ReactMarkdown>
  )
}
