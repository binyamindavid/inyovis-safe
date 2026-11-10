import {test} from 'node:test';
import {strict as assert} from 'node:assert';
import {parseReviewDecimal,warningCodes,sumReviewDecimals} from './review-decimal.mjs';
test('French grouping and exact decimals',()=>{
 assert.equal(parseReviewDecimal('1 234,56','fr'),'1234.56');
 assert.equal(parseReviewDecimal('1\u202f234,56','fr'),'1234.56');
 assert.equal(parseReviewDecimal('12 34,56','fr'),undefined);
 assert.equal(parseReviewDecimal('1,234.56','fr'),undefined);
 assert.equal(parseReviewDecimal('1e3','en'),undefined);
});
test('warnings use exact arithmetic',()=>{
 assert.deepEqual(warningCodes({subtotal:'0.1',tax:'0.2',total:'0.3',currency:'ILS',taxLines:[]}),[]);
});

test('tax lines sum exactly',()=>{assert.equal(sumReviewDecimals(['0.1','0.2','-0.01']),'0.29');});
