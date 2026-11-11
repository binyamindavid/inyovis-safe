'use client';
import { createArchiveExport } from '@invoyis/contracts';
import { Button } from '@invoyis/ui';
import { useState } from 'react';
import { intent } from '../../../lib/archive-ui';
import { useWords } from '../../../lib/catalog';

export function ExportAction({ archiveId }: { archiveId: string }) {
  const { t } = useWords();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  async function create() {
    if (busy) return;
    setBusy(true); setError(false);
    try {
      const { data } = await createArchiveExport({ path: { archiveId }, headers: intent, credentials: 'same-origin' });
      if (!data) { setError(true); return; }
      const link = document.createElement('a');
      link.href = `/api/v1/archives/${archiveId}/exports/${data.id}`;
      document.body.append(link); link.click(); link.remove();
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  }
  return <section><Button isDisabled={busy} onPress={() => void create()}>{busy ? t.exportPreparing : t.exportArchive}</Button>{error && <p role="alert">{t.exportFailed}</p>}</section>;
}
