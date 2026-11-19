import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { mkdir, readFile } from 'node:fs/promises';
const base='https://localhost:3000', mail='http://127.0.0.1:8025/api/v1/messages';
const output='docs/screenshots/shredding-live-local';
await mkdir(output,{recursive:true});
const browser=await chromium.launch({channel:'chrome',headless:true});
const context=await browser.newContext({ignoreHTTPSErrors:true});
try {
 const page=await context.newPage();
 const before=new Set((await (await fetch(mail)).json()).messages?.map(x=>x.ID)??[]);
 await page.goto(base+'/sign-in');
 await page.getByRole('textbox',{name:'Email address'}).fill('shred-live@example.test');
 await page.getByRole('button',{name:'Send code'}).click();
 await page.getByRole('textbox',{name:'Six-digit code'}).waitFor();
 let code;
 for(let i=0;i<40;i++){
  const messages=(await (await fetch(mail)).json()).messages??[];
  code=messages.find(x=>!before.has(x.ID)&&x.To.some(y=>y.Address==='shred-live@example.test'))?.Snippet.match(/\b[0-9]{6}\b/)?.[0];
  if(code)break;await new Promise(r=>setTimeout(r,250));
 }
 assert.ok(code,'no synthetic sign-in code');
 await page.getByRole('textbox',{name:'Six-digit code'}).fill(code);
 await page.getByRole('button',{name:'Sign in',exact:true}).click();
 await page.getByText('Signed in as').waitFor();
 const headers={Origin:base,'X-Invoyis-Intent':'browser'};
 const created=await context.request.post(base+'/api/v1/archives',{headers,data:{kind:'personal',name:'Shred live local verification'}});
 assert.equal(created.status(),201,await created.text());
 const archive=(await created.json()).id;
 console.log('archive',archive);
 const fixture=await readFile('.local-bin/issue35-fixtures/lease.pdf');
 const name='shred-live-en.pdf';
 await page.goto(`${base}/archives/${archive}`);
 await page.getByRole('button',{name:'Add documents'}).click();
 await page.locator('input[type=file]').first().setInputFiles({name,mimeType:'application/pdf',buffer:fixture});
 await page.getByRole('button',{name:/Upload to/}).click();
 await page.locator('[data-upload-queue] [data-receipt-id]').waitFor({timeout:30000});
 console.log('uploaded');
 await page.screenshot({path:`${output}/upload-en-desktop.png`,fullPage:true});
} finally {await browser.close()}
