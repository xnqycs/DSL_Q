"""Exercise an actual SIGKILL and restart of the packaged Go process."""
from pathlib import Path
import json, tempfile
from browser_smoke import ROOT, Core, wait, check_hash
from fixture import start_server

work=Path(tempfile.mkdtemp(prefix='crash.',dir=ROOT/'.cache'))
server=start_server();first=Core(work/'data');second=None
try:
    settings=first.snapshot['settings'];settings.update(directory=str(work/'downloads'),connections=2)
    first.call('settings.update',settings)
    task=first.call('tasks.add',{'url':f'http://127.0.0.1:{server.server_port}/crash.zip?size=67108864'})
    def checkpoint():
        current=first.call('snapshot')['tasks'][0]
        return current if any(c['done'] for c in current['chunks']) else None
    before=wait(checkpoint);saved=sum(c['done'] for c in before['chunks'])
    first.p.kill();first.p.wait(timeout=5)
    second=Core(work/'data')
    restored=second.snapshot['tasks'][0]
    assert sum(c['done'] for c in restored['chunks'])>=saved
    def complete():
        t=second.call('snapshot')['tasks'][0]
        assert t['state']!='failed',t['error']
        return t if t['state']=='completed' else None
    done=wait(complete);check_hash(Path(done['path']),64*1024*1024)
    report={'checks':['packaged process SIGKILL','durable completed chunks restored','resumed download SHA-256 matches'],'savedChunks':saved,'restoredChunks':sum(c['done'] for c in restored['chunks']),'fileSize':done['total']}
    (ROOT/'.cache/core-process-report.json').write_text(json.dumps(report,indent=2))
    print(json.dumps(report))
finally:
    first.close()
    if second:second.close()
    server.shutdown()
