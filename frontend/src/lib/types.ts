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
  /** Название VPN из ссылки (vpn) или Profile-Title */
  vpnName?: string;
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
}

export type TunnelState = 'idle' | 'connecting' | 'connected' | 'disconnecting';

export const DEFAULT_SETTINGS: AppSettings = {
  bypassMode: 'АВТ',
  power: 9,
  mtu: 1380,
  tray: true,
  autoStart: true,
  hashes: ['', '', '', ''],
  useGlobalHashes: false,
  metricsRefreshSec: 0,
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
