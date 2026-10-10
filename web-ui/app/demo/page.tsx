'use client';

import { useState } from 'react';
import {
  Server as ServerIcon, LayoutGrid, Network, Activity, BarChart2,
  Shield, ClipboardList, Bell,
} from 'lucide-react';
import AppBar from '@/src/components/layout/AppBar';
import ContainerTopology from '@/src/components/containers/ContainerTopology';
import AppsView from '@/src/components/apps/AppsView';
import NetworkTopologyView from '@/src/components/network/NetworkTopologyView';
import TrafficView from '@/src/components/traffic/TrafficView';
import MonitoringView from '@/src/components/monitoring/MonitoringView';
import SecurityView from '@/src/components/security/SecurityView';
import AuditView from '@/src/components/audit/AuditView';
import AlertsView from '@/src/components/alerts/AlertsView';
import { Container, ContainerMetricsWithRate, SystemInfo, BackendInfo } from '@/src/types/container';
import { App, NetworkTopology, ProxyRoute, PassthroughRoute, DNSRecord } from '@/src/types/app';
import { DEMO_SERVER } from '@/src/lib/demo/DemoClient';

// ── Mock Containers ────────────────────────────────────────────────────────────

const MOCK_CONTAINERS: Container[] = [
  {
    name: 'alice-container', username: 'alice', state: 'Running',
    ipAddress: '10.0.100.10', cpu: '4', memory: '8GB', disk: '100GB', gpu: '',
    image: 'ubuntu/24.04', podmanEnabled: false, stack: 'python',
    createdAt: new Date(Date.now() - 30 * 86400000).toISOString(),
    updatedAt: new Date(Date.now() - 3600000).toISOString(),
    labels: { env: 'production', project: 'ml-research' }, sshKeys: ['ssh-rsa AAAA...alice'],
    backendId: 'local',
  },
  {
    name: 'bob-container', username: 'bob', state: 'Running',
    ipAddress: '10.0.100.11', cpu: '2', memory: '4GB', disk: '50GB', gpu: '',
    image: 'ubuntu/24.04', podmanEnabled: true, stack: 'nodejs',
    createdAt: new Date(Date.now() - 14 * 86400000).toISOString(),
    updatedAt: new Date(Date.now() - 7200000).toISOString(),
    labels: { env: 'staging' }, sshKeys: ['ssh-rsa AAAA...bob'],
    backendId: 'local',
  },
  {
    name: 'charlie-container', username: 'charlie', state: 'Stopped',
    ipAddress: '10.0.100.12', cpu: '1', memory: '2GB', disk: '30GB', gpu: '',
    image: 'ubuntu/22.04', podmanEnabled: false, stack: '',
    createdAt: new Date(Date.now() - 60 * 86400000).toISOString(),
    updatedAt: new Date(Date.now() - 86400000).toISOString(),
    labels: {}, sshKeys: [],
    backendId: 'local',
  },
  {
    name: 'dave-container', username: 'dave', state: 'Running',
    ipAddress: '10.0.100.13', cpu: '8', memory: '32GB', disk: '200GB',
    gpu: 'GPU_MODEL_NVIDIA_RTX_4090',
    image: 'ubuntu/24.04', podmanEnabled: false, stack: 'python',
    createdAt: new Date(Date.now() - 7 * 86400000).toISOString(),
    updatedAt: new Date(Date.now() - 1800000).toISOString(),
    labels: { env: 'production', project: 'diffusion-model' }, sshKeys: ['ssh-rsa AAAA...dave'],
    backendId: 'tunnel-fts-5900x-gpu',
  },
  {
    name: 'eve-container', username: 'eve', state: 'Running',
    ipAddress: '10.0.100.14', cpu: '2', memory: '4GB', disk: '50GB', gpu: '',
    image: 'ubuntu/24.04', podmanEnabled: false, stack: 'fullstack',
    createdAt: new Date(Date.now() - 3 * 86400000).toISOString(),
    updatedAt: new Date().toISOString(),
    labels: { env: 'development' }, sshKeys: ['ssh-rsa AAAA...eve'],
    backendId: 'local',
  },
];

const MOCK_METRICS: Record<string, ContainerMetricsWithRate> = {
  'alice-container': {
    name: 'alice-container',
    cpuUsageSeconds: 1200, cpuUsagePercent: 62.4,
    memoryUsageBytes: 5.5 * 1024 * 1024 * 1024,
    memoryPeakBytes: 6 * 1024 * 1024 * 1024,
    diskUsageBytes: 38 * 1024 * 1024 * 1024,
    networkRxBytes: 1200 * 1024 * 1024, networkTxBytes: 450 * 1024 * 1024,
    processCount: 24,
  },
  'bob-container': {
    name: 'bob-container',
    cpuUsageSeconds: 180, cpuUsagePercent: 12.1,
    memoryUsageBytes: 1.8 * 1024 * 1024 * 1024,
    memoryPeakBytes: 2.1 * 1024 * 1024 * 1024,
    diskUsageBytes: 22 * 1024 * 1024 * 1024,
    networkRxBytes: 320 * 1024 * 1024, networkTxBytes: 80 * 1024 * 1024,
    processCount: 8,
  },
  'dave-container': {
    name: 'dave-container',
    cpuUsageSeconds: 3600, cpuUsagePercent: 87.5,
    memoryUsageBytes: 28 * 1024 * 1024 * 1024,
    memoryPeakBytes: 30 * 1024 * 1024 * 1024,
    diskUsageBytes: 140 * 1024 * 1024 * 1024,
    networkRxBytes: 4500 * 1024 * 1024, networkTxBytes: 1200 * 1024 * 1024,
    processCount: 12,
  },
  'eve-container': {
    name: 'eve-container',
    cpuUsageSeconds: 60, cpuUsagePercent: 5.0,
    memoryUsageBytes: 512 * 1024 * 1024,
    memoryPeakBytes: 700 * 1024 * 1024,
    diskUsageBytes: 10 * 1024 * 1024 * 1024,
    networkRxBytes: 50 * 1024 * 1024, networkTxBytes: 20 * 1024 * 1024,
    processCount: 4,
  },
};

const MOCK_SYSTEM_INFO: SystemInfo = {
  version: '0.16.1',
  incusVersion: '6.22',
  hostname: 'containarium-demo',
  os: 'Ubuntu 24.04 LTS',
  kernel: '6.8.0-49-generic',
  containerCount: 5,
  runningCount: 4,
  networkCidr: '10.0.100.0/24',
  totalCpus: 16,
  totalMemoryBytes: 64 * 1024 * 1024 * 1024,
  availableMemoryBytes: 22 * 1024 * 1024 * 1024,
  totalDiskBytes: 2 * 1024 * 1024 * 1024 * 1024,
  availableDiskBytes: 1.4 * 1024 * 1024 * 1024 * 1024,
};

const MOCK_BACKENDS: BackendInfo[] = [
  {
    id: 'local', type: 'local', healthy: true, priority: 0,
    version: '0.16.1', hostname: 'containarium-demo', uptimeSeconds: 864000,
    lastSeenAt: new Date().toISOString(), os: 'Ubuntu 24.04 LTS', containerCount: 4, gpus: [],
  },
  {
    id: 'tunnel-fts-5900x-gpu', type: 'tunnel', healthy: true, priority: 1,
    version: '0.16.1', hostname: 'fts-5900x', uptimeSeconds: 604800,
    lastSeenAt: new Date().toISOString(), os: 'Ubuntu 24.04 LTS', containerCount: 1,
    gpus: [{ vendor: 'NVIDIA', modelName: 'RTX 4090', vramBytes: 24 * 1024 * 1024 * 1024 }],
  },
];

// ── Mock Apps ──────────────────────────────────────────────────────────────────

const MOCK_APPS: App[] = [
  {
    id: 'app-alice-1', name: 'ml-api', username: 'alice', containerName: 'alice-container',
    subdomain: 'alice', fullDomain: 'alice.containarium.app', port: 8000,
    state: 'APP_STATE_RUNNING', dockerImage: 'python:3.11-slim',
    envVars: { MODEL_PATH: '/models/llm', PORT: '8000' },
    createdAt: new Date(Date.now() - 25 * 86400000).toISOString(),
    updatedAt: new Date(Date.now() - 3600000).toISOString(),
    deployedAt: new Date(Date.now() - 25 * 86400000).toISOString(),
    restartCount: 2, containerIp: '10.0.100.10',
    aclPreset: 'ACL_PRESET_HTTP_ONLY',
    resources: { cpu: '2', memory: '4GB', disk: '10GB' },
  },
  {
    id: 'app-bob-1', name: 'web-frontend', username: 'bob', containerName: 'bob-container',
    subdomain: 'bob', fullDomain: 'bob.containarium.app', port: 3000,
    state: 'APP_STATE_RUNNING', dockerImage: 'node:20-alpine',
    envVars: { NODE_ENV: 'production', PORT: '3000' },
    createdAt: new Date(Date.now() - 10 * 86400000).toISOString(),
    updatedAt: new Date(Date.now() - 7200000).toISOString(),
    deployedAt: new Date(Date.now() - 10 * 86400000).toISOString(),
    restartCount: 0, containerIp: '10.0.100.11',
    aclPreset: 'ACL_PRESET_HTTP_ONLY',
    resources: { cpu: '1', memory: '2GB', disk: '5GB' },
  },
  {
    id: 'app-charlie-1', name: 'data-processor', username: 'charlie', containerName: 'charlie-container',
    subdomain: 'charlie', fullDomain: 'charlie.containarium.app', port: 5000,
    state: 'APP_STATE_STOPPED', dockerImage: 'python:3.10',
    envVars: { WORKERS: '4' },
    createdAt: new Date(Date.now() - 45 * 86400000).toISOString(),
    updatedAt: new Date(Date.now() - 86400000).toISOString(),
    restartCount: 5, containerIp: '10.0.100.12',
    aclPreset: 'ACL_PRESET_FULL_ISOLATION',
    resources: { cpu: '1', memory: '2GB', disk: '5GB' },
  },
];

// ── Mock Network ───────────────────────────────────────────────────────────────

const MOCK_ROUTES: ProxyRoute[] = [
  {
    subdomain: 'alice', fullDomain: 'alice.containarium.app',
    containerIp: '10.0.100.10', port: 8000, active: true,
    appId: 'app-alice-1', appName: 'ml-api', username: 'alice',
    protocol: 'ROUTE_PROTOCOL_HTTP',
  },
  {
    subdomain: 'alice-grpc', fullDomain: 'alice-grpc.containarium.app',
    containerIp: '10.0.100.10', port: 50051, active: true,
    username: 'alice', protocol: 'ROUTE_PROTOCOL_GRPC',
  },
  {
    subdomain: 'bob', fullDomain: 'bob.containarium.app',
    containerIp: '10.0.100.11', port: 3000, active: true,
    appId: 'app-bob-1', appName: 'web-frontend', username: 'bob',
    protocol: 'ROUTE_PROTOCOL_HTTP',
  },
  {
    subdomain: 'charlie', fullDomain: 'charlie.containarium.app',
    containerIp: '10.0.100.12', port: 5000, active: false,
    appId: 'app-charlie-1', appName: 'data-processor', username: 'charlie',
    protocol: 'ROUTE_PROTOCOL_HTTP',
  },
];

const MOCK_PASSTHROUGH: PassthroughRoute[] = [
  {
    externalPort: 2222, targetIp: '10.0.100.10', targetPort: 22,
    protocol: 'ROUTE_PROTOCOL_TCP', active: true,
    containerName: 'alice-container', description: 'Alice SSH',
  },
  {
    externalPort: 8883, targetIp: '10.0.100.11', targetPort: 8883,
    protocol: 'ROUTE_PROTOCOL_TCP', active: true,
    containerName: 'bob-container', description: 'MQTT Broker',
  },
  {
    externalPort: 5353, targetIp: '10.0.100.13', targetPort: 5353,
    protocol: 'ROUTE_PROTOCOL_UDP', active: false,
    containerName: 'dave-container', description: 'mDNS',
  },
];

const MOCK_TOPOLOGY: NetworkTopology = {
  networkCidr: '10.0.100.0/24',
  gatewayIp: '10.0.100.1',
  nodes: [
    { id: 'proxy', type: 'proxy', name: 'nginx-proxy', ipAddress: '10.0.100.1', state: 'Running' },
    { id: 'alice-container', type: 'container', name: 'alice-container', ipAddress: '10.0.100.10', state: 'Running', aclName: 'HTTP Only' },
    { id: 'bob-container', type: 'container', name: 'bob-container', ipAddress: '10.0.100.11', state: 'Running', aclName: 'HTTP Only' },
    { id: 'charlie-container', type: 'container', name: 'charlie-container', ipAddress: '10.0.100.12', state: 'Stopped', aclName: 'Full Isolation' },
    { id: 'dave-container', type: 'container', name: 'dave-container', ipAddress: '10.0.100.13', state: 'Running', aclName: 'Permissive' },
  ],
  edges: [
    { source: 'proxy', target: 'alice-container', type: 'route', ports: '8000', protocol: 'HTTP' },
    { source: 'proxy', target: 'bob-container', type: 'route', ports: '3000', protocol: 'HTTP' },
    { source: 'proxy', target: 'charlie-container', type: 'blocked' },
  ],
};

const MOCK_DNS: DNSRecord[] = [
  { type: 'A', name: 'alice.containarium.app', data: '34.100.200.10', ttl: 300 },
  { type: 'A', name: 'bob.containarium.app', data: '34.100.200.10', ttl: 300 },
  { type: 'CNAME', name: '*.containarium.app', data: 'containarium.app', ttl: 300 },
];

// ─────────────────────────────────────────────────────────────────────────────

const TABS = [
  { label: 'Containers', icon: ServerIcon },
  { label: 'Apps',       icon: LayoutGrid },
  { label: 'Network',    icon: Network },
  { label: 'Traffic',    icon: Activity },
  { label: 'Monitoring', icon: BarChart2 },
  { label: 'Security',   icon: Shield },
  { label: 'Audit',      icon: ClipboardList },
  { label: 'Alerts',     icon: Bell },
] as const;

export default function DemoPage() {
  const [viewTab, setViewTab] = useState(0);

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-[var(--background)]">
      <AppBar onAddServer={() => {}} />

      {/* Demo banner */}
      <div className="flex shrink-0 items-center gap-2 border-b border-amber-500/30 bg-amber-500/10 px-4 py-1.5">
        <span className="text-xs font-medium text-[var(--c-amber)]">Demo Mode</span>
        <span className="text-xs text-[var(--text-muted)]">— all data is synthetic; actions are disabled</span>
      </div>

      {/* Tab bar */}
      <div className="flex items-end gap-0 overflow-x-auto border-b border-[var(--border-subtle)] bg-[var(--surface)] px-4 shrink-0">
        {TABS.map(({ label, icon: Icon }, i) => (
          <button
            key={label}
            onClick={() => setViewTab(i)}
            className={[
              'flex items-center gap-1.5 px-3 py-2.5 text-xs font-medium transition-colors border-b-2 -mb-px',
              viewTab === i
                ? 'border-[var(--accent)] text-[var(--accent)]'
                : 'border-transparent text-[var(--text-secondary)] hover:text-[var(--text)]',
            ].join(' ')}
          >
            <Icon size={13} />
            {label}
          </button>
        ))}
      </div>

      {/* Tab content */}
      <div className="flex-1 overflow-auto">
        {viewTab === 0 && (
          <ContainerTopology
            containers={MOCK_CONTAINERS}
            metricsMap={MOCK_METRICS}
            systemInfo={MOCK_SYSTEM_INFO}
            backends={MOCK_BACKENDS}
            isLoading={false}
            error={null}
            onCreateContainer={() => {}}
            onDeleteContainer={() => {}}
            onStartContainer={() => {}}
            onStopContainer={() => {}}
            onTerminalContainer={() => {}}
            onEditFirewall={() => {}}
            onEditLabels={() => {}}
            onResize={() => {}}
            onManageCollaborators={() => {}}
            onRefresh={() => {}}
            onSelectBackend={async () => MOCK_SYSTEM_INFO}
          />
        )}
        {viewTab === 1 && (
          <AppsView
            apps={MOCK_APPS}
            isLoading={false}
            error={null}
            onStopApp={async () => {}}
            onStartApp={async () => {}}
            onRestartApp={async () => {}}
            onDeleteApp={async () => {}}
            onRefresh={() => {}}
          />
        )}
        {viewTab === 2 && (
          <NetworkTopologyView
            topology={MOCK_TOPOLOGY}
            routes={MOCK_ROUTES}
            passthroughRoutes={MOCK_PASSTHROUGH}
            dnsRecords={MOCK_DNS}
            baseDomain="containarium.app"
            isLoading={false}
            error={null}
            includeStopped={false}
            onIncludeStoppedChange={() => {}}
            onAddRoute={async () => {}}
            onDeleteRoute={async () => {}}
            onToggleRoute={async () => {}}
            onAddPassthroughRoute={async () => {}}
            onDeletePassthroughRoute={async () => {}}
            onTogglePassthroughRoute={async () => {}}
            onRefresh={() => {}}
          />
        )}
        {viewTab === 3 && (
          <TrafficView
            server={DEMO_SERVER}
            containers={MOCK_CONTAINERS}
            proxyRoutes={MOCK_ROUTES}
            passthroughRoutes={MOCK_PASSTHROUGH}
          />
        )}
        {viewTab === 4 && <MonitoringView server={DEMO_SERVER} />}
        {viewTab === 5 && <SecurityView server={DEMO_SERVER} />}
        {viewTab === 6 && <AuditView server={DEMO_SERVER} />}
        {viewTab === 7 && <AlertsView server={DEMO_SERVER} />}
      </div>
    </div>
  );
}
