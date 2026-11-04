"""Disposable local HTTP fixtures. No external network or real credentials."""
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlparse, parse_qs
from pathlib import Path
import json, time, threading

PATTERN = bytes(range(251)) * 132
class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    def log_message(self, *args): pass
    def handle(self):
        try:super().handle()
        except (ConnectionResetError,BrokenPipeError):pass
    def do_POST(self):
        self.rfile.read(int(self.headers.get('Content-Length','0')))
        self.send_file('post.zip', 24*1024*1024)
    def do_GET(self):
        parsed=urlparse(self.path); query=parse_qs(parsed.query)
        if parsed.path=='/':
            body=b'''<!doctype html><html><head><title>DSL Q integration fixtures</title></head><body><h1>Download fixtures</h1><a href="/auto.zip">Automatic archive</a><br><a href="/small.unlisted">Small file</a><br><a href="/cookie.zip">Cookie file</a><br><a href="/manual.bin?size=1048576">Manual file</a><br><a href="/recover.bin">Recovery file</a><br><a href="/abort.bin">Abort file</a><br><a href="/download?filename=panel-archive.TAR.GZ&amp;delay=5&amp;unknown=1&amp;size=2097152">Panel archive</a><br><a href="/download?filename=small-document.pdf&amp;size=1024">Small document</a><br><a href="/download?filename=encoded&amp;size=2097152">Encoded filename</a><br><a href="/redirect-panel">Panel redirect</a><br><a href="/unreplayable.zip?size=2097152">Unreplayable login</a><br><form method="post" action="/post.zip"><button>POST download</button></form></body></html>'''
            self.send_response(200);self.send_header('Content-Type','text/html');self.send_header('Content-Length',str(len(body)));self.send_header('Set-Cookie','session=dslq-test; Path=/; SameSite=Lax');self.end_headers();self.wfile.write(body);return
        if parsed.path=='/cookie.zip' and 'session=dslq-test' not in self.headers.get('Cookie',''):
            self.send_response(403);self.send_header('Content-Length','0');self.end_headers();return
        if parsed.path=='/download' and ('session=dslq-test' not in self.headers.get('Cookie','') or not self.headers.get('Referer','')):
            self.send_response(403);self.send_header('Content-Length','0');self.end_headers();return
        if parsed.path=='/unreplayable.zip' and self.headers.get('Range')=='bytes=0-0':
            self.send_response(403);self.send_header('Content-Length','0');self.end_headers();return
        if parsed.path=='/redirect-panel':
            self.send_response(302);self.send_header('Location','/download?filename=redirected.tar.gz&size=2097152');self.send_header('Content-Length','0');self.end_headers();return
        if parsed.path=='/redirect.zip':
            self.send_response(302);self.send_header('Location','/auto.zip');self.send_header('Content-Length','0');self.end_headers();return
        size=int(query.get('size',[1024 if parsed.path=='/small.unlisted' else 24*1024*1024])[0])
        if 'Range' not in self.headers: time.sleep(float(query.get('delay',[0])[0]))
        name=query.get('filename',[Path(parsed.path).name or 'file.bin'])[0]
        self.send_file(name,size,unknown=query.get('unknown',['0'])[0]=='1')
    def send_file(self,name,size,unknown=False):
        start,end=0,size-1; partial=False
        if self.command=='GET' and 'Range' in self.headers:
            try:
                first,last=self.headers['Range'].removeprefix('bytes=').split('-');start=int(first);end=min(int(last or size-1),size-1);partial=True
            except ValueError:
                self.send_error(400);return
        if start>=size:
            self.send_response(416);self.send_header('Content-Range',f'bytes */{size}');self.send_header('Content-Length','0');self.end_headers();return
        self.send_response(206 if partial else 200)
        self.send_header('Content-Type','application/octet-stream');self.send_header('Content-Disposition',f'attachment; filename="{name}"' if name!='encoded' else "attachment; filename=archive.tar.gz; filename*=UTF-8''%E6%B5%8B%E8%AF%95.TAR.GZ")
        self.send_header('ETag','"dslq-fixture-v1"');self.send_header('Accept-Ranges','bytes')
        if not unknown or partial:self.send_header('Content-Length',str(end-start+1))
        else:self.send_header('Connection','close');self.close_connection=True
        if partial:self.send_header('Content-Range',f'bytes {start}-{end}/{size}')
        self.end_headers()
        try:
            offset=start
            while offset<=end:
                n=min(32*1024,end-offset+1);pos=offset%251
                self.wfile.write(PATTERN[pos:pos+n]);self.wfile.flush();offset+=n
                if end>start:time.sleep(0.008)
        except (BrokenPipeError,ConnectionResetError): pass

def start_server(port=0):
    server=ThreadingHTTPServer(('127.0.0.1',port),Handler);server.daemon_threads=True
    threading.Thread(target=server.serve_forever,daemon=True).start()
    return server

if __name__=='__main__':
    import argparse
    parser=argparse.ArgumentParser();parser.add_argument('--port',type=int,default=0);args=parser.parse_args()
    server=start_server(args.port);url=f'http://127.0.0.1:{server.server_port}'
    Path('.cache/fixture.json').write_text(json.dumps({'url':url}))
    print(url,flush=True)
    try:
        while True:time.sleep(1)
    except KeyboardInterrupt:server.shutdown()
