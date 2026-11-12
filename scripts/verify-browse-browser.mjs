import assert from 'node:assert/strict';
import {chromium} from 'playwright';
// Browser check for the archive library with a mocked archive API. Run while `yarn dev:local` (or `yarn dev`) serves the web app.
// BASE_URL defaults to the HTTPS dev:local origin; CHROME_PATH defaults to macOS Chrome, set CHROME_PATH= (empty) to use Playwright's Chromium.
const base=process.env.BASE_URL||'https://localhost:3000';
const host=new URL(base).hostname;
const executablePath=process.env.CHROME_PATH===''?undefined:(process.env.CHROME_PATH||'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome');
const browser=await chromium.launch({headless:true,executablePath});
const a='11111111-1111-4111-8111-111111111111',supplier='22222222-2222-4222-8222-222222222222',category='33333333-3333-4333-8333-333333333333',property='44444444-4444-4444-8444-444444444444',location='55555555-5555-4555-8555-555555555555',doc='66666666-6666-4666-8666-666666666666',doc2='77777777-7777-4777-8777-777777777777';
const words={
 en:{filter:'Filter',category:'Category',edit:'Edit details',supplierHistory:'Supplier history',processing:'Processing',needsReview:'Needs review',reference:'Reference'},
 fr:{filter:'Filtrer',category:'Catégorie',edit:'Modifier les détails',supplierHistory:'Historique du fournisseur',processing:'Traitement en cours',needsReview:'À vérifier',reference:'Référence'},
 he:{filter:'סינון',category:'קטגוריה',edit:'עריכת פרטים',supplierHistory:'היסטוריית הספק',processing:'בעיבוד',needsReview:'דורש בדיקה',reference:'מספר סימוכין'},
};
const archive={id:a,name:'Archive',kind:'personal',role:'viewer',jurisdiction:null,canBulkExport:false};
try{for(const locale of ['en','fr','he']){
 const w=words[locale];
 const page=await browser.newPage({ignoreHTTPSErrors:true});await page.context().addCookies([{name:'invoyis_locale',value:locale,domain:host,path:'/'}]);
 let queries=[];await page.route('**/api/v1/archives**',async route=>{const u=new URL(route.request().url());const p=u.pathname;
 if(p==='/api/v1/archives')return route.fulfill({json:{items:[archive]}});
 if(p.endsWith(`/archives/${a}`))return route.fulfill({json:archive});
 if(p.includes('/organisation/')){if(p.endsWith(`/suppliers/${supplier}`))return route.fulfill({json:{id:supplier,displayName:'ספק Café',archived:false}});const kind=p.split('/').at(-1);const item=kind==='categories'?{id:category,displayName:'Énergie'}:kind==='properties'?{id:property,displayName:'דירה א׳'}:{id:location,displayName:'Shelf A'};return route.fulfill({json:{items:[item]}})}
 if(p.endsWith('/original'))return route.fulfill({status:200,body:Buffer.from('%PDF-1.4\n%%EOF'),headers:{'content-type':'application/pdf','content-disposition':'attachment; filename="synthetic.pdf"'}});
 if(p.includes('/preview/'))return route.fulfill({status:422,json:{code:'PREVIEW_UNAVAILABLE'}});
 if(p.endsWith('/review'))return route.fulfill({status:404,json:{code:'NOT_FOUND'}});
 if(p.endsWith('/documents')){queries.push(u.searchParams.toString());const second=u.searchParams.has('cursor');const item=second?{id:doc2,status:'ready',originalFilename:'second.pdf',confirmedType:'lease',confirmedTitle:'Second page lease',issueDate:'2025-01-01',referenceScan:false}:{id:doc,status:'ready',originalFilename:'file.pdf',confirmedType:'invoice',issueDate:'2026-01-02',printedSupplier:'ספק Café',documentNumber:'000-07',accountReference:'00 1-A',total:'10.00',currency:'ILS',supplierId:supplier,supplierName:'ספק Café',categoryName:'Énergie',propertyName:'דירה א׳',paperLocationName:null,referenceScan:true};return route.fulfill({json:{items:[item],nextCursor:second?'':'next'}})}
 return route.fulfill({status:404,json:{code:'NOT_FOUND'}})});
 await page.goto(`${base}/archives/${a}`);await page.getByRole('heading',{name:'Archive',level:1}).waitFor();assert.equal(await page.locator('html').getAttribute('dir'),locale==='he'?'rtl':'ltr');
 // The library follows nextCursor itself, so later pages appear without a "More documents" button.
 await page.getByText('Second page lease').waitFor();assert(queries.some(q=>q.includes('cursor=next')));
 await page.getByRole('button',{name:w.filter}).click();await page.getByRole('dialog').getByRole('button',{name:new RegExp(w.category)}).click();await page.getByRole('option',{name:'Énergie'}).click();await page.waitForTimeout(150);
 assert(queries.some(q=>q.includes(`categoryId=${category}`)&&!q.includes('cursor=')));await page.keyboard.press('Escape');
 await page.locator(`#row-${doc}`).click();await page.getByText('000-07').waitFor();
 assert.equal(await page.getByRole('link',{name:w.edit}).count(),1);assert.equal(await page.locator(`a[href="/api/v1/archives/${a}/documents/${doc}/original"]`).count(),1);
 await page.keyboard.press('Tab');assert(await page.evaluate(()=>document.activeElement?.tagName!=='BODY'));
 const download=page.waitForEvent('download');await page.locator(`a[href="/api/v1/archives/${a}/documents/${doc}/original"]`).click();assert.equal((await download).suggestedFilename(),'synthetic.pdf');
 await page.getByRole('link',{name:'ספק Café'}).click();await page.getByRole('heading',{name:'ספק Café',level:1}).waitFor();await page.getByText(w.supplierHistory).first().waitFor();await page.locator(`#row-${doc}`).click();await page.getByText('00 1-A').waitFor();
 await page.goBack();await page.goBack();await page.locator(`#row-${doc}`).waitFor();
 await page.getByRole('link',{name:w.edit}).click();await page.waitForURL(`**/documents/${doc}/review`);await page.close();console.log(`${locale}: library, auto-pagination, filters, supplier history, links, RTL and keyboard passed`)
 }
 const page=await browser.newPage({ignoreHTTPSErrors:true});await page.context().addCookies([{name:'invoyis_locale',value:'en',domain:host,path:'/'}]);let calls=0;const started=Date.now();
 await page.route('**/api/v1/archives**',async route=>{const u=new URL(route.request().url());const p=u.pathname;if(p==='/api/v1/archives')return route.fulfill({json:{items:[archive]}});if(p.endsWith(`/archives/${a}`))return route.fulfill({json:archive});if(p.includes('/organisation/'))return route.fulfill({json:{items:[]}});if(p.includes('/preview/'))return route.fulfill({status:422,json:{code:'PREVIEW_UNAVAILABLE'}});if(p.endsWith('/documents')){calls++;return route.fulfill({json:{items:[{id:doc,status:Date.now()-started<2000?'processing':'needs_review',originalFilename:'lease.pdf',confirmedType:'lease',confirmedTitle:'Lease',documentNumber:'000-42',referenceScan:false}],nextCursor:''}})}return route.fulfill({status:404,json:{code:'NOT_FOUND'}})});
 await page.goto(`${base}/archives/${a}`);await page.getByText(words.en.processing,{exact:true}).waitFor();await page.getByText(words.en.needsReview,{exact:true}).first().waitFor({timeout:8000});assert(calls>=2);
 await page.locator(`#row-${doc}`).click();await page.getByText('000-42').waitFor();assert.match(await page.locator('dl').innerText(),/Reference\s*000-42/);
 assert.equal(await page.getByRole('link',{name:'Review',exact:true}).count(),1);await page.close();console.log('processing to needs-review refresh without navigation passed');
}finally{await browser.close()}
