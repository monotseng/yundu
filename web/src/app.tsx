import "./global.less";
import { I18nProvider } from "./i18n";
export const rootContainer = (container: React.ReactNode) => (
  <I18nProvider>{container}</I18nProvider>
);
