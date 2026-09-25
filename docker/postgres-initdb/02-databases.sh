#!/bin/bash
# Runs database/setup.sql in each database: extensions, the system schema,
# its privileges and the tenant-role functions are per database.
set -euo pipefail

for db in "$POSTGRES_DB" goerp_dev; do
	psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$db" \
		-f /docker-entrypoint-initdb.d/database/setup.sql
done
