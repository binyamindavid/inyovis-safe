import assert from 'node:assert/strict';
import {chromium} from 'playwright';
// Browser checks for the archive library against a mocked archive API (no Go, database or storage needed).
// Run while `yarn dev:local` (or `yarn dev`) serves the web app. BASE_URL defaults to the dev:local HTTPS origin;
// CHROME_PATH defaults to macOS Chrome, or set it to another Chromium binary.
const base=process.env.BASE_URL||'https://localhost:3000';
const host=new URL(base).hostname;
const executablePath=process.env.CHROME_PATH||'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const browser=await chromium.launch({headless:true,executablePath});
const A='11111111-1111-4111-8111-111111111111',B='88888888-8888-4888-8888-888888888888',supplier='22222222-2222-4222-8222-222222222222',category='33333333-3333-4333-8333-333333333333',property='44444444-4444-4444-8444-444444444444',location='55555555-5555-4555-8555-555555555555';
const uuid=n=>`${String(n).padStart(8,'0')}-0000-4000-8000-000000000000`;
const words={
 en:{filter:'Filter',category:'Category',edit:'Edit details',supplierHistory:'Supplier history',processing:'Processing',needsReview:'Needs review',more:'More documents',bill:'Bill',invoiceId:'Printed invoice ID',add:'Add documents',review:'Review'},
 fr:{filter:'Filtrer',category:'Catégorie',edit:'Modifier les détails',supplierHistory:'Historique du fournisseur',processing:'Traitement en cours',needsReview:'À vérifier',more:'Autres documents',bill:'Note',invoiceId:'Numéro imprimé',add:'Ajouter des documents',review:'Vérifier'},
 he:{filter:'סינון',category:'קטגוריה',edit:'עריכת פרטים',supplierHistory:'היסטוריית הספק',processing:'בעיבוד',needsReview:'דורש בדיקה',more:'מסמכים נוספים',bill:'חשבון',invoiceId:'מספר חשבונית מודפס',add:'הוספת מסמכים',review:'בדיקה'},
};
const ready=st=>st==='ready';
/** A small in-memory archive API: status/q/types filters, 20-row cursor pages, archive-wide counts and uploads. */
function mockApi(page,{archives,docs,listArchives=true,log=[]}){
 const pageOf=(rows,cursor)=>{const start=cursor?Number(cursor):0;const items=rows.slice(start,start+20);return {items,nextCursor:start+20<rows.length?String(start+20):''}};
 return page.route('**/api/v1/archives**',async route=>{const u=new URL(route.request().url());const p=u.pathname;const m=route.request().method();log.push(`${m} ${p}?${u.searchParams}`);
  if(p==='/api/v1/archives')return listArchives?route.fulfill({json:{items:archives}}):route.fulfill({status:503,json:{code:'UPSTREAM_UNAVAILABLE'}});
  const a=archives.find(x=>p.startsWith(`/api/v1/archives/${x.id}`));if(!a)return route.fulfill({status:404,json:{code:'NOT_FOUND'}});
  const rest=p.slice(`/api/v1/archives/${a.id}`.length);const mine=docs.filter(d=>d.archive===a.id);
  if(rest==='')return route.fulfill({json:a});
  if(rest==='/document-counts')return route.fulfill({json:{total:mine.length,needsReview:mine.filter(d=>d.status==='needs_review').length,attention:mine.filter(d=>!ready(d.status)).length}});
  if(rest.startsWith('/organisation/')){if(rest===`/organisation/suppliers/${supplier}`)return route.fulfill({json:{id:supplier,displayName:'ספק Café',archived:false}});const kind=rest.split('/').at(-1);const item=kind==='categories'?{id:category,displayName:'Énergie'}:kind==='properties'?{id:property,displayName:'דירה א׳'}:{id:location,displayName:'Shelf A'};return route.fulfill({json:{items:[item]}})}
  if(rest.endsWith('/original'))return route.fulfill({status:200,body:Buffer.from('%PDF-1.4\n%%EOF'),headers:{'content-type':'application/pdf','content-disposition':'attachment; filename="synthetic.pdf"'}});
  if(rest.includes('/preview/'))return route.fulfill({status:422,json:{code:'PREVIEW_UNAVAILABLE'}});
  const status=rest.match(/^\/documents\/([^/]+)\/status$/);if(status){const d=mine.find(x=>x.id===status[1]);return d?route.fulfill({json:d}):route.fulfill({status:404,json:{code:'NOT_FOUND'}})}
  if(rest.endsWith('/review'))return route.fulfill({status:404,json:{code:'NOT_FOUND'}});
  if(rest.endsWith('/documents')){const s=u.searchParams;let rows=rest.startsWith('/suppliers/')?mine.filter(d=>d.supplierId===supplier):mine;
   if(s.get('categoryId'))rows=rows.filter(d=>d.categoryId===s.get('categoryId'));
   const st=s.get('status');if(st==='ready')rows=rows.filter(d=>ready(d.status));if(st==='attention')rows=rows.filter(d=>!ready(d.status));if(st==='needs_review')rows=rows.filter(d=>d.status==='needs_review');
   const q=s.get('q')?.toLowerCase();const types=(s.get('types')||'').split(',').filter(Boolean);
   if(q)rows=rows.filter(d=>[d.originalFilename,d.confirmedTitle,d.printedSupplier,d.documentNumber,d.supplierName].some(v=>v&&v.toLowerCase().includes(q))||types.includes(d.confirmedType));
   return route.fulfill({json:pageOf(rows,s.get('cursor'))})}
  if(rest==='/upload-sessions'&&m==='POST'){const body=route.request().postDataJSON();return route.fulfill({status:201,json:{id:uuid(9000+docs.length),state:'created',finaliseBy:'2026-11-13T10:00:00Z',grant:{method:'PUT',url:`${base}/mock-storage/${a.id}/${encodeURIComponent(body.filename)}`,headers:{},fields:{},expiresAt:'2026-11-12T12:00:00Z'}}})}
  if(rest.endsWith('/complete')){const id=uuid(7000+docs.length);const doc={id,archive:a.id,status:'processing',originalFilename:'uploaded.pdf',referenceScan:false};docs.unshift(doc);setTimeout(()=>{doc.status='needs_review'},2500);return route.fulfill({json:{id:uuid(6000+docs.length),submissionId:uuid(5000),documentId:id,state:'received',byteCount:20,sha256:'a'.repeat(64),receivedAt:'2026-11-12T10:00:00Z'}})}
  return route.fulfill({status:404,json:{code:'NOT_FOUND'}})});
}
function libraryDocs(){
 const docs=[];
 // 45 confirmed documents push undated, unconfirmed ones far past the first page of a date-ordered list.
 for(let i=0;i<45;i++)docs.push({id:uuid(100+i),archive:A,status:'ready',originalFilename:`file-${i}.pdf`,confirmedType:'lease',confirmedTitle:`Lease ${String(i).padStart(2,'0')}`,issueDate:'2026-01-01',referenceScan:false});
 docs.unshift({id:uuid(1),archive:A,status:'ready',originalFilename:'file.pdf',confirmedType:'invoice',issueDate:'2026-01-02',printedSupplier:'ספק Café',documentNumber:'000-07',accountReference:'00 1-A',total:'10.00',currency:'ILS',supplierId:supplier,supplierName:'ספק Café',categoryId:category,categoryName:'Énergie',propertyName:'דירה א׳',paperLocationName:null,referenceScan:true});
 docs.splice(2,0,{id:uuid(2),archive:A,status:'ready',originalFilename:'bill.pdf',confirmedType:'bill',confirmedTitle:null,issueDate:'2026-01-02',printedSupplier:'Water Co',documentNumber:'B-0042',referenceScan:false});
 docs.push({id:uuid(3),archive:A,status:'needs_review',originalFilename:'awaiting.pdf',referenceScan:false});
 return docs;
}
const archiveA={id:A,name:'Archive',kind:'personal',role:'viewer',jurisdiction:null,canBulkExport:false};
try{
 for(const locale of ['en','fr','he']){
  const w=words[locale];const log=[];const docs=libraryDocs();
  const page=await browser.newPage({ignoreHTTPSErrors:true});await page.context().addCookies([{name:'invoyis_locale',value:locale,domain:host,path:'/'}]);
  await mockApi(page,{archives:[archiveA],docs,log});
  await page.goto(`${base}/archives/${A}`);await page.getByRole('heading',{name:'Archive',level:1}).waitFor();assert.equal(await page.locator('html').getAttribute('dir'),locale==='he'?'rtl':'ltr');
  // The undated document awaiting review is listed first even though it would sort after 47 dated rows.
  await page.locator(`#row-${uuid(3)}`).waitFor();assert.equal(await page.locator('ul li button[id^=row]').first().getAttribute('id'),`row-${uuid(3)}`);
  // Archive-wide counts come from the counts endpoint, not the loaded rows.
  await page.locator('aside').getByText('48',{exact:true}).waitFor();
  // Cursor pagination stays available.
  assert.equal(await page.locator('ul li button[id^=row]').count(),21);
  await page.getByRole('button',{name:w.more}).click();await page.locator(`#row-${uuid(100+37)}`).waitFor();assert.equal(await page.locator(`#row-${uuid(100+38)}`).count(),0);assert(log.some(l=>l.includes('status=ready')&&l.includes('cursor=20')));
  await page.getByRole('button',{name:w.more}).click();await page.locator(`#row-${uuid(100+44)}`).waitFor();assert.equal(await page.getByRole('button',{name:w.more}).count(),0);
  // Bills keep their type label, and searching by the translated type name finds them across the archive.
  assert.match(await page.locator(`#row-${uuid(2)}`).innerText(),new RegExp(w.bill));
  await page.locator('input[type=search]').fill(w.bill.toLowerCase());await page.waitForFunction(id=>document.querySelectorAll('ul li button[id^=row]').length===1&&document.getElementById(`row-${id}`),uuid(2));
  assert(log.some(l=>l.includes('types=')&&l.includes('bill')));
  await page.locator(`#row-${uuid(2)}`).click();await page.getByText('B-0042').waitFor();assert.match(await page.locator('dl').innerText(),new RegExp(w.invoiceId));
  await page.keyboard.press('Escape');await page.locator('input[type=search]').fill('');
  // Server-side filters.
  await page.getByRole('button',{name:w.filter}).click();await page.getByRole('dialog').getByRole('button',{name:new RegExp(w.category)}).click();await page.getByRole('option',{name:'Énergie'}).click();await page.waitForTimeout(150);
  assert(log.some(l=>l.includes(`categoryId=${category}`)&&!l.includes('cursor=')));await page.keyboard.press('Escape');
  await page.locator(`#row-${uuid(1)}`).click();await page.getByText('000-07').waitFor();
  assert.equal(await page.getByRole('link',{name:w.edit}).count(),1);assert.equal(await page.locator(`a[href="/api/v1/archives/${A}/documents/${uuid(1)}/original"]`).count(),1);
  await page.keyboard.press('Tab');assert(await page.evaluate(()=>document.activeElement?.tagName!=='BODY'));
  const download=page.waitForEvent('download');await page.locator(`a[href="/api/v1/archives/${A}/documents/${uuid(1)}/original"]`).click();assert.equal((await download).suggestedFilename(),'synthetic.pdf');
  await page.getByRole('link',{name:'ספק Café'}).click();await page.getByRole('heading',{name:'ספק Café',level:1}).waitFor();await page.getByText(w.supplierHistory).first().waitFor();await page.locator(`#row-${uuid(1)}`).click();await page.getByText('00 1-A').waitFor();
  await page.goBack();await page.goBack();await page.locator(`#row-${uuid(1)}`).waitFor();
  await page.getByRole('link',{name:w.edit}).click();await page.waitForURL(`**/documents/${uuid(1)}/review`);await page.close();
  console.log(`${locale}: attention first, archive-wide counts, More documents, bill labels and type search, filters, supplier history, links, RTL and keyboard passed`);
 }

 // Switch archive → Add documents → upload reaches that archive → Needs review → Review → back to the same list position.
 {const log=[];const docs=libraryDocs();for(let i=0;i<30;i++)docs.push({id:uuid(400+i),archive:B,status:'ready',originalFilename:`b-${i}.pdf`,confirmedType:'invoice',confirmedTitle:`Business ${String(i).padStart(2,'0')}`,issueDate:'2026-02-01',referenceScan:false});
  const archives=[{...archiveA,role:'admin'},{id:B,name:'Studio',kind:'business',role:'admin',jurisdiction:'IL',canBulkExport:false,uploadMaxBytes:26214400}];
  const page=await browser.newPage({ignoreHTTPSErrors:true,viewport:{width:1280,height:720}});await page.context().addCookies([{name:'invoyis_locale',value:'en',domain:host,path:'/'}]);
  await mockApi(page,{archives,docs,log});const stored=[];await page.route('**/mock-storage/**',r=>{stored.push(new URL(r.request().url()).pathname);return r.fulfill({status:200,body:''})});
  await page.goto(`${base}/archives/${A}`);
  // New archive opens as a dialog over the library; a Business archive needs its country.
  await page.locator('aside').getByRole('button',{name:'New archive'}).click();const create=page.getByRole('dialog',{name:'New archive'});await create.waitFor();
  await create.getByLabel('Business').check({force:true});await create.getByRole('textbox',{name:/^Name/}).fill('Second');await create.getByRole('button',{name:'Create archive'}).click();
  await create.getByText('Enter a two-letter country code.').waitFor();await create.getByRole('button',{name:'Cancel'}).click();await create.waitFor({state:'detached'});
  await page.locator('aside').getByRole('link',{name:/Studio/}).click();await page.waitForURL(`**/archives/${B}`);await page.locator(`#row-${uuid(400)}`).waitFor();
  await page.getByRole('button',{name:'Add documents'}).first().click();const dialog=page.getByRole('dialog');await dialog.getByText('Adding to').waitFor();await dialog.getByText('Studio',{exact:true}).waitFor();
  await dialog.locator('input[type=file]').setInputFiles({name:'new-invoice.pdf',mimeType:'application/pdf',buffer:Buffer.from('%PDF-1.4\n% new\n%%EOF')});await dialog.getByText('new-invoice.pdf').waitFor();
  await dialog.getByRole('button',{name:'Upload to Studio'}).click();await dialog.waitFor({state:'detached'});
  await page.getByText('Processing',{exact:true}).first().waitFor({timeout:10000});
  assert(log.some(l=>l.startsWith(`POST /api/v1/archives/${B}/upload-sessions`)),'upload session in the chosen archive');assert.deepEqual(stored,[`/mock-storage/${B}/new-invoice.pdf`]);
  const uploaded=docs.find(d=>d.archive===B&&d.status!=='ready').id;
  await page.locator('aside').getByRole('button',{name:/Needs review/}).click();await page.locator(`#row-${uploaded}`).waitFor({timeout:10000});
  assert.match(await page.locator(`#row-${uploaded}`).innerText(),/Needs review/);
  // Back to All documents, scroll down after loading a second page, then open the uploaded document's review and return.
  await page.locator('aside').getByRole('button',{name:/All documents/}).click();await page.getByRole('button',{name:'More documents'}).click();await page.locator(`#row-${uuid(429)}`).waitFor();
  await page.locator(`#row-${uuid(425)}`).scrollIntoViewIfNeeded();await page.locator(`#row-${uuid(425)}`).click();await page.getByRole('link',{name:'Edit details'}).waitFor();
  const before=await page.evaluate(()=>document.querySelector('section[aria-busy]')?.scrollTop??0);assert(before>0,'list scrolled');
  await page.getByRole('link',{name:'Edit details'}).click();await page.waitForURL(`**/documents/${uuid(425)}/review`);
  await page.locator('header a',{hasText:'Back'}).click();await page.waitForURL(`**/archives/${B}**`);
  await page.locator(`#row-${uuid(429)}`).waitFor();await page.waitForFunction(id=>document.activeElement?.id===`row-${id}`,uuid(425));
  // The list scrolls back so the document you opened is in view again (the exact offset can clamp when the upload card is gone).
  const after=await page.evaluate(id=>{const pane=document.querySelector('section[aria-busy]');const row=document.getElementById(`row-${id}`).getBoundingClientRect();const box=pane.getBoundingClientRect();return {scroll:pane.scrollTop,visible:row.top>=box.top&&row.bottom<=box.bottom};},uuid(425));
  assert(after.scroll>0&&after.visible,`list position restored (${before} → ${after.scroll}, row visible: ${after.visible})`);
  await page.close();console.log('new archive dialog, switch archive, upload to chosen archive, needs review, review and return to the same list position passed');}

 // The current archive's upload queue does not depend on the archive list request.
 {const docs=libraryDocs();const page=await browser.newPage({ignoreHTTPSErrors:true});await page.context().addCookies([{name:'invoyis_locale',value:'en',domain:host,path:'/'}]);const log=[];
  await mockApi(page,{archives:[{...archiveA,role:'contributor',uploadMaxBytes:26214400}],docs,listArchives:false,log});await page.route('**/mock-storage/**',r=>r.fulfill({status:200,body:''}));
  await page.goto(`${base}/archives/${A}`);await page.locator(`#row-${uuid(3)}`).waitFor();
  await page.getByRole('button',{name:'Add documents'}).first().click();const dialog=page.getByRole('dialog');
  await dialog.locator('input[type=file]').setInputFiles({name:'offline-list.pdf',mimeType:'application/pdf',buffer:Buffer.from('%PDF-1.4\n%%EOF')});
  await dialog.getByRole('button',{name:'Upload to Archive'}).click();await dialog.waitFor({state:'detached'});
  await page.waitForFunction(()=>true);await page.getByText('Processing',{exact:true}).first().waitFor({timeout:10000});assert(log.some(l=>l.startsWith(`POST /api/v1/archives/${A}/upload-sessions`)));
  await page.close();console.log('upload without the archive list passed');}
}finally{await browser.close()}
