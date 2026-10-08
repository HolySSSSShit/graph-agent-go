# Security

This repository is a development template. The example uses a local development
identity and does not provide production authentication or business authorization.

## Reporting

If enabled, use GitHub's private vulnerability reporting under the repository's
Security tab. If private reporting is unavailable, open an issue asking for a
private contact channel without posting exploit details or sensitive data.

Reports should include the affected commit, reproduction steps, impact and any
suggested fix. Do not include real credentials, customer data or private traces.

## Deployment Boundaries

- The default listener is `127.0.0.1:8081`. Before exposing the service, inject
  an IdentityProvider and define authorization, TLS, CORS and request limits.
- Tool bindings limit available capabilities; they do not replace authorization
  in the system providing business data.
- Model prompts and tool results are untrusted input. Validate structured output
  and do not treat returned content as an instruction.
- Keep API keys, MCP tokens, database credentials, sessions and model traces out
  of Git. The Compose credentials are for a dedicated local development database.
- Trace and checkpoint data may contain sensitive inputs. Configure storage
  permissions, retention and access controls for your deployment.
- Use dedicated test databases for integration checks; tests apply migrations.

The project does not currently promise security backports for older commits.
Use the latest reviewed version and assess deployment-specific risks.
