/**
 * ClamAV scan report
 */
export interface ClamavReport {
  id: number;
  containerName: string;
  username: string;
  status: 'clean' | 'infected';
  findingsCount: number;
  findings: string;
  scannedAt: string;
  scanDuration: string;
  createdAt: string;
  backendId?: string;
}

/**
 * ClamAV container summary
 */
export interface ClamavContainerSummary {
  containerName: string;
  username: string;
  lastScanAt: string;
  lastStatus: 'clean' | 'infected' | 'never';
  lastFindingsCount: number;
  totalScans: number;
  infectedScans: number;
  backendId?: string;
}

/**
 * ClamAV summary response
 */
export interface ClamavSummaryResponse {
  containers: ClamavContainerSummary[];
  totalContainers: number;
  cleanContainers: number;
  infectedContainers: number;
  neverScannedContainers: number;
  lastCollectionAt: string;
}

/**
 * ClamAV reports list response
 */
export interface ClamavReportsResponse {
  reports: ClamavReport[];
  totalCount: number;
}

/**
 * Response from triggering a ClamAV scan
 */
export interface TriggerScanResponse {
  message: string;
  scannedCount: number;
}

/**
 * Parameters for listing ClamAV reports
 */
export interface ListClamavReportsParams {
  containerName?: string;
  status?: string;
  from?: string;
  to?: string;
  limit?: number;
  offset?: number;
}

/**
 * A queued ClamAV scan job
 */
export interface ScanJob {
  id: number;
  containerName: string;
  username: string;
  status: 'pending' | 'running' | 'completed' | 'failed';
  retryCount: number;
  errorMessage: string;
  createdAt: string;
  startedAt: string;
  completedAt: string;
  backendId?: string;
}

/**
 * Response from GET /v1/security/scan-status
 */
export interface ScanStatusResponse {
  jobs: ScanJob[];
  pendingCount: number;
  runningCount: number;
  completedCount: number;
  failedCount: number;
}

// ============= Pentest Types =============

export interface PentestScanRun {
  id: string;
  trigger: string;
  status: 'running' | 'completed' | 'failed';
  modules: string;
  targetsCount: number;
  criticalCount: number;
  highCount: number;
  mediumCount: number;
  lowCount: number;
  infoCount: number;
  errorMessage: string;
  startedAt: string;
  completedAt: string;
  duration: string;
  completedCount: number;
}

export interface PentestFinding {
  id: number;
  fingerprint: string;
  category: string;
  severity: 'critical' | 'high' | 'medium' | 'low' | 'info';
  title: string;
