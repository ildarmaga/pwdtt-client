import TurnSettings from '../components/TurnSettings';

export default function Turn() {
  return <main className="turn-page">
    <style>{`
      .turn-page { display:flex; flex-direction:column; flex:1; min-height:0; padding:14px 16px 12px; color:var(--text); font-size:13px; }
      .turn-page h1 { font-size:16px; margin:0 0 10px; font-weight:600; }
      .turn-page .st-vk-block { display:flex; flex-direction:column; flex:1; min-height:0; }
      .turn-list { flex:1; min-height:0; overflow:auto; padding-right:7px; scrollbar-width:thin; scrollbar-color:color-mix(in srgb,var(--accent) 55%,transparent) var(--surface); }
      .turn-list::-webkit-scrollbar { width:5px; height:5px; }
      .turn-list::-webkit-scrollbar-track { background:var(--surface); border-radius:8px; }
      .turn-list::-webkit-scrollbar-thumb { background:color-mix(in srgb,var(--accent) 55%,transparent); border-radius:8px; }
      .turn-list::-webkit-scrollbar-thumb:hover { background:var(--accent); }
      .turn-list::-webkit-scrollbar-button { display:none; height:0; width:0; }
      .turn-pool { border:1px solid var(--border); border-radius:9px; margin-bottom:7px; padding:8px 10px; background:var(--surface); }
      .turn-page .st-row { display:flex; align-items:center; justify-content:space-between; gap:8px; min-height:30px; }
      .turn-page .st-row > div > span { font-size:13px; font-weight:600; }
      .turn-page .st-row > div > div { font-size:10px !important; margin-top:2px; }
      .turn-page .st-row > span { font-size:11px !important; white-space:nowrap; }
      .turn-page .st-section-title { display:none; }
      .turn-page .st-toggle { width:34px; height:20px; border:0; border-radius:20px; cursor:pointer; position:relative; flex-shrink:0; background:var(--border); }
      .turn-page .st-toggle::after { content:''; position:absolute; top:3px; left:3px; width:14px; height:14px; border-radius:50%; background:var(--text); transition:transform .15s; }
      .turn-page .st-toggle--on { background:var(--accent); }
      .turn-page .st-toggle--on::after { transform:translateX(14px); background:var(--accent-fg); }
      .turn-page button:disabled { opacity:.5; cursor:wait; }
      .turn-page .st-hash-btn { flex:1; border:1px solid var(--border); border-radius:8px; padding:8px; min-height:34px; font-size:12px; background:var(--surface); color:var(--text); cursor:pointer; }
      .turn-page .st-hash-btn:last-child { background:var(--accent); color:var(--accent-fg); }
      .turn-address { display:flex; align-items:center; justify-content:space-between; gap:8px; padding:6px 0; border-top:1px solid var(--border); font-size:11px; }
      .turn-address > span { white-space:nowrap; }
      .turn-address .turn-connected { color:var(--accent); font-size:10px; white-space:normal; }
      .turn-address:first-of-type { margin-top:5px; }
      .turn-address input { appearance:none; -webkit-appearance:none; width:14px; height:14px; margin:0; border:1px solid var(--text-3); border-radius:4px; background:var(--input-bg); flex-shrink:0; cursor:pointer; }
      .turn-address input:checked { background:var(--accent); border-color:var(--accent); }
      .turn-address input:checked::after { content:''; display:block; width:4px; height:7px; border:solid var(--accent-fg); border-width:0 2px 2px 0; transform:translate(4px,1px) rotate(45deg); }
      .turn-address input:focus-visible, .turn-page button:focus-visible { outline:2px solid var(--accent); outline-offset:3px; }
      .turn-address input:disabled { opacity:.5; cursor:default; }
      .turn-page .st-vk-msg { font-size:12px; margin-top:8px; color:var(--text-3); }
      @media(max-width:480px) { .turn-page { padding:12px; } }
    `}</style>
    <h1>TURN-серверы</h1>
    <TurnSettings />
  </main>;
}
