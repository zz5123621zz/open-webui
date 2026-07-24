import { writable } from 'svelte/store';

export type Locale = 'zh-CN' | 'en';

function initialLocale(): Locale {
  const saved = globalThis.localStorage?.getItem('personal-chat-locale');
  if (saved === 'zh-CN' || saved === 'en') return saved;
  return 'zh-CN';
}

export const locale = writable<Locale>(initialLocale());

export function setLocale(value: Locale): void {
  locale.set(value);
  globalThis.localStorage?.setItem('personal-chat-locale', value);
  if (globalThis.document) document.documentElement.lang = value;
}

export function translate(value: Locale, chinese: string, english: string): string {
  return value === 'zh-CN' ? chinese : english;
}
