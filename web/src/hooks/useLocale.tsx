import { createContext, useContext, useState, useCallback, useMemo, type ReactNode } from 'react';
import zh from '../locales/zh.json';
import en from '../locales/en.json';
import antdZhCN from 'antd/locale/zh_CN';
import antdEnUS from 'antd/locale/en_US';

export type Locale = 'zh' | 'en';
type TranslationMap = Record<string, Record<string, any>>;

const translations: TranslationMap = { zh, en };

const antdLocales: Record<Locale, any> = {
  zh: antdZhCN,
  en: antdEnUS,
};

interface LocaleContextType {
  locale: Locale;
  t: (path: string, fallback?: string) => string;
  toggleLocale: () => void;
  setLocale: (locale: Locale) => void;
  antdLocale: any;
}

const LocaleContext = createContext<LocaleContextType | null>(null);

export function LocaleProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(() => {
    return (localStorage.getItem('locale') as Locale) || 'zh';
  });

  const antdLocale = antdLocales[locale];

  const t = useCallback((path: string, fallback?: string): string => {
    const parts = path.split('.');
    let value: any = translations[locale];
    for (const part of parts) {
      if (value == null) return fallback ?? path;
      value = value[part];
    }
    return (typeof value === 'string' ? value : fallback) ?? path;
  }, [locale]);

  const toggleLocale = useCallback(() => {
    const next: Locale = locale === 'zh' ? 'en' : 'zh';
    localStorage.setItem('locale', next);
    setLocaleState(next);
  }, [locale]);

  const setLocale = useCallback((l: Locale) => {
    localStorage.setItem('locale', l);
    setLocaleState(l);
  }, []);

  const value = useMemo(() => ({ locale, t, toggleLocale, setLocale, antdLocale }), [locale, t, toggleLocale, setLocale, antdLocale]);

  return (
    <LocaleContext.Provider value={value}>
      {children}
    </LocaleContext.Provider>
  );
}

export function useLocale(): LocaleContextType {
  const context = useContext(LocaleContext);
  if (!context) {
    throw new Error('useLocale must be used within a LocaleProvider');
  }
  return context;
}
