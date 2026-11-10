import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';

const origin = process.env.BROWSER_ORIGIN;
const database = process.env.DATABASE_URL;
if (!origin?.startsWith('https://localhost:') || !database) throw new Error('Set local BROWSER_ORIGIN and DATABASE_URL');
const marker = Date.now();
const address = (name) => `pilot+${name}${marker}@example.test`;
const sql = (statement) => execFileSync('psql', [database, '-X', '-v', 'ON_ERROR_STOP=1', '-Atc', statement], { encoding: 'utf8' });
const seed = (email) => sql(`INSERT INTO invoyis.accounts (email) VALUES ('${email}') ON CONFLICT (email) DO NOTHING`);

async function post(path, body, options = {}) {
  return fetch(`${origin}/api/v1/auth/${path}`, {
    method: 'POST',
    headers: { Origin: options.origin ?? origin, 'X-Invoyis-Intent': 'browser', 'Content-Type': 'application/json', Cookie: options.cookie ?? '', 'Accept-Language': 'en' },
    body: JSON.stringify(body),
  });
}
const request = (email, options) => post('code/request', { email }, options);
const verify = (email, code) => post('code/verify', { email, code });
const session = (cookie) => fetch(`${origin}/api/v1/auth/session`, { headers: { Cookie: cookie } });
const logout = (cookie) => post('logout', {}, { cookie });
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function latestCode(email) {
  for (let attempt = 0; attempt < 30; attempt++) {
    const inbox = await (await fetch('http://127.0.0.1:8025/api/v1/messages')).json();
    const message = inbox.messages?.find((item) => item.To.some((recipient) => recipient.Address === email));
    const code = message?.Snippet.match(/\b[0-9]{6}\b/)?.[0];
    if (code) return code;
    await pause(200);
  }
  throw new Error('Mailpit did not receive a synthetic code');
}

const successEmail = address('success');
seed(successEmail);
assert.equal((await request(successEmail)).status, 202);
const successCode = await latestCode(successEmail);
assert.equal((await verify(successEmail, '000000')).status, 422);
const signedIn = await verify(successEmail, successCode);
assert.equal(signedIn.status, 200);
const sessionCookie = signedIn.headers.getSetCookie()[0].split(';')[0];
assert.match(signedIn.headers.getSetCookie()[0], /Secure/);
assert.equal((await session(sessionCookie)).status, 200);
assert.equal((await logout(sessionCookie)).status, 204);
assert.equal((await session(sessionCookie)).status, 401);
console.log('HTTPS Mailpit sign-in, invalid code, restoration, logout, and revoked-cookie replay passed');

const resendEmail = address('resend');
seed(resendEmail);
assert.equal((await request(resendEmail)).status, 202);
const oldCode = await latestCode(resendEmail);
assert.equal((await request(resendEmail)).status, 202);
await pause(500);
const inbox = await (await fetch('http://127.0.0.1:8025/api/v1/messages')).json();
const resendMessages = inbox.messages.filter((item) => item.To.some((recipient) => recipient.Address === resendEmail));
const newCode = resendMessages[0]?.Snippet.match(/\b[0-9]{6}\b/)?.[0];
assert.ok(newCode);
if (newCode !== oldCode) assert.equal((await verify(resendEmail, oldCode)).status, 422);
const concurrent = await Promise.all([verify(resendEmail, newCode), verify(resendEmail, newCode)]);
assert.deepEqual(concurrent.map((response) => response.status).sort(), [200, 422]);
console.log('HTTPS resend invalidation and concurrent single redemption passed');

const expiryEmail = address('expiry');
seed(expiryEmail);
assert.equal((await request(expiryEmail)).status, 202);
const expiryCode = await latestCode(expiryEmail);
sql(`UPDATE invoyis.auth_challenges SET expires_at=now()-interval '1 minute' WHERE email='${expiryEmail}'`);
const expired = await verify(expiryEmail, expiryCode);
assert.equal(expired.status, 422);
assert.equal((await expired.json()).code, 'EXPIRED_CODE');
console.log('HTTPS expired-code rejection passed');

const sessionExpiryEmail = address('sessionexpiry');
seed(sessionExpiryEmail);
assert.equal((await request(sessionExpiryEmail)).status, 202);
const sessionExpiryCode = await latestCode(sessionExpiryEmail);
const expirySignIn = await verify(sessionExpiryEmail, sessionExpiryCode);
assert.equal(expirySignIn.status, 200);
const expiryCookie = expirySignIn.headers.getSetCookie()[0].split(';')[0];
sql(`UPDATE invoyis.sessions SET expires_at=now()-interval '1 minute' WHERE account_id=(SELECT id FROM invoyis.accounts WHERE email='${sessionExpiryEmail}')`);
assert.equal((await session(expiryCookie)).status, 401);
console.log('HTTPS session expiry passed');

const limitEmail = address('limit');
seed(limitEmail);
for (let i = 0; i < 5; i++) assert.equal((await request(limitEmail)).status, 202);
const limited = await request(limitEmail);
assert.equal(limited.status, 429);
assert.equal(limited.headers.get('retry-after'), '900');
const crossOrigin = await request(address('cross'), { origin: 'https://evil.example' });
assert.equal(crossOrigin.status, 403);
console.log('HTTPS request rate limit and cross-origin rejection passed');
