'use client';
import type { Archive } from '@invoyis/contracts';
import { Button, ModalDialog, Select } from '@invoyis/ui';
import { useEffect, useRef, useState, type DragEvent } from 'react';
import { useWords } from '../../../../lib/catalog';
import { ArchiveIcon } from './archive-shell';
import { CloseIcon, FileGlyph, UploadIcon } from './icons';
import s from './library.module.css';

function fill(template: string, values: Record<string, string | number>) { return template.replace(/\{(\w+)\}/g, (_, k) => String(values[k] ?? '')); }

/**
 * Add documents: name the destination archive first, pick PDFs, then upload.
 * The dialog only collects files; the archive's mounted upload queue does the transfer, so closing it never stops an upload.
 */
export function AddDocumentsDialog({ isOpen, onOpenChange, current, archives, onUpload }: {
  isOpen: boolean; onOpenChange: (open: boolean) => void; current: Archive; archives: Archive[]; onUpload: (archiveId: string, files: File[]) => boolean;
}) {
  const { t } = useWords();
  const [dest, setDest] = useState(current.id);
  const [files, setFiles] = useState<File[]>([]);
  const [error, setError] = useState('');
  const [dragging, setDragging] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => { if (isOpen) { setDest(current.id); setFiles([]); setError(''); } }, [isOpen, current.id]);
  const options = archives.some(a => a.id === current.id) ? archives : [current, ...archives];
  const destination = options.find(a => a.id === dest) ?? current;
  const maxMiB = Math.round(((destination.uploadMaxBytes ?? current.uploadMaxBytes) ?? 25 * 1024 * 1024) / 1048576);
  const kind = (a: Archive) => a.kind === 'personal' ? t.personal : t.business;

  function add(list: FileList | File[] | null) {
    const picked = Array.from(list ?? []);
    if (!picked.length) return;
    setError('');
    setFiles(prev => [...prev, ...picked.filter(f => !prev.some(p => p.name === f.name && p.size === f.size && p.lastModified === f.lastModified))]);
  }
  function onDrop(event: DragEvent) { event.preventDefault(); setDragging(false); add(event.dataTransfer.files); }

  return <ModalDialog isOpen={isOpen} onOpenChange={onOpenChange} title={t.libAddDocuments} className={s.addDialog}>
    <div className={s.destination} data-kind={destination.kind}>
      <ArchiveIcon kind={destination.kind} />
      <div><span className={s.eyebrow}>{t.libAddingTo}</span><strong dir="auto">{destination.name}</strong><small>{kind(destination)}</small></div>
    </div>
    {options.length > 1 && <Select label={t.libChangeArchive} selectedKey={dest} onSelectionChange={k => setDest(String(k))}
      options={options.map(a => ({ id: a.id, label: `${a.name} · ${kind(a)}` }))} />}
    <div className={s.dropZone} data-dragging={dragging || undefined}
      onDragOver={e => { e.preventDefault(); setDragging(true); }} onDragLeave={() => setDragging(false)} onDrop={onDrop}>
      <UploadIcon width={22} height={22} />
      <Button variant="secondary" className={s.secondary} onPress={() => input.current?.click()}>{t.libChooseFiles}</Button>
      <p className={s.muted}>{fill(t.libDropHint, { max: maxMiB })}</p>
      <input ref={input} type="file" multiple accept="application/pdf,.pdf" hidden onChange={e => { add(e.target.files); e.target.value = ''; }} />
    </div>
    {files.length > 0 && <ul className={s.fileList} aria-label={t.libSelected}>
      {files.map((f, i) => <li key={`${f.name}-${f.size}-${i}`}>
        <FileGlyph /><span dir="auto">{f.name}</span>
        <button type="button" className={s.iconButton} aria-label={fill(t.libRemoveFile, { file: f.name })} onClick={() => setFiles(prev => prev.filter((_, j) => j !== i))}><CloseIcon width={14} height={14} /></button>
      </li>)}
    </ul>}
    {error && <p className="ui-error" role="alert">{error}</p>}
    <div className="ui-dialog-actions">
      <Button variant="secondary" className={s.secondary} onPress={() => onOpenChange(false)}>{t.cancel}</Button>
      <Button data-action="upload" onPress={() => { if (!files.length) { setError(t.libNoFile); return; } if (!onUpload(destination.id, files)) setError(t.libUploadNotReady); }}>{fill(t.libUploadTo, { archive: destination.name })}</Button>
    </div>
  </ModalDialog>;
}
