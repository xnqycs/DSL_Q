import { normalizeExtensions } from "./defaults.ts";

export interface CaptureSettings {
  autoCapture: boolean;
  captureMinBytes: number;
  captureExtensions: string[];
  excludedHosts: string[];
}
export interface Candidate {
  id?: number; url: string; finalUrl?: string; filename?: string; totalBytes: number;
  danger: string; state: string; incognito: boolean;
}
export type DecisionCode = "captured" | "disabled" | "rule_mismatch" | "excluded" | "incognito" | "unsafe" |
  "unsupported_url" | "other_extension" | "request_missing" | "request_ambiguous" | "request_unsupported" |
  "auth_failed" | "prepare_failed" | "handoff_pending" | "browser_finished";
export const decisionLabels: Record<DecisionCode, string> = {
  captured: "已交给 DSL Q", disabled: "自动接管未开启", rule_mismatch: "未匹配文件类型或大小规则",
  excluded: "站点已排除", incognito: "无痕下载由 Chrome 处理", unsafe: "Chrome 正在检查或已警告此下载",
  unsupported_url: "此链接类型由 Chrome 处理（例如 blob）", other_extension: "由其他扩展发起，保留在 Chrome",
  request_missing: "未找到对应请求，请检查网站授权", request_ambiguous: "存在多个相同请求，保留在 Chrome",
  request_unsupported: "非 GET 请求由 Chrome 处理", auth_failed: "无法复现网站登录或授权，已恢复 Chrome 下载",
  prepare_failed: "客户端准备失败，已恢复 Chrome 下载", handoff_pending: "交接尚未确认，等待客户端恢复",
  browser_finished: "Chrome 已结束下载",
};
export function captureReason(item: Candidate, settings: CaptureSettings, forced = false): DecisionCode | undefined {
  if (item.incognito) return "incognito";
  if (item.state !== "in_progress") return "browser_finished";
  if (item.danger !== "safe") return "unsafe";
  let url: URL;
  try { url = new URL(item.finalUrl || item.url); } catch { return "unsupported_url"; }
  if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) return "unsupported_url";
  // Both the original site and the final destination participate in exclusions.
  const hosts = [url.hostname];
  try { hosts.push(new URL(item.url).hostname); } catch { /* final URL already validated */ }
  if (settings.excludedHosts.some(h => hosts.some(host => host === h || host.endsWith(`.${h}`)))) return "excluded";
  if (forced) return;
  if (!settings.autoCapture) return "disabled";
  let name = item.filename;
  if (!name) { try { name = decodeURIComponent(url.pathname); } catch { name = url.pathname; } }
  const lower = name.toLowerCase();
  if ((item.totalBytes >= 0 && item.totalBytes >= settings.captureMinBytes) ||
      normalizeExtensions(settings.captureExtensions).some(ext => lower.endsWith(`.${ext}`))) return;
  return "rule_mismatch";
}
export function eligible(item: Candidate, settings: CaptureSettings, forced = false): boolean {
  return captureReason(item, settings, forced) === undefined;
}

// All timestamps here are Date.now() at receipt, never webRequest.timeStamp.
export interface ObservedRequest {
  requestId: string; urls: string[]; method: string; originalMethod: string;
  time: number; updatedAt: number; headers: Record<string, string>; headersOrigin?: string;
  responseUrl?: string; responseAt?: number; completedAt?: number;
  filename?: string; totalBytes?: number; downloadId?: number;
}
export interface DownloadIdentity { id?: number; url: string; finalUrl?: string }
export function matchRequest(item: DownloadIdentity, records: ObservedRequest[], now = Date.now()):
  { record?: ObservedRequest; reason?: DecisionCode } {
  const finalURL = item.finalUrl || item.url;
  const matches = records.filter(r => (r.downloadId === undefined || r.downloadId === item.id) &&
    r.responseUrl === finalURL && r.urls.includes(item.url) && r.urls.includes(finalURL) &&
    now - r.updatedAt < 90_000);
  if (matches.length !== 1) return { reason: matches.length ? "request_ambiguous" : "request_missing" };
  const record = matches[0];
  if (record.originalMethod !== "GET" || record.method !== "GET") return { reason: "request_unsupported" };
  return { record };
}
export function correlate(item: DownloadIdentity, records: ObservedRequest[], now = Date.now()): ObservedRequest | undefined {
  return matchRequest(item, records, now).record;
}

export function responseFilename(disposition: string): string | undefined {
  const extended = /(?:^|;)\s*filename\*\s*=\s*(?:"([^"]*)"|([^;]*))/i.exec(disposition);
  const value = extended?.[1] ?? extended?.[2];
  if (value) {
    const parts = /^utf-8'[^']*'(.*)$/i.exec(value.trim());
    if (parts) { try { return basename(decodeURIComponent(parts[1])); } catch { /* use plain filename */ } }
  }
  const plain = /(?:^|;)\s*filename\s*=\s*(?:"((?:\\.|[^"\\])*)"|([^;]*))/i.exec(disposition);
  const name = plain?.[1]?.replace(/\\(.)/g, "$1") ?? plain?.[2]?.trim();
  return name ? basename(name) : undefined;
}
export function basename(name: string): string {
  return name.split(/[\\/]/).at(-1)?.replace(/[\u0000-\u001f\u007f]/g, "") ?? "";
}
export interface CaptureDecision { downloadId: number; time: number; host: string; filename: string; code: DecisionCode }
export function decisionFor(item: {id: number; url: string; filename?: string}, code: DecisionCode): CaptureDecision {
  let host = "";
  try { host = new URL(item.url).host; } catch { /* unsupported URL */ }
  // Never derive a display name from the URL: query strings can contain tokens.
  return { downloadId: item.id, time: Date.now(), host, filename: basename(item.filename ?? "").split(/[?#]/)[0].slice(0, 120), code };
}

export type HandoffPhase = "paused" | "prepared" | "cancelling" | "cancelled";
export function recoveryAction(phase: HandoffPhase, browserState: string, coreState: string): "commit" | "abort" | "done" | "wait" {
  if (coreState === "committed") return "done";
  if (coreState === "missing" || coreState === "aborted") {
    return phase === "cancelled" || (phase === "cancelling" && browserState !== "in_progress") ? "wait" : "abort";
  }
  if (phase === "cancelled" || (phase === "cancelling" && browserState === "interrupted")) return "commit";
  if (browserState === "complete") return "abort";
  if (browserState === "in_progress" || phase === "paused" || phase === "prepared") return "abort";
  return "wait";
}
