import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import Ajv2020 from 'ajv/dist/2020.js';
import yaml from 'js-yaml';

const spec = yaml.load(readFileSync(new URL('../openapi.yaml', import.meta.url), 'utf8'));
const schemas = spec.components.schemas;
const validateAmount = new Ajv2020().compile(schemas.Money.properties.amount);

test('OpenAPI accepts canonical zero and signed two-place amounts', () => {
  for (const amount of ['0.00', '125.40', '-0.01', '-125.40']) {
    assert.equal(validateAmount(amount), true, `${amount}: ${JSON.stringify(validateAmount.errors)}`);
  }
});

test('OpenAPI rejects negative zero and malformed amounts', () => {
  for (const amount of ['-0.00', '1.2', '01.00', '1e2', 125.40]) {
    assert.equal(validateAmount(amount), false, `${amount} unexpectedly accepted`);
  }
});

test('OpenAPI currency enum is exactly ILS and EUR', () => {
  assert.deepEqual(schemas.Currency.enum, ['ILS', 'EUR']);
});
