export interface CaptureSettings {
  autoCapture: boolean;
  captureMinBytes: number;
  captureExtensions: string[];
  excludedHosts: string[];
}
export interface Candidate {
  url: string; finalUrl?: string; filename?: string; totalBytes: number;
  danger: string; state: string; incognito: boolean;
}
export function eligible(item: Candidate, settings: CaptureSettings, forced = false): boolean {
  if (item.incognito || item.state !== "in_progress" || item.danger !== "safe") return false;
  let url: URL;
  try { url = new URL(item.finalUrl || item.url); } catch { return false; }
  if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) return false;
  if (settings.excludedHosts.some(h => url.hostname === h || url.hostname.endsWith(`.${h}`))) return false;
  if (forced) return true;
  if (!settings.autoCapture) return false;
  const name = item.filename || url.pathname;
  const extension = name.split(".").at(-1)?.toLowerCase() ?? "";
  return (item.totalBytes >= 0 && item.totalBytes >= settings.captureMinBytes) || settings.captureExtensions.includes(extension);
}

export interface ObservedRequest {
  requestId: string; urls: string[]; method: string; originalMethod: string;
  time: number; headers: Record<string, string>;
}
export function correlate(item: {url: string; finalUrl?: string; startTime: string}, records: ObservedRequest[]): ObservedRequest | undefined {
  const start = Date.parse(item.startTime);
  const matches = records.filter(r => r.originalMethod === "GET" && r.method === "GET" &&
    r.urls.includes(item.url) && r.urls.includes(item.finalUrl || item.url) && Math.abs(r.time - start) < 3000);
  return matches.length === 1 ? matches[0] : undefined;
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
