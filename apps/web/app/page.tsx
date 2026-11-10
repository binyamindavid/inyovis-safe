import { HealthCheck } from './health-check';

export default function Home() {
  return (
    <main>
      <h1>Invoyis</h1>
      <p>The invoice archive is in development.</p>
      <HealthCheck />
    </main>
  );
}
