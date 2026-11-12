import type { Metadata } from 'next';
import { notFound } from 'next/navigation';
import { serverLocale } from '../../../lib/locale-server';
import { ArchivePrototype } from './archive-prototype';
import { parseUrlState } from './url-state';

export const metadata: Metadata = { title: 'Archive prototype · Invoyis', robots: { index: false, follow: false } };

/**
 * Clickable archive prototype. Synthetic fixtures only: no API calls, no uploads, no stored state.
 * Available in `next dev`; production builds return 404 unless INVOYIS_PROTOTYPES=1.
 */
export default async function ArchivePrototypePage({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  if (process.env.NODE_ENV === 'production' && process.env.INVOYIS_PROTOTYPES !== '1') notFound();
  const raw = await searchParams;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(raw)) if (typeof value === 'string') params.set(key, value);
  const initial = parseUrlState(params, await serverLocale());
  return <ArchivePrototype initial={initial} />;
}
