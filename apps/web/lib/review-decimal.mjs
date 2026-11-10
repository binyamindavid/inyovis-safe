// Return an exact API decimal without involving JavaScript Number.
export function parseReviewDecimal(raw, locale) {
  const value = raw.trim();
  if (!value) return null;
  if (locale === 'fr') {
    const match = /^(-?)(\d{1,3}(?:[ \u00a0\u202f]\d{3})+|\d+)(?:,(\d{1,8}))?$/.exec(value);
    if (!match || /[ \u00a0\u202f]/.test(match[2]) && !/^\d{1,3}(?:([ \u00a0\u202f])\d{3})(?:\1\d{3})*$/.test(match[2])) return undefined;
    const digits = match[2].replace(/[ \u00a0\u202f]/g, '');
    if (digits.length > 16) return undefined;
    return `${match[1]}${digits}${match[3] === undefined ? '' : '.' + match[3]}`;
  }
  return /^-?\d{1,16}(?:\.\d{1,8})?$/.test(value) ? value : undefined;
}
export function displayReviewDecimal(value, locale) { return value === null || value === undefined ? '' : locale === 'fr' ? value.replace('.', ',') : value; }
export function warningCodes(meta) {
  const scaled = value => { if (value === null || value === undefined) return null; const [whole, fraction=''] = value.split('.'); return BigInt(whole) * 100000000n + BigInt((whole.startsWith('-') ? '-' : '') + (fraction.padEnd(8, '0') || '0')); };
  const codes = [];
  if (meta.subtotal != null && meta.tax != null && meta.total != null && scaled(meta.subtotal) + scaled(meta.tax) !== scaled(meta.total)) codes.push('TOTAL_MISMATCH');
  if (meta.taxLines.length) {
    if (meta.taxLines.some(line => line.currency && line.currency !== meta.currency)) codes.push('TAX_CURRENCY_MISMATCH');
    else if (meta.tax != null && meta.taxLines.reduce((sum,line) => sum + scaled(line.amount), 0n) !== scaled(meta.tax)) codes.push('TAX_LINES_MISMATCH');
  }
  return codes;
}
export function sumReviewDecimals(values) {
  let sum = 0n;
  for (const value of values) {
    if (!/^-?\d{1,16}(?:\.\d{1,8})?$/.test(value)) return null;
    const negative = value.startsWith('-');
    const [whole, fraction=''] = (negative ? value.slice(1) : value).split('.');
    const scaled = BigInt(whole) * 100000000n + BigInt(fraction.padEnd(8, '0') || '0');
    sum += negative ? -scaled : scaled;
  }
  const sign = sum < 0n ? '-' : '';
  const absolute = sum < 0n ? -sum : sum;
  const fraction = String(absolute % 100000000n).padStart(8, '0').replace(/0+$/, '');
  return `${sign}${absolute / 100000000n}${fraction ? '.' + fraction : ''}`;
}
