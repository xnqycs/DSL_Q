export {};
const status = document.querySelector<HTMLElement>("#status")!;
const detail = document.querySelector<HTMLElement>("#detail")!;
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
    detail.textContent = response.ok ? (response.settings.autoCapture ? "按规则自动接管已开启。右键文件链接也可手动发送。" : "自动接管尚未开启，请在 DSL Q 设置中启用。右键链接仍可手动发送。") : (response.error ?? "先在客户端设置中安装浏览器连接组件，再点击下方按钮。");
  } catch { status.textContent = "连接失败"; }
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
    detail.textContent = response?.ok ? "下载已发起，可在 DSL Q 查看任务。" : response?.error ?? "未能发起下载。";
  }).catch(() => { detail.textContent = "未能发起下载，请检查客户端连接。"; });
});
void refresh("status");
