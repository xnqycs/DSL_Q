import { eligible, correlate, recoveryAction, type CaptureSettings, type ObservedRequest, type HandoffPhase } from "./rules.ts";

const HOST = "local.dslq.download";
const defaults: CaptureSettings = { autoCapture: false, captureMinBytes: 20 * 1024 * 1024, captureExtensions: ["dmg", "pkg", "zip", "7z", "rar", "tar", "gz", "xz", "iso"], excludedHosts: [] };
let settings = defaults;
const records = new Map<string, ObservedRequest>();
const handling = new Set<number>();
const manualURLs = new Set<string>();
const ready = chrome.storage.session.get(["requests", "settings"]).then(data => {
  for (const r of (data.requests ?? []) as ObservedRequest[]) if (Date.now() - r.time < 90_000) records.set(r.requestId, r);
  settings = (data.settings as CaptureSettings | undefined) ?? defaults;
});

async function native<T>(method: string, params: object = {}): Promise<T> {
  const result = await chrome.runtime.sendNativeMessage(HOST, { jsonrpc: "2.0", protocol: 1, id: crypto.randomUUID(), method, params });
  if (result?.protocol !== 1 || result?.error) throw new Error(result?.error?.message ?? "客户端版本不兼容");
  return result.result as T;
}

async function refreshSettings(): Promise<boolean> {
  try {
    // A background check must not reopen an explicitly quit application.
    await native("browser.ping");
    settings = await native<CaptureSettings>("browser.settings");
    await chrome.storage.session.set({ settings });
    await chrome.action.setBadgeText({ text: "" });
    return true;
  } catch { return false; }
}

async function persistRequests() {
  for (const [id, r] of records) if (Date.now() - r.time > 90_000) records.delete(id);
  while (records.size > 256) records.delete(records.keys().next().value!);
  await chrome.storage.session.set({ requests: [...records.values()] });
}

chrome.webRequest.onBeforeRequest.addListener(details => {
  void (async () => {
    await ready;
    if (!settings.autoCapture && !manualURLs.has(details.url) && !records.has(details.requestId)) return;
    const existing = records.get(details.requestId);
    records.set(details.requestId, { requestId: details.requestId, urls: [...new Set([...(existing?.urls ?? []), details.url])], method: details.method,
      originalMethod: existing?.originalMethod ?? details.method, time: existing?.time ?? details.timeStamp, headers: {} });
    await persistRequests();
  })();
}, { urls: ["http://*/*", "https://*/*"] });

chrome.webRequest.onSendHeaders.addListener(details => {
  void (async () => {
    await ready;
    const record = records.get(details.requestId); if (!record) return;
    record.headers = {};
    for (const h of details.requestHeaders ?? []) {
      const name = h.name.toLowerCase();
      if (["cookie", "referer", "user-agent", "accept", "accept-language", "origin"].includes(name) && h.value) record.headers[h.name] = h.value;
    }
    await persistRequests();
  })();
}, { urls: ["http://*/*", "https://*/*"] }, ["requestHeaders", "extraHeaders"]);

interface Pending { id: string; downloadId: number; phase: HandoffPhase; createdAt: number }
const pendingKey = (id: string) => `handoff:${id}`;
async function savePending(p: Pending) { await chrome.storage.local.set({ [pendingKey(p.id)]: p }); }
async function forget(p: Pending) { await chrome.storage.local.remove(pendingKey(p.id)); }
async function resumeBrowser(p: Pending) {
  const [item] = await chrome.downloads.search({ id: p.downloadId });
  if (item?.state === "in_progress" && item.paused) await chrome.downloads.resume(item.id);
}

async function processDownload(id: number) {
  if (handling.has(id)) return;
  handling.add(id);
  let pending: Pending | undefined;
  try {
    await ready;
    const [item] = await chrome.downloads.search({ id });
    if (!item || item.paused) return;
    const forced = item.byExtensionId === chrome.runtime.id;
    if (!eligible(item, settings, forced)) return;
    if (item.byExtensionId && !forced) return;
    // Chrome does not expose webRequest events for downloads initiated by
    // this extension. Those are known GETs from our explicit manual action;
    // if authentication cannot be replayed, prepare fails and Chrome resumes.
    const record = correlate(item, [...records.values()]) ?? (forced ? {headers:{}} : undefined);
    // Download IDs have no direct webRequest ID; ambiguous matches stay in browser.
    if (!record) return;
    pending = { id: crypto.randomUUID(), downloadId: item.id, phase: "paused", createdAt: Date.now() };
    await savePending(pending);
    await chrome.downloads.pause(item.id);
    const [paused] = await chrome.downloads.search({ id: item.id });
    if (!paused || paused.state !== "in_progress" || !paused.paused || paused.danger !== "safe") throw new Error("原下载状态已变化");
    const filename = item.filename?.split(/[\\/]/).at(-1) ?? "";
    await native("browser.prepare", { url: item.finalUrl || item.url, filename, headers: record.headers, handoffId: pending.id });
    pending.phase = "prepared"; await savePending(pending);
    const [current] = await chrome.downloads.search({ id: item.id });
    if (!current || current.state !== "in_progress" || current.danger !== "safe") throw new Error("原下载已结束");
    pending.phase = "cancelling"; await savePending(pending);
    await chrome.downloads.cancel(item.id);
    pending.phase = "cancelled"; await savePending(pending);
    await native("browser.commit", { handoffId: pending.id });
    await forget(pending);
  } catch {
    if (pending) await recover(pending).catch(async () => {
      await chrome.action.setBadgeText({ text: "!" });
      await chrome.action.setBadgeBackgroundColor({ color: "#d97706" });
    });
  } finally { handling.delete(id); }
}

async function recover(p: Pending) {
  const [item] = await chrome.downloads.search({ id: p.downloadId });
  let status: { state: string };
  try { status = await native("browser.status", { handoffId: p.id }); }
  catch {
    if (p.phase !== "cancelled" && p.phase !== "cancelling") await resumeBrowser(p);
    throw new Error("等待客户端恢复");
  }
  const action = recoveryAction(p.phase, item?.state ?? "missing", status.state);
  if (action === "commit") { await native("browser.commit", { handoffId: p.id }); await forget(p); }
  else if (action === "abort") { await native("browser.abort", { handoffId: p.id }); await resumeBrowser(p); await forget(p); }
  else if (action === "done") await forget(p);
  else throw new Error("交接状态尚未确认，请保持客户端开启");
}

async function recoverAll() {
  const saved = await chrome.storage.local.get(null);
  for (const [key, value] of Object.entries(saved)) {
    if (!key.startsWith("handoff:")) continue;
    const p = value as Pending;
    if (!handling.has(p.downloadId)) await recover(p).catch(async () => { await chrome.action.setBadgeText({text:"!"}); await chrome.action.setBadgeBackgroundColor({color:"#d97706"}); });
  }
}

chrome.downloads.onCreated.addListener(item => { void processDownload(item.id); });
chrome.downloads.onChanged.addListener(delta => {
  if (delta.filename || delta.totalBytes || delta.danger || delta.finalUrl) void processDownload(delta.id);
});

chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.removeAll(() => chrome.contextMenus.create({ id: "send-dslq", title: "使用 DSL Q 下载链接", contexts: ["link"] }));
  void chrome.alarms.create("refresh", { periodInMinutes: 0.5 });
  void refreshSettings();
});
chrome.runtime.onStartup.addListener(() => { void refreshSettings(); void recoverAll(); });
chrome.alarms.onAlarm.addListener(alarm => { if (alarm.name === "refresh") { void refreshSettings(); void recoverAll(); } });
chrome.contextMenus.onClicked.addListener(info => {
  if (info.menuItemId !== "send-dslq" || !info.linkUrl) return;
  const url = info.linkUrl;
  let origin: string;
  try { const parsed = new URL(url); if (!["http:","https:"].includes(parsed.protocol)) return; origin = `${parsed.protocol}//${parsed.hostname}/*`; } catch { return; }
  void (async () => {
    const granted = await chrome.permissions.request({ origins: [origin] });
    if (!granted) return;
    await queueManualDownload(url);
  })();
});

async function queueManualDownload(url: string): Promise<void> {
  const parsed = new URL(url);
  if (!["http:","https:"].includes(parsed.protocol) || parsed.username || parsed.password) throw new Error("只支持 HTTP / HTTPS 文件链接");
  manualURLs.add(url);
  try { await chrome.downloads.download({ url }); }
  finally { setTimeout(() => manualURLs.delete(url), 15_000); }
}

chrome.runtime.onMessage.addListener((message, sender, respond) => {
  if (sender.id !== chrome.runtime.id || !sender.url?.startsWith(chrome.runtime.getURL(""))) return;
  if (message?.type === "download" && typeof message.url === "string") {
    queueManualDownload(message.url).then(() => respond({ok:true})).catch(e => respond({ok:false,error:String(e.message)}));
    return true;
  }
  if (message?.type === "connect") {
    native<CaptureSettings>("browser.settings").then(async s => { settings = s; await chrome.storage.session.set({ settings }); await recoverAll(); respond({ ok: true, settings }); }).catch(e => respond({ ok: false, error: String(e.message) }));
    return true;
  }
  if (message?.type === "status") {
    refreshSettings().then(ok => respond({ ok, settings })); return true;
  }
});

void ready.then(async () => { await refreshSettings(); await recoverAll(); });
