'use client';
import { getHealth, problemMessage, type ProblemCode } from '@invoyis/contracts';
import { useEffect, useState } from 'react';
import { useWords } from '../lib/catalog';
export function HealthCheck() {
  const {locale,t}=useWords(); const [message,setMessage]=useState(t.checkingAPI);
  useEffect(()=>{let active=true;getHealth().then(({data,error})=>{if(active)setMessage(data?.status==='ok'?t.apiConnected:problemMessage((error?.code??'INTERNAL_ERROR') as ProblemCode,locale));}).catch(()=>{if(active)setMessage(problemMessage('UPSTREAM_UNAVAILABLE',locale));});return()=>{active=false;};},[locale,t]);
  return <p aria-live="polite">{message}</p>;
}
