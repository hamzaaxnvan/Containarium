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
