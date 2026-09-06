\set ON_ERROR_STOP on

SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'business_user', :'business_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'business_user') \gexec
SELECT format('ALTER ROLE %I WITH LOGIN PASSWORD %L', :'business_user', :'business_password') \gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'business_db', :'business_user')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'business_db') \gexec

SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'log_user', :'log_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'log_user') \gexec
SELECT format('ALTER ROLE %I WITH LOGIN PASSWORD %L', :'log_user', :'log_password') \gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'log_db', :'log_user')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'log_db') \gexec

\connect :business_db
SELECT format('GRANT ALL ON SCHEMA public TO %I', :'business_user') \gexec
\connect :log_db
SELECT format('GRANT ALL ON SCHEMA public TO %I', :'log_user') \gexec
