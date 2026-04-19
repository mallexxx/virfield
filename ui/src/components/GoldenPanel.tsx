import { useState } from 'react';
import { useGet, apiPost } from '../hooks/useAPI.ts';
import { useVMs } from '../hooks/useAPI.ts';
import { GHCRPushModal } from './GHCRPushModal.tsx';

interface GoldenVersion {
  id: string;
  vm_name: string;
  macos_version: string | null;
  xcode_version: string | null;
  promoted_at: number;
  notes: string | null;
}

function fmtDate(epochSecs: number) {
  return new Date(epochSecs * 1000).toLocaleString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit',
  });
}

export function GoldenPanel() {
  const { data: vms, loading: vmsLoading, refresh: refreshVMs } = useVMs();
  const { data: goldenData, refresh: refreshVersions } = useGet<{ versions: GoldenVersion[] }>('/golden');
  const goldenVMs = (vms ?? []).filter(v => v.meta?.tag === 'golden');
  const versions = goldenData?.versions ?? [];
  const [pushTarget, setPushTarget] = useState<string | null>(null);

  function refresh() { refreshVMs(); refreshVersions(); }

  async function cloneGolden(name: string) {
    const dest = `${name.replace(/-golden$/, '')}-run-${Date.now()}`;
    await apiPost(`/vms/${name}/clone`, { destName: dest });
    alert(`Cloned → ${dest}`);
    refresh();
  }

  return (
    <div>
      <div className="flex items-center gap-3 mb-4">
        <h2 className="text-gray-200 font-semibold text-sm">Golden Images</h2>
        <button onClick={refresh} className="text-xs text-gray-500 hover:text-gray-300 px-2 py-1.5 hover:bg-gray-800 rounded">↻</button>
      </div>

      {vmsLoading && <div className="text-gray-600 text-sm py-8 text-center">Loading...</div>}

      {!vmsLoading && goldenVMs.length === 0 && (
        <div className="text-center py-8 text-gray-600">
          <div className="text-4xl mb-3">🏅</div>
          <div className="text-sm">No golden VMs yet.</div>
          <div className="text-xs mt-1 text-gray-700">
            Tag a VM as &ldquo;golden&rdquo; or use &ldquo;Set as Golden&rdquo; from a VM card.
          </div>
        </div>
      )}

      <div className="space-y-3">
        {goldenVMs.map(vm => (
          <div key={vm.name} className="border border-yellow-800/40 rounded-lg p-4 bg-yellow-900/10">
            <div className="flex items-start justify-between">
              <div>
                <div className="flex items-center gap-2">
                  <span className="text-yellow-300 font-semibold text-sm">{vm.name}</span>
                  <span className="text-xs text-yellow-600">{vm.status}</span>
                </div>
                <div className="mt-1 text-xs text-gray-500 flex gap-3">
                  {vm.meta?.macos_version && <span>macOS {vm.meta.macos_version}</span>}
                  {vm.meta?.xcode_version && <span>Xcode {vm.meta.xcode_version}</span>}
                  <span>{vm.cpuCount} vCPU · {(vm.memorySize / 1e9).toFixed(0)} GB RAM</span>
                </div>
                <div className="mt-2 flex gap-1 flex-wrap">
                  {vm.stages.map(s => (
                    <span
                      key={s.stage}
                      title={s.stage}
                      className={`text-[10px] px-1.5 py-0.5 rounded border ${
                        s.status === 'done'   ? 'bg-green-900/30 text-green-400 border-green-800/40' :
                        s.status === 'failed' ? 'bg-red-900/30 text-red-400 border-red-800/40' :
                                                'bg-gray-800 text-gray-600 border-gray-700'
                      }`}
                    >
                      {s.stage.replace(/_/g, ' ')}
                    </span>
                  ))}
                </div>
              </div>
              <div className="flex gap-2 flex-shrink-0">
                <button
                  onClick={() => setPushTarget(vm.name)}
                  className="text-xs px-3 py-1.5 bg-blue-500/20 text-blue-300 border border-blue-500/40 rounded hover:bg-blue-500/30"
                >
                  ↑ Push to GHCR
                </button>
                <button
                  onClick={() => cloneGolden(vm.name)}
                  className="text-xs px-3 py-1.5 bg-yellow-500/20 text-yellow-300 border border-yellow-500/40 rounded hover:bg-yellow-500/30"
                >
                  Clone for test run
                </button>
              </div>
            </div>
          </div>
        ))}
      </div>

      {/* Version history */}
      <div className="mt-6">
        <h3 className="text-xs text-gray-500 uppercase tracking-wider mb-2">Promotion History</h3>
        {versions.length === 0 ? (
          <div className="text-xs text-gray-700 py-4 text-center">No promotions recorded yet.</div>
        ) : (
          <div className="border border-gray-800 rounded-lg overflow-hidden">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-gray-800 bg-gray-900/60">
                  <th className="text-left px-3 py-2 text-gray-500 font-medium">VM</th>
                  <th className="text-left px-3 py-2 text-gray-500 font-medium">macOS</th>
                  <th className="text-left px-3 py-2 text-gray-500 font-medium">Xcode</th>
                  <th className="text-left px-3 py-2 text-gray-500 font-medium">Promoted</th>
                  <th className="text-left px-3 py-2 text-gray-500 font-medium">Notes</th>
                </tr>
              </thead>
              <tbody>
                {versions.map((v, i) => (
                  <tr key={v.id} className={`border-b border-gray-800/50 ${i % 2 === 0 ? '' : 'bg-gray-900/30'}`}>
                    <td className="px-3 py-2 text-gray-300 font-mono">{v.vm_name}</td>
                    <td className="px-3 py-2 text-gray-400">{v.macos_version ?? '—'}</td>
                    <td className="px-3 py-2 text-gray-400">{v.xcode_version ?? '—'}</td>
                    <td className="px-3 py-2 text-gray-500">{fmtDate(v.promoted_at)}</td>
                    <td className="px-3 py-2 text-gray-600 italic">{v.notes ?? ''}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* Push modal */}
      {pushTarget && (
        <GHCRPushModal
          vmName={pushTarget}
          onClose={() => setPushTarget(null)}
        />
      )}
    </div>
  );
}
