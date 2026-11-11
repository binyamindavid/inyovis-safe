import assert from 'node:assert/strict';
import { archive, lease as pdf, sql as query, browserContext } from './config.mjs';
const { browser, context } = await browserContext();
function sql(session) { return query(`SELECT s.id,r.document_id,(SELECT count(*) FROM invoyis.submissions WHERE upload_session_id=s.id),(SELECT count(*) FROM invoyis.processing_jobs j WHERE j.document_id=r.document_id) FROM invoyis.upload_sessions s JOIN invoyis.upload_receipts r ON r.upload_session_id=s.id WHERE s.id='${session}'`); }
async function scenario(name, kind){
 const page=await context.newPage();let session,transferCount=0,completionCount=0,missingCode=false;
 page.on('response',async response=>{if(response.url().endsWith('/upload-sessions')&&response.status()===201){try{session=(await response.json()).id;}catch{}}});
 await page.route(url=>url.port==='8333',async route=>{transferCount++;if(kind==='storage-lost'&&transferCount===1){const response=await route.fetch();assert.ok(response.ok());await route.abort('failed');return;}if(kind==='bytes-missing'&&transferCount===1){await route.abort('failed');return;}await route.continue();});
 await page.route(url=>url.pathname.endsWith('/complete'),async route=>{completionCount++;if(kind==='completion-lost'&&completionCount===1){const response=await route.fetch();assert.equal(response.status(),200);await route.abort('failed');return;}const response=await route.fetch();if(response.status()===409){const body=await response.json();missingCode=body.code==='UPLOAD_STAGED_OBJECT_MISSING';}await route.fulfill({response});});
 try{await page.goto(`https://localhost:3000/archives/${archive}`);await page.locator('input[type=file]').waitFor();await page.locator('input[type=file]').setInputFiles({name:`${name}.pdf`,mimeType:'application/pdf',buffer:pdf});if(kind==='completion-lost'){const retry=page.getByRole('button',{name:`Retry ${name}.pdf`});await retry.waitFor({timeout:20000});await retry.click();}await page.locator('ol li').first().getByText('Received', { exact: true }).waitFor({ state: 'attached', timeout: 20000 });assert.ok(session);const result=sql(session);const [id,document,submissions,jobs]=result.split('|');assert.equal(id,session);assert.equal(submissions,'1');assert.equal(jobs,'1');assert.equal(transferCount,kind==='bytes-missing'?2:1);assert.equal(completionCount,kind==='completion-lost'?2:kind==='bytes-missing'?2:1);if(kind==='bytes-missing')assert.equal(missingCode,true);console.log(JSON.stringify({name,kind,session,document,transferCount,completionCount,missingCode,submissions,jobs,browser:browser.version()}));}finally{await page.close();}
}
async function renewalResponseLost() {
 const page=await context.newPage();const keys=[],sessions=[];let transfers=0,completions=0;
 await page.route(url=>url.pathname.endsWith('/upload-sessions'),async route=>{
  keys.push(JSON.parse(route.request().postData()||'{}').idempotencyKey);
  const response=await route.fetch();assert.equal(response.status(),201);
  const body=await response.json();sessions.push(body.id);
  if(sessions.length===1)query(`UPDATE invoyis.upload_sessions SET grant_expires_at=now()-interval '1 minute' WHERE id='${body.id}'`);
  await route.fulfill({response});
 });
 await page.route(url=>url.port==='8333',async route=>{
  transfers++;
  if(transfers===1){await route.fulfill({status:403,headers:{'Access-Control-Allow-Origin':'https://localhost:3000'},body:'ExpiredToken'});return;}
  if(transfers===2){const response=await route.fetch();assert.ok(response.ok());await route.abort('failed');return;}
  await route.continue();
 });
 await page.route(url=>url.pathname.endsWith('/complete'),async route=>{completions++;await route.continue();});
 try{
  await page.goto(`https://localhost:3000/archives/${archive}`);
  await page.locator('input[type=file]').waitFor();
  await page.locator('input[type=file]').setInputFiles({name:'renewal-response-lost.pdf',mimeType:'application/pdf',buffer:pdf});
  await page.locator('ol li').first().getByText('Received', { exact: true }).waitFor({ state: 'attached', timeout: 20000 });
  assert.equal(keys.length,2);assert.equal(keys[0],keys[1]);assert.equal(sessions[0],sessions[1]);
  assert.equal(transfers,2,'completion must recover accepted bytes without a third transfer');assert.equal(completions,1);
  const [id,document,submissions,jobs]=sql(sessions[0]).split('|');
  assert.equal(id,sessions[0]);assert.equal(submissions,'1');assert.equal(jobs,'1');
  console.log(JSON.stringify({case:'renewal-response-lost',session:id,document,transfers,completions,submissions,jobs,result:'pass'}));
 }finally{await page.close();}
}
try{await scenario('storage-lost','storage-lost');await scenario('bytes-missing','bytes-missing');await scenario('completion-lost','completion-lost');await renewalResponseLost();}finally{await browser.close();}
