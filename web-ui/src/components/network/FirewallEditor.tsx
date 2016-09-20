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
