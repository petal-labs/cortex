# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.1.x   | :white_check_mark: |

## Reporting a Vulnerability

We take security vulnerabilities seriously. If you discover a security issue, please report it responsibly.

### How to Report

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please report security vulnerabilities through GitHub's private security advisory feature:

1. Go to [Security Advisories](https://github.com/petal-labs/cortex/security/advisories)
2. Click "New draft security advisory"
3. Fill out the vulnerability details

Alternatively, you can email security concerns to the maintainers directly.

### What to Include

- Description of the vulnerability
- Steps to reproduce the issue
- Potential impact assessment
- Any suggested fixes (optional)
- Your contact information for follow-up

### What to Expect

- **Acknowledgment**: We will acknowledge receipt within 48 hours
- **Initial Assessment**: We will provide an initial assessment within 7 days
- **Resolution Timeline**: We aim to resolve critical issues within 30 days
- **Disclosure**: We will coordinate disclosure timing with you

## Security Considerations

### Data Storage

- **SQLite**: Data is stored locally in `~/.cortex/data/` by default
- **PostgreSQL**: Ensure your database connection uses TLS in production
- **Sensitive Data**: Cortex stores conversation history and knowledge documents; protect access accordingly

### Configuration

- Never commit `config.yaml` files containing credentials
- Use environment variables for sensitive configuration in production
- Restrict file permissions on configuration and data directories

### Network Security

- The MCP server (stdio mode) does not expose network ports
- SSE transport mode binds to localhost by default
- Use a reverse proxy with TLS for production SSE deployments
- Metrics endpoint (`:9811`) should not be exposed publicly without authentication

### Embedding Service

- Connections to the Iris embedding service should use HTTPS in production
- API keys should be stored securely and rotated regularly

## Security Best Practices

### For Operators

```bash
# Restrict data directory permissions
chmod 700 ~/.cortex
chmod 600 ~/.cortex/config.yaml

# Use environment variables for secrets
export CORTEX_DATABASE_URL="postgres://..."
export IRIS_API_KEY="..."
```

### For Developers

- Validate and sanitize all user inputs
- Use parameterized queries (already implemented)
- Follow least privilege principles
- Keep dependencies updated

## Known Security Considerations

- **SQL Injection**: Mitigated through parameterized queries in both SQLite and PostgreSQL backends
- **Path Traversal**: File operations are restricted to configured directories
- **Memory Safety**: Go's memory safety prevents buffer overflow vulnerabilities

## Dependency Updates

We regularly update dependencies to address security vulnerabilities. Security updates are prioritized and released as patch versions.

To check for known vulnerabilities in dependencies:

```bash
go list -m all | nancy sleuth
# or
govulncheck ./...
```
