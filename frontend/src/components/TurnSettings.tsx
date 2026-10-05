import { useEffect, useState } from 'react';
import { DiscoverTURNRelays, GetTURNSettings, ProbeTURNRelays, SaveTURNSelection } from '../../wailsjs/go/backend/App';
import { serverStore, settingsStore } from '../lib/store';
import { selectedServerStore } from '../lib/stores/selectedServerStore';

type Probe = { address: string; replies: number; probes: number; averageMs: number; minMs: number; maxMs: number };
const knownPools = ['95.163.34.0/24', '91.231.135.0/24', '90.156.232.0/21', '193.203.43.0/24', '185.180.200.0/22'];
function numberIP(ip: string) { return ip.split('.').reduce((n, octet) => (n * 256 + Number(octet)) >>> 0, 0); }
function poolOf(host: string) {
  const ip = numberIP(host);
  return knownPools.find(pool => {
    const [base, bits] = pool.split('/'); const mask = (0xffffffff << (32 - Number(bits))) >>> 0;
    return ((ip & mask) >>> 0) === ((numberIP(base) & mask) >>> 0);
  }) ?? `${host.split('.').slice(0, 3).join('.')}.0/24`;
}
function activeHashes() {
  const settings = settingsStore.get(); const all = serverStore.getAll();
  const server = all.find(s => s.id === selectedServerStore.getId()) ?? all[0];
  return (settings.useGlobalHashes ? settings.hashes : server?.hashes ?? []).filter(h => h.trim());
}

export default function TurnSettings() {
  const [relays, setRelays] = useState<string[]>([]);
  const [preferred, setPreferred] = useState<string[]>([]);
  const [results, setResults] = useState<Probe[]>([]);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  useEffect(() => {
    let alive = true;
    setBusy(true);
    (async () => {
      let settings = await GetTURNSettings();
      if (!settings.relays?.length) settings = await DiscoverTURNRelays(activeHashes());
      if (alive) { setRelays(settings.relays ?? []); setPreferred(settings.preferred ?? []); }
    })().catch(e => { if (alive) setMessage(String(e)); }).finally(() => { if (alive) setBusy(false); });
    return () => { alive = false; };
  }, []);
  const probe = async () => {
    setBusy(true); setMessage('');
    try {
      const settings = await DiscoverTURNRelays(activeHashes()); setRelays(settings.relays ?? []);
      setResults(await ProbeTURNRelays());
    } catch (e) { setMessage(String(e)); } finally { setBusy(false); }
  };
  const save = async () => {
    setBusy(true); setMessage('');
    try { await SaveTURNSelection(preferred); setMessage('Сохранено · применится после переподключения'); }
    catch (e) { setMessage(String(e)); } finally { setBusy(false); }
  };
  const pools = [...new Set(relays.map(url => poolOf(url.split(':')[0])))];
  return <div className="st-vk-block">
    <div className="st-section-title">TURN-подсети</div>
    {pools.map(pool => {
      const addresses = relays.filter(url => poolOf(url.split(':')[0]) === pool);
      const measurements = results.filter(r => addresses.includes(r.address) && r.replies > 0);
      const replies = measurements.reduce((sum, r) => sum + r.replies, 0);
      const average = replies ? measurements.reduce((sum, r) => sum + r.averageMs * r.replies, 0) / replies : 0;
      const probes = results.filter(r => addresses.includes(r.address)).reduce((sum, r) => sum + r.probes, 0);
      const selected = preferred.includes(pool) || addresses.some(url => preferred.includes(url.split(':')[0]));
      return <div className="st-row" key={pool}>
        <div><span>{pool}</span><div style={{fontSize:12,color:'var(--text-3)'}}>{addresses.length} адресов от VK</div></div>
        <span style={{fontSize:12}}>{replies ? `${Math.round(average)} мс · ${replies}/${probes}` : results.length ? 'Нет ответа' : '—'}</span>
        <button type="button" role="switch" aria-checked={selected} aria-label={pool} disabled={busy} className={`st-toggle st-toggle--${selected ? 'on' : 'off'}`} onClick={() => setPreferred(old => {
          const remaining = old.filter(x => x !== pool && !addresses.some(url => url.split(':')[0] === x));
          return selected ? remaining : [...remaining, pool];
        })}/>
      </div>;
    })}
    <div style={{display:'flex',gap:8,marginTop:10}}>
      <button className="st-hash-btn" disabled={busy} onClick={probe}>{busy ? 'Проверка…' : 'Проверить пинг'}</button>
      <button className="st-hash-btn" disabled={busy} onClick={save}>Сохранить</button>
    </div>
    {message && <div role="status" className="st-vk-msg">{message}</div>}
  </div>;
}
