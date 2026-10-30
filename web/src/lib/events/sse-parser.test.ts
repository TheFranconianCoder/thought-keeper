import { describe, expect, it } from 'vitest';
import { SSEParser } from './sse-parser';

describe('SSEParser', () => {
  it('parses a single data frame', () => {
    const parser = new SSEParser();
    const messages = parser.push('data: {"type":"source"}\n\n');
    expect(messages).toEqual([{ data: '{"type":"source"}', event: undefined, id: undefined, retry: undefined }]);
  });

  it('keeps a frame split across chunks', () => {
    const parser = new SSEParser();
    expect(parser.push('data: {"type":"sou')).toEqual([]);
    const messages = parser.push('rce"}\n\n');
    expect(messages).toHaveLength(1);
    expect(messages[0].data).toBe('{"type":"source"}');
  });

  it('joins multi-line data and reports comments as heartbeats', () => {
    const parser = new SSEParser();
    const messages = parser.push(': ping\n\ndata: one\ndata: two\n\n');
    expect(messages[0].comment).toBe('ping');
    expect(messages[1].data).toBe('one\ntwo');
  });

  it('accepts CRLF line endings', () => {
    const parser = new SSEParser();
    const messages = parser.push('event: run\r\ndata: {}\r\n\r\n');
    expect(messages[0].event).toBe('run');
    expect(messages[0].data).toBe('{}');
  });

  it('does not dispatch an event without data', () => {
    const parser = new SSEParser();
    expect(parser.push('event: run\n\n')).toEqual([]);
  });

  it('reads retry and id fields', () => {
    const parser = new SSEParser();
    const messages = parser.push('id: 42\nretry: 1500\ndata: x\n\n');
    expect(messages[0]).toMatchObject({ id: '42', retry: 1500, data: 'x' });
  });
});
