export const GLASS_ALPHA_KEY = 'aidog.glassAlpha'
export const GLASS_BLUR_KEY = 'aidog.glassBlur'

export const DEFAULT_GLASS_ALPHA = 0.7
export const DEFAULT_GLASS_BLUR = 18
export const MIN_GLASS_ALPHA = 0.65
export const MAX_GLASS_ALPHA = 0.95
export const MIN_GLASS_BLUR = 0
export const MAX_GLASS_BLUR = 32

const clamp = (v: number, min: number, max: number) => Math.min(max, Math.max(min, v))

// 透明度下限 65%：再低会让毛玻璃后方的亮部/噪点透上来，正文与次级文字对比度会掉出 AA。
export function loadGlass() {
  const rawAlpha = localStorage.getItem(GLASS_ALPHA_KEY)
  const rawBlur = localStorage.getItem(GLASS_BLUR_KEY)
  const alpha = rawAlpha === null ? NaN : Number(rawAlpha)
  const blur = rawBlur === null ? NaN : Number(rawBlur)
  return {
    alpha: Number.isFinite(alpha) ? clamp(alpha, MIN_GLASS_ALPHA, MAX_GLASS_ALPHA) : DEFAULT_GLASS_ALPHA,
    blur: Number.isFinite(blur) ? clamp(blur, MIN_GLASS_BLUR, MAX_GLASS_BLUR) : DEFAULT_GLASS_BLUR,
  }
}

export function applyGlass(alpha: number, blur: number) {
  const a = clamp(alpha, MIN_GLASS_ALPHA, MAX_GLASS_ALPHA)
  const b = clamp(blur, MIN_GLASS_BLUR, MAX_GLASS_BLUR)
  const root = document.documentElement
  root.style.setProperty('--glass-alpha', String(a))
  root.style.setProperty('--glass-blur', `${b}px`)
  return { alpha: a, blur: b }
}

export function saveGlass(alpha: number, blur: number) {
  localStorage.setItem(GLASS_ALPHA_KEY, String(alpha))
  localStorage.setItem(GLASS_BLUR_KEY, String(blur))
}

export function applyStoredGlass() {
  const { alpha, blur } = loadGlass()
  return applyGlass(alpha, blur)
}
