const amountPattern = /^(?:0|[1-9]\d*)(?:\.\d{2})$|^-(?:0\.(?!00)\d{2}|[1-9]\d*\.\d{2})$/;
export function formatExactMoney(amount, currency, locale) {
  if (!['EUR','ILS'].includes(currency) || !amountPattern.test(amount)) throw new Error('Invalid exact money');
  const negative = amount.startsWith('-');
  const [whole, fraction] = (negative ? amount.slice(1) : amount).split('.');
  const group = new Intl.NumberFormat(locale, { maximumFractionDigits:0 }).format(BigInt(whole));
  const decimal = new Intl.NumberFormat(locale).formatToParts(1.1).find(part => part.type === 'decimal')?.value ?? '.';
  const sign = negative ? new Intl.NumberFormat(locale).formatToParts(-1).find(part => part.type === 'minusSign')?.value ?? '-' : '';
  return `${sign}${group}${decimal}${fraction} ${currency}`;
}
export function formatDateOnly(value, locale) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) throw new Error('Invalid calendar date');
  const [year, month, day] = value.split('-').map(Number);
  const utcNoon = Date.UTC(year, month - 1, day, 12);
  if (new Date(utcNoon).toISOString().slice(0,10) !== value) throw new Error('Invalid calendar date');
  return new Intl.DateTimeFormat(locale, { year:'numeric', month:'2-digit', day:'2-digit', calendar:'gregory', timeZone:'UTC' }).format(utcNoon);
}
