import type { ProtoDocument } from './fixtures';

/**
 * A synthetic "PDF page" drawn as SVG so the prototype needs no file storage or renderer.
 * The page is always laid out left-to-right, like a real scanned page, regardless of UI direction.
 */
export function SamplePage({ doc, label }: { doc: ProtoDocument; label: string }) {
  const kind = doc.type ?? 'invoice';
  const title = kind === 'invoice' ? 'INVOICE' : kind === 'lease' ? 'RESIDENTIAL LEASE AGREEMENT' : 'NOTICE OF HEARING';
  const lines = (y: number, count: number, gap = 14, widths = [430, 400, 445, 380, 410]) => Array.from({ length: count }, (_, i) =>
    <rect key={`${y}-${i}`} x="60" y={y + i * gap} width={widths[i % widths.length]} height="5" rx="2.5" fill="#d9dee7" />);
  return <svg viewBox="0 0 560 740" role="img" aria-label={label} className="proto-page" direction="ltr">
    <rect width="560" height="740" fill="#fff" />
    <text x="60" y="78" fontSize="13" fontWeight="700" fill="#2b3445" fontFamily="system-ui, sans-serif">{doc.from}</text>
    {lines(92, 2, 12, [150, 120])}
    <text x="500" y="78" fontSize="11" textAnchor="end" fill="#6b7587" fontFamily="system-ui, sans-serif">{doc.date}</text>
    {doc.reference && <text x="500" y="96" fontSize="11" textAnchor="end" fill="#6b7587" fontFamily="ui-monospace, monospace">{doc.reference}</text>}
    <line x1="60" x2="500" y1="132" y2="132" stroke="#e3e7ee" />
    <text x="60" y="172" fontSize="18" fontWeight="700" letterSpacing="1.5" fill="#1d2636" fontFamily="system-ui, sans-serif">{title}</text>
    <text x="60" y="194" fontSize="11" fill="#8a94a6" fontFamily="system-ui, sans-serif">SYNTHETIC SAMPLE — NOT A REAL DOCUMENT</text>
    {kind === 'invoice' ? <>
      <rect x="60" y="230" width="440" height="26" fill="#f2f4f8" />
      {['Description', 'Qty', 'Amount'].map((h, i) => <text key={h} x={[72, 360, 488][i]} y="247" fontSize="10.5" fontWeight="600" textAnchor={i === 2 ? 'end' : 'start'} fill="#4a5568" fontFamily="system-ui, sans-serif">{h}</text>)}
      {[0, 1, 2].map(r => <g key={r}><rect x="72" y={276 + r * 30} width={[190, 150, 170][r]} height="5" rx="2.5" fill="#d9dee7" /><text x="360" y={281 + r * 30} fontSize="10.5" fill="#4a5568" fontFamily="system-ui">{r + 1}</text><text x="488" y={281 + r * 30} fontSize="10.5" textAnchor="end" fill="#4a5568" fontFamily="ui-monospace, monospace">{['120.00', '45.50', '18.00'][r]}</text><line x1="60" x2="500" y1={292 + r * 30} y2={292 + r * 30} stroke="#eef1f5" /></g>)}
      <text x="380" y="392" fontSize="11" fill="#4a5568" fontFamily="system-ui">Total</text>
      <text x="488" y="392" fontSize="13" fontWeight="700" textAnchor="end" fill="#1d2636" fontFamily="ui-monospace, monospace">183.50</text>
      {lines(450, 4)}
    </> : <>
      {lines(232, 6)}
      <rect x="60" y="330" width="440" height="58" rx="4" fill="none" stroke="#e3e7ee" />
      <rect x="76" y="350" width="300" height="5" rx="2.5" fill="#d9dee7" /><rect x="76" y="364" width="250" height="5" rx="2.5" fill="#d9dee7" />
      {lines(420, 8)}
    </>}
    <line x1="60" x2="220" y1="660" y2="660" stroke="#c4cad6" />
    <text x="60" y="678" fontSize="9.5" fill="#8a94a6" fontFamily="system-ui">Signature</text>
    <text x="500" y="712" fontSize="9.5" textAnchor="end" fill="#a0a8b8" fontFamily="system-ui">1 / {doc.pages}</text>
  </svg>;
}
