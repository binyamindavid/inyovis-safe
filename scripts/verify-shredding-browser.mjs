import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { mkdir } from 'node:fs/promises';

const base=process.env.BASE_URL||'http://127.0.0.1:3100';
const output=process.env.SCREENSHOTS||'docs/screenshots/shredding-draft';
await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'});
const archive='11111111-1111-4111-8111-111111111111',doc='22222222-2222-4222-8222-222222222222';
const name='invoice-עברית.pdf';
const evidence={archiveKind:'business',current:{state:'current',snapshot:{id:'33333333-3333-4333-8333-333333333333',ruleSetVersion:'synthetic-v1',inputs:{facts:{jurisdiction:'FR'}},outcome:{status:'calculated',keepThrough:'2024-12-31',obligations:[]}}},holds:[],reviewBlocker:''};
try{
 for(const locale of ['en','fr','he'])for(const [device,width,height] of [['desktop',1440,900],['mobile',390,844]]){
  const page=await browser.newPage({viewport:{width,height},deviceScaleFactor:1});
  await page.context().addCookies([{name:'invoyis_locale',value:locale,domain:'127.0.0.1',path:'/'}]);
  let submitted;
  await page.route('**/api/v1/archives/**',route=>{
   const path=new URL(route.request().url()).pathname;
   if(path.endsWith('/status'))return route.fulfill({json:{originalFilename:name}});
   if(path.endsWith('/shred')&&route.request().method()==='GET')return route.fulfill({json:{status:'eligible'}});
   if(path.endsWith('/shred')&&route.request().method()==='POST'){submitted=route.request().postDataJSON();return route.fulfill({json:{status:'pending'}})}
   if(path.endsWith('/shredding-review')||path.endsWith('/retention'))return route.fulfill({json:evidence});
   return route.fulfill({status:404,json:{code:'NOT_FOUND'}});
  });
  await page.goto(`${base}/archives/${archive}/documents/${doc}/shredding-review`);
  const action=page.getByRole('button',{name:locale==='fr'?'Détruire définitivement':locale==='he'?'גריסה לצמיתות':'Shred permanently'}).first();
  await action.waitFor();
  assert.equal(await page.locator('html').getAttribute('dir'),locale==='he'?'rtl':'ltr');
  await action.focus();await page.keyboard.press('Enter');
  const dialog=page.getByRole('dialog');await dialog.waitFor();
  await page.screenshot({path:`${output}/${locale}-${device}-confirmation.png`,fullPage:true});
  const confirm=dialog.getByRole('button',{name:locale==='fr'?'Détruire définitivement':locale==='he'?'גריסה לצמיתות':'Shred permanently'});
  assert.equal(await confirm.isDisabled(),true);
  await dialog.getByRole('textbox').fill(name);
  assert.equal(await confirm.isDisabled(),false);
  await confirm.focus();await page.keyboard.press('Enter');
  await page.getByRole('status').filter({hasText:locale==='fr'?'Suppression en attente':locale==='he'?'המחיקה ממתינה':'Deletion pending'}).waitFor();
  assert.deepEqual(submitted,{name});
  await page.screenshot({path:`${output}/${locale}-${device}-pending.png`,fullPage:true});
  await page.close();
  console.log(`${locale} ${device}: confirmation keyboard and pending state passed`);
 }
}finally{await browser.close()}
