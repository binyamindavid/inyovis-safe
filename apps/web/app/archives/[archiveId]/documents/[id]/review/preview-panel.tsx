'use client';
import { Button } from '@invoyis/ui';
import { useEffect, useState } from 'react';
import { useWords } from '../../../../../../lib/catalog';
import styles from './review.module.css';
export function PreviewPanel({ archiveId, id, onAccessLost }: {
    archiveId: string;
    id: string;
    onAccessLost: () => void;
}) {
    const { t } = useWords();
    const [page, setPage] = useState(1);
    const [count, setCount] = useState(1);
    const [url, setUrl] = useState('');
    const [error, setError] = useState('');
    useEffect(() => {
        let active = true;
        let objectUrl = '';
        const controller = new AbortController();
        setError('');
        setUrl('');
        void fetch(`/api/v1/archives/${archiveId}/documents/${id}/preview/${page}`, { credentials: 'same-origin', cache: 'no-store', signal: controller.signal }).then(async (response) => {
            if (!active)
                return;
            if (response.status === 404) {
                onAccessLost();
                return;
            }
            ;
            if (!response.ok || response.headers.get('content-type') !== 'image/png') {
                const code = (await response.json().catch(() => null))?.code;
                setError(code === 'PREVIEW_TOO_LARGE' ? t.previewTooLarge : code === 'PREVIEW_TIMEOUT' ? t.previewTimeout : t.previewUnavailable);
                return;
            }
            setCount(Number(response.headers.get('x-preview-page-count')) || 1);
            const blob = await response.blob();
            if (!active)
                return;
            objectUrl = URL.createObjectURL(blob);
            setUrl(objectUrl);
        }).catch(() => { if (active)
            setError(t.previewUnavailable); });
        return () => { active = false; controller.abort(); if (objectUrl)
            URL.revokeObjectURL(objectUrl); };
    }, [archiveId, id, page, onAccessLost, t.previewUnavailable, t.previewTooLarge, t.previewTimeout]);
    return <div>
<h2>{t.preview}</h2>{count > 10 && <p role="status">{t.showingFirstPages.replace('{count}', '10')}</p>}{error ? <p role="status">{error}</p> : url ? <img className={styles.pageImage} src={url} alt={`${t.preview} ${page}`} dir="ltr"/> : <p role="status">{t.loading}</p>}<div className={styles.actions}>
<Button variant="secondary" isDisabled={page <= 1} onPress={() => setPage(value => value - 1)}>{t.previous}</Button>
<span>{page} / {Math.min(count, 10)}</span>
<Button variant="secondary" isDisabled={page >= Math.min(count, 10)} onPress={() => setPage(value => value + 1)}>{t.next}</Button>
</div>
</div>;
}
