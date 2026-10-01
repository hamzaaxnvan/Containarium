'use client';

import { useState } from 'react';
import { RefreshCw, Globe, Network, Play, Pause } from 'lucide-react';
import { Server } from '@/src/types/server';
import { Container } from '@/src/types/container';
import { ProxyRoute, PassthroughRoute, getRouteProtocolName, isGRPCRoute } from '@/src/types/app';
import { useTraffic } from '@/src/lib/hooks/useTraffic';
import { formatBytes } from '@/src/types/traffic';
import ConnectionsTable from './ConnectionsTable';

export interface RouteTrafficStats {
  routeId: string;
  requestsPerMin: number;
  bytesPerMin: number;
}

function formatTraffic(value: number): string {
  if (value >= 1000000) return `${(value / 1000000).toFixed(1)}M`;
  if (value >= 1000) return `${(value / 1000).toFixed(1)}K`;
  return value.toString();
}

interface TrafficViewProps {
  server: Server;
  containers: Container[];
  proxyRoutes?: ProxyRoute[];
  passthroughRoutes?: PassthroughRoute[];
  trafficStats?: RouteTrafficStats[];
  onDateRangeChange?: (startDate: string, endDate: string) => void;
}

export default function TrafficView({
  server,
  containers,
  proxyRoutes = [],
  passthroughRoutes = [],
  trafficStats = [],
  onDateRangeChange,
}: TrafficViewProps) {
  const [selectedContainer, setSelectedContainer] = useState('');
  const [activeTab, setActiveTab] = useState(0);
  const [startDate, setStartDate] = useState(() => {
    const d = new Date();
    d.setHours(d.getHours() - 1);
    return d.toISOString().slice(0, 16);
  });
  const [endDate, setEndDate] = useState(() => new Date().toISOString().slice(0, 16));

  const { connections, summary, isLoading, error, autoRefresh, refresh, toggleAutoRefresh, eventStatus } =
    useTraffic(server, selectedContainer || null);

  const runningContainers = containers.filter(c => c.state === 'Running');

  const allRoutesWithTraffic = [
    ...proxyRoutes.map(r => ({ type: 'proxy' as const, route: r, traffic: trafficStats.find(t => t.routeId === r.fullDomain) || null })),
    ...passthroughRoutes.map(r => ({ type: 'passthrough' as const, route: r, traffic: trafficStats.find(t => t.routeId === `${r.externalPort}-${r.protocol}`) || null })),
  ].sort((a, b) => {
    if (a.route.active !== b.route.active) return a.route.active ? -1 : 1;
    return (b.traffic?.requestsPerMin || 0) - (a.traffic?.requestsPerMin || 0);
  });

  const maxTraffic = Math.max(...allRoutesWithTraffic.map(r => r.traffic?.requestsPerMin || 0), 1);
  const totalRequests = allRoutesWithTraffic.reduce((sum, r) => sum + (r.traffic?.requestsPerMin || 0), 0);

  const inputCls = 'rounded-md border border-[var(--border-subtle)] bg-[var(--surface-2)] px-3 py-1.5 text-xs text-[var(--text)] focus:border-[var(--accent)] focus:outline-none';
  const selectCls = 'appearance-none rounded-md border border-[var(--border-subtle)] bg-[var(--surface-2)] px-3 py-1.5 text-xs text-[var(--text)] focus:border-[var(--accent)] focus:outline-none';

  return (
    <div className="p-6">
      <h1 className="mb-4 text-base font-semibold text-[var(--text)]">Traffic Monitor</h1>

      {/* Tab Bar */}
      <div className="mb-6 flex border-b border-[var(--border-subtle)]">
        {['Route Traffic', 'Container Connections'].map((tab, i) => (
          <button key={tab} onClick={() => setActiveTab(i)}
            className={`-mb-px px-4 py-2 text-xs font-medium transition-colors ${activeTab === i ? 'border-b-2 border-[var(--accent)] text-[var(--accent)]' : 'text-[var(--text-muted)] hover:text-[var(--text-secondary)]'}`}>
            {tab}
          </button>
        ))}
      </div>

      {/* Route Traffic Tab */}
      {activeTab === 0 && (
        <div className="flex flex-col gap-4">
          {/* Date Range */}
          <div className="flex flex-wrap items-center gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] p-4">
            <span className="text-xs text-[var(--text-muted)]">Time Range:</span>
            <div className="flex flex-col gap-1">
              <label className="text-[10px] text-[var(--text-muted)]">Start</label>
              <input type="datetime-local" value={startDate} onChange={e => { setStartDate(e.target.value); onDateRangeChange?.(e.target.value, endDate); }} className={inputCls} />
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-[10px] text-[var(--text-muted)]">End</label>
              <input type="datetime-local" value={endDate} onChange={e => { setEndDate(e.target.value); onDateRangeChange?.(startDate, e.target.value); }} className={inputCls} />
            </div>
            <button onClick={() => onDateRangeChange?.(startDate, endDate)} className="ml-auto rounded-md border border-[var(--border-subtle)] bg-[var(--surface)] p-2 text-[var(--text-muted)] transition-colors hover:bg-[var(--surface-2)] hover:text-[var(--text)]">
              <RefreshCw size={13} />
            </button>
          </div>

          {/* Summary */}
          <div className="flex flex-wrap gap-6 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] px-5 py-3">
            {[
              { label: 'Total Routes', value: allRoutesWithTraffic.length },
              { label: 'Active', value: allRoutesWithTraffic.filter(r => r.route.active).length },
              { label: 'Requests/min', value: formatTraffic(totalRequests) },
            ].map(s => (
              <div key={s.label} className="flex flex-col">
                <span className="text-[10px] text-[var(--text-muted)]">{s.label}</span>
                <span className="text-xl font-semibold text-[var(--text)]">{s.value}</span>
              </div>
            ))}
          </div>

          {/* Route List */}
          {allRoutesWithTraffic.length === 0 ? (
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/10 p-3 text-xs text-[var(--c-blue)]">
              No routes configured. Add routes in the Network tab to see traffic data.
            </div>
          ) : (
            <div className="flex flex-col gap-2">
              {allRoutesWithTraffic.map(({ type, route, traffic }) => {
                const isProxy = type === 'proxy';
                const proxyRoute = isProxy ? (route as ProxyRoute) : null;
                const passthroughRoute = !isProxy ? (route as PassthroughRoute) : null;
                const requests = traffic?.requestsPerMin || 0;
                const percentage = maxTraffic > 0 ? (requests / maxTraffic) * 100 : 0;
                const routeKey = isProxy ? proxyRoute?.fullDomain : `${passthroughRoute?.externalPort}-${passthroughRoute?.protocol}`;
                const label = isProxy
                  ? (isGRPCRoute(proxyRoute?.protocol) ? 'gRPC' : 'HTTP')
                  : getRouteProtocolName(passthroughRoute?.protocol);

                return (
                  <div key={routeKey} className={`rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] p-3 ${!route.active ? 'opacity-50' : ''}`}>
                    <div className="mb-1.5 flex items-center gap-2">
                      {isProxy
                        ? <Globe size={13} className="shrink-0 text-[var(--accent)]" />
                        : <Network size={13} className="shrink-0 text-[var(--c-violet)]" />}
                      <span className={`flex-1 truncate font-mono text-xs font-medium text-[var(--text)] ${!route.active ? 'line-through' : ''}`}>
                        {isProxy ? proxyRoute?.fullDomain : `:${passthroughRoute?.externalPort}`}
                      </span>
                      <span className={`rounded-full border px-2 py-0.5 text-[10px] ${isProxy ? 'border-blue-500/30 bg-blue-500/10 text-[var(--c-blue)]' : 'border-violet-500/30 bg-violet-500/10 text-[var(--c-violet)]'}`}>
                        {label}
                      </span>
                      <span className="text-[10px] text-[var(--text-muted)]">
                        {requests > 0 ? `${formatTraffic(requests)} req/min` : 'No traffic'}
                      </span>
                    </div>
                    <div className="h-1.5 w-full overflow-hidden rounded-full bg-[var(--surface-2)]">
                      <div className={`h-1.5 rounded-full transition-all ${route.active ? (isProxy ? 'bg-[var(--accent)]' : 'bg-violet-500') : 'bg-[var(--border)]'}`} style={{ width: `${percentage}%` }} />
                    </div>
                    <div className="mt-1 flex justify-between text-[10px] text-[var(--text-muted)]">
                      <span>
                        → {isProxy ? `${proxyRoute?.containerIp}:${proxyRoute?.port}` : `${passthroughRoute?.targetIp}:${passthroughRoute?.targetPort}`}
                        {(isProxy ? proxyRoute?.appName : passthroughRoute?.containerName) && (
                          <span> ({isProxy ? proxyRoute?.appName : passthroughRoute?.containerName})</span>
                        )}
                      </span>
                      {traffic && traffic.bytesPerMin > 0 && <span>{formatBytes(traffic.bytesPerMin)}/min</span>}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      )}

      {/* Container Connections Tab */}
      {activeTab === 1 && (
        <div className="flex flex-col gap-4">
          {/* Controls */}
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] p-4">
            <select value={selectedContainer} onChange={e => setSelectedContainer(e.target.value)} className={selectCls} style={{ minWidth: 250 }}>
              <option value="">Select a container</option>
              {runningContainers.map(c => <option key={c.name} value={c.name}>{c.name} ({c.ipAddress})</option>)}
            </select>
            <div className="flex items-center gap-2">
              <span className={`rounded-full border px-2 py-0.5 text-[10px] ${eventStatus === 'connected' ? 'border-emerald-500/30 bg-emerald-500/10 text-[var(--c-emerald)]' : 'border-[var(--border-subtle)] bg-[var(--surface-2)] text-[var(--text-muted)]'}`}>
                {eventStatus === 'connected' ? 'Live' : eventStatus}
              </span>
              <button onClick={toggleAutoRefresh} title={autoRefresh ? 'Pause auto-refresh' : 'Enable auto-refresh'} className="rounded p-1.5 text-[var(--text-muted)] transition-colors hover:bg-[var(--surface-2)] hover:text-[var(--text)]">
                {autoRefresh ? <Pause size={13} /> : <Play size={13} />}
              </button>
              <button onClick={refresh} disabled={!selectedContainer} title="Refresh now" className="rounded p-1.5 text-[var(--text-muted)] transition-colors hover:bg-[var(--surface-2)] hover:text-[var(--text)] disabled:opacity-40">
                <RefreshCw size={13} />
              </button>
            </div>
          </div>

          {!selectedContainer && (
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/10 p-3 text-xs text-[var(--c-blue)]">
              Select a running container to view its network connections.
            </div>
          )}

          {error && selectedContainer && (
            <div className="rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-xs text-[var(--c-red)]">
              Failed to load traffic data: {error.message || 'Unknown error'}
            </div>
          )}

          {selectedContainer && (
            <>
              {summary && (
                <div className="flex flex-wrap gap-6 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] px-5 py-3">
                  {[
                    { label: 'Active Connections', value: summary.activeConnections },
                    { label: 'TCP', value: summary.tcpConnections },
                    { label: 'UDP', value: summary.udpConnections },
                    { label: 'Total Sent', value: formatBytes(summary.totalBytesSent) },
                    { label: 'Total Received', value: formatBytes(summary.totalBytesReceived) },
                  ].map(s => (
                    <div key={s.label} className="flex flex-col">
                      <span className="text-[10px] text-[var(--text-muted)]">{s.label}</span>
                      <span className="text-lg font-semibold text-[var(--text)]">{s.value}</span>
                    </div>
                  ))}
                </div>
              )}

              <div className="rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] p-4">
                <p className="mb-3 text-xs font-medium text-[var(--text)]">Active Connections ({connections.length})</p>
                <ConnectionsTable connections={connections} isLoading={isLoading} />
              </div>

              {summary?.topDestinations && summary.topDestinations.length > 0 && (
                <div className="rounded-xl border border-[var(--border-subtle)] bg-[var(--surface)] p-4">
                  <p className="mb-3 text-xs font-medium text-[var(--text)]">Top Destinations</p>
                  <div className="flex flex-wrap gap-1.5">
                    {summary.topDestinations.slice(0, 10).map(dest => (
                      <span key={dest.destIp} className="rounded-full border border-[var(--border-subtle)] bg-[var(--surface-2)] px-2 py-0.5 text-[10px] text-[var(--text-secondary)]">
                        {dest.destIp} ({dest.connectionCount} conn, {formatBytes(dest.bytesTotal)})
                      </span>
                    ))}
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}
