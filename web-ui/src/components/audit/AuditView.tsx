'use client';

import { useState, useCallback } from 'react';
import { RefreshCw, Loader2 } from 'lucide-react';
import { Server } from '@/src/types/server';
import { AuditLogsParams } from '@/src/types/audit';
import { useAudit } from '@/src/lib/hooks/useAudit';

interface AuditViewProps { server: Server; }

function formatDate(iso: string): string {
  if (!iso) return '—';
  try { return new Date(iso).toLocaleString(); } catch { return iso; }
}

const ACTION_OPTIONS = [
  { value: '', label: 'All' },
  { value: 'ssh_login', label: 'SSH Login' },
  { value: 'terminal_access', label: 'Terminal Access' },
  { value: 'api_post', label: 'API POST' },
  { value: 'api_put', label: 'API PUT' },
  { value: 'api_delete', label: 'API DELETE' },
  { value: 'api_get', label: 'API GET' },
  { value: 'EVENT_TYPE_CONTAINER_CREATED', label: 'Container Created' },
  { value: 'EVENT_TYPE_CONTAINER_STARTED', label: 'Container Started' },
  { value: 'EVENT_TYPE_CONTAINER_STOPPED', label: 'Container Stopped' },
  { value: 'EVENT_TYPE_CONTAINER_DELETED', label: 'Container Deleted' },
  { value: 'EVENT_TYPE_APP_DEPLOYED', label: 'App Deployed' },
  { value: 'EVENT_TYPE_APP_STOPPED', label: 'App Stopped' },
  { value: 'EVENT_TYPE_ROUTE_ADDED', label: 'Route Added' },
  { value: 'EVENT_TYPE_ROUTE_REMOVED', label: 'Route Removed' },
];

const RESOURCE_TYPE_OPTIONS = [
  { value: '', label: 'All' },
  { value: 'container', label: 'Container' },
  { value: 'app', label: 'App' },
  { value: 'route', label: 'Route' },
  { value: 'api', label: 'API' },
];

const METHOD_BADGE: Record<string, string> = {
  api_get:    'border-emerald-500/30 bg-emerald-500/10 text-[var(--c-emerald)]',
  api_post:   'border-blue-500/30 bg-blue-500/10 text-[var(--c-blue)]',
  api_put:    'border-amber-500/30 bg-amber-500/10 text-[var(--c-amber)]',
  api_delete: 'border-red-500/30 bg-red-500/10 text-[var(--c-red)]',
  api_patch:  'border-violet-500/30 bg-violet-500/10 text-[var(--c-violet)]',
};
const METHOD_LABEL: Record<string, string> = { api_get: 'GET', api_post: 'POST', api_put: 'PUT', api_delete: 'DELETE', api_patch: 'PATCH' };

function ActionBadge({ action }: { action: string }) {
  if (action === 'ssh_login') return <span className="rounded-full border border-blue-500/30 bg-blue-500/10 px-2 py-0.5 text-[10px] text-[var(--c-blue)]">SSH Login</span>;
  if (action === 'terminal_access') return <span className="rounded-full border border-violet-500/30 bg-violet-500/10 px-2 py-0.5 text-[10px] text-[var(--c-violet)]">Terminal</span>;
  if (METHOD_BADGE[action]) return <span className={`rounded-full border px-2 py-0.5 text-[10px] font-bold ${METHOD_BADGE[action]}`}>{METHOD_LABEL[action]}</span>;
  if (action.startsWith('api_')) return <span className="rounded-full border border-zinc-600 bg-zinc-600/10 px-2 py-0.5 text-[10px] font-bold text-zinc-400">{action.replace('api_', '').toUpperCase()}</span>;
  if (action.startsWith('EVENT_TYPE_')) {
    const label = action.replace('EVENT_TYPE_', '').split('_').map(w => w.charAt(0) + w.slice(1).toLowerCase()).join(' ');
    return <span className="rounded-full border border-emerald-500/30 bg-emerald-500/10 px-2 py-0.5 text-[10px] text-[var(--c-emerald)]">{label}</span>;
  }
  return <span className="rounded-full border border-[var(--border)] bg-[var(--surface-2)] px-2 py-0.5 text-[10px] text-[var(--text-secondary)]">{action}</span>;
}

export default function AuditView({ server }: AuditViewProps) {
  const [username, setUsername] = useState('');
  const [action, setAction] = useState('');
  const [resourceType, setResourceType] = useState('');
  const [fromDate, setFromDate] = useState('');
  const [toDate, setToDate] = useState('');
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(25);

  const params: AuditLogsParams = {
    ...(username && { username }),
    ...(action && { action }),
    ...(resourceType && { resource_type: resourceType }),
    ...(fromDate && { from: new Date(fromDate).toISOString() }),
    ...(toDate && { to: new Date(toDate).toISOString() }),
    limit: rowsPerPage,
    offset: page * rowsPerPage,
  };

  const { logs, totalCount, isLoading, error, refresh } = useAudit(server, params);

  const totalPages = Math.ceil(totalCount / rowsPerPage);

  const handlePageChange = useCallback((newPage: number) => { setPage(newPage); }, []);

  const filterInput = 'w-full rounded-md border border-[var(--border-subtle)] bg-[var(--surface-2)] px-3 py-1.5 text-xs text-[var(--text)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent)] focus:outline-none';
  const filterSelect = 'w-full appearance-none rounded-md border border-[var(--border-subtle)] bg-[var(--surface-2)] px-3 py-1.5 text-xs text-[var(--text)] focus:border-[var(--accent)] focus:outline-none';

  return (
    <div className="p-6">
      {/* Header */}
      <div className="mb-6 flex items-center gap-3">
        <h1 className="mr-auto text-base font-semibold text-[var(--text)]">Audit Logs</h1>
        <button onClick={refresh} disabled={isLoading} className="flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--surface)] px-3 py-1.5 text-xs text-[var(--text-secondary)] transition-colors hover:bg-[var(--surface-2)] disabled:opacity-50">
          <RefreshCw size={12} className={isLoading ? 'animate-spin' : ''} />
          Refresh
        </button>
      </div>

      {error && (
        <div className="mb-4 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-xs text-[var(--c-red)]">
          Failed to load audit logs: {(error as Error).message}
        </div>
      )}

      {/* Filters */}
      <div className="mb-4 flex flex-wrap gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] p-4">
        <div className="flex flex-col gap-1">
          <label className="text-[10px] font-medium text-[var(--text-muted)]">Username</label>
          <input type="text" value={username} onChange={e => { setUsername(e.target.value); setPage(0); }} placeholder="Filter by username" className={filterInput} style={{ width: 140 }} />
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-[10px] font-medium text-[var(--text-muted)]">Action</label>
          <select value={action} onChange={e => { setAction(e.target.value); setPage(0); }} className={filterSelect} style={{ width: 180 }}>
            {ACTION_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-[10px] font-medium text-[var(--text-muted)]">Resource Type</label>
          <select value={resourceType} onChange={e => { setResourceType(e.target.value); setPage(0); }} className={filterSelect} style={{ width: 150 }}>
            {RESOURCE_TYPE_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-[10px] font-medium text-[var(--text-muted)]">From</label>
          <input type="datetime-local" value={fromDate} onChange={e => { setFromDate(e.target.value); setPage(0); }} className={filterInput} style={{ width: 200 }} />
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-[10px] font-medium text-[var(--text-muted)]">To</label>
          <input type="datetime-local" value={toDate} onChange={e => { setToDate(e.target.value); setPage(0); }} className={filterInput} style={{ width: 200 }} />
        </div>
      </div>

      {/* Table */}
      <div className="overflow-x-auto rounded-xl border border-[var(--border-subtle)]">
        {isLoading && (
          <div className="flex justify-center border-b border-[var(--border-subtle)] p-3">
            <Loader2 size={16} className="animate-spin text-[var(--text-secondary)]" />
          </div>
        )}
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-[var(--border-subtle)] bg-[var(--surface)]">
              {['Timestamp', 'Username', 'Action', 'Resource', 'Detail', 'Source IP', 'Status'].map(h => (
                <th key={h} className="px-3 py-2.5 text-left font-medium text-[var(--text-secondary)] whitespace-nowrap">{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {logs.length === 0 && !isLoading ? (
              <tr>
                <td colSpan={7} className="py-10 text-center text-[var(--text-muted)]">No audit log entries found</td>
              </tr>
            ) : (
              logs.map(entry => (
                <tr key={entry.id} className="border-b border-[var(--border-subtle)] transition-colors hover:bg-[var(--surface)] last:border-0">
                  <td className="px-3 py-2 whitespace-nowrap text-[var(--text-muted)]">{formatDate(entry.timestamp)}</td>
                  <td className="px-3 py-2 text-[var(--text-secondary)]">{entry.username || '—'}</td>
                  <td className="px-3 py-2"><ActionBadge action={entry.action} /></td>
                  <td className="px-3 py-2">
                    {entry.resourceType === 'api' ? (
                      <span className="font-mono text-[var(--text-secondary)]">{entry.resourceId.replace(/^(GET|POST|PUT|DELETE|PATCH)\s+/, '') || '—'}</span>
                    ) : (
                      <span className="text-[var(--text-secondary)]">
                        {entry.resourceType && <span className="text-[var(--text-muted)]">{entry.resourceType}/</span>}
                        {entry.resourceId || '—'}
                      </span>
                    )}
                  </td>
                  <td className="px-3 py-2 max-w-[250px] truncate text-[var(--text-muted)]" title={entry.detail}>{entry.detail || '—'}</td>
                  <td className="px-3 py-2 font-mono text-[var(--text-muted)]">{entry.sourceIp || '—'}</td>
                  <td className="px-3 py-2 text-[var(--text-muted)]">{entry.statusCode > 0 ? entry.statusCode : '—'}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>

        {/* Pagination */}
        <div className="flex items-center justify-between border-t border-[var(--border-subtle)] px-4 py-2">
          <div className="flex items-center gap-2 text-xs text-[var(--text-muted)]">
            <span>Rows per page:</span>
            <select value={rowsPerPage} onChange={e => { setRowsPerPage(parseInt(e.target.value)); setPage(0); }} className="rounded border border-[var(--border-subtle)] bg-[var(--surface-2)] px-2 py-0.5 text-xs text-[var(--text)] focus:outline-none">
              {[25, 50, 100].map(n => <option key={n} value={n}>{n}</option>)}
            </select>
          </div>
          <div className="flex items-center gap-2 text-xs text-[var(--text-muted)]">
            <span>{page * rowsPerPage + 1}–{Math.min((page + 1) * rowsPerPage, totalCount)} of {totalCount}</span>
            <button onClick={() => handlePageChange(page - 1)} disabled={page === 0} className="rounded px-2 py-1 hover:bg-[var(--surface-2)] disabled:opacity-40 transition-colors">‹</button>
            <button onClick={() => handlePageChange(page + 1)} disabled={page >= totalPages - 1} className="rounded px-2 py-1 hover:bg-[var(--surface-2)] disabled:opacity-40 transition-colors">›</button>
          </div>
        </div>
      </div>
    </div>
  );
}
