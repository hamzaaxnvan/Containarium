'use client';

import { X, Pencil } from 'lucide-react';
import { ServerWithStatus } from '@/src/types/server';

interface ServerTabsProps {
  servers: ServerWithStatus[];
  activeServerId: string | null;
  onServerChange: (serverId: string) => void;
  onRemoveServer: (serverId: string) => void;
  onEditServer: (serverId: string) => void;
}

function statusDot(status: ServerWithStatus['status']) {
  switch (status) {
    case 'connected':  return 'bg-emerald-500';
    case 'error':      return 'bg-red-500';
    case 'connecting': return 'bg-amber-400 animate-pulse';
    default:           return 'bg-zinc-500';
  }
}

export default function ServerTabs({ servers, activeServerId, onServerChange, onRemoveServer, onEditServer }: ServerTabsProps) {
  if (servers.length === 0) return null;

  return (
    <div className="flex items-end gap-0.5 overflow-x-auto border-b border-[var(--border-subtle)] bg-[var(--surface)] px-2 shrink-0">
      {servers.map((server) => {
        const active = server.id === activeServerId;
        return (
          <button
            key={server.id}
            onClick={() => onServerChange(server.id)}
            className={[
              'group relative flex items-center gap-1.5 rounded-t-md px-3 py-2 text-xs font-medium transition-colors',
              active
                ? 'bg-[var(--background)] text-[var(--text)] after:absolute after:bottom-0 after:left-0 after:right-0 after:h-px after:bg-[var(--background)]'
                : 'text-[var(--text-secondary)] hover:bg-[var(--surface-2)] hover:text-[var(--text)]',
            ].join(' ')}
          >
            <span className={`size-1.5 rounded-full shrink-0 ${statusDot(server.status)}`} />
            <span className="max-w-[140px] truncate">{server.name}</span>
            <span
              role="button"
              tabIndex={0}
              title="Edit server"
              onClick={(e) => { e.stopPropagation(); onEditServer(server.id); }}
              onKeyDown={(e) => e.key === 'Enter' && (e.stopPropagation(), onEditServer(server.id))}
              className="ml-0.5 rounded p-0.5 opacity-0 transition-opacity hover:bg-[var(--surface-2)] group-hover:opacity-60 hover:!opacity-100"
            >
              <Pencil size={10} />
            </span>
            <span
              role="button"
              tabIndex={0}
              title="Remove server"
              onClick={(e) => { e.stopPropagation(); onRemoveServer(server.id); }}
              onKeyDown={(e) => e.key === 'Enter' && (e.stopPropagation(), onRemoveServer(server.id))}
              className="rounded p-0.5 opacity-0 transition-opacity hover:bg-red-500/20 hover:text-[var(--c-red)] group-hover:opacity-60 hover:!opacity-100"
            >
              <X size={10} />
            </span>
          </button>
        );
      })}
    </div>
  );
}
