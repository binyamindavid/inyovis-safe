'use client';
import { HealthCheck } from './health-check';
import Link from 'next/link';
import { useWords } from '../lib/catalog';
export default function Home() {
  const {t}=useWords();
  return <main><h1>Invoyis</h1><p>{t.homeIntro}</p><HealthCheck /><Link href="/sign-in">{t.signIn}</Link></main>;
}
