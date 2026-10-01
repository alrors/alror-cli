import assert from 'node:assert/strict';
import { describe, test } from 'node:test';

import { SSEParser, parseSSE } from '@alror/sdk';

const collect = (chunks) => {
  const out = [];
  const p = new SSEParser((m) => out.push(m));
  for (const c of chunks) p.push(c);
  p.end();
  return out;
};

describe('SSE parser', () => {
  test('event, data and id fields', () => {
    const msgs = parseSSE('id: 7\nevent: deployment.updated\ndata: {"a":1}\n\n');
    assert.deepEqual(msgs, [{ event: 'deployment.updated', data: '{"a":1}', id: '7' }]);
  });

  test('default event type is "message"; multi-line data is joined with \\n', () => {
    assert.deepEqual(parseSSE('data: line1\ndata: line2\ndata\n\n'), [{ event: 'message', data: 'line1\nline2\n', id: '' }]);
  });

  test('CRLF, CR and LF line endings', () => {
    const expected = [
      { event: 'a', data: '1', id: '' },
      { event: 'b', data: '2', id: '' },
    ];
    assert.deepEqual(parseSSE('event: a\r\ndata: 1\r\n\r\nevent: b\r\ndata: 2\r\n\r\n'), expected);
    assert.deepEqual(parseSSE('event: a\rdata: 1\r\revent: b\rdata: 2\r\r'), expected);
  });

  test('handles every possible chunk split, including CR|LF across chunks', () => {
    const text = '﻿: hi\r\nid: 1\r\nevent: x\r\ndata: {"k":"v"}\r\n\r\ndata:no-space\r\n\r\n';
    const whole = collect([text]);
    assert.deepEqual(whole, [
      { event: 'x', data: '{"k":"v"}', id: '1' },
      { event: 'message', data: 'no-space', id: '1' },
    ]);
    for (let i = 1; i < text.length; i++) {
      for (let j = i; j < text.length; j += 7) {
        assert.deepEqual(collect([text.slice(0, i), text.slice(i, j), text.slice(j)]), whole, `split at ${i},${j}`);
      }
    }
    // One character at a time.
    assert.deepEqual(collect([...text]), whole);
  });

  test('comments and unknown fields are ignored; events without data are not dispatched', () => {
    assert.deepEqual(parseSSE(': keep-alive\n\nfoo: bar\nevent: nothing\n\ndata: x\n\n'), [{ event: 'message', data: 'x', id: '' }]);
  });

  test('id is sticky and ids containing NUL are ignored', () => {
    const msgs = parseSSE('id: 5\ndata: a\n\ndata: b\n\nid: bad\0id\ndata: c\n\nid\ndata: d\n\n');
    assert.deepEqual(msgs.map((m) => m.id), ['5', '5', '5', '']);
  });

  test('retry is reported, only for digit values', () => {
    const out = collect(['retry: 1500\n\nretry: soon\ndata: x\n\n']);
    assert.equal(out[0].retry, 1500);
    assert.equal(out[1].retry, undefined);
    assert.equal(out[1].data, 'x');
  });

  test('an unterminated final message is discarded', () => {
    assert.deepEqual(parseSSE('data: complete\n\ndata: partial'), [{ event: 'message', data: 'complete', id: '' }]);
  });

  test('only the first leading space of a value is stripped', () => {
    assert.deepEqual(parseSSE('data:  two spaces\n\n')[0].data, ' two spaces');
  });
});
