import { zhCN, type MessageNamespace } from "@/locales/zh-CN";

// The locale object is intentionally isolated from business components. A
// later i18n runtime only needs to replace this catalog lookup.
export function translateCode(namespace: MessageNamespace, value: unknown, fallback = "—"): string {
  if (value === null || value === undefined || value === "") return fallback;
  const code = String(value);
  const catalog = zhCN[namespace] as Record<string, string>;
  return catalog[code] || code;
}

export const statusText = (value: unknown) => translateCode("status", value);
export const directionText = (value: unknown) => translateCode("direction", value);
export const classificationText = (value: unknown) => translateCode("classification", value);
export const zoneText = (value: unknown) => translateCode("zone", value);
export const integrationTypeText = (value: unknown) => translateCode("integrationType", value);
export const checkTypeText = (value: unknown) => translateCode("checkType", value);
