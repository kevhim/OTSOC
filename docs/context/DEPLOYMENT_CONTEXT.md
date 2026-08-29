# Deployment Context

## Development
- **PostgreSQL**: Local Docker Compose (Port 5433).
- **Valkey**: Local Docker Compose (Port 6379).
- **API Server / Worker**: Local host process or Docker.
- **Dashboard**: Local host node process.
*Never treat development credentials as production credentials.*

## Future (Production)
- rural deployment
- offline operation
- passive sensor
- eventual resilience/HA
