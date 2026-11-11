// Parse IP ranges like "192.168.1.101-192.168.1.105" or comma/newline separated IPs.
export function parseIPInput(input: string): string[] {
  const lines = input.split(/[\n,;]+/).map((s) => s.trim()).filter(Boolean);
  const result = new Set<string>();

  for (const line of lines) {
    if (line.includes('-')) {
      const parts = line.split('-').map((s) => s.trim());
      if (parts.length === 2 && isIPv4(parts[0]) && isIPv4(parts[1])) {
        const expanded = expandIPRange(parts[0], parts[1]);
        expanded.forEach((ip) => result.add(ip));
        continue;
      }
    }
    if (isIPv4(line)) {
      result.add(line);
    }
  }

  return Array.from(result);
}

function isIPv4(ip: string): boolean {
  const parts = ip.split('.');
  if (parts.length !== 4) return false;
  return parts.every((p) => {
    const n = Number(p);
    return !isNaN(n) && n >= 0 && n <= 255 && String(n) === p;
  });
}

function ipToLong(ip: string): number {
  return ip.split('.').reduce((acc, oct) => (acc << 8) + parseInt(oct, 10), 0) >>> 0;
}

function longToIP(long: number): string {
  return [
    (long >>> 24) & 255,
    (long >>> 16) & 255,
    (long >>> 8) & 255,
    long & 255,
  ].join('.');
}

function expandIPRange(startIP: string, endIP: string): string[] {
  const start = ipToLong(startIP);
  const end = ipToLong(endIP);
  if (start > end || end - start > 256) {
    return [startIP, endIP];
  }
  const ips: string[] = [];
  for (let current = start; current <= end; current++) {
    ips.push(longToIP(current));
  }
  return ips;
}

