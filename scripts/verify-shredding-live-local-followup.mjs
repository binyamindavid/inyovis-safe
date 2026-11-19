import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { mkdir } from 'node:fs/promises';
const base='https://localhost:3000', archive='70ef950f-8662-4553-825e-8714619db3fc', doc='0577763d-bae5-4877-a76d-9fdddc5d3e3a';
const out='docs/screenshots/shredding-live-local';await mkdir(out,{recursive:true});
const browser=await chromium.launch({channel:'chrome',headless:true});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:900}});
try{
 const page=await context.newPage();
 const mail='http://127.0.0.1:8025/api/v1/messages';const before=new Set((await (await fetch(mail)).json()).messages?.map(x=>x.ID)??[]);
 await page.goto(base+'/sign-in');await page.getByRole('textbox',{name:'Email address'}).fill('shred-live@example.test');await page.getByRole('button',{name:'Send code'}).click();await page.getByRole('textbox',{name:'Six-digit code'}).waitFor();
 let code;for(let i=0;i<40;i++){const messages=(await (await fetch(mail)).json()).messages??[];code=messages.find(x=>!before.has(x.ID)&&x.To.some(y=>y.Address==='shred-live@example.test'))?.Snippet.match(/\b[0-9]{6}\b/)?.[0];if(code)break;await new Promise(r=>setTimeout(r,250))}assert.ok(code);
 await page.getByRole('textbox',{name:'Six-digit code'}).fill(code);await page.getByRole('button',{name:'Sign in',exact:true}).click();await page.getByText('Signed in as').waitFor();
 const path=`${base}/api/v1/archives/${archive}/documents/${doc}`;const headers={Origin:base,'X-Invoyis-Intent':'browser'};
 const review=await context.request.get(path+'/review');console.log('review read',review.status(),await review.text());assert.equal(review.status(),200);
 const version=(await review.json()).reviewVersion;
 const write=await context.request.put(path+'/review',{headers,data:{action:'confirm',expectedVersion:version,idempotencyKey:crypto.randomUUID(),sourceRunId:null,metadata:{documentType:'lease',title:'Local disposal proof',issueDate:'2020-01-01'},acknowledgedWarnings:[],inputLocale:'en',rawAmounts:{subtotal:null,tax:null,total:null,taxLines:[]}}});console.log('review write',write.status(),await write.text());assert.equal(write.status(),200);
 const eligible=await context.request.get(path+'/shred');console.log('eligibility',eligible.status(),await eligible.text());assert.equal((await eligible.json()).status,'eligible');
 await page.goto(`${base}/archives/${archive}/documents/${doc}/shredding-review`);
 const action=page.getByRole('button',{name:'Shred permanently'}).first();await action.waitFor();await action.focus();await page.keyboard.press('Enter');
 const dialog=page.getByRole('dialog');await dialog.waitFor();await page.screenshot({path:`${out}/en-desktop-confirmation.png`,fullPage:true});
 const confirm=dialog.getByRole('button',{name:'Shred permanently'});assert.equal(await confirm.isDisabled(),true);
 await dialog.getByRole('textbox').fill('shred-live-en.pdf');assert.equal(await confirm.isDisabled(),false);await confirm.focus();await page.keyboard.press('Enter');
 await page.getByRole('status').filter({hasText:'Deletion pending'}).waitFor();await page.screenshot({path:`${out}/en-desktop-pending.png`,fullPage:true});
 console.log('live local confirmation and pending passed');
}finally{await browser.close()}
