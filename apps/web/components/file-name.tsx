'use client';
import { useLocale } from '@invoyis/ui';

/**
 * Base direction for a filename or document title. A filename ends in a Latin extension, so in a left-to-right
 * interface it is laid out LTR (Hebrew runs inside still read right to left): `חשבונית.pdf`, never `pdf.חשבונית`.
 * In a right-to-left interface it follows its first strong character, so Latin names with leading digits stay intact
 * and Hebrew names end with the extension on the reading side.
 */
export function fileNameDir(direction: 'ltr' | 'rtl'): 'ltr' | 'auto' {
  return direction === 'rtl' ? 'auto' : 'ltr';
}

/** A filename or document title, isolated from the surrounding text (see `fileNameDir`). */
export function FileName({ children, className, title }: { children: string; className?: string; title?: string }) {
  const { direction } = useLocale();
  return <bdi dir={fileNameDir(direction)} className={className} title={title}>{children}</bdi>;
}
