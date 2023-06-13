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
  const [config, setConfig] = useState<ZapConfig | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [scanning, setScanning] = useState(false);
  const [snackMessage, setSnackMessage] = useState<string | null>(null);
  const [riskFilter, setRiskFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState('open');
  const [domainInput, setDomainInput] = useState('');
  const [domainFilter, setDomainFilter] = useState('');
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(25);
  const [suppressId, setSuppressId] = useState<number | null>(null);
  const [suppressReason, setSuppressReason] = useState('');
  const [showScanHistory, setShowScanHistory] = useState(false);
  const [downloadFormat, setDownloadFormat] = useState<'csv' | 'json'>('csv');
  const [downloading, setDownloading] = useState(false);
  const [installing, setInstalling] = useState(false);

  useEffect(() => {
    if (snackMessage) { const t = setTimeout(() => setSnackMessage(null), 5000); return () => clearTimeout(t); }
  }, [snackMessage]);

  // Debounce the free-text domain filter so it doesn't re-query on every keystroke.
  useEffect(() => {
    const t = setTimeout(() => { setDomainFilter(domainInput.trim()); setPage(0); }, 400);
    return () => clearTimeout(t);
  }, [domainInput]);

  const loadData = useCallback(async () => {
    try {
      const client = getClient(server);
      const limit = rowsPerPage === -1 ? 1000 : rowsPerPage;
      const offset = rowsPerPage === -1 ? 0 : page * rowsPerPage;
      const [summaryResp, alertsResp, runsResp, configResp] = await Promise.all([
        client.getZapAlertSummary(),
        client.listZapAlerts({ risk: riskFilter || undefined, status: statusFilter || undefined, domain: domainFilter || undefined, limit, offset }),
        client.listZapScanRuns(10),
        client.getZapConfig(),
      ]);
      setSummary(summaryResp.summary); setAlerts(alertsResp.alerts); setTotalCount(alertsResp.totalCount);
      setScanRuns(runsResp.scanRuns); setConfig(configResp.config); setError(null);
    } catch (err) { setError(err instanceof Error ? err.message : 'Failed to load ZAP data'); }
    finally { setIsLoading(false); }
  }, [server, riskFilter, statusFilter, domainFilter, page, rowsPerPage]);

  useEffect(() => { loadData(); const interval = setInterval(loadData, 60000); return () => clearInterval(interval); }, [loadData]);

  const handleTriggerScan = async () => {
    setScanning(true);
    try {
      const client = getClient(server);
      const result = await client.triggerZapScan();
      setSnackMessage(result.message || `Scan started: ${result.scanRunId}`);
      const pollInterval = setInterval(async () => {
        try {
          const runsResp = await client.listZapScanRuns(5);
          setScanRuns(runsResp.scanRuns);
          const latest = runsResp.scanRuns[0];
          if (latest && latest.id === result.scanRunId && latest.status !== 'running') { clearInterval(pollInterval); setScanning(false); loadData(); }
        } catch { /* ignore */ }
      }, 10000);
      setTimeout(() => { clearInterval(pollInterval); setScanning(false); }, 1800000);
    } catch (err) { setSnackMessage(err instanceof Error ? err.message : 'Scan trigger failed'); setScanning(false); }
  };

  const handleSuppress = async () => {
    if (suppressId === null) return;
    try {
      const client = getClient(server);
      await client.suppressZapAlert(suppressId, suppressReason);
      setSnackMessage('Alert suppressed'); setSuppressId(null); setSuppressReason(''); loadData();
    } catch (err) { setSnackMessage(err instanceof Error ? err.message : 'Failed to suppress alert'); }
  };

  const handleDownload = async () => {
    setDownloading(true);
    try {
      const client = getClient(server);
      const resp = await client.listZapAlerts({ risk: riskFilter || undefined, status: statusFilter || undefined, domain: domainFilter || undefined, limit: 1000, offset: 0 });
      const all = resp.alerts;
      const dateStr = new Date().toISOString().slice(0, 10);
      let blob: Blob; let filename: string;
      if (downloadFormat === 'json') {
        blob = new Blob([JSON.stringify(all, null, 2)], { type: 'application/json' }); filename = `zap-alerts-${dateStr}.json`;
      } else {
        const cols = ['id', 'risk', 'confidence', 'alertName', 'description', 'url', 'method', 'evidence', 'solution', 'cweIds', 'status', 'firstSeenAt', 'lastSeenAt', 'resolvedAt', 'suppressed', 'suppressedReason'] as const;
        const esc = (v: unknown) => { const s = String(v ?? ''); return s.includes(',') || s.includes('"') || s.includes('\n') ? `"${s.replace(/"/g, '""')}"` : s; };
        blob = new Blob([[cols.join(','), ...all.map(a => cols.map(c => esc(a[c])).join(','))].join('\n')], { type: 'text/csv' });
        filename = `zap-alerts-${dateStr}.csv`;
      }
      const url = URL.createObjectURL(blob); const a = document.createElement('a');
      a.href = url; a.download = filename; document.body.appendChild(a); a.click(); document.body.removeChild(a); URL.revokeObjectURL(url);
    } catch (err) { setSnackMessage(err instanceof Error ? err.message : 'Download failed'); }
    finally { setDownloading(false); }
  };

  const handleDownloadReport = async (scanRunId: string) => {
    try {
      const client = getClient(server);
      const report = await client.getZapReport(scanRunId, 'html');
      const blob = new Blob([report.content], { type: report.contentType || 'text/html' });
      const url = URL.createObjectURL(blob); const a = document.createElement('a');
      a.href = url; a.download = report.filename || `zap-report-${scanRunId}.html`;
      document.body.appendChild(a); a.click(); document.body.removeChild(a); URL.revokeObjectURL(url);
