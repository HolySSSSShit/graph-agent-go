# Go Agent

A Go agent runtime template with a small example workflow. Clone or fork this
repository to build your own workflows. This is a template, not a public Go SDK:
implementation packages live under `internal/`.

The runtime provides typed workflow state, graph routing, checkpoint recovery,
leases, cancellation, approvals, HTTP/SSE, session storage, model adapters,
MCP tools, context management and audit interfaces.

The runnable `order_lookup` example uses fixed responses. It does not query real
orders or call a model, MCP server or external business API. Its skill and tool
bindings demonstrate declarations only.

## Quick Start

Requires Go 1.26 or newer. Run commands from the repository root.

```bash
git clone https://github.com/HolySSSSShit/go-agent.git
cd go-agent
AGENT_CONFIG_FILE=config.integration.toml go run ./cmd/agent
```

On Windows PowerShell:

```powershell
$env:AGENT_CONFIG_FILE = "config.integration.toml"
go run ./cmd/agent
```

The sample configuration uses memory storage and listens on
`127.0.0.1:8081`. No API key or database is required. In another terminal:

```bash
curl http://127.0.0.1:8081/healthz
curl -X POST http://127.0.0.1:8081/api/agent/chat \
  -H 'Content-Type: application/json' \
  -d '{"workflow":"order_lookup","message":"1001"}'
```

The response contains `data.run_id`, `data.session_id` and `data.events_url`.
Subscribe to the returned events URL:

```bash
curl -N http://127.0.0.1:8081/api/agent/runs/<run_id>/events
```

The stream ends with `message.completed` and the example's fixed response.
Memory sessions are lost on restart.

## Architecture

```mermaid
flowchart LR
    HTTP[HTTP API] --> Runner[Runner]
    Runner --> Graph[Workflow Graph]
    Graph --> Nodes[Typed Nodes]
    Runner --> State[StateCodec and Checkpoints]
    Runner --> Storage[Sessions and Leases]
    Runner --> Events[Audit and SSE]
```

| Package | Responsibility |
| --- | --- |
| `internal/core` | Shared protocols and dependency interfaces |
| `internal/orchestrator` | Graphs, routing, typed definitions and Runner |
| `internal/api` | HTTP, SSE, session management, approvals and cancellation |
| `internal/session`, `internal/trace` | Memory/PostgreSQL storage, migrations and audit sinks |
| `internal/model`, `internal/ctxmgr` | Model/image adapters and context management |
| `internal/mcpclient`, `internal/toolregistry`, `internal/harness` | MCP sources, tool registry and result handles |
| `internal/workflow/orderexample` | Minimal workflow example |
| `prompts/` | Global and example workflow prompt modules |

Only the example's required dependencies are wired into `cmd/agent`.
Model calls, MCP access, compaction, approval handling and trace sinks require
explicit wiring when you add a workflow that uses them.

## Add a Workflow

Start with [the workflow guide](docs/workflow-example.md).
Each workflow owns its typed State, StateCodec, Graph, NodeRegistry, skill/tool
bindings and prompt module. Nodes update their state; Graph deciders choose routes.
Runner handles execution and persistence without reading workflow-specific fields.

Forks should replace the module path in `go.mod` and imports with their own
repository path if they intend to maintain a separate module.

## Configuration and Storage

Use `AGENT_CONFIG_FILE` to select a TOML file and `AGENT_ENV_FILE` to select an
environment file. Without those variables the loader uses `config.toml` and
`.env`; missing files fall back to built-in defaults.

Copy `.env.example` for optional model/MCP credentials. Keep real credentials
out of Git. Model and MCP configuration does not enable those dependencies in
the example.

For PostgreSQL storage, run `docker compose -f docker-compose.storage.yml up -d`
and set the following in a local, ignored `config.toml`:

```toml
[database]
driver = "postgres"
url = "postgres://agent:agent-local-password@127.0.0.1:5432/agent?sslmode=disable"
timezone = "Asia/Shanghai"
```

Session initialization applies the bundled migrations. Use a dedicated
development database. This template uses local development identity by default;
see [SECURITY.md](SECURITY.md) before exposing an endpoint to other users.

## Development

```bash
go test ./...
go test -race ./internal/...
go vet ./...
go build -o bin/agent ./cmd/agent
```

Use `bin/agent.exe` for a Windows build. Race checks require CGO and a C compiler.
Database tests skip unless `AGENT_TEST_POSTGRES_DSN` is set. To run them against a
dedicated test database:

```bash
AGENT_TEST_POSTGRES_DSN='postgres://agent:agent-local-password@127.0.0.1:5432/agent?sslmode=disable' \
  go test -count=1 -p 1 ./internal/session ./internal/trace
```

GitHub Actions runs Windows/Linux tests, race checks, builds and PostgreSQL
integration tests.

See [CONTRIBUTING.md](CONTRIBUTING.md), [FEATURES.md](FEATURES.md) and the
[architecture notes](docs/technical-architecture.md).

## License

[MIT](LICENSE).
