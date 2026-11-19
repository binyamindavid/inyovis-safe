import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { mkdir } from 'node:fs/promises';
const base='https://localhost:3000', archive='70ef950f-8662-4553-825e-8714619db3fc', doc='0577763d-bae5-4877-a76d-9fdddc5d3e3a';
const out='docs/screenshots/shredding-live-local';await mkdir(out,{recursive:true});
const browser=await chromium.launch({channel:'chrome',headless:true});
const context=await browser.newContext({ignoreHTTPSErrors:true});
try{
 const page=await context.newPage(), mail='http://127.0.0.1:8025/api/v1/messages';const before=new Set((await (await fetch(mail)).json()).messages?.map(x=>x.ID)??[]);
 await page.goto(base+'/sign-in');await page.getByRole('textbox',{name:'Email address'}).fill('shred-live@example.test');await page.getByRole('button',{name:'Send code'}).click();await page.getByRole('textbox',{name:'Six-digit code'}).waitFor();
 let code;for(let i=0;i<40;i++){const messages=(await (await fetch(mail)).json()).messages??[];code=messages.find(x=>!before.has(x.ID)&&x.To.some(y=>y.Address==='shred-live@example.test'))?.Snippet.match(/\b[0-9]{6}\b/)?.[0];if(code)break;await new Promise(r=>setTimeout(r,250))}assert.ok(code);
 await page.getByRole('textbox',{name:'Six-digit code'}).fill(code);await page.getByRole('button',{name:'Sign in',exact:true}).click();await page.getByText('Signed in as').waitFor();
 for(const locale of ['en','fr','he'])for(const [device,width,height] of [['desktop',1440,900],['mobile',390,844]]){
  const changed=await context.request.put(base+'/api/v1/account/language',{headers:{Origin:base,'X-Invoyis-Intent':'browser'},data:{language:locale}});assert.equal(changed.status(),200);
  await page.setViewportSize({width,height});await page.goto(`${base}/archives/${archive}/documents/${doc}/shredding-review`);
  await page.getByRole('status').filter({hasText:locale==='fr'?'Nettoyage du stockage actif vérifié':locale==='he'?'ניקוי האחסון הפעיל אומת':'Active storage cleanup verified'}).waitFor();
  assert.equal(await page.locator('main').getAttribute('dir'),locale==='he'?'rtl':'ltr');
  await page.screenshot({path:`${out}/${locale}-${device}-verified.png`,fullPage:true});
  console.log(locale,device,'verified via real API');
 }
}finally{await browser.close()}
