// Pure helpers for the archive folder grid (#59): ordering, counts, monograms and folder colours.

/** Folder ID for documents without a linked supplier or category; the API accepts it as `supplierId=none` / `categoryId=none`. */
export const FOLDER_NONE = 'none';

/**
 * Folders in reading order for the interface language, with the missing-metadata folder last and only while it holds
 * documents. `items` and `unassigned` come from GET /document-folders.
 */
export function orderFolders(items, unassigned, locale, unassignedName) {
  const collator = new Intl.Collator(locale, { sensitivity: 'base', numeric: true });
  const sorted = [...items].sort((a, b) => collator.compare(a.displayName, b.displayName) || (a.id < b.id ? -1 : 1));
  return unassigned > 0 ? [...sorted, { id: FOLDER_NONE, displayName: unassignedName, archived: false, count: unassigned }] : sorted;
}

/** "No documents", "1 document" or "{count} documents", with the number formatted for the locale. */
export function folderCountLabel(words, count, locale) {
  if (count === 0) return words.folderCountNone;
  if (count === 1) return words.folderCountOne;
  return words.folderCountMany.replace('{count}', new Intl.NumberFormat(locale).format(count));
}

/** Up to two letters from a company name for its folder (Israel Gas → IG, ספק Café → סC). Empty when nothing usable. */
export function monogram(name) {
  const words = String(name).normalize('NFC').split(/[\s\-–—_.,/&·]+/u).filter(w => /[\p{L}\p{N}]/u.test(w));
  const first = word => [...word].find(ch => /[\p{L}\p{N}]/u.test(ch)) ?? '';
  const letters = words.length > 1 ? first(words[0]) + first(words[1]) : [...(words[0] ?? '')].filter(ch => /[\p{L}\p{N}]/u.test(ch)).slice(0, 2).join('');
  return letters.toLocaleUpperCase();
}

const tones = ['blue', 'teal', 'violet', 'amber', 'rose', 'green', 'indigo', 'orange'];
/** A stable colour for a folder, derived from its ID so it stays put when names change or folders are added. */
export function folderTone(id) {
  if (id === FOLDER_NONE) return 'grey';
  let hash = 0;
  for (const ch of String(id)) hash = (hash * 31 + ch.charCodeAt(0)) >>> 0;
  return tones[hash % tones.length];
}
