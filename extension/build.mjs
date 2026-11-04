import { build } from 'esbuild';
import { readFile, writeFile, mkdir, copyFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
const { version } = JSON.parse(await readFile(new URL('package.json', import.meta.url), 'utf8'));
const key = (await readFile(new URL('public-key.txt', import.meta.url), 'utf8')).trim();
const id = [...createHash('sha256').update(Buffer.from(key, 'base64')).digest().subarray(0,16)].flatMap(x => [x >> 4,x & 15]).map(x => String.fromCharCode(97+x)).join('');
await mkdir('dist', { recursive: true });
await build({ entryPoints: ['src/background.ts','src/popup.ts'], outdir:'dist', bundle:true, format:'esm', target:'chrome120', sourcemap:false });
const manifest = { manifest_version:3, name:'DSL Q 下载助手', version, description:'将文件下载交给 DSL Q，支持分段下载与断点续传。', minimum_chrome_version:'120', key,
  permissions:['downloads','webRequest','nativeMessaging','storage','contextMenus','alarms'], optional_host_permissions:['http://*/*','https://*/*'],
  background:{ service_worker:'background.js',type:'module' }, action:{default_popup:'popup.html',default_title:'DSL Q 下载助手'}, incognito:'not_allowed' };
await writeFile('dist/manifest.json',JSON.stringify(manifest,null,2)+'\n');
await writeFile('dist/extension-id.txt',id+'\n');
for(const f of ['popup.html','popup.css']) await copyFile(f,`dist/${f}`);
process.stdout.write(`Extension ID: ${id}\n`);
