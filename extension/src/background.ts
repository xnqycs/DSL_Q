import { captureReason, matchRequest, recoveryAction, responseFilename, basename, decisionFor,
  type CaptureSettings, type ObservedRequest, type HandoffPhase, type CaptureDecision, type DecisionCode } from "./rules.ts";
import { defaults } from "./defaults.ts";
import { downloadQueue } from "./queue.ts";

const HOST = "local.dslq.download";
const filter = { urls: ["http://*/*", "https://*/*"] };
let settings = defaults;
let connectionError = "";
let decisions: CaptureDecision[] = [];
const records = new Map<string, ObservedRequest>();
const handling = new Set<number>();
const settled = new Set<number>();
const manualURLs = new Set<string>();
const candidates = new Map<number, { filename?: string }>();
// Chrome asks extensions before opening Save As, but stops waiting after 15 s.
// Keep our callback until cancellation or an explicit fallback, with time left
// for Chrome to recover. Never take over after releasing it to the browser.
const filenameGates = new Map<number, { release: () => void; metadataReady: boolean }>();
function releaseFilename(id: number) { filenameGates.get(id)?.release(); }
function leaveInBrowser(id: number) {
  if (!filenameGates.has(id)) return; // onCreated may precede the actual filename.
  void finish(id);
  releaseFilename(id);
}
interface Pending { id: string; downloadId: number; phase: HandoffPhase; createdAt: number }
const pendingDownloads = new Map<number, Pending>();
const pendingKey = (id: string) => `handoff:${id}`;
const ready = Promise.all([chrome.storage.session.get(["requests", "settings", "decisions", "settled"]), chrome.storage.local.get(null)]).then(([data, local]) => {
  for (const r of (data.requests ?? []) as ObservedRequest[]) if (Date.now() - r.updatedAt < (r.completedAt ? 90_000 : 300_000)) records.set(r.requestId, r);
  settings = (data.settings as CaptureSettings | undefined) ?? defaults;
  decisions = (data.decisions as CaptureDecision[] | undefined) ?? [];
  for (const id of (data.settled as number[] | undefined) ?? []) settled.add(id);
  for (const [key, value] of Object.entries(local)) {
    if (key.startsWith("handoff:")) { const p = value as Pending; pendingDownloads.set(p.downloadId, p); }
  }
});

async function native<T>(method: string, params: object = {}): Promise<T> {
  const result = await chrome.runtime.sendNativeMessage(HOST, { jsonrpc: "2.0", protocol: 1, id: crypto.randomUUID(), method, params });
  if (result?.protocol !== 1 || result?.error) throw new Error(result?.error?.message ?? "客户端版本不兼容");
  return result.result as T;
}
function connectionMessage(error: unknown): string {
  const message = String(error);
  if (/not found|not registered|forbidden/i.test(message)) return "未找到可用的连接组件，请在客户端重新安装浏览器集成。";
  return "客户端未连接，请打开 DSL Q；若仍失败，请重新安装连接组件。";
}
async function refreshSettings(launch = false): Promise<boolean> {
  await ready;
  try {
    // A background check must not reopen an explicitly quit application.
    if (!launch) await native("browser.ping");
    settings = await native<CaptureSettings>("browser.settings");
    await chrome.storage.session.set({ settings });
    connectionError = "";
    if (!pendingDownloads.size) await chrome.action.setBadgeText({ text: "" });
    return true;
  } catch (error) { connectionError = connectionMessage(error); return false; }
}

let requestSave = Promise.resolve();
function persistRequests() {
  const now = Date.now();
  for (const [id, r] of records) {
    // Keep waiting requests across a slow response; completed records have a
    // short reuse window. A record is claimed by a single download on handoff.
    if (now - r.updatedAt > (r.completedAt ? 90_000 : 300_000)) records.delete(id);
  }
  if (records.size > 1024) {
    for (const [id, r] of records) {
      if (r.completedAt && !r.filename) records.delete(id);
      if (records.size <= 1024) break;
    }
  }
  requestSave = requestSave.catch(() => {}).then(() => chrome.storage.session.set({ requests: [...records.values()] }));
  return requestSave;
}
function observe(work: () => void) {
  void ready.then(() => { work(); return persistRequests(); }).catch(() => {});
  return undefined;
}
function wakeCandidates() { for (const id of candidates.keys()) enqueue(id); }

chrome.webRequest.onBeforeRequest.addListener(details => observe(() => {
  if (!settings.autoCapture && !manualURLs.has(details.url) && !records.has(details.requestId)) return;
  const old = records.get(details.requestId);
  records.set(details.requestId, { requestId: details.requestId,
    urls: [...new Set([...(old?.urls ?? []), details.url])], method: details.method,
    originalMethod: old?.originalMethod ?? details.method, time: old?.time ?? Date.now(), updatedAt: Date.now(), headers: {} });
}), filter);
chrome.webRequest.onSendHeaders.addListener(details => observe(() => {
  const record = records.get(details.requestId); if (!record) return;
  record.updatedAt = Date.now(); record.headers = {};
  record.headersOrigin = new URL(details.url).origin;
  for (const h of details.requestHeaders ?? []) {
    if (["cookie", "referer", "user-agent", "accept", "accept-language", "origin", "authorization"].includes(h.name.toLowerCase()) && h.value) record.headers[h.name] = h.value;
  }
}), filter, ["requestHeaders", "extraHeaders"]);
chrome.webRequest.onHeadersReceived.addListener(details => observe(() => {
  const record = records.get(details.requestId); if (!record) return;
  record.updatedAt = Date.now();
  if (details.statusCode >= 300 && details.statusCode < 400) return;
  record.responseUrl = details.url; record.responseAt = Date.now();
  const headers = new Map((details.responseHeaders ?? []).map(h => [h.name.toLowerCase(), h.value ?? ""]));
  record.filename = responseFilename(headers.get("content-disposition") ?? "");
  const length = Number(headers.get("content-length") ?? -1);
  record.totalBytes = Number.isSafeInteger(length) && length >= 0 ? length : -1;
  wakeCandidates();
}), filter, ["responseHeaders"]);
for (const event of [chrome.webRequest.onCompleted, chrome.webRequest.onErrorOccurred]) {
  event.addListener(details => observe(() => {
    const record = records.get(details.requestId); if (!record) return;
    record.completedAt = record.updatedAt = Date.now(); wakeCandidates();
  }), filter);
}

async function note(item: {id: number; url: string; filename?: string}, code: DecisionCode) {
  const previous = decisions.find(d => d.downloadId === item.id);
  if (previous?.code === code && previous.filename === basename(item.filename ?? "")) return;
  decisions = [decisionFor(item, code), ...decisions.filter(d => d.downloadId !== item.id)].slice(0, 20);
  await chrome.storage.session.set({ decisions });
}
async function finish(id: number) {
  settled.add(id); candidates.delete(id);
  while (settled.size > 500) settled.delete(settled.values().next().value!);
  await chrome.storage.session.set({ settled: [...settled] });
}
async function badge() {
  await chrome.action.setBadgeText({ text: "!" });
  await chrome.action.setBadgeBackgroundColor({ color: "#d97706" });
}
async function savePending(p: Pending) {
  pendingDownloads.set(p.downloadId, p);
  await chrome.storage.local.set({ [pendingKey(p.id)]: p });
}
async function forget(p: Pending) {
  await finish(p.downloadId);
  await chrome.storage.local.remove(pendingKey(p.id));
  pendingDownloads.delete(p.downloadId);
}
async function resumeBrowser(p: Pending) {
  const [item] = await chrome.downloads.search({ id: p.downloadId });
  if (item?.state === "in_progress" && item.paused) await chrome.downloads.resume(item.id);
}

const queue = downloadQueue(processDownload);
function enqueue(id: number, filename?: string): Promise<void> {
  if (settled.has(id)) return Promise.resolve();
  candidates.set(id, { ...candidates.get(id), ...(filename ? { filename } : {}) });
  return queue(id).catch(() => badge());
}
async function processDownload(id: number) {
  await ready;
  if (settled.has(id) || pendingDownloads.has(id)) return;
  let pending: Pending | undefined;
  let item: chrome.downloads.DownloadItem | undefined;
  handling.add(id);
  try {
    [item] = await chrome.downloads.search({ id });
    if (!item) { candidates.delete(id); releaseFilename(id); return; }
    if (item.state !== "in_progress") {
      candidates.delete(id);
      if (!decisions.some(d => d.downloadId === id)) await note(item, "browser_finished");
      releaseFilename(id);
      return;
    }
    if (item.paused) { leaveInBrowser(id); return; }
    const forced = item.byExtensionId === chrome.runtime.id;
    const match = matchRequest(item, [...records.values()]);
    const record = match.record;
    // Chrome (and managed download destinations) can expose a temporary UUID or
    // .crdownload path. Content-Disposition identifies the actual file type.
    item = { ...item, filename: record?.filename || candidates.get(id)?.filename || item.filename,
      totalBytes: item.totalBytes >= 0 ? item.totalBytes : record?.totalBytes ?? -1 };
    const reason = captureReason(item, settings, forced);
    if (reason) {
      await note(item, reason);
      // Response and filename events may arrive in either order. Give missing
      // metadata a short grace period, without delaying known browser-only cases.
      if (reason !== "rule_mismatch" || record || filenameGates.get(id)?.metadataReady) leaveInBrowser(id);
      return;
    }
    if (item.byExtensionId && !forced) { await note(item, "other_extension"); leaveInBrowser(id); return; }
    // Only our explicit downloads.download action is a known GET without
    // webRequest evidence. Never assume an unobserved website request is GET.
    if (!record && !forced) {
      await note(item, match.reason!);
      if (match.reason !== "request_missing" || filenameGates.get(id)?.metadataReady) leaveInBrowser(id);
      return;
    }
    if (settled.has(id)) return;
    const url = item.finalUrl || item.url;
    const headers = record?.headersOrigin === new URL(url).origin ? record.headers : {};
    if (record) { record.downloadId = id; await persistRequests(); }
    pending = { id: crypto.randomUUID(), downloadId: id, phase: "paused", createdAt: Date.now() };
    await savePending(pending);
    await chrome.downloads.pause(id);
    const [paused] = await chrome.downloads.search({ id });
    if (!paused || paused.state !== "in_progress" || !paused.paused || paused.danger !== "safe") throw new Error("原下载状态已变化");
    await native("browser.prepare", { url, filename: basename(item.filename), headers, handoffId: pending.id });
    pending.phase = "prepared"; await savePending(pending);
    if (settled.has(id)) throw new Error("filename_timeout");
    const [current] = await chrome.downloads.search({ id });
    if (!current || current.state !== "in_progress" || current.danger !== "safe") throw new Error("原下载已结束");
    pending.phase = "cancelling"; await savePending(pending);
    if (settled.has(id)) { pending.phase = "prepared"; await savePending(pending); throw new Error("filename_timeout"); }
    await chrome.downloads.cancel(id);
    releaseFilename(id);
    pending.phase = "cancelled"; await savePending(pending);
    await native("browser.commit", { handoffId: pending.id });
    await note(item, "captured"); await forget(pending);
  } catch (error) {
    if (pending) {
      try {
        const action = await recover(pending);
        if (item) await note(item, action === "commit" || action === "done" ? "captured" :
          /filename_timeout/.test(String(error)) ? "prepare_timeout" :
          /授权|登录|返回网页|401|403/.test(String(error)) ? "auth_failed" : "prepare_failed");
      } catch { if (item) await note(item, "handoff_pending"); await badge(); }
    } else if (item) await note(item, "prepare_failed");
  } finally {
    handling.delete(id);
    if (pending || settled.has(id)) releaseFilename(id);
  }
}

async function recover(p: Pending): Promise<"commit" | "abort" | "done"> {
  const [item] = await chrome.downloads.search({ id: p.downloadId });
  let status: { state: string };
  try { status = await native("browser.status", { handoffId: p.id }); }
  catch {
    if (p.phase !== "cancelled" && p.phase !== "cancelling") await resumeBrowser(p);
    throw new Error("等待客户端恢复");
  }
  const action = recoveryAction(p.phase, item?.state ?? "missing", status.state);
  if (action === "commit") await native("browser.commit", { handoffId: p.id });
  else if (action === "abort") { await native("browser.abort", { handoffId: p.id }); await resumeBrowser(p); }
  else if (action !== "done") throw new Error("交接状态尚未确认，请保持客户端开启");
  if (item) await note(item, action === "abort" ? "prepare_failed" : "captured");
  await forget(p);
  return action;
}
async function recoverAll() {
  await ready;
  // Read storage again so persisted handoffs are also recoverable after reload.
  const saved = await chrome.storage.local.get(null);
  for (const [key, value] of Object.entries(saved)) {
    if (!key.startsWith("handoff:")) continue;
    const p = value as Pending;
    pendingDownloads.set(p.downloadId, p);
    if (handling.has(p.downloadId)) continue;
    handling.add(p.downloadId);
    try { await recover(p); }
    catch { await badge(); }
    finally { handling.delete(p.downloadId); }
  }
}

chrome.downloads.onCreated.addListener(item => { void enqueue(item.id); });
chrome.downloads.onDeterminingFilename.addListener((item, suggest) => {
  if (settled.has(item.id) || item.state !== "in_progress") { suggest(); return; }
  let released = false;
  const release = () => {
    if (released) return;
    released = true;
    clearTimeout(deadline); clearTimeout(metadata);
    filenameGates.delete(item.id);
    suggest();
  };
  const deadline = setTimeout(() => {
    void finish(item.id); // Synchronously prevents a late prepare from cancelling.
    release();
    const pending = pendingDownloads.get(item.id);
    if (pending) void resumeBrowser(pending).catch(() => badge());
    void note(item, "prepare_timeout");
  }, 13_000);
  const metadata = setTimeout(() => {
    const gate = filenameGates.get(item.id);
    if (gate) { gate.metadataReady = true; void enqueue(item.id); }
  }, 750);
  filenameGates.set(item.id, { release, metadataReady: false });
  void enqueue(item.id, item.filename);
  return true;
});
chrome.downloads.onChanged.addListener(delta => {
  if (delta.filename || delta.totalBytes || delta.danger || delta.finalUrl || delta.state || delta.paused) void enqueue(delta.id, delta.filename?.current);
});
chrome.downloads.onErased.addListener(id => { candidates.delete(id); releaseFilename(id); });

chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.removeAll(() => chrome.contextMenus.create({ id: "send-dslq", title: "使用 DSL Q 下载链接", contexts: ["link"] }));
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
    if (granted) await queueManualDownload(url);
  })().catch(() => badge());
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
    queueManualDownload(message.url).then(() => respond({ok:true})).catch(() => respond({ok:false,error:"未能发起下载，请检查客户端连接。"}));
    return true;
  }
  if (message?.type === "connect" || message?.type === "status") {
    void (async () => {
      const ok = await refreshSettings(message.type === "connect");
      if (ok) await recoverAll();
      const permissions = await chrome.permissions.contains({ origins: filter.urls });
      respond({ ok, settings, permissions, error: connectionError, decisions });
    })().catch(() => respond({ok:false,error:"无法检查扩展状态，请重新加载扩展。",decisions}));
    return true;
  }
});
void ready.then(async () => {
  await chrome.alarms.create("refresh", { periodInMinutes: 0.5 });
  await refreshSettings(); await recoverAll();
  for (const item of await chrome.downloads.search({ state: "in_progress" })) void enqueue(item.id);
}).catch(() => badge());
