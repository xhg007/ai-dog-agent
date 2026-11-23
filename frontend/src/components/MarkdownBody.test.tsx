import { describe, it, expect } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import MarkdownBody from './MarkdownBody'

describe('MarkdownBody', () => {
  it('将 GFM 表格渲染成 table 元素', () => {
    const html = renderToStaticMarkup(
      <MarkdownBody content={'| 能力 | 状态 |\n| --- | --- |\n| 项目探索 | ✅ |'} />,
    )
    expect(html).toContain('<table>')
    expect(html).toContain('能力')
    expect(html).toContain('项目探索')
  })

  it('渲染代码块且不对 HTML 注入原始内容', () => {
    const html = renderToStaticMarkup(
      <MarkdownBody content={'```go\nfunc main() {}\n```\n\n<script>alert(1)</script>'} />,
    )
    expect(html).toContain('<pre>')
    expect(html).toContain('<code')
    expect(html).not.toContain('<script>')
  })
})
