'use client';
import { Button, ModalDialog } from '@invoyis/ui';
import { useEffect, useRef, useState, type MouseEvent } from 'react';
import { useWords } from '../../../../../../lib/catalog';
import { ChevronIcon } from '../../../library/icons';
import styles from './review.module.css';
export function PreviewPanel({ archiveId, id, onAccessLost }: {
    archiveId: string;
    id: string;
    onAccessLost: () => void;
}) {
    const { locale, t } = useWords();
    const rtl = locale === 'he';
    const [page, setPage] = useState(1);
    const [count, setCount] = useState(1);
    const [url, setUrl] = useState('');
    const [error, setError] = useState('');
    const [zoomOpen, setZoomOpen] = useState(false);
    const [zoom, setZoom] = useState(1);
    const [imageSize, setImageSize] = useState({ width: 1, height: 1 });
    const [viewerSize, setViewerSize] = useState({ width: 1, height: 1 });
    const viewerRef = useRef<HTMLDivElement>(null);
    const openerRef = useRef<HTMLElement | null>(null);
    const accessLostRef = useRef(onAccessLost);
    accessLostRef.current = onAccessLost;
    useEffect(() => {
        if (!zoomOpen || !viewerRef.current) return;
        const viewer = viewerRef.current;
        const observer = new ResizeObserver(() => setViewerSize({ width: viewer.clientWidth, height: viewer.clientHeight }));
        observer.observe(viewer);
        return () => observer.disconnect();
    }, [zoomOpen]);
    function openZoom(event?: MouseEvent<HTMLElement>) {
        openerRef.current = event?.currentTarget ?? document.activeElement as HTMLElement;
        setZoom(1);
        setZoomOpen(true);
    }
    function closeZoom(open: boolean) {
        setZoomOpen(open);
        if (!open) window.setTimeout(() => openerRef.current?.focus(), 0);
    }
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
                accessLostRef.current();
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
    }, [archiveId, id, page, t.previewUnavailable, t.previewTooLarge, t.previewTimeout]);
    const pages = Math.min(count, 10);
    const fit = Math.min((viewerSize.width - 32) / imageSize.width, (viewerSize.height - 32) / imageSize.height);
    // Page controls only when there is more than one page; the counter keeps "1 / 3" order in RTL.
    return <div>
<h2>{t.preview}</h2>{count > 10 && <p role="status">{t.showingFirstPages.replace('{count}', '10')}</p>}{error ? <p role="status">{error}</p> : url ? <><button type="button" className={styles.previewTrigger} aria-label={t.zoomPreview} onClick={openZoom}><img className={styles.pageImage} src={url} alt={`${t.preview} ${page}`} dir="ltr" onLoad={e => setImageSize({ width: e.currentTarget.naturalWidth, height: e.currentTarget.naturalHeight })}/></button><Button variant="secondary" onPress={() => openZoom()}>{t.zoomPreview}</Button></> : <p role="status">{t.loading}</p>}
{pages > 1 && <div className={styles.pager}>
<Button variant="quiet" className={styles.pagerButton} aria-label={t.previous} isDisabled={page <= 1} onPress={() => setPage(value => value - 1)}><ChevronIcon flip={rtl}/></Button>
<span className={styles.pagerCount}><bdi dir="ltr">{page} / {pages}</bdi></span>
<Button variant="quiet" className={styles.pagerButton} aria-label={t.next} isDisabled={page >= pages} onPress={() => setPage(value => value + 1)}><ChevronIcon flip={!rtl}/></Button>
</div>}
<ModalDialog isOpen={zoomOpen} onOpenChange={closeZoom} title={`${t.zoomPreview} · ${page} / ${pages}`} className={styles.zoomModal}>
<div className={styles.zoomControls}><Button variant="secondary" onPress={() => setZoom(value => Math.max(.5, +(value - .25).toFixed(2)))} isDisabled={zoom <= .5}>{t.zoomOut}</Button><Button variant="secondary" onPress={() => setZoom(value => Math.min(4, +(value + .25).toFixed(2)))} isDisabled={zoom >= 4}>{t.zoomIn}</Button><Button variant="secondary" onPress={() => setZoom(1)}>{t.fitToScreen}</Button><Button variant="secondary" onPress={() => closeZoom(false)}>{t.closeZoom}</Button></div>
<div ref={viewerRef} className={styles.zoomViewport} tabIndex={0} aria-label={t.zoomPreview} dir="ltr"><img src={url} alt={`${t.preview} ${page}`} style={{ width: imageSize.width * fit * zoom, height: imageSize.height * fit * zoom }}/></div>
</ModalDialog>
</div>;
}
