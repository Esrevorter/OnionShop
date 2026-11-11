# OnionShop

OnionShop is a server-rendered Go storefront intended to run as a Tor v3 onion service. The UI has no JavaScript or third-party CDNs, and the application is designed not to log client IP addresses.

> **Project status:** The application features in Phases 1–8 are implemented. Production deployment is **not yet production-ready**: see [Production readiness and deployment sequence](PRODUCTION_READINESS.md) for known infrastructure and TLS blockers. Do not treat a successful build or installer run as a security review.

## Documentation

| File | Contents |
|---|---|
| [AGENTS.md](AGENTS.md) | Contributor and agent constraints |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Application components and request flow |
| [SECURITY_AND_OPSEC.md](SECURITY_AND_OPSEC.md) | Threat model, intended controls, and security limitations |
| [PRODUCTION_READINESS.md](PRODUCTION_READINESS.md) | Deployment sequence, configuration checks, and unresolved production gates |
| [INSTALL.md](INSTALL.md) | Operator installation and development instructions |
| [TASKS.md](TASKS.md) | Feature and infrastructure status |
| [FIXMEFIRST.md](FIXMEFIRST.md) | Audit findings and remediation status |

## Stack

- Go, chi, `html/template`, pgx, Redis, and PostgreSQL
- nginx reverse proxy; application traffic to nginx uses a UNIX socket
- BTCPay Server invoice polling (no inbound webhooks)
- Tor onion service; admin access is intended to use a separate private onion and Dropbear

## Development

```sh
make test       # go test ./...
make build      # sqlc generation then Go build
make sqlc       # requires sqlc v1.27.0
make compose-config
```

Compose commands require the environment variables described in `INSTALL.md`. Do not use the development-generated self-signed certificate as an onion identity or rely on its printed SPKI hash to authenticate a service.

## Installation

The current installer and compose configuration are scaffolding, not a production security boundary. Review [PRODUCTION_READINESS.md](PRODUCTION_READINESS.md) and resolve its explicit gates before exposing or operating a real service.

## License

MIT — see [LICENSE](LICENSE).
