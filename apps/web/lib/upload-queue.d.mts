export type QueueState = 'queued' | 'validating' | 'hashing' | 'session_created' | 'uploading' | 'completing' | 'received' | 'rejected' | 'failed_recoverable' | 'failed_terminal';
export const MAX_FILES: number;
export const MAX_TOTAL_BYTES: number;
export const TERMINAL_PROCESSING: Set<string>;
export const ACTIVE_STATES: Set<string>;
export function queueReducer<T extends { id: string; state: QueueState }>(rows: T[], action: { type: string; id?: string; rows?: T[]; state?: QueueState; patch?: Partial<T>; progress?: number; status?: string }): T[];
export function validateSelection(files: File[], existing: { file: File }[], maxBytes: number): (string | null)[];
export function classifyFailure(status: number, code: string, step: string): 'pause' | 'rate' | 'missing' | 'terminal' | 'grant' | 'rejected' | 'recoverable';
