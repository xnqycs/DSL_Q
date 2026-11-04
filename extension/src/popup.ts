import { decisionLabels, type CaptureDecision } from "./rules.ts";
const status = document.querySelector<HTMLElement>("#status")!;
const detail = document.querySelector<HTMLElement>("#detail")!;
const rules = document.querySelector<HTMLElement>("#rules")!;
const recent = document.querySelector<HTMLElement>("#recent")!;
const connect = document.querySelector<HTMLButtonElement>("#connect")!;
const permission = document.querySelector<HTMLButtonElement>("#permission")!;
const download = document.querySelector<HTMLButtonElement>("#download")!;
const urlInput = document.querySelector<HTMLInputElement>("#url")!;
async function refresh(type: "status" | "connect") {
  status.textContent = "正在连接…";
  try {
    const response = await chrome.runtime.sendMessage({ type });
    status.textContent = response.ok ? "已连接 DSL Q" : "客户端未连接";
    status.dataset.connected = String(response.ok);
    detail.textContent = response.ok ? "连接正常。下载结果和未接管原因会显示在下方。" : response.error || "请打开客户端并检查浏览器集成设置。";
    rules.textContent = response.settings ? (response.settings.autoCapture ?
      `自动接管已开启 · ${response.settings.captureExtensions.length} 种类型，或至少 ${response.settings.captureMinBytes / (1024 * 1024)} MiB` :
      "自动接管未开启 · 请在客户端设置中启用并保存") : "暂时无法读取接管规则";
    recent.replaceChildren();
    for (const entry of (response.decisions ?? []) as CaptureDecision[]) {
      const row = document.createElement("li");
      const name = document.createElement("strong");
      name.textContent = entry.filename || entry.host || "文件下载";
      const result = document.createElement("span");
      result.textContent = decisionLabels[entry.code] ?? "等待处理";
      const origin = document.createElement("small");
      origin.textContent = `${new Date(entry.time).toLocaleTimeString()}${entry.host ? ` · ${entry.host}` : ""}`;
      row.append(name, result, origin); recent.append(row);
    }
    if (!recent.children.length) { const row = document.createElement("li"); row.textContent = "暂无下载记录"; recent.append(row); }
  } catch { status.textContent = "连接失败"; detail.textContent = "请重新加载扩展后重试。"; }
  const granted = await chrome.permissions.contains({ origins: ["http://*/*", "https://*/*"] });
  permission.textContent = granted ? "网站访问已授权" : "授权网站访问以接管下载";
  permission.disabled = granted;
}
connect.addEventListener("click", () => void refresh("connect"));
permission.addEventListener("click", () => {
  void chrome.permissions.request({ origins: ["http://*/*", "https://*/*"] }).then(() => refresh("status"));
});
download.addEventListener("click", () => {
  let url: URL;
  try { url = new URL(urlInput.value); if (!["http:","https:"].includes(url.protocol) || url.username || url.password) throw new Error(); }
  catch { detail.textContent = "请输入有效的 HTTP / HTTPS 文件链接。"; return; }
  void chrome.permissions.request({origins:[`${url.protocol}//${url.hostname}/*`]}).then(async granted => {
    if (!granted) { detail.textContent = "需要授权此站点，才能接管下载。"; return; }
    const response = await chrome.runtime.sendMessage({type:"download",url:url.href});
    detail.textContent = response?.ok ? "下载已发起；请在下方查看交接结果，或在 Chrome 查看保留的下载。" : response?.error ?? "未能发起下载。";
  }).catch(() => { detail.textContent = "未能发起下载，请检查客户端连接。"; });
});
chrome.storage.onChanged.addListener((changes, area) => {
  if (area === "session" && changes.decisions) void refresh("status");
});
void refresh("status");
