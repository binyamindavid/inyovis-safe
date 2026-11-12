// Synthetic fixtures only. Nothing here comes from, or is sent to, the API.
export type DocType = 'invoice' | 'lease' | 'court';
export type DocStatus = 'processing' | 'needsReview' | 'ready';
export type ProtoArchive = { id: string; name: string; kind: 'personal' | 'business' };
export type ProtoDocument = {
  id: string; archiveId: string; name: string; type: DocType | null; status: DocStatus;
  date: string; added: string; from: string; reference?: string;
  category?: string; property?: string; paperLocation?: string;
  pages: number;
  /** Title proposed by (simulated) processing; becomes the name once review is confirmed. */
  suggestedTitle?: string;
};

export const archives: ProtoArchive[] = [
  { id: 'personal', name: 'Home & family', kind: 'personal' },
  { id: 'business', name: 'Levi Design Studio', kind: 'business' },
];

export const initialDocuments: ProtoDocument[] = [
  { id: 'p-1', archiveId: 'personal', name: 'Electricity invoice — October 2026', type: 'invoice', status: 'ready', date: '2026-10-28', added: '2026-11-02', from: 'Northwind Power Co.', reference: '000042-A/26', category: 'Utilities', property: 'Herzl St apartment', paperLocation: 'Blue binder', pages: 1 },
  { id: 'p-2', archiveId: 'personal', name: 'Apartment lease 2026–2027', type: 'lease', status: 'ready', date: '2026-08-01', added: '2026-07-29', from: 'Oren Properties Ltd', reference: 'L-2026-118', category: 'Housing', property: 'Herzl St apartment', paperLocation: 'Fireproof box', pages: 3 },
  { id: 'p-3', archiveId: 'personal', name: 'Court letter — hearing notice', type: 'court', status: 'needsReview', date: '2026-11-03', added: '2026-11-10', from: 'Tel Aviv Magistrates’ Court (sample)', reference: '12345-06-26', category: 'Legal', property: 'Herzl St apartment', pages: 2 },
  { id: 'p-4', archiveId: 'personal', name: 'חשבון מים — ספטמבר 2026', type: 'invoice', status: 'ready', date: '2026-09-30', added: '2026-10-04', from: 'Sample Water Utility', reference: 'W-7781', category: 'Utilities', property: 'Herzl St apartment', paperLocation: 'Blue binder', pages: 1 },
  { id: 'b-1', archiveId: 'business', name: 'Cloud hosting invoice INV-80114', type: 'invoice', status: 'ready', date: '2026-10-14', added: '2026-10-15', from: 'Contoso Cloud', reference: 'INV-80114', category: 'Software', property: 'Dizengoff studio', paperLocation: 'Digital only', pages: 1 },
  { id: 'b-2', archiveId: 'business', name: 'Studio lease — Dizengoff 50', type: 'lease', status: 'ready', date: '2026-01-01', added: '2025-12-20', from: 'Fabrikam Estates', reference: 'DZ-50-2026', category: 'Rent', property: 'Dizengoff studio', paperLocation: 'Office cabinet, drawer 2', pages: 4 },
  { id: 'b-3', archiveId: 'business', name: 'Court letter — small claims summons', type: 'court', status: 'needsReview', date: '2026-11-06', added: '2026-11-09', from: 'Small Claims Court (sample)', reference: '000731-11-26', category: 'Legal', property: 'Dizengoff studio', pages: 1 },
  { id: 'b-4', archiveId: 'business', name: 'חשבונית ציוד משרדי 2026/118', type: 'invoice', status: 'ready', date: '2026-09-21', added: '2026-09-22', from: 'Office Supplies Sample Ltd', reference: '2026/118', category: 'Office', property: 'Dizengoff studio', paperLocation: 'Office cabinet, drawer 1', pages: 1 },
];

/** What the simulated processing step "finds" in the sample PDF. */
export const sampleUpload = {
  fileName: 'sample-invoice-november.pdf',
  detected: { name: 'Printing invoice — November 2026', type: 'invoice' as DocType, date: '2026-11-11', from: 'Sample Print House', reference: 'PH-2026-0457', category: 'Office', property: 'Dizengoff studio' },
};

export const filterKeys = ['category', 'property', 'paperLocation'] as const;
export type FilterKey = typeof filterKeys[number];
export type Filters = Partial<Record<FilterKey, string>>;
