# OnionShop

OnionShop is a server-rendered Go storefront for **Tor Browser and Tor v3 onion services only**. The UI has no JavaScript or third-party CDNs, and the application is designed not to log client IP addresses. Mainstream browsers are not a supported target; this is a project policy, not something the server can reliably enforce.

> **Project status:** Application features in Phases 1–8 are implemented. Production deployment is **not yet production-ready**. See [Production readiness and deployment sequence](PRODUCTION_READINESS.md), especially the TLS/browser compatibility gate.

## TLS policy

OnionShop requires a self-signed ECDSA P-384 TLS certificate with an exact DNS SAN for the service's v3 `.onion` hostname. This is a separate TLS key, not the onion service's Ed25519 key. Tor's onion protocol authenticates the onion endpoint; SAN matching alone does not make a self-signed certificate trusted by a browser. Test the exact supported Tor Browser release and never click through a certificate warning. Do not use publicly CA-issued certificates or SPKI/hash pinning for this project. If the supported Tor Browser displays a warning, deployment is blocked pending a verified browser-compatible solution.

Generate the certificate only after the Tor v3 onion hostname is known:

```sh
./scripts/gen-certs.sh '<56-character-v3-hostname>.onion'
```

See `INSTALL.md` and `PRODUCTION_READINESS.md` for permissions, verification, and the required Tor Browser test.

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

Compose commands require the environment variables described in `INSTALL.md`. The self-signed certificate script requires the actual v3 onion hostname and does not print an SPKI pin.

## License

MIT — see [LICENSE](LICENSE).
