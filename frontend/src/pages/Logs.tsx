import { useState, useEffect, useLayoutEffect, useRef, useCallback } from 'react';
import { IconSearch, IconTrashX, IconCopy } from '@tabler/icons-react';
import { logStore, type LogEntry, type LogLevel } from '../lib/stores/logStore';
import { useMobileUI } from '../lib/useMobileUI';

type Filter = 'ALL' | 'INFO' | 'GO' | 'STATUS' | 'WARN' | 'ERROR';

const LEVEL_COLOR: Record<LogLevel, string> = {
  INFO:  'var(--text)',
  WARN:  '#f59e0b',
  ERROR: '#ef4444',
  DEBUG: 'var(--text-3)',
  GO:    '#a78bfa',
  STATUS:'#34d399',
};

export default function Logs() {
  const compact = useMobileUI();
  const [filter, setFilter] = useState<Filter>('ALL');
  const [search, setSearch] = useState('');
  const [entries, setEntries] = useState<LogEntry[]>(() => logStore.getAll());
  const listRef = useRef<HTMLDivElement>(null);
  const autoScroll = useRef(true);

  useEffect(() => logStore.subscribe(setEntries), []);

  const scrollToBottom = useCallback(() => {
    const el = listRef.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
  }, []);

  useLayoutEffect(() => {
    if (autoScroll.current) scrollToBottom();
  }, [entries, filter, search, scrollToBottom]);

  const onScroll = useCallback(() => {
    const el = listRef.current;
    if (!el) return;
    autoScroll.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  }, []);

  const visible = entries.filter(e => {
    if (filter !== 'ALL' && e.level !== filter) return false;
    if (search && !e.message.toLowerCase().includes(search.toLowerCase())) return false;
    return true;
  });

  const handleCopy = () => {
    const text = visible.map(e => `[${e.time}] [${e.level}] ${e.message}${e.count > 1 ? ` (×${e.count})` : ''}`).join('\n');
    navigator.clipboard.writeText(text);
  };

  return (
    <>
      <style>{`
        .logs-main { flex: 1; min-height: 0; padding: 2px 2px 2px 0; display: flex; flex-direction: column; animation: page-in 0.25s ease-out; }
        .logs-card { flex: 1; min-height: 0; border: 1px solid var(--border); border-radius: 12px; display: flex; flex-direction: column; overflow: hidden; background: var(--surface); }
        .logs-toolbar { display: flex; align-items: center; gap: 8px; padding: 10px 12px; border-bottom: 1px solid var(--border-2); flex-shrink: 0; min-width: 0; }
        .logs-toolbar--compact { flex-direction: column; align-items: stretch; gap: 6px; padding: 8px 8px; }
        .search-wrap { flex: 1; display: flex; justify-content: center; min-width: 0; }
        .logs-toolbar--compact .search-wrap { flex: none; width: 100%; justify-content: stretch; }
        .search-inner { position: relative; width: 100%; max-width: 380px; }
        .logs-toolbar--compact .search-inner { max-width: none; }
        .search-input { width: 100%; padding: 7px 32px 7px 12px; border: 1px solid var(--input-border); border-radius: 10px; background: var(--input-bg); font-size: 13px; color: var(--text); outline: none; box-sizing: border-box; }
        .search-input::placeholder { color: var(--text-4); }
        .search-icon { position: absolute; right: 8px; top: 50%; transform: translateY(-50%); color: var(--text-3); pointer-events: none; }
        .logs-toolbar-right { display: flex; align-items: center; gap: 6px; flex-shrink: 0; min-width: 0; }
        .logs-toolbar--compact .logs-toolbar-right { width: 100%; justify-content: space-between; }
        .filter-group { display: flex; background: var(--seg-bg); border-radius: 8px; padding: 2px; gap: 1px; min-width: 0; }
        .logs-toolbar--compact .filter-group { flex: 1; overflow-x: auto; -webkit-overflow-scrolling: touch; scrollbar-width: none; }
        .logs-toolbar--compact .filter-group::-webkit-scrollbar { display: none; }
        .filter-btn { padding: 5px 10px; border: none; border-radius: 7px; background: transparent; font-size: 11px; font-weight: 600; color: var(--seg-text); cursor: pointer; transition: background 0.15s, color 0.15s; white-space: nowrap; flex-shrink: 0; }
        .logs-toolbar--compact .filter-btn { padding: 4px 8px; font-size: 10px; }
        .filter-btn--active { background: var(--accent); color: var(--accent-fg); }
        .logs-actions { display: flex; align-items: center; gap: 4px; flex-shrink: 0; }
        .icon-btn { width: 32px; height: 32px; border: 0.5px solid var(--border); border-radius: 8px; background: transparent; cursor: pointer; display: flex; align-items: center; justify-content: center; color: var(--text-3); transition: background 0.12s, border-color 0.12s, color 0.12s; flex-shrink: 0; }
        .logs-toolbar--compact .icon-btn { width: 28px; height: 28px; border-radius: 7px; }
        .icon-btn:hover { background: var(--bg-3); border-color: var(--text-3); color: var(--text); }
        .logs-list { flex: 1; min-height: 0; overflow-y: auto; padding: 6px 0; }
        .log-row { display: flex; align-items: baseline; gap: 8px; padding: 3px 10px; font-size: 12px; line-height: 1.45; }
        .logs-card--compact .log-row { padding: 3px 8px; font-size: 11px; gap: 6px; }
        .log-row:hover { background: var(--bg-2); }
        .log-time { color: var(--text-4); flex-shrink: 0; font-size: 11px; font-variant-numeric: tabular-nums; }
        .logs-card--compact .log-time { font-size: 10px; }
        .log-level { flex-shrink: 0; font-weight: 700; font-size: 11px; width: 40px; }
        .logs-card--compact .log-level { font-size: 10px; width: 36px; }
        .log-msg { flex: 1; word-break: break-all; color: var(--text); min-width: 0; }
        .log-count { flex-shrink: 0; background: var(--seg-bg); border-radius: 20px; padding: 1px 6px; font-size: 10px; color: var(--text-2); }
        .logs-empty { flex: 1; display: flex; align-items: center; justify-content: center; color: var(--text-4); font-size: 13px; }
      `}</style>
      <main className="logs-main">
        <div className={`logs-card${compact ? ' logs-card--compact' : ''}`}>
          <div className={`logs-toolbar${compact ? ' logs-toolbar--compact' : ''}`}>
            {!compact && (
              <div className="search-wrap">
                <div className="search-inner">
                  <input
                    className="search-input"
                    placeholder="Поиск...."
                    value={search}
                    onChange={e => setSearch(e.target.value)}
                  />
                  <IconSearch size={16} className="search-icon" />
                </div>
              </div>
            )}
            <div className="logs-toolbar-right">
              <div className="filter-group">
                {(['ALL', 'INFO', 'GO', 'STATUS', 'WARN', 'ERROR'] as Filter[]).map(f => (
                  <button key={f} className={`filter-btn${filter === f ? ' filter-btn--active' : ''}`} onClick={() => setFilter(f)}>{f}</button>
                ))}
              </div>
              <div className="logs-actions">
                <button className="icon-btn" onClick={logStore.clear} title="Очистить" aria-label="Очистить логи">
                  <IconTrashX stroke={2} size={14} />
                </button>
                <button className="icon-btn" onClick={handleCopy} title="Копировать" aria-label="Копировать логи">
                  <IconCopy stroke={2} size={14} />
                </button>
              </div>
            </div>
            {compact && (
              <div className="search-wrap">
                <div className="search-inner">
                  <input
                    className="search-input"
                    placeholder="Поиск...."
                    value={search}
                    onChange={e => setSearch(e.target.value)}
                  />
                  <IconSearch size={14} className="search-icon" />
                </div>
              </div>
            )}
          </div>

          {visible.length === 0 ? (
            <div className="logs-empty">{entries.length === 0 ? 'Логи появятся здесь...' : 'Ничего не найдено'}</div>
          ) : (
            <div className="logs-list" ref={listRef} onScroll={onScroll}>
              {visible.map(e => (
                <div key={e.id} className="log-row">
                  <span className="log-time">{e.time}</span>
                  <span className="log-level" style={{ color: LEVEL_COLOR[e.level] }}>{e.level}</span>
                  <span className="log-msg">{e.message}</span>
                  {e.count > 1 && <span className="log-count">×{e.count}</span>}
                </div>
              ))}
            </div>
          )}
        </div>
      </main>
    </>
  );
}