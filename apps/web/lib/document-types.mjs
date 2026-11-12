import { groupOf, invoiceTypes, documentTypes } from './review-groups.mjs';

// Every confirmed document type, in the order the review form offers them.
export const allDocumentTypes = [...invoiceTypes, ...documentTypes];
const labelKeys = { invoice: 'invoice', bill: 'bill', credit_note: 'creditNote', lease: 'typeLease', court_correspondence: 'typeCourtCorrespondence', other: 'typeOther' };

/** Translated label for a confirmed document type, or '' when the type is unknown or not yet confirmed. */
export function documentTypeLabel(words, type) {
  const key = labelKeys[type];
  return key ? words[key] ?? '' : '';
}

/** Types whose translated label contains the query, so archive search can match "credit note" or "חשבון". */
export function typesMatching(words, query, locale) {
  const needle = String(query ?? '').trim().toLocaleLowerCase(locale);
  if (!needle) return [];
  return allDocumentTypes.filter(type => documentTypeLabel(words, type).toLocaleLowerCase(locale).includes(needle));
}

/** Invoice, bill and credit note show a printed invoice ID; the other types show a reference. */
export function identifierIsInvoiceId(type) {
  return !type || groupOf(type) === 'invoice';
}
