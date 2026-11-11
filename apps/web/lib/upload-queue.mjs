export const MAX_FILES = 25;
export const MAX_TOTAL_BYTES = 250 * 1024 * 1024;
export const TERMINAL_PROCESSING = new Set(['needs_review', 'ready', 'file_blocked', 'processing_failed']);
export const ACTIVE_STATES = new Set(['validating', 'hashing', 'session_created', 'uploading', 'completing']);
const transitions = {
  queued: ['validating', 'rejected'], validating: ['hashing', 'rejected'],
  hashing: ['validating', 'session_created', 'failed_recoverable', 'failed_terminal'],
  session_created: ['uploading', 'completing', 'failed_recoverable', 'rejected', 'failed_terminal'],
  uploading: ['session_created', 'completing', 'failed_recoverable', 'rejected', 'failed_terminal'],
  completing: ['received', 'session_created', 'uploading', 'failed_recoverable', 'failed_terminal', 'rejected'],
  failed_recoverable: ['validating', 'hashing', 'session_created', 'uploading', 'completing', 'rejected', 'failed_terminal'],
  received: [], rejected: [], failed_terminal: [],
};
export function queueReducer(rows, action) {
  if (action.type === 'add') return [...rows, ...action.rows];
  if (action.type === 'dismiss') return [];
  return rows.map(row => {
    if (row.id !== action.id) return row;
    if (action.type === 'progress') return row.state === 'uploading' ? { ...row, progress: action.progress } : row;
    if (action.type === 'processing') return row.state === 'received' ? { ...row, processing: action.status } : row;
    if (action.type === 'patch') return { ...row, ...action.patch };
    if (action.type === 'transition') {
      if (!transitions[row.state]?.includes(action.state)) throw new Error(`Invalid upload transition: ${row.state} -> ${action.state}`);
      return { ...row, state: action.state, ...action.patch };
    }
    return row;
  });
}
export function validateSelection(files, existing, maxBytes) {
  let count = existing.length;
  let total = existing.reduce((sum, row) => sum + row.file.size, 0);
  return files.map(file => {
    count += 1; total += file.size;
    if (count > MAX_FILES) return 'count';
    if (total > MAX_TOTAL_BYTES) return 'total';
    if (file.size < 1 || file.size > maxBytes || (file.type && file.type !== 'application/pdf') || !file.name.toLowerCase().endsWith('.pdf') || file.name.length > 255 || /[\0/\\]/.test(file.name)) return 'file';
    return null;
  });
}
export function classifyFailure(status, code, step) {
  if ((status === 401 || status === 403) && step !== 'transfer') return 'pause';
  if (status === 429) return 'rate';
  if (code === 'UPLOAD_STAGED_OBJECT_MISSING' && step === 'complete') return 'missing';
  if (status === 422 || status === 409) return step === 'session' ? 'rejected' : 'terminal';
  if ((status === 401 || status === 403) && step === 'transfer') return 'grant';
  if (status >= 400 && status < 500) return 'rejected';
  return 'recoverable';
}
