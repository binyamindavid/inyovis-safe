import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { mkdir } from 'node:fs/promises';
const base='https://localhost:3000';
const archive=process.env.LIVE_ARCHIVE_ID, doc=process.env.LIVE_DOC_ID;
const locale=process.env.LIVE_LOCALE??'fr';
assert.match(archive??'',/^[0-9a-f-]{36}$/i);assert.match(doc??'',/^[0-9a-f-]{36}$/i);
assert.ok(['fr','he'].includes(locale));assert.ok(process.env.LIVE_BROWSER_STATE);
const out='docs/screenshots/shredding-live-local';await mkdir(out,{recursive:true});
const device=process.env.LIVE_DEVICE??(locale==='he'?'mobile':'desktop');assert.ok(['desktop','mobile'].includes(device));
const browser=await chromium.launch({channel:'chrome',headless:true});
const context=await browser.newContext({ignoreHTTPSErrors:true,storageState:process.env.LIVE_BROWSER_STATE,viewport:device==='mobile'?{width:390,height:844}:{width:1440,height:900}});
try{
 const page=await context.newPage();const path=`${base}/api/v1/archives/${archive}/documents/${doc}`;
 await page.goto(`${base}/archives/${archive}/documents/${doc}/review`);
 const initial=await context.request.get(path+'/review');assert.equal(initial.status(),200);
 if(!(await initial.json()).confirmed){await page.locator('[data-type-choice]').waitFor();await page.getByRole('button',{name:'Lease',exact:true}).click();await page.getByRole('button',{name:'File document'}).click();}
 const review=await context.request.get(path+'/review');assert.equal(review.status(),200);assert.ok((await review.json()).confirmed,'normal review did not confirm document');
 const changed=await context.request.put(base+'/api/v1/account/language',{headers:{Origin:base,'X-Invoyis-Intent':'browser'},data:{language:locale}});assert.equal(changed.status(),200);
 let eligible=false;for(let i=0;i<15;i++){const response=await context.request.get(path+'/shred');const state=await response.json();if(state.status==='eligible'){eligible=true;break}await new Promise(r=>setTimeout(r,200))}assert.ok(eligible,'document not eligible after review');
 const status=await context.request.get(path+'/status');assert.equal(status.status(),200);const name=(await status.json()).originalFilename;
 await page.goto(`${base}/archives/${archive}/documents/${doc}/shredding-review`);
 const label=locale==='fr'?'Détruire définitivement':'גריסה לצמיתות';const action=page.getByRole('button',{name:label}).first();await action.waitFor();
 assert.equal(await page.locator('main').getAttribute('dir'),locale==='he'?'rtl':'ltr');
 await action.focus();await page.keyboard.press('Enter');const dialog=page.getByRole('dialog');await dialog.waitFor();
 const confirm=dialog.getByRole('button',{name:label});assert.equal(await confirm.isDisabled(),true);
 await page.screenshot({path:`${out}/${locale}-${device}-confirmation-live.png`,fullPage:true});
 await dialog.getByRole('textbox').fill(name);assert.equal(await confirm.isDisabled(),false);
 await confirm.focus();await page.keyboard.press('Enter');
 await page.getByRole('status').filter({hasText:locale==='fr'?'Suppression en attente':'המחיקה ממתינה'}).waitFor();
 await page.screenshot({path:`${out}/${locale}-${device}-pending-live.png`,fullPage:true});
 console.log(locale,'normal review and keyboard shred confirmation reached pending through real local API');
}finally{await browser.close()}
