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

/** Preset templates (user can edit after load). */
export function presetRules(preset: Exclude<WBRoutingPreset, 'custom'>): WBRoutingRule[] {
  const blockQuic: WBRoutingRule = {
    id: newRuleId(),
    enabled: true,
    remark: 'Блок UDP/443 (QUIC)',
    outboundTag: 'block',
    domains: '',
    ips: '',
    port: '443',
    network: 'udp',
  };
  switch (preset) {
    case 'global':
      return [blockQuic];
    case 'bypass_lan':
      return [
        blockQuic,
        {
          id: newRuleId(),
          enabled: true,
          remark: 'Локальные IP',
          outboundTag: 'direct',
          domains: '',
          ips: 'geoip:private',
          port: '',
          network: '',
        },
        {
          id: newRuleId(),
          enabled: true,
          remark: 'Локальные домены',
          outboundTag: 'direct',
          domains: 'geosite:private',
          ips: '',
          port: '',
          network: '',
        },
      ];
    case 'ru_direct':
      return [
        blockQuic,
        {
          id: newRuleId(),
          enabled: true,
          remark: 'Локальные IP',
          outboundTag: 'direct',
          domains: '',
          ips: 'geoip:private',
          port: '',
          network: '',
        },
        {
          id: newRuleId(),
          enabled: true,
          remark: 'Локальные домены',
          outboundTag: 'direct',
          domains: 'geosite:private',
          ips: '',
          port: '',
          network: '',
        },
        {
          id: newRuleId(),
          enabled: true,
          remark: 'RU IP → direct',
          outboundTag: 'direct',
          domains: '',
          ips: 'geoip:ru',
          port: '',
          network: '',
        },
        {
          id: newRuleId(),
          enabled: true,
          remark: 'RU домены → direct',
          outboundTag: 'direct',
          domains: 'geosite:category-ru, geosite:ru',
          ips: '',
          port: '',
          network: '',
        },
      ];
  }
}

export const DEFAULT_WB_ROUTING_RULES = presetRules('global');

export function ruleToXrayList(r: WBRoutingRule): Record<string, unknown>[] {
  if (!r.enabled) return [];
  const domains = splitList(r.domains);
  const ips = splitList(r.ips);
  const port = r.port.trim();
  const network = r.network;
  if (!domains.length && !ips.length && !port && !network) return [];

  const base = (): Record<string, unknown> => {
    const out: Record<string, unknown> = { type: 'field', outboundTag: r.outboundTag };
    if (port) out.port = port;
    if (network) out.network = network;
    return out;
  };

  // xray ANDs domain+ip in one rule — split so geosite OR geoip works (v2rayN-style).
  if (domains.length && ips.length) {
    const d = base();
    d.domain = domains;
    const i = base();
    i.ip = ips;
    return [d, i];
  }
  const out = base();
  if (domains.length) out.domain = domains;
  if (ips.length) out.ip = ips;
  return [out];
}

/** @deprecated use ruleToXrayList */
export function ruleToXray(r: WBRoutingRule): Record<string, unknown> | null {
  const list = ruleToXrayList(r);
  return list.length === 1 ? list[0] : list.length > 1 ? list[0] : null;
}

/** JSON array for wbxray CustomRulesJSON (signaling + default proxy added by backend). */
export function rulesToXrayJSON(rules: WBRoutingRule[]): string {
  const arr: Record<string, unknown>[] = [];
  for (const r of rules) {
    arr.push(...ruleToXrayList(r));
  }
  return JSON.stringify(arr);
}

export interface WBRoutingConnectPayload {
  mode: WBRoutingPreset;
  rules: WBRoutingRule[];
}

export function buildRoutingPayload(mode: WBRoutingPreset, rules: WBRoutingRule[]): string {
  const active = rules.length ? rules : presetRules(mode === 'custom' ? 'global' : mode);
  return JSON.stringify({
    mode: mode === 'custom' ? 'custom' : mode,
    xrayRules: JSON.parse(rulesToXrayJSON(active)),
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

export const PRESET_LABELS: Record<Exclude<WBRoutingPreset, 'custom'>, string> = {
  global: 'Global',
  bypass_lan: 'Bypass LAN',
  ru_direct: 'RU direct',
};
