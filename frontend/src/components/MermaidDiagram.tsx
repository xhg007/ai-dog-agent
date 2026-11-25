import { useEffect, useId, useState } from 'react'

type MermaidApi = typeof import('mermaid')['default']

const SHADES = [
  '#111111', '#161616', '#1b1b1b', '#202020', '#252525', '#2a2a2a',
  '#2f2f2f', '#343434', '#393939', '#3e3e3e', '#434343', '#484848',
]

// 饼图需要更高的明度差，全部用 #111 一档会在深色底上看不出扇区
const PIE_SHADES = [
  '#1c1c1c', '#232323', '#2a2a2a', '#313131', '#383838', '#3f3f3f',
  '#464646', '#4d4d4d', '#545454', '#5b5b5b', '#626262', '#696969',
]

function palette() {
  const root = getComputedStyle(document.documentElement)
  const v = (name: string, fallback: string) => root.getPropertyValue(name).trim() || fallback
  const bg = v('--background', '#0b0b0b')
  const surface = v('--popover', '#161616')
  const card = v('--accent', '#1c1c1c')
  const fg = v('--foreground', '#ededed')
  const dim = v('--muted-foreground', '#a3a3a3')
  const border = v('--border-strong', '#3a3a3a')
  const line = '#5f5f5f'
  const fontFamily = getComputedStyle(document.body).fontFamily || 'sans-serif'
  return { bg, surface, card, fg, dim, border, line, fontFamily }
}

function themeVariables(p: ReturnType<typeof palette>) {
  const series: Record<string, string> = {}
  SHADES.forEach((c, i) => {
    series[`pie${i + 1}`] = PIE_SHADES[i] ?? c
    series[`cScale${i}`] = c
    series[`cScaleLabel${i}`] = p.fg
    series[`fillType${i}`] = c
    if (i < 8) {
      series[`git${i}`] = c
      series[`gitInv${i}`] = p.bg
    }
  })

  return {
    ...series,
    darkMode: true,
    useGradient: false,
    dropShadow: 'none',
    background: 'transparent',
    primaryColor: p.surface,
    primaryTextColor: p.fg,
    primaryBorderColor: p.border,
    secondaryColor: p.card,
    secondaryTextColor: p.fg,
    secondaryBorderColor: p.border,
    tertiaryColor: p.bg,
    tertiaryTextColor: p.fg,
    tertiaryBorderColor: p.border,
    lineColor: p.line,
    textColor: p.fg,
    mainBkg: p.surface,
    mainContrastColor: p.fg,
    nodeBkg: p.surface,
    nodeBorder: p.border,
    clusterBkg: p.bg,
    clusterBorder: p.border,
    edgeLabelBackground: p.bg,
    labelBackground: p.bg,
    titleColor: p.fg,
    labelColor: p.fg,
    // sequence
    actorBkg: p.surface,
    actorBorder: p.border,
    actorTextColor: p.fg,
    actorLineColor: p.line,
    signalColor: p.line,
    signalTextColor: p.fg,
    labelBoxBkgColor: p.surface,
    labelBoxBorderColor: p.border,
    labelTextColor: p.fg,
    loopTextColor: p.fg,
    noteBkgColor: p.card,
    noteBorderColor: p.border,
    noteTextColor: p.fg,
    activationBkgColor: p.surface,
    activationBorderColor: p.border,
    sequenceNumberColor: p.bg,
    // state
    transitionColor: p.line,
    transitionLabelColor: p.fg,
    stateLabelColor: p.fg,
    stateBkg: p.surface,
    compositeBackground: p.bg,
    compositeBorder: p.border,
    compositeTitleBackground: p.surface,
    innerEndBackground: p.bg,
    specialStateColor: p.fg,
    altBackground: p.card,
    // class / ER
    classText: p.fg,
    attributeBackgroundColorOdd: p.card,
    attributeBackgroundColorEven: p.bg,
    rowOdd: p.card,
    rowEven: p.bg,
    // gantt
    sectionBkgColor: p.bg,
    sectionBkgColor2: p.bg,
    altSectionBkgColor: p.bg,
    taskBkgColor: p.surface,
    taskBorderColor: p.border,
    taskTextColor: p.fg,
    taskTextLightColor: p.fg,
    taskTextOutsideColor: p.fg,
    taskTextClickableColor: p.fg,
    taskTextDarkColor: p.fg,
    activeTaskBkgColor: p.card,
    activeTaskBorderColor: p.fg,
    gridColor: '#2a2a2a',
    doneTaskBkgColor: '#242424',
    doneTaskBorderColor: p.border,
    critBkgColor: '#2a1515',
    critBorderColor: '#f85149',
    todayLineColor: p.dim,
    vertLineColor: p.dim,
    // pie
    pieTitleTextColor: p.fg,
    pieSectionTextColor: p.fg,
    pieLegendTextColor: p.fg,
    pieStrokeColor: p.border,
    pieOuterStrokeColor: p.border,
    // quadrant
    quadrant1Fill: p.surface,
    quadrant2Fill: '#141414',
    quadrant3Fill: '#101010',
    quadrant4Fill: p.bg,
    quadrant1TextFill: p.fg,
    quadrant2TextFill: p.fg,
    quadrant3TextFill: p.fg,
    quadrant4TextFill: p.fg,
    quadrantPointFill: p.fg,
    quadrantPointTextFill: p.fg,
    quadrantXAxisTextFill: p.dim,
    quadrantYAxisTextFill: p.dim,
    quadrantInternalBorderStrokeFill: p.border,
    quadrantExternalBorderStrokeFill: p.border,
    quadrantTitleFill: p.fg,
    // git
    commitLabelColor: p.fg,
    commitLabelBackground: p.bg,
    tagLabelColor: p.fg,
    tagLabelBackground: p.surface,
    // venn
    vennTitleTextColor: p.fg,
    vennSetTextColor: p.fg,
    // xy chart
    xyChart: {
      backgroundColor: 'transparent',
      titleColor: p.fg,
      xAxisLabelColor: p.fg,
      xAxisTitleColor: p.fg,
      xAxisTickColor: p.dim,
      xAxisLineColor: p.border,
      yAxisLabelColor: p.fg,
      yAxisTitleColor: p.fg,
      yAxisTickColor: p.dim,
      yAxisLineColor: p.border,
      plotColorPalette: `${p.dim},${p.fg},#7a7a7a,#b0b0b0`,
    },
    fontFamily: p.fontFamily,
    fontSize: '13px',
  }
}

function themeCss(p: ReturnType<typeof palette>) {
  return `
    .label, .nodeLabel, .edgeLabel, .messageText, .noteText, .loopText,
    .titleText, .classTitle, .stateLabel, .transitionLabel,
    text, tspan { fill: ${p.fg} !important; color: ${p.fg} !important; }

    .edgePaths path, .edgePath path, .flowchart-link, path.relation,
    .messageLine0, .messageLine1, .transition, .today line,
    .grid line, .tick line, .xychart .plot .line,
    [class*="section-edge"] { stroke: ${p.line} !important; }

    .node rect, .node circle, .node ellipse, .node polygon,
    .classGroup rect, .stateGroup rect, .actor, .labelBox {
      fill: ${p.surface} !important;
      stroke: ${p.border} !important;
    }

    .row-rect-odd path { fill: ${p.card} !important; }
    .row-rect-even path { fill: ${p.bg} !important; }
    .section { fill: ${p.bg} !important; }
    .pieCircle, .pieOuterCircle { stroke: ${p.line} !important; }
    marker path { fill: ${p.line} !important; stroke: ${p.line} !important; }
    path[stroke="black"], line[stroke="black"] { stroke: ${p.line} !important; }
    rect[fill="white"], rect[fill="#fff"] { fill: ${p.bg} !important; }
  `
}

let mermaidPromise: Promise<MermaidApi> | null = null
let renderChain: Promise<unknown> = Promise.resolve()

function loadMermaid() {
  if (!mermaidPromise) {
    mermaidPromise = import('mermaid').then(({ default: mermaid }) => {
      const p = palette()
      mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'strict',
        theme: 'dark',
        themeVariables: themeVariables(p),
        themeCSS: themeCss(p),
        flowchart: { htmlLabels: true, curve: 'basis' },
      })
      return mermaid
    })
  }
  return mermaidPromise
}

function renderDiagram(id: string, code: string) {
  const task = renderChain.then(async () => {
    const mermaid = await loadMermaid()
    return mermaid.render(id, code)
  })
  renderChain = task.catch(() => undefined)
  return task
}

export default function MermaidDiagram({ chart }: { chart: string }) {
  const rawId = useId()
  const id = `mermaid-${rawId.replace(/[^a-zA-Z0-9_-]/g, '')}`
  const code = chart.trim()
  const [svg, setSvg] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    setSvg('')
    setError('')
    if (!code) return

    renderDiagram(id, code)
      .then(({ svg }) => {
        if (!cancelled) setSvg(svg)
      })
      .catch((e: any) => {
        if (!cancelled) setError(e?.message ?? String(e))
      })

    return () => { cancelled = true }
  }, [code, id])

  if (error) {
    return (
      <div className="mermaid-block mermaid-block-error">
        <div className="mermaid-error">Mermaid 渲染失败：{error}</div>
        <pre className="mermaid-source">{code}</pre>
      </div>
    )
  }

  if (!svg) {
    return <div className="mermaid-block mermaid-loading">Mermaid 渲染中…</div>
  }

  // Mermaid 在 securityLevel=strict 下会清洗标签；这里渲染的是它生成的 SVG。
  return <div className="mermaid-block" dangerouslySetInnerHTML={{ __html: svg }} />
}
