'use client';

import { Trash2, ExternalLink, GitBranch } from 'lucide-react';
import { ProxyRoute } from '@/src/types/app';

interface RouteCardProps {
  route: ProxyRoute;
  onDelete?: (domain: string) => void;
}

export default function RouteCard({ route, onDelete }: RouteCardProps) {
  return (
    <div className="flex flex-col rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] p-4">
      <div className="mb-3 flex items-start gap-2">
        <GitBranch size={15} className="mt-0.5 shrink-0 text-[var(--accent)]" />
        <p className="flex-1 truncate text-sm font-medium text-[var(--text)]">{route.fullDomain}</p>
        <span className={`shrink-0 rounded-full border px-2 py-0.5 text-[10px] font-medium ${route.active ? 'border-emerald-500/30 bg-emerald-500/10 text-[var(--c-emerald)]' : 'border-zinc-600/30 bg-zinc-600/10 text-zinc-500'}`}>
          {route.active ? 'Active' : 'Inactive'}
        </span>
      </div>

      <div className="flex flex-col gap-1.5 text-xs">
        <div className="flex justify-between">
          <span className="text-[var(--text-muted)]">Target</span>
          <span className="font-mono text-[var(--text-secondary)]">{route.containerIp || 'N/A'}:{route.port || 'N/A'}</span>
        </div>
        {route.appName && (
          <div className="flex justify-between">
            <span className="text-[var(--text-muted)]">App</span>
            <span className="text-[var(--text-secondary)]">{route.appName}</span>
          </div>
        )}
        {route.username && (
          <div className="flex justify-between">
            <span className="text-[var(--text-muted)]">Owner</span>
            <span className="text-[var(--text-secondary)]">{route.username}</span>
          </div>
        )}
      </div>

      <div className="mt-3 flex items-center justify-end gap-1 border-t border-[var(--border-subtle)] pt-2">
        <a href={`https://${route.fullDomain}`} target="_blank" rel="noopener noreferrer" title="Open in new tab"
          className="rounded p-1 text-[var(--text-muted)] transition-colors hover:bg-[var(--surface-2)] hover:text-[var(--text)]">
          <ExternalLink size={13} />
        </a>
        {onDelete && (
          <button title="Delete route" onClick={() => onDelete(route.fullDomain)}
            className="rounded p-1 text-[var(--text-muted)] transition-colors hover:bg-red-500/10 hover:text-[var(--c-red)]">
            <Trash2 size={13} />
          </button>
        )}
      </div>
    </div>
  );
}
