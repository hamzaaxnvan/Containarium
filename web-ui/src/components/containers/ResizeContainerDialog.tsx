'use client';

import { useState, useEffect } from 'react';
import { Loader2, Trash2, AlertTriangle, XCircle, CheckCircle } from 'lucide-react';
import { Modal, ModalBtn } from '@/src/components/ui/Modal';
import { AxiosError } from 'axios';

interface ResizeContainerDialogProps {
  open: boolean;
  onClose: () => void;
  containerName: string;
  username: string;
  currentCpu: string;
  currentMemory: string;
  currentDisk: string;
  memoryUsageBytes?: number;
  diskUsageBytes?: number;
  onResize: (resources: { cpu?: string; memory?: string; disk?: string }) => Promise<void>;
  onCleanupDisk?: () => Promise<{ message: string; freedBytes: number }>;
}

function parseSize(s: string): { value: number; unit: string } {
  if (!s) return { value: 0, unit: 'GB' };
  const m = s.match(/^([\d.]+)\s*(MB|GB|TB|M|G|T)?$/i);
  if (!m) return { value: 0, unit: 'GB' };
  const v = parseFloat(m[1]);
  let u = (m[2] || 'GB').toUpperCase();
  if (u === 'M') u = 'MB'; if (u === 'G') u = 'GB'; if (u === 'T') u = 'TB';
  return { value: v, unit: u };
}

function toBytes(v: number, u: string): number {
  return v * ({ MB: 1048576, GB: 1073741824, TB: 1099511627776 }[u] || 1073741824);
}

function formatBytes(b: number): string {
  if (b === 0) return '0 B';
  const k = 1024, sz = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(b) / Math.log(k));
  return parseFloat((b / Math.pow(k, i)).toFixed(1)) + ' ' + sz[i];
}

function ResourceRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs font-medium text-[var(--text-secondary)]">{label}</p>
      {children}
    </div>
  );
}

export default function ResizeContainerDialog({
  open, onClose, containerName, currentCpu, currentMemory, currentDisk,
  memoryUsageBytes, diskUsageBytes, onResize, onCleanupDisk,
}: ResizeContainerDialogProps) {
  const [cpuValue, setCpuValue] = useState(4);
  const [memoryValue, setMemoryValue] = useState(4);
  const [memoryUnit, setMemoryUnit] = useState('GB');
  const [diskValue, setDiskValue] = useState(50);
  const [diskUnit, setDiskUnit] = useState('GB');
  const [originalCpu, setOriginalCpu] = useState(4);
  const [originalMemoryBytes, setOriginalMemoryBytes] = useState(0);
  const [originalDiskBytes, setOriginalDiskBytes] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [cleaning, setCleaning] = useState(false);
  const [cleanupResult, setCleanupResult] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      const cpu = parseInt(currentCpu) || 4;
      setCpuValue(cpu); setOriginalCpu(cpu);
      const mem = parseSize(currentMemory);
      setMemoryValue(mem.value || 4); setMemoryUnit(mem.unit || 'GB');
      setOriginalMemoryBytes(toBytes(mem.value || 4, mem.unit || 'GB'));
      const disk = parseSize(currentDisk);
      setDiskValue(disk.value || 50); setDiskUnit(disk.unit || 'GB');
      setOriginalDiskBytes(toBytes(disk.value || 50, disk.unit || 'GB'));
      setError(null); setCleanupResult(null);
    }
  }, [open, currentCpu, currentMemory, currentDisk]);

  const handleCleanup = async () => {
    if (!onCleanupDisk) return;
    setCleaning(true); setError(null); setCleanupResult(null);
    try {
      const result = await onCleanupDisk();
      setCleanupResult(result.message);
    } catch (err) {
      const axiosErr = err as AxiosError<{ error?: string; message?: string }>;
      setError(axiosErr.response?.data?.error || axiosErr.response?.data?.message || (err instanceof Error ? err.message : String(err)));
    } finally {
      setCleaning(false);
    }
  };

  const handleSave = async () => {
    const resources: { cpu?: string; memory?: string; disk?: string } = {};
    if (cpuValue !== originalCpu) resources.cpu = cpuValue.toString();
    const newMemBytes = toBytes(memoryValue, memoryUnit);
    if (newMemBytes !== originalMemoryBytes) resources.memory = `${memoryValue}${memoryUnit}`;
    const newDiskBytes = toBytes(diskValue, diskUnit);
    if (newDiskBytes !== originalDiskBytes) {
      if (newDiskBytes < originalDiskBytes) { setError('Disk size can only be increased, not decreased'); return; }
      resources.disk = `${diskValue}${diskUnit}`;
    }
    if (Object.keys(resources).length === 0) { onClose(); return; }
    setSaving(true); setError(null);
    try {
      await onResize(resources);
      onClose();
    } catch (err) {
      const axiosErr = err as AxiosError<{ error?: string; message?: string }>;
      setError(axiosErr.response?.data?.error || axiosErr.response?.data?.message || (err instanceof Error ? err.message : String(err)));
    } finally {
      setSaving(false);
    }
  };

  const hasChanges = () => cpuValue !== originalCpu || toBytes(memoryValue, memoryUnit) !== originalMemoryBytes || toBytes(diskValue, diskUnit) !== originalDiskBytes;
  const diskShrinking = toBytes(diskValue, diskUnit) < originalDiskBytes;
  const diskHighUsage = diskUsageBytes !== undefined && originalDiskBytes > 0 && diskUsageBytes / originalDiskBytes > 0.9;

  return (
    <Modal
      open={open}
      onClose={() => { if (!saving) onClose(); }}
      title={`Resize Container — ${containerName}`}
      footer={
        <>
          <ModalBtn onClick={onClose} disabled={saving}>Cancel</ModalBtn>
          <ModalBtn variant="primary" onClick={handleSave} disabled={saving || !hasChanges() || diskShrinking}>
            {saving && <Loader2 size={13} className="animate-spin" />}
            {saving ? 'Applying…' : 'Apply Changes'}
          </ModalBtn>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        {error && (
          <div className="flex items-start gap-2 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-xs text-[var(--c-red)]">
            <XCircle size={14} className="mt-0.5 shrink-0" />
            <span>{error}</span>
          </div>
        )}
        {cleanupResult && (
          <div className="flex items-start gap-2 rounded-lg border border-emerald-500/30 bg-emerald-500/10 p-3 text-xs text-[var(--c-emerald)]">
            <CheckCircle size={14} className="mt-0.5 shrink-0" />
            <span>{cleanupResult}</span>
          </div>
        )}

        {/* CPU */}
        <ResourceRow label="CPU Cores">
          <input type="range" min={1} max={32} step={1} value={cpuValue} onChange={e => setCpuValue(+e.target.value)} disabled={saving} className="w-full accent-[var(--accent)]" />
          <div className="flex items-center gap-2">
            <input
              type="number" min={1} max={32} value={cpuValue} disabled={saving}
              onChange={e => setCpuValue(Math.min(Math.max(parseInt(e.target.value) || 1, 1), 32))}
              className="w-20 rounded border border-[var(--border-subtle)] bg-[var(--surface-2)] px-2 py-1 text-xs text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
            />
            <span className="text-xs text-[var(--text-muted)]">cores</span>
            {cpuValue !== originalCpu && <span className="text-xs text-[var(--c-blue)]">Changed from {originalCpu}</span>}
          </div>
        </ResourceRow>

        {/* Memory */}
        <ResourceRow label="Memory">
          <input type="range" min={1} max={64} step={1} value={memoryUnit === 'GB' ? memoryValue : memoryValue / 1024} onChange={e => setMemoryValue(memoryUnit === 'GB' ? +e.target.value : +e.target.value * 1024)} disabled={saving} className="w-full accent-[var(--accent)]" />
          <div className="flex items-center gap-2">
            <input
              type="number" min={0.5} step={0.5} value={memoryValue} disabled={saving}
              onChange={e => setMemoryValue(Math.max(parseFloat(e.target.value) || 1, 0.5))}
              className="w-20 rounded border border-[var(--border-subtle)] bg-[var(--surface-2)] px-2 py-1 text-xs text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
            />
            <select value={memoryUnit} onChange={e => setMemoryUnit(e.target.value)} disabled={saving} className="rounded border border-[var(--border-subtle)] bg-[var(--surface-2)] px-2 py-1 text-xs text-[var(--text)] focus:outline-none">
              <option value="MB">MB</option>
              <option value="GB">GB</option>
            </select>
            {memoryUsageBytes !== undefined && memoryUsageBytes > 0 && (
              <span className="text-xs text-[var(--text-muted)]">Using: {formatBytes(memoryUsageBytes)}</span>
            )}
          </div>
        </ResourceRow>

        {/* Disk */}
        <ResourceRow label="Disk Storage">
          <input type="range" min={10} max={500} step={10} value={diskUnit === 'GB' ? diskValue : diskValue * 1024} onChange={e => setDiskValue(diskUnit === 'GB' ? +e.target.value : +e.target.value / 1024)} disabled={saving} className="w-full accent-[var(--accent)]" />
          <div className="flex items-center gap-2">
            <input
              type="number" min={1} step={1} value={diskValue} disabled={saving}
              onChange={e => setDiskValue(Math.max(parseFloat(e.target.value) || 10, 1))}
              className="w-20 rounded border border-[var(--border-subtle)] bg-[var(--surface-2)] px-2 py-1 text-xs text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
            />
            <select value={diskUnit} onChange={e => setDiskUnit(e.target.value)} disabled={saving} className="rounded border border-[var(--border-subtle)] bg-[var(--surface-2)] px-2 py-1 text-xs text-[var(--text)] focus:outline-none">
              <option value="GB">GB</option>
              <option value="TB">TB</option>
            </select>
            {diskUsageBytes !== undefined && diskUsageBytes > 0 && (
              <span className="text-xs text-[var(--text-muted)]">Using: {formatBytes(diskUsageBytes)}</span>
            )}
          </div>
          {diskShrinking && (
            <div className="flex items-center gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 p-2.5 text-xs text-[var(--c-amber)]">
              <AlertTriangle size={13} />
              Disk size can only be increased, not decreased
            </div>
          )}
          <p className="text-[10px] text-[var(--text-muted)]">Disk can only be increased. Changes take effect immediately.</p>
        </ResourceRow>

        {/* Disk cleanup */}
        {onCleanupDisk && (
          <div className="flex flex-col gap-2 border-t border-[var(--border-subtle)] pt-4">
            {diskHighUsage && (
              <div className="flex items-center gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 p-2.5 text-xs text-[var(--c-amber)]">
                <AlertTriangle size={13} />
                Disk usage is high — consider cleaning up before resizing
              </div>
            )}
            <div className="flex items-center gap-2">
              <button
                onClick={handleCleanup}
                disabled={cleaning || saving}
                className="flex items-center gap-1.5 rounded-md border border-[var(--border)] px-3 py-1.5 text-xs text-[var(--text-secondary)] transition-colors hover:bg-[var(--surface-2)] disabled:opacity-50"
              >
                {cleaning ? <Loader2 size={13} className="animate-spin" /> : <Trash2 size={13} />}
                {cleaning ? 'Cleaning…' : 'Clean Up Disk'}
              </button>
              <span className="text-[10px] text-[var(--text-muted)]">Removes temp files, package caches, and trims logs</span>
            </div>
          </div>
        )}
      </div>
    </Modal>
  );
}
