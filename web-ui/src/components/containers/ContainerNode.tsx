'use client';

import {
  Trash2, Play, Square, Terminal, Monitor,
  Shield, Tag, SlidersHorizontal, Network, Moon, ShieldAlert,
} from 'lucide-react';
import { Container, ContainerState, ContainerMetricsWithRate } from '@/src/types/container';
import { SecurityBadge } from '@/src/lib/hooks/useSecurity';

interface ContainerNodeProps {
  container: Container;
  metrics?: ContainerMetricsWithRate;
  securityBadge?: SecurityBadge | null;
  onDelete: (username: string) => void;
  onStart?: (username: string) => void;
  onStop?: (username: string) => void;
  onTerminal?: (username: string) => void;
  onEditFirewall?: (username: string) => void;
  onEditLabels?: (username: string) => void;
  onResize?: (username: string) => void;
  onToggleAutoSleep?: (username: string, current: { enabled: boolean; threshold: number }) => void;
  onSecurityClick?: () => void;
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

function UsageBar({ used, total, color }: { used: number; total: number; color: string }) {
  const pct = total > 0 ? Math.min((used / total) * 100, 100) : 0;
  return (
    <div className="h-1 w-full rounded-full bg-zinc-800 overflow-hidden">
      <div className={`h-full rounded-full transition-all ${color}`} style={{ width: `${pct}%` }} />
    </div>
  );
}

function barColor(pct: number) {
  if (pct > 80) return 'bg-red-500';
  if (pct > 60) return 'bg-amber-500';
  return 'bg-emerald-500';
}

function SecurityBadgePill({ badge }: { badge: SecurityBadge }) {
  const total = badge.critical + badge.high;
  if (total > 0) return (
    <span title={`${badge.critical} critical, ${badge.high} high CVEs`}
      className="inline-flex items-center gap-0.5 rounded-full border border-red-500/30 bg-red-500/15 px-1.5 py-0.5 text-[10px] font-medium text-red-400">
      <ShieldAlert size={9} />
      {total}
    </span>
  );
  if (badge.medium > 0) return (
    <span title={`${badge.medium} medium CVEs`}
      className="inline-flex items-center gap-0.5 rounded-full border border-amber-500/30 bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-400">
      <ShieldAlert size={9} />
      {badge.medium}
    </span>
  );
  return null;
}
