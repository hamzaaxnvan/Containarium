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
