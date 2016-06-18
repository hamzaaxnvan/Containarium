'use client';

import {
  Trash2, Play, Square, Terminal, Monitor, Shield,
  Tag, SlidersHorizontal, Users, Moon, ShieldAlert,
} from 'lucide-react';
import { Container, ContainerState, ContainerMetricsWithRate } from '@/src/types/container';
import { SecurityBadge } from '@/src/lib/hooks/useSecurity';

interface ContainerListViewProps {
  containers: Container[];
  metricsMap: Record<string, ContainerMetricsWithRate>;
  securityBadgesMap?: Record<string, SecurityBadge | null>;
  onDelete: (username: string) => void;
  onStart?: (username: string) => void;
  onStop?: (username: string) => void;
  onTerminal?: (username: string) => void;
  onEditFirewall?: (username: string) => void;
  onEditLabels?: (username: string, labels: Record<string, string>) => void;
  onResize?: (username: string, currentResources: { cpu: string; memory: string; disk: string }) => void;
  onManageCollaborators?: (username: string) => void;
  onToggleAutoSleep?: (username: string, current: { enabled: boolean; threshold: number }) => void;
  onSecurityClick?: (containerName: string) => void;
}

function parseSize(s: string): number {
  if (!s) return 0;
  const m = s.match(/^([\d.]+)\s*(B|KB|MB|GB|TB|K|M|G|T)?$/i);
  if (!m) return 0;
  const v = parseFloat(m[1]);
  const u = (m[2] || 'B').toUpperCase();
  const mul: Record<string, number> = { B: 1, K: 1024, KB: 1024, M: 1048576, MB: 1048576, G: 1073741824, GB: 1073741824, T: 1099511627776, TB: 1099511627776 };
  return v * (mul[u] || 1);
}

function formatBytes(b: number): string {
  if (b === 0) return '0 B';
  const k = 1024, sz = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(b) / Math.log(k));
  return parseFloat((b / Math.pow(k, i)).toFixed(1)) + ' ' + sz[i];
}

function stateBadge(state: ContainerState) {
  switch (state) {
    case 'Running':     return 'bg-emerald-500/15 text-[var(--c-emerald)] border-emerald-500/30';
    case 'Stopped':     return 'bg-red-500/15 text-[var(--c-red)] border-red-500/30';
    case 'Frozen':
    case 'Creating':
    case 'Provisioning': return 'bg-amber-500/15 text-[var(--c-amber)] border-amber-500/30';
    default:            return 'bg-zinc-500/15 text-zinc-400 border-zinc-500/30';
  }
}

function MiniBar({ pct }: { pct: number }) {
  const color = pct > 80 ? 'bg-red-500' : pct > 60 ? 'bg-amber-500' : 'bg-emerald-500';
  return (
    <div className="h-1 w-20 rounded-full bg-zinc-800">
      <div className={`h-full rounded-full ${color}`} style={{ width: `${Math.min(pct, 100)}%` }} />
    </div>
  );
}

function IconBtn({ title, onClick, className = '', children }: { title: string; onClick: () => void; className?: string; children: React.ReactNode }) {
  return (
    <button
      title={title}
      onClick={onClick}
      className={`rounded p-1 text-[var(--text-muted)] transition-colors hover:text-[var(--text)] hover:bg-[var(--surface-2)] ${className}`}
    >
      {children}
    </button>
  );
}

export default function ContainerListView({
  containers, metricsMap, securityBadgesMap, onDelete, onStart, onStop, onTerminal,
  onEditFirewall, onEditLabels, onResize, onManageCollaborators, onToggleAutoSleep,
  onSecurityClick,
}: ContainerListViewProps) {
  return (
    <div className="overflow-x-auto rounded-xl border border-[var(--border-subtle)]">
      <table className="w-full text-xs">
        <thead>
          <tr className="border-b border-[var(--border-subtle)] bg-[var(--surface)]">
            {['Name', 'State', 'IP Address', 'CPU', 'Memory', 'Disk', 'Node', 'Labels', ''].map((h) => (
              <th key={h} className="px-3 py-2.5 text-left font-medium text-[var(--text-secondary)] whitespace-nowrap last:text-right">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {containers.map((c) => {
            const metrics = metricsMap[c.name];
            const isRunning = c.state === 'Running';
            const username = c.username || c.name;

            const memLimit = parseSize(c.memory);
            const diskLimit = parseSize(c.disk);
            const cpuCores = parseInt(c.cpu) || 0;
            const memUsed = metrics?.memoryUsageBytes || 0;
            const diskUsed = metrics?.diskUsageBytes || 0;
            const cpuPct = metrics?.cpuUsagePercent || 0;
            const memPct = memLimit > 0 ? (memUsed / memLimit) * 100 : 0;
            const diskPct = diskLimit > 0 ? (diskUsed / diskLimit) * 100 : 0;

            return (
              <tr key={c.name} className="border-b border-[var(--border-subtle)] transition-colors hover:bg-[var(--surface)]">
                {/* Name */}
                <td className="px-3 py-2.5">
                  <p className="font-medium text-[var(--text)]">{username}</p>
                  {c.image && <p className="text-[var(--text-muted)]">{c.image}</p>}
                </td>

                {/* State */}
                <td className="px-3 py-2.5">
                  <div className="flex items-center flex-wrap gap-1">
                    {c.state === 'Stopped' && c.autoSleepEnabled ? (
                      <span className="inline-flex items-center gap-1 rounded-full border border-indigo-500/30 bg-indigo-500/15 px-2 py-0.5 text-[10px] font-medium text-indigo-400">
                        <Moon size={10} />
                        Sleeping
                      </span>
                    ) : (
                      <span className={`rounded-full border px-2 py-0.5 text-[10px] font-medium ${stateBadge(c.state)}`}>
                        {c.state}
                      </span>
                    )}
                    {(() => {
                      const badge = securityBadgesMap?.[c.name];
                      if (!badge || badge.critical + badge.high + badge.medium === 0) return null;
                      const total = badge.critical + badge.high;
                      const cls = 'cursor-pointer focus:outline-none';
                      if (total > 0) return (
                        <button title={`${badge.critical} critical, ${badge.high} high CVEs — click to view`}
                          onClick={() => onSecurityClick?.(c.name)}
                          className={`inline-flex items-center gap-0.5 rounded-full border border-red-500/30 bg-red-500/15 px-1.5 py-0.5 text-[10px] font-medium text-red-400 ${cls}`}>
                          <ShieldAlert size={9} />
                          {total}
                        </button>
                      );
                      return (
                        <button title={`${badge.medium} medium CVEs — click to view`}
