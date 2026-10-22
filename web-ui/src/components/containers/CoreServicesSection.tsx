'use client';

import { useState } from 'react';
import { ChevronDown } from 'lucide-react';
import { CoreService } from '@/src/lib/api/client';

const ROLE_NAMES: Record<string, string> = {
  'core-postgres': 'PostgreSQL',
  'core-caddy': 'Caddy',
  'core-victoriametrics': 'VictoriaMetrics',
  'core-security': 'ClamAV',
};

function stateBadge(state: string) {
  if (state === 'Running') return 'bg-emerald-500/15 text-[var(--c-emerald)] border-emerald-500/30';
  if (state === 'Stopped' || state === 'Error') return 'bg-red-500/15 text-[var(--c-red)] border-red-500/30';
  return 'bg-zinc-500/15 text-zinc-400 border-zinc-500/30';
}

export default function CoreServicesSection({ services }: { services: CoreService[] }) {
  const [open, setOpen] = useState(false);
  if (services.length === 0) return null;

  return (
    <div className="mb-4 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] overflow-hidden">
      <button
        onClick={() => setOpen(o => !o)}
        className="flex w-full items-center gap-2 px-4 py-3 text-left text-sm font-semibold text-[var(--text)] hover:bg-[var(--surface-2)] transition-colors"
      >
        <span>Core Infrastructure</span>
        <span className="rounded-full border border-[var(--border)] bg-[var(--surface-2)] px-2 py-0.5 text-[10px] font-medium text-[var(--text-secondary)]">
          {services.length} services
        </span>
        <ChevronDown size={14} className={`ml-auto text-[var(--text-muted)] transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>
      {open && (
        <div className="overflow-x-auto border-t border-[var(--border-subtle)]">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-[var(--border-subtle)] bg-[var(--surface-2)]">
                {['Service', 'Status', 'IP Address'].map(h => (
                  <th key={h} className="px-4 py-2 text-left font-medium text-[var(--text-secondary)]">{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {services.map(svc => (
                <tr key={svc.name} className="border-b border-[var(--border-subtle)] last:border-0">
                  <td className="px-4 py-2">
                    <p className="font-medium text-[var(--text)]">{ROLE_NAMES[svc.role] || svc.role}</p>
                    <p className="text-[var(--text-muted)]">{svc.name}</p>
                  </td>
                  <td className="px-4 py-2">
                    <span className={`rounded-full border px-2 py-0.5 text-[10px] font-medium ${stateBadge(svc.state)}`}>
                      {svc.state}
                    </span>
                  </td>
                  <td className="px-4 py-2 font-mono text-[var(--text-secondary)]">
                    {svc.ipAddress || <span className="text-[var(--text-muted)]">—</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
