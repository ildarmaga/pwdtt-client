import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'

const SUB_HDR = [
  'content-type',
  'subscription-userinfo',
  'profile-title',
  'announce',
  'profile-web-page-url',
  'profile-update-interval',
  'content-disposition',
];

function subFetchPlugin(): Plugin {
  return {
    name: 'sub-fetch',
    configureServer(server) {
      server.middlewares.use(async (req, res, next) => {
        if (!req.url?.startsWith('/sub-fetch?')) return next();
        try {
          const q = new URL(req.url, 'http://localhost');
          const target = q.searchParams.get('u');
          if (!target) {
            res.statusCode = 400;
            res.end('missing url');
            return;
          }
          const method = req.method === 'HEAD' ? 'HEAD' : 'GET';
          const r = await fetch(target, {
            method,
            headers: { Accept: 'text/plain', 'User-Agent': 'PWDTT/1.0' },
          });
          res.statusCode = r.status;
          SUB_HDR.forEach(h => {
            const v = r.headers.get(h);
            if (v) res.setHeader(h, v);
          });
          if (method === 'GET') {
            res.end(await r.text());
          } else {
            res.end();
          }
        } catch (e) {
          res.statusCode = 502;
          res.end(String(e));
        }
      });
    },
  };
}

export default defineConfig({
  plugins: [react(), subFetchPlugin()],
  server: {
    host: '0.0.0.0',
    port: 5173,
  },
})
