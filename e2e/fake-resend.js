// Minimal HTTP server that stands in for the Resend API during E2E tests.
// Stores sent emails in memory and exposes a test-only API to read/clear them.
//
// POST <any path>  — accept an email payload, store it, return {"id":"..."}
// GET  /emails     — return all stored emails as JSON
// DELETE /emails   — clear stored emails (call in beforeEach)
const http = require('http');

const emails = [];

http
  .createServer((req, res) => {
    if (req.method === 'POST') {
      let body = '';
      req.on('data', (chunk) => (body += chunk));
      req.on('end', () => {
        try {
          emails.push(JSON.parse(body));
        } catch {}
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ id: `fake-${Date.now()}` }));
      });
      return;
    }

    if (req.method === 'GET' && req.url === '/emails') {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify(emails));
      return;
    }

    if (req.method === 'DELETE' && req.url === '/emails') {
      emails.length = 0;
      res.writeHead(204);
      res.end();
      return;
    }

    res.writeHead(404);
    res.end();
  })
  .listen(3000, '0.0.0.0', () => {
    console.log('fake-resend listening on :3000');
  });
