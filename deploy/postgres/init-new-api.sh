#!/bin/sh
set -eu

for name in NEW_API_DB_NAME NEW_API_DB_USER NEW_API_DB_PASSWORD NEW_API_LOG_DB_NAME NEW_API_LOG_DB_USER NEW_API_LOG_DB_PASSWORD; do
  eval "value=\${$name:-}"
  if [ -z "$value" ]; then
    echo "$name is required" >&2
    exit 1
  fi
done

psql --set=ON_ERROR_STOP=1 \
  --username="$POSTGRES_USER" \
  --dbname="$POSTGRES_DB" \
  --set=business_db="$NEW_API_DB_NAME" \
  --set=business_user="$NEW_API_DB_USER" \
  --set=business_password="$NEW_API_DB_PASSWORD" \
  --set=log_db="$NEW_API_LOG_DB_NAME" \
  --set=log_user="$NEW_API_LOG_DB_USER" \
  --set=log_password="$NEW_API_LOG_DB_PASSWORD" \
  --file=/opt/manyrouter/init-new-api.sql
