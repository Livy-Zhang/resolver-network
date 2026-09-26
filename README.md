# resolver-network

This Go service runs a monthly settlement task. It reads delegation events from The Graph, calculates `delegatedAmount × durationSeconds` for each natural month, and persists the result in PostgreSQL.

Reward periods use UTC boundaries. Configure the cloud-server scheduler to trigger the task at `00:30 UTC` on the first day of each month. The allocation formula is `delegatedAmount × durationSeconds × rewardRate / 365 days / 1e18`. This uses the standard APR convention of a fixed 365-day year; leap years do not change the denominator to 366 days.

## Docker development

```bash
cp .env.example .env
# Edit .env: set GRAPH_ENDPOINT, RPC_URL, and REWARDS_UPDATER_PRIVATE_KEY.
# Use the legacy Docker builder when Docker Buildx is unavailable.
DOCKER_BUILDKIT=0 docker build -t resolver-network:local .
docker compose up -d --no-build
docker compose logs -f migrate api
```

The `migrate` container applies database migrations before the API starts. PostgreSQL data is stored in the `postgres-data` named volume.

The `postgres` hostname in `.env` is only resolvable inside Docker Compose. If you run Go commands directly on the host, override the database URLs to use `localhost`.

## Test database

Integration tests use an isolated PostgreSQL container and never share the production-development database:

```bash
docker compose -f docker-compose.test.yml up -d --wait
export TEST_DATABASE_URL='postgres://resolver:resolver@127.0.0.1:55433/resolver_network_test?sslmode=disable'
DATABASE_URL="$TEST_DATABASE_URL" go run ./cmd/migrate up
go test ./...
docker compose -f docker-compose.test.yml down -v
```

The final command deletes only the disposable `postgres-test-data` volume.

The service exposes read-only endpoints on `HTTP_ADDR` (default `127.0.0.1:8080`):

```bash
curl 'http://127.0.0.1:8080/healthz'
curl 'http://127.0.0.1:8080/readyz'
curl 'http://127.0.0.1:8080/metrics'
curl 'http://127.0.0.1:8080/v1/monthly-rewards-details?month=202608'
curl 'http://127.0.0.1:8080/v1/merkle-proof-submission?month=202608'
```

`/healthz` reports that the HTTP process is running. `/readyz` also verifies database readiness and applied migrations; Docker uses it for the API healthcheck. `/metrics` exposes expvar metrics for monitoring.

The business endpoints contain delegator addresses, reward amounts, and Merkle proofs. Keep the service bound to localhost unless an authenticated gateway or reverse proxy protects it.

`cmd/api` does not apply schema migrations. Run `go run ./cmd/migrate up` as a separate release step.

## Monthly workflow

Run one monthly settlement manually:

```bash
docker compose --profile workers run --rm monthly-workflow
```

`monthly-workflow` runs `settlement-worker`, then `merkle-worker`, then `root-worker`; it stops if any step fails. Docker Compose does not schedule this command by itself. For automatic monthly execution on a cloud server, trigger this command with a server scheduler such as a systemd timer.

The monthly settlement workflow is idempotent: repeated runs for the same month produce the same result. It does not implement a distributed mutex for settlement workers, so deployments must run a single instance. Database migrations use a PostgreSQL advisory lock to prevent concurrent execution.

All three workers use the previous complete UTC calendar month when `--month` is omitted.
Pass `--month YYYYMM` to select a different period; the workflow passes the same selected month
to every worker. In particular, `root-worker` queries and processes only root submissions for
that month, never pending submissions from other months.

Submitting a Merkle root does not make rewards claimable. Each resolver must approve its
RewardsDistributor to spend that epoch's `totalReward` and then call
`confirmRoot(epochId)` from the resolver account. The distributor moves the epoch from
`Pending` to `Claimable` only after that transfer succeeds. `root-worker` records a root as
`confirmed` only after its on-chain epoch status is `Claimable`; a submitted `Pending` root is
therefore intentionally reported as not yet confirmed.

### Resolver and service responsibilities

The resolver owns and signs the `approve` and `confirmRoot` transactions with its own wallet.
The service never stores a resolver private key and never transfers a resolver's reward tokens.
It exposes `GET /v1/merkle-proof-submission?month=YYYYMM` so a resolver can obtain the
distributor address, epoch ID, total reward, root submission transaction hash, and current
status needed by its wallet or frontend. The service's `root-worker` is an observer only: it
polls the distributor epoch and updates the API status to `confirmed` after the resolver's
confirmation has made the epoch `Claimable`.

## Production release

Run the migration job with a database role that can change the schema, verify `status`, then deploy the API or worker using a separate least-privileged database role. The repository includes a manually triggered GitHub Actions workflow that requires a protected `production` environment and the `DATABASE_URL_MIGRATOR` secret. It only migrates the database; service rollout remains deployment-platform specific.

Migration files are append-only. After `001_initial_schema.sql` is deployed, create a new `002_description.sql` for every schema change instead of modifying `001`.

On its first calculation, the service replays historical delegation events to build the opening state; later runs use the prior month-end snapshot incrementally.

Before monthly settlement, the service requires the RPC finalized block to pass the month boundary and the Subgraph `_meta` block to reach that finalized height. This prevents settlement from using provisional chain data.
