// Incremental parser for text/event-stream, following the WHATWG
// "server-sent events" interpretation rules.

export interface SSEMessage {
  /** The `event:` field, or "message" when absent. */
  event: string;
  /** All `data:` lines joined with "\n". */
  data: string;
  /** The last event id seen on the stream (sticky, as in EventSource). */
  id: string;
  /** A `retry:` value in milliseconds, when this message carried one. */
  retry?: number;
}

export class SSEParser {
  private buf = '';
  private data: string[] = [];
  private event = '';
  private retry: number | undefined;
  private first = true;
  private pendingCR = false;
  /** Last event id, kept across messages and reconnects. */
  lastEventId = '';

  constructor(private readonly onMessage: (m: SSEMessage) => void) {}

  /** Feed a decoded chunk of the stream. */
  push(chunk: string): void {
    if (chunk === '') return;
    if (this.first) {
      if (chunk.charCodeAt(0) === 0xfeff) chunk = chunk.slice(1);
      this.first = false;
    }
    if (this.pendingCR && chunk.startsWith('\n')) chunk = chunk.slice(1); // "\r" + "\n" split across chunks
    this.pendingCR = false;
    this.buf += chunk;
    let start = 0;
    for (let i = 0; i < this.buf.length; i++) {
      const c = this.buf.charCodeAt(i);
      if (c !== 10 && c !== 13) continue;
      const line = this.buf.slice(start, i);
      if (c === 13) {
        if (i + 1 < this.buf.length) {
          if (this.buf.charCodeAt(i + 1) === 10) i++;
        } else {
          this.pendingCR = true;
        }
      }
      start = i + 1;
      this.line(line);
    }
    this.buf = this.buf.slice(start);
  }

  /** Signal end of stream: an unterminated final message is discarded, per spec. */
  end(): void {
    this.buf = '';
    this.data = [];
    this.event = '';
    this.retry = undefined;
  }

  private line(line: string): void {
    if (line === '') {
      this.dispatch();
      return;
    }
    if (line.startsWith(':')) return; // comment / keep-alive
    const colon = line.indexOf(':');
    let field = line;
    let value = '';
    if (colon !== -1) {
      field = line.slice(0, colon);
      value = line.slice(colon + 1);
      if (value.startsWith(' ')) value = value.slice(1);
    }
    switch (field) {
      case 'event':
        this.event = value;
        break;
      case 'data':
        this.data.push(value);
        break;
      case 'id':
        if (!value.includes('\0')) this.lastEventId = value;
        break;
      case 'retry':
        if (/^\d+$/.test(value)) this.retry = Number(value);
        break;
      default:
        break; // unknown fields are ignored
    }
  }

  private dispatch(): void {
    const retry = this.retry;
    this.retry = undefined;
    if (this.data.length === 0) {
      this.event = '';
      if (retry !== undefined) this.onMessage({ event: '', data: '', id: this.lastEventId, retry });
      return;
    }
    const msg: SSEMessage = { event: this.event || 'message', data: this.data.join('\n'), id: this.lastEventId };
    if (retry !== undefined) msg.retry = retry;
    this.data = [];
    this.event = '';
    this.onMessage(msg);
  }
}

/** Parse a complete event-stream text into messages (handy for tests). */
export function parseSSE(text: string): SSEMessage[] {
  const out: SSEMessage[] = [];
  const p = new SSEParser((m) => {
    if (m.event !== '' || m.data !== '') out.push(m);
  });
  p.push(text);
  p.end();
  return out;
}
