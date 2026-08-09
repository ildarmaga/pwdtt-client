import type { WBRoutingRule } from './wbRouting';
export type { WBRoutingRule, WBRoutingPreset, WBOutboundTag } from './wbRouting';
export { DEFAULT_WB_ROUTING_RULES } from './wbRouting';

export interface Server {
  id: string;
  name: string;
  host: string;
  password: string;
  deviceId?: string;
  ping?: number;
  icon?: string;
  hashes?: [string, string, string, string];
  power?: number;
  subUrl?: string;
  /** WB Stream room / join link из подписки */
  wbRoom?: string;
  /** Название VPN из ссылки (vpn) или Profile-Title */
  vpnName?: string;
  /** Сервер добавлен по ссылке подписки панели — поля подключения не редактируются */
  linkManaged?: boolean;
}

export function isLinkManagedServer(s: Server): boolean {
  return s.linkManaged === true;
}

/** Протокол туннеля на экране подключения. VK — TURN/VK Calls; WB — WB Stream WebRTC. */
export type TunnelProtocol = 'vk' | 'wb';

/** Маскировка RTP под медиа: audio=OPUS PT111, video=VP8-like PT96. */
export type ObfsMode = 'audio' | 'video';

/** In-app update download progress (EventsOn update_progress). */
export interface UpdateProgressEvent {
  phase?: string;
  percent?: number;
  written?: number;
  total?: number;
  message?: string;
}

export interface AppSettings {
  bypassMode: 'РУЧ' | 'АВТ';
  power: number;
  mtu: number;
  tray: boolean;
  autoStart: boolean;
  hashes: [string, string, string, string];
  useGlobalHashes: boolean;
  /** 0 = авто (каждые 5 сек) */
  metricsRefreshSec: number;
  /** VK (веб/API) гнать через VPN-туннель; по умолчанию VK идёт напрямую */
  vkThroughTunnel: boolean;
  /** Выбранный протокол на главном экране */
  tunnelProtocol: TunnelProtocol;
  /** VK TURN: маскировка DTLS под аудио (OPUS) или видео (VP8) */
  obfsMode: ObfsMode;
  /** wg = WireGuard поверх TURN; raw = IP поверх DTLS без WireGuard (нужен сервер с RAW) */
  tunnelMode: 'wg' | 'raw';
  /** Канал клиент↔VK TURN: tcp|udp. Для RAW лучше udp (multipath). */
  turnTransport: 'tcp' | 'udp';
  /** WB: dual-track (экран + камера) */
  wbDualTrack: boolean;
  /** Ревизия VP8-настроек: bump сбрасывает legacy-значения на безопасные */
  wbVp8Rev: number;
  /** WB Stream: показывать вкладку логов */
  wbShowLogs: boolean;
  wbFps: number;
  wbBatch: number;
  wbProxyAuth: 'auto' | 'manual';
  wbProxyUser: string;
  wbProxyPass: string;
  /** Как iOS: только SOCKS5 — вставить в v2rayN/V2BOX (встроенный TUN отключён) */
  wbSocksOnly: boolean;
  /** Порт локального SOCKS (по умолчанию 10809 — не пересекаться с inbound v2rayN) */
  wbSocksPort: number;
  /** legacy: xray routing (не используется в SOCKS-only) */
  wbRoutingMode: 'global' | 'bypass_lan' | 'ru_direct' | 'custom';
  wbRoutingRules: WBRoutingRule[];
}

export type TunnelState = 'idle' | 'connecting' | 'connected' | 'disconnecting';

export const DEFAULT_SETTINGS: AppSettings = {
  bypassMode: 'АВТ',
  power: 9,
  mtu: 1280,
  tray: true,
  autoStart: true,
  hashes: ['', '', '', ''],
  useGlobalHashes: false,
  metricsRefreshSec: 0,
  vkThroughTunnel: true,
  tunnelProtocol: 'vk',
  obfsMode: 'audio',
  tunnelMode: 'wg',
  turnTransport: 'tcp',
  turnTcpRev: 1,
  // RelayBridge (kulikov0): dual-track off by default — SFU often drops
  // screenshare shards and SOCKS stalls. Enable for extra uplink capacity.
  wbDualTrack: false,
  wbVp8Rev: 4,
  wbShowLogs: true,
  wbFps: 30,
  wbBatch: 64,
  wbProxyAuth: 'auto',
  wbProxyUser: '',
  wbProxyPass: '',
  wbSocksOnly: true,
  wbSocksPort: 10809,
  wbRoutingMode: 'global',
  wbRoutingRules: [],
};

export const METRICS_REFRESH_OPTIONS: { value: number; label: string }[] = [
  { value: 0, label: 'Авто (5 сек)' },
  { value: 30, label: '30 сек' },
  { value: 60, label: '1 мин' },
  { value: 300, label: '5 мин' },
  { value: 900, label: '15 мин' },
  { value: 1800, label: '30 мин' },
  { value: 3600, label: '1 час' },
];
