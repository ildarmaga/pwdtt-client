/** xray routing rule for WB Stream VPN (v2rayN-style). */
export type WBOutboundTag = 'proxy' | 'direct' | 'block';

export type WBRoutingPreset = 'global' | 'bypass_lan' | 'ru_direct' | 'custom';

export interface WBRoutingRule {
  id: string;
  enabled: boolean;
  remark: string;
  outboundTag: WBOutboundTag;
  /** comma/space separated: geosite:google, domain:example.com */
  domains: string;
  /** comma/space separated: geoip:ru, 10.0.0.0/8 */
  ips: string;
  port: string;
  network: '' | 'tcp' | 'udp' | 'tcp,udp';
}

export function newRuleId(): string {
  if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID();
  return `r-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

function splitList(s: string): string[] {
  return s.split(/[\n,;]+/).map(v => v.trim()).filter(Boolean);
}

export function ruleToXray(r: WBRoutingRule): Record<string, unknown> | null {
  if (!r.enabled) return null;
  const out: Record<string, unknown> = { type: 'field', outboundTag: r.outboundTag };
  const domains = splitList(r.domains);
  const ips = splitList(r.ips);
  if (domains.length) out.domain = domains;
  if (ips.length) out.ip = ips;
  if (r.port.trim()) out.port = r.port.trim();
  if (r.network) out.network = r.network;
  if (!domains.length && !ips.length && !r.port.trim() && !r.network) return null;
  return out;
}

/** JSON array for wbxray CustomRulesJSON (signaling + default proxy added by backend). */
export function rulesToXrayJSON(rules: WBRoutingRule[]): string {
  const arr: Record<string, unknown>[] = [];
  for (const r of rules) {
    const x = ruleToXray(r);
    if (x) arr.push(x);
  }
  return JSON.stringify(arr);
}

export interface WBRoutingConnectPayload {
  mode: WBRoutingPreset;
  rules: WBRoutingRule[];
}

export function buildRoutingPayload(_mode: WBRoutingPreset, rules: WBRoutingRule[]): string {
  const xrayRules = JSON.parse(rulesToXrayJSON(rules)) as unknown[];
  return JSON.stringify({
    mode: xrayRules.length > 0 ? 'custom' : 'global',
    xrayRules,
  });
}

export function parseRoutingPayload(raw: string): WBRoutingConnectPayload | null {
  try {
    const p = JSON.parse(raw) as WBRoutingConnectPayload;
    if (p && Array.isArray(p.rules)) return p;
  } catch { /* ignore */ }
  return null;
}

export const OUTBOUND_LABELS: Record<WBOutboundTag, string> = {
  proxy: 'Proxy (WB)',
  direct: 'Direct',
  block: 'Block',
};
