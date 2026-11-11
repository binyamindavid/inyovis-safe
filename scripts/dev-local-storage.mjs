import { existsSync } from 'node:fs';

export function localStorageArgs(env, storageRunning, pathExists = existsSync) {
  if (storageRunning) return null;
  const paths = [env.LOCAL_S3_BINARY, env.LOCAL_S3_DATA_DIR, env.LOCAL_S3_CONFIG];
  if (!paths.some(Boolean)) return null;
  if (!paths.every(Boolean) || !paths.every(pathExists)) {
    throw new Error('LOCAL_S3_BINARY, LOCAL_S3_DATA_DIR, and LOCAL_S3_CONFIG must all exist.');
  }
  return [
    'mini', `-dir=${env.LOCAL_S3_DATA_DIR}`, `-bucket=${env.S3_BUCKET || 'invoyis-originals'}`,
    '-ip.bind=127.0.0.1', '-s3.port=8333', '-s3.allowedOrigins=https://localhost:3000',
    `-s3.config=${env.LOCAL_S3_CONFIG}`,
  ];
}
