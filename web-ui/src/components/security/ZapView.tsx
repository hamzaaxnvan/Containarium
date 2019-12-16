'use client';

import { useState, useEffect, useCallback } from 'react';
import { Play, RefreshCw, Download, ChevronDown, ChevronUp, EyeOff, Loader2 } from 'lucide-react';
import { Server } from '@/src/types/server';
import { ZapAlert, ZapAlertSummary, ZapScanRun, ZapConfig } from '@/src/types/security';
import { getClient } from '@/src/lib/api/client';
import { Modal, ModalBtn, Textarea, FormField } from '@/src/components/ui/Modal';

interface ZapViewProps { server: Server; }

function formatDate(iso: string): string {
  if (!iso) return '-';
  try { return new Date(iso).toLocaleString(); } catch { return iso; }
}

function RiskBadge({ risk }: { risk: string }) {
  const cls: Record<string, string> = {
    high:          'border-red-500/30 bg-red-500/10 text-[var(--c-red)]',
    medium:        'border-amber-500/30 bg-amber-500/10 text-[var(--c-amber)]',
    low:           'border-blue-500/30 bg-blue-500/10 text-[var(--c-blue)]',
    informational: 'border-[var(--border-subtle)] bg-[var(--surface-2)] text-[var(--text-muted)]',
  };
  return <span className={`rounded-full border px-2 py-0.5 text-[10px] font-medium ${cls[risk] || cls.informational} ${risk === 'high' ? 'font-bold' : ''}`}>{risk}</span>;
}

function StatusBadge({ status }: { status: string }) {
  switch (status) {
    case 'open': return <span className="rounded-full border border-red-500/30 bg-red-500/10 px-2 py-0.5 text-[10px] text-[var(--c-red)]">Open</span>;
    case 'resolved': return <span className="rounded-full border border-emerald-500/30 bg-emerald-500/10 px-2 py-0.5 text-[10px] text-[var(--c-emerald)]">Resolved</span>;
    case 'suppressed': return <span className="rounded-full border border-[var(--border-subtle)] bg-[var(--surface-2)] px-2 py-0.5 text-[10px] text-[var(--text-muted)]">Suppressed</span>;
    default: return <span className="rounded-full border border-[var(--border-subtle)] px-2 py-0.5 text-[10px] text-[var(--text-muted)]">{status}</span>;
  }
}

function SummaryCard({ title, value, color }: { title: string; value: number; color: string }) {
  return (
    <div className="flex min-w-[100px] flex-col items-center rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] p-3">
      <span className={`text-2xl font-bold ${color}`}>{value}</span>
      <span className="text-[10px] text-[var(--text-muted)]">{title}</span>
    </div>
  );
}

function AlertRow({ alert, onSuppress }: { alert: ZapAlert; onSuppress: (id: number) => void }) {
  const [expanded, setExpanded] = useState(false);
  const TD = 'px-3 py-2 text-xs';

  return (
    <>
      <tr onClick={() => setExpanded(!expanded)} className="cursor-pointer border-b border-[var(--border-subtle)] transition-colors hover:bg-[var(--surface)] last:border-0">
        <td className={TD}>
          <div className="flex items-center gap-1">
            {expanded ? <ChevronUp size={10} /> : <ChevronDown size={10} />}
            <RiskBadge risk={alert.risk} />
          </div>
        </td>
        <td className={TD + ' font-medium text-[var(--text)]'}>{alert.alertName}</td>
        <td className={TD}><span className="block max-w-[280px] truncate font-mono text-[10px] text-[var(--text-secondary)]" title={alert.url}>{alert.url}</span></td>
        <td className={TD}><span className="rounded-full border border-[var(--border-subtle)] bg-[var(--surface-2)] px-2 py-0.5 text-[10px] text-[var(--text-muted)]">{alert.confidence}</span></td>
        <td className={TD}><StatusBadge status={alert.status} /></td>
        <td className={TD + ' whitespace-nowrap text-[var(--text-muted)]'}>{formatDate(alert.lastSeenAt)}</td>
        <td className="px-3 py-2 text-right">
          {alert.status === 'open' && (
            <button title="Suppress alert" onClick={e => { e.stopPropagation(); onSuppress(alert.id); }}
              className="rounded p-1 text-[var(--text-muted)] transition-colors hover:bg-[var(--surface-2)] hover:text-[var(--text)]"><EyeOff size={12} /></button>
          )}
        </td>
      </tr>
      {expanded && (
        <tr className="border-b border-[var(--border-subtle)]">
          <td colSpan={7} className="bg-[var(--surface-2)] px-8 py-3">
            <div className="flex flex-col gap-2 text-xs">
              {alert.description && <div><p className="mb-0.5 text-[10px] text-[var(--text-muted)]">Description</p><p className="text-[var(--text)]">{alert.description}</p></div>}
              {alert.evidence && (
                <div>
                  <p className="mb-0.5 text-[10px] text-[var(--text-muted)]">Evidence</p>
                  <pre className="overflow-x-auto rounded border border-[var(--border-subtle)] bg-[var(--surface)] p-2 font-mono text-[10px] text-[var(--text)] whitespace-pre-wrap">{alert.evidence}</pre>
                </div>
              )}
              {alert.solution && <div><p className="mb-0.5 text-[10px] text-[var(--text-muted)]">Solution</p><p className="text-[var(--text)]">{alert.solution}</p></div>}
              {alert.cweIds && <div><p className="mb-0.5 text-[10px] text-[var(--text-muted)]">CWE IDs</p><code className="font-mono text-[var(--text-secondary)]">{alert.cweIds}</code></div>}
              {alert.references && <div><p className="mb-0.5 text-[10px] text-[var(--text-muted)]">References</p><p className="text-[10px] text-[var(--text-secondary)] whitespace-pre-wrap">{alert.references}</p></div>}
              <div className="flex flex-wrap gap-4 text-[10px] text-[var(--text-muted)]">
                <span>Method: {alert.method || 'GET'}</span>
                <span>Plugin: {alert.pluginId}</span>
                <span>First seen: {formatDate(alert.firstSeenAt)}</span>
                <span>Last seen: {formatDate(alert.lastSeenAt)}</span>
                {alert.resolvedAt && <span>Resolved: {formatDate(alert.resolvedAt)}</span>}
              </div>
            </div>
          </td>
        </tr>
      )}
    </>
  );
}

export default function ZapView({ server }: ZapViewProps) {
  const [summary, setSummary] = useState<ZapAlertSummary | null>(null);
  const [alerts, setAlerts] = useState<ZapAlert[]>([]);
  const [totalCount, setTotalCount] = useState(0);
  const [scanRuns, setScanRuns] = useState<ZapScanRun[]>([]);
