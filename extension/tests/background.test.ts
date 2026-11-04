import test from 'node:test';
import assert from 'node:assert/strict';
import { buildSync } from 'esbuild';
import { runInNewContext } from 'node:vm';
import { randomUUID } from 'node:crypto';
import { defaults } from '../src/defaults.ts';

const source=buildSync({entryPoints:[new URL('../src/background.ts',import.meta.url).pathname],bundle:true,write:false,format:'iife',platform:'browser'}).outputFiles[0].text;
const copy=(v:any)=>JSON.parse(JSON.stringify(v));
const tick=()=>new Promise<void>(r=>setImmediate(r));
async function until(check:()=>boolean) { for(let n=0;n<200;n++){if(check())return;await tick();}assert.ok(check(),'background did not settle'); }
function event(){const listeners:Function[]=[];return {addListener:(f:Function)=>listeners.push(f),emit:(...args:any[])=>listeners.forEach(f=>f(...args))};}
function harness(failPrepare=false, holdPrepare=false) {
  let releasePrepare!:()=>void;
  const prepared = new Promise<void>(resolve=>{releasePrepare=resolve;});
  const timers=new Map<number,{fn:()=>void;delay:number}>();let timerID=0;
  const setTimer=(fn:()=>void,delay:number)=>{const id=++timerID;timers.set(id,{fn,delay});return id;};
  const fireTimer=(delay:number)=>{for(const [id,timer] of timers)if(timer.delay===delay){timers.delete(id);timer.fn();}};
  const calls:{method:string;params:any}[]=[];const items=new Map<number,any>();const core=new Map<string,string>();
  const session:Record<string,any>={};const local:Record<string,any>={};
  function area(data:Record<string,any>){return {
    get:async(keys:any)=>copy(keys===null?data:Object.fromEntries((Array.isArray(keys)?keys:[keys]).filter(k=>k in data).map(k=>[k,data[k]]))),
    set:async(values:any)=>{Object.assign(data,copy(values));},remove:async(key:string)=>{delete data[key];},
  };}
  const changed=event();
  const chrome:any={
    storage:{session:area(session),local:area(local)},
    runtime:{id:'dslq',getURL:(s:string)=>'chrome-extension://dslq/'+s,onInstalled:event(),onStartup:event(),onMessage:event(),
      sendNativeMessage:async(_:string,message:any)=>{
        const {method,params}=message;calls.push({method,params:copy(params)});
        let result:any={};
        if(method==='browser.settings')result={...defaults,autoCapture:true};
        if(method==='browser.prepare'){
          if(holdPrepare)await prepared;
          if(failPrepare)return {protocol:1,error:{message:'下载授权已失效'}};
          core.set(params.handoffId,'prepared');
        }
        if(method==='browser.commit')core.set(params.handoffId,'committed');
        if(method==='browser.abort')core.set(params.handoffId,'aborted');
        if(method==='browser.status')result={state:core.get(params.handoffId)??'missing'};
        return {protocol:1,result};
      }},
    action:{setBadgeText:async()=>{},setBadgeBackgroundColor:async()=>{}},
    alarms:{create:async()=>{},onAlarm:event()},permissions:{contains:async()=>true,request:async()=>true},
    contextMenus:{onClicked:event(),removeAll:(f:Function)=>f(),create:()=>{}},
    webRequest:{onBeforeRequest:event(),onSendHeaders:event(),onHeadersReceived:event(),onCompleted:event(),onErrorOccurred:event()},
    downloads:{onCreated:event(),onChanged:changed,onDeterminingFilename:event(),onErased:event(),
      search:async(q:any)=>copy([...items.values()].filter(i=>q.id!==undefined?i.id===q.id:!q.state||i.state===q.state)),
      pause:async(id:number)=>{items.get(id).paused=true;changed.emit({id,paused:{current:true}});},
      resume:async(id:number)=>{items.get(id).paused=false;changed.emit({id,paused:{current:false}});},
      cancel:async(id:number)=>{items.get(id).state='interrupted';changed.emit({id,state:{current:'interrupted'}});},
    },
  };
  runInNewContext(source,{chrome,crypto:{randomUUID},URL,Date,setTimeout:setTimer,clearTimeout:(id:number)=>timers.delete(id),console});
  const item=(id=1)=>({id,url:'https://panel.test/download?token=secret',finalUrl:'https://panel.test/download?token=secret',filename:'',totalBytes:-1,danger:'safe',state:'in_progress',incognito:false,paused:false});
  function request(requestId:string,method='GET',url=item().url){
    chrome.webRequest.onBeforeRequest.emit({requestId,url,method,timeStamp:1});
    chrome.webRequest.onSendHeaders.emit({requestId,url,requestHeaders:[{name:'Cookie',value:'session=private'},{name:'Referer',value:'https://panel.test/files'}]});
  }
  function response(requestId:string,url=item().url){chrome.webRequest.onHeadersReceived.emit({requestId,url,statusCode:200,responseHeaders:[{name:'Content-Disposition',value:'attachment; filename="archive.TAR.GZ"'}]});}
  return {chrome,session,local,calls,items,item,request,response,core,releasePrepare,fireTimer};
}

test('late response metadata rechecks a waiting panel download and commits exactly once',async()=>{
  const h=harness();await until(()=>!!h.session.settings);
  h.request('slow');const item=h.item();h.items.set(1,item);h.chrome.downloads.onCreated.emit(item);
  await until(()=>h.session.decisions?.[0]?.code==='rule_mismatch');
  h.response('slow');
  await until(()=>h.session.decisions?.[0]?.code==='captured');
  for(let i=0;i<5;i++)h.chrome.downloads.onChanged.emit({id:1,filename:{current:'archive.TAR.GZ'}});
  await tick();await tick();
  const prepare=h.calls.filter(c=>c.method==='browser.prepare');assert.equal(prepare.length,1);
  assert.equal(prepare[0].params.filename,'archive.TAR.GZ');assert.equal(prepare[0].params.headers.Cookie,'session=private');
  assert.equal(h.calls.filter(c=>c.method==='browser.commit').length,1);assert.equal(item.state,'interrupted');
  assert.ok(!JSON.stringify(h.session.decisions).includes('secret'));assert.ok(!JSON.stringify(h.session.decisions).includes('private'));
});
test('failed authentication resumes Chrome without retrying the same download',async()=>{
  const h=harness(true);await until(()=>!!h.session.settings);h.request('auth');h.response('auth');
  const item={...h.item(),filename:'temporary-uuid'};h.items.set(1,item);h.chrome.downloads.onCreated.emit(item);
  await until(()=>h.session.decisions?.[0]?.code==='auth_failed');
  await tick();assert.equal(item.state,'in_progress');assert.equal(item.paused,false);
  assert.equal(h.calls.filter(c=>c.method==='browser.prepare').length,1);assert.equal(h.calls.filter(c=>c.method==='browser.commit').length,0);
  assert.equal(Object.keys(h.local).length,0);
});
test('same URL GET/POST ambiguity never cancels Chrome',async()=>{
  const h=harness();await until(()=>!!h.session.settings);h.request('get');h.response('get');h.request('post','POST');h.response('post');
  const item={...h.item(),filename:'archive.tar.gz'};h.items.set(1,item);h.chrome.downloads.onCreated.emit(item);
  await until(()=>h.session.decisions?.[0]?.code==='request_ambiguous');
  assert.equal(item.paused,false);assert.equal(h.calls.filter(c=>c.method==='browser.prepare').length,0);
});
test('cross-origin redirects cannot replay headers from the previous origin',async()=>{
  const h=harness();await until(()=>!!h.session.settings);h.request('redirect');
  const end='https://cdn.test/archive.tar.gz';
  h.chrome.webRequest.onBeforeRequest.emit({requestId:'redirect',url:end,method:'GET',timeStamp:2});h.response('redirect',end);
  const item={...h.item(),finalUrl:end,filename:'archive.tar.gz'};h.items.set(1,item);h.chrome.downloads.onCreated.emit(item);
  await until(()=>h.session.decisions?.[0]?.code==='captured');
  assert.deepEqual(h.calls.find(c=>c.method==='browser.prepare')!.params.headers,{});
});


test('slow prepare holds Save As until Chrome is cancelled, then releases exactly once',async()=>{
  const h=harness(false,true);await until(()=>!!h.session.settings);h.request('slow');h.response('slow');
  const item=h.item();h.items.set(1,item);const releases:string[]=[];
  h.chrome.downloads.onCreated.emit(item);
  h.chrome.downloads.onDeterminingFilename.emit({...item,filename:'archive.TAR.GZ'},()=>releases.push(item.state));
  await until(()=>h.calls.some(c=>c.method==='browser.prepare'));
  h.fireTimer(750);h.fireTimer(1500);await tick();assert.deepEqual(releases,[]);
  h.releasePrepare();await until(()=>h.session.decisions?.[0]?.code==='captured');
  h.fireTimer(13000);await tick();assert.deepEqual(releases,['interrupted']);
  assert.equal(h.calls.filter(c=>c.method==='browser.commit').length,1);
});
test('filename callback waits for response events arriving after the first evaluation',async()=>{
  const h=harness();await until(()=>!!h.session.settings);h.request('late');
  const item=h.item();h.items.set(1,item);const releases:string[]=[];
  h.chrome.downloads.onDeterminingFilename.emit(item,()=>releases.push(item.state));
  await until(()=>h.session.decisions?.[0]?.code==='rule_mismatch');assert.deepEqual(releases,[]);
  h.response('late');await until(()=>h.session.decisions?.[0]?.code==='captured');
  assert.deepEqual(releases,['interrupted']);
});
test('failed preparation restores Chrome before releasing its Save As callback',async()=>{
  const h=harness(true);await until(()=>!!h.session.settings);h.request('auth');h.response('auth');
  const item=h.item();h.items.set(1,item);const releases:any[]=[];
  h.chrome.downloads.onDeterminingFilename.emit(item,()=>releases.push({state:item.state,paused:item.paused}));
  await until(()=>h.session.decisions?.[0]?.code==='auth_failed');await tick();
  assert.deepEqual(releases,[{state:'in_progress',paused:false}]);
});
test('deadline releases Chrome and aborts late prepare instead of opening a second confirmation',async()=>{
  const h=harness(false,true);await until(()=>!!h.session.settings);h.request('timeout');h.response('timeout');
  const item=h.item();h.items.set(1,item);let releases=0;
  h.chrome.downloads.onDeterminingFilename.emit(item,()=>releases++);
  await until(()=>h.calls.some(c=>c.method==='browser.prepare'));
  h.fireTimer(13000);await until(()=>!item.paused);
  assert.equal(releases,1);assert.equal(item.state,'in_progress');
  h.releasePrepare();await until(()=>h.calls.some(c=>c.method==='browser.abort'));
  await until(()=>h.session.decisions?.[0]?.code==='prepare_timeout');
  assert.equal(h.calls.filter(c=>c.method==='browser.commit').length,0);assert.equal(releases,1);
  assert.equal(item.state,'in_progress');assert.equal(Object.keys(h.local).length,0);
});
test('a definitive non-GET request keeps Chrome Save As and never starts native preparation',async()=>{
  const h=harness();await until(()=>!!h.session.settings);h.request('post','POST');h.response('post');
  const item={...h.item(),filename:'archive.tar.gz'};h.items.set(1,item);let releases=0;
  h.chrome.downloads.onDeterminingFilename.emit(item,()=>releases++);
  await until(()=>releases===1);
  assert.equal(item.state,'in_progress');assert.equal(item.paused,false);
  assert.equal(h.calls.filter(c=>c.method==='browser.prepare').length,0);
});
test('missing metadata releases the callback after grace and cannot trigger a late capture',async()=>{
  const h=harness();await until(()=>!!h.session.settings);h.request('lost');
  const item=h.item();h.items.set(1,item);let releases=0;
  h.chrome.downloads.onDeterminingFilename.emit(item,()=>releases++);
  await until(()=>h.session.decisions?.[0]?.code==='rule_mismatch');
  h.fireTimer(750);await until(()=>releases===1);
  h.response('lost');await tick();await tick();
  assert.equal(h.calls.filter(c=>c.method==='browser.prepare').length,0);assert.equal(item.state,'in_progress');
});
