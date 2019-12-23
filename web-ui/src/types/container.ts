/**
 * Container state enum
 */
export type ContainerState = 'Running' | 'Stopped' | 'Frozen' | 'Creating' | 'Provisioning' | 'Error' | 'Unknown';

/**
 * Container information from Containarium API
 */
/**
 * Access type enum — how to connect to an instance
 */
export type AccessType = 'ACCESS_TYPE_SSH' | 'ACCESS_TYPE_RDP';

export interface Container {
  name: string;
  username: string;
  state: ContainerState;
  ipAddress: string;
  cpu: string;
  memory: string;
  disk: string;
  gpu: string;
  image: string;
  podmanEnabled: boolean;
  stack: string;
  createdAt: string;
  updatedAt: string;
  labels: Record<string, string>;
  sshKeys: string[];
  backendId?: string;
  osType?: string;
  accessType?: AccessType;
  rdpAddress?: string;
  // Phase 1 scale-down opt-in (optional — older daemons omit these).
  // Treat undefined as false / 15.
  autoSleepEnabled?: boolean;
  idleThresholdMinutes?: number;
}

/**
 * Request to create a container
 */
export interface CreateContainerRequest {
  username: string;
  resources?: {
    cpu?: string;
    memory?: string;
    disk?: string;
  };
  sshKeys?: string[];
  labels?: Record<string, string>;
  image?: string;
  enablePodman?: boolean;
  stack?: string; // Software stack to install (e.g., "nodejs", "python", "fullstack")
  stackParameters?: Record<string, string>; // Stack parameter values from the UI form
  staticIp?: string; // Static IP address (e.g., "10.100.0.100") - empty for DHCP
  gpu?: string; // GPU device ID for passthrough (e.g., "0" for first GPU)
  backendId?: string; // Target backend for creation (empty = primary)
  osType?: number; // OS type enum (4 = Windows Server 2022)
}

/**
 * Backend instance information
 */
export interface BackendInfo {
  id: string;
  type: 'local' | 'gcp' | 'tunnel';
  healthy: boolean;
  priority: number;
  version?: string;
  hostname?: string;
  uptimeSeconds?: number;
  lastSeenAt?: string;
  os?: string;
  containerCount?: number;
  gpus?: Array<{ vendor?: string; modelName?: string; vramBytes?: number }>;
}

/**
 * Available software stacks for container provisioning
 */
export interface StackParameter {
  name: string;
  label: string;
  description?: string;
  type: 'string' | 'password' | 'number' | 'boolean';
  default?: string;
  required?: boolean;
}

export interface Stack {
  id: string;
  name: string;
  description: string;
  icon: string;
  parameters?: StackParameter[];
}

/**
 * Fallback list used when the API /v1/stacks call fails (older daemons).
 * The real catalog (including parameters) comes from listStacks().
 */
export const AVAILABLE_STACKS: Stack[] = [
  { id: '', name: 'None', description: 'No pre-configured stack', icon: 'none' },
  { id: 'nodejs', name: 'Node.js Development', description: 'Node.js LTS with npm, yarn, pnpm, TypeScript', icon: 'nodejs' },
  { id: 'python', name: 'Python Development', description: 'Python 3 with pip, virtualenv, poetry', icon: 'python' },
  { id: 'golang', name: 'Go Development', description: 'Go with gopls, golangci-lint', icon: 'golang' },
  { id: 'rust', name: 'Rust Development', description: 'Rust with cargo', icon: 'rust' },
  { id: 'datascience', name: 'Data Science', description: 'Python with Jupyter, pandas, numpy, scikit-learn', icon: 'jupyter' },
  { id: 'docker', name: 'Docker Development', description: 'Docker CE container runtime', icon: 'docker' },
  { id: 'devops', name: 'DevOps Tools', description: 'kubectl, Terraform, infrastructure tools', icon: 'kubernetes' },
  { id: 'database', name: 'Database Clients', description: 'PostgreSQL, MySQL, Redis CLI clients', icon: 'database' },
  { id: 'fullstack', name: 'Full Stack Web', description: 'Node.js, Python, database clients', icon: 'code' },
  { id: 'gpu', name: 'GPU / CUDA Development', description: 'NVIDIA drivers, CUDA toolkit, cuDNN', icon: 'gpu' },
  { id: 'gpu-docker', name: 'GPU + Docker (CUDA)', description: 'NVIDIA CUDA, Docker CE, nvidia-container-toolkit', icon: 'gpu' },
];

/**
 * Response from creating a container
 */
export interface CreateContainerResponse {
  container: Container;
  message: string;
  sshCommand: string;
  rdpAddress?: string;
}

/**
 * Response from listing containers
 */
export interface ListContainersResponse {
  containers: Container[];
  totalCount: number;
}

/**
 * System info from Containarium API
 */
export interface SystemInfo {
  version: string;
  incusVersion: string;
  hostname: string;
  os: string;
  kernel: string;
  containerCount: number;
  runningCount: number;
