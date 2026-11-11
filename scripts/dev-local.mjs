import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync } from 'node:fs';
import { createConnection } from 'node:net';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { localStorageArgs } from './dev-local-storage.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const envFile = resolve(root, '.env');
const certificate = resolve(root, '.local-certs/localhost.pem');
const key = resolve(root, '.local-certs/localhost-key.pem');
const workerBinary = resolve(root, '.local-bin/invoyis-worker');

if (!existsSync(envFile)) throw new Error('Missing .env. Run scripts/create-local-env.sh first.');
if (!existsSync(certificate) || !existsSync(key)) {
  throw new Error('Missing local HTTPS certificate. Follow the mkcert setup in README.md.');
}

const env = { ...process.env };
for (const line of readFileSync(envFile, 'utf8').split(/\r?\n/)) {
  const match = /^([A-Za-z_][A-Za-z_0-9]*)=(.*)$/.exec(line);
  if (match) env[match[1]] = match[2];
}
if (env.BROWSER_ORIGIN !== 'https://localhost:3000') {
  throw new Error('Set BROWSER_ORIGIN=https://localhost:3000 in .env.');
}
if (env.API_BASE_URL !== 'http://127.0.0.1:8081') {
  throw new Error('Set API_BASE_URL=http://127.0.0.1:8081 in .env.');
}
if (env.MAIL_DRIVER !== 'smtp' || env.SMTP_ADDR !== '127.0.0.1:1025') {
  throw new Error('Local development needs MAIL_DRIVER=smtp and SMTP_ADDR=127.0.0.1:1025 in .env.');
}

function portBusy(port, host = '127.0.0.1') {
  return new Promise((done) => {
    const socket = createConnection({ host, port });
    socket.once('connect', () => { socket.destroy(); done(true); });
    socket.once('error', () => done(false));
    socket.setTimeout(500, () => { socket.destroy(); done(false); });
  });
}

for (const port of [3000, 8081, 8025, 1025]) {
  if (await portBusy(port) || (port === 3000 && await portBusy(port, '::1'))) {
    throw new Error(`Port ${port} is in use. Stop the existing server, then run yarn dev:local again.`);
  }
}

const storageRunning = await portBusy(8333);
const storageArgs = localStorageArgs(env, storageRunning);
if (!storageRunning && !storageArgs) {
  console.warn('Local S3 is not running on port 8333. Uploads will fail until it is started.');
}
const workerConfigured = Boolean(env.DOCUMENT_WORKER_DATABASE_URL && env.WORKER_S3_ACCESS_KEY_ID && env.WORKER_S3_SECRET_ACCESS_KEY && env.CLAMAV_DATABASE);
if (!workerConfigured) {
  console.warn('Document worker is not configured. Received uploads will remain pending; see README.md.');
} else if (!existsSync(env.CLAMAV_DATABASE)) {
  throw new Error('CLAMAV_DATABASE does not exist. Refresh the local ClamAV signatures.');
}

const backend = resolve(root, 'apps/backend');
const binary = resolve(root, '.local-bin/invoyis-api');
mkdirSync(dirname(binary), { recursive: true });
const build = spawnSync('go', ['build', '-o', binary, './cmd/api'], { cwd: backend, env, stdio: 'inherit' });
if (build.status !== 0) process.exit(build.status ?? 1);
if (workerConfigured) {
  const workerBuild = spawnSync('go', ['build', '-o', workerBinary, './cmd/worker'], {
    cwd: backend, env: { ...env, CGO_ENABLED: '0' }, stdio: 'inherit',
  });
  if (workerBuild.status !== 0) process.exit(workerBuild.status ?? 1);
}

const children = [];
let stopping = false;
function start(name, command, args, cwd, extraEnv = {}) {
  const child = spawn(command, args, { cwd, env: { ...env, ...extraEnv }, stdio: 'inherit' });
  children.push(child);
  child.once('error', (error) => { console.error(`${name}: ${error.message}`); void stop(1); });
  child.once('exit', (code) => { if (!stopping) { console.error(`${name} stopped unexpectedly`); void stop(code || 1); } });
}

async function stop(code) {
  if (stopping) return;
  stopping = true;
  for (const child of children) if (child.exitCode === null) child.kill('SIGTERM');
  await Promise.race([
    Promise.all(children.map((child) => child.exitCode !== null ? undefined : new Promise((done) => child.once('exit', done)))),
    new Promise((done) => setTimeout(done, 3000)),
  ]);
  process.exit(code);
}

process.on('SIGINT', () => void stop(0));
process.on('SIGTERM', () => void stop(0));
if (storageArgs) start('Local S3', env.LOCAL_S3_BINARY, storageArgs, root);
start('Mailpit', 'mailpit', ['--listen', '127.0.0.1:8025', '--smtp', '127.0.0.1:1025'], root);
start('Go API', binary, [], backend, { API_ADDR: '127.0.0.1:8081' });
if (workerConfigured) start('Document worker', workerBinary, [], backend);
start('Next.js', process.execPath, [
  resolve(root, 'node_modules/next/dist/bin/next'), 'dev',
  '--hostname', 'localhost', '--port', '3000',
  '--experimental-https', '--experimental-https-key', key, '--experimental-https-cert', certificate,
], resolve(root, 'apps/web'));
console.log('Sign in at https://localhost:3000/sign-in; Mailpit at http://127.0.0.1:8025. Press Ctrl+C to stop all services.');
