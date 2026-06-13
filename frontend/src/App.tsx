import { useEffect } from 'react';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import DevPreviewShell from './components/DevPreviewShell';
import Connect from './pages/Connect';
import Logs from './pages/Logs';
import Toast from './components/Toast';
import { wdttLinkStore, resolveWdttImport, isImportableInput } from './lib/utils/wdttLink';
import { isPasteTargetEditable } from './lib/utils/inputPaste';
import { toastStore } from './lib/stores/toastStore';
import { connectionErrorStore } from './lib/stores/connectionErrorStore';
import { parseConnectionError } from './lib/utils/connectionErrors';
import { logStore } from './lib/stores/logStore';
import { tunnelStore } from './lib/stores/tunnelStore';
import { tunnelStatsStore } from './lib/stores/tunnelStatsStore';
import type { LogLevel } from './lib/stores/logStore';
import { EventsOn } from '../wailsjs/runtime/runtime';
import { settingsStore } from './lib/store';
import { SetTrayEnabled } from '../wailsjs/go/backend/App';

function useWdttPaste() {
  useEffect(() => {
    const handler = (e: ClipboardEvent) => {
      if (isPasteTargetEditable(e.target)) return;
      const text = e.clipboardData?.getData('text') ?? '';
      const trimmed = text.trim();
      if (!isImportableInput(trimmed)) return;
      e.preventDefault();
      void (async () => {
        const link = await resolveWdttImport(trimmed);
        if (!link) {
          toastStore.show('Неверный формат ссылки');
          return;
        }
        wdttLinkStore.set(link);
      })();
    };
    document.addEventListener('paste', handler);
    return () => document.removeEventListener('paste', handler);
  }, []);
}

function useWailsEvents() {
  useEffect(() => {
    const offs = [
      EventsOn('log', (level: unknown, msg: unknown) => {
        const text = String(msg ?? '');
        logStore.push((level as LogLevel) ?? 'INFO', text);
        const connErr = parseConnectionError(text);
        if (connErr) connectionErrorStore.show(connErr);
      }),
      EventsOn('error', (msg: unknown) => {
        const s = String(msg ?? '');
        logStore.push('ERROR', s);
        connectionErrorStore.show(s);
        toastStore.show(s, 8000);
      }),
      EventsOn('state_changed', (status: unknown) => {
        const s = String(status ?? '');
        if (s === 'running') {
          tunnelStore.set('connected');
          connectionErrorStore.clear();
          logStore.push('INFO', '✓ Туннель активен');
        }
        else if (s === 'connecting') { tunnelStore.set('connecting'); logStore.push('INFO', '⟳ Подключение...'); }
        else if (s === 'stopped' || s === 'error' || s === 'disconnected') {
          tunnelStore.set('idle');
          tunnelStatsStore.reset();
          logStore.push('INFO', '— Отключено');
        }
      }),
      EventsOn('tunnel_stats', (rx: unknown, tx: unknown, workers: unknown, turnRtt: unknown, dtlsHs: unknown, internetRtt: unknown) => {
        const rxN = Number(rx) || 0;
        const txN = Number(tx) || 0;
        if (rxN === 0 && txN === 0 && Number(workers) === 0) {
          tunnelStatsStore.reset();
          return;
        }
        tunnelStatsStore.update({
          rxBytes: rxN,
          txBytes: txN,
          workers: Number(workers) || 0,
          turnRttMs: Number(turnRtt) || 0,
          dtlsHsMs: Number(dtlsHs) || 0,
          internetRttMs: Number(internetRtt) || 0,
        });
      }),
      EventsOn('event', (name: unknown) => {
        if (name === 'wg_config') tunnelStore.set('connected');
      }),
    ];
    return () => offs.forEach(off => off());
  }, []);
}

export default function App() {
  useWailsEvents();
  useWdttPaste();

  useEffect(() => {
    const s = settingsStore.get();
    SetTrayEnabled(s.tray);
  }, []);

  return (
    <DevPreviewShell>
      <BrowserRouter>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<Connect />} />
            <Route path="/logs" element={<Logs />} />
          </Route>
        </Routes>
        <Toast />
      </BrowserRouter>
    </DevPreviewShell>
  );
}
