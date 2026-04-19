import { useState, useEffect } from 'react';
import { useGet, apiPost, apiDelete } from '../hooks/useAPI.ts';

interface Settings {
  github_username: string;
  github_token: string;
  github_token_set: boolean;
  scripts_dir: string;
  log_base: string;
  state_dir: string;
  recordings_dir: string;
  vmshare: string;
  repo_dir: string;
}

interface GhcrSource {
  id: string;
  name: string;
  registry: string;
  organization: string;
  is_default: number;
}

interface LumeStatus {
  running: boolean;
  processAlive: boolean;
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mb-6">
      <h3 className="text-xs font-semibold text-gray-400 uppercase tracking-wider mb-3 pb-1 border-b border-gray-800">
        {title}
      </h3>
      {children}
    </div>
  );
}

function Field({
  label, value, onChange, type = 'text', placeholder, hint, masked,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  type?: string;
  placeholder?: string;
  hint?: string;
  masked?: boolean;
}) {
  const [show, setShow] = useState(false);
  return (
    <div className="mb-3">
      <label className="block text-xs text-gray-400 mb-1">{label}</label>
      <div className="flex gap-2">
        <input
          type={masked && !show ? 'password' : type}
          value={value}
          onChange={e => onChange(e.target.value)}
          placeholder={placeholder}
          className="flex-1 bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60 font-mono"
        />
        {masked && (
          <button
            onClick={() => setShow(s => !s)}
            className="text-xs px-2 py-1 bg-gray-800 border border-gray-700 rounded text-gray-500 hover:text-gray-300"
          >
            {show ? 'Hide' : 'Show'}
          </button>
        )}
      </div>
      {hint && <p className="mt-1 text-[10px] text-gray-600">{hint}</p>}
    </div>
  );
}

export function SettingsPanel() {
  const { data: settings, loading, refresh: refreshSettings } = useGet<Settings>('/settings');
  const { data: sources, refresh: refreshSources } = useGet<GhcrSource[]>('/ghcr/sources');
  const { data: lumeStatus, refresh: refreshLume } = useGet<LumeStatus>('/host/lume-status', [], 5000);

  const [form, setForm] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [lumeAction, setLumeAction] = useState<string | null>(null);
  const [serverRestarting, setServerRestarting] = useState(false);

  // New GHCR source form
  const [newSrc, setNewSrc] = useState({ name: '', registry: 'ghcr.io', organization: '', isDefault: false });
  const [addingSrc, setAddingSrc] = useState(false);

  useEffect(() => {
    if (!settings) return;
    setForm({
      github_username: settings.github_username ?? '',
      github_token: '',          // never pre-fill token — user must type to change
      scripts_dir: settings.scripts_dir ?? '',
      log_base: settings.log_base ?? '',
      state_dir: settings.state_dir ?? '',
      recordings_dir: settings.recordings_dir ?? '',
      vmshare: settings.vmshare ?? '',
      repo_dir: settings.repo_dir ?? '',
    });
  }, [settings]);

  function set(key: string, value: string) {
    setForm(f => ({ ...f, [key]: value }));
  }

  async function save() {
    setSaving(true);
    setSaved(false);
    try {
      const payload: Record<string, string> = { ...form };
      // Don't send empty token — that would clear it
      if (!payload.github_token) delete payload.github_token;
      await apiPost('/settings', payload);
      setSaved(true);
      setTimeout(() => setSaved(false), 2500);
      refreshSettings();
    } finally {
      setSaving(false);
    }
  }

  async function clearToken() {
    await apiDelete('/settings/github_token');
    refreshSettings();
  }

  async function restartServer() {
    setServerRestarting(true);
    try {
      await apiPost('/host/server-restart');
    } catch { /* expected — server dies before response */ }
    // Poll until the server is back up
    const start = Date.now();
    while (Date.now() - start < 15000) {
      await new Promise(r => setTimeout(r, 800));
      try {
        const resp = await fetch('/api/settings');
        if (resp.ok) { setServerRestarting(false); refreshSettings(); return; }
      } catch { /* not up yet */ }
    }
    setServerRestarting(false);
  }

  async function lumeAction_(action: 'start' | 'stop' | 'restart') {
    setLumeAction(action);
    try {
      const endpoint = action === 'start' ? '/host/lume-serve'
        : action === 'stop'    ? '/host/lume-stop'
        : '/host/lume-restart';
      await apiPost(endpoint);
      await refreshLume();
    } finally {
      setLumeAction(null);
    }
  }

  async function addSource() {
    if (!newSrc.name || !newSrc.organization) return;
    setAddingSrc(true);
    try {
      await apiPost('/ghcr/sources', newSrc);
      setNewSrc({ name: '', registry: 'ghcr.io', organization: '', isDefault: false });
      refreshSources();
    } finally {
      setAddingSrc(false);
    }
  }

  async function removeSource(id: string) {
    await apiDelete(`/ghcr/sources/${id}`);
    refreshSources();
  }

  async function setDefaultSource(id: string) {
    await apiPost(`/ghcr/sources/${id}/default`);
    refreshSources();
  }

  if (loading) return <div className="text-gray-600 text-sm py-12 text-center">Loading settings…</div>;

  const lumeUp = lumeStatus?.running ?? false;

  return (
    <div className="max-w-2xl">
      {/* ── lume serve ── */}
      <Section title="lume serve">
        <div className="flex items-center gap-3 mb-3">
          <span className={`flex items-center gap-1.5 text-xs ${lumeUp ? 'text-green-400' : 'text-red-400'}`}>
            <span className={`w-2 h-2 rounded-full inline-block ${lumeUp ? 'bg-green-500' : 'bg-red-500'}`} />
            {lumeUp ? 'Running' : 'Stopped'}
          </span>
          <span className="text-xs text-gray-600">localhost:7777</span>
        </div>
        <div className="flex gap-2">
          <button
            onClick={() => lumeAction_('start')}
            disabled={lumeAction !== null || lumeUp}
            className="text-xs px-3 py-1.5 bg-green-500/20 text-green-300 border border-green-500/40 rounded hover:bg-green-500/30 disabled:opacity-40"
          >
            {lumeAction === 'start' ? 'Starting…' : 'Start'}
          </button>
          <button
            onClick={() => lumeAction_('restart')}
            disabled={lumeAction !== null}
            className="text-xs px-3 py-1.5 bg-orange-500/20 text-orange-300 border border-orange-500/40 rounded hover:bg-orange-500/30 disabled:opacity-40"
          >
            {lumeAction === 'restart' ? 'Restarting…' : 'Restart'}
          </button>
          <button
            onClick={() => lumeAction_('stop')}
            disabled={lumeAction !== null || !lumeUp}
            className="text-xs px-3 py-1.5 bg-red-500/20 text-red-300 border border-red-500/40 rounded hover:bg-red-500/30 disabled:opacity-40"
          >
            {lumeAction === 'stop' ? 'Stopping…' : 'Stop'}
          </button>
        </div>
        <p className="mt-2 text-[10px] text-gray-600">
          lume serve runs as a detached process — it survives server restarts and keeps VMs alive.
          Stop only when you want to shut down all VM management.
        </p>
      </Section>

      {/* ── Backend server ── */}
      <Section title="Backend Server">
        <div className="flex items-center gap-3 mb-3">
          <span className="flex items-center gap-1.5 text-xs text-green-400">
            <span className="w-2 h-2 rounded-full inline-block bg-green-500" />
            {serverRestarting ? 'Restarting…' : 'Running'}
          </span>
          <span className="text-xs text-gray-600">localhost:3000</span>
        </div>
        <button
          onClick={restartServer}
          disabled={serverRestarting}
          className="text-xs px-3 py-1.5 bg-orange-500/20 text-orange-300 border border-orange-500/40 rounded hover:bg-orange-500/30 disabled:opacity-40"
        >
          {serverRestarting ? 'Restarting…' : 'Restart Backend'}
        </button>
        <p className="mt-2 text-[10px] text-gray-600">
          Restarts the Node.js API server (tsx watch respawns it automatically).
          Use this to apply config changes or recover from a DB error.
          lume serve and running VMs are unaffected.
        </p>
      </Section>

      {/* ── GitHub credentials ── */}
      <Section title="GitHub Credentials (GHCR)">
        <Field
          label="GitHub Username"
          value={form.github_username ?? ''}
          onChange={v => set('github_username', v)}
          placeholder="e.g. your-github-username"
        />
        <div className="mb-3">
          <div className="flex items-center gap-2 mb-1">
            <label className="text-xs text-gray-400">GitHub Token (PAT)</label>
            {settings?.github_token_set ? (
              <span className="flex items-center gap-1 text-[10px] text-green-400 bg-green-900/20 border border-green-700/40 rounded px-1.5 py-0.5">
                <span className="w-1.5 h-1.5 rounded-full bg-green-500 inline-block" />
                saved
              </span>
            ) : (
              <span className="text-[10px] text-gray-600">not set</span>
            )}
          </div>
          <div className="flex gap-2">
            <input
              type="password"
              value={form.github_token ?? ''}
              onChange={e => set('github_token', e.target.value)}
              placeholder={settings?.github_token_set ? 'Enter new token to replace…' : 'ghp_…'}
              className="flex-1 bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-200 placeholder-gray-500 focus:outline-none focus:border-orange-500/60 font-mono"
            />
            {settings?.github_token_set && (
              <button
                onClick={clearToken}
                className="text-xs px-2 py-1 bg-red-900/20 border border-red-800/40 rounded text-red-400/80 hover:text-red-300 hover:bg-red-900/30 whitespace-nowrap"
              >
                Clear
              </button>
            )}
          </div>
          <p className="mt-1 text-[10px] text-gray-600">
            Requires <code className="font-mono">write:packages</code> + <code className="font-mono">read:packages</code> + <code className="font-mono">repo</code> scopes.
            {settings?.github_token_set && ' Leave blank to keep existing token.'}
          </p>
        </div>
      </Section>

      {/* ── Folder paths ── */}
      <Section title="Folder Paths">
        <Field label="Scripts Dir" value={form.scripts_dir ?? ''} onChange={v => set('scripts_dir', v)}
          hint="build-golden-vm.sh + phase scripts" />
        <Field label="Log Base" value={form.log_base ?? ''} onChange={v => set('log_base', v)}
          hint="Root for per-build log dirs and state.json files" />
        <Field label="State Dir" value={form.state_dir ?? ''} onChange={v => set('state_dir', v)} />
        <Field label="Recordings Dir" value={form.recordings_dir ?? ''} onChange={v => set('recordings_dir', v)} />
        <Field label="VMShare" value={form.vmshare ?? ''} onChange={v => set('vmshare', v)}
          hint="virtiofs shared volume (~/VMShare)" />
        <Field label="Repo Dir" value={form.repo_dir ?? ''} onChange={v => set('repo_dir', v)}
          hint="Git repo containing build-for-testing.sh" />
      </Section>

      {/* Save button */}
      <div className="flex items-center gap-3 mb-8">
        <button
          onClick={save}
          disabled={saving}
          className="text-xs px-4 py-2 bg-orange-500/20 text-orange-300 border border-orange-500/40 rounded hover:bg-orange-500/30 disabled:opacity-50"
        >
          {saving ? 'Saving…' : 'Save Settings'}
        </button>
        {saved && <span className="text-xs text-green-400">✓ Saved</span>}
      </div>

      {/* ── GHCR Sources ── */}
      <Section title="GHCR Package Sources">
        {(sources ?? []).length === 0 && (
          <p className="text-xs text-gray-600 mb-3">No sources configured. Add one below.</p>
        )}
        <div className="space-y-2 mb-4">
          {(sources ?? []).map(src => (
            <div key={src.id} className={`flex items-center justify-between px-3 py-2 rounded border text-xs ${
              src.is_default ? 'border-orange-600/40 bg-orange-900/10' : 'border-gray-800 bg-gray-900/40'
            }`}>
              <div>
                <span className="text-gray-200 font-medium">{src.name}</span>
                <span className="ml-2 text-gray-500 font-mono">{src.registry}/{src.organization}</span>
                {src.is_default === 1 && (
                  <span className="ml-2 text-[10px] text-orange-400 border border-orange-500/40 rounded px-1">default</span>
                )}
              </div>
              <div className="flex gap-2">
                {src.is_default !== 1 && (
                  <button
                    onClick={() => setDefaultSource(src.id)}
                    className="text-[10px] text-gray-500 hover:text-orange-300"
                  >
                    Set default
                  </button>
                )}
                <button
                  onClick={() => removeSource(src.id)}
                  className="text-[10px] text-red-500/60 hover:text-red-400"
                >
                  Remove
                </button>
              </div>
            </div>
          ))}
        </div>

        {/* Add new source */}
        <div className="border border-gray-800 rounded p-3 bg-gray-900/40">
          <p className="text-[10px] text-gray-500 uppercase tracking-wider mb-2">Add source</p>
          <div className="grid grid-cols-2 gap-2 mb-2">
            <div>
              <label className="block text-[10px] text-gray-500 mb-1">Name</label>
              <input
                value={newSrc.name}
                onChange={e => setNewSrc(s => ({ ...s, name: e.target.value }))}
                placeholder="e.g. My Personal"
                className="w-full bg-gray-800 border border-gray-700 rounded px-2 py-1 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60"
              />
            </div>
            <div>
              <label className="block text-[10px] text-gray-500 mb-1">Registry</label>
              <input
                value={newSrc.registry}
                onChange={e => setNewSrc(s => ({ ...s, registry: e.target.value }))}
                placeholder="ghcr.io"
                className="w-full bg-gray-800 border border-gray-700 rounded px-2 py-1 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60"
              />
            </div>
            <div className="col-span-2">
              <label className="block text-[10px] text-gray-500 mb-1">Organization / User</label>
              <input
                value={newSrc.organization}
                onChange={e => setNewSrc(s => ({ ...s, organization: e.target.value }))}
                placeholder="e.g. your-username or your-org"
                className="w-full bg-gray-800 border border-gray-700 rounded px-2 py-1 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60"
              />
            </div>
          </div>
          <div className="flex items-center justify-between">
            <label className="flex items-center gap-1.5 text-xs text-gray-400 cursor-pointer">
              <input
                type="checkbox"
                checked={newSrc.isDefault}
                onChange={e => setNewSrc(s => ({ ...s, isDefault: e.target.checked }))}
                className="accent-orange-500"
              />
              Set as default
            </label>
            <button
              onClick={addSource}
              disabled={addingSrc || !newSrc.name || !newSrc.organization}
              className="text-xs px-3 py-1.5 bg-orange-500/20 text-orange-300 border border-orange-500/40 rounded hover:bg-orange-500/30 disabled:opacity-40"
            >
              {addingSrc ? 'Adding…' : '+ Add Source'}
            </button>
          </div>
        </div>
      </Section>
    </div>
  );
}
