'use client';

import { useState, useEffect } from 'react';
import { Plus, RefreshCw, Trash2, Edit2, CheckCircle, XCircle, Lock, Copy, KeyRound, Send, Info, Loader2 } from 'lucide-react';
import { Server } from '@/src/types/server';
import { AlertRule, CreateAlertRuleRequest } from '@/src/types/alerts';
import { useAlerts, useAlertingInfo, useDefaultAlertRules, useWebhookDeliveries } from '@/src/lib/hooks/useAlerts';
import { getClient } from '@/src/lib/api/client';
import { Modal, ModalBtn, FormField, Input, Textarea } from '@/src/components/ui/Modal';

interface AlertsViewProps { server: Server; }

function SeverityBadge({ severity }: { severity: string }) {
  const cls: Record<string, string> = {
    critical: 'border-red-500/30 bg-red-500/10 text-[var(--c-red)]',
    warning:  'border-amber-500/30 bg-amber-500/10 text-[var(--c-amber)]',
    info:     'border-blue-500/30 bg-blue-500/10 text-[var(--c-blue)]',
  };
  return <span className={`rounded-full border px-2 py-0.5 text-[10px] font-medium ${cls[severity] || 'border-[var(--border-subtle)] bg-[var(--surface-2)] text-[var(--text-muted)]'}`}>{severity}</span>;
}

function StatusDot({ status }: { status: string }) {
  const ok = status === 'healthy';
  return (
    <span className={`flex items-center gap-1 text-xs font-medium ${ok ? 'text-[var(--c-emerald)]' : 'text-[var(--c-red)]'}`}>
      {ok ? <CheckCircle size={13} /> : <XCircle size={13} />}
      {status || 'unknown'}
    </span>
  );
}

function Toggle({ checked, onChange, disabled }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <button role="switch" aria-checked={checked} onClick={() => !disabled && onChange(!checked)} disabled={disabled}
      className={`relative h-4 w-8 shrink-0 rounded-full transition-colors disabled:opacity-40 ${checked ? 'bg-[var(--accent)]' : 'bg-[var(--border)]'}`}>
      <span className={`absolute top-0.5 h-3 w-3 rounded-full bg-white shadow transition-transform ${checked ? 'translate-x-4' : 'translate-x-0.5'}`} />
    </button>
  );
}

function formatTimestamp(unix: string): string {
  if (!unix || unix === '0') return '-';
  try { return new Date(Number(unix) * 1000).toLocaleString(); } catch { return unix; }
}

const EMPTY_RULE: CreateAlertRuleRequest = { name: '', expr: '', duration: '5m', severity: 'warning', description: '', enabled: true };

const TH = 'px-3 py-2.5 text-left text-xs font-medium text-[var(--text-secondary)] whitespace-nowrap';
const TD = 'px-3 py-2 text-xs text-[var(--text-secondary)]';
const inputCls = 'w-full rounded-md border border-[var(--border-subtle)] bg-[var(--surface-2)] px-3 py-1.5 text-xs text-[var(--text)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent)] focus:outline-none';
const selectCls = 'w-full appearance-none rounded-md border border-[var(--border-subtle)] bg-[var(--surface-2)] px-3 py-1.5 text-xs text-[var(--text)] focus:border-[var(--accent)] focus:outline-none';

export default function AlertsView({ server }: AlertsViewProps) {
  const { rules, isLoading, error, refresh } = useAlerts(server);
  const { info, refresh: refreshInfo } = useAlertingInfo(server);
  const { rules: defaultRules } = useDefaultAlertRules(server);
  const { deliveries, refresh: refreshDeliveries } = useWebhookDeliveries(server);

  const [ruleTab, setRuleTab] = useState(0);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingRule, setEditingRule] = useState<AlertRule | null>(null);
  const [formData, setFormData] = useState<CreateAlertRuleRequest>(EMPTY_RULE);
  const [saving, setSaving] = useState(false);
  const [snackMessage, setSnackMessage] = useState<string | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null);
  const [webhookDialogOpen, setWebhookDialogOpen] = useState(false);
  const [webhookUrl, setWebhookUrl] = useState('');
  const [savingWebhook, setSavingWebhook] = useState(false);
  const [testingWebhook, setTestingWebhook] = useState(false);
  const [generatingSecret, setGeneratingSecret] = useState(false);
  const [generatedSecret, setGeneratedSecret] = useState<string | null>(null);
  const [detailRule, setDetailRule] = useState<AlertRule | null>(null);

  useEffect(() => {
    if (snackMessage) {
      const t = setTimeout(() => setSnackMessage(null), 5000);
      return () => clearTimeout(t);
    }
  }, [snackMessage]);

  const handleOpenCreate = () => { setEditingRule(null); setFormData(EMPTY_RULE); setDialogOpen(true); };
  const handleOpenEdit = (rule: AlertRule) => {
    setEditingRule(rule);
    setFormData({ name: rule.name, expr: rule.expr, duration: rule.duration, severity: rule.severity, description: rule.description, enabled: rule.enabled });
    setDialogOpen(true);
  };

  const handleSave = async () => {
    setSaving(true);
    try {
      const client = getClient(server);
      if (editingRule) { await client.updateAlertRule(editingRule.id, formData); setSnackMessage('Alert rule updated'); }
      else { await client.createAlertRule(formData); setSnackMessage('Alert rule created'); }
      setDialogOpen(false); refresh(); refreshInfo();
    } catch (err) { setSnackMessage(`Error: ${err instanceof Error ? err.message : String(err)}`); }
    finally { setSaving(false); }
  };

  const handleDelete = async (id: string) => {
    try {
      const client = getClient(server);
      await client.deleteAlertRule(id);
      setSnackMessage('Alert rule deleted'); setDeleteConfirm(null); refresh(); refreshInfo();
    } catch (err) { setSnackMessage(`Error: ${err instanceof Error ? err.message : String(err)}`); }
  };

  const handleToggleEnabled = async (rule: AlertRule) => {
    try {
      const client = getClient(server);
      await client.updateAlertRule(rule.id, { enabled: !rule.enabled }); refresh();
    } catch (err) { setSnackMessage(`Error: ${err instanceof Error ? err.message : String(err)}`); }
  };

  const handleSaveWebhook = async () => {
    setSavingWebhook(true);
    try {
      const client = getClient(server);
      await client.updateAlertingConfig(webhookUrl);
      setSnackMessage(webhookUrl ? 'Webhook URL updated' : 'Webhook disabled');
      setWebhookDialogOpen(false); refreshInfo();
    } catch (err) { setSnackMessage(`Error: ${err instanceof Error ? err.message : String(err)}`); }
    finally { setSavingWebhook(false); }
  };

  const handleTestWebhook = async () => {
    setTestingWebhook(true);
    try {
      const client = getClient(server);
      const result = await client.testWebhook(); setSnackMessage(result.message);
    } catch (err) { setSnackMessage(`Error: ${err instanceof Error ? err.message : String(err)}`); }
    finally { setTestingWebhook(false); }
  };

  const handleGenerateSecret = async () => {
    setGeneratingSecret(true);
    try {
      const client = getClient(server);
      const result = await client.updateAlertingConfig(webhookUrl || info?.webhookUrl || '', true);
      if (result.webhookSecret) { setGeneratedSecret(result.webhookSecret); setSnackMessage('Webhook secret generated. Copy it now — it will not be shown again.'); }
      else { setSnackMessage('Secret generated but not returned. Check server logs.'); }
      refreshInfo();
    } catch (err) { setSnackMessage(`Error: ${err instanceof Error ? err.message : String(err)}`); }
    finally { setGeneratingSecret(false); }
  };

  if (isLoading) return <div className="flex justify-center p-8"><Loader2 size={20} className="animate-spin text-[var(--text-secondary)]" /></div>;
  if (error) return <div className="p-6"><div className="rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-xs text-[var(--c-red)]">Failed to load alerts: {error.message}</div></div>;

  return (
    <div className="p-6">
      {/* Header */}
      <div className="mb-6 flex items-center gap-3">
        <h1 className="mr-auto text-base font-semibold text-[var(--text)]">Alerts</h1>
        <button onClick={handleOpenCreate} className="flex items-center gap-1.5 rounded-md bg-[var(--accent)] px-3 py-1.5 text-xs text-white transition-colors hover:bg-[var(--accent-hover)]">
          <Plus size={12} /> Create Rule
        </button>
        <button onClick={() => { refresh(); refreshInfo(); refreshDeliveries(); }} className="rounded-md border border-[var(--border-subtle)] bg-[var(--surface)] p-1.5 text-[var(--text-muted)] transition-colors hover:bg-[var(--surface-2)]">
          <RefreshCw size={12} />
        </button>
      </div>

      {/* Status Cards */}
      {info && (
        <div className="mb-6 flex flex-wrap gap-3">
          {[
            { label: 'vmalert', content: <StatusDot status={info.vmalertStatus} /> },
            { label: 'Alertmanager', content: <StatusDot status={info.alertmanagerStatus} /> },
            { label: 'Total Rules', content: <span className="text-lg font-semibold text-[var(--text)]">{info.totalRules}</span> },
            { label: 'Custom Rules', content: <span className="text-lg font-semibold text-[var(--text)]">{info.customRules}</span> },
          ].map(c => (
            <div key={c.label} className="flex min-w-[140px] flex-col gap-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] p-3">
              <span className="text-[10px] text-[var(--text-muted)]">{c.label}</span>
              {c.content}
            </div>
          ))}
          <button
            onClick={() => { setWebhookUrl(''); setGeneratedSecret(null); setWebhookDialogOpen(true); }}
            className="flex min-w-[180px] flex-col gap-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] p-3 text-left transition-colors hover:bg-[var(--surface-2)]"
          >
            <span className="text-[10px] text-[var(--text-muted)]">Webhook Target</span>
            <span className="truncate text-xs text-[var(--text-secondary)]">{info.webhookUrl || 'Not configured (click to set)'}</span>
          </button>
        </div>
      )}

      {/* Rule Tabs */}
      <div className="mb-4 flex border-b border-[var(--border-subtle)]">
        {[`Default Rules (${defaultRules.length})`, `Custom Rules (${rules.length})`, `Delivery History (${deliveries.length})`].map((tab, i) => (
          <button key={tab} onClick={() => setRuleTab(i)}
            className={`-mb-px px-4 py-2 text-xs font-medium transition-colors ${ruleTab === i ? 'border-b-2 border-[var(--accent)] text-[var(--accent)]' : 'text-[var(--text-muted)] hover:text-[var(--text-secondary)]'}`}>
            {tab}
          </button>
        ))}
      </div>

      {/* Default Rules Table */}
      {ruleTab === 0 && (
        <div className="overflow-x-auto rounded-xl border border-[var(--border-subtle)]">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-[var(--border-subtle)] bg-[var(--surface)]">
                {['Name', 'Expression', 'Duration', 'Severity', 'Status'].map(h => <th key={h} className={TH}>{h}</th>)}
              </tr>
            </thead>
            <tbody>
              {defaultRules.length === 0 ? (
                <tr><td colSpan={5} className="py-8 text-center text-[var(--text-muted)]"><Loader2 size={16} className="mx-auto animate-spin" /></td></tr>
              ) : (
                defaultRules.map(rule => (
                  <tr key={rule.id} onClick={() => setDetailRule(rule)} className="cursor-pointer border-b border-[var(--border-subtle)] transition-colors hover:bg-[var(--surface)] last:border-0">
                    <td className={TD}>
                      <div className="flex items-center gap-1.5">
                        <Lock size={11} className="shrink-0 text-[var(--text-muted)]" />
                        <div>
                          <p className="font-medium text-[var(--text)]">{rule.name}</p>
                          {rule.description && <p className="text-[var(--text-muted)] max-w-xs truncate">{rule.description}</p>}
                        </div>
                      </div>
                    </td>
                    <td className={TD}><span className="block max-w-[320px] truncate font-mono" title={rule.expr}>{rule.expr}</span></td>
                    <td className={TD}>{rule.duration}</td>
                    <td className={TD}><SeverityBadge severity={rule.severity} /></td>
                    <td className={TD}><span className="rounded-full border border-emerald-500/30 bg-emerald-500/10 px-2 py-0.5 text-[10px] text-[var(--c-emerald)]">Always Active</span></td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}

      {/* Custom Rules Table */}
      {ruleTab === 1 && (
        <div className="overflow-x-auto rounded-xl border border-[var(--border-subtle)]">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-[var(--border-subtle)] bg-[var(--surface)]">
                {['Name', 'Expression', 'Duration', 'Severity', 'Enabled', 'Created', ''].map(h => <th key={h} className={TH}>{h}</th>)}
              </tr>
            </thead>
            <tbody>
              {rules.length === 0 ? (
                <tr><td colSpan={7} className="py-10 text-center text-[var(--text-muted)]">No custom alert rules. Click "Create Rule" to add one.</td></tr>
              ) : (
                rules.map(rule => (
                  <tr key={rule.id} className="border-b border-[var(--border-subtle)] transition-colors hover:bg-[var(--surface)] last:border-0">
                    <td className="cursor-pointer px-3 py-2" onClick={() => setDetailRule(rule)}>
                      <p className="font-medium text-[var(--text)]">{rule.name}</p>
                      {rule.description && <p className="text-[10px] text-[var(--text-muted)]">{rule.description}</p>}
                    </td>
                    <td className="cursor-pointer px-3 py-2" onClick={() => setDetailRule(rule)}>
                      <span className="block max-w-[280px] truncate font-mono text-[10px] text-[var(--text-secondary)]">{rule.expr}</span>
                    </td>
                    <td className={TD}>{rule.duration}</td>
                    <td className={TD}><SeverityBadge severity={rule.severity} /></td>
                    <td className={TD}><Toggle checked={rule.enabled} onChange={() => handleToggleEnabled(rule)} /></td>
                    <td className={TD + ' whitespace-nowrap text-[var(--text-muted)]'}>{formatTimestamp(rule.createdAt)}</td>
                    <td className="px-3 py-2">
                      <div className="flex items-center justify-end gap-1">
                        <button title="Edit" onClick={() => handleOpenEdit(rule)} className="rounded p-1 text-[var(--text-muted)] transition-colors hover:bg-[var(--surface-2)] hover:text-[var(--text)]"><Edit2 size={12} /></button>
                        <button title="Delete" onClick={() => setDeleteConfirm(rule.id)} className="rounded p-1 text-[var(--text-muted)] transition-colors hover:bg-red-500/10 hover:text-[var(--c-red)]"><Trash2 size={12} /></button>
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}

      {/* Delivery History Table */}
      {ruleTab === 2 && (
        <div className="overflow-x-auto rounded-xl border border-[var(--border-subtle)]">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-[var(--border-subtle)] bg-[var(--surface)]">
                {['Time', 'Alert', 'Source', 'Status', 'HTTP Code', 'Duration', 'Error'].map(h => <th key={h} className={TH}>{h}</th>)}
              </tr>
            </thead>
            <tbody>
              {deliveries.length === 0 ? (
                <tr><td colSpan={7} className="py-10 text-center text-[var(--text-muted)]">No delivery history yet. Send a test webhook or wait for alerts to fire.</td></tr>
              ) : (
                deliveries.map(d => (
                  <tr key={d.id} className="border-b border-[var(--border-subtle)] transition-colors hover:bg-[var(--surface)] last:border-0">
                    <td className={TD + ' whitespace-nowrap'}>{d.timestamp ? new Date(d.timestamp).toLocaleString() : '-'}</td>
                    <td className={TD}><span className="font-medium text-[var(--text)]">{d.alertName || '-'}</span></td>
                    <td className={TD}>
                      <span className={`rounded-full border px-2 py-0.5 text-[10px] ${d.source === 'test' ? 'border-blue-500/30 bg-blue-500/10 text-[var(--c-blue)]' : 'border-[var(--border-subtle)] bg-[var(--surface-2)] text-[var(--text-muted)]'}`}>
                        {d.source === 'test' && <Send size={9} className="mr-0.5 inline" />}{d.source}
                      </span>
                    </td>
                    <td className={TD}>
                      {d.success ? <CheckCircle size={15} className="text-[var(--c-emerald)]" /> : <XCircle size={15} className="text-[var(--c-red)]" />}
                    </td>
                    <td className={TD + ' font-mono'}>{d.httpStatus || '-'}</td>
                    <td className={TD}>{d.durationMs}ms</td>
                    <td className={TD}>
                      {d.errorMessage && <span className="block max-w-[180px] truncate text-[var(--c-red)]" title={d.errorMessage}>{d.errorMessage}</span>}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}

      {/* Create/Edit Rule Dialog */}
      <Modal
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
        title={editingRule ? 'Edit Alert Rule' : 'Create Alert Rule'}
        size="md"
        footer={
          <>
            <ModalBtn onClick={() => setDialogOpen(false)} disabled={saving}>Cancel</ModalBtn>
            <ModalBtn variant="primary" onClick={handleSave} disabled={saving || !formData.name || !formData.expr}>
              {saving ? <Loader2 size={13} className="animate-spin" /> : null}
              {editingRule ? 'Update' : 'Create'}
            </ModalBtn>
          </>
        }
      >
        <div className="flex flex-col gap-3">
          <FormField label="Name *">
            <Input value={formData.name} onChange={e => setFormData({ ...formData, name: e.target.value })} placeholder="e.g. HighMemoryUsage" />
          </FormField>
          <FormField label="PromQL Expression *">
            <Textarea value={formData.expr} onChange={e => setFormData({ ...formData, expr: e.target.value })} placeholder="e.g. system_memory_used_bytes / system_memory_total_bytes * 100 > 90" rows={2} className="font-mono text-[10px]" />
          </FormField>
          <div className="flex items-end gap-3">
            <div style={{ width: 110 }}>
              <FormField label="Duration">
                <Input value={formData.duration} onChange={e => setFormData({ ...formData, duration: e.target.value })} placeholder="5m" />
              </FormField>
            </div>
            <div style={{ flex: 1 }}>
              <FormField label="Severity">
                <select value={formData.severity} onChange={e => setFormData({ ...formData, severity: e.target.value })} className={selectCls}>
                  <option value="critical">Critical</option>
                  <option value="warning">Warning</option>
                  <option value="info">Info</option>
                </select>
              </FormField>
            </div>
            <div style={{ width: 80 }}>
              <FormField label="Enabled">
                <div className="flex h-[30px] items-center">
                  <Toggle checked={formData.enabled} onChange={v => setFormData({ ...formData, enabled: v })} />
                </div>
              </FormField>
            </div>
          </div>
          <FormField label="Description">
            <Textarea value={formData.description || ''} onChange={e => setFormData({ ...formData, description: e.target.value })} placeholder="Describe what this alert means and when it fires" rows={2} />
          </FormField>
        </div>
      </Modal>

      {/* Webhook Config Dialog */}
      <Modal
        open={webhookDialogOpen}
        onClose={() => { setWebhookDialogOpen(false); setGeneratedSecret(null); }}
        title="Configure Webhook"
        size="lg"
        footer={
          <>
            <ModalBtn onClick={() => { setWebhookDialogOpen(false); setGeneratedSecret(null); }}>Cancel</ModalBtn>
            {info?.webhookUrl && (
              <ModalBtn onClick={handleTestWebhook} disabled={testingWebhook}>
                {testingWebhook ? <Loader2 size={13} className="animate-spin" /> : <Send size={13} />} Send Test
              </ModalBtn>
            )}
            {info?.webhookUrl && (
              <ModalBtn variant="danger" onClick={() => { setWebhookUrl(''); handleSaveWebhook(); }} disabled={savingWebhook}>Disable</ModalBtn>
            )}
            <ModalBtn variant="primary" onClick={handleSaveWebhook} disabled={savingWebhook || !webhookUrl}>
              {savingWebhook ? <Loader2 size={13} className="animate-spin" /> : null} Save
            </ModalBtn>
          </>
        }
      >
        <div className="flex flex-col gap-4">
          <p className="text-xs text-[var(--text-muted)]">
            Set a webhook URL to receive alert notifications. Alerts will be sent as HTTP POST requests in Alertmanager webhook format. Leave empty to disable notifications.
          </p>
          {info?.webhookUrl && (
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/10 p-3 text-xs text-[var(--c-blue)]">Current webhook: {info.webhookUrl}</div>
          )}
          <FormField label="Webhook URL">
            <Input value={webhookUrl} onChange={e => setWebhookUrl(e.target.value)} placeholder="https://hooks.slack.com/services/... or https://your-endpoint.com/alerts" />
          </FormField>

          {/* HMAC Secret Section */}
          <div className="rounded-lg border border-[var(--border-subtle)] p-3">
            <div className="mb-2 flex items-center gap-2">
              <KeyRound size={14} className="text-[var(--text-muted)]" />
              <span className="text-xs font-medium text-[var(--text)]">HMAC Signing Secret</span>
              <span className={`rounded-full border px-2 py-0.5 text-[10px] ${info?.webhookSecretConfigured ? 'border-emerald-500/30 bg-emerald-500/10 text-[var(--c-emerald)]' : 'border-[var(--border-subtle)] bg-[var(--surface-2)] text-[var(--text-muted)]'}`}>
                {info?.webhookSecretConfigured ? 'Configured' : 'Not set'}
              </span>
            </div>
            <p className="mb-3 text-[10px] text-[var(--text-muted)]">
              When configured, all webhook payloads are signed with HMAC-SHA256. The signature is sent in the <code className="font-mono">X-Containarium-Signature</code> header.
            </p>
            <button
              onClick={handleGenerateSecret}
              disabled={generatingSecret}
              className="flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--surface)] px-3 py-1.5 text-xs text-[var(--text-secondary)] transition-colors hover:bg-[var(--surface-2)] disabled:opacity-50"
            >
              {generatingSecret ? <Loader2 size={12} className="animate-spin" /> : <KeyRound size={12} />}
              {info?.webhookSecretConfigured ? 'Rotate Secret' : 'Generate Secret'}
            </button>
            {generatedSecret && (
              <div className="mt-3 rounded-lg border border-amber-500/30 bg-amber-500/10 p-3">
                <p className="mb-2 text-[10px] font-semibold text-[var(--c-amber)]">Copy this secret now. It will not be shown again.</p>
                <div className="flex items-center gap-2">
                  <code className="flex-1 truncate rounded border border-[var(--border-subtle)] bg-[var(--surface-2)] px-2 py-1 font-mono text-[10px] text-[var(--text)]">{generatedSecret}</code>
                  <button onClick={() => { navigator.clipboard.writeText(generatedSecret); setSnackMessage('Secret copied to clipboard'); }}
                    className="rounded p-1.5 text-[var(--text-muted)] hover:text-[var(--text)]"><Copy size={13} /></button>
                </div>
              </div>
            )}
          </div>

          {/* Verification docs */}
          <details className="rounded-lg border border-[var(--border-subtle)]">
            <summary className="cursor-pointer px-4 py-2.5 text-xs font-medium text-[var(--text)] select-none">How to verify webhooks</summary>
            <div className="border-t border-[var(--border-subtle)] px-4 pb-4 pt-3">
              <p className="mb-3 text-[10px] text-[var(--text-muted)]">
                Containarium sends a <code className="font-mono">X-Containarium-Signature</code> header. Format: <code className="font-mono">sha256=&lt;hex HMAC-SHA256&gt;</code> over the raw request body.
              </p>
              {[
                { lang: 'Python', code: `import hmac, hashlib\n\ndef verify(body: bytes, secret: str, signature: str) -> bool:\n    expected = "sha256=" + hmac.new(\n        secret.encode(), body, hashlib.sha256\n    ).hexdigest()\n    return hmac.compare_digest(expected, signature)` },
                { lang: 'Go', code: `import (\n    "crypto/hmac"\n    "crypto/sha256"\n    "encoding/hex"\n)\n\nfunc verify(body []byte, secret, signature string) bool {\n    mac := hmac.New(sha256.New, []byte(secret))\n    mac.Write(body)\n    expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))\n    return hmac.Equal([]byte(expected), []byte(signature))\n}` },
                { lang: 'Node.js', code: `const crypto = require('crypto');\n\nfunction verify(body, secret, signature) {\n  const expected = 'sha256=' + crypto\n    .createHmac('sha256', secret)\n    .update(body)\n    .digest('hex');\n  return crypto.timingSafeEqual(\n    Buffer.from(expected), Buffer.from(signature)\n  );\n}` },
              ].map(({ lang, code }) => (
                <div key={lang} className="mb-3">
                  <p className="mb-1 text-[10px] font-semibold text-[var(--text-secondary)]">{lang}</p>
                  <pre className="overflow-auto rounded border border-[var(--border-subtle)] bg-[var(--surface-2)] p-2 text-[10px] font-mono text-[var(--text-secondary)]">{code}</pre>
                </div>
              ))}
            </div>
          </details>
        </div>
      </Modal>

      {/* Delete Confirmation Dialog */}
      <Modal
        open={!!deleteConfirm}
        onClose={() => setDeleteConfirm(null)}
        title="Delete Alert Rule"
        size="sm"
        footer={
          <>
            <ModalBtn onClick={() => setDeleteConfirm(null)}>Cancel</ModalBtn>
            <ModalBtn variant="danger" onClick={() => deleteConfirm && handleDelete(deleteConfirm)}>Delete</ModalBtn>
          </>
        }
      >
        <p className="text-sm text-[var(--text)]">Are you sure you want to delete this alert rule? This cannot be undone.</p>
      </Modal>

      {/* Rule Detail Dialog */}
      {detailRule && (
        <Modal
          open={!!detailRule}
          onClose={() => setDetailRule(null)}
          title={detailRule.name}
          size="lg"
          footer={<ModalBtn onClick={() => setDetailRule(null)}>Close</ModalBtn>}
        >
          <div className="flex flex-col gap-4">
            <div className="flex items-center gap-2">
              <Info size={14} className="text-[var(--accent)]" />
              <SeverityBadge severity={detailRule.severity} />
            </div>
            {detailRule.description && (
              <div>
                <p className="mb-1 text-[10px] font-medium text-[var(--text-muted)]">Description</p>
                <p className="text-xs text-[var(--text)]">{detailRule.description}</p>
              </div>
            )}
            <div>
              <p className="mb-1 text-[10px] font-medium text-[var(--text-muted)]">PromQL Expression</p>
              <pre className="overflow-x-auto rounded border border-[var(--border-subtle)] bg-[var(--surface-2)] p-3 text-[11px] font-mono text-[var(--text)] whitespace-pre-wrap break-all">{detailRule.expr}</pre>
            </div>
            <div className="flex gap-6">
              <div>
                <p className="mb-1 text-[10px] font-medium text-[var(--text-muted)]">Duration</p>
                <code className="font-mono text-xs text-[var(--text)]">{detailRule.duration}</code>
              </div>
              <div>
                <p className="mb-1 text-[10px] font-medium text-[var(--text-muted)]">Severity</p>
                <SeverityBadge severity={detailRule.severity} />
              </div>
              <div>
                <p className="mb-1 text-[10px] font-medium text-[var(--text-muted)]">Status</p>
                <span className={`rounded-full border px-2 py-0.5 text-[10px] ${detailRule.enabled ? 'border-emerald-500/30 bg-emerald-500/10 text-[var(--c-emerald)]' : 'border-[var(--border-subtle)] bg-[var(--surface-2)] text-[var(--text-muted)]'}`}>
                  {detailRule.enabled ? 'Enabled' : 'Disabled'}
                </span>
              </div>
            </div>
            {detailRule.labels && Object.keys(detailRule.labels).length > 0 && (
              <div>
                <p className="mb-1 text-[10px] font-medium text-[var(--text-muted)]">Labels</p>
                <div className="flex flex-wrap gap-1">
                  {Object.entries(detailRule.labels).map(([k, v]) => (
                    <code key={k} className="rounded border border-[var(--border-subtle)] bg-[var(--surface-2)] px-2 py-0.5 text-[10px] font-mono text-[var(--text-secondary)]">{k}={v}</code>
                  ))}
                </div>
              </div>
            )}
            <details className="rounded-lg border border-[var(--border-subtle)]">
              <summary className="cursor-pointer px-4 py-2.5 text-xs font-medium text-[var(--text)] select-none">How this rule works</summary>
              <div className="border-t border-[var(--border-subtle)] px-4 pb-4 pt-3 text-[10px] text-[var(--text-muted)]">
                <p className="mb-3">This alert fires when the PromQL expression evaluates to <strong>true</strong> continuously for <code className="font-mono">{detailRule.duration}</code>.</p>
                <p className="mb-1 font-semibold text-[var(--text-secondary)]">Equivalent vmalert YAML</p>
                <pre className="overflow-auto rounded border border-[var(--border-subtle)] bg-[var(--surface-2)] p-2 font-mono">{`- alert: ${detailRule.name}\n  expr: ${detailRule.expr}\n  for: ${detailRule.duration}\n  labels:\n    severity: ${detailRule.severity}`}</pre>
              </div>
            </details>
          </div>
        </Modal>
      )}

      {/* Toast */}
      {snackMessage && (
        <div onClick={() => setSnackMessage(null)} className="fixed bottom-4 left-1/2 z-50 -translate-x-1/2 cursor-pointer rounded-lg border border-[var(--border)] bg-[var(--surface-2)] px-4 py-2.5 text-xs text-[var(--text)] shadow-xl">
          {snackMessage}
        </div>
      )}
    </div>
  );
}
