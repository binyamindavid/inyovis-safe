export function canResolveDuplicate(role: string | undefined): boolean;
export function resolveAndRefresh<T extends { data?: unknown; response?: { status?: number } }>(send: () => Promise<T>, refresh: () => Promise<unknown>): Promise<T>;
