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

export default function ConnectionsTable({ connections, isLoading }: ConnectionsTableProps) {
  if (isLoading) {
    return <p className="py-6 text-center text-xs text-[var(--text-muted)]">Loading connections…</p>;
  }
  if (connections.length === 0) {
    return <p className="py-6 text-center text-xs text-[var(--text-muted)]">No active connections</p>;
  }

  return (
    <div className="overflow-x-auto rounded-xl border border-[var(--border-subtle)]">
      <table className="w-full text-xs">
        <thead>
          <tr className="border-b border-[var(--border-subtle)] bg-[var(--surface)]">
            {['Protocol', 'Destination', 'Port', 'State', 'Sent', 'Received', 'Duration'].map((h, i) => (
              <th key={h} className={`px-3 py-2.5 font-medium text-[var(--text-secondary)] ${i >= 4 ? 'text-right' : 'text-left'}`}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {connections.map(conn => (
            <tr key={conn.id} className="border-b border-[var(--border-subtle)] transition-colors hover:bg-[var(--surface)] last:border-0">
              <td className="px-3 py-2.5">
                <span className={`rounded-full border px-2 py-0.5 text-[10px] font-medium ${protocolBadge(conn.protocol)}`}>
                  {getProtocolLabel(conn.protocol)}
                </span>
              </td>
              <td className="px-3 py-2.5 font-mono text-[var(--text-secondary)]" title={`Source: ${conn.sourceIp}:${conn.sourcePort}`}>
                {conn.destIp}
              </td>
              <td className="px-3 py-2.5 font-mono text-[var(--text-secondary)]">{conn.destPort}</td>
              <td className="px-3 py-2.5">
                <span className={`rounded-full border px-2 py-0.5 text-[10px] font-medium ${stateBadge(conn.state)}`}>
                  {getStateLabel(conn.state)}
                </span>
              </td>
              <td className="px-3 py-2.5 text-right font-mono text-[var(--text-secondary)]">{formatBytes(conn.bytesSent)}</td>
              <td className="px-3 py-2.5 text-right font-mono text-[var(--text-secondary)]">{formatBytes(conn.bytesReceived)}</td>
              <td className="px-3 py-2.5 text-right text-[var(--text-muted)]">{formatDuration(duration(conn.firstSeen, conn.lastSeen))}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
