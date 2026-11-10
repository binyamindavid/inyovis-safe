export const supportedLocales = ['en', 'fr', 'he'];
export function normalizeLocale(value) {
  const base = String(value ?? '').trim().split(/[-_]/, 1)[0].toLowerCase();
  return supportedLocales.includes(base) ? base : null;
}
export function firstAcceptedLocale(header) {
  return String(header ?? '').split(',').map((part, index) => {
    const [tag, ...parameters] = part.trim().split(';');
    const weight = parameters.map(value => /^\s*q\s*=\s*([0-9.]+)\s*$/i.exec(value)?.[1]).find(value => value !== undefined);
    const quality = weight === undefined ? 1 : Number(weight);
    return { locale: normalizeLocale(tag), quality, index };
  }).filter((part) => part.locale && Number.isFinite(part.quality) && part.quality > 0 && part.quality <= 1)
    .sort((a, b) => b.quality - a.quality || a.index - b.index)[0]?.locale ?? null;
}
export function resolveLocale({ account, choice, acceptLanguage }) {
  return normalizeLocale(account) ?? normalizeLocale(choice) ?? firstAcceptedLocale(acceptLanguage) ?? 'en';
}
