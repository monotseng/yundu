import { ConfigProvider } from "antd";
import enUSAntd from "antd/locale/en_US";
import zhCNAntd from "antd/locale/zh_CN";
import { createContext, useContext, useEffect, useMemo, useState } from "react";
import { enUI, enUS } from "@/locales/en-US";
import { zhCN } from "@/locales/zh-CN";

export type Language = "zh-CN" | "en-US";
const storageKey = "yundu.language";
let activeLanguage: Language =
  localStorage.getItem(storageKey) === "en-US" ? "en-US" : "zh-CN";

const codeTranslations = new Map<string, string>();
for (const namespace of Object.keys(zhCN) as Array<keyof typeof zhCN>) {
  const zhValues = zhCN[namespace] as Record<string, string>;
  const enValues = enUS[namespace] as Record<string, string>;
  for (const code of Object.keys(zhValues))
    codeTranslations.set(zhValues[code], enValues[code] || code);
}
for (const [zh, en] of Object.entries(enUI)) codeTranslations.set(zh, en);
const reverseTranslations = new Map(
  Array.from(codeTranslations, ([zh, en]) => [en, zh]),
);

export function getLanguage(): Language {
  return activeLanguage;
}

export function translateInterfaceText(
  value: string,
  language = activeLanguage,
): string {
  if (!value) return value;
  const translated =
    language === "en-US"
      ? codeTranslations.get(value.trim())
      : reverseTranslations.get(value.trim());
  if (!translated) return value;
  const leading = value.match(/^\s*/)?.[0] || "";
  const trailing = value.match(/\s*$/)?.[0] || "";
  return leading + translated + trailing;
}

type I18nValue = {
  language: Language;
  setLanguage: (language: Language) => void;
  toggleLanguage: () => void;
};
const I18nContext = createContext<I18nValue>({
  language: "zh-CN",
  setLanguage: () => undefined,
  toggleLanguage: () => undefined,
});

function translateDOM(root: ParentNode, language: Language) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const nodes: Text[] = [];
  while (walker.nextNode()) nodes.push(walker.currentNode as Text);
  for (const node of nodes)
    node.nodeValue = translateInterfaceText(node.nodeValue || "", language);
  if (root instanceof Element) {
    for (const attribute of ["placeholder", "title", "aria-label"]) {
      const current = root.getAttribute(attribute);
      if (current)
        root.setAttribute(attribute, translateInterfaceText(current, language));
    }
  }
  for (const element of root.querySelectorAll?.(
    "[placeholder],[title],[aria-label]",
  ) || []) {
    for (const attribute of ["placeholder", "title", "aria-label"]) {
      const current = element.getAttribute(attribute);
      if (current)
        element.setAttribute(
          attribute,
          translateInterfaceText(current, language),
        );
    }
  }
}

export function I18nProvider({ children }: { children: React.ReactNode }) {
  const [language, updateLanguage] = useState<Language>(activeLanguage);
  const setLanguage = (next: Language) => {
    activeLanguage = next;
    localStorage.setItem(storageKey, next);
    document.documentElement.lang = next;
    updateLanguage(next);
  };
  useEffect(() => {
    document.documentElement.lang = language;
    document.title =
      language === "en-US"
        ? "Yundu File Exchange Platform"
        : "云渡文件交换平台";
    translateDOM(document.body, language);
    const observer = new MutationObserver((mutations) => {
      for (const mutation of mutations) {
        for (const node of mutation.addedNodes) {
          if (node.nodeType === Node.TEXT_NODE)
            node.nodeValue = translateInterfaceText(
              node.nodeValue || "",
              language,
            );
          else if (node instanceof Element) translateDOM(node, language);
        }
      }
    });
    observer.observe(document.body, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, [language]);
  const value = useMemo(
    () => ({
      language,
      setLanguage,
      toggleLanguage: () =>
        setLanguage(language === "zh-CN" ? "en-US" : "zh-CN"),
    }),
    [language],
  );
  return (
    <I18nContext.Provider value={value}>
      <ConfigProvider locale={language === "en-US" ? enUSAntd : zhCNAntd}>
        {children}
      </ConfigProvider>
    </I18nContext.Provider>
  );
}

export const useI18n = () => useContext(I18nContext);
