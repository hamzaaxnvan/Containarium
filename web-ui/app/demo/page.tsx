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
