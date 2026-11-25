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

  it('渲染行内/块级数学公式（KaTeX）', () => {
    const html = renderToStaticMarkup(
      <MarkdownBody content={'质能方程 $E=mc^2$。\n\n$$\n\\int_0^1 x^2 dx\n$$'} />,
    )
    expect(html).toContain('class="katex"')
    expect(html).toContain('katex-display')
  })

  it('渲染 gemoji 短代码', () => {
    const html = renderToStaticMarkup(<MarkdownBody content={'状态 :smile: :+1:'} />)
    expect(html).toContain('😄')
    expect(html).toContain('👍')
  })

  it('保留安全 HTML，同时清洗脚本与事件属性', () => {
    const html = renderToStaticMarkup(
      <MarkdownBody content={'<kbd>Ctrl</kbd><img src="x" onerror="alert(1)"><script>alert(1)</script>'} />,
    )
    expect(html).toContain('<kbd>Ctrl</kbd>')
    expect(html).not.toContain('onerror')
    expect(html).not.toContain('<script>')
  })

  it('带语言的代码块进行高亮，并渲染复制按钮', () => {
    const html = renderToStaticMarkup(
      <MarkdownBody content={'```go\nfunc main() {}\n```'} />,
    )
    expect(html).toContain('hljs')
    expect(html).toContain('language-go')
    expect(html).toContain('复制代码')
  })

  it('外部链接新窗口打开并带安全 rel', () => {
    const html = renderToStaticMarkup(
      <MarkdownBody content={'[示例](https://example.com)'} />,
    )
    expect(html).toContain('target="_blank"')
    expect(html).toContain('rel="noreferrer noopener"')
  })

  it('渲染 GFM 任务列表', () => {
    const html = renderToStaticMarkup(
      <MarkdownBody content={'- [x] 完成\n- [ ] 待办'} />,
    )
    expect(html).toContain('contains-task-list')
    expect(html).toContain('task-list-item')
    expect(html).toContain('type="checkbox"')
  })

  it('mermaid 代码块渲染为图表容器而不是普通代码块', () => {
    const html = renderToStaticMarkup(
      <MarkdownBody content={'```mermaid\ngraph TD\nA-->B\n```'} />,
    )
    expect(html).toContain('mermaid-block')
    expect(html).toContain('Mermaid 渲染中')
    expect(html).not.toContain('language-mermaid')
  })
})
