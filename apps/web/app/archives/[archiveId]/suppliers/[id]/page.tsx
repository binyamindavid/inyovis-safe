import { ArchiveLibrary } from '../../library/archive-library';
import { parseLibraryState, stateParams } from '../../library/url-state';

/** Supplier history uses the library layout, limited to this supplier's confirmed documents. */
export default async function SupplierPage({ params, searchParams }: { params: Promise<{ archiveId: string; id: string }>; searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const [{ archiveId, id }, raw] = await Promise.all([params, searchParams]);
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(raw)) if (typeof value === 'string') query.set(key, value);
  return <ArchiveLibrary key={`${archiveId}:${id}`} archiveId={archiveId} supplier={{ id }} initial={parseLibraryState(query)} hasQuery={stateParams.some(key => query.has(key))} />;
}
