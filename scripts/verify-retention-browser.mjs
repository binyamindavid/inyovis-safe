import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { mkdir } from 'node:fs/promises';
const base=process.env.BASE_URL||'http://127.0.0.1:3100';
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'});
const archive='11111111-1111-4111-8111-111111111111',doc='22222222-2222-4222-8222-222222222222',decision='33333333-3333-4333-8333-333333333333';
const output='docs/screenshots/retention-review';await mkdir(output,{recursive:true});
const archiveRow={id:archive,name:'Synthetic company',kind:'business',role:'admin',jurisdiction:'FR',canBulkExport:true};
const documentRow={id:doc,status:'ready',originalFilename:'invoice-עברית.pdf',confirmedType:'invoice',confirmedTitle:'Invoice 00042-AB',issueDate:'2024-02-29',printedSupplier:'ספק Café',documentNumber:'00042-AB',referenceScan:false};
const detail={current:{state:'current',snapshot:{id:decision,archiveId:archive,documentId:doc,confirmedRevisionId:'44444444-4444-4444-8444-444444444444',ruleSetVersion:'synthetic-fr-v1',inputs:{facts:{kind:'business',jurisdiction:'FR',entity:'company',documentType:'invoice',issueDate:'2024-02-29'}},outcome:{status:'calculated',keepThrough:'2026-02-28',obligations:[{ruleId:'FR-INVOICE',ruleVersion:1,source:'Synthetic French rule',trigger:'issue_date',inputs:{issueDate:'2024-02-29'},keepThrough:'2026-02-28'}]}}},history:[],holds:[],reviewBlocker:'',canManage:true,canEdit:true};detail.history=[detail.current.snapshot];
try{for(const locale of ['en','fr','he']){
 const page=await browser.newPage({viewport:{width:1440,height:900},deviceScaleFactor:1});
 let lateBlocker='';
 await page.context().addCookies([{name:'invoyis_locale',value:locale,domain:'127.0.0.1',path:'/'}]);
 await page.route('**/api/v1/archives**',route=>{const url=new URL(route.request().url()),path=url.pathname;
  if(path==='/api/v1/archives')return route.fulfill({json:{items:[archiveRow]}});
  if(path===`/api/v1/archives/${archive}`)return route.fulfill({json:archiveRow});
  if(path.endsWith('/document-counts'))return route.fulfill({json:{total:1,needsReview:0,attention:0}});
  if(path.endsWith('/document-folders'))return route.fulfill({json:{by:url.searchParams.get('by'),items:[],unassigned:1,total:1}});
  if(path.endsWith('/documents'))return route.fulfill({json:{items:[documentRow],nextCursor:''}});
  if(path.endsWith('/status'))return route.fulfill({json:documentRow});
  if(path.endsWith('/shredding-review')&&lateBlocker)return route.fulfill({status:409,json:{code:'CONFLICT'}});
  if(path.endsWith('/retention')||path.endsWith('/shredding-review'))return route.fulfill({json:lateBlocker?{...detail,reviewBlocker:lateBlocker}:detail});
  if(path.includes('/preview/'))return route.fulfill({status:422,json:{code:'PREVIEW_UNAVAILABLE'}});
  if(path.includes('/organisation/'))return route.fulfill({json:{items:[]}});
  return route.fulfill({status:404,json:{code:'NOT_FOUND'}});
 });
 await page.goto(`${base}/archives/${archive}?view=all`);await page.locator(`#row-${doc}`).waitFor();await page.locator(`#row-${doc}`).click();
 await page.getByRole('heading',{name:locale==='he'?'שמירה':locale==='fr'?'Conservation':'Retention'}).waitFor();
 assert.equal(await page.locator('html').getAttribute('dir'),locale==='he'?'rtl':'ltr');
 assert.equal(await page.getByText('2026-02-28').count()>0,true);
 await page.keyboard.press('Tab');assert.notEqual(await page.evaluate(()=>document.activeElement?.tagName),'BODY');
 await page.screenshot({path:`${output}/retention-${locale}.png`,fullPage:true});
 await page.getByRole('link',{name:locale==='he'?'בדיקה לקראת גריסה':locale==='fr'?'Examiner pour destruction':'Review for shredding'}).click();
 await page.waitForURL('**/shredding-review');await page.getByText(locale==='he'?'בדיקה זו משאירה את המסמך בארכיון שלך.':locale==='fr'?'Cet examen laisse le document dans vos archives.':'This review leaves the document in your archive.').waitFor();
 await page.screenshot({path:`${output}/review-${locale}.png`,fullPage:true});
 if(locale==='en'){await page.goto(`${base}/archives/${archive}?view=all`);await page.locator(`#row-${doc}`).click();await page.getByRole('link',{name:'Review for shredding'}).waitFor();lateBlocker='active_hold';await page.getByRole('link',{name:'Review for shredding'}).click();await page.getByRole('alert').getByText('active hold').waitFor();}
 console.log(`${locale}: retention, date, review, keyboard and direction passed`);await page.close();
}
 // Personal reminders stay date-only and do not show shredding review.
 {const page=await browser.newPage({viewport:{width:1280,height:800}});let saved='';
  await page.route('**/api/v1/archives**',route=>{const u=new URL(route.request().url()),p=u.pathname;
   if(p==='/api/v1/archives')return route.fulfill({json:{items:[{...archiveRow,kind:'personal',jurisdiction:null}]}});
   if(p===`/api/v1/archives/${archive}`)return route.fulfill({json:{...archiveRow,kind:'personal',jurisdiction:null}});
   if(p.endsWith('/document-counts'))return route.fulfill({json:{total:1,needsReview:0,attention:0}});
   if(p.endsWith('/document-folders'))return route.fulfill({json:{by:u.searchParams.get('by'),items:[],unassigned:1,total:1}});
   if(p.endsWith('/documents'))return route.fulfill({json:{items:[documentRow],nextCursor:''}});
   if(p.endsWith('/status'))return route.fulfill({json:documentRow});
   if(p.endsWith('/reminder')&&route.request().method()==='PUT'){saved=route.request().postDataJSON().date;return route.fulfill({status:204,body:''})}
   if(p.endsWith('/retention'))return route.fulfill({json:{current:{state:'unavailable'},history:[],holds:[],reminder:saved||undefined,reviewBlocker:'business_document_required',canManage:true,canEdit:true}});
   if(p.includes('/preview/'))return route.fulfill({status:422,json:{code:'PREVIEW_UNAVAILABLE'}});
   return route.fulfill({status:404,json:{code:'NOT_FOUND'}});
  });
  await page.goto(`${base}/archives/${archive}?view=all`);await page.locator(`#row-${doc}`).click();await page.getByRole('heading',{name:'Reminder'}).waitFor();await page.locator('input[type=date]').fill('2026-11-14');await page.getByRole('button',{name:'Save reminder'}).click();await page.waitForFunction(()=>document.querySelector('input[type=date]')?.value==='2026-11-14');assert.equal(saved,'2026-11-14');assert.equal(await page.getByRole('link',{name:'Review for shredding'}).count(),0);await page.close();console.log('personal: date-only reminder and no shredding action passed');
 }
}finally{await browser.close()}
