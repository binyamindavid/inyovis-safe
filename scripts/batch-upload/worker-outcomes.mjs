import assert from 'node:assert/strict';
import { archive, other as pdf, sql, browserContext } from './config.mjs';
const { browser, context } = await browserContext();
try{
 const language=await context.request.put('https://localhost:3000/api/v1/account/language',{headers:{Origin:'https://localhost:3000','X-Invoyis-Intent':'browser'},data:{language:'en'}});assert.equal(language.status(),200);
 for(const outcome of ['processing_failed','file_blocked']){
  const page=await context.newPage();let document;
  page.on('response',async response=>{if(response.url().endsWith('/complete')&&response.status()===200){try{document=(await response.json()).documentId;}catch{}}});
  try{await page.goto(`https://localhost:3000/archives/${archive}`);await page.locator('input[type=file]').waitFor();await page.locator('input[type=file]').setInputFiles({name:`worker-${outcome}.pdf`,mimeType:'application/pdf',buffer:pdf});await page.locator('ol li').first().getByText('Received', { exact: true }).waitFor({ state: 'attached', timeout: 20000 });assert.ok(document);
   const failure=outcome==='file_blocked'?'scan_rejected':'invalid_pdf';sql(`BEGIN; UPDATE invoyis.processing_jobs SET state='failed', failure_code='${failure}', lease_token=NULL, lease_expires_at=NULL WHERE document_id='${document}'; UPDATE invoyis.documents SET state='failed',clearance='blocked' WHERE id='${document}'; COMMIT;`);
   await page.locator('ol li').first().getByText(outcome==='file_blocked'?'File blocked':'Processing failed').waitFor({timeout:20000});const row=await page.locator('ol li').first().innerText();assert.match(row,/Receipt/);assert.equal(await page.locator('ol li').first().getByRole('button',{name:/Retry/}).count(),0);console.log(JSON.stringify({case:outcome,document,receiptRetained:true,uploadRetry:false,source:'synthetic database worker-state injection',result:'pass'}));
  }finally{await page.close();}
 }
}finally{await browser.close();}
