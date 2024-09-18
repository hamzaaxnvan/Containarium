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
