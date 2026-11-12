import { ref } from 'vue'

export const THEME_STORAGE_KEY = 'tiktok_theme'

// 全局共享的明暗状态，MainLayout 的切换按钮直接绑定它
export const isDark = ref(false)

function readStoredTheme() {
  try {
    const value = localStorage.getItem(THEME_STORAGE_KEY)
    return value === 'dark' || value === 'light' ? value : ''
  } catch {
    return ''
  }
}

function systemTheme() {
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

/**
 * 应用主题：同步 html.dark、color-scheme 与全局状态。
 * persist 为 false 时用于「读取系统主题」的初始化，避免把系统偏好误存成用户选择。
 */
export function applyTheme(theme, { persist = true } = {}) {
  const dark = theme === 'dark'
  const root = document.documentElement

  root.classList.toggle('dark', dark)
  root.dataset.theme = dark ? 'dark' : 'light'
  root.style.colorScheme = dark ? 'dark' : 'light'
  isDark.value = dark

  if (persist) {
    try {
      localStorage.setItem(THEME_STORAGE_KEY, dark ? 'dark' : 'light')
    } catch {
      // localStorage 不可用时仅影响持久化，不阻塞主题切换
    }
  }
}

export function initTheme() {
  const stored = readStoredTheme()
  applyTheme(stored || systemTheme(), { persist: Boolean(stored) })
}

export function toggleTheme() {
  applyTheme(isDark.value ? 'light' : 'dark')
}
