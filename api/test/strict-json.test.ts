import assert from 'node:assert/strict';
import test from 'node:test';
import { parseStrictJson } from '../src/strict-json.js';

test('strict JSON uses null-prototype objects including __proto__ keys', () => {
  const parsed = parseStrictJson('{"__proto__":{"polluted":true},"nested":{"constructor":"safe"}}') as Record<string, unknown>;
  assert.equal(Object.getPrototypeOf(parsed), null);
  assert.equal(Object.getPrototypeOf(parsed.__proto__ as object), null);
  assert.deepEqual(Object.keys(parsed), ['__proto__', 'nested']);
  assert.equal(({} as { polluted?: boolean }).polluted, undefined);
});

test('strict JSON accepts decimal-equivalent renderings and rejects lossy numbers', () => {
  assert.deepEqual(parseStrictJson('[0.1,1.2300,1e3,9007199254740992,-0]'), [0.1, 1.23, 1000, 9007199254740992, -0]);
  for (const lexeme of ['9007199254740993', '1000000000000000100', '1.0000000000000001', '1e-400', '1e400', '1e9999999']) {
    assert.throws(() => parseStrictJson(lexeme), /number/);
  }
});
