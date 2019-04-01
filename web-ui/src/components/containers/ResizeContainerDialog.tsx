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
