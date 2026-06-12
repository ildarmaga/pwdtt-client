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
}

export interface AppSettings {
  bypassMode: 'РУЧ' | 'АВТ';
  power: number;
  mtu: number;
  tray: boolean;
  autoStart: boolean;
  hashes: [string, string, string, string];
  useGlobalHashes: boolean;
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
};
