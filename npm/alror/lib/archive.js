'use strict';
// Minimal readers for the two archive formats GoReleaser produces
// (tar.gz and zip), using only Node built-ins. They return regular files as
// a Map of entry path -> Buffer. Good enough for release archives of a few
// MB; not a general-purpose extractor.

const zlib = require('zlib');

/** @returns {Map<string, Buffer>} */
function readTarGz(buf) {
  return readTar(zlib.gunzipSync(buf));
}

/** @returns {Map<string, Buffer>} */
function readTar(tar) {
  const files = new Map();
  let off = 0;
  let longName = null;
  let paxPath = null;
  while (off + 512 <= tar.length) {
    const header = tar.subarray(off, off + 512);
    if (header.every((b) => b === 0)) break; // end-of-archive marker
    const field = (start, len) => {
      const raw = header.subarray(start, start + len);
      const nul = raw.indexOf(0);
      return raw.subarray(0, nul === -1 ? len : nul).toString('utf8');
    };
    let name = field(0, 100);
    const sizeField = header.subarray(124, 136);
    let size;
    if (sizeField[0] & 0x80) {
      // GNU base-256 encoding for large files
      size = 0;
      for (let i = 1; i < 12; i++) size = size * 256 + sizeField[i];
    } else {
      size = parseInt(field(124, 12).trim() || '0', 8);
    }
    const type = String.fromCharCode(header[156] || 48); // '\0' means a regular file
    const magic = field(257, 6);
    if (magic.startsWith('ustar')) {
      const prefix = field(345, 155);
      if (prefix) name = `${prefix}/${name}`;
    }
    const dataStart = off + 512;
    const data = tar.subarray(dataStart, dataStart + size);
    off = dataStart + Math.ceil(size / 512) * 512;

    if (type === 'L') {
      longName = data.toString('utf8').replace(/\0+$/, '');
      continue;
    }
    if (type === 'x') {
      paxPath = parsePax(data).path || null;
      continue;
    }
    if (type === 'g') continue;
    if (longName) name = longName;
    if (paxPath) name = paxPath;
    longName = null;
    paxPath = null;
    if (type === '0' || type === '7') files.set(normalize(name), Buffer.from(data));
  }
  return files;
}

function parsePax(buf) {
  const out = {};
  let off = 0;
  const s = buf.toString('utf8');
  while (off < s.length) {
    const sp = s.indexOf(' ', off);
    if (sp === -1) break;
    const len = parseInt(s.slice(off, sp), 10);
    if (!len) break;
    const rec = s.slice(sp + 1, off + len - 1);
    const eq = rec.indexOf('=');
    if (eq !== -1) out[rec.slice(0, eq)] = rec.slice(eq + 1);
    off += len;
  }
  return out;
}

/** @returns {Map<string, Buffer>} */
function readZip(buf) {
  const files = new Map();
  // Find the end-of-central-directory record (it sits in the last 64 KiB + 22 bytes).
  let eocd = -1;
  for (let i = buf.length - 22; i >= Math.max(0, buf.length - 22 - 0xffff); i--) {
    if (buf.readUInt32LE(i) === 0x06054b50) {
      eocd = i;
      break;
    }
  }
  if (eocd === -1) throw new Error('zip: end of central directory not found');
  const count = buf.readUInt16LE(eocd + 10);
  let p = buf.readUInt32LE(eocd + 16);
  for (let n = 0; n < count; n++) {
    if (buf.readUInt32LE(p) !== 0x02014b50) throw new Error('zip: bad central directory entry');
    const method = buf.readUInt16LE(p + 10);
    const compSize = buf.readUInt32LE(p + 20);
    const nameLen = buf.readUInt16LE(p + 28);
    const extraLen = buf.readUInt16LE(p + 30);
    const commentLen = buf.readUInt16LE(p + 32);
    const localOff = buf.readUInt32LE(p + 42);
    const name = buf.subarray(p + 46, p + 46 + nameLen).toString('utf8');
    p += 46 + nameLen + extraLen + commentLen;
    if (name.endsWith('/')) continue; // directory
    if (buf.readUInt32LE(localOff) !== 0x04034b50) throw new Error('zip: bad local header');
    const lNameLen = buf.readUInt16LE(localOff + 26);
    const lExtraLen = buf.readUInt16LE(localOff + 28);
    const start = localOff + 30 + lNameLen + lExtraLen;
    const raw = buf.subarray(start, start + compSize);
    let data;
    if (method === 0) data = Buffer.from(raw);
    else if (method === 8) data = zlib.inflateRawSync(raw);
    else throw new Error(`zip: unsupported compression method ${method} for ${name}`);
    files.set(normalize(name), data);
  }
  return files;
}

function normalize(name) {
  return name.replace(/\\/g, '/').replace(/^\.\//, '');
}

/** Read an archive by file name (".zip" or ".tar.gz"/".tgz"). */
function readArchive(fileName, buf) {
  if (/\.zip$/i.test(fileName)) return readZip(buf);
  if (/\.(tar\.gz|tgz)$/i.test(fileName)) return readTarGz(buf);
  throw new Error(`unsupported archive type: ${fileName}`);
}

/** Find an entry by base name anywhere in the archive. */
function findEntry(files, baseName) {
  for (const [name, data] of files) {
    if (name === baseName || name.endsWith(`/${baseName}`)) return data;
  }
  return undefined;
}

module.exports = { readArchive, readTarGz, readTar, readZip, findEntry };
