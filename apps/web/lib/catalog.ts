import { useLocale } from '@invoyis/ui';
import { catalog, type Locale } from './catalog-data';
export { catalog, type Locale };
export function useWords() {
  const locale = useLocale().locale as Locale;
  return { locale, t: catalog[locale] ?? catalog.en };
}
