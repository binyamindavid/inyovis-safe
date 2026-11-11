import {test} from 'node:test';
import {strict as assert} from 'node:assert';
import {parseReviewDecimal,warningCodes,sumReviewDecimals,applicableAcknowledgements} from './review-decimal.mjs';
import {readFileSync} from 'node:fs';
const vectors = JSON.parse(readFileSync(new URL('../../../packages/contracts/review-decimal-vectors.json',import.meta.url),'utf8'));
test('shared locale decimal vectors',()=>{ for(const vector of vectors) assert.equal(parseReviewDecimal(vector.input,vector.locale) ?? null,vector.canonical,vector.name); });
test('French grouping and exact decimals',()=>{
 assert.equal(parseReviewDecimal('1 234,56','fr'),'1234.56');
 assert.equal(parseReviewDecimal('1\u202f234,56','fr'),'1234.56');
 assert.equal(parseReviewDecimal('12 34,56','fr'),undefined);
 assert.equal(parseReviewDecimal('1,234.56','fr'),undefined);
 assert.equal(parseReviewDecimal('1,234.56','en'),'1234.56');
 assert.equal(parseReviewDecimal('1,234.56','he'),'1234.56');
 assert.equal(parseReviewDecimal('12,34.56','en'),undefined);
 assert.equal(parseReviewDecimal('1e3','en'),undefined);
});
test('warnings use exact arithmetic',()=>{
 assert.deepEqual(warningCodes({subtotal:'0.1',tax:'0.2',total:'0.3',currency:'ILS',taxLines:[]}),[]);
});

test('tax lines sum exactly',()=>{assert.equal(sumReviewDecimals(['0.1','0.2','-0.01']),'0.29');});
test('acknowledge then correct leaves no stale warning on save',()=>{
 const before={subtotal:'9.00',tax:'1.00',total:'11.00',currency:'ILS',taxLines:[]};
 const acknowledged=['TOTAL_MISMATCH'];
 assert.deepEqual(warningCodes(before),acknowledged);
 const corrected={...before,total:'10.00'};
 assert.deepEqual(warningCodes(corrected),[]);
 assert.deepEqual(applicableAcknowledgements(acknowledged,warningCodes(corrected)),[]);
});
