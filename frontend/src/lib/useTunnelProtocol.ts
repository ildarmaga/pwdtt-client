import { useState, useEffect } from 'react';
import { settingsStore } from './store';
import type { TunnelProtocol } from './types';

export function useTunnelProtocol(): TunnelProtocol {
  const [protocol, setProtocol] = useState<TunnelProtocol>(() => settingsStore.get().tunnelProtocol);
  useEffect(() => settingsStore.subscribe(s => setProtocol(s.tunnelProtocol)), []);
  return protocol;
}

export function isVkProtocol(p: TunnelProtocol): boolean {
  return p === 'vk';
}

export function isWbProtocol(p: TunnelProtocol): boolean {
  return p === 'wb';
}
