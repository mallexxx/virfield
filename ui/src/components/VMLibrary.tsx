import { useState, useMemo } from 'react';
import { useVMs, useActiveBuilds, apiPost } from '../hooks/useAPI.ts';
import { VMCard } from './VMCard.tsx';
import { VMWizard } from './VMWizard.tsx';
import { PullVMModal } from './PullVMModal.tsx';

export function VMLibrary() {
  const { data: vms, loading, error, refresh } = useVMs();
  const { data: activeBuilds, refresh: refreshBuilds } = useActiveBuilds();
  const [showWizard, setShowWizard] = useState(false);
  const [showPull, setShowPull] = useState(false);
  const [filter, setFilter] = useState('');

  // Set of all VM names currently being built (golden + base + nosip)
  const activeBuildVmIds = useMemo(() => {
    const ids = new Set<string>();
    for (const b of activeBuilds ?? []) {
      for (const id of b.vmIds) ids.add(id);
    }
    return ids;
  }, [activeBuilds]);

  async function stopBuild(vmId: string) {
    try {
      await apiPost(`/vms/${vmId}/stop-build`);
    } catch { /* ignore */ }
    refresh();
    refreshBuilds();
  }

  const filtered = (vms ?? []).filter(vm =>
    vm.name.toLowerCase().includes(filter.toLowerCase())
  );

  return (
    <div>
      <div className="flex items-center gap-3 mb-4">
        <h2 className="text-gray-200 font-semibold text-sm">VM Library</h2>
        <input
          type="text"
          placeholder="Filter VMs..."
          value={filter}
          onChange={e => setFilter(e.target.value)}
          className="bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60 w-48"
        />
        <div className="flex-1" />
        <button
          onClick={refresh}
          className="text-xs text-gray-500 hover:text-gray-300 px-2 py-1.5 hover:bg-gray-800 rounded"
        >
          ↻ Refresh
        </button>
        <button
          onClick={() => setShowPull(true)}
          className="text-xs px-3 py-1.5 bg-blue-500/20 text-blue-300 border border-blue-500/40 rounded hover:bg-blue-500/30"
        >
          ⬇ Pull VM
        </button>
        <button
          onClick={() => setShowWizard(true)}
          className="text-xs px-3 py-1.5 bg-orange-500/20 text-orange-300 border border-orange-500/40 rounded hover:bg-orange-500/30"
        >
          + New VM
        </button>
      </div>

      {loading && (
        <div className="text-gray-600 text-sm text-center py-12">Loading VMs...</div>
      )}

      {error && (
        <div className="bg-red-900/20 border border-red-800 rounded p-4 text-red-300 text-sm">
          <strong>Error:</strong> {error}
          <br />
          <span className="text-xs text-red-400/70">Is the backend running? Run: <code>npm run dev</code></span>
        </div>
      )}

      {!loading && !error && filtered.length === 0 && (
        <div className="text-center py-16 text-gray-600">
          <div className="text-4xl mb-3">🦆</div>
          <div className="text-sm">No VMs found. Create one with the &ldquo;+ New VM&rdquo; button.</div>
        </div>
      )}

      <div className="space-y-3">
        {filtered.map(vm => (
          <VMCard
            key={vm.name}
            vm={vm}
            onRefresh={refresh}
            isBeingBuilt={activeBuildVmIds.has(vm.name) || vm.building === true}
            onStopBuild={stopBuild}
          />
        ))}
      </div>

      {showWizard && (
        <VMWizard
          onClose={() => setShowWizard(false)}
          onCreated={() => { setShowWizard(false); refresh(); }}
        />
      )}
      {showPull && (
        <PullVMModal
          onClose={() => setShowPull(false)}
          onPulled={() => { setShowPull(false); refresh(); }}
        />
      )}
    </div>
  );
}
