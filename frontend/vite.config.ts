import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import type { IncomingMessage } from 'node:http';

const CLIENT_SECRETS: Record<string, string> = {
  'internal-service': 'internal-secret',
  'provider-a': 'provider-a-secret',
  'provider-b': 'provider-b-secret',
};

const KEYCLOAK_TOKEN_URL =
  'http://localhost:8081/realms/jungle-go/protocol/openid-connect/token';

function readBody(req: IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    let body = '';
    req.on('data', (chunk) => {
      body += chunk;
    });
    req.on('end', () => resolve(body));
    req.on('error', reject);
  });
}

function tokenProxy(): Plugin {
  return {
    name: 'jungle-go-token-proxy',
    configureServer(server) {
      server.middlewares.use('/token', async (req, res) => {
        if (req.method !== 'POST') {
          res.statusCode = 405;
          res.end('method not allowed');
          return;
        }

        try {
          const raw = await readBody(req);
          const { client } = JSON.parse(raw || '{}') as { client?: string };
          const secret = client ? CLIENT_SECRETS[client] : undefined;
          if (!client || !secret) {
            res.statusCode = 400;
            res.setHeader('Content-Type', 'application/json');
            res.end(JSON.stringify({ error: 'unknown client' }));
            return;
          }

          const response = await fetch(KEYCLOAK_TOKEN_URL, {
            method: 'POST',
            headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
            body: new URLSearchParams({
              grant_type: 'client_credentials',
              client_id: client,
              client_secret: secret,
            }),
          });

          const text = await response.text();
          res.statusCode = response.status;
          res.setHeader('Content-Type', 'application/json');
          res.end(text);
        } catch (error) {
          res.statusCode = 500;
          res.setHeader('Content-Type', 'application/json');
          res.end(JSON.stringify({ error: String(error) }));
        }
      });
    },
  };
}

export default defineConfig({
  plugins: [react(), tokenProxy()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
    },
  },
});
