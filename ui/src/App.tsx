import { useState, useEffect, useCallback } from 'react';
import { VMLibrary } from './components/VMLibrary.tsx';
import { IPSWPanel } from './components/IPSWPanel.tsx';
import { XcodePanel } from './components/XcodePanel.tsx';
import { StoragePanel } from './components/StoragePanel.tsx';
import { GoldenPanel } from './components/GoldenPanel.tsx';
import { SettingsPanel } from './components/SettingsPanel.tsx';

type Tab = 'vms' | 'ipsw' | 'xcode' | 'storage' | 'golden' | 'settings';

const TABS: Array<{ id: Tab; label: string }> = [
  { id: 'vms',      label: 'VMs' },
  { id: 'ipsw',     label: 'macOS / IPSW' },
  { id: 'xcode',    label: 'Xcode' },
  { id: 'storage',  label: 'Storage' },
  { id: 'golden',   label: 'Golden Images' },
];

export default function App() {
  const [tab, setTab] = useState<Tab>('vms');
  const [lumeRunning, setLumeRunning] = useState<boolean | null>(null);
  const [lumeStarting, setLumeStarting] = useState(false);

  const checkLume = useCallback(async () => {
    try {
      const resp = await fetch('/api/host/lume-status');
      if (resp.ok) {
        const { running } = await resp.json() as { running: boolean };
        setLumeRunning(running);
      }
    } catch { /* backend itself down */ }
  }, []);

  // Poll lume status every 5s
  useEffect(() => {
    checkLume();
    const t = setInterval(checkLume, 5000);
    return () => clearInterval(t);
  }, [checkLume]);

  async function startLume() {
    setLumeStarting(true);
    try {
      await fetch('/api/host/lume-serve', { method: 'POST' });
      await checkLume();
    } finally {
      setLumeStarting(false);
    }
  }

  return (
    <div className="min-h-screen flex flex-col">
      {/* Header */}
      <header className="border-b border-gray-800 px-5 py-3 flex items-center gap-4">
        <div className="flex items-center gap-2.5">
          <span className="text-xl">🧚‍♀️</span>
          <span className="text-gray-100 font-semibold tracking-tight">virfield</span>
        </div>
        <nav className="flex gap-1 ml-4">
          {TABS.map(t => (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              className={`px-3 py-1.5 text-sm rounded transition-colors ${
                tab === t.id
                  ? 'bg-orange-500/20 text-orange-300 border border-orange-500/40'
                  : 'text-gray-400 hover:text-gray-200 hover:bg-gray-800'
              }`}
            >
              {t.label}
            </button>
          ))}
        </nav>

        {/* lume serve status indicator — right-aligned */}
        <div className="ml-auto flex items-center gap-3">
          {lumeRunning === false && (
            <div className="flex items-center gap-2">
              <span className="flex items-center gap-1.5 text-xs text-red-400">
                <span className="w-1.5 h-1.5 rounded-full bg-red-500 inline-block" />
                lume serve down
              </span>
              <button
                onClick={startLume}
                disabled={lumeStarting}
                className="text-xs px-2.5 py-1 bg-red-500/20 text-red-300 border border-red-500/40 rounded hover:bg-red-500/30 disabled:opacity-50"
              >
                {lumeStarting ? 'Starting…' : 'Start'}
              </button>
            </div>
          )}
          {lumeRunning === true && (
            <span className="flex items-center gap-1.5 text-xs text-gray-600">
              <span className="w-1.5 h-1.5 rounded-full bg-green-500 inline-block" />
              lume
            </span>
          )}
          <span className="text-xs text-gray-700">localhost:3000</span>

          {/* Settings gear — after status */}
          <button
            onClick={() => setTab('settings')}
            className={`px-2.5 py-1.5 text-sm rounded transition-colors ${
              tab === 'settings'
                ? 'bg-orange-500/20 text-orange-300 border border-orange-500/40'
                : 'text-gray-500 hover:text-gray-300 hover:bg-gray-800'
            }`}
            title="Settings"
          >
            ⚙
          </button>
        </div>
      </header>

      {/* lume down banner */}
      {lumeRunning === false && (
        <div className="bg-red-950/40 border-b border-red-900/50 px-5 py-2 flex items-center justify-between">
          <span className="text-xs text-red-300">
            lume serve is not running — VM operations will fail until it's started.
          </span>
          <button
            onClick={startLume}
            disabled={lumeStarting}
            className="text-xs px-3 py-1 bg-red-500/20 text-red-300 border border-red-500/40 rounded hover:bg-red-500/30 disabled:opacity-50"
          >
            {lumeStarting ? 'Starting…' : 'Start lume serve'}
          </button>
        </div>
      )}

      {/* Main content */}
      <main className="flex-1 overflow-auto p-5">
        {tab === 'vms'      && <VMLibrary />}
        {tab === 'ipsw'     && <IPSWPanel />}
        {tab === 'xcode'    && <XcodePanel />}
        {tab === 'storage'  && <StoragePanel />}
        {tab === 'golden'   && <GoldenPanel />}
        {tab === 'settings' && <SettingsPanel />}
      </main>
    </div>
  );
}
