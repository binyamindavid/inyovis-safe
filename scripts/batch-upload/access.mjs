import assert from 'node:assert/strict';
import { archive, lease as pdf, sql, browserContext } from './config.mjs';
const { browser, context } = await browserContext();
try {
 const page=await context.newPage();
 const language=await page.request.put('https://localhost:3000/api/v1/account/language',{headers:{Origin:'https://localhost:3000','X-Invoyis-Intent':'browser'},data:{language:'he'}});
 assert.equal(language.status(),200,await language.text());
 await page.goto(`https://localhost:3000/archives/${archive}`);await page.locator('input[type=file]').waitFor();
 assert.equal(await page.locator('html').getAttribute('dir'),'rtl');
 let transfers=0;await page.route(url=>url.port==='8333',async route=>{transfers++;if(transfers<=2){await route.abort('failed');return;}await route.continue();});
 const filename='invoice חשבונית.pdf';await page.locator('input[type=file]').setInputFiles({name:filename,mimeType:'application/pdf',buffer:pdf});
 const retry=page.getByRole('button',{name:`ניסיון נוסף ${filename}`});await retry.waitFor({timeout:20000});
 const before=await page.locator('ol li').first().innerText();assert.match(before,/ההעלאה הופסקה/);
 assert.equal(await page.locator('ol li bdi[dir=auto]').first().innerText(),filename);
 const live=page.locator('[aria-live=polite]').filter({hasText:filename}).first();assert.ok((await live.innerText()).includes(filename));
 await page.locator('input[type=file]').focus();let focused=false;for(let i=0;i<10;i++){await page.keyboard.press('Tab');if(await retry.evaluate(el=>el===document.activeElement)){focused=true;break;}}
 assert.equal(focused,true,'retry is keyboard reachable');await page.keyboard.press('Enter');
 const status=page.locator('ol li span[tabindex="-1"]').first();await status.waitFor();assert.equal(await status.evaluate(el=>el===document.activeElement),true,'focus moved to row status');
 await page.locator('ol li').first().getByText('התקבל · ממתין לעיבוד').waitFor({timeout:20000});
 assert.match(await page.locator('ol li').first().innerText(),/אישור קליטה/);assert.equal(await page.getByRole('button',{name:`ניסיון נוסף ${filename}`}).count(),0);
 const receiptText=await page.locator('ol li').first().innerText();await page.reload();await page.locator('input[type=file]').waitFor();assert.equal(await page.locator('ol li').count(),0,'queue is page-local');await page.getByText(filename,{exact:true}).first().waitFor({timeout:10000});
 const old=sql(`SELECT id FROM invoyis.documents WHERE archive_id='${archive}' ORDER BY created_at DESC,id DESC OFFSET 20 LIMIT 1`);assert.ok(old);
 const list=await (await page.request.get(`https://localhost:3000/api/v1/archives/${archive}/documents`)).json();assert.equal(list.items.some(i=>i.id===old),false);
 const direct=await page.request.get(`https://localhost:3000/api/v1/archives/${archive}/documents/${old}/status`);assert.equal(direct.status(),200);assert.equal((await direct.json()).id,old);
 console.log(JSON.stringify({case:'hebrew-keyboard-reload-direct-status',browser:browser.version(),transfers,rtl:true,keyboardRetry:true,focusPreserved:true,liveRegion:true,olderDocument:old,receiptVisibleBeforeReload:receiptText.includes('אישור קליטה'),result:'pass'}));
 await page.close();
}finally{await browser.close();}
