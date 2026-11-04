"""Real Chrome integration in a disposable profile and encrypted database."""
import argparse, base64, hashlib, json, os, queue, shlex, shutil, subprocess, tempfile, threading, time, uuid
from pathlib import Path
from playwright.sync_api import sync_playwright
from fixture import start_server

ROOT=Path(__file__).resolve().parents[1]
APP=ROOT/'dist/DSL Q.app'
EXTENSION=APP/'Contents/Resources/extension'
KEY=base64.b64encode(bytes(32)).decode()

class Core:
    def __init__(self, directory):
        self.p=subprocess.Popen([str(APP/'Contents/Helpers/dslq-core'),'--data-dir',str(directory)],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
        self.responses={};self.condition=threading.Condition();self.snapshot=None
        def read():
            for line in self.p.stdout:
                item=json.loads(line)
                with self.condition:
                    if item.get('method')=='snapshot':self.snapshot=item['params']
                    else:self.responses[item.get('id')]=item
                    self.condition.notify_all()
        threading.Thread(target=read,daemon=True).start()
        self.snapshot=self.call('initialize',{'key':KEY})
    def call(self, method, params=None):
        identifier=str(time.time_ns())
        self.p.stdin.write(json.dumps({'jsonrpc':'2.0','protocol':1,'id':identifier,'method':method,'params':params or {}})+'\n');self.p.stdin.flush()
        with self.condition:
            assert self.condition.wait_for(lambda:identifier in self.responses,timeout=30), method+' timed out'
            response=self.responses.pop(identifier)
        assert not response.get('error'),response
        return response.get('result')
    def close(self):
        if self.p.poll() is None:
            self.call('shutdown');self.p.wait(timeout=10)

def wait(fn, timeout=30):
    end=time.time()+timeout
    while time.time()<end:
        value=fn()
        if value:return value
        time.sleep(.1)
    raise AssertionError('condition timed out')

def check_hash(path,size):
    expected=hashlib.sha256();block=bytes(range(251))*4096
    remain=size
    while remain:n=min(remain,len(block));expected.update(block[:n]);remain-=n
    assert path.stat().st_size==size
    assert hashlib.sha256(path.read_bytes()).hexdigest()==expected.hexdigest()

def main():
    parser=argparse.ArgumentParser();parser.add_argument('--browser',default='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome');parser.add_argument('--label',default='chrome');parser.add_argument('--headed',action='store_true');parser.add_argument('--cold-start',action='store_true');parser.add_argument('--trace-handoff',action='store_true');parser.add_argument('--small-only',action='store_true');args=parser.parse_args()
    work=Path(tempfile.mkdtemp(prefix='bc.',dir=ROOT/'.cache'));profile=work/'profile';profile.mkdir();data=work/'data';downloads=work/'downloads'
    # Exercise Chrome's real filename pipeline. Playwright's allowAndName mode
    # forces UUID filenames and bypasses extension filename determination.
    (profile/'Default').mkdir()
    (profile/'Default/Preferences').write_text(json.dumps({'download':{'default_directory':str(work/'browser-downloads'),'prompt_for_download':False}}))
    core=Core(data);server=start_server();base=f'http://127.0.0.1:{server.server_port}'
    settings=core.snapshot['settings'];settings.update(directory=str(downloads),autoCapture=True,connections=8,preventIdleSleepWhileDownloading=True)
    core.call('settings.update',settings)
    wrapper=work/'native-host.sh'
    wrapper.write_text('#!/bin/sh\n'+f'export DSLQ_INTEGRATION_TEST=1\nexport DSLQ_TEST_DATA_DIR={shlex.quote(str(data))}\nexport DSLQ_TEST_KEY={shlex.quote(KEY)}\nexec {shlex.quote(str(APP/"Contents/Helpers/dslq-host"))} "$@"\n');wrapper.chmod(0o700)
    extension_id=(EXTENSION/'extension-id.txt').read_text().strip()
    manifests=profile/'NativeMessagingHosts';manifests.mkdir()
    (manifests/'local.dslq.download.json').write_text(json.dumps({'name':'local.dslq.download','description':'DSL Q isolated integration test','path':str(wrapper),'type':'stdio','allowed_origins':[f'chrome-extension://{extension_id}/']}))
    report={'browser':args.label,'work':str(work),'checks':[]}
    # Test-only manifest grants only the loopback fixture host on installation;
    # production keeps host access optional and asks through its popup.
    runtime_extension=work/'extension';shutil.copytree(EXTENSION,runtime_extension)
    manifest=json.loads((runtime_extension/'manifest.json').read_text())
    manifest['host_permissions']=['http://127.0.0.1/*']
    (runtime_extension/'manifest.json').write_text(json.dumps(manifest))
    try:
        with sync_playwright() as p:
            context=p.chromium.launch_persistent_context(str(profile),executable_path=str(Path(args.browser).resolve()),headless=not args.headed,accept_downloads=True,
                args=['--enable-unsafe-extension-debugging','--no-first-run','--no-default-browser-check'],ignore_default_args=['--disable-extensions'])
            try:
                cdp=context.browser.new_browser_cdp_session()
                cdp.send('Browser.setDownloadBehavior',{'behavior':'default','eventsEnabled':True})
                loaded=cdp.send('Extensions.loadUnpacked',{'path':str(runtime_extension)})
                assert loaded['id']==extension_id,loaded
                print('Loaded extension',loaded,flush=True)
                worker=context.service_workers[0] if context.service_workers else context.wait_for_event('serviceworker')
                if args.trace_handoff:
                    context.on('console',lambda message:print('Browser trace:',message.text,flush=True))
                    worker.evaluate('''() => {
                        for(const method of ['pause','cancel','resume','search']) {
                            const original=chrome.downloads[method].bind(chrome.downloads);
                            chrome.downloads[method]=async (...args)=>{try {const result=await original(...args);console.log('downloads.'+method,JSON.stringify(args),method==='search'?JSON.stringify(result.map(i=>({id:i.id,state:i.state,paused:i.paused,received:i.bytesReceived,total:i.totalBytes}))):'ok');return result;}catch(e){console.log('downloads.'+method,'FAILED',e.message);throw e;}};
                        }
                        const native=chrome.runtime.sendNativeMessage.bind(chrome.runtime);
                        chrome.runtime.sendNativeMessage=async (...args)=>{const result=await native(...args);console.log(args[1].method,result.error?.message??'ok');return result;};
                    }''')
                popup=context.new_page();popup.on('pageerror',lambda e:print('Popup error:',e,flush=True));popup.goto(f'chrome-extension://{extension_id}/popup.html');popup.wait_for_load_state('networkidle')
                service_workers=context.new_cdp_session(popup);versions={};worker_states=[]
                def remember_versions(event):
                    for version in event['versions']:
                        versions[version['versionId']]=version;worker_states.append((version['versionId'],version['runningStatus']))
                service_workers.on('ServiceWorker.workerVersionUpdated',remember_versions)
                service_workers.send('ServiceWorker.enable')
                print('Popup:',popup.locator('body').inner_text(),flush=True)
                popup.screenshot(path=str(work/'popup.png'))
                popup.get_by_role('button',name='打开并连接客户端').click()
                print('Connect result:',popup.locator('body').inner_text(),flush=True)
                popup.get_by_role('heading',name='已连接 DSL Q',exact=True).wait_for(timeout=15000)
                report['checks'].append('native messaging handshake')
                granted=popup.evaluate('() => chrome.permissions.contains({origins:["http://127.0.0.1/*"]})')
                assert granted,'test origin permission not granted'
                report['checks'].append('loopback-only test manifest permission')
                page=context.new_page();page.goto(base);page.wait_for_load_state('networkidle')
                print('Fixture:',page.locator('body').inner_text(),flush=True)
                if args.small_only:
                    page.get_by_role('link',name='Small document',exact=True).click()
                    task=wait(lambda:next((t for t in core.call('snapshot')['tasks'] if t['name']=='small-document.pdf' and t['state']=='completed'),None),timeout=15)
                    check_hash(Path(task['path']),1024)
                    report['checks'].append('1 KiB PDF captured using native Chrome filename determination')
                    return
                page.get_by_role('link',name='Automatic archive').click()
                def completed(name):
                    tasks=core.call('snapshot')['tasks']
                    failed=[t for t in tasks if t['state']=='failed']
                    assert not failed,failed
                    return next((t for t in tasks if t['source'].endswith('/'+name) and t['state']=='completed'),None)
                task=wait(lambda:completed('auto.zip'))
                check_hash(Path(task['path']),24*1024*1024)
                report['checks'].append('automatic capture and SHA-256')
                page.get_by_role('link',name='Cookie file').click();task=wait(lambda:completed('cookie.zip'));check_hash(Path(task['path']),24*1024*1024)
                report['checks'].append('cookie-authenticated capture')
                for label, filename, size in [('Panel archive','panel-archive.TAR.GZ',2*1024*1024),('Small document','small-document.pdf',1024),('Encoded filename','测试.TAR.GZ',2*1024*1024),('Panel redirect','redirected.tar.gz',2*1024*1024)]:
                    before_ids={t['id'] for t in core.call('snapshot')['tasks']}
                    page.get_by_role('link',name=label,exact=True).click()
                    def new_completed():
                        tasks=core.call('snapshot')['tasks']
                        failed=[t for t in tasks if t['id'] not in before_ids and t['state']=='failed'];assert not failed,failed
                        return next((t for t in tasks if t['id'] not in before_ids and t['state']=='completed'),None)
                    task=wait(new_completed,timeout=45)
                    assert task['name']==filename,task
                    check_hash(Path(task['path']),size)
                    assert len([t for t in core.call('snapshot')['tasks'] if t['id'] not in before_ids])==1
                    decision=wait(lambda:next((d for d in popup.evaluate('async()=> (await chrome.storage.session.get("decisions")).decisions') if d['filename']==filename and d['code']=='captured'),None))
                    matching=popup.evaluate('(id)=>chrome.downloads.search({id})',decision['downloadId'])
                    assert len(matching)==1 and matching[0]['state']=='interrupted',matching
                    report['checks'].append(label+': filename, one task, cancelled Chrome download, SHA-256')
                before=len(core.call('snapshot')['tasks'])
                page.get_by_role('link',name='Unreplayable login').click()
                wait(lambda:next((i for i in popup.evaluate('(url)=>chrome.downloads.search({url})',base+'/unreplayable.zip?size=2097152') if i['state']=='complete'),None))
                assert len(core.call('snapshot')['tasks'])==before
                diagnostic=popup.evaluate('async()=> (await chrome.storage.session.get("decisions")).decisions')
                assert any(d['code']=='auth_failed' for d in diagnostic),diagnostic
                assert all('?' not in d['host'] and '?' not in d['filename'] for d in diagnostic)
                report['checks'].append('authentication replay failure restores Chrome and shows sanitized diagnostic')

                before=len(core.call('snapshot')['tasks'])
                def browser_completed(url):
                    items=popup.evaluate('(url) => chrome.downloads.search({url})',url)
                    return next((i for i in items if i['state']=='complete'),None)
                page.get_by_role('link',name='Small file').click()
                small=wait(lambda:browser_completed(base+'/small.unlisted'));check_hash(Path(small['filename']),1024)
                assert len(core.call('snapshot')['tasks'])==before
                report['checks'].append('small file retained in browser')
                page.get_by_role('button',name='POST download').click()
                post=wait(lambda:browser_completed(base+'/post.zip'));check_hash(Path(post['filename']),24*1024*1024)
                assert len(core.call('snapshot')['tasks'])==before
                report['checks'].append('POST retained in browser')
                # Exercise the very same handler used by the native context menu,
                # with only the test fixture host preauthorized.
                popup.get_by_role('textbox',name='文件链接').fill(base+'/manual.bin?size=1048576')
                popup.get_by_role('button',name='下载此链接').click()
                task=wait(lambda:completed('manual.bin'));check_hash(Path(task['path']),1024*1024)
                report['checks'].append('popup manual download below automatic size threshold')
                print('Manual download passed',flush=True)
                settings['excludedHosts']=['127.0.0.1'];core.call('settings.update',settings)
                popup.evaluate('() => chrome.runtime.sendMessage({type:"connect"})')
                before=len(core.call('snapshot')['tasks']);page.get_by_role('link',name='Automatic archive').click()
                excluded=wait(lambda:browser_completed(base+'/auto.zip'));check_hash(Path(excluded['filename']),24*1024*1024)
                assert len(core.call('snapshot')['tasks'])==before
                report['checks'].append('excluded host retained in browser')
                print('Excluded host passed',flush=True)
                settings['excludedHosts']=[];settings['autoCapture']=False;core.call('settings.update',settings)
                popup.evaluate('() => chrome.runtime.sendMessage({type:"connect"})')
                def native(method,params):
                    return popup.evaluate('''async p => {const r=await chrome.runtime.sendNativeMessage('local.dslq.download',{jsonrpc:'2.0',protocol:1,id:crypto.randomUUID(),method:p.method,params:p.params});if(r.error)throw new Error(r.error.message);return r.result;}''',{'method':method,'params':params})
                for filename,label,phase in [('recover.bin','Recovery file','cancelling'),('abort.bin','Abort file','prepared')]:
                    page.get_by_role('link',name=label).click()
                    item=wait(lambda:next(iter(popup.evaluate('(url) => chrome.downloads.search({url})',base+'/'+filename)),None))
                    popup.evaluate('(id)=>chrome.downloads.pause(id)',item['id'])
                    token=str(uuid.uuid4());request={'url':base+'/'+filename,'handoffId':token,'filename':filename}
                    prepared=native('browser.prepare',request);duplicate=native('browser.prepare',request);assert prepared['id']==duplicate['id']
                    popup.evaluate('(p)=>chrome.storage.local.set({["handoff:"+p.id]:p})',{'id':token,'downloadId':item['id'],'phase':phase,'createdAt':int(time.time()*1000)})
                    if phase=='cancelling':popup.evaluate('(id)=>chrome.downloads.cancel(id)',item['id'])
                    current=next(v for v in versions.values() if extension_id in v['scriptURL'] and v['runningStatus']=='running')
                    history_start=len(worker_states)
                    service_workers.send('ServiceWorker.stopWorker',{'versionId':current['versionId']})
                    popup.evaluate('() => chrome.runtime.sendMessage({type:"connect"})')
                    observed=[status for identifier,status in worker_states[history_start:] if identifier==current['versionId']]
                    assert 'stopped' in observed and 'running' in observed,observed
                    print('Worker restarted during',phase,flush=True)
                    if phase=='cancelling':
                        task=wait(lambda:completed(filename));check_hash(Path(task['path']),24*1024*1024)
                        assert native('browser.commit',{'handoffId':token})['state']=='committed'
                        report['checks'].append('worker restart after browser cancellation commits exactly one task')
                    else:
                        restored=wait(lambda:browser_completed(base+'/'+filename));check_hash(Path(restored['filename']),24*1024*1024)
                        assert native('browser.status',{'handoffId':token})['state']=='aborted'
                        report['checks'].append('worker restart before cancellation restores browser download')
                    assert 'handoff:'+token not in popup.evaluate('()=>chrome.storage.local.get(null)')
                if args.cold_start:
                    settings['autoCapture']=True;core.call('settings.update',settings)
                    cold_popup=context.new_page();cold_popup.goto(f'chrome-extension://{extension_id}/popup.html');cold_popup.wait_for_load_state('networkidle')
                    cold_popup.evaluate('() => chrome.runtime.sendMessage({type:"connect"})')
                    before=set(downloads.iterdir());core.close()
                    cold_popup.get_by_role('textbox',name='文件链接').fill(base+'/cold.bin?size=25165824')
                    cold_popup.get_by_role('button',name='下载此链接').click()
                    saw_assertion=False;deadline=time.time()+35
                    while time.time()<deadline:
                        output=subprocess.check_output(['pmset','-g','assertions'],text=True)
                        saw_assertion=saw_assertion or '(DSLQ)' in output
                        new_files=[f for f in downloads.iterdir() if f not in before and not f.name.startswith('.')]
                        if new_files:break
                        time.sleep(.1)
                    assert new_files,'cold launch did not complete download';check_hash(new_files[0],24*1024*1024)
                    assert saw_assertion,'SwiftUI app never held the download power assertion'
                    wait(lambda:'(DSLQ)' not in subprocess.check_output(['pmset','-g','assertions'],text=True))
                    report['checks'].append('native host cold-launches SwiftUI app, downloads, and releases real power assertion')
                report['version']=context.browser.version
                print(json.dumps(report,ensure_ascii=False),flush=True)
            finally:
                try:
                    report['tasks']=core.call('snapshot')['tasks'] if core.p.poll() is None else core.snapshot['tasks']
                    print('Task states:',[(t['source'],t['state'],t['error']) for t in report['tasks']],flush=True)
                    report['diagnostics']=popup.evaluate('async()=> (await chrome.storage.session.get("decisions")).decisions')
                    print('Capture decisions:',report['diagnostics'],flush=True)
                except Exception as e:print('State collection:',str(e),flush=True)
                context.close()
    finally:
        core.close();server.shutdown()
        (ROOT/'.cache'/f'{args.label}-report.json').write_text(json.dumps(report,ensure_ascii=False,indent=2))

if __name__=='__main__':main()
