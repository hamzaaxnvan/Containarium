'use client';

import { useState } from 'react';
import { Loader2, CheckCircle, Ban, Info } from 'lucide-react';
import { Modal, ModalBtn } from '@/src/components/ui/Modal';
import { NetworkACL, ACLRule, ACLPreset, ACLPresetInfo, getACLPresetName, getACLActionDisplay } from '@/src/types/app';

interface FirewallEditorProps {
  open: boolean;
  onClose: () => void;
  acl: NetworkACL | null;
  presets: ACLPresetInfo[];
  isLoading: boolean;
  appName: string;
  username: string;
  onSave: (preset: ACLPreset) => Promise<void>;
}

function RulesTable({ rules, title }: { rules: ACLRule[]; title: string }) {
  if (rules.length === 0) {
    return <p className="py-4 text-center text-xs text-[var(--text-muted)]">No {title.toLowerCase()} rules configured</p>;
  }
  return (
    <div className="overflow-x-auto rounded-lg border border-[var(--border-subtle)]">
      <table className="w-full text-xs">
        <thead>
          <tr className="border-b border-[var(--border-subtle)] bg-[var(--surface-2)]">
            {['Action', 'Source/Destination', 'Port', 'Protocol', 'Description'].map(h => (
              <th key={h} className="px-3 py-2 text-left font-medium text-[var(--text-secondary)]">{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rules.map((rule, i) => {
            const ad = getACLActionDisplay(rule.action);
            const isAllow = rule.action === 'ACL_ACTION_ALLOW';
            return (
              <tr key={i} className="border-b border-[var(--border-subtle)] last:border-0">
                <td className="px-3 py-2">
                  <span className={`flex w-fit items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-medium ${isAllow ? 'border-emerald-500/30 bg-emerald-500/10 text-[var(--c-emerald)]' : 'border-red-500/30 bg-red-500/10 text-[var(--c-red)]'}`}>
                    {isAllow ? <CheckCircle size={10} /> : <Ban size={10} />}
                    {ad.label}
                  </span>
                </td>
                <td className="px-3 py-2 font-mono text-[var(--text-secondary)]">{rule.source || rule.destination || '*'}</td>
                <td className="px-3 py-2 font-mono text-[var(--text-secondary)]">{rule.destinationPort || '*'}</td>
                <td className="px-3 py-2 text-[var(--text-secondary)]">{rule.protocol || 'any'}</td>
                <td className="px-3 py-2 text-[var(--text-muted)]">{rule.description}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

const TABS = ['Choose Preset', 'Preview Rules', 'Current Rules'] as const;

export default function FirewallEditor({ open, onClose, acl, presets, isLoading, appName, username, onSave }: FirewallEditorProps) {
  const [selectedPreset, setSelectedPreset] = useState<ACLPreset>(acl?.preset || 'ACL_PRESET_FULL_ISOLATION');
  const [tab, setTab] = useState(0);
  const [saving, setSaving] = useState(false);

  const selectedPresetInfo = presets.find(p => p.preset === selectedPreset);
  const tabCount = acl ? 3 : 2;

  const handleSave = async () => {
    setSaving(true);
    try { await onSave(selectedPreset); onClose(); } catch { /* ignore */ } finally { setSaving(false); }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={`Firewall Settings — ${appName}`}
      size="lg"
      footer={
        <>
          <ModalBtn onClick={onClose} disabled={saving}>Cancel</ModalBtn>
          <ModalBtn variant="primary" onClick={handleSave} disabled={saving || isLoading}>
            {saving && <Loader2 size={13} className="animate-spin" />}
            Apply Preset
          </ModalBtn>
        </>
      }
    >
      {isLoading ? (
        <div className="flex justify-center py-10">
          <Loader2 size={24} className="animate-spin text-[var(--text-secondary)]" />
        </div>
      ) : (
        <div className="flex flex-col gap-4">
          <div className="flex items-start gap-2 rounded-lg border border-blue-500/30 bg-blue-500/10 p-3 text-xs text-[var(--c-blue)]">
            <Info size={14} className="mt-0.5 shrink-0" />
            <span>Firewall rules control network traffic to and from your application container. We recommend <strong>Full Isolation</strong> for production apps.</span>
          </div>
          <p className="text-xs text-[var(--text-muted)]">Owner: {username}</p>

          {/* Tabs */}
          <div className="flex border-b border-[var(--border-subtle)]">
            {TABS.slice(0, tabCount).map((t, i) => (
              <button
                key={t}
                onClick={() => setTab(i)}
                className={`px-4 py-2 text-xs font-medium border-b-2 -mb-px transition-colors ${tab === i ? 'border-[var(--accent)] text-[var(--accent)]' : 'border-transparent text-[var(--text-secondary)] hover:text-[var(--text)]'}`}
              >
                {t}
              </button>
            ))}
          </div>

          {tab === 0 && (
            <div className="flex flex-col gap-2">
              {presets.filter(p => p.preset !== 'ACL_PRESET_CUSTOM').map(preset => {
                const isSelected = preset.preset === selectedPreset;
                return (
                  <button
                    key={preset.preset}
                    onClick={() => setSelectedPreset(preset.preset)}
                    className={`flex items-center justify-between rounded-xl border p-4 text-left transition-colors ${isSelected ? 'border-[var(--accent)] bg-blue-500/5' : 'border-[var(--border-subtle)] hover:border-[var(--border)] hover:bg-[var(--surface-2)]'}`}
                  >
                    <div>
                      <p className="text-sm font-semibold text-[var(--text)]">{preset.name}</p>
                      <p className="text-xs text-[var(--text-muted)]">{preset.description}</p>
                    </div>
                    {isSelected && <CheckCircle size={16} className="text-[var(--accent)]" />}
                  </button>
                );
              })}
            </div>
          )}

          {tab === 1 && selectedPresetInfo && (
            <div className="flex flex-col gap-4">
              <div>
                <p className="mb-2 text-xs font-medium text-[var(--text-secondary)]">Ingress Rules (Incoming Traffic)</p>
                <RulesTable rules={selectedPresetInfo.defaultIngressRules} title="ingress" />
              </div>
              <div>
                <p className="mb-2 text-xs font-medium text-[var(--text-secondary)]">Egress Rules (Outgoing Traffic)</p>
                <RulesTable rules={selectedPresetInfo.defaultEgressRules} title="egress" />
              </div>
            </div>
          )}

          {tab === 2 && acl && (
            <div className="flex flex-col gap-4">
              <p className="text-xs text-[var(--text-muted)]">Current preset: <strong className="text-[var(--text-secondary)]">{getACLPresetName(acl.preset)}</strong></p>
              <div>
                <p className="mb-2 text-xs font-medium text-[var(--text-secondary)]">Ingress Rules</p>
                <RulesTable rules={acl.ingressRules} title="ingress" />
              </div>
              <div>
                <p className="mb-2 text-xs font-medium text-[var(--text-secondary)]">Egress Rules</p>
                <RulesTable rules={acl.egressRules} title="egress" />
              </div>
            </div>
          )}
        </div>
      )}
    </Modal>
  );
}
