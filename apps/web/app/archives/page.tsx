'use client';
import { listArchives, problemMessage, type Archive, type ProblemCode } from '@invoyis/contracts';
import { useEffect, useState } from 'react';
import { LanguageSelect } from '../../components/language-select';
import { useWords } from '../../lib/catalog';
import { lastArchiveKey } from './[archiveId]/library/url-state';
import { NewArchiveForm } from './[archiveId]/library/new-archive-dialog';
import { ArchiveIcon } from './[archiveId]/library/archive-shell';
import s from './[archiveId]/library/library.module.css';

/**
 * Entry point for archives. With at least one archive it opens the library on the archive used last
 * (`?new=1` opens the New archive dialog there). With none, or after access changed, it shows a
 * first-run screen with the same New archive form.
 */
export default function ArchivesPage() {
  const { locale, t } = useWords();
  const [items, setItems] = useState<Archive[] | null>(null);
  const [accessChanged, setAccessChanged] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const changed = params.get('access') === 'changed';
    setAccessChanged(changed);
    listArchives({ credentials: 'same-origin' }).then(({ data, error, response }) => {
      if (data && data.items.length && !changed) {
        let last: string | null = null; try { last = sessionStorage.getItem(lastArchiveKey); } catch { /* storage unavailable */ }
        const target = data.items.find(item => item.id === last) ?? data.items[0];
        window.location.replace(`/archives/${target.id}${params.has('new') ? '?newArchive=1' : ''}`); return;
      }
      if (data) setItems(data.items);
      else if (response?.status === 401) window.location.assign('/sign-in?next=/archives');
      else setError(problemMessage((error as { code?: ProblemCode })?.code ?? 'INTERNAL_ERROR', locale));
    }).catch(() => setError(problemMessage('UPSTREAM_UNAVAILABLE', locale)));
  }, [locale]);

  if (!items && !error) return <main className={s.onboarding}><p className={s.muted} role="status">{t.loading}</p></main>;
  return <main className={s.onboarding}>
    <div className={s.onboardingTop}><div className={s.brand}><span className={s.logo} aria-hidden="true">I</span>Invoyis</div><div className={s.langSelect}><LanguageSelect signedIn /></div></div>
    <div className={s.onboardingCard}>
      {accessChanged && <p className={s.alertBox} role="alert">{t.accessLost}</p>}
      {error && <p className={s.alertBox} role="alert">{error}</p>}
      {items && items.length > 0 && <section aria-labelledby="your-archives">
        <h2 id="your-archives" className={s.panelTitle}>{t.archives}</h2>
        <ul className={s.archiveList}>{items.map(a => <li key={a.id}><a href={`/archives/${a.id}`} className={s.archiveItem}>
          <ArchiveIcon kind={a.kind} /><span className={s.archiveText}><span className={s.archiveName} dir="auto">{a.name}</span><small>{a.kind === 'personal' ? t.personal : t.business} · {t[a.role]}</small></span>
        </a></li>)}</ul>
      </section>}
      <section aria-labelledby="new-archive">
        <h1 id="new-archive" className={s.onboardingTitle}>{items && items.length > 0 ? t.libNewArchive : t.libFirstArchive}</h1>
        <p className={s.muted}>{t.libFirstArchiveHint}</p>
        <NewArchiveForm autoFocus={!accessChanged} />
      </section>
    </div>
  </main>;
}
