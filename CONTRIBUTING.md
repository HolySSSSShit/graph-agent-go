# Contributing

Issues and pull requests are welcome. For a substantial feature or a public
contract change, describe the intended behavior in an issue before implementing it.

## Local Setup

1. Install Go 1.26 or newer.
2. Fork and clone the repository.
3. Run the example using the README instructions.
4. Create a branch for your change.

No model key or MCP server is required for the example or ordinary unit tests.

## Design Rules

Graph routing is the only source of flow control. Each workflow owns its typed
State, StateCodec, node registry and graph. Nodes must not select the next node,
mutate the read-only Runtime or call another node directly.

Runner handles generic execution and persistence. Workflow-specific behavior
belongs in the workflow package. Keep startup wiring small and reuse the existing
interfaces rather than adding parallel owners or implicit fallbacks.

Keep files in UTF-8 and use concise Chinese comments to match existing source.
Update README, FEATURES and relevant design documents when behavior changes.

## Validation

Run from the repository root:

```bash
gofmt -w cmd internal
go test ./...
go vet ./...
go test -race ./internal/...
go build ./cmd/agent
```

Race tests require a C compiler and CGO. Add tests for changes to routing,
state ownership, protocols, persistence or concurrency.

Database tests require a dedicated PostgreSQL database and
`AGENT_TEST_POSTGRES_DSN`. They apply migrations and write test records.
Run the session and trace packages serially with `-p 1`, as shown in README.

## Pull Requests

Explain the problem, resulting behavior and verification performed. Keep changes
focused. Include doc updates and avoid checking in credentials, local configs,
runtime data, logs, executables or build caches.

Contributions are provided under this repository's MIT license. Only submit
code and assets you have the right to contribute.
