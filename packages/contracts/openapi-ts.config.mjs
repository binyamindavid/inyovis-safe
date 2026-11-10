/** @type {import('@hey-api/openapi-ts').UserConfig} */
export default {
  input: './openapi.yaml',
  output: './src/generated',
  plugins: ['@hey-api/typescript', '@hey-api/sdk', '@hey-api/client-fetch'],
};
