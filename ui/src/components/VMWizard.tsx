/**
 * New VM Wizard — two modes:
 *
 * 1. "Build Golden VM" — runs build-golden-vm.sh (all 4 phases) and polls
 *    state.json for live progress. Produces: base VM → nosip VM → golden VM.
 *
 * 2. "Clone from Golden" — instant APFS CoW clone from an existing golden VM.
 *    Suitable for ephemeral run VMs.
 */

import { useState, useEffect, useRef, useMemo, useCallback } from 'react';
import { apiPost, useIPSW, useIPSWCatalog, useXcode, useVMs, useProvisionTools, BuildState, BuildStageInfo, IPSWCatalogEntry } from '../hooks/useAPI.ts';

const STAGE_ORDER = ['download_ipsw', 'create_vm', 'setup_assistant', 'disable_sip', 'provision_vm'] as const;
const STAGE_LABELS: Record<string, string> = {
  download_ipsw:   'Download macOS IPSW',
  create_vm:       'Create base VM',
  setup_assistant: 'Setup Assistant',
  disable_sip:     'Disable SIP',
  provision_vm:    'Install Xcode & tools',
};
// state.json stage key → UI stage key
const STATE_KEY_MAP: Record<string, string> = {
  '00-download-ipsw':   'download_ipsw',
  '01-create-vm':       'create_vm',
  '02-setup-assistant': 'setup_assistant',
  '03-disable-sip':     'disable_sip',
  '04-provision-vm':    'provision_vm',
};

// ── Name suggestion helpers ───────────────────────────────────────────────────

const WORD_ADJS  = ['swift', 'bright', 'quiet', 'bold', 'cool', 'deep', 'keen', 'calm', 'wide', 'fair', 'sharp', 'dark', 'warm', 'pale', 'rich'];
const WORD_NOUNS = ['cedar', 'ridge', 'brook', 'pine', 'oak', 'lake', 'peak', 'grove', 'vale', 'ford', 'stone', 'cliff', 'field', 'crest', 'dawn'];

function randomGoldenName(existing: Set<string>): string {
  for (let i = 0; i < 100; i++) {
    const a = WORD_ADJS[Math.floor(Math.random() * WORD_ADJS.length)];
    const n = WORD_NOUNS[Math.floor(Math.random() * WORD_NOUNS.length)];
    const name = `${a}-${n}-golden`;
    if (!existing.has(name)) return name;
  }
  return `golden-${Date.now()}`;
}

const SPEC_TO_MAJOR: Record<string, string> = {
  tahoe: '26', sequoia: '15', sonoma: '14', ventura: '13', monterey: '12',
};

/**
 * Extract/derive macOS version string for VM name suggestion.
 * Handles: local IPSW paths, version specs (sequoia/15/15.4), "latest"
 */
function ipswVersion(path: string | null): string | null {
  if (!path || path === 'latest') return null;
  // Named spec: sequoia → "15"
  if (SPEC_TO_MAJOR[path.toLowerCase()]) return SPEC_TO_MAJOR[path.toLowerCase()];
  // Numeric spec: "15" or "15.4.1"
  if (/^\d+(\.\d+)*$/.test(path)) return path;
  // Local IPSW path — extract from filename
  const filename = path.split('/').pop() ?? '';
  const m = filename.match(/[_\-](\d+\.\d+(?:\.\d+)?)[_\-]/);
  return m?.[1] ?? null;
}

// ── Status dot ────────────────────────────────────────────────────────────────

function statusDot(s: string) {
  if (s === 'done')    return <span className="text-green-400 text-sm">✓</span>;
  if (s === 'failed')  return <span className="text-red-400 text-sm">✗</span>;
  if (s === 'running') return <span className="text-yellow-400 animate-pulse text-sm">⟳</span>;
  if (s === 'skipped') return <span className="text-gray-600 text-sm">—</span>;
  return <span className="text-gray-600 text-sm">○</span>;
}

interface Props {
  onClose: () => void;
  onCreated: () => void;
}

// ── Build Golden wizard ────────────────────────────────────────────────────────

function BuildGoldenWizard({ onClose, onCreated }: Props) {
  const { data: ipswInfo, refresh: refreshIpsw } = useIPSW();
  const { data: ipswCatalog } = useIPSWCatalog();
  const { data: xcodeInfo } = useXcode();
  const { data: vms } = useVMs();
  const { data: provisionTools } = useProvisionTools();

  const existingNames = useMemo(() => new Set((vms ?? []).map(v => v.name)), [vms]);

  // ── Step 1: configure ──────────────────────────────────────────────────────
  const [selectedIpsw, setSelectedIpsw] = useState<string | null>(null);
  const [selectedXcode, setSelectedXcode] = useState<string | null>(null);
  const [goldenVm, setGoldenVm] = useState('');
  const [nameTouched, setNameTouched] = useState(false);
  const [selectedToolIds, setSelectedToolIds] = useState<Set<string>>(new Set());
  const [toolsInited, setToolsInited] = useState(false);
  const [downloadingIpswSpec, setDownloadingIpswSpec] = useState<string | null>(null);
  const [downloadIpswStatus, setDownloadIpswStatus] = useState<string | null>(null);
  const [record, setRecord] = useState(false);
  const [openVnc, setOpenVnc] = useState(false);
  const [installMissing, setInstallMissing] = useState(true);
  const vncOpenedRef = useRef<Set<string>>(new Set());
  const [cpu, setCpu] = useState('');
  const [memory, setMemory] = useState('');
  const [disk, setDisk] = useState('');

  // ── Step 2: building ───────────────────────────────────────────────────────
  const [building, setBuilding] = useState(false);
  const [buildError, setBuildError] = useState<string | null>(null);
  const [buildState, setBuildState] = useState<BuildState | null>(null);
  const [buildLog, setBuildLog] = useState<string>('');
  const [showLog, setShowLog] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const logRef = useRef<HTMLPreElement | null>(null);

  // Pre-select all tools once the list loads
  useEffect(() => {
    if (toolsInited || !provisionTools?.length) return;
    setSelectedToolIds(new Set(provisionTools.map(t => t.id)));
    setToolsInited(true);
  }, [provisionTools, toolsInited]);

  // Auto-suggest golden VM name based on IPSW selection
  useEffect(() => {
    if (nameTouched) return;
    const ver = ipswVersion(selectedIpsw);
    setGoldenVm(ver ? `macos-${ver}-golden` : randomGoldenName(existingNames));
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedIpsw, existingNames, nameTouched]);

  const nameConflict = goldenVm.length > 0 && existingNames.has(goldenVm);

  // ── Tools helpers ──────────────────────────────────────────────────────────
  function toggleTool(id: string) {
    setSelectedToolIds(prev => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }

  const allSelected = !!provisionTools?.length && selectedToolIds.size === provisionTools.length;
  const toolsArg = allSelected || !provisionTools?.length ? 'all' : Array.from(selectedToolIds).join(',');

  // ── IPSW download ──────────────────────────────────────────────────────────
  async function downloadIpsw(entry: IPSWCatalogEntry) {
    setDownloadingIpswSpec(entry.spec);
    setDownloadIpswStatus(null);
    try {
      const result = await apiPost('/ipsw/download', { url: entry.url }) as { message: string; name: string; destPath: string };
      setDownloadIpswStatus(`Download started: ${result.name}`);
      // Poll every 10s for the file to appear in local list, up to 3h
      let attempts = 0;
      const poll = setInterval(async () => {
        attempts++;
        await refreshIpsw();
        if (attempts >= 1080) clearInterval(poll);
      }, 10000);
    } catch (err) {
      setDownloadIpswStatus(`Error: ${String(err)}`);
    } finally {
      setDownloadingIpswSpec(null);
    }
  }

  // ── Build ──────────────────────────────────────────────────────────────────
  function mergedPhases(): Record<string, BuildStageInfo> {
    const base: Record<string, BuildStageInfo> = Object.fromEntries(
      STAGE_ORDER.map(k => [k, { status: 'pending' as const }])
    );
    if (buildState?.stages) {
      for (const [stateKey, info] of Object.entries(buildState.stages)) {
        const key = STATE_KEY_MAP[stateKey];
        if (key) base[key] = info as BuildStageInfo;
      }
    }
    return base;
  }

  const pollBuildState = useCallback(async () => {
    try {
      const [stateResp, logResp] = await Promise.all([
        fetch(`/api/vms/${encodeURIComponent(goldenVm)}/build-state`),
        fetch(`/api/vms/${encodeURIComponent(goldenVm)}/build-log?lines=40`),
      ]);
      if (stateResp.ok) {
        const state = await stateResp.json() as BuildState;
        setBuildState(state);
        if (state.status === 'done' || state.status === 'failed') {
          if (pollRef.current) clearInterval(pollRef.current);
          if (state.status === 'done') onCreated();
        }
      }
      if (logResp.ok) {
        const text = await logResp.text();
        setBuildLog(text);
        // Auto-scroll log to bottom
        setTimeout(() => {
          if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
        }, 0);
      }
    } catch { /* transient */ }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [goldenVm, onCreated]);

  async function startBuild() {
    setBuildError(null);
    setBuilding(true);
    try {
      await apiPost('/vms/build-golden', {
        ipsw: selectedIpsw ?? 'latest',
        xcode: selectedXcode ?? undefined,
        tools: toolsArg,
        record,
        installMissing,
        goldenVm,
        ...(cpu    ? { cpu: Number(cpu) } : {}),
        ...(memory ? { memory }          : {}),
        ...(disk   ? { disk }            : {}),
      });
      pollRef.current = setInterval(pollBuildState, 3000);
      pollBuildState();
    } catch (err) {
      setBuildError(String(err));
      setBuilding(false);
    }
  }

  useEffect(() => {
    return () => { if (pollRef.current) clearInterval(pollRef.current); };
  }, []);

  // Base VM name is deterministic from goldenVm (strip -golden, add -base)
  const baseVmName = goldenVm.replace(/-golden$/, '') + '-base';

  async function openVNC(vmName = baseVmName) {
    try {
      const resp = await fetch(`/api/vms/${encodeURIComponent(vmName)}`);
      if (!resp.ok) return;
      const vm = await resp.json() as { vncUrl?: string | null; ipAddress?: string | null };
      const url = vm.vncUrl ?? (vm.ipAddress ? `vnc://lume@${vm.ipAddress}` : null);
      if (url) window.open(url, '_blank');
    } catch { /* ignore */ }
  }

  // Auto-open Screen Sharing when a VNC phase starts running (if option enabled)
  const prevPhasesRef = useRef<Record<string, string>>({});
  useEffect(() => {
    if (!openVnc || !buildState?.stages) return;
    const VNC_PHASES: Record<string, string> = {
      '02-setup-assistant': baseVmName,
      '03-disable-sip':     goldenVm.replace(/-golden$/, '') + '-nosip',
    };
    for (const [stateKey, vmName] of Object.entries(VNC_PHASES)) {
      const stage = buildState.stages[stateKey];
      const prev = prevPhasesRef.current[stateKey];
      if (stage?.status === 'running' && prev !== 'running' && !vncOpenedRef.current.has(stateKey)) {
        vncOpenedRef.current.add(stateKey);
        openVNC(vmName);
      }
      prevPhasesRef.current[stateKey] = stage?.status ?? '';
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [buildState, openVnc]);

  // ── Build progress view ────────────────────────────────────────────────────
  if (building) {
    const phases = mergedPhases();
    const overallStatus = buildState?.status;

    return (
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-gray-200">Building <span className="text-orange-300">{goldenVm}</span></h3>
          <div className="flex items-center gap-2">
            <button
              onClick={openVNC}
              className="text-[10px] px-2 py-1 bg-purple-600/20 text-purple-300 border border-purple-600/40 rounded hover:bg-purple-600/30"
              title={`Open Screen Sharing to ${baseVmName}`}
            >
              Screen Sharing
            </button>
            {overallStatus && (
              <span className={`text-xs ${overallStatus === 'done' ? 'text-green-400' : overallStatus === 'failed' ? 'text-red-400' : 'text-yellow-400'}`}>
                {overallStatus}
              </span>
            )}
          </div>
        </div>

        {buildError && (
          <div className="text-red-300 text-xs bg-red-900/20 border border-red-800 rounded p-2">{buildError}</div>
        )}

        <div className="space-y-2">
          {STAGE_ORDER.map(key => {
            const info = phases[key];
            // Use label from state.json if available, fall back to our constant
            const label = info.label ?? STAGE_LABELS[key];
            // substage: from the stage info, or top-level if this is the current running stage
            const substage = info.substage ?? (info.status === 'running' ? buildState?.substage : undefined);
            const percent = info.status === 'running' && key === 'download_ipsw' ? buildState?.percent : null;
            return (
              <div key={key} className={`border rounded text-xs ${
                info.status === 'done'    ? 'border-green-800 bg-green-900/20' :
                info.status === 'failed'  ? 'border-red-800 bg-red-900/20' :
                info.status === 'running' ? 'border-yellow-800 bg-yellow-900/20' :
                'border-gray-800 bg-gray-900/20'
              }`}>
                <div className="flex items-center gap-3 px-3 py-2">
                  {statusDot(info.status)}
                  <span className="flex-1">{label}</span>
                  {info.started && (
                    <span className="text-gray-600 text-[10px]">
                      {new Date(info.started).toLocaleTimeString()}
                      {info.finished && ` → ${new Date(info.finished).toLocaleTimeString()}`}
                    </span>
                  )}
                </div>
                {/* Progress bar for download stage */}
                {percent != null && (
                  <div className="px-3 pb-2 space-y-1">
                    <div className="h-1.5 bg-gray-800 rounded overflow-hidden">
                      <div className="h-full bg-blue-500 rounded transition-all" style={{ width: `${percent}%` }} />
                    </div>
                    {substage && <div className="text-[10px] text-gray-500">{substage}</div>}
                  </div>
                )}
                {/* Substage text for non-download stages */}
                {percent == null && substage && info.status === 'running' && (
                  <div className="px-3 pb-2 text-[10px] text-yellow-400/70 italic">{substage}</div>
                )}
                {info.status === 'failed' && info.error && (
                  <div className="px-3 pb-2">
                    <pre className="text-[10px] text-red-400/80 whitespace-pre-wrap font-mono leading-tight bg-red-950/40 rounded p-1.5 max-h-24 overflow-y-auto">{info.error}</pre>
                  </div>
                )}
              </div>
            );
          })}
        </div>

        {/* VM network info once available */}
        {(buildState?.hostname || buildState?.ip) && (
          <div className="text-[10px] text-cyan-700 flex gap-3">
            {buildState.hostname && <span>{buildState.hostname}</span>}
            {buildState.ip && <span>{buildState.ip}</span>}
          </div>
        )}

        {/* Build log */}
        <div>
          <button
            onClick={() => setShowLog(v => !v)}
            className="text-[10px] text-gray-600 hover:text-gray-400 flex items-center gap-1"
          >
            {showLog ? '▼' : '▶'} build log {buildLog ? `(${buildLog.split('\n').filter(Boolean).length} lines)` : ''}
          </button>
          {showLog && (
            <pre
              ref={logRef}
              className="mt-1 text-[10px] text-gray-400 bg-black/50 border border-gray-800 rounded p-2 max-h-48 overflow-y-auto whitespace-pre-wrap"
            >
              {buildLog || '(no log yet)'}
            </pre>
          )}
        </div>

        {(buildState?.log ?? buildState?.log_dir) && (
          <div className="text-[10px] text-gray-600">Log: {buildState.log ?? buildState.log_dir}</div>
        )}
        {buildState?.recordings?.length ? (
          <div className="text-[10px] text-cyan-700">{buildState.recordings.length} recording{buildState.recordings.length > 1 ? 's' : ''} saved</div>
        ) : null}

        <div className="flex justify-end gap-2 pt-2 border-t border-gray-800">
          <button onClick={onClose} className="text-xs px-3 py-1.5 text-gray-400 hover:text-gray-200">
            {overallStatus === 'done' ? 'Close' : 'Close (build continues in background)'}
          </button>
        </div>
      </div>
    );
  }

  // ── Config form ────────────────────────────────────────────────────────────
  return (
    <div className="space-y-4">
      <h3 className="text-sm font-semibold text-gray-200">Build Golden VM</h3>
      <p className="text-xs text-gray-500">
        Runs the full 4-phase pipeline via <code className="text-gray-400">build-golden-vm.sh</code>. Takes 1–2 hours.
      </p>

      {/* IPSW picker */}
      <div className="space-y-1.5">
        <label className="text-xs text-gray-400">macOS IPSW</label>

        {downloadIpswStatus && (
          <p className={`text-[10px] ${downloadIpswStatus.startsWith('Error') ? 'text-red-400' : 'text-cyan-400/80'}`}>
            {downloadIpswStatus}
          </p>
        )}

        <div className="space-y-1">
          {/* Local files — already downloaded */}
          {(ipswInfo?.localFiles.length ?? 0) > 0 && (
            <div className="text-[10px] text-gray-600 uppercase tracking-wider pt-0.5 pb-0.5">Local files</div>
          )}
          {ipswInfo?.localFiles.map(f => (
            <button
              key={f.path}
              onClick={() => setSelectedIpsw(f.path)}
              className={`w-full text-left px-3 py-2 rounded text-xs border transition-colors ${
                selectedIpsw === f.path
                  ? 'border-orange-500/60 bg-orange-500/10 text-orange-200'
                  : 'border-gray-700 text-gray-400 hover:border-gray-600'
              }`}
            >
              {f.name} <span className="text-gray-600">({(f.size / 1e9).toFixed(1)} GB)</span>
            </button>
          ))}

          {/* Catalog from api.ipsw.me — version specs auto-download at build time */}
          {ipswCatalog && ipswCatalog.length > 0 && (
            <div className="text-[10px] text-gray-600 uppercase tracking-wider pt-1 pb-0.5">Available (api.ipsw.me)</div>
          )}
          {ipswCatalog?.map(entry => {
            const isSelected = selectedIpsw === entry.spec;
            const isDownloading = downloadingIpswSpec === entry.spec;
            // Already have local copy?
            const alreadyLocal = ipswInfo?.localFiles.some(f => f.name.includes(`_${entry.version}_`) || f.name.includes(`-${entry.version}-`));
            return (
              <div key={entry.spec} className={`flex items-center gap-2 px-3 py-2 rounded text-xs border transition-colors ${
                isSelected
                  ? 'border-orange-500/60 bg-orange-500/10'
                  : 'border-gray-700 hover:border-gray-600'
              }`}>
                <button
                  className="flex-1 text-left"
                  onClick={() => setSelectedIpsw(entry.spec)}
                >
                  <span className={isSelected ? 'text-orange-200' : 'text-gray-300'}>
                    macOS {entry.major}{entry.name ? ` ${entry.name}` : ''} — {entry.version}
                  </span>
                  <span className="ml-2 text-gray-600">{entry.sizeGb} GB</span>
                  {alreadyLocal && <span className="ml-2 text-green-600 text-[10px]">✓ local</span>}
                  {isSelected && !alreadyLocal && <span className="ml-2 text-gray-600 text-[10px]">(auto-downloads at build time)</span>}
                </button>
                {!alreadyLocal && (
                  <button
                    onClick={() => downloadIpsw(entry)}
                    disabled={!!downloadingIpswSpec}
                    className="shrink-0 text-[10px] px-2 py-0.5 bg-cyan-500/20 text-cyan-400 border border-cyan-500/40 rounded hover:bg-cyan-500/30 disabled:opacity-40"
                    title={`Download ${entry.version} (${entry.sizeGb} GB) to VMShare`}
                  >
                    {isDownloading ? '…' : '↓'}
                  </button>
                )}
              </div>
            );
          })}

          {/* Fallback: latest via lume (when catalog unavailable) */}
          {(!ipswCatalog || ipswCatalog.length === 0) && (
            <button
              onClick={() => setSelectedIpsw('latest')}
              className={`w-full text-left px-3 py-2 rounded text-xs border transition-colors ${
                selectedIpsw === 'latest'
                  ? 'border-orange-500/60 bg-orange-500/10 text-orange-200'
                  : 'border-gray-700 text-gray-400 hover:border-gray-600'
              }`}
            >
              latest (auto-download at build time)
            </button>
          )}
        </div>
        {!selectedIpsw && (
          <p className="text-[10px] text-red-400/70">Select a macOS version to continue</p>
        )}
      </div>

      {/* Xcode picker */}
      <div className="space-y-1.5">
        <label className="text-xs text-gray-400">Xcode (optional)</label>
        <div className="space-y-1">
          <button
            onClick={() => setSelectedXcode(null)}
            className={`w-full text-left px-3 py-2 rounded text-xs border transition-colors ${
              selectedXcode === null
                ? 'border-gray-600 bg-gray-800/40 text-gray-300'
                : 'border-gray-700 text-gray-500 hover:border-gray-600'
            }`}
          >
            Skip Xcode install
          </button>
          {xcodeInfo?.apps.map(a => (
            <button
              key={a.path}
              onClick={() => setSelectedXcode(a.path)}
              className={`w-full text-left px-3 py-2 rounded text-xs border transition-colors ${
                selectedXcode === a.path
                  ? 'border-blue-500/60 bg-blue-500/10 text-blue-200'
                  : 'border-gray-700 text-gray-400 hover:border-gray-600'
              }`}
            >
              {a.name} {a.version ? `(${a.version})` : ''}
            </button>
          ))}
        </div>
      </div>

      {/* Golden VM name */}
      <div>
        <label className="text-xs text-gray-400">Golden VM name</label>
        <input
          type="text"
          value={goldenVm}
          onChange={e => { setGoldenVm(e.target.value); setNameTouched(true); }}
          className={`mt-1 w-full bg-gray-800 border rounded px-2 py-1.5 text-xs text-gray-200 focus:outline-none focus:border-orange-500/60 ${
            nameConflict ? 'border-red-500/60' : 'border-gray-700'
          }`}
        />
        {nameConflict && (
          <p className="mt-0.5 text-[10px] text-red-400">A VM named "{goldenVm}" already exists</p>
        )}
        {!nameConflict && goldenVm && (
          <p className="mt-0.5 text-[10px] text-gray-600">
            Also creates: {goldenVm.replace(/-golden$/, '')}-base, {goldenVm.replace(/-golden$/, '')}-nosip
          </p>
        )}
      </div>

      {/* Provision tools */}
      {provisionTools && provisionTools.length > 0 && (
        <div>
          <div className="flex items-center gap-2 mb-1.5">
            <label className="text-xs text-gray-400">Provision tools</label>
            {!allSelected && (
              <button
                onClick={() => setSelectedToolIds(new Set(provisionTools.map(t => t.id)))}
                className="text-[10px] text-gray-600 hover:text-gray-400"
              >
                select all
              </button>
            )}
          </div>
          <div className="grid grid-cols-3 gap-1.5">
            {provisionTools.map(tool => (
              <label key={tool.id} className="flex items-center gap-1.5 text-xs cursor-pointer">
                <input
                  type="checkbox"
                  checked={selectedToolIds.has(tool.id)}
                  onChange={() => toggleTool(tool.id)}
                  className="accent-orange-500"
                />
                <span className={selectedToolIds.has(tool.id) ? 'text-gray-300' : 'text-gray-600'}>{tool.label}</span>
              </label>
            ))}
          </div>
          <p className="mt-1 text-[10px] text-gray-600">
            {allSelected ? '→ --tools all' : selectedToolIds.size === 0 ? 'No tools selected' : `→ --tools ${toolsArg}`}
          </p>
        </div>
      )}

      {/* Resource overrides */}
      <div className="grid grid-cols-3 gap-2">
        {[
          { key: 'cpu',    label: 'vCPUs',   val: cpu,    set: setCpu,    placeholder: '4' },
          { key: 'memory', label: 'RAM',      val: memory, set: setMemory, placeholder: '8GB' },
          { key: 'disk',   label: 'Disk',     val: disk,   set: setDisk,   placeholder: '80GB' },
        ].map(({ key, label, val, set, placeholder }) => (
          <div key={key}>
            <label className="text-xs text-gray-400">{label}</label>
            <input
              type="text"
              value={val}
              onChange={e => set(e.target.value)}
              placeholder={placeholder}
              className="mt-1 w-full bg-gray-800 border border-gray-700 rounded px-2 py-1.5 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60"
            />
          </div>
        ))}
      </div>

      <div className="flex flex-col gap-1.5">
        <label className="flex items-center gap-2 text-xs text-gray-400 cursor-pointer">
          <input type="checkbox" checked={openVnc} onChange={e => setOpenVnc(e.target.checked)} className="accent-orange-500" />
          Open Screen Sharing automatically during VNC phases
        </label>
        <label className="flex items-center gap-2 text-xs text-gray-400 cursor-pointer">
          <input type="checkbox" checked={record} onChange={e => setRecord(e.target.checked)} className="accent-orange-500" />
          Record VNC during setup phases (saves .mp4 to recordings dir)
        </label>
        <label className="flex items-center gap-2 text-xs text-gray-400 cursor-pointer">
          <input type="checkbox" checked={installMissing} onChange={e => setInstallMissing(e.target.checked)} className="accent-orange-500" />
          Auto-install missing prerequisites (lume, ffmpeg, openssl) via Homebrew
        </label>
      </div>

      <div className="flex justify-end gap-2 pt-2 border-t border-gray-800">
        <button onClick={onClose} className="text-xs px-3 py-1.5 text-gray-400 hover:text-gray-200">
          Cancel
        </button>
        <button
          onClick={startBuild}
          disabled={!selectedIpsw || nameConflict || !goldenVm}
          className="text-xs px-4 py-1.5 bg-orange-500/20 text-orange-300 border border-orange-500/40 rounded hover:bg-orange-500/30 disabled:opacity-40"
        >
          Start Build
        </button>
      </div>
    </div>
  );
}

// ── Clone from Golden wizard ──────────────────────────────────────────────────

function CloneWizard({ onClose, onCreated }: Props) {
  const { data: vms } = useVMs();
  const goldenVMs = (vms ?? []).filter(v => v.meta?.tag === 'golden');
  const existingNames = useMemo(() => new Set((vms ?? []).map(v => v.name)), [vms]);

  const [goldenSource, setGoldenSource] = useState('');
  const [destName, setDestName] = useState(`run-${new Date().toISOString().slice(0, 16).replace(/[T:]/g, '-')}`);
  const [nameTouched, setNameTouched] = useState(false);
  const [cloning, setCloning] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // When source changes and name hasn't been touched, derive a default
  useEffect(() => {
    if (nameTouched || !goldenSource) return;
    const base = goldenSource.replace(/-golden$/, '');
    let candidate = `${base}-run`;
    if (existingNames.has(candidate)) {
      candidate = `${base}-run-${new Date().toISOString().slice(0, 16).replace(/[T:]/g, '-')}`;
    }
    setDestName(candidate);
  }, [goldenSource, existingNames, nameTouched]);

  const nameConflict = destName.length > 0 && existingNames.has(destName);

  async function doClone() {
    if (!goldenSource || !destName) return;
    setError(null);
    setCloning(true);
    try {
      await apiPost(`/vms/${encodeURIComponent(goldenSource)}/clone`, { destName });
      onCreated();
    } catch (err) {
      setError(String(err));
      setCloning(false);
    }
  }

  return (
    <div className="space-y-4">
      <h3 className="text-sm font-semibold text-gray-200">Clone Golden VM</h3>
      <p className="text-xs text-gray-500">
        Instant APFS CoW clone — completes in seconds. Use for ephemeral run VMs.
      </p>

      {error && (
        <div className="text-red-300 text-xs bg-red-900/20 border border-red-800 rounded p-2">{error}</div>
      )}

      <div className="space-y-1.5">
        <label className="text-xs text-gray-400">Source golden VM</label>
        {goldenVMs.length === 0 && (
          <p className="text-xs text-gray-600">No golden VMs found. Build one first.</p>
        )}
        {goldenVMs.map(v => (
          <button
            key={v.name}
            onClick={() => setGoldenSource(v.name)}
            className={`w-full text-left px-3 py-2 rounded text-xs border transition-colors ${
              goldenSource === v.name
                ? 'border-yellow-500/60 bg-yellow-500/10 text-yellow-200'
                : 'border-gray-700 text-gray-400 hover:border-gray-600'
            }`}
          >
            {v.name}
            {v.meta?.macos_version && <span className="ml-2 text-gray-600">macOS {v.meta.macos_version}</span>}
            {v.meta?.xcode_version && <span className="ml-2 text-gray-600">Xcode {v.meta.xcode_version}</span>}
          </button>
        ))}
        {goldenVMs.length > 0 && !goldenSource && (
          <p className="text-[10px] text-red-400/70">Select a golden VM</p>
        )}
      </div>

      <div>
        <label className="text-xs text-gray-400">New VM name</label>
        <input
          type="text"
          value={destName}
          onChange={e => { setDestName(e.target.value); setNameTouched(true); }}
          className={`mt-1 w-full bg-gray-800 border rounded px-2 py-1.5 text-xs text-gray-200 focus:outline-none focus:border-orange-500/60 ${
            nameConflict ? 'border-red-500/60' : 'border-gray-700'
          }`}
        />
        {nameConflict && (
          <p className="mt-0.5 text-[10px] text-red-400">A VM named "{destName}" already exists</p>
        )}
      </div>

      <div className="flex justify-end gap-2 pt-2 border-t border-gray-800">
        <button onClick={onClose} className="text-xs px-3 py-1.5 text-gray-400 hover:text-gray-200">
          Cancel
        </button>
        <button
          onClick={doClone}
          disabled={cloning || !goldenSource || !destName || nameConflict}
          className="text-xs px-4 py-1.5 bg-orange-500/20 text-orange-300 border border-orange-500/40 rounded hover:bg-orange-500/30 disabled:opacity-40"
        >
          {cloning ? 'Cloning…' : 'Clone'}
        </button>
      </div>
    </div>
  );
}

// ── Main wizard modal ─────────────────────────────────────────────────────────

export function VMWizard({ onClose, onCreated }: Props) {
  const [mode, setMode] = useState<'build' | 'clone' | null>(null);

  return (
    <div className="fixed inset-0 z-50 bg-black/60 flex items-center justify-center p-4">
      <div className="bg-gray-900 border border-gray-700 rounded-xl w-full max-w-lg max-h-[90vh] overflow-y-auto shadow-2xl">
        <div className="p-5">
          {!mode && (
            <div className="space-y-4">
              <h3 className="text-sm font-semibold text-gray-200">New VM</h3>
              <div className="grid grid-cols-2 gap-3">
                <button
                  onClick={() => setMode('build')}
                  className="flex flex-col gap-1 p-4 bg-gray-800/60 border border-gray-700 rounded-lg text-left hover:border-orange-500/50 transition-colors"
                >
                  <span className="text-sm font-medium text-gray-200">Build Golden VM</span>
                  <span className="text-xs text-gray-500">
                    Full 4-phase pipeline from IPSW. Creates base → nosip → golden VM. Takes ~1–2 h.
                  </span>
                </button>
                <button
                  onClick={() => setMode('clone')}
                  className="flex flex-col gap-1 p-4 bg-gray-800/60 border border-gray-700 rounded-lg text-left hover:border-orange-500/50 transition-colors"
                >
                  <span className="text-sm font-medium text-gray-200">Clone from Golden</span>
                  <span className="text-xs text-gray-500">
                    Instant APFS CoW clone. Ready in seconds. Use for run VMs.
                  </span>
                </button>
              </div>
              <div className="flex justify-end pt-2 border-t border-gray-800">
                <button onClick={onClose} className="text-xs px-3 py-1.5 text-gray-400 hover:text-gray-200">
                  Cancel
                </button>
              </div>
            </div>
          )}
          {mode === 'build' && (
            <BuildGoldenWizard onClose={onClose} onCreated={onCreated} />
          )}
          {mode === 'clone' && (
            <CloneWizard onClose={onClose} onCreated={onCreated} />
          )}
        </div>
      </div>
    </div>
  );
}
