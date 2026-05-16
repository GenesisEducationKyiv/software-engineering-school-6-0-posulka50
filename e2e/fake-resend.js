// Minimal HTTP server that accepts any Resend API request and returns 200.
// Used in docker-compose.e2e.yml to avoid sending real emails during E2E tests.
const http = require('http');

http
  .createServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ id: 'fake-email-id' }));
  })
  .listen(3000, '0.0.0.0', () => {
    console.log('fake-resend listening on :3000');
  });
