import { ArchiveLibrary } from './library/archive-library';
import { parseLibraryState, stateParams } from './library/url-state';

/** An archive opens as a document library: sidebar of archives, toolbar, list and preview. */
export default async function ArchivePage({ params, searchParams }: { params: Promise<{ archiveId: string }>; searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const [{ archiveId }, raw] = await Promise.all([params, searchParams]);
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(raw)) if (typeof value === 'string') query.set(key, value);
  const hasQuery = stateParams.some(key => query.has(key));
  return <ArchiveLibrary key={archiveId} archiveId={archiveId} initial={parseLibraryState(query)} hasQuery={hasQuery} openAdd={query.get('add') === '1'} />;
}
