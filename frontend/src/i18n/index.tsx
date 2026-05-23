/* Tiny in-process i18n.
 *
 * Why hand-rolled (not react-i18next):
 *   - Two locales (en/ru), about 150 keys — the full surface area is
 *     smaller than the configuration block of a library solution.
 *   - The TypeScript `TranslationKey` union catches typos at build
 *     time, which the lib's runtime fallback wouldn't.
 *   - One file to read for the next person joining the project.
 *
 * Persistence: localStorage["cicd-ml.locale"]. The first visit picks
 * locale from `navigator.language` (so a Russian browser opens in
 * Russian without clicking anything); the choice from /setup is then
 * stored and honoured everywhere.
 */
import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import { en } from "./en";
import { ru } from "./ru";
import type { TranslationKey } from "./types";

export type Locale = "en" | "ru";

const DICTS: Record<Locale, Record<TranslationKey, string>> = { en, ru };
const STORAGE_KEY = "cicd-ml.locale";

type Ctx = {
  locale: Locale;
  setLocale: (l: Locale) => void;
};

const LocaleCtx = createContext<Ctx>({ locale: "en", setLocale: () => {} });

function pickInitialLocale(): Locale {
  if (typeof window === "undefined") return "en";
  const stored = window.localStorage.getItem(STORAGE_KEY) as Locale | null;
  if (stored === "en" || stored === "ru") return stored;
  // Fallback to browser language. Anything that starts with "ru-" or
  // is plain "ru" gets Russian; everything else is English.
  const browser = (window.navigator.language || "").toLowerCase();
  return browser.startsWith("ru") ? "ru" : "en";
}

export function LocaleProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(pickInitialLocale);
  const setLocale = useCallback((l: Locale) => {
    setLocaleState(l);
    try {
      window.localStorage.setItem(STORAGE_KEY, l);
    } catch {
      // Storage can fail in private mode — fine, the state still flips.
    }
    // Mirror onto <html lang="…"> for accessibility / browser hyphenation.
    document.documentElement.lang = l;
  }, []);

  const value = useMemo<Ctx>(() => ({ locale, setLocale }), [locale, setLocale]);
  return <LocaleCtx.Provider value={value}>{children}</LocaleCtx.Provider>;
}

export function useLocale(): Ctx {
  return useContext(LocaleCtx);
}

/* useT returns a translator function `t(key, vars?)`.
 *
 * Variable interpolation: `t("setup.months", {n: 6})` looks for the
 * placeholder `{n}` in the value string. The format intentionally
 * mirrors what Go's `fmt.Sprintf("%s", ...)` does in the backend
 * error envelope `user_action` field — same shape across the stack.
 */
export function useT(): (
  key: TranslationKey,
  vars?: Record<string, string | number>,
) => string {
  const { locale } = useLocale();
  return useCallback(
    (key, vars) => {
      const dict = DICTS[locale];
      const raw = dict[key] ?? DICTS.en[key] ?? key;
      if (!vars) return raw;
      return Object.entries(vars).reduce(
        (acc, [k, v]) => acc.replace(new RegExp(`\\{${k}\\}`, "g"), String(v)),
        raw,
      );
    },
    [locale],
  );
}

/* Inline language switcher widget — used on /setup (top-right corner)
 * and inside the AppShell footer.
 *
 * Two flat pills inside one outlined container. Single-colour accent
 * border on the active pill so it reads as "you're here" without the
 * usual filled-button heaviness that would compete with primary
 * actions next to it.
 */
export function LanguageSwitcher({ compact = false }: { compact?: boolean }) {
  const { locale, setLocale } = useLocale();
  const items: { id: Locale; label: string }[] = [
    { id: "en", label: "EN" },
    { id: "ru", label: "RU" },
  ];
  return (
    <div
      role="group"
      aria-label="Language switcher"
      style={{
        display: "inline-flex",
        border: "1px solid var(--border-subtle)",
        borderRadius: "var(--r-6)",
        overflow: "hidden",
        fontFamily: "var(--font-mono)",
        fontSize: compact ? 11 : 12,
      }}
    >
      {items.map((it, i) => {
        const active = locale === it.id;
        return (
          <button
            key={it.id}
            onClick={() => setLocale(it.id)}
            style={{
              padding: compact ? "3px 8px" : "4px 10px",
              borderLeft: i === 0 ? "none" : "1px solid var(--border-subtle)",
              background: active ? "var(--bg-elevated)" : "transparent",
              color: active ? "var(--text-primary)" : "var(--text-tertiary)",
              cursor: active ? "default" : "pointer",
              letterSpacing: "0.06em",
              boxShadow: active ? "inset 0 0 0 1px var(--accent-soft)" : undefined,
            }}
          >
            {it.label}
          </button>
        );
      })}
    </div>
  );
}
