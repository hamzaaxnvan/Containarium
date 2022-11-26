'use client';

import { Connection, formatBytes, formatDuration, getProtocolLabel, getStateLabel } from '@/src/types/traffic';

interface ConnectionsTableProps {
  connections: Connection[];
  isLoading?: boolean;
}

function protocolBadge(protocol: string) {
  switch (protocol) {
    case 'TCP': return 'border-blue-500/30 bg-blue-500/10 text-[var(--c-blue)]';
    case 'UDP': return 'border-violet-500/30 bg-violet-500/10 text-[var(--c-violet)]';
    default:    return 'border-zinc-500/30 bg-zinc-500/10 text-zinc-400';
  }
}

function stateBadge(state: string) {
  switch (state) {
    case 'ESTABLISHED': return 'border-emerald-500/30 bg-emerald-500/10 text-[var(--c-emerald)]';
    case 'SYN_SENT': case 'SYN_RECV': case 'NEW': return 'border-amber-500/30 bg-amber-500/10 text-[var(--c-amber)]';
    case 'TIME_WAIT': case 'CLOSE_WAIT': case 'FIN_WAIT': case 'CLOSED': return 'border-red-500/30 bg-red-500/10 text-[var(--c-red)]';
    default: return 'border-zinc-500/30 bg-zinc-500/10 text-zinc-400';
  }
}

function duration(firstSeen: string, lastSeen: string) {
  return Math.floor((new Date(lastSeen).getTime() - new Date(firstSeen).getTime()) / 1000);
}

