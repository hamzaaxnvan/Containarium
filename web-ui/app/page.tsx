'use client';

import { useState, useMemo, useEffect, useCallback } from 'react';
import dynamic from 'next/dynamic';
import { usePathname } from 'next/navigation';
import {
  Server as ServerIcon, LayoutGrid, Network, Activity, BarChart2,
  Shield, ClipboardList, Bell, Loader2, RefreshCw, Bot, Cpu
} from 'lucide-react';
import AppBar from '@/src/components/layout/AppBar';
import ServerTabs from '@/src/components/layout/ServerTabs';
import AddServerDialog from '@/src/components/servers/AddServerDialog';
import ContainerTopology from '@/src/components/containers/ContainerTopology';
import CreateContainerDialog from '@/src/components/containers/CreateContainerDialog';
import DeleteConfirmDialog from '@/src/components/containers/DeleteConfirmDialog';
import LabelEditorDialog from '@/src/components/containers/LabelEditorDialog';
import ResizeContainerDialog from '@/src/components/containers/ResizeContainerDialog';
import AutoSleepDialog from '@/src/components/containers/AutoSleepDialog';
import CollaboratorsDialog from '@/src/components/containers/CollaboratorsDialog';
import ContainerSecurityDialog from '@/src/components/containers/ContainerSecurityDialog';
import AppsView from '@/src/components/apps/AppsView';
import NetworkTopologyView from '@/src/components/network/NetworkTopologyView';
import FirewallEditor from '@/src/components/network/FirewallEditor';
import TrafficView from '@/src/components/traffic/TrafficView';
import MonitoringView from '@/src/components/monitoring/MonitoringView';
import SecurityView from '@/src/components/security/SecurityView';
import AuditView from '@/src/components/audit/AuditView';
import AlertsView from '@/src/components/alerts/AlertsView';
import VersionsView from '@/src/components/versions/VersionsView';
import WorkspaceView from '@/src/components/workspace/WorkspaceView';
import AgentEnginesPanel from '@/src/components/agents/AgentEnginesPanel';
import { useServers } from '@/src/lib/hooks/useServers';
import { useContainers, CreateContainerProgress } from '@/src/lib/hooks/useContainers';
import { useMetrics } from '@/src/lib/hooks/useMetrics';
import { useContainerSecurityBadges } from '@/src/lib/hooks/useSecurity';
import { isAdminToken } from '@/src/lib/tokenUtils';
import { useApps } from '@/src/lib/hooks/useApps';
import { useRoutes, usePassthroughRoutes, useNetworkTopology, useContainerACL, useACLPresets, useDNSRecords } from '@/src/lib/hooks/useNetwork';
import { useCollaborators } from '@/src/lib/hooks/useCollaborators';
import { CreateContainerRequest, ContainerMetricsWithRate } from '@/src/types/container';
import { Server } from '@/src/types/server';
import { ACLPreset } from '@/src/types/app';

const TerminalDialog = dynamic(
  () => import('@/src/components/containers/TerminalDialog'),
  { ssr: false }
);

const TAB_PATHS = ['/containers/', '/apps/', '/network/', '/traffic/', '/monitoring/', '/security/', '/audit/', '/alerts/', '/versions/', '/workspace/', '/agents/'] as const;
const TAB_INDICES: Record<string, number> = {
  '/': 0,
  '/containers/': 0,
  '/apps/': 1,
  '/network/': 2,
  '/traffic/': 3,
  '/monitoring/': 4,
  '/security/': 5,
  '/audit/': 6,
  '/alerts/': 7,
  '/versions/': 8,
  '/workspace/': 9,
  '/agents/': 10,
};

const TABS = [
  { label: 'Containers', icon: ServerIcon },
  { label: 'Apps',       icon: LayoutGrid },
  { label: 'Network',    icon: Network },
  { label: 'Traffic',    icon: Activity },
  { label: 'Monitoring', icon: BarChart2 },
  { label: 'Security',   icon: Shield },
  { label: 'Audit',      icon: ClipboardList },
  { label: 'Alerts',     icon: Bell },
  { label: 'Versions',   icon: RefreshCw },
  { label: 'Workspace',  icon: Bot },
  { label: 'Agents',     icon: Cpu },
] as const;

export default function Home() {
  const pathname = usePathname();

  const {
    servers,
    activeServer,
    activeServerId,
    setActiveServerId,
    addServer,
    removeServer,
    updateServer,
    isLoading: serversLoading,
  } = useServers();

  const currentPath = (pathname || '/').replace(/\/?$/, '/');
  const tabFromPath = TAB_INDICES[currentPath] ?? 0;
  const [viewTab, setViewTabState] = useState(tabFromPath);

  useEffect(() => {
    const onPopState = () => {
      const path = window.location.pathname.replace(/^\/webui/, '').replace(/\/?$/, '/');
      setViewTabState(TAB_INDICES[path] ?? 0);
    };
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, []);

  const setViewTab = useCallback((newTab: number) => {
    setViewTabState(newTab);
    const path = '/webui' + (TAB_PATHS[newTab] || '/containers/');
    window.history.pushState(null, '', path);
  }, []);

  const {
    containers,
    coreServices,
    systemInfo,
    backends,
    isLoading: containersLoading,
    error: containersError,
    createContainer,
    deleteContainer,
    startContainer,
    stopContainer,
    resizeContainer,
    cleanupDisk,
    setLabels,
    removeLabel,
    toggleAutoSleep,
    getSystemInfoForBackend,
    refresh: refreshContainers,
  } = useContainers(activeServer);

  const hasRunningContainers = containers.some(c => c.state === 'Running');
  const { metrics } = useMetrics(activeServer, hasRunningContainers);

  const {
    apps,
    isLoading: appsLoading,
    error: appsError,
    stopApp,
    startApp,
    restartApp,
    deleteApp,
    refresh: refreshApps,
  } = useApps(activeServer);

  const [includeStopped, setIncludeStopped] = useState(false);
  const { routes, isLoading: routesLoading, error: routesError, addRoute, deleteRoute, updateRoute, refresh: refreshRoutes } = useRoutes(activeServer);
  const { routes: passthroughRoutes, isLoading: passthroughLoading, addPassthroughRoute, deletePassthroughRoute, updatePassthroughRoute, refresh: refreshPassthrough } = usePassthroughRoutes(activeServer);
  const { topology, isLoading: topologyLoading, error: topologyError, refresh: refreshTopology } = useNetworkTopology(activeServer, includeStopped);
  const { presets, isLoading: presetsLoading } = useACLPresets(activeServer);
  const { records: dnsRecords, baseDomain, refresh: refreshDNS } = useDNSRecords(activeServer);

  const [firewallEditorOpen, setFirewallEditorOpen] = useState(false);
  const [selectedContainer, setSelectedContainer] = useState<string | null>(null);
  const { acl, isLoading: aclLoading, updateACL } = useContainerACL(activeServer, selectedContainer || '');
