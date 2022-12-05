'use client';

import { useState, useMemo } from 'react';
import { Plus, RefreshCw, LayoutGrid, List, Search, Server as ServerIcon, Loader2 } from 'lucide-react';
import { Container, ContainerMetricsWithRate, SystemInfo, BackendInfo } from '@/src/types/container';
import ContainerNode from './ContainerNode';
import ContainerListView from './ContainerListView';
import CoreServicesSection from './CoreServicesSection';
import SystemResourcesCard from '../system/SystemResourcesCard';
import { CoreService } from '@/src/lib/api/client';
import { SecurityBadge } from '@/src/lib/hooks/useSecurity';

type ViewMode = 'grid' | 'list';

interface ContainerTopologyProps {
  containers: Container[];
  coreServices?: CoreService[];
  metricsMap: Record<string, ContainerMetricsWithRate>;
  systemInfo?: SystemInfo | null;
  isLoading: boolean;
  error?: Error | null;
  onCreateContainer: () => void;
  onDeleteContainer: (username: string) => void;
  onStartContainer: (username: string) => void;
  onStopContainer: (username: string) => void;
  onTerminalContainer?: (username: string) => void;
  onEditFirewall?: (username: string) => void;
  onEditLabels?: (username: string, labels: Record<string, string>) => void;
  onResize?: (username: string, currentResources: { cpu: string; memory: string; disk: string }) => void;
  onManageCollaborators?: (username: string) => void;
  onToggleAutoSleep?: (username: string, current: { enabled: boolean; threshold: number }) => void;
  onRefresh: () => void;
  backends?: BackendInfo[];
  onSelectBackend?: (backendId: string) => Promise<SystemInfo | null>;
  securityBadgesMap?: Record<string, SecurityBadge | null>;
  onSecurityClick?: (containerName: string) => void;
}

export default function ContainerTopology({
  containers, coreServices, metricsMap, systemInfo, isLoading, error,
  onCreateContainer, onDeleteContainer, onStartContainer, onStopContainer,
  onTerminalContainer, onEditFirewall, onEditLabels, onResize,
  onManageCollaborators, onToggleAutoSleep, onRefresh, backends, onSelectBackend,
  securityBadgesMap, onSecurityClick,
}: ContainerTopologyProps) {
  const [viewMode, setViewMode] = useState<ViewMode>('grid');
  const [groupByLabel, setGroupByLabel] = useState('');
  const [nodeFilter, setNodeFilter] = useState('');
  const [searchQuery, setSearchQuery] = useState('');

  const availableNodes = useMemo(() => {
    const s = new Set<string>();
    containers.forEach(c => { if (c.backendId) s.add(c.backendId); });
    return Array.from(s).sort();
  }, [containers]);

  const filteredContainers = useMemo(() => {
    let r = containers;
    if (nodeFilter) r = r.filter(c => c.backendId === nodeFilter);
    if (searchQuery) {
      const q = searchQuery.toLowerCase();
      r = r.filter(c => c.name.toLowerCase().includes(q) || c.username.toLowerCase().includes(q) || (c.ipAddress && c.ipAddress.toLowerCase().includes(q)));
    }
    return r;
  }, [containers, nodeFilter, searchQuery]);

  const availableLabelKeys = useMemo(() => {
    const keys = new Set<string>();
    filteredContainers.forEach(c => { if (c.labels) Object.keys(c.labels).forEach(k => keys.add(k)); });
    return Array.from(keys).sort();
  }, [filteredContainers]);

  const groupedContainers = useMemo(() => {
    if (!groupByLabel) return { '': filteredContainers };
    const groups: Record<string, Container[]> = {};
    filteredContainers.forEach(c => {
      const v = c.labels?.[groupByLabel] || '(no label)';
      (groups[v] ||= []).push(c);
    });
    return Object.fromEntries(Object.keys(groups).sort().map(k => [k, groups[k]]));
  }, [filteredContainers, groupByLabel]);

  if (isLoading && containers.length === 0) {
    return (
      <div className="flex min-h-[300px] items-center justify-center">
        <Loader2 size={24} className="animate-spin text-[var(--text-secondary)]" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex flex-col items-center justify-center gap-2 py-16">
        <p className="text-sm font-medium text-[var(--c-red)]">Failed to load containers</p>
        <p className="text-xs text-[var(--text-muted)]">{error.message}</p>
        <button onClick={onRefresh} className="mt-2 rounded-md border border-[var(--border)] px-3 py-1.5 text-xs text-[var(--text-secondary)] hover:bg-[var(--surface-2)] transition-colors">
          Retry
        </button>
      </div>
    );
  }

  return (
    <div className="p-6">
      {/* Toolbar */}
