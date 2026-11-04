import test from 'node:test';
import assert from 'node:assert/strict';
import { eligible, correlate, recoveryAction } from '../src/rules.ts';
const rules = { autoCapture:true,captureMinBytes:20*1024*1024,captureExtensions:['zip','dmg'],excludedHosts:['blocked.example'] };
const item = { url:'https://example.com/file',filename:'file.bin',totalBytes:30*1024*1024,danger:'safe',state:'in_progress',incognito:false };
test('size, extension, manual capture and exclusions',()=>{
  assert.ok(eligible(item,rules));assert.ok(eligible({...item,totalBytes:-1,filename:'install.dmg'},rules));
  assert.ok(!eligible({...item,totalBytes:1},rules));assert.ok(eligible({...item,totalBytes:1},rules,true));
  assert.ok(!eligible({...item,url:'https://a.blocked.example/file'},rules,true));
  for(const danger of ['url','uncommon','accepted','sensitiveContentBlock'])assert.ok(!eligible({...item,danger},rules));
  for(const url of ['blob:https://example.com/id','file:///tmp/file','https://user:pass@example.com/file'])assert.ok(!eligible({...item,url},rules,true));
  assert.ok(!eligible({...item,incognito:true},rules));
});
test('only unambiguous observed GET requests correlate',()=>{
  const time=Date.now();const download={url:item.url,startTime:new Date(time).toISOString()};
  const record={requestId:'1',urls:[item.url],method:'GET',originalMethod:'GET',time,headers:{}};
  assert.equal(correlate(download,[record]),record);
  assert.equal(correlate(download,[record,{...record,requestId:'2'}]),undefined);
  assert.equal(correlate(download,[{...record,originalMethod:'POST'}]),undefined);
  assert.equal(correlate(download,[{...record,time:time-10000}]),undefined);
});
test('recover every handoff interruption without double starting',()=>{
  assert.equal(recoveryAction('paused','in_progress','prepared'),'abort');
  assert.equal(recoveryAction('prepared','in_progress','prepared'),'abort');
  assert.equal(recoveryAction('cancelling','in_progress','prepared'),'abort');
  assert.equal(recoveryAction('cancelling','interrupted','prepared'),'commit');
  assert.equal(recoveryAction('cancelled','interrupted','prepared'),'commit');
  assert.equal(recoveryAction('cancelled','interrupted','committed'),'done');
  assert.equal(recoveryAction('prepared','complete','prepared'),'abort');
  assert.equal(recoveryAction('cancelled','interrupted','missing'),'wait');
  assert.equal(recoveryAction('paused','in_progress','missing'),'abort');
});
