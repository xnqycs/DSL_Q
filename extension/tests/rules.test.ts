import test from 'node:test';
import assert from 'node:assert/strict';
import { eligible, correlate, matchRequest, responseFilename, decisionFor, recoveryAction } from '../src/rules.ts';
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
  const record={requestId:'1',urls:[item.url],method:'GET',originalMethod:'GET',time,updatedAt:time,responseAt:time,responseUrl:item.url,headers:{}};
  assert.equal(correlate(download,[record]),record);
  assert.equal(correlate(download,[record,{...record,requestId:'2'}]),undefined);
  assert.equal(correlate(download,[{...record,originalMethod:'POST'}]),undefined);
  assert.equal(correlate(download,[{...record,updatedAt:time-100000}]),undefined);
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

test('slow responses, redirects, request ownership and POST ambiguity',()=>{
  const now=Date.now(); const url='https://example.com/download?filename=file.tar.gz'; const end='https://cdn.example.com/file';
  const record={requestId:'slow',urls:[url,end],method:'GET',originalMethod:'GET',time:now-20000,updatedAt:now,responseAt:now,responseUrl:end,headers:{}};
  const download={id:1,url,finalUrl:end};
  assert.equal(correlate(download,[record]),record);
  assert.equal(correlate(download,[{...record,responseUrl:url}]),undefined);
  assert.equal(correlate(download,[{...record,downloadId:2}]),undefined);
  assert.ok(correlate(download,[{...record,downloadId:1}]));
  assert.equal(matchRequest(download,[record,{...record,requestId:'post',originalMethod:'POST'}]).reason,'request_ambiguous');
  assert.equal(matchRequest(download,[{...record,originalMethod:'POST'}]).reason,'request_unsupported');
});
test('response filename parsing and composite suffix matching',()=>{
  assert.equal(responseFilename(`attachment; filename="fallback.tar.gz"; filename*=UTF-8''%E6%B5%8B%E8%AF%95.TAR.GZ`),'测试.TAR.GZ');
  assert.equal(responseFilename('attachment; filename="folder/archive.tar.gz"'),'archive.tar.gz');
  assert.equal(responseFilename(`attachment; filename="fallback.zip"; filename*=UTF-8''%ZZ`),'fallback.zip');
  assert.ok(eligible({...item,filename:'测试.TAR.GZ',totalBytes:-1},{...rules,captureExtensions:['.TAR.GZ']}));
  assert.ok(eligible({...item,filename:'',url:'https://example.com/%E6%B5%8B%E8%AF%95.ZIP?token=x',totalBytes:1},rules));
  assert.ok(!eligible({...item,filename:'file.zip.exe',totalBytes:1},rules));
});
test('diagnostics contain neither query tokens nor authentication headers',()=>{
  const result=decisionFor({id:1,url:'https://user:password@example.com/download?token=secret#fragment',filename:'archive.tar.gz?secret'},'request_missing');
  assert.equal(result.host,'example.com');assert.equal(result.filename,'archive.tar.gz');
  assert.ok(!JSON.stringify(result).includes('secret'));assert.ok(!JSON.stringify(result).includes('password'));
});
