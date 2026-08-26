# Debian-based postgres:18 (not postgres:18-alpine) — the official image's
# apt.postgresql.org PGDG sources already list a matching pg_partman
# package for the installed major version; Alpine has no equivalent
# prebuilt package. Production Postgres provisioning lives outside this
# repo and must install the same postgresql-18-partman package (or build
# pg_partman from source) for goerp#194's partman.create_parent/
# run_maintenance calls to work there too.
FROM postgres:18

RUN apt-get update \
    && apt-get install -y --no-install-recommends postgresql-18-partman \
    && rm -rf /var/lib/apt/lists/*
