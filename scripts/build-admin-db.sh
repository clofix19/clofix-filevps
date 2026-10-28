#!/usr/bin/env bash
# Build admin (UI + controller) image, pull db image, and start both via docker compose.
# Usage: ./scripts/build-admin-db.sh [build|up|down|logs]
set -euo pipefail
cd "$(dirname "$0")/.."
[ -f .env ] || { echo "missing .env (cp .env.example .env and set DB_PASSWORD)"; exit 1; }
case "${1:-up}" in
  build) docker compose build admin; docker compose pull db ;;
  up)    docker compose build admin; docker compose pull db; docker compose up -d db admin; docker compose ps ;;
  down)  docker compose down ;;
  logs)  docker compose logs -f db admin ;;
  *) echo "usage: $0 [build|up|down|logs]"; exit 2 ;;
esac
