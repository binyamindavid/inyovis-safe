import type { SVGProps } from 'react';
type P = SVGProps<SVGSVGElement> & { flip?: boolean };
const base = { width: 16, height: 16, viewBox: '0 0 16 16', fill: 'none', stroke: 'currentColor', strokeWidth: 1.5, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const, 'aria-hidden': true };
export const SearchIcon = (p: P) => <svg {...base} {...p}><circle cx="7" cy="7" r="4.5" /><path d="m10.5 10.5 3 3" /></svg>;
export const FilterIcon = (p: P) => <svg {...base} {...p}><path d="M2.5 4h11M4.5 8h7M6.5 12h3" /></svg>;
export const PlusIcon = (p: P) => <svg {...base} {...p}><path d="M8 3v10M3 8h10" /></svg>;
export const CloseIcon = (p: P) => <svg {...base} {...p}><path d="m4 4 8 8M12 4l-8 8" /></svg>;
export const GearIcon = (p: P) => <svg {...base} {...p}><circle cx="8" cy="8" r="2" /><path d="M8 1.8v1.6M8 12.6v1.6M1.8 8h1.6M12.6 8h1.6M3.6 3.6l1.1 1.1M11.3 11.3l1.1 1.1M3.6 12.4l1.1-1.1M11.3 4.7l1.1-1.1" /></svg>;
export const DocsIcon = (p: P) => <svg {...base} {...p}><path d="M4 2.5h5l3 3v8H4z" /><path d="M9 2.5v3h3" /></svg>;
export const ReviewIcon = (p: P) => <svg {...base} {...p}><circle cx="8" cy="8" r="5.5" /><path d="M8 5v3.2M8 10.6v.1" /></svg>;
export const PersonIcon = (p: P) => <svg {...base} {...p}><circle cx="8" cy="5.5" r="2.5" /><path d="M3 13.5c.6-2.4 2.6-3.5 5-3.5s4.4 1.1 5 3.5" /></svg>;
export const BriefcaseIcon = (p: P) => <svg {...base} {...p}><rect x="2" y="5" width="12" height="8.5" rx="1.5" /><path d="M6 5V3.5h4V5M2 9h12" /></svg>;
export const MenuIcon = (p: P) => <svg {...base} {...p}><path d="M2.5 4h11M2.5 8h11M2.5 12h11" /></svg>;
export const ChevronIcon = ({ flip, ...p }: P) => <svg {...base} {...p} style={{ transform: flip ? 'scaleX(-1)' : undefined, ...p.style }}><path d="m10 3.5-4.5 4.5 4.5 4.5" /></svg>;
export const DownloadIcon = (p: P) => <svg {...base} {...p}><path d="M8 2.5v8M4.5 7 8 10.5 11.5 7M3 13.5h10" /></svg>;
export const UploadIcon = (p: P) => <svg {...base} {...p}><path d="M8 10.5v-8M4.5 6 8 2.5 11.5 6M3 13.5h10" /></svg>;
export const Spinner = (p: P) => <svg {...base} {...p} className={`proto-spin ${p.className ?? ''}`}><path d="M8 2a6 6 0 1 1-6 6" /></svg>;

const typeTint: Record<string, string> = { invoice: '#2f6fdb', lease: '#2f8a5b', court_correspondence: '#b4542d', other: '#7a5bb5' };
/** Small PDF glyph with a type-coloured band, like Finder's document icons. Unconfirmed documents stay grey. */
export function FileGlyph({ type }: { type?: string | null }) {
  const tint = (type && typeTint[type]) || '#8a94a6';
  return <svg width="20" height="24" viewBox="0 0 20 24" aria-hidden="true"><path d="M3 1.5h9l5 5V22a.5.5 0 0 1-.5.5h-13A.5.5 0 0 1 3 22z" fill="#fff" stroke="#b9c2d0" /><path d="M12 1.5v5h5" fill="#eef1f6" stroke="#b9c2d0" /><rect x="1" y="13" width="12" height="6" rx="1.2" fill={tint} /><text x="7" y="17.6" fill="#fff" fontSize="4.6" fontWeight="700" textAnchor="middle" fontFamily="system-ui, sans-serif">PDF</text></svg>;
}
