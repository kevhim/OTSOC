# Local Development Environment

This directory contains the Docker Compose configuration for local development of RedCyberFox.

## Prerequisites
- Docker
- Docker Compose

## How to Start
Run the following command in this directory:
```bash
docker compose up -d
```

## How to Stop
```bash
docker compose down
```

## How to Inspect Health
Check the container status and health:
```bash
docker compose ps
```
Or view logs:
```bash
docker compose logs -f
```

## How to Reset Development Data
To completely wipe the local development databases and start fresh:
```bash
docker compose down -v
```
**Warning:** This will destroy all data in the local PostgreSQL and Valkey volumes.
