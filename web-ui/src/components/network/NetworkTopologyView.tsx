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
          {tlsPassthroughRoutes.map(route => (
            <tr key={`tls-${route.fullDomain || route.subdomain}`}
              className={`border-b border-[var(--border-subtle)] transition-colors hover:bg-[var(--surface)] last:border-0 ${!route.active ? 'opacity-60' : ''}`}>
              <td className={TD}>
                <span title="TLS Passthrough: Raw TLS forwarded via SNI" className="rounded-full border border-violet-500/30 bg-violet-500/10 px-2 py-0.5 text-[10px] text-[var(--c-violet)] flex items-center gap-1 w-fit">
                  <Lock size={10} /> TLS Pass
                </span>
              </td>
              <td className={`${TD} font-mono pl-6 ${!route.active ? 'line-through' : ''}`}>{route.fullDomain || route.subdomain}:443</td>
              <td className={TD + ' font-mono'}>{route.containerIp ? `${route.containerIp}:${route.port}` : 'N/A'}</td>
              <td className={TD}><span className="rounded-full border border-violet-500/30 bg-violet-500/10 px-2 py-0.5 text-[10px] text-[var(--c-violet)]">TLS Passthrough</span></td>
              <td className={TD}>{route.appName || '-'}</td>
              <td className={TD}><Toggle checked={route.active} onChange={v => onToggleProxyRoute?.(route.fullDomain, v)} disabled={!onToggleProxyRoute} /></td>
              <td className="px-3 py-2">
                {onDeleteProxyRoute && (
                  <button onClick={() => onDeleteProxyRoute(route.fullDomain)} title="Delete route"
                    className="rounded p-1 text-[var(--text-muted)] transition-colors hover:bg-red-500/10 hover:text-[var(--c-red)]"><Trash2 size={12} /></button>
                )}
              </td>
            </tr>
          ))}

          {/* Passthrough TCP/UDP section */}
          {passthroughRoutes.length > 0 && (
            <tr className="border-b border-[var(--border-subtle)] bg-[var(--surface-2)]">
              <td colSpan={7} className="px-3 py-2">
                <div className="flex items-center gap-1.5">
                  <Network size={13} className="text-[var(--c-amber)]" />
                  <span className="font-medium text-[var(--text)]">Passthrough (TCP/UDP)</span>
                  <span className="rounded-full border border-[var(--border-subtle)] bg-[var(--surface)] px-1.5 py-0.5 text-[10px] text-[var(--text-muted)]">{passthroughRoutes.length}</span>
                </div>
              </td>
            </tr>
          )}
          {passthroughRoutes.map(route => (
            <tr key={`passthrough-${route.externalPort}-${route.protocol}`}
              className={`border-b border-[var(--border-subtle)] transition-colors hover:bg-[var(--surface)] last:border-0 ${!route.active ? 'opacity-60' : ''}`}>
              <td className={TD}>
                <span title="Passthrough: Direct TCP/UDP forwarding" className="rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[10px] text-[var(--c-amber)] flex items-center gap-1 w-fit">
                  <Network size={10} /> Pass
                </span>
              </td>
              <td className={`${TD} font-mono ${!route.active ? 'line-through' : ''}`}>:{route.externalPort}</td>
              <td className={TD + ' font-mono'}>{route.targetIp}:{route.targetPort}</td>
              <td className={TD}><span className="rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[10px] text-[var(--c-amber)]">{getRouteProtocolName(route.protocol)}</span></td>
              <td className={TD}>{route.containerName || '-'}</td>
              <td className={TD}><Toggle checked={route.active} onChange={v => onTogglePassthroughRoute?.(route.externalPort, route.protocol, v)} disabled={!onTogglePassthroughRoute} /></td>
              <td className="px-3 py-2">
                {onDeletePassthroughRoute && (
                  <button onClick={() => onDeletePassthroughRoute(route.externalPort, route.protocol)} title="Delete route"
                    className="rounded p-1 text-[var(--text-muted)] transition-colors hover:bg-red-500/10 hover:text-[var(--c-red)]"><Trash2 size={12} /></button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export default function NetworkTopologyView({
  topology, routes, passthroughRoutes = [], dnsRecords = [], baseDomain = '', isLoading, error,
  includeStopped, onIncludeStoppedChange, onAddRoute, onDeleteRoute, onToggleRoute,
  onAddPassthroughRoute, onDeletePassthroughRoute, onTogglePassthroughRoute, onRefresh,
}: NetworkTopologyViewProps) {
  const [addRouteDialog, setAddRouteDialog] = useState(false);
  const [newRoute, setNewRoute] = useState({ domain: '', targetIp: '', targetPort: '', protocol: 'ROUTE_PROTOCOL_HTTP' as RouteProtocol, externalPort: '' });
  const [deleteRouteDialog, setDeleteRouteDialog] = useState<{ open: boolean; domain: string }>({ open: false, domain: '' });
  const [deletePassthroughDialog, setDeletePassthroughDialog] = useState<{ open: boolean; externalPort: number; protocol: RouteProtocol }>({ open: false, externalPort: 0, protocol: 'ROUTE_PROTOCOL_TCP' });

  const domainSuggestions = useMemo(() => {
    const suggestions = dnsRecords.map(r => ({ subdomain: r.name, fullDomain: r.data }));
    for (const domain of routes.map(r => r.fullDomain).filter(Boolean)) {
      if (!suggestions.find(s => s.fullDomain === domain)) {
        suggestions.push({ subdomain: domain.replace('.' + baseDomain, ''), fullDomain: domain });
      }
    }
    return suggestions;
  }, [dnsRecords, routes, baseDomain]);

  const containerOptions = topology.nodes
    .filter(n => n.type === 'container' && n.ipAddress && n.state === 'running')
    .map(n => ({ name: n.name, ip: n.ipAddress || '' }));

  const handleAddRoute = async () => {
    if (onAddRoute && newRoute.domain && newRoute.targetIp && newRoute.targetPort) {
      await onAddRoute(newRoute.domain, newRoute.targetIp, parseInt(newRoute.targetPort, 10), newRoute.protocol);
      setAddRouteDialog(false);
      setNewRoute({ domain: '', targetIp: '', targetPort: '', protocol: 'ROUTE_PROTOCOL_HTTP', externalPort: '' });
    }
  };

  const handleConfirmDeleteRoute = async () => {
    if (onDeleteRoute) { await onDeleteRoute(deleteRouteDialog.domain); setDeleteRouteDialog({ open: false, domain: '' }); }
  };

  const handleConfirmDeletePassthrough = async () => {
    if (onDeletePassthroughRoute) {
      await onDeletePassthroughRoute(deletePassthroughDialog.externalPort, deletePassthroughDialog.protocol);
      setDeletePassthroughDialog({ open: false, externalPort: 0, protocol: 'ROUTE_PROTOCOL_TCP' });
    }
  };

  const inputCls = 'w-full rounded-md border border-[var(--border-subtle)] bg-[var(--surface-2)] px-3 py-1.5 text-xs text-[var(--text)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent)] focus:outline-none';
  const selectCls = 'w-full appearance-none rounded-md border border-[var(--border-subtle)] bg-[var(--surface-2)] px-3 py-1.5 text-xs text-[var(--text)] focus:border-[var(--accent)] focus:outline-none';

  if (isLoading && topology.nodes.length === 0) {
    return <div className="flex min-h-[300px] items-center justify-center"><div className="h-5 w-5 animate-spin rounded-full border-2 border-[var(--border)] border-t-[var(--accent)]" /></div>;
  }

  if (error) {
    return (
      <div className="flex flex-col items-center gap-2 py-16 text-center">
        <p className="text-sm font-medium text-[var(--c-red)]">Failed to load network topology</p>
        <p className="text-xs text-[var(--text-muted)]">{error.message}</p>
        <button onClick={onRefresh} className="mt-2 rounded-md border border-[var(--border-subtle)] px-3 py-1.5 text-xs text-[var(--text-secondary)] hover:bg-[var(--surface-2)]">Retry</button>
      </div>
    );
  }

  return (
    <div className="p-6">
      {/* Header */}
      <div className="mb-6 flex items-center gap-3">
        <h1 className="mr-auto text-base font-semibold text-[var(--text)]">Network Topology</h1>
        <label className="flex cursor-pointer items-center gap-2 text-xs text-[var(--text-secondary)]">
          <input type="checkbox" checked={includeStopped} onChange={e => onIncludeStoppedChange(e.target.checked)} className="accent-[var(--accent)]" />
          Include stopped
        </label>
        <button onClick={onRefresh} disabled={isLoading}
          className="flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--surface)] px-3 py-1.5 text-xs text-[var(--text-secondary)] transition-colors hover:bg-[var(--surface-2)] disabled:opacity-50">
          <RefreshCw size={12} className={isLoading ? 'animate-spin' : ''} /> Refresh
        </button>
      </div>

      {/* Route Table */}
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-medium text-[var(--text)]">Routes <span className="ml-1 text-xs font-normal text-[var(--text-muted)]">({routes.length + passthroughRoutes.length})</span></h2>
        {(onAddRoute || onAddPassthroughRoute) && (
          <button onClick={() => setAddRouteDialog(true)}
            className="flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--surface)] px-3 py-1.5 text-xs text-[var(--text-secondary)] transition-colors hover:bg-[var(--surface-2)]">
            <Plus size={12} /> Add Route
          </button>
        )}
      </div>

      <div className="rounded-xl border border-[var(--border-subtle)]">
        <UnifiedRouteTable
          proxyRoutes={routes}
          passthroughRoutes={passthroughRoutes}
          onDeleteProxyRoute={onDeleteRoute ? d => setDeleteRouteDialog({ open: true, domain: d }) : undefined}
          onToggleProxyRoute={onToggleRoute}
          onDeletePassthroughRoute={onDeletePassthroughRoute ? (p, proto) => setDeletePassthroughDialog({ open: true, externalPort: p, protocol: proto || 'ROUTE_PROTOCOL_TCP' }) : undefined}
          onTogglePassthroughRoute={onTogglePassthroughRoute}
        />
      </div>

      {/* Add Route Dialog */}
      <Modal
        open={addRouteDialog}
        onClose={() => setAddRouteDialog(false)}
        title="Add Route"
        size="md"
        footer={
          <>
            <ModalBtn onClick={() => setAddRouteDialog(false)}>Cancel</ModalBtn>
            <ModalBtn variant="primary" onClick={handleAddRoute} disabled={!newRoute.domain || !newRoute.targetIp || !newRoute.targetPort}>Add Route</ModalBtn>
          </>
        }
      >
        <div className="flex flex-col gap-3">
          <p className="text-xs text-[var(--text-muted)]">Map a domain to a container. For HTTP/gRPC, TLS is terminated at Caddy. For TLS Passthrough, raw TLS is forwarded via SNI routing (mTLS preserved).</p>
          <FormField label="Domain" hint={baseDomain ? `Base domain: ${baseDomain}` : 'Enter the full domain name'}>
            <>
              <datalist id="domain-suggestions-list">
                {domainSuggestions.map(s => <option key={s.fullDomain} value={s.fullDomain}>{s.subdomain}</option>)}
              </datalist>
              <input list="domain-suggestions-list" type="text" value={newRoute.domain} onChange={e => setNewRoute({ ...newRoute, domain: e.target.value })}
                placeholder={baseDomain ? `subdomain.${baseDomain}` : 'test.example.com'} className={inputCls} />
            </>
          </FormField>
          <FormField label="Protocol">
            <select value={newRoute.protocol} onChange={e => setNewRoute({ ...newRoute, protocol: e.target.value as RouteProtocol })} className={selectCls}>
              <option value="ROUTE_PROTOCOL_HTTP">HTTP (Web traffic)</option>
              <option value="ROUTE_PROTOCOL_GRPC">gRPC (HTTP/2)</option>
              <option value="ROUTE_PROTOCOL_TLS_PASSTHROUGH">TLS Passthrough (mTLS/SNI)</option>
            </select>
          </FormField>
          {newRoute.protocol === 'ROUTE_PROTOCOL_TLS_PASSTHROUGH' && (
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/10 p-3 text-[10px] text-[var(--c-blue)]">
              TLS passthrough routes forward raw TLS traffic on :443 via SNI, preserving end-to-end mTLS. No additional firewall changes needed.
            </div>
          )}
          <FormField label="Target IP" hint="Select a container or enter IP manually">
            <>
              <datalist id="container-ip-list">
                {containerOptions.map(c => <option key={c.ip} value={c.ip}>{c.name}</option>)}
              </datalist>
              <input list="container-ip-list" type="text" value={newRoute.targetIp} onChange={e => setNewRoute({ ...newRoute, targetIp: e.target.value })}
                placeholder="10.0.3.136" className={inputCls} />
            </>
          </FormField>
          <FormField label="Target Port" hint="The port on the container">
            <input type="number" value={newRoute.targetPort} onChange={e => setNewRoute({ ...newRoute, targetPort: e.target.value })}
              placeholder="8080" className={inputCls} />
          </FormField>
        </div>
      </Modal>

      {/* Delete Proxy Route Dialog */}
      <Modal open={deleteRouteDialog.open} onClose={() => setDeleteRouteDialog({ open: false, domain: '' })} title="Delete Proxy Route" size="sm"
        footer={
          <>
            <ModalBtn onClick={() => setDeleteRouteDialog({ open: false, domain: '' })}>Cancel</ModalBtn>
            <ModalBtn variant="danger" onClick={handleConfirmDeleteRoute}>Delete</ModalBtn>
          </>
        }>
        <p className="text-sm text-[var(--text)]">Delete the route for <strong>{deleteRouteDialog.domain}</strong>?</p>
        <p className="mt-1 text-xs text-[var(--text-muted)]">This will remove the proxy configuration for this domain.</p>
      </Modal>

      {/* Delete Passthrough Route Dialog */}
      <Modal open={deletePassthroughDialog.open} onClose={() => setDeletePassthroughDialog({ open: false, externalPort: 0, protocol: 'ROUTE_PROTOCOL_TCP' })} title="Delete Passthrough Route" size="sm"
        footer={
          <>
            <ModalBtn onClick={() => setDeletePassthroughDialog({ open: false, externalPort: 0, protocol: 'ROUTE_PROTOCOL_TCP' })}>Cancel</ModalBtn>
            <ModalBtn variant="danger" onClick={handleConfirmDeletePassthrough}>Delete</ModalBtn>
          </>
        }>
        <p className="text-sm text-[var(--text)]">Delete the passthrough route for port <strong>{deletePassthroughDialog.externalPort}</strong>?</p>
        <p className="mt-1 text-xs text-[var(--text-muted)]">This will remove the TCP/UDP port forwarding rule from iptables.</p>
      </Modal>
    </div>
  );
}
