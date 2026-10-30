/**
 * The interface language.
 *
 * The catalog is a flat map of keys to sentences. A component asks for a key, never for a sentence,
 * and the English catalog is the fallback: a missing German key shows English instead of the key.
 *
 * The language is a reader preference, remembered in localStorage. The server's `language.ui` is
 * only the default for a reader who has not chosen one yet — the Status lens shows the same value,
 * so a first visit follows the vault's configuration.
 */
import { en } from './en';
import { de } from './de';

export type Locale = 'en' | 'de';

export interface LocaleOption {
  code: Locale;
  label: string;
}

export const LOCALES: readonly LocaleOption[] = [
  { code: 'en', label: 'English' },
  { code: 'de', label: 'Deutsch' },
];

const STORAGE_KEY = 'tk-locale';
const catalogs: Record<Locale, Record<string, string>> = { en, de };

export const i18n = $state<{ locale: Locale; chosen: boolean }>({ locale: 'en', chosen: false });

export function isLocale(value: string): value is Locale {
  return value === 'en' || value === 'de';
}

function storedLocale(): Locale | null {
  try {
    const value = localStorage.getItem(STORAGE_KEY);
    return value && isLocale(value) ? value : null;
  } catch {
    return null;
  }
}

function applyDocumentLanguage(locale: Locale): void {
  if (typeof document !== 'undefined') document.documentElement.lang = locale;
}

/** setLocale changes the interface language and remembers the choice. */
export function setLocale(locale: Locale, remember = true): void {
  i18n.locale = locale;
  if (remember) i18n.chosen = true;
  applyDocumentLanguage(locale);
  if (!remember) return;
  try {
    localStorage.setItem(STORAGE_KEY, locale);
  } catch {
    /* storage can be unavailable */
  }
}

/** initLocale restores the reader's choice (if any) and otherwise the server's UI default. */
export function initLocale(serverDefault = ''): void {
  const saved = storedLocale();
  if (saved) {
    setLocale(saved, false);
    i18n.chosen = true;
    return;
  }
  setLocale(isLocale(serverDefault) ? serverDefault : 'en', false);
}

/** adoptServerDefault applies the configured UI language only while the reader has not chosen one. */
export function adoptServerDefault(serverDefault: string): void {
  if (i18n.chosen || !isLocale(serverDefault)) return;
  if (i18n.locale === serverDefault) return;
  setLocale(serverDefault, false);
}

/** cycleLocale switches to the next shipped language; the Topbar's language button uses it. */
export function cycleLocale(): void {
  const index = LOCALES.findIndex((option) => option.code === i18n.locale);
  const next = LOCALES[(index + 1) % LOCALES.length];
  setLocale(next.code);
}

export function localeLabel(locale: Locale = i18n.locale): string {
  return LOCALES.find((option) => option.code === locale)?.label ?? locale;
}

function interpolate(text: string, params?: Record<string, string | number>): string {
  if (!params) return text;
  let out = text;
  for (const [name, value] of Object.entries(params)) {
    out = out.split(`{${name}}`).join(String(value));
  }
  return out;
}

/** t returns one translated sentence, interpolating `{name}` placeholders. */
export function t(key: string, params?: Record<string, string | number>): string {
  const text = catalogs[i18n.locale]?.[key] ?? en[key] ?? key;
  return interpolate(text, params);
}

/**
 * tn picks the plural form of a key: `key.one` / `key.many`, chosen by Intl.PluralRules for the
 * current language. `{count}` is interpolated like any other parameter.
 */
export function tn(key: string, count: number, params?: Record<string, string | number>): string {
  const form = new Intl.PluralRules(i18n.locale).select(count) === 'one' ? 'one' : 'many';
  const pluralKey = `${key}.${form}`;
  const table = catalogs[i18n.locale] ?? en;
  const fallbackKey = en[pluralKey] !== undefined ? pluralKey : key;
  const text = table[pluralKey] ?? en[fallbackKey] ?? key;
  return interpolate(text, { ...params, count });
}
