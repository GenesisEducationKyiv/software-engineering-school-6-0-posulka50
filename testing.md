# Testing

## Unit tests

```bash
go test -v -race -count=1 ./...
```

Runs all unit tests. No external services required.

## Integration tests

```bash
go test -tags integration -v -race -count=1 -timeout 300s ./integration/...
```

Requires Docker. A PostgreSQL container is started automatically via testcontainers on every run.

## E2E tests

```bash
cd e2e && npm ci && npx playwright install --with-deps chromium
docker compose -f docker-compose.e2e.yml up -d --build --wait
BASE_URL=http://localhost:8080 npx playwright test
docker compose -f docker-compose.e2e.yml down
```

Requires Docker and Node.js. The full application stack (PostgreSQL, Redis, fake email server, app) is started via Docker Compose before the tests run.

To view the HTML report after a run:

```bash
cd e2e && npx playwright show-report
```
