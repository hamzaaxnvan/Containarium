/**
 * Event types from the backend
 */
export type EventType =
  | 'EVENT_TYPE_UNSPECIFIED'
  | 'EVENT_TYPE_CONTAINER_CREATED'
  | 'EVENT_TYPE_CONTAINER_DELETED'
  | 'EVENT_TYPE_CONTAINER_STARTED'
  | 'EVENT_TYPE_CONTAINER_STOPPED'
  | 'EVENT_TYPE_CONTAINER_STATE_CHANGED'
  | 'EVENT_TYPE_APP_DEPLOYED'
  | 'EVENT_TYPE_APP_DELETED'
  | 'EVENT_TYPE_APP_STARTED'
  | 'EVENT_TYPE_APP_STOPPED'
  | 'EVENT_TYPE_APP_STATE_CHANGED'
  | 'EVENT_TYPE_ROUTE_ADDED'
  | 'EVENT_TYPE_ROUTE_DELETED'
  | 'EVENT_TYPE_METRICS_UPDATE'
  | 'EVENT_TYPE_TRAFFIC_UPDATE';

/**
 * Resource types from the backend
 */
export type ResourceType =
  | 'RESOURCE_TYPE_UNSPECIFIED'
  | 'RESOURCE_TYPE_CONTAINER'
  | 'RESOURCE_TYPE_APP'
  | 'RESOURCE_TYPE_ROUTE'
  | 'RESOURCE_TYPE_METRICS'
  | 'RESOURCE_TYPE_TRAFFIC';

/**
 * Container event payload
 */
export interface ContainerEventPayload {
  container?: {
    name: string;
    username: string;
    state: string;
    ipAddress?: string;
    cpu?: string;
    memory?: string;
    disk?: string;
    image?: string;
    podmanEnabled?: boolean;
    stack?: string;
  };
  previousState?: string;
}

/**
 * App event payload
 */
export interface AppEventPayload {
  app?: {
    id: string;
    name: string;
    username: string;
    containerName: string;
    subdomain: string;
    fullDomain: string;
    port: number;
    state: string;
  };
  previousState?: string;
}

/**
 * Route event payload
 */
export interface RouteEventPayload {
  route?: {
    subdomain: string;
    fullDomain: string;
    containerIp: string;
    port: number;
    active: boolean;
    appId?: string;
    appName?: string;
  };
}

/**
 * Metrics event payload
 */
export interface MetricsEventPayload {
  metrics: Array<{
    name: string;
    cpuUsageSeconds: number;
    memoryUsageBytes: number;
    memoryPeakBytes: number;
    diskUsageBytes: number;
    networkRxBytes: number;
    networkTxBytes: number;
    processCount: number;
  }>;
}

/**
 * Traffic event payload
 */
export interface TrafficEventPayload {
  eventType: 'NEW' | 'UPDATE' | 'DESTROY';
  connection: {
    id: string;
    containerName: string;
    containerIp: string;
    protocol: string;
    sourceIp: string;
    sourcePort: number;
    destIp: string;
    destPort: number;
    state: string;
    direction: string;
