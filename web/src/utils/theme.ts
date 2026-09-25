import { ref } from 'vue'

export type ThemePreference = 'system' | 'light' | 'dark'

const STORAGE_KEY = 'zero-api-theme'
const mediaQuery = typeof window !== 'undefined'
  ? window.matchMedia('(prefers-color-scheme: dark)')
  : null

function parsePreference(value: string | null): ThemePreference {
  return value === 'light' || value === 'dark' || value === 'system' ? value : 'system'
}

function readPreference(): ThemePreference {
  if (typeof window === 'undefined') return 'system'
  try {
    return parsePreference(window.localStorage.getItem(STORAGE_KEY))
  } catch {
    return 'system'
  }
}

export const themePreference = ref<ThemePreference>(readPreference())
export const isDarkTheme = ref(false)

function applyTheme() {
  const dark = themePreference.value === 'system'
    ? Boolean(mediaQuery?.matches)
    : themePreference.value === 'dark'
  isDarkTheme.value = dark

  if (typeof document !== 'undefined') {
    document.documentElement.dataset.theme = dark ? 'dark' : 'light'
    document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
  }
}

export function setThemePreference(preference: ThemePreference) {
  themePreference.value = preference
  try {
    window.localStorage.setItem(STORAGE_KEY, preference)
  } catch {
    // Theme still applies for this page even when storage is unavailable.
  }
  applyTheme()
}

if (mediaQuery) {
  mediaQuery.addEventListener('change', () => {
    if (themePreference.value === 'system') applyTheme()
  })
}

if (typeof window !== 'undefined') {
  window.addEventListener('storage', (event) => {
    if (event.key !== STORAGE_KEY) return
    themePreference.value = parsePreference(event.newValue)
    applyTheme()
  })
}

applyTheme()

export function useTheme() {
  return { themePreference, isDarkTheme, setThemePreference }
}
