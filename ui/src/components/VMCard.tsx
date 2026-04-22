import { useState, useRef, useEffect } from 'react';
import { VMData, VMStage, apiPost, apiDelete, apiPatch, useRecordStatus } from '../hooks/useAPI.ts';
import { StageRunner } from './StageRunner.tsx';
import { LogViewer } from './LogViewer.tsx';
import { RecordingsTab } from './RecordingsTab.tsx';
import { ScreenshotsTab } from './ScreenshotsTab.tsx';
import { FilesTab } from './FilesTab.tsx';
import { GHCRPushModal } from './GHCRPushModal.tsx';

interface GhcrTask { id: string; status: string; error?: string; log: string; }

interface LogFileEntry { name: string; size: number; mtime: number; }

/** Shows per-run log files from LOG_BASE/<vmId>/ with a dropdown to pick runs.
 *  Falls back to SSH log viewer when VM is running cleanly (user toggle). */
function BuildLogPanel({ vmId, isRunning }: { vmId: string; isRunning: boolean }) {
  const [text, setText] = useState<string | null>(null);
  const [files, setFiles] = useState<LogFileEntry[]>([]);
  // null = "auto" (track newest file); once user picks from dropdown, we lock to that name
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [autoScroll, setAutoScroll] = useState(true);
  const [showSSH, setShowSSH] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);

  // The file to actually fetch: locked selection or newest available (files[0])
  const activeFile = selectedFile ?? files[0]?.name ?? 'latest.log';

  // Refresh file list every 5s; auto-advance selectedFile when new run appears
  useEffect(() => {
    let cancelled = false;
    async function fetchFiles() {
      try {
        const resp = await fetch(`/api/vms/${encodeURIComponent(vmId)}/log-files`);
        if (!cancelled && resp.ok) {
          const list = await resp.json() as LogFileEntry[];
          setFiles(list);
          // If user hasn't manually selected a file, stay on newest (auto mode)
          setSelectedFile(prev => prev === null ? null : prev);
        }
      } catch { /* ignore */ }
    }
    fetchFiles();
    const t = setInterval(fetchFiles, 5000);
    return () => { cancelled = true; clearInterval(t); };
  }, [vmId]);

  // Poll active file every 2s
  useEffect(() => {
    let cancelled = false;
    async function fetchLog() {
      try {
        const url = `/api/vms/${encodeURIComponent(vmId)}/build-log?file=${encodeURIComponent(activeFile)}&lines=2000`;
        const resp = await fetch(url);
        if (!cancelled) setText(resp.ok ? await resp.text() : null);
      } catch { /* ignore */ }
    }
    setText(null);
    fetchLog();
    const t = setInterval(fetchLog, 2000);
    return () => { cancelled = true; clearInterval(t); };
  }, [vmId, activeFile]);

  useEffect(() => {
    if (autoScroll) bottomRef.current?.scrollIntoView();
  }, [text, autoScroll]);

  if (showSSH) return (
    <div className="flex flex-col h-80">
      <div className="flex items-center gap-2 px-3 py-1.5 border-b border-gray-800 bg-gray-900/80 flex-shrink-0">
        <button onClick={() => setShowSSH(false)} className="text-[10px] text-orange-400 hover:text-orange-300">← Build log</button>
      </div>
      <LogViewer vmId={vmId} />
    </div>
  );

  return (
    <div className="flex flex-col h-80">
      <div className="flex items-center gap-2 px-3 py-1.5 border-b border-gray-800 bg-gray-900/80 flex-shrink-0">
        <select
          value={selectedFile ?? ''}
          onChange={e => setSelectedFile(e.target.value || null)}
          className="bg-gray-800 border border-gray-700 rounded px-2 py-0.5 text-[10px] text-gray-300 focus:outline-none focus:border-orange-500/60 max-w-[260px]"
        >
          <option value="">{files[0]?.name ?? 'latest.log'} (auto)</option>
          {files.map(f => (
            <option key={f.name} value={f.name}>{f.name}</option>
          ))}
        </select>
        <label className="flex items-center gap-1 text-[10px] text-gray-500 cursor-pointer ml-auto">
          <input type="checkbox" checked={autoScroll} onChange={e => setAutoScroll(e.target.checked)} className="accent-orange-400" />
          auto-scroll
        </label>
        {isRunning && (
          <button onClick={() => setShowSSH(true)} className="text-[10px] text-gray-500 hover:text-gray-300">SSH logs →</button>
        )}
      </div>
      <div className="flex-1 overflow-y-auto bg-black/60 p-3 font-mono text-[11px] text-gray-300">
        {text === null && <div className="text-gray-600 mt-8 text-center">Loading...</div>}
        {text !== null && !text.trim() && (
          <div className="text-gray-600 mt-8 text-center text-xs space-y-1">
            <div>No build logs for this VM.</div>
            {isRunning && <div className="text-gray-700">Stage scripts write logs here. For SSH app logs use <span className="text-gray-500">SSH logs →</span></div>}
          </div>
        )}
        {text?.trim() && <pre className="whitespace-pre-wrap leading-5">{text}</pre>}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}

const STAGE_LABELS: Record<string, string> = {
  download_ipsw:   'Download macOS IPSW',
  create_vm:       'Create base VM',
  setup_assistant: 'Setup Assistant',
  disable_sip:     'Disable SIP',
  provision_vm:    'Install Xcode & tools',
};

function statusDot(s: VMStage['status']) {
  if (s === 'done')    return <span className="text-green-400">✓</span>;
  if (s === 'failed')  return <span className="text-red-400">✗</span>;
  if (s === 'running') return <span className="text-yellow-400 animate-pulse">⟳</span>;
  return <span className="text-gray-600">○</span>;
}

function vmStatusColor(status: string) {
  if (status === 'running')              return 'text-green-400';
  if (status === 'stopped')              return 'text-gray-500';
  if (status === 'not_created')          return 'text-yellow-500';
  if (status.includes('(stale)'))        return 'text-red-500/70';
  if (status.includes('provisioning'))   return 'text-yellow-500/80';
  return 'text-gray-400';
}

function formatBytes(bytes: number) {
  if (bytes >= 1e9) return `${(bytes / 1e9).toFixed(1)} GB`;
  if (bytes >= 1e6) return `${(bytes / 1e6).toFixed(0)} MB`;
  return `${bytes} B`;
}

interface Props {
  vm: VMData;
  onRefresh: () => void;
  isBeingBuilt?: boolean;
  onStopBuild?: (vmId: string) => void;
}

export function VMCard({ vm, onRefresh, isBeingBuilt, onStopBuild }: Props) {
  const [expanded, setExpanded] = useState(false);
  const [activePanel, setActivePanel] = useState<'stages' | 'logs' | 'recordings' | 'screenshots' | 'files' | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [canForceStop, setCanForceStop] = useState(false);
  const [stopConfirm, setStopConfirm] = useState(false);
  const stopConfirmTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [sshOpen, setSshOpen] = useState(false);
  const sshRef = useRef<HTMLDivElement>(null);
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const [deleteConfirm, setDeleteConfirm] = useState(false);
  const [deleteWithFiles, setDeleteWithFiles] = useState(true);
  const [deleteFileList, setDeleteFileList] = useState<{ name: string; category: string; size: number }[]>([]);
  const [showGhcrPush, setShowGhcrPush] = useState(false);
  const [pushTaskId, setPushTaskId] = useState<string | null>(null);
  const [pushLabel, setPushLabel] = useState('');
  const [pushTask, setPushTask] = useState<GhcrTask | null>(null);
  const pushLogRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    if (!sshOpen) return;
    function onOutside(e: MouseEvent) {
      if (sshRef.current && !sshRef.current.contains(e.target as Node)) setSshOpen(false);
    }
    document.addEventListener('mousedown', onOutside);
    return () => document.removeEventListener('mousedown', onOutside);
  }, [sshOpen]);

  useEffect(() => {
    if (!menuOpen) return;
    function onOutside(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false);
    }
    document.addEventListener('mousedown', onOutside);
    return () => document.removeEventListener('mousedown', onOutside);
  }, [menuOpen]);

  // Poll inline push task
  useEffect(() => {
    if (!pushTaskId) return;
    const poll = async () => {
      try {
        const resp = await fetch(`/api/ghcr/task/${pushTaskId}`);
        if (resp.ok) setPushTask(await resp.json() as GhcrTask);
      } catch { /* ignore */ }
    };
    poll();
    const t = setInterval(poll, 1500);
    return () => clearInterval(t);
  }, [pushTaskId]);

  // Auto-scroll inline push log
  useEffect(() => {
    if (pushLogRef.current) pushLogRef.current.scrollTop = pushLogRef.current.scrollHeight;
  }, [pushTask?.log]);

  // Inline config editing (G1)
  const [editingField, setEditingField] = useState<'cpu' | 'memory' | 'disk' | null>(null);
  const [editValue, setEditValue] = useState('');
  const editInputRef = useRef<HTMLInputElement>(null);

  const tag = vm.meta?.tag ?? 'dev';
  const tagColors: Record<string, string> = {
    golden: 'bg-yellow-500/20 text-yellow-300 border-yellow-500/40',
    run:    'bg-blue-500/20 text-blue-300 border-blue-500/40',
    dev:    'bg-gray-700/40 text-gray-400 border-gray-600/40',
  };

  async function doAction(action: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await action();
      onRefresh();
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }

  const isRunning = vm.status === 'running';
  const { data: recStatus, refresh: refreshRecStatus } = useRecordStatus(vm.name);
  const isRecording = isRunning && (recStatus?.recording ?? false);
  // lume marks explicitly stuck VMs with "(stale)" — they're not actually running
  const isStale = vm.status.includes('(stale)');
  // A stage script running on the host counts as active — hide Start, show progress
  const hasRunningStage = vm.stages.some(s => s.status === 'running');
  // Any status that isn't stopped/not_created/stale — show Stop, not Start
  const isActive = !isStale && (vm.status !== 'stopped' && vm.status !== 'not_created' || hasRunningStage);
  const isNotCreated = vm.status === 'not_created';
  // VMs that are part of an active build track — identified by server-side activeBuildMap
  const isIntermediateVM = isBeingBuilt;

  // Show the most recent failed build stage error prominently
  // "interrupted" means the server restarted mid-run — not a real failure, don't show banner.
  const failedStage = vm.stages.find(s => s.status === 'failed' && s.output && s.output.trim() !== 'interrupted');

  // Inline config edit helpers (G1)
  function startEdit(field: 'cpu' | 'memory' | 'disk', currentVal: string) {
    setEditingField(field);
    setEditValue(currentVal);
    setTimeout(() => editInputRef.current?.select(), 0);
  }

  async function commitEdit() {
    if (!editingField) return;
    const field = editingField;
    const val = editValue.trim();
    setEditingField(null);
    if (!val) return;
    try {
      const body: Record<string, unknown> =
        field === 'cpu'    ? { cpu: Number(val) } :
        field === 'memory' ? { memory: val } :
                             { diskSize: val };
      await apiPatch(`/vms/${vm.name}/config`, body);
      onRefresh();
    } catch (err) {
      setError(String(err));
    }
  }

  function EditableValue({ field, display, rawVal }: { field: 'cpu' | 'memory' | 'disk'; display: string; rawVal: string }) {
    if (editingField === field) {
      return (
        <input
          ref={editInputRef}
          value={editValue}
          onChange={e => setEditValue(e.target.value)}
          onBlur={commitEdit}
          onKeyDown={e => { if (e.key === 'Enter') commitEdit(); if (e.key === 'Escape') setEditingField(null); }}
          className="w-16 bg-gray-800 border border-orange-500/60 rounded px-1 text-orange-200 text-xs outline-none"
        />
      );
    }
    return (
      <span
        className="cursor-pointer hover:text-orange-300 hover:underline decoration-dotted"
        title={`Click to edit ${field}`}
        onClick={() => startEdit(field, rawVal)}
      >{display}</span>
    );
  }

  // G2: last-run timestamp
  const lastRun = vm.meta?.last_run_at
    ? new Date(vm.meta.last_run_at * 1000).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
    : null;

  return (
    <div className="border border-gray-800 rounded-lg bg-gray-900/60">
      {/* Card header */}
      <div className="px-4 py-3 flex items-center gap-3">
        <button
          onClick={() => {
            const next = !expanded;
            setExpanded(next);
            if (next && activePanel === null) setActivePanel('stages');
          }}
          className="text-gray-500 hover:text-gray-300 text-xs w-4"
        >
          {expanded ? '▼' : '▶'}
        </button>

        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="font-semibold text-gray-100 text-sm">{vm.name}</span>
            <span className={`text-xs px-1.5 py-0.5 rounded border ${tagColors[tag] ?? tagColors.dev}`}>{tag}</span>
            <span className={`text-xs ${hasRunningStage && vm.status === 'stopped' ? 'text-yellow-400' : vmStatusColor(vm.status)}`}>
              {hasRunningStage && vm.status === 'stopped' ? 'stage running' : vm.status}
            </span>
          </div>
          <div className="text-xs text-gray-500 mt-0.5 flex gap-3 flex-wrap">
            {(vm.meta?.macos_version ?? vm.name.match(/(\d+\.\d+(?:\.\d+)?)/)?.[1]) && (
              <span className="text-gray-400">macOS {vm.meta?.macos_version ?? vm.name.match(/(\d+\.\d+(?:\.\d+)?)/)![1]}</span>
            )}
            {vm.meta?.xcode_version && <span className="text-gray-500">Xcode {vm.meta.xcode_version}</span>}
            <EditableValue field="cpu" display={`${vm.cpuCount} vCPU`} rawVal={String(vm.cpuCount)} />
            <EditableValue field="memory" display={`${(vm.memorySize / 1e9).toFixed(0)} GB RAM`} rawVal={`${(vm.memorySize / 1e9).toFixed(0)}GB`} />
            <EditableValue field="disk" display={`${formatBytes(vm.diskSize.allocated)} / ${formatBytes(vm.diskSize.total)}`} rawVal={formatBytes(vm.diskSize.total)} />
            <span>{vm.display}</span>
            {vm.ipAddress && <span className="text-cyan-500">{vm.ipAddress}</span>}
            {lastRun && <span className="text-gray-600" title="Last started">ran {lastRun}</span>}
          </div>
        </div>

        {/* Stage badges — compact */}
        <div className="hidden sm:flex gap-1 flex-wrap max-w-[180px]">
          {vm.stages.map(s => {
            const label = STAGE_LABELS[s.stage] ?? s.stage;
            const tip = s.output ? `${label}: ${s.output.slice(0, 120)}` : label;
            return (
              <span key={s.stage} title={tip} className="text-xs">
                {statusDot(s.status)}
              </span>
            );
          })}
        </div>

        {/* Actions */}
        <div className="flex gap-1.5 flex-shrink-0">
          {(isBeingBuilt || isIntermediateVM) ? (
            <button
              disabled={busy}
              onClick={() => onStopBuild ? onStopBuild(vm.name) : doAction(() => apiPost(`/vms/${vm.name}/stop-build`))}
              className="btn-sm bg-red-600/20 text-red-300 border-red-600/40 hover:bg-red-600/30"
              title="Kill the build process"
            >Stop Build</button>
          ) : isNotCreated ? null : isActive ? (
            <>
              {isRunning && (
                <button
                  onClick={async () => {
                    try {
                      const resp = await fetch(`/api/vms/${encodeURIComponent(vm.name)}/vnc-url`);
                      if (!resp.ok) throw new Error('VNC URL not available — stop and restart this VM via virfield to get a VNC URL');
                      const { url } = await resp.json() as { url: string };
                      const a = document.createElement('a');
                      a.href = url;
                      a.style.display = 'none';
                      document.body.appendChild(a);
                      a.click();
                      document.body.removeChild(a);
                    } catch (e) {
                      setError(e instanceof Error ? e.message : 'VNC URL not available');
                    }
                  }}
                  className="btn-sm bg-purple-600/20 text-purple-300 border-purple-600/40 hover:bg-purple-600/30"
                  title="Open in Screen Sharing"
                >VNC</button>
              )}
              {isRunning && vm.ipAddress && (
                <div className="relative" ref={sshRef}>
                  <button
                    onClick={() => setSshOpen(v => !v)}
                    className={`btn-sm bg-cyan-600/20 text-cyan-300 border-cyan-600/40 hover:bg-cyan-600/30 ${sshOpen ? 'ring-1 ring-cyan-500/50' : ''}`}
                  >SSH</button>
                  {sshOpen && (
                    <div className="absolute right-0 top-full mt-1 z-20 flex flex-col gap-1 bg-gray-900 border border-gray-700 rounded shadow-xl p-2 min-w-[220px]">
                      <code className="text-[11px] text-cyan-300 font-mono select-all whitespace-nowrap px-1">
                        ssh lume@{vm.ipAddress}
                      </code>
                      <div className="flex gap-1 pt-1">
                        <button
                          onClick={() => { navigator.clipboard.writeText(`ssh lume@${vm.ipAddress}`); setSshOpen(false); }}
                          className="flex-1 text-[10px] px-2 py-1 bg-gray-700 hover:bg-gray-600 text-gray-300 rounded"
                        >Copy</button>
                        <button
                          onClick={() => { fetch(`/api/vms/${encodeURIComponent(vm.name)}/ssh-open`, { method: 'POST' }).catch(() => {}); setSshOpen(false); }}
                          className="flex-1 text-[10px] px-2 py-1 bg-cyan-600/30 hover:bg-cyan-600/50 text-cyan-200 rounded"
                        >Open Terminal</button>
                      </div>
                    </div>
                  )}
                </div>
              )}
              {/* Record / Stop-record button */}
              <button
                onClick={async () => {
                  try {
                    if (isRecording) {
                      await apiPost(`/vms/${vm.name}/record/stop`);
                    } else {
                      await apiPost(`/vms/${vm.name}/record/start`);
                    }
                    refreshRecStatus();
                    // Switch to recordings tab so user sees the result
                    if (!isRecording) setActivePanel('recordings');
                  } catch (err) {
                    setError(String(err));
                  }
                }}
                title={isRecording ? 'Stop recording' : 'Start VNC recording'}
                className={`btn-sm ${isRecording ? 'bg-red-600/40 text-red-200 border-red-500/60 hover:bg-red-600/50 animate-pulse' : 'bg-gray-700/40 text-gray-400 border-gray-600/40 hover:bg-gray-700/60'}`}
              >{isRecording ? '⏹ Rec' : '⏺'}</button>
              {canForceStop ? (
                <button
                  disabled={busy}
                  onClick={() => doAction(async () => {
                    setCanForceStop(false);
                    await apiPost(`/vms/${vm.name}/force-stop`);
                  })}
                  className="btn-sm bg-red-700/40 text-red-200 border-red-600/60 hover:bg-red-700/60"
                  title="VM is provisioning — this will delete it"
                >Force Delete</button>
              ) : stopConfirm ? (
                <button
                  disabled={busy}
                  onClick={async () => {
                    if (stopConfirmTimer.current) clearTimeout(stopConfirmTimer.current);
                    setStopConfirm(false);
                    setBusy(true);
                    setError(null);
                    setCanForceStop(false);
                    try {
                      const resp = await fetch(`/api/vms/${encodeURIComponent(vm.name)}/stop`, { method: 'POST' });
                      if (resp.status === 409) {
                        const data = await resp.json() as { error: string; canForce?: boolean };
                        const inner = data.error.match(/"message":"([^"]+)"/)?.[1] ?? data.error;
                        setError(inner);
                        if (data.canForce) setCanForceStop(true);
                      } else if (!resp.ok) {
                        setError(await resp.text());
                      } else {
                        onRefresh();
                      }
                    } catch (err) {
                      setError(String(err));
                    } finally {
                      setBusy(false);
                    }
                  }}
                  className="btn-sm bg-red-600 text-white border-red-500 hover:bg-red-700 animate-pulse"
                  title="Click again to confirm stop"
                >Confirm?</button>
              ) : (
                <button
                  disabled={busy}
                  onClick={() => {
                    setStopConfirm(true);
                    if (stopConfirmTimer.current) clearTimeout(stopConfirmTimer.current);
                    stopConfirmTimer.current = setTimeout(() => setStopConfirm(false), 3000);
                  }}
                  className="btn-sm bg-red-600/20 text-red-300 border-red-600/40 hover:bg-red-600/30"
                >Stop</button>
              )}
            </>
          ) : (
            <button
              disabled={busy}
              onClick={() => doAction(() => apiPost(`/vms/${vm.name}/start`, { noDisplay: true }))}
              className="btn-sm bg-green-600/20 text-green-300 border-green-600/40 hover:bg-green-600/30"
            >Start</button>
          )}
          {!isBeingBuilt && !isNotCreated && !isIntermediateVM && !hasRunningStage && (
          <button
            onClick={() => setShowGhcrPush(true)}
            className="btn-sm bg-blue-600/20 text-blue-300 border-blue-600/40 hover:bg-blue-600/30"
            title="Push to GHCR"
          >↑ GHCR</button>
          )}
          {!isBeingBuilt && !isNotCreated && !isIntermediateVM && vm.status === 'stopped' && !hasRunningStage && (
          <button
            disabled={busy}
            onClick={() => doAction(() => apiPost(`/vms/${vm.name}/clone`, { destName: `${vm.name}-clone-${Date.now()}` }))}
            className="btn-sm"
          >Clone</button>
          )}
          <div className="relative" ref={menuRef}>
            <button
              onClick={() => setMenuOpen(v => !v)}
              onMouseDown={(e) => e.preventDefault()}
              className={`btn-sm ${menuOpen ? 'ring-1 ring-gray-500/50' : ''}`}
            >···</button>
            {menuOpen && (
              <div className="absolute right-0 top-full mt-1 z-20 bg-gray-800 border border-gray-700 rounded shadow-xl min-w-[140px]">
                <button
                  onClick={() => { setMenuOpen(false); doAction(() => apiPost(`/vms/${vm.name}/promote-golden`)); }}
                  className="menu-item text-yellow-300 hover:bg-yellow-500/10"
                >Set as Golden</button>
                <button
                  onClick={async () => {
                    setMenuOpen(false);
                    try {
                      const resp = await fetch(`/api/vms/${encodeURIComponent(vm.name)}/files`);
                      const list = resp.ok ? await resp.json() as { name: string; category: string; size: number }[] : [];
                      setDeleteFileList(list);
                    } catch { setDeleteFileList([]); }
                    setDeleteWithFiles(true);
                    setDeleteConfirm(true);
                  }}
                  className="menu-item text-red-300 hover:bg-red-500/10"
                >Delete</button>
              </div>
            )}
          </div>
        </div>
      </div>

      {deleteConfirm && (
        <div className="px-4 py-3 border-t border-red-900/50 bg-red-950/40 space-y-2">
          <div className="text-xs text-red-300 font-medium">Delete <span className="font-mono">{vm.name}</span>?</div>
          <label className="flex items-start gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={deleteWithFiles}
              onChange={e => setDeleteWithFiles(e.target.checked)}
              className="mt-0.5 accent-red-500"
            />
            <span className="text-[11px] text-red-400/80">
              {deleteFileList.length > 0 ? (() => {
                const counts: Record<string, number> = {};
                for (const f of deleteFileList) counts[f.category] = (counts[f.category] ?? 0) + 1;
                const summary = Object.entries(counts).map(([c, n]) => `${n} ${c}`).join(', ');
                return <>Delete associated files ({summary})</>;
              })() : 'Delete associated files (none found)'}
            </span>
          </label>
          <div className="flex gap-2">
            <button
              onClick={() => setDeleteConfirm(false)}
              className="text-[11px] px-3 py-1 bg-gray-700 hover:bg-gray-600 text-gray-300 rounded"
            >Cancel</button>
            <button
              disabled={busy}
              onClick={() => {
                setDeleteConfirm(false);
                doAction(async () => {
                  if (deleteWithFiles) await apiDelete(`/vms/${vm.name}/files`);
                  await apiDelete(`/vms/${vm.name}?deleteFiles=false`);
                });
              }}
              className="text-[11px] px-3 py-1 bg-red-700/60 hover:bg-red-700/80 text-red-200 rounded"
            >Delete</button>
          </div>
        </div>
      )}

      {error && (
        <div className="px-4 py-2 bg-red-900/30 text-red-300 text-xs border-t border-red-800/40">{error}</div>
      )}

      {/* Build error banner — shown when a build stage has failed */}
      {failedStage && (
        <div className="px-4 py-2 bg-red-950/50 border-t border-red-900/50 space-y-0.5">
          <div className="flex items-center justify-between gap-2">
            <div className="text-xs text-red-400 font-medium">Build failed: {STAGE_LABELS[failedStage.stage] ?? failedStage.stage}</div>
            <button
              className="text-[10px] text-red-400/60 hover:text-red-300 border border-red-800/40 rounded px-1.5 py-0.5 leading-none shrink-0"
              onClick={() => doAction(() =>
                fetch(`/api/vms/${vm.name}/stages/${failedStage.stage}`, { method: 'DELETE' })
                  .then(() => onRefresh())
              )}
            >Clear</button>
          </div>
          <pre className="text-[10px] text-red-400/80 whitespace-pre-wrap font-mono leading-tight max-h-20 overflow-y-auto">{failedStage.output}</pre>
        </div>
      )}

      {/* Expanded section */}
      {expanded && (
        <div className="border-t border-gray-800">
          {/* Sub-tabs */}
          <div className="flex gap-0 border-b border-gray-800 px-4 pt-2">
            {([
              { id: 'stages',      label: 'Setup Stages' },
              { id: 'logs',        label: 'Logs' },
              { id: 'recordings',  label: 'Recordings' },
              { id: 'screenshots', label: 'Screenshots' },
              { id: 'files',       label: 'Files' },
            ] as const).map(p => (
              <button
                key={p.id}
                onClick={() => setActivePanel(activePanel === p.id ? null : p.id)}
                onMouseDown={(e) => e.preventDefault()}
                className={`px-3 py-1.5 text-xs border-b-2 transition-colors ${
                  activePanel === p.id
                    ? 'border-orange-400 text-orange-300'
                    : 'border-transparent text-gray-500 hover:text-gray-300'
                }`}
              >
                {p.label}
              </button>
            ))}
          </div>

          {activePanel === 'stages' && (
            <StageRunner vm={vm} onRefresh={onRefresh} />
          )}
          {activePanel === 'logs' && (
            <BuildLogPanel vmId={vm.name} isRunning={isRunning} />
          )}
          {activePanel === 'recordings' && (
            <RecordingsTab vmId={vm.name} />
          )}
          {activePanel === 'screenshots' && (
            <ScreenshotsTab vmId={vm.name} />
          )}
          {activePanel === 'files' && (
            <FilesTab vmId={vm.name} />
          )}
        </div>
      )}
      {/* Inline GHCR push progress */}
      {pushTaskId && pushTask && (
        <div className="border-t border-gray-800 px-4 py-2 space-y-1">
          <div className="flex items-center gap-2">
            <span className={`text-[10px] font-medium ${
              pushTask.status === 'done' ? 'text-green-400' :
              ['failed','cancelled'].includes(pushTask.status) ? 'text-red-400' :
              'text-orange-400 animate-pulse'
            }`}>
              {pushTask.status === 'done' ? '✓' : ['failed','cancelled'].includes(pushTask.status) ? '✗' : '⟳'}
            </span>
            <span className="text-[10px] text-gray-400 font-mono truncate flex-1" title={pushLabel}>{pushLabel}</span>
            {pushTask.status === 'done' && (
              <button onClick={() => { setPushTaskId(null); setPushTask(null); }}
                className="text-[10px] text-gray-600 hover:text-gray-400 ml-auto shrink-0">✕</button>
            )}
            {['failed','cancelled'].includes(pushTask.status) && (
              <button onClick={() => { setPushTaskId(null); setPushTask(null); }}
                className="text-[10px] text-red-500/60 hover:text-red-400 ml-auto shrink-0">✕</button>
            )}
          </div>
          {pushTask.log && (
            <pre ref={pushLogRef}
              className="bg-gray-950 rounded p-1.5 text-[10px] text-gray-500 font-mono overflow-auto max-h-20 whitespace-pre-wrap">
              {pushTask.log.split('\n').slice(-6).join('\n')}
            </pre>
          )}
          {['failed','cancelled'].includes(pushTask.status) && pushTask.error && (
            <div className="text-[10px] text-red-400">{pushTask.error}</div>
          )}
        </div>
      )}

      {showGhcrPush && (
        <GHCRPushModal
          vmName={vm.name}
          onClose={() => setShowGhcrPush(false)}
          onTaskStarted={(id, label) => {
            setPushTaskId(id);
            setPushLabel(label);
            setPushTask(null);
            setShowGhcrPush(false);
          }}
        />
      )}
    </div>
  );
}
