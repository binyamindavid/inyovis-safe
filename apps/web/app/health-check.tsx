'use client';

import { getHealth, problemMessage, type ProblemCode } from '@invoyis/contracts';
import { useEffect, useState } from 'react';

export function HealthCheck() {
  const [message, setMessage] = useState('Checking API…');

  useEffect(() => {
    let active = true;
    getHealth().then(({ data, error }) => {
      if (!active) return;
      setMessage(data?.status === 'ok'
        ? 'API connected'
        : problemMessage((error?.code ?? 'INTERNAL_ERROR') as ProblemCode, 'en'));
    }).catch(() => {
      if (active) setMessage(problemMessage('UPSTREAM_UNAVAILABLE', 'en'));
    });
    return () => { active = false; };
  }, []);

  return <p aria-live="polite">{message}</p>;
}
