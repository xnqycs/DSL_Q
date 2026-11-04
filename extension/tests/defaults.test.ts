import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { commonCaptureExtensions, normalizeExtensions, defaults } from '../src/defaults.ts';
import { eligible } from '../src/rules.ts';

test('Go, Swift and extension ship the same complete preset',()=>{
  const go=readFileSync(new URL('../../internal/engine/capture.go',import.meta.url),'utf8').match(/const captureExtensions = `([^`]+)`/)![1].trim().split(/\s+/);
  const swift=readFileSync(new URL('../../macos/Sources/DSLQ/Models.swift',import.meta.url),'utf8').match(/commonCaptureExtensions = """([\s\S]*?)"""/)![1].trim().split(/\s+/);
  assert.deepEqual(commonCaptureExtensions,go);assert.deepEqual(swift,go);
  assert.equal(new Set(go).size,go.length);
  assert.deepEqual(normalizeExtensions([' .ZIP，.tar.GZ; PDF\nzip', '； .png','']),['zip','tar.gz','pdf','png']);
  const item={url:'https://example.test/download',totalBytes:1024,danger:'safe',state:'in_progress',incognito:false};
  for(const ext of commonCaptureExtensions) assert.ok(eligible({...item,filename:`file.${ext}`},{...defaults,autoCapture:true}),ext);
  assert.ok(!eligible({...item,filename:'file.unlisted'},{...defaults,autoCapture:true}));
  assert.ok(eligible({...item,filename:'file.unlisted',totalBytes:20*1024*1024},{...defaults,autoCapture:true}));
});
