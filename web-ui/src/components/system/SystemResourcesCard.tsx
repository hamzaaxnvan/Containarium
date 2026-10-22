'use client';

import { useState, useEffect } from 'react';
import { Cpu, HardDrive, MemoryStick, CircuitBoard } from 'lucide-react';
import { SystemInfo, BackendInfo, gpuVendorDisplayName, gpuModelDisplayName } from '@/src/types/container';

interface SystemResourcesCardProps {
  systemInfo: SystemInfo | null;
  backends?: BackendInfo[];
  onSelectBackend?: (backendId: string) => Promise<SystemInfo | null>;
}

function formatBytes(b: number): string {
  if (b === 0) return '0 B';
  const k = 1024, sz = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(b) / Math.log(k));
  return parseFloat((b / Math.pow(k, i)).toFixed(1)) + ' ' + sz[i];
}

function barColor(pct: number) {
  if (pct > 80) return 'bg-red-500';
  if (pct > 60) return 'bg-amber-500';
  return 'bg-emerald-500';
}

function ResourceItem({ icon: Icon, label, value, sub, pct, title }: { icon: React.ElementType; label: string; value: string; sub?: string; pct?: number; title?: string }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-1.5">
        <Icon size={12} className="text-[var(--text-muted)]" />
        <span className="text-[11px] font-medium text-[var(--text-secondary)]">{label}</span>
      </div>
      <div className="flex items-baseline gap-1">
        <span className="text-sm font-semibold text-[var(--text)]">{value}</span>
        {sub && <span className="text-xs text-[var(--text-muted)]">{sub}</span>}
      </div>
      {pct !== undefined && (
        <div className="h-1.5 w-full rounded-full bg-zinc-800 overflow-hidden" title={title}>
          <div className={`h-full rounded-full transition-all ${barColor(pct)}`} style={{ width: `${Math.min(pct, 100)}%` }} />
        </div>
      )}
    </div>
  );
}

export default function SystemResourcesCard({ systemInfo, backends, onSelectBackend }: SystemResourcesCardProps) {
  const [selectedBackend, setSelectedBackend] = useState('');
  const [backendSystemInfo, setBackendSystemInfo] = useState<SystemInfo | null>(null);

  const activeInfo = selectedBackend && backendSystemInfo ? backendSystemInfo : systemInfo;

  const handleBackendChange = async (id: string) => {
    setSelectedBackend(id);
    if (!id || !onSelectBackend) { setBackendSystemInfo(null); return; }
    try { setBackendSystemInfo(await onSelectBackend(id)); } catch { setBackendSystemInfo(null); }
  };

  useEffect(() => {
    if (!selectedBackend || !onSelectBackend) return;
    const id = setInterval(async () => {
      try { setBackendSystemInfo(await onSelectBackend(selectedBackend)); } catch { /* ignore */ }
    }, 60000);
    return () => clearInterval(id);
  }, [selectedBackend, onSelectBackend]);

  if (!activeInfo) return null;

  const cpuLoad1 = activeInfo.cpuLoad1min || 0;
  const cpuPct = activeInfo.totalCpus > 0 ? Math.min((cpuLoad1 / activeInfo.totalCpus) * 100, 100) : 0;
  const memUsed = (activeInfo.totalMemoryBytes || 0) - (activeInfo.availableMemoryBytes || 0);
  const memPct = activeInfo.totalMemoryBytes ? (memUsed / activeInfo.totalMemoryBytes) * 100 : 0;
  const diskUsed = (activeInfo.totalDiskBytes || 0) - (activeInfo.availableDiskBytes || 0);
  const diskPct = activeInfo.totalDiskBytes ? (diskUsed / activeInfo.totalDiskBytes) * 100 : 0;

  const hasData = activeInfo.totalCpus > 0 || activeInfo.totalMemoryBytes > 0 || activeInfo.totalDiskBytes > 0;
  if (!hasData) return null;

  const showBackendSelector = backends && backends.length > 1;

  return (
    <div className="mb-4 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] p-4">
      <div className="mb-3 flex items-center justify-between">
        <span className="text-xs font-medium text-[var(--text-muted)]">
          System Resources{activeInfo.hostname ? ` — ${activeInfo.hostname}` : ''}
        </span>
        {showBackendSelector && (
          <div className="flex rounded-md border border-[var(--border-subtle)] overflow-hidden">
            {backends!.map(b => (
              <button
                key={b.id}
                onClick={() => handleBackendChange(selectedBackend === b.id ? '' : b.id)}
                disabled={!b.healthy}
                className={`px-2.5 py-1 text-[10px] font-medium transition-colors disabled:opacity-40 ${selectedBackend === b.id ? 'bg-[var(--accent)] text-white' : 'bg-[var(--surface)] text-[var(--text-secondary)] hover:bg-[var(--surface-2)]'}`}
              >
                {b.id}
              </button>
            ))}
          </div>
        )}
      </div>

      <div className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3 lg:grid-cols-4">
        {activeInfo.totalCpus > 0 && (
          <ResourceItem
            icon={Cpu}
            label="CPU Load"
            value={cpuLoad1.toFixed(2)}
            sub={`/ ${activeInfo.totalCpus} cores`}
            pct={cpuPct}
            title={`${cpuPct.toFixed(1)}% utilized (1-min avg)`}
          />
        )}
        {activeInfo.totalMemoryBytes > 0 && (
          <ResourceItem
            icon={MemoryStick}
            label="Memory"
            value={formatBytes(memUsed)}
            sub={`/ ${formatBytes(activeInfo.totalMemoryBytes)}`}
            pct={memPct}
            title={`${memPct.toFixed(1)}% used`}
          />
        )}
        {activeInfo.totalDiskBytes > 0 && (
          <ResourceItem
            icon={HardDrive}
            label="Storage"
            value={formatBytes(diskUsed)}
            sub={`/ ${formatBytes(activeInfo.totalDiskBytes)}`}
            pct={diskPct}
            title={`${diskPct.toFixed(1)}% used`}
          />
        )}
        {activeInfo.gpus?.map((gpu, idx) => (
          <div key={gpu.pciAddress || idx} className="flex flex-col gap-1.5">
            <div className="flex items-center gap-1.5">
              <CircuitBoard size={12} className="text-[var(--text-muted)]" />
              <span className="text-[11px] font-medium text-[var(--text-secondary)]">GPU{activeInfo.gpus!.length > 1 ? ` #${idx}` : ''}</span>
            </div>
            <span className="text-sm font-semibold text-[var(--text)]">{gpuModelDisplayName(gpu.model, gpu.modelName)}</span>
            <span className="text-[10px] text-[var(--text-muted)]">
              {gpuVendorDisplayName(gpu.vendor)}
              {gpu.driverVersion ? ` · Driver ${gpu.driverVersion}` : ''}
              {gpu.cudaVersion ? ` · CUDA ${gpu.cudaVersion}` : ''}
              {gpu.vramBytes > 0 ? ` · ${formatBytes(gpu.vramBytes)} VRAM` : ''}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}
