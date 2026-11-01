export class PreviewCache<T extends { evidence: { text: string } }> {
  private records = new Map<string, { value: T; bytes: number }>();
  private totalBytes = 0;

  constructor(private readonly maxRecords = 10, private readonly maxTextBytes = 4 * 1024 * 1024) {}

  get(key: string): T | undefined {
    const entry = this.records.get(key);
    if (!entry) return undefined;
    this.records.delete(key);
    this.records.set(key, entry);
    return entry.value;
  }

  set(key: string, value: T): string[] {
    this.delete(key);
    const bytes = Buffer.byteLength(value.evidence.text, 'utf8');
    if (bytes > this.maxTextBytes) return [key];
    this.records.set(key, { value, bytes });
    this.totalBytes += bytes;
    const evicted: string[] = [];
    while (this.records.size > this.maxRecords || this.totalBytes > this.maxTextBytes) {
      const oldest = this.records.keys().next().value as string | undefined;
      if (oldest === undefined) break;
      evicted.push(oldest);
      this.delete(oldest);
    }
    return evicted;
  }

  delete(key: string): boolean {
    const entry = this.records.get(key);
    if (!entry) return false;
    this.records.delete(key);
    this.totalBytes -= entry.bytes;
    return true;
  }

  clear(): void {
    this.records.clear();
    this.totalBytes = 0;
  }

  get size(): number { return this.records.size; }
  get textBytes(): number { return this.totalBytes; }
}
