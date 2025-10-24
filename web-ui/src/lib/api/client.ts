import axios, { AxiosInstance, AxiosError } from 'axios';
import { Container, ContainerMetrics, CreateContainerRequest, CreateContainerResponse, ListContainersResponse, MetricsResponse, SystemInfo, Collaborator, AddCollaboratorRequest, BackendInfo, Stack } from '@/src/types/container';
import { Server } from '@/src/types/server';
import { App, NetworkACL, ProxyRoute, NetworkTopology, ACLPresetInfo, DNSRecord, PassthroughRoute, WorkspaceAccess } from '@/src/types/app';
import { Connection, ConnectionSummary, HistoricalConnection, TrafficAggregate, GetConnectionsResponse, GetConnectionSummaryResponse, QueryTrafficHistoryResponse, GetTrafficAggregatesResponse } from '@/src/types/traffic';
import { ClamavSummaryResponse, ClamavReportsResponse, ListClamavReportsParams, TriggerScanResponse, ScanStatusResponse, PentestScanRunsResponse, PentestFindingsResponse, PentestFindingSummaryResponse, PentestConfigResponse, TriggerPentestScanResponse, ListPentestFindingsParams, InstallPentestToolResponse, RemediatePentestFindingResponse, ZapScanRunsResponse, ZapAlertsResponse, ZapAlertSummaryResponse, ZapConfigResponse, TriggerZapScanResponse, ZapReportResponse, InstallZapResponse, ListZapAlertsParams } from '@/src/types/security';
import { AuditLogsResponse, AuditLogsParams } from '@/src/types/audit';
import { AlertRule, AlertRulesResponse, AlertingInfoResponse, CreateAlertRuleRequest, UpdateAlertRuleRequest, UpdateAlertingConfigResponse, TestWebhookResponse, WebhookDeliveriesResponse } from '@/src/types/alerts';
import { ListAgentEnginesResponse, ListAgentEnginesResponseSchema } from '@/src/types/agents';

/**
 * Core infrastructure service info (read-only)
 */
export interface CoreService {
  name: string;
  role: string;
  state: string;
  ipAddress: string;
}

/**
 * API error response
 */
export interface APIError {
  error: string;
  code: number;
}

/**
 * Normalize endpoint URL to ensure it has a protocol
 */
function normalizeEndpoint(endpoint: string): string {
  let url = endpoint.trim();
  // Add http:// if no protocol is specified
  if (!url.startsWith('http://') && !url.startsWith('https://')) {
    url = 'http://' + url;
  }
  // Remove trailing slash
  if (url.endsWith('/')) {
    url = url.slice(0, -1);
  }
  return url;
}

/**
 * Sanitize token by removing all whitespace (spaces, newlines, etc.)
 */
function sanitizeToken(token: string): string {
  return token.replace(/\s+/g, '');
}

/**
 * Map API container state to frontend state
 */
function mapContainerState(apiState: string): string {
  const stateMap: Record<string, string> = {
    'CONTAINER_STATE_RUNNING': 'Running',
    'CONTAINER_STATE_STOPPED': 'Stopped',
    'CONTAINER_STATE_FROZEN': 'Frozen',
    'CONTAINER_STATE_CREATING': 'Creating',
    'CONTAINER_STATE_PROVISIONING': 'Provisioning',
    'CONTAINER_STATE_ERROR': 'Error',
    'CONTAINER_STATE_UNKNOWN': 'Unknown',
    'CONTAINER_STATE_UNSPECIFIED': 'Unknown',
  };
  return stateMap[apiState] || apiState;
}

/**
 * Transform API container response to frontend Container type
 */
function transformContainer(apiContainer: Record<string, unknown>): Container {
  return {
    name: (apiContainer.name as string) || '',
    username: (apiContainer.username as string) || '',
    state: mapContainerState((apiContainer.state as string) || '') as Container['state'],
    ipAddress: ((apiContainer.network as Record<string, unknown>)?.ipAddress as string) || '',
    cpu: ((apiContainer.resources as Record<string, unknown>)?.cpu as string) || '',
    memory: ((apiContainer.resources as Record<string, unknown>)?.memory as string) || '',
    disk: ((apiContainer.resources as Record<string, unknown>)?.disk as string) || '',
    gpu: (apiContainer.gpuDevice as string) || ((apiContainer.resources as Record<string, unknown>)?.gpu as string) || '',
    image: (apiContainer.image as string) || '',
    podmanEnabled: (apiContainer.podmanEnabled as boolean) || false,
    stack: (apiContainer.stack as string) || '',
    createdAt: (apiContainer.createdAt as string) || '',
    updatedAt: (apiContainer.updatedAt as string) || '',
    labels: (apiContainer.labels as Record<string, string>) || {},
    sshKeys: (apiContainer.sshKeys as string[]) || [],
    backendId: (apiContainer.backendId as string) || '',
    osType: (apiContainer.osType as string) || '',
    accessType: (apiContainer.accessType as Container['accessType']) || undefined,
    rdpAddress: (apiContainer.rdpAddress as string) || '',
    autoSleepEnabled: Boolean(apiContainer.autoSleepEnabled) || false,
    idleThresholdMinutes: Number(apiContainer.idleThresholdMinutes) || 0,
  };
}

/**
 * Transform an API collaborator response into the frontend Collaborator
 * type. sshPublicKeys carries every authorized key (#1144); sshPublicKey is
 * kept only for back-compat display of the first key.
 */
function transformCollaborator(c: Record<string, unknown>): Collaborator {
  const rawKeys = (c.sshPublicKeys ?? c.ssh_public_keys) as unknown;
  const scalarKey = String(c.sshPublicKey || c.ssh_public_key || '');
  const arrayKeys = Array.isArray(rawKeys) ? rawKeys.map(String) : [];
  // Backfill in both directions (caught in review of #1914): a response
  // shape carrying only one of the two fields — an older server, or a
  // caller that never populated both — must not silently drop the key
  // from whichever field downstream code reads. sshPublicKeys is the
  // source of truth going forward (#1144); sshPublicKey is kept only
  // for back-compat display of the first key.
  const sshPublicKeys = arrayKeys.length > 0 ? arrayKeys : scalarKey ? [scalarKey] : [];
  const sshPublicKey = scalarKey || sshPublicKeys[0] || '';
  return {
    id: String(c.id || ''),
    containerName: String(c.containerName || c.container_name || ''),
    ownerUsername: String(c.ownerUsername || c.owner_username || ''),
    collaboratorUsername: String(c.collaboratorUsername || c.collaborator_username || ''),
    accountName: String(c.accountName || c.account_name || ''),
    sshPublicKey,
    sshPublicKeys,
    addedAt: Number(c.addedAt || c.added_at) || 0,
    createdBy: String(c.createdBy || c.created_by || ''),
    hasSudo: Boolean(c.hasSudo || c.has_sudo || false),
    hasContainerRuntime: Boolean(c.hasContainerRuntime || c.has_container_runtime || false),
  };
}

/**
 * Create an API client for a specific server
 */
export function createAPIClient(server: Server): AxiosInstance {
  const baseURL = normalizeEndpoint(server.endpoint);
  const token = sanitizeToken(server.token);
  const client = axios.create({
    baseURL,
    headers: {
      'Authorization': 'Bearer ' + token,
      'Content-Type': 'application/json',
    },
    timeout: 30000,
  });

  // Response interceptor for error handling
  client.interceptors.response.use(
    response => response,
    (error: AxiosError<APIError>) => {
      if (error.response?.status === 401) {
        console.error('Authentication failed for server:', server.name);
      }
      return Promise.reject(error);
    }
  );

  return client;
}

/**
 * Containarium API client
 */
export class ContaineriumClient {
  private client: AxiosInstance;
  private server: Server;

  constructor(server: Server) {
    this.server = server;
    this.client = createAPIClient(server);
  }

  /**
   * Test connection to the server
   */
  async testConnection(): Promise<boolean> {
    try {
      await this.getSystemInfo();
      return true;
    } catch {
      return false;
    }
  }

  /**
   * Get system information
   */
  async getSystemInfo(backendId?: string): Promise<SystemInfo> {
    if (backendId) {
      const response = await this.client.get(`/backends/${backendId}/system-info`);
      return response.data.info || response.data;
    }
    const response = await this.client.get('/system/info');
    return response.data.info || response.data;
  }

  /**
   * Get system information including peer backends
   */
  async getSystemInfoWithPeers(): Promise<{ info: SystemInfo; peers: SystemInfo[] }> {
    const response = await this.client.get('/system/info');
    return {
      info: response.data.info || response.data,
      peers: response.data.peers || [],
    };
  }

  /**
   * List all containers
   */
  async listContainers(): Promise<Container[]> {
    const response = await this.client.get('/containers');
    const containers = response.data.containers || [];
    return containers.map((c: Record<string, unknown>) => transformContainer(c));
  }

  /**
   * List available software stacks (with parameter schemas).
   * The Create Container dialog uses this to render dynamic form fields
   * for the selected stack's parameters.
   */
  async listStacks(): Promise<Stack[]> {
    const response = await this.client.get('/stacks');
    return (response.data.stacks || []) as Stack[];
  }

  /**
   * Get a specific container
   */
  async getContainer(username: string): Promise<Container> {
    const response = await this.client.get('/containers/' + username);
    const container = response.data.container || response.data;
    return transformContainer(container);
  }

  /**
   * Create a new container with async support
   * When async=true, returns immediately with CREATING state
   */
  async createContainer(request: CreateContainerRequest, async: boolean = true): Promise<CreateContainerResponse> {
    const response = await this.client.post<CreateContainerResponse>('/containers', {
      username: request.username,
      resources: request.resources,
      ssh_keys: request.sshKeys,
      labels: request.labels,
      image: request.image,
      enable_podman: request.enablePodman ?? true,
      stack: request.stack || '',
      static_ip: request.staticIp || '',
      gpu: request.gpu || '',
      backend_id: request.backendId || '',
      os_type: request.osType || 0,
      async: async,
    }, {
      timeout: async ? 30000 : 300000, // 30s for async, 5min for sync
    });
    return response.data;
  }

  /**
   * List available backends
   */
  async listBackends(): Promise<BackendInfo[]> {
    const response = await this.client.get('/backends');
    return response.data.backends || [];
  }

  /**
   * Configured agent engines and their readiness (#2224).
   */
  async listAgentEngines(): Promise<ListAgentEnginesResponse> {
    const response = await this.client.get('/v1/agent-engines');
    return ListAgentEnginesResponseSchema.parse(response.data);
  }

  /**
   * Latest published release vs the running daemon version (cached daemon-side). #354.
   */
  async getLatestRelease(): Promise<{ latestRelease: string; currentVersion: string; updateAvailable: boolean }> {
    const response = await this.client.get('/releases/latest');
    return {
      latestRelease: response.data.latestRelease || '',
      currentVersion: response.data.currentVersion || '',
      updateAvailable: !!response.data.updateAvailable,
    };
  }

  /**
   * Trigger a daemon upgrade on a backend (empty backendId = the local/primary
   * daemon). Async — a successful local upgrade restarts the daemon. #354.
   */
  async triggerBackendUpgrade(backendId: string, force = false): Promise<{ upgradeId: string; status: string; currentVersion: string; message: string; backendId: string }> {
    const response = await this.client.post('/backends/upgrade', { backend_id: backendId, force });
    return {
      upgradeId: response.data.upgradeId || '',
      status: response.data.status || '',
      currentVersion: response.data.currentVersion || '',
      message: response.data.message || '',
      backendId: response.data.backendId || '',
    };
  }

  /**
   * Poll an upgrade started by triggerBackendUpgrade. Returns status "unknown"
   * when the id isn't found (e.g. a local self-upgrade restarted the daemon). #354.
   */
  async getUpgradeStatus(upgradeId: string): Promise<{ status: string; currentVersion: string; error: string; completedAt: string }> {
    const response = await this.client.get('/upgrades/' + encodeURIComponent(upgradeId));
    return {
      status: response.data.status || '',
      currentVersion: response.data.currentVersion || '',
      error: response.data.error || '',
      completedAt: response.data.completedAt || '',
    };
  }

  /**
   * Poll container until it reaches a final state (Running, Stopped, or Error)
   * Throws an error if the container ends up in Error state
   */
  async waitForContainer(
    username: string,
    onProgress?: (state: string, message: string) => void,
    maxAttempts: number = 60,
    intervalMs: number = 5000
  ): Promise<Container> {
    const finalStates = ['Running', 'Stopped', 'Error'];

    for (let attempt = 0; attempt < maxAttempts; attempt++) {
      try {
        const container = await this.getContainer(username);

        if (onProgress) {
          onProgress(container.state, `Container is ${container.state.toLowerCase()}...`);
        }

        if (finalStates.includes(container.state)) {
          // Throw an error if container ended up in Error state
          if (container.state === 'Error') {
            throw new Error('Container creation failed. Check server logs for details.');
          }
          return container;
        }

        // Wait before next poll
        await new Promise(resolve => setTimeout(resolve, intervalMs));
      } catch (err) {
        // If it's our own error about container failure, re-throw it
        if (err instanceof Error && err.message.includes('Container creation failed')) {
          throw err;
        }
        // Container might not exist yet, keep polling
        if (onProgress) {
          onProgress('Creating', 'Waiting for container to be created...');
        }
        await new Promise(resolve => setTimeout(resolve, intervalMs));
      }
    }

    throw new Error('Timeout waiting for container to be ready');
  }

  /**
   * Delete a container
   */
  async deleteContainer(username: string, force: boolean = false): Promise<void> {
    await this.client.delete('/containers/' + username, {
      params: { force },
    });
  }

  /**
   * Start a container
   */
  async startContainer(username: string): Promise<Container> {
