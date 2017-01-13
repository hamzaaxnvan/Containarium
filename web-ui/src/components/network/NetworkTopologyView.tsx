'use client';

import { useState, useMemo } from 'react';
import { RefreshCw, Plus, Trash2, Globe, Network, Lock, ExternalLink, ChevronDown, ChevronRight } from 'lucide-react';
import { NetworkTopology, ProxyRoute, DNSRecord, RouteProtocol, PassthroughRoute, getRouteProtocolName, isGRPCRoute, isTLSPassthroughProtocol } from '@/src/types/app';
import { Modal, ModalBtn, FormField, Input } from '@/src/components/ui/Modal';

interface NetworkTopologyViewProps {
  topology: NetworkTopology;
  routes: ProxyRoute[];
  passthroughRoutes?: PassthroughRoute[];
  dnsRecords?: DNSRecord[];
  baseDomain?: string;
  isLoading: boolean;
  error?: Error | null;
  includeStopped: boolean;
  onIncludeStoppedChange: (value: boolean) => void;
  onAddRoute?: (domain: string, targetIp: string, targetPort: number, protocol?: RouteProtocol) => Promise<void>;
  onDeleteRoute?: (domain: string) => Promise<void>;
  onToggleRoute?: (domain: string, enabled: boolean) => Promise<void>;
  onAddPassthroughRoute?: (externalPort: number, targetIp: string, targetPort: number, protocol?: RouteProtocol, containerName?: string) => Promise<void>;
  onDeletePassthroughRoute?: (externalPort: number, protocol?: RouteProtocol) => Promise<void>;
  onTogglePassthroughRoute?: (externalPort: number, protocol: RouteProtocol, enabled: boolean) => Promise<void>;
  onRefresh: () => void;
}

function Toggle({ checked, onChange, disabled }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <button role="switch" aria-checked={checked} onClick={() => !disabled && onChange(!checked)} disabled={disabled}
      className={`relative h-4 w-8 shrink-0 rounded-full transition-colors disabled:opacity-40 ${checked ? 'bg-[var(--accent)]' : 'bg-[var(--border)]'}`}>
      <span className={`absolute top-0.5 h-3 w-3 rounded-full bg-white shadow transition-transform ${checked ? 'translate-x-4' : 'translate-x-0.5'}`} />
    </button>
  );
}

const TH = 'px-3 py-2.5 text-left text-xs font-medium text-[var(--text-secondary)] whitespace-nowrap';
const TD = 'px-3 py-2 text-xs text-[var(--text-secondary)]';

interface UnifiedRouteTableProps {
  proxyRoutes: ProxyRoute[];
  passthroughRoutes: PassthroughRoute[];
  onDeleteProxyRoute?: (domain: string) => void;
  onToggleProxyRoute?: (domain: string, enabled: boolean) => void;
  onDeletePassthroughRoute?: (externalPort: number, protocol?: RouteProtocol) => void;
  onTogglePassthroughRoute?: (externalPort: number, protocol: RouteProtocol, enabled: boolean) => void;
}

function UnifiedRouteTable({ proxyRoutes, passthroughRoutes, onDeleteProxyRoute, onToggleProxyRoute, onDeletePassthroughRoute, onTogglePassthroughRoute }: UnifiedRouteTableProps) {
  const totalRoutes = proxyRoutes.length + passthroughRoutes.length;
  const [collapsedGroups, setCollapsedGroups] = useState<Record<string, boolean>>({});

  const getParentDomain = (fullDomain: string) => { const p = fullDomain.split('.'); return p.length <= 2 ? fullDomain : p.slice(1).join('.'); };
  const getSubdomainPrefix = (fullDomain: string) => { const p = fullDomain.split('.'); return p.length <= 2 ? fullDomain : p[0]; };

  const { httpGrpcRoutes, tlsPassthroughRoutes } = useMemo(() => {
    const httpGrpc: ProxyRoute[] = [], tlsPassthrough: ProxyRoute[] = [];
    for (const r of proxyRoutes) { (isTLSPassthroughProtocol(r.protocol) ? tlsPassthrough : httpGrpc).push(r); }
    return { httpGrpcRoutes: httpGrpc, tlsPassthroughRoutes: tlsPassthrough };
  }, [proxyRoutes]);

  const proxyGroups = useMemo(() => {
    const groups: Record<string, ProxyRoute[]> = {};
    for (const r of httpGrpcRoutes) {
      const domain = r.fullDomain || r.subdomain;
      const parent = getParentDomain(domain);
      if (!groups[parent]) groups[parent] = [];
      groups[parent].push(r);
    }
    const sorted = Object.entries(groups).sort(([a], [b]) => a.localeCompare(b));
    for (const [, routes] of sorted) routes.sort((a, b) => (a.fullDomain || '').localeCompare(b.fullDomain || ''));
    return sorted;
  }, [httpGrpcRoutes]);

  const toggleGroup = (group: string) => setCollapsedGroups(prev => ({ ...prev, [group]: !prev[group] }));

  if (totalRoutes === 0) return <p className="py-8 text-center text-xs text-[var(--text-muted)]">No routes configured</p>;

  const protocolBadge = (protocol: string | undefined) => {
    if (isTLSPassthroughProtocol(protocol as RouteProtocol | undefined)) return 'border-violet-500/30 bg-violet-500/10 text-[var(--c-violet)]';
    if (isGRPCRoute(protocol as RouteProtocol | undefined)) return 'border-blue-500/30 bg-blue-500/10 text-[var(--c-blue)]';
    return 'border-[var(--border-subtle)] bg-[var(--surface-2)] text-[var(--text-muted)]';
  };

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs">
        <thead>
          <tr className="border-b border-[var(--border-subtle)] bg-[var(--surface)]">
            {['Type', 'Endpoint', 'Target', 'Protocol', 'Container', 'Enabled', ''].map(h => <th key={h} className={TH}>{h}</th>)}
          </tr>
        </thead>
        <tbody>
          {/* Proxy route groups */}
          {proxyGroups.map(([parentDomain, routes]) => {
            const isCollapsed = collapsedGroups[parentDomain] ?? false;
            const activeCount = routes.filter(r => r.active).length;
            return [
              <tr key={`group-${parentDomain}`} onClick={() => toggleGroup(parentDomain)}
                className="cursor-pointer border-b border-[var(--border-subtle)] bg-[var(--surface-2)] transition-colors hover:bg-[var(--surface)]">
                <td colSpan={2} className="px-3 py-2">
                  <div className="flex items-center gap-1.5">
                    {isCollapsed ? <ChevronRight size={12} /> : <ChevronDown size={12} />}
                    <Globe size={13} className="text-[var(--accent)]" />
                    <span className="font-medium text-[var(--text)]">*.{parentDomain}</span>
                    <span className="rounded-full border border-[var(--border-subtle)] bg-[var(--surface)] px-1.5 py-0.5 text-[10px] text-[var(--text-muted)]">{routes.length}</span>
                  </div>
                </td>
                <td colSpan={3} className="px-3 py-2 text-[10px] text-[var(--text-muted)]">{activeCount}/{routes.length} active</td>
                <td colSpan={2} />
              </tr>,
              ...(!isCollapsed ? routes.map(route => (
                <tr key={`proxy-${route.fullDomain || route.subdomain}`}
                  className={`border-b border-[var(--border-subtle)] transition-colors hover:bg-[var(--surface)] last:border-0 ${!route.active ? 'opacity-60' : ''}`}>
                  <td className={TD}>
                    <span title="Proxy: TLS terminated at Caddy" className="rounded-full border border-blue-500/30 bg-blue-500/10 px-2 py-0.5 text-[10px] text-[var(--c-blue)] flex items-center gap-1 w-fit">
                      <Globe size={10} /> Proxy
                    </span>
                  </td>
                  <td className="px-3 py-2 pl-6">
                    <a href={`https://${route.fullDomain}`} target="_blank" rel="noopener noreferrer" onClick={e => e.stopPropagation()}
                      className={`flex items-center gap-1 font-mono text-[10px] hover:underline ${route.active ? 'text-[var(--accent)]' : 'text-[var(--text-muted)] line-through'}`}>
                      <strong>{getSubdomainPrefix(route.fullDomain)}</strong>
                      <span className="text-[var(--text-muted)]">.{parentDomain}</span>
                      <ExternalLink size={10} />
                    </a>
                  </td>
                  <td className={TD + ' font-mono'}>{route.containerIp ? `${route.containerIp}:${route.port}` : 'N/A'}</td>
                  <td className={TD}>
                    <span className={`rounded-full border px-2 py-0.5 text-[10px] ${protocolBadge(route.protocol)}`}>{getRouteProtocolName(route.protocol)}</span>
                  </td>
                  <td className={TD}>{route.appName || '-'}</td>
                  <td className={TD}><Toggle checked={route.active} onChange={v => onToggleProxyRoute?.(route.fullDomain, v)} disabled={!onToggleProxyRoute} /></td>
                  <td className="px-3 py-2">
                    {onDeleteProxyRoute && (
                      <button onClick={() => onDeleteProxyRoute(route.fullDomain)} title="Delete route"
                        className="rounded p-1 text-[var(--text-muted)] transition-colors hover:bg-red-500/10 hover:text-[var(--c-red)]"><Trash2 size={12} /></button>
                    )}
                  </td>
                </tr>
              )) : []),
            ];
          })}

          {/* TLS Passthrough section */}
          {tlsPassthroughRoutes.length > 0 && (
            <tr className="border-b border-[var(--border-subtle)] bg-[var(--surface-2)]">
              <td colSpan={7} className="px-3 py-2">
                <div className="flex items-center gap-1.5">
                  <Lock size={13} className="text-[var(--c-violet)]" />
                  <span className="font-medium text-[var(--text)]">TLS Passthrough (SNI)</span>
                  <span className="rounded-full border border-[var(--border-subtle)] bg-[var(--surface)] px-1.5 py-0.5 text-[10px] text-[var(--text-muted)]">{tlsPassthroughRoutes.length}</span>
                  <span className="ml-2 text-[10px] text-[var(--text-muted)]">All on :443 — raw TLS forwarded, mTLS preserved</span>
                </div>
              </td>
            </tr>
          )}
