const TOKEN_PATTERN = /^mesij_v1\.([A-Za-z0-9_-]{22})\.([A-Za-z0-9_-]{43})$/;

function base64Url(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
}

export function secureRandomBytes(length: number): Uint8Array {
  return crypto.getRandomValues(new Uint8Array(length));
}

export function generateToken(randomBytes: (length: number) => Uint8Array = secureRandomBytes): { token: string; tokenId: string } {
  const tokenId = base64Url(randomBytes(16));
  const secret = base64Url(randomBytes(32));
  return { token: `mesij_v1.${tokenId}.${secret}`, tokenId };
}

export function parseToken(token: string): { tokenId: string } | null {
  const match = TOKEN_PATTERN.exec(token);
  return match?.[1] ? { tokenId: match[1] } : null;
}

export async function hashToken(token: string): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(token)));
}

export function constantTimeEqual(left: Uint8Array, right: Uint8Array): boolean {
  const length = Math.max(left.length, right.length);
  let difference = left.length ^ right.length;
  for (let index = 0; index < length; index++) {
    difference |= (left[index] ?? 0) ^ (right[index] ?? 0);
  }
  return difference === 0;
}

export function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
}
