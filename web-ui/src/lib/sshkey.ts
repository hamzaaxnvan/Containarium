import nacl from 'tweetnacl';
import { encodeBase64 } from 'tweetnacl-util';

/**
 * SSH Key Pair generated in the browser
 */
export interface SSHKeyPair {
  publicKey: string;   // OpenSSH format (ssh-ed25519 AAAA... user@host)
  privateKey: string;  // PEM format for download
  fingerprint: string; // SHA256 fingerprint
}

/**
 * Convert Uint8Array to base64 string
 */
function toBase64(data: Uint8Array): string {
  return encodeBase64(data);
}

/**
 * Create SHA-256 hash (using SubtleCrypto)
 */
async function sha256(data: Uint8Array): Promise<Uint8Array> {
  const hashBuffer = await crypto.subtle.digest('SHA-256', data.buffer as ArrayBuffer);
  return new Uint8Array(hashBuffer);
}

/**
 * Encode a length as 4-byte big-endian
 */
function encodeLength(len: number): Uint8Array {
  const buffer = new Uint8Array(4);
  buffer[0] = (len >> 24) & 0xff;
  buffer[1] = (len >> 16) & 0xff;
  buffer[2] = (len >> 8) & 0xff;
  buffer[3] = len & 0xff;
  return buffer;
}

/**
 * Encode a string as length-prefixed bytes
 */
function encodeString(str: string): Uint8Array {
  const strBytes = new TextEncoder().encode(str);
  const lenBytes = encodeLength(strBytes.length);
  const result = new Uint8Array(lenBytes.length + strBytes.length);
  result.set(lenBytes);
  result.set(strBytes, lenBytes.length);
  return result;
}

/**
 * Encode bytes as length-prefixed bytes
 */
function encodeBytes(data: Uint8Array): Uint8Array {
  const lenBytes = encodeLength(data.length);
  const result = new Uint8Array(lenBytes.length + data.length);
  result.set(lenBytes);
  result.set(data, lenBytes.length);
  return result;
}

/**
 * Generate OpenSSH format public key from Ed25519 public key bytes
 */
function formatOpenSSHPublicKey(publicKeyBytes: Uint8Array, comment: string = ''): string {
  const keyType = encodeString('ssh-ed25519');
  const keyData = encodeBytes(publicKeyBytes);

  const combined = new Uint8Array(keyType.length + keyData.length);
  combined.set(keyType);
  combined.set(keyData, keyType.length);

  const base64Key = toBase64(combined);
  return 'ssh-ed25519 ' + base64Key + (comment ? ' ' + comment : '');
}

/**
 * Generate OpenSSH format private key (PEM) from Ed25519 key pair
 */
function formatOpenSSHPrivateKey(secretKey: Uint8Array, publicKey: Uint8Array, comment: string = ''): string {
  const AUTH_MAGIC = 'openssh-key-v1\x00';
  const NONE = 'none';
  const ED25519 = 'ssh-ed25519';

  // Generate random check integers
  const checkInt = crypto.getRandomValues(new Uint8Array(4));

  // Build the public key blob
  const pubKeyType = encodeString(ED25519);
  const pubKeyData = encodeBytes(publicKey);
  const pubKeyBlob = new Uint8Array(pubKeyType.length + pubKeyData.length);
  pubKeyBlob.set(pubKeyType);
  pubKeyBlob.set(pubKeyData, pubKeyType.length);

  // Build the private key section
  const commentBytes = encodeString(comment);
  const privateSection: number[] = [];

  // Check integers (same value twice)
  privateSection.push(...checkInt);
  privateSection.push(...checkInt);

  // Key type
  const keyTypeBytes = encodeString(ED25519);
  privateSection.push(...keyTypeBytes);

  // Public key
  const pubKeyEncoded = encodeBytes(publicKey);
  privateSection.push(...pubKeyEncoded);

  // Private key (64 bytes: 32 secret + 32 public, length-prefixed)
  const fullPrivKey = encodeBytes(secretKey);
  privateSection.push(...fullPrivKey);

  // Comment
  privateSection.push(...commentBytes);

  // Padding to block size (8 bytes)
  const blockSize = 8;
  let padLen = blockSize - (privateSection.length % blockSize);
  if (padLen === blockSize) padLen = 0;
  for (let i = 1; i <= padLen; i++) {
    privateSection.push(i);
  }

  const privateSectionBytes = new Uint8Array(privateSection);

  // Build the full key structure
  const parts: Uint8Array[] = [];

  // Auth magic
  const magicBytes = new TextEncoder().encode(AUTH_MAGIC);
  parts.push(magicBytes);

  // Cipher name (none for unencrypted)
  parts.push(encodeString(NONE));

  // KDF name (none for unencrypted)
  parts.push(encodeString(NONE));

  // KDF options (empty for none)
  parts.push(encodeBytes(new Uint8Array(0)));

  // Number of keys
  parts.push(encodeLength(1));

  // Public key blob
  parts.push(encodeBytes(pubKeyBlob));

  // Encrypted private key section
  parts.push(encodeBytes(privateSectionBytes));

  // Combine all parts
  const totalLen = parts.reduce((sum, p) => sum + p.length, 0);
  const combined = new Uint8Array(totalLen);
  let offset = 0;
  for (const part of parts) {
    combined.set(part, offset);
    offset += part.length;
  }

  // Convert to base64 with line wrapping
  const base64Key = toBase64(combined);
  const wrappedBase64 = base64Key.match(/.{1,70}/g)?.join('\n') || base64Key;

  return '-----BEGIN OPENSSH PRIVATE KEY-----\n' + wrappedBase64 + '\n-----END OPENSSH PRIVATE KEY-----\n';
}

/**
 * Generate an ED25519 SSH key pair in the browser
 */
export async function generateSSHKeyPair(comment: string = 'containarium'): Promise<SSHKeyPair> {
  // Generate Ed25519 key pair using tweetnacl
  const keyPair = nacl.sign.keyPair();

  const publicKeyBytes = keyPair.publicKey;
  const secretKeyBytes = keyPair.secretKey;

  // Generate fingerprint (SHA-256 of the public key blob)
  const keyType = encodeString('ssh-ed25519');
  const keyData = encodeBytes(publicKeyBytes);
  const pubKeyBlob = new Uint8Array(keyType.length + keyData.length);
  pubKeyBlob.set(keyType);
  pubKeyBlob.set(keyData, keyType.length);

  const hash = await sha256(pubKeyBlob);
  const fingerprint = 'SHA256:' + toBase64(hash).replace(/=+$/, '');

  // Format keys
  const publicKey = formatOpenSSHPublicKey(publicKeyBytes, comment);
  const privateKey = formatOpenSSHPrivateKey(secretKeyBytes, publicKeyBytes, comment);

  return {
    publicKey,
    privateKey,
    fingerprint,
  };
}

/**
 * Download a private key as a file
 */
export function downloadPrivateKey(privateKey: string, filename: string): void {
  const blob = new Blob([privateKey], { type: 'application/x-pem-file' });
  const url = URL.createObjectURL(blob);

  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  link.style.display = 'none';

  document.body.appendChild(link);
  link.click();

  document.body.removeChild(link);
  URL.revokeObjectURL(url);
}
