'use client';
import { listArchives, type Archive } from '@invoyis/contracts';
import { Select } from '@invoyis/ui';
import { useEffect, useState } from 'react';
import { useWords } from '../lib/catalog';

export function ArchiveSwitcher({ currentId }: { currentId: string }) {
  const { t } = useWords();
  const [items, setItems] = useState<Archive[]>([]);
  useEffect(() => {
    if (!currentId) return;
    let active = true;
    listArchives({ credentials: 'same-origin' }).then(({ data }) => { if (active && data) setItems(data.items); });
    return () => { active = false; };
  }, [currentId]);
  if (!items.length) return null;
  return <Select label={t.switch} selectedKey={currentId} onSelectionChange={key => {
    const id = String(key);
    if (items.some(item => item.id === id) && id !== currentId) window.location.assign(`/archives/${id}`);
  }} options={items.map(item => ({ id: item.id, label: item.name }))} />;
}
