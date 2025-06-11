'use client';

import { useState, useEffect } from 'react';
import { Loader2, Download, XCircle, CheckCircle } from 'lucide-react';
import { Modal, ModalBtn, FormField, Input, Textarea } from '@/src/components/ui/Modal';
import { CreateContainerRequest, AVAILABLE_STACKS, BackendInfo, Stack } from '@/src/types/container';
import { generateSSHKeyPair, downloadPrivateKey, SSHKeyPair } from '@/src/lib/sshkey';
import { CreateContainerProgress } from '@/src/lib/hooks/useContainers';
import { getClient } from '@/src/lib/api/client';
import { Server } from '@/src/types/server';

interface CreateContainerDialogProps {
  open: boolean;
  onClose: () => void;
  onSubmit: (request: CreateContainerRequest, onProgress?: (progress: CreateContainerProgress) => void) => Promise<unknown>;
  networkCidr?: string;
  backends?: BackendInfo[];
  server?: Server | null;
}

const IMAGES = [
  { value: 'images:ubuntu/24.04', label: 'Ubuntu 24.04' },
  { value: 'images:ubuntu/22.04', label: 'Ubuntu 22.04' },
  { value: 'images:debian/12', label: 'Debian 12' },
  { value: 'images:alpine/3.19', label: 'Alpine 3.19' },
  { value: 'windows:server-2022', label: 'Windows Server 2022 (VM)', osType: 4 },
];

const DEFAULT_NETWORK_CIDR = '10.100.0.0/24';

function ipToNumber(ip: string): number | null {
  const parts = ip.split('.');
  if (parts.length !== 4) return null;
  let result = 0;
  for (const part of parts) {
    const num = parseInt(part, 10);
    if (isNaN(num) || num < 0 || num > 255) return null;
    result = result * 256 + num;
  }
  return result;
}

function isIPInCIDR(ip: string, cidr: string): boolean {
  const [networkAddr, prefix] = cidr.split('/');
  const prefixLen = parseInt(prefix, 10);
  if (isNaN(prefixLen) || prefixLen < 0 || prefixLen > 32) return false;
  const ipNum = ipToNumber(ip), networkNum = ipToNumber(networkAddr);
  if (ipNum === null || networkNum === null) return false;
  const mask = ~((1 << (32 - prefixLen)) - 1) >>> 0;
  return (ipNum & mask) === (networkNum & mask);
}

function validateStaticIP(ip: string, cidr: string): { valid: boolean; error?: string } {
  if (!ip) return { valid: true };
  if (!/^(\d{1,3}\.){3}\d{1,3}$/.test(ip)) return { valid: false, error: 'Invalid IP address format' };
  for (const part of ip.split('.')) { const n = parseInt(part, 10); if (n < 0 || n > 255) return { valid: false, error: 'Invalid IP address: octets must be 0-255' }; }
  if (!isIPInCIDR(ip, cidr)) return { valid: false, error: `IP must be within network ${cidr}` };
  const np = cidr.split('/')[0].split('.');
  const gw = [...np.slice(0, 3), '1'].join('.');
  const bc = [...np.slice(0, 3), '255'].join('.');
  if (ip === gw) return { valid: false, error: 'Cannot use gateway IP address' };
  if (ip === bc) return { valid: false, error: 'Cannot use broadcast IP address' };
  return { valid: true };
}

function SelectField({ label, value, onChange, disabled, children }: { label: string; value: string; onChange: (v: string) => void; disabled?: boolean; children: React.ReactNode }) {
  return (
    <FormField label={label}>
      <select
        value={value}
        onChange={e => onChange(e.target.value)}
        disabled={disabled}
        className="w-full rounded-md border border-[var(--border-subtle)] bg-[var(--surface-2)] px-3 py-2 text-xs text-[var(--text)] focus:border-[var(--accent)] focus:outline-none disabled:opacity-50"
      >
        {children}
      </select>
    </FormField>
  );
}

export default function CreateContainerDialog({ open, onClose, onSubmit, networkCidr, backends, server }: CreateContainerDialogProps) {
  const effectiveCidr = networkCidr || DEFAULT_NETWORK_CIDR;

  const [username, setUsername] = useState('');
  const [image, setImage] = useState('images:ubuntu/24.04');
  const [cpu, setCpu] = useState('4');
  const [memory, setMemory] = useState('4GB');
  const [disk, setDisk] = useState('50GB');
  const [stack, setStack] = useState('');
  const [stackCatalog, setStackCatalog] = useState<Stack[]>(AVAILABLE_STACKS);
  const [stackParamValues, setStackParamValues] = useState<Record<string, string>>({});
  const [gpu, setGpu] = useState('');
  const [backendId, setBackendId] = useState('');
  const [enablePodman, setEnablePodman] = useState(true);
  const [staticIp, setStaticIp] = useState('');
  const [staticIpError, setStaticIpError] = useState<string | null>(null);
  const [labelsText, setLabelsText] = useState('');
  const [autoGenerateKey, setAutoGenerateKey] = useState(true);
  const [sshPublicKey, setSshPublicKey] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [generatedKeyPair, setGeneratedKeyPair] = useState<SSHKeyPair | null>(null);
  const [progress, setProgress] = useState<CreateContainerProgress | null>(null);

  const parseLabels = (text: string): Record<string, string> => {
    const labels: Record<string, string> = {};
    if (!text.trim()) return labels;
    for (const pair of text.split(',')) {
      const [key, ...rest] = pair.split('=');
      const k = key?.trim(), v = rest.join('=').trim();
      if (k && v) labels[k] = v;
    }
    return labels;
  };

  useEffect(() => {
    if (!open || !server) return;
    let cancelled = false;
    (async () => {
      try {
        const stacks = await getClient(server).listStacks();
        if (!cancelled && stacks.length > 0) {
          setStackCatalog([{ id: '', name: 'None', description: 'No pre-configured stack', icon: 'none' }, ...stacks]);
        }
      } catch { /* use hardcoded fallback */ }
    })();
    return () => { cancelled = true; };
  }, [open, server]);

  useEffect(() => {
    const selected = stackCatalog.find(s => s.id === stack);
    if (selected?.parameters && selected.parameters.length > 0) {
      const seeded: Record<string, string> = {};
      for (const p of selected.parameters) seeded[p.name] = stackParamValues[p.name] ?? p.default ?? '';
      setStackParamValues(seeded);
    } else {
      setStackParamValues({});
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stack, stackCatalog]);

  const resetForm = () => {
    setUsername(''); setImage('images:ubuntu/24.04'); setCpu('4'); setMemory('4GB'); setDisk('50GB');
    setStack(''); setStackParamValues({}); setGpu(''); setBackendId(''); setEnablePodman(true);
    setStaticIp(''); setStaticIpError(null); setLabelsText(''); setAutoGenerateKey(true);
    setSshPublicKey(''); setSubmitting(false); setError(null); setSuccess(false);
    setGeneratedKeyPair(null); setProgress(null);
  };

  const handleClose = () => { if (submitting) return; resetForm(); onClose(); };

  const handleSubmit = async () => {
    if (!username) { setError('Please enter a username'); return; }
    const selectedImage = IMAGES.find(i => i.value === image);
    const isWindows = !!(selectedImage && 'osType' in selectedImage);
    if (!isWindows && !autoGenerateKey && !sshPublicKey) { setError('Please enter an SSH public key or enable auto-generate'); return; }
    if (staticIp) {
      const v = validateStaticIP(staticIp, effectiveCidr);
      if (!v.valid) { setError(v.error || 'Invalid static IP'); return; }
    }

    setSubmitting(true); setError(null); setProgress({ state: 'Creating', message: 'Preparing...' });

    try {
      let publicKey = sshPublicKey;
      if (autoGenerateKey) {
        setProgress({ state: 'Creating', message: 'Generating SSH key pair...' });
        const kp = await generateSSHKeyPair(username);
        publicKey = kp.publicKey;
        setGeneratedKeyPair(kp);
      }

      const labels = parseLabels(labelsText);
      const request: CreateContainerRequest = {
        username,
        image: isWindows ? undefined : image,
        resources: { cpu: isWindows ? (cpu || '4') : cpu, memory: isWindows ? (memory || '8GB') : memory, disk: isWindows ? (disk || '50GB') : disk },
        sshKeys: isWindows ? undefined : [publicKey],
        labels: Object.keys(labels).length > 0 ? labels : undefined,
        enablePodman: isWindows ? false : enablePodman,
        stack: isWindows ? undefined : (stack || undefined),
        stackParameters: isWindows || !stack || Object.keys(stackParamValues).length === 0 ? undefined : stackParamValues,
