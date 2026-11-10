import { HealthCheck } from './health-check';
import Link from 'next/link';

export default function Home() {
  return (
    <main>
      <h1>Invoyis</h1>
      <p>The invoice archive is in development.</p>
      <HealthCheck />
      <Link href="/sign-in">Sign in</Link>
    </main>
  );
}
