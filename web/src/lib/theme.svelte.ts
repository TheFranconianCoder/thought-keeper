import { t } from './i18n/index.svelte';

export type ThemeMode = 'auto' | 'light' | 'dark';

const STORAGE_KEY = 'tk-theme';
const ORDER: ThemeMode[] = ['auto', 'light', 'dark'];

function storedMode(): ThemeMode {
  try {
    const value = localStorage.getItem(STORAGE_KEY);
    if (value === 'light' || value === 'dark' || value === 'auto') return value;
  } catch {
    /* storage can be unavailable */
  }
  return 'auto';
}

export const theme = $state<{ mode: ThemeMode }>({ mode: storedMode() });

function apply(mode: ThemeMode): void {
  document.documentElement.dataset.theme = mode;
  try {
    localStorage.setItem(STORAGE_KEY, mode);
  } catch {
    /* storage can be unavailable */
  }
}

export function initTheme(): void {
  apply(theme.mode);
}

export function setTheme(mode: ThemeMode): void {
  theme.mode = mode;
  apply(mode);
}

export function cycleTheme(): void {
  const next = ORDER[(ORDER.indexOf(theme.mode) + 1) % ORDER.length];
  setTheme(next);
}

export function themeLabel(mode: ThemeMode = theme.mode): string {
  if (mode === 'auto') return t('theme.titleAuto');
  return t(mode === 'light' ? 'theme.titleLight' : 'theme.titleDark');
}

export function themeIcon(mode: ThemeMode = theme.mode): string {
  return mode === 'dark' ? 'moon' : mode === 'light' ? 'sun' : 'auto';
}
