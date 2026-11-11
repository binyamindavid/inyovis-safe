// Field groups of the shared review form. Invoice types own the invoice group;
// lease, court correspondence and other own the document group.
export const documentTypes = ['lease', 'court_correspondence', 'other'];
export const invoiceTypes = ['invoice', 'bill', 'credit_note'];
export const invoiceFields = ['supplier', 'invoiceId', 'accountReference', 'issueDate', 'currency', 'subtotal', 'tax', 'total'];
export const documentFields = ['title', 'sender', 'issueDate', 'reference', 'notes'];
export const optionalDocumentFields = ['sender', 'issueDate', 'reference', 'notes'];

export function groupOf(type) { return documentTypes.includes(type) ? 'document' : 'invoice'; }
export function fieldsFor(type) { return groupOf(type) === 'document' ? documentFields : invoiceFields; }

const blank = value => value === null || value === undefined || value === '';
const ownFields = { invoice: ['supplier', 'invoiceId', 'accountReference', 'currency', 'subtotal', 'tax', 'total', 'taxLines'], document: ['title', 'sender', 'reference', 'notes'] };

// Fields (by name) whose unsaved values a type switch would discard. issueDate is shared by both
// groups and never discarded. `saved` is the form as last loaded or saved.
export function discardedByTypeSwitch(form, saved, nextType) {
  const from = groupOf(form.documentType);
  if (from === groupOf(nextType)) return [];
  return ownFields[from].filter(name => {
    const value = form[name];
    const empty = Array.isArray(value) ? value.length === 0 : blank(value);
    return !empty && JSON.stringify(value) !== JSON.stringify(saved?.[name]);
  });
}

// Form after a type switch: the other group is emptied, shared values are kept.
export function switchType(form, nextType) {
  const next = { ...form, documentType: nextType };
  if (groupOf(form.documentType) === groupOf(nextType)) return next;
  const from = groupOf(form.documentType);
  for (const name of ownFields[from]) next[name] = name === 'taxLines' ? [] : ['subtotal', 'tax', 'total'].includes(name) ? null : '';
  return next;
}

// Metadata for the API: only the group that applies to the type. Raw typed amounts are sent separately.
export function metadataForType(form) {
  if (groupOf(form.documentType) === 'document') {
    return { documentType: form.documentType, title: form.title ?? '', sender: form.sender ?? '', issueDate: form.issueDate ?? '', reference: form.reference ?? '', notes: form.notes ?? '' };
  }
  return null; // invoice metadata keeps the existing amount normalisation in the page
}

export function confirmRequirements(type) { return groupOf(type) === 'document' ? ['documentType', 'title'] : ['documentType', 'supplier', 'issueDate', 'currency', 'total']; }

// Extraction warnings that only describe invoice data. A lease or court letter has no reason to carry
// supported invoice XML, so "no supported XML", an unsupported profile or ambiguous XML say nothing
// useful about it. issueDate is shared with the document group and stays. Any other code, including
// one this list does not know yet, stays visible so a real file problem is never hidden.
const invoiceOnlyWarning = /^(?:unsafe_candidate_(?:supplier|invoiceId|currency)|conflicting_(?!issueDate$).+|inconsistent_(?:currency|total)|no_supported_xml|unsupported_profile|ambiguous_invoice_xml)$/;
export function candidateWarningsFor(type, warnings) {
  return groupOf(type) === 'document' ? warnings.filter(code => !invoiceOnlyWarning.test(code)) : warnings;
}
