import { useState, useEffect, useRef, ReactNode } from 'react';
import { VMData, BuildState, BuildStageInfo, apiPost, useBuildState, useXcode } from '../hooks/useAPI.ts';

// 5-stage pipeline — matches virfield scripts (00-download-ipsw added)
const STAGE_ORDER = ['download_ipsw', 'create_vm', 'setup_assistant', 'disable_sip', 'provision_vm'] as const;
type StageKey = (typeof STAGE_ORDER)[number];

const STAGE_LABELS: Record<StageKey, string> = {
  download_ipsw:   'Download macOS IPSW',
  create_vm:       'Create VM',
  setup_assistant: 'Setup Assistant',
  disable_sip:     'Disable SIP',
  provision_vm:    'Install Xcode & tools',
};

// state.json key → stage key
const STATE_KEY_MAP: Record<string, StageKey> = {
  '00-download-ipsw':   'download_ipsw',
  '01-create-vm':       'create_vm',
  '02-setup-assistant': 'setup_assistant',
  '03-disable-sip':     'disable_sip',
  '04-provision-vm':    'provision_vm',
};

// Stages that show an options panel before running
const STAGE_HAS_OPTIONS: Partial<Record<StageKey, true>> = {
  create_vm:    true,
  provision_vm: true,
};

const PROVISION_TOOLS = [
  'system', 'autologin', 'ssh_key', 'homebrew', 'socat',
  'peekaboo', 'peekaboo_agent', 'screenresolution', 'xcbeautify',
  'jq', 'logging', 'tcc', 'automation',
] as const;
type ToolId = (typeof PROVISION_TOOLS)[number];

interface StageRunOpts {
  ipsw?: string;
  xcode?: string;
  tools?: string;
}

function statusDot(status: BuildStageInfo['status'] | 'pending') {
  if (status === 'done')    return <span className="text-green-400">✓</span>;
  if (status === 'failed')  return <span className="text-red-400">✗</span>;
  if (status === 'running') return <span className="text-yellow-400 animate-pulse">⟳</span>;
  return <span className="text-gray-600">○</span>;
}

function statusColors(status: string) {
  if (status === 'done')    return 'text-green-400 bg-green-900/20 border-green-800';
  if (status === 'failed')  return 'text-red-400 bg-red-900/20 border-red-800';
  if (status === 'running') return 'text-yellow-400 bg-yellow-900/20 border-yellow-800';
  return 'text-gray-500 bg-gray-900/20 border-gray-800';
}

function formatTime(iso?: string) {
  if (!iso) return '';
  return new Date(iso).toLocaleTimeString();
}

// ── Per-stage options panels ──────────────────────────────────────────────────

function CreateVmOptions({
  ipswInput, setIpswInput, onConfirm, onCancel,
}: {
  ipswInput: string;
  setIpswInput: (v: string) => void;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="mt-2 ml-7 bg-gray-900/60 border border-gray-700 rounded p-3 space-y-2">
      <div className="text-[11px] text-gray-400 font-medium mb-1">Create VM options</div>
      <div className="flex items-center gap-2 text-xs">
        <span className="text-gray-500 w-28 shrink-0">IPSW path</span>
        <input
          type="text"
          value={ipswInput}
          onChange={e => setIpswInput(e.target.value)}
          placeholder="latest"
          className="flex-1 bg-gray-800 border border-gray-700 rounded px-2 py-1 text-gray-300 placeholder-gray-600 focus:outline-none focus:border-orange-500/60 text-[11px]"
        />
      </div>
      <div className="flex gap-2 pt-1">
        <button
          onClick={onConfirm}
          className="px-3 py-1 bg-orange-500/30 hover:bg-orange-500/50 text-orange-200 border border-orange-500/50 rounded text-[11px]"
        >
          Run
        </button>
        <button
          onClick={onCancel}
          className="px-3 py-1 bg-gray-700 hover:bg-gray-600 text-gray-400 rounded text-[11px]"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}

function ProvisionVmOptions({
  xcodeInput, setXcodeInput, selectedTools, setSelectedTools, xcodeApps, onConfirm, onCancel,
}: {
  xcodeInput: string;  // '' = skip, path = selected Xcode.app
  setXcodeInput: (v: string) => void;
  selectedTools: Set<ToolId>;
  setSelectedTools: (fn: (prev: Set<ToolId>) => Set<ToolId>) => void;
  xcodeApps: Array<{ path: string; name: string; version: string | null }>;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const allChecked = PROVISION_TOOLS.every(t => selectedTools.has(t));
  const noneChecked = selectedTools.size === 0;

  function toggleAll() {
    setSelectedTools(() => allChecked ? new Set() : new Set(PROVISION_TOOLS));
  }

  function toggleTool(tool: ToolId) {
    setSelectedTools(prev => {
      const next = new Set(prev);
      if (next.has(tool)) next.delete(tool);
      else next.add(tool);
      return next;
    });
  }

  return (
    <div className="mt-2 ml-7 bg-gray-900/60 border border-gray-700 rounded p-3 space-y-3">
      <div className="text-[11px] text-gray-400 font-medium">Install Xcode & tools options</div>

      {/* Xcode picker */}
      <div className="space-y-1">
        <div className="text-[11px] text-gray-500 mb-1">Xcode</div>
        <button
          onClick={() => setXcodeInput('')}
          className={`w-full text-left px-2.5 py-1.5 rounded text-[11px] border transition-colors ${
            xcodeInput === ''
              ? 'border-gray-600 bg-gray-800/60 text-gray-300'
              : 'border-gray-700/60 text-gray-500 hover:border-gray-600'
          }`}
        >
          Skip Xcode install
        </button>
        {xcodeApps.map(a => (
          <button
            key={a.path}
            onClick={() => setXcodeInput(a.path)}
            className={`w-full text-left px-2.5 py-1.5 rounded text-[11px] border transition-colors ${
              xcodeInput === a.path
                ? 'border-orange-500/60 bg-orange-500/10 text-orange-200'
                : 'border-gray-700/60 text-gray-400 hover:border-gray-600'
            }`}
          >
            {a.name}{a.version ? ` (${a.version})` : ''}
          </button>
        ))}
        {xcodeApps.length === 0 && (
          <div className="text-[10px] text-gray-600 px-1">No Xcode installations found in /Applications or VMShare</div>
        )}
      </div>

      {/* Tools */}
      <div>
        <div className="flex items-center gap-2 mb-1.5">
          <span className="text-[11px] text-gray-400 font-medium">Tools</span>
          <label className="flex items-center gap-1 text-[10px] text-gray-500 cursor-pointer select-none hover:text-gray-300">
            <input
              type="checkbox"
              checked={allChecked}
              ref={el => { if (el) el.indeterminate = !allChecked && !noneChecked; }}
              onChange={toggleAll}
              className="accent-orange-500"
            />
            all
          </label>
        </div>
        <div className="grid grid-cols-3 gap-x-4 gap-y-1">
          {PROVISION_TOOLS.map(tool => (
            <label key={tool} className="flex items-center gap-1.5 text-[10px] text-gray-400 cursor-pointer hover:text-gray-200 select-none">
              <input
                type="checkbox"
                checked={selectedTools.has(tool)}
                onChange={() => toggleTool(tool)}
                className="accent-orange-500"
              />
              {tool}
            </label>
          ))}
        </div>
      </div>

      <div className="flex gap-2 pt-1">
        <button
          onClick={onConfirm}
          disabled={noneChecked}
          className="px-3 py-1 bg-orange-500/30 hover:bg-orange-500/50 text-orange-200 border border-orange-500/50 rounded text-[11px] disabled:opacity-40"
        >
          Run
        </button>
        <button
          onClick={onCancel}
          className="px-3 py-1 bg-gray-700 hover:bg-gray-600 text-gray-400 rounded text-[11px]"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}

// ── PhaseRow ──────────────────────────────────────────────────────────────────

function PhaseRow({
  stageKey,
  info,
  percent,
  optionsOpen,
  onRunRequest,
  buildLog,
  optionsPanel,
}: {
  stageKey: StageKey;
  info: BuildStageInfo;
  percent?: number | null;
  optionsOpen: boolean;
  onRunRequest: (s: StageKey) => void;
  buildLog?: string;
  optionsPanel?: ReactNode;
}) {
  const [showLog, setShowLog] = useState(info.status !== 'done' && !!(info.error || buildLog));

  useEffect(() => {
    if (info.status !== 'done' && (info.error || buildLog)) setShowLog(true);
  }, [info.status, info.error, buildLog]);

  const label = info.label ?? STAGE_LABELS[stageKey];
  const substage = info.substage;
  const hasLog = !!(info.error || buildLog);
  // download_ipsw can't be re-run independently — it's part of the orchestrator
  const canRun = stageKey !== 'download_ipsw';
  const hasOptions = !!STAGE_HAS_OPTIONS[stageKey];

  const runLabel = info.status === 'failed' ? 'Retry' : info.status === 'done' ? 'Re-run' : 'Run';

  return (
    <div className={`border rounded px-3 py-2 text-xs ${statusColors(info.status)}`}>
      <div className="flex items-center gap-3">
        <span className="w-4 text-center">{statusDot(info.status)}</span>
        <span className="flex-1">{label}</span>
        {info.started && (
          <span className="text-gray-600 text-[10px]">
            {formatTime(info.started)}
            {info.finished && ` → ${formatTime(info.finished)}`}
          </span>
        )}
        {hasLog && (
          <button onClick={() => setShowLog(v => !v)} className="text-gray-600 hover:text-gray-400 text-[10px]">
            {showLog ? 'hide' : 'log'}
          </button>
        )}
        {canRun && (
          <button
            onClick={() => onRunRequest(stageKey)}
            disabled={info.status === 'running'}
            className={`px-2 py-0.5 rounded text-[10px] disabled:opacity-40 ${
              optionsOpen && hasOptions
                ? 'bg-orange-600/40 text-orange-200 hover:bg-orange-600/50'
                : 'bg-gray-700 hover:bg-gray-600 text-gray-200'
            }`}
          >
            {optionsOpen && hasOptions ? `${runLabel} ▲` : hasOptions ? `${runLabel} ▾` : runLabel}
          </button>
        )}
      </div>
      {/* Progress bar for download stage */}
      {stageKey === 'download_ipsw' && percent != null && (
        <div className="mt-1.5 ml-7 space-y-1">
          <div className="h-1.5 bg-gray-800 rounded overflow-hidden">
            <div className="h-full bg-blue-500 rounded transition-all" style={{ width: `${percent}%` }} />
          </div>
          {substage && <div className="text-[10px] text-gray-500">{substage}</div>}
        </div>
      )}
      {/* Substage text for other running stages */}
      {stageKey !== 'download_ipsw' && substage && info.status === 'running' && (
        <div className="mt-1 ml-7 text-[10px] text-yellow-400/70 italic">{substage}</div>
      )}
      {/* Inline options panel */}
      {optionsOpen && optionsPanel}
      {showLog && (info.error || buildLog) && (
        <pre className="mt-2 ml-7 text-[10px] text-gray-400 max-h-48 overflow-y-auto bg-black/40 p-2 rounded whitespace-pre-wrap">
          {info.error ?? buildLog}
        </pre>
      )}
    </div>
  );
}

// ── StageRunner ───────────────────────────────────────────────────────────────

interface Props {
  vm: VMData;
  onRefresh: () => void;
}

export function StageRunner({ vm, onRefresh }: Props) {
  const [error, setError] = useState<string | null>(null);

  // Per-stage options state
  const [pendingStage, setPendingStage] = useState<StageKey | null>(null);
  const [ipswInput, setIpswInput] = useState('');
  const [xcodeInput, setXcodeInput] = useState('');  // '' = skip, path = selected
  const [selectedTools, setSelectedTools] = useState<Set<ToolId>>(() => new Set(PROVISION_TOOLS));

  // Xcode installations for provision_vm picker
  const { data: xcodeInfo } = useXcode();

  // Poll build-state every 3s — this is the authoritative source
  const { data: buildState, refresh: refreshBuildState } = useBuildState(vm.name, 3000);

  // Keep a ref so polling callbacks always read the latest stages
  const stagesRef = useRef<Record<StageKey, BuildStageInfo>>({} as Record<StageKey, BuildStageInfo>);

  // Merge db stages + state.json into a single view
  const mergedStages: Record<StageKey, BuildStageInfo> = (() => {
    const base: Record<StageKey, BuildStageInfo> = {
      download_ipsw:   { status: 'pending' },
      create_vm:       { status: 'pending' },
      setup_assistant: { status: 'pending' },
      disable_sip:     { status: 'pending' },
      provision_vm:    { status: 'pending' },
    };

    // Seed from DB stages
    for (const s of vm.stages) {
      const key = s.stage as StageKey;
      if (key in base) {
        base[key] = {
          status: s.status as BuildStageInfo['status'],
          error: s.output ?? undefined,
          started: s.last_run_at ? new Date(s.last_run_at * 1000).toISOString() : undefined,
        };
      }
    }

    // Overlay state.json — overwrites DB values, except when DB says 'running'.
    // The DB is updated first (before the script writes state.json), so a stale
    // state.json 'done' must not overwrite a freshly-started 'running' DB stage.
    if (buildState?.stages) {
      for (const [stateKey, info] of Object.entries(buildState.stages)) {
        const key = STATE_KEY_MAP[stateKey];
        if (key) {
          const dbStatus = base[key]?.status;
          if (dbStatus === 'running' && (info as BuildStageInfo).status !== 'running') continue;
          base[key] = info as BuildStageInfo;
        }
      }
    }

    // G38: If the build process is dead (backend set status='stale'), unlock all
    // stages stuck as 'running' so the user can re-run them.
    if (buildState?.status === 'stale') {
      for (const key of STAGE_ORDER) {
        if (base[key].status === 'running') {
          base[key] = { ...base[key], status: 'failed', error: base[key].error ?? 'Process exited unexpectedly' };
        }
      }
    }

    // Inject top-level substage into any running stage that lacks per-stage substage
    if (buildState?.substage && buildState.status === 'running') {
      for (const key of STAGE_ORDER) {
        if (base[key].status === 'running' && !base[key].substage) {
          base[key] = { ...base[key], substage: buildState.substage };
        }
      }
    }

    return base;
  })();

  useEffect(() => { stagesRef.current = mergedStages; });

  async function runStageWithOpts(stage: StageKey, opts: StageRunOpts) {
    setError(null);
    try {
      await apiPost(`/vms/${vm.name}/stages/${stage}/run`, {
        ipsw: opts.ipsw || 'latest',
        xcode: opts.xcode || undefined,
        tools: opts.tools || undefined,
      });

      // Refresh immediately so the 'running' state is visible without waiting 3s
      onRefresh();
      refreshBuildState();

      let polls = 0;
      const poll = setInterval(() => {
        polls++;
        if (polls > 240) { clearInterval(poll); return; }
        onRefresh();
        refreshBuildState();
        const s = stagesRef.current[stage];
        if (s && s.status !== 'running') clearInterval(poll);
      }, 3000);
    } catch (err) {
      setError(String(err));
    }
  }

  function handleRunRequest(stage: StageKey) {
    if (STAGE_HAS_OPTIONS[stage]) {
      // Toggle: if already open for this stage, close; otherwise open
      setPendingStage(prev => prev === stage ? null : stage);
    } else {
      runStageWithOpts(stage, {});
    }
  }

  function handleConfirmRun() {
    if (!pendingStage) return;
    const opts: StageRunOpts = {};

    if (pendingStage === 'create_vm') {
      if (ipswInput.trim()) opts.ipsw = ipswInput.trim();
    }

    if (pendingStage === 'provision_vm') {
      if (xcodeInput.trim()) opts.xcode = xcodeInput.trim();
      const allSelected = PROVISION_TOOLS.every(t => selectedTools.has(t));
      opts.tools = allSelected ? 'all' : [...selectedTools].join(',');
    }

    runStageWithOpts(pendingStage, opts);
    setPendingStage(null);
  }

  function handleCancelOptions() {
    setPendingStage(null);
  }

  function runAllFromFirst() {
    const firstIncomplete = STAGE_ORDER.filter(s => s !== 'download_ipsw').find(s => mergedStages[s].status !== 'done');
    if (firstIncomplete) {
      if (STAGE_HAS_OPTIONS[firstIncomplete]) {
        setPendingStage(firstIncomplete);
      } else {
        runStageWithOpts(firstIncomplete, {});
      }
    }
  }

  const anyRunning = STAGE_ORDER.some(s => mergedStages[s].status === 'running');
  const lastUpdated = buildState?.updated;
  const isStaleState = lastUpdated && buildState?.status === 'running'
    && (Date.now() - new Date(lastUpdated).getTime()) > 5 * 60 * 1000;

  // Build per-stage options panels
  function getOptionsPanel(stage: StageKey): ReactNode | undefined {
    if (stage === 'create_vm') {
      return (
        <CreateVmOptions
          ipswInput={ipswInput}
          setIpswInput={setIpswInput}
          onConfirm={handleConfirmRun}
          onCancel={handleCancelOptions}
        />
      );
    }
    if (stage === 'provision_vm') {
      return (
        <ProvisionVmOptions
          xcodeInput={xcodeInput}
          setXcodeInput={setXcodeInput}
          selectedTools={selectedTools}
          setSelectedTools={setSelectedTools}
          xcodeApps={xcodeInfo?.apps ?? []}
          onConfirm={handleConfirmRun}
          onCancel={handleCancelOptions}
        />
      );
    }
    return undefined;
  }

  return (
    <div className="p-4 space-y-2">
      {error && (
        <div className="text-red-300 text-xs bg-red-900/20 border border-red-800 rounded p-2">{error}</div>
      )}

      {/* Build metadata */}
      {buildState && buildState.status !== 'no_state' && (
        <div className="flex items-center gap-3 text-[10px] text-gray-600 mb-2 flex-wrap">
          <span>Status: <span className={
            isStaleState ? 'text-amber-500' :
            buildState.status === 'done' ? 'text-green-500' :
            buildState.status === 'failed' ? 'text-red-400' :
            'text-yellow-400'
          }>{isStaleState ? 'stale' : buildState.status}</span></span>
          {lastUpdated && (
            <span>
              Updated: {new Date(lastUpdated).toLocaleTimeString()}
              {isStaleState && (
                <span className="ml-1.5 px-1 py-0.5 rounded text-[9px] bg-amber-900/40 text-amber-400 border border-amber-700/50" title="state.json hasn't been updated in over 5 minutes — script may be stale or crashed">
                  stale
                </span>
              )}
            </span>
          )}
          {(buildState.hostname || buildState.ip) && (
            <span className="text-cyan-700">{buildState.hostname ?? buildState.ip}</span>
          )}
          {buildState.recordings?.length ? <span className="text-cyan-700">● {buildState.recordings.length} recording{buildState.recordings.length > 1 ? 's' : ''}</span> : null}
        </div>
      )}

      <div className="flex justify-end mb-2">
        <button
          onClick={runAllFromFirst}
          disabled={anyRunning}
          className="text-xs px-3 py-1.5 bg-orange-500/20 text-orange-300 border border-orange-500/40 rounded hover:bg-orange-500/30 disabled:opacity-40"
        >
          Run all incomplete
        </button>
      </div>

      {STAGE_ORDER.map(stage => (
        <PhaseRow
          key={stage}
          stageKey={stage}
          info={mergedStages[stage]}
          percent={stage === 'download_ipsw' ? buildState?.percent : null}
          optionsOpen={pendingStage === stage}
          onRunRequest={handleRunRequest}
          optionsPanel={getOptionsPanel(stage)}
        />
      ))}
    </div>
  );
}
