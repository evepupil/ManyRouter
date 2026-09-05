-- +goose Up
CREATE UNIQUE INDEX sites_new_api_base_url_unique ON sites(new_api_base_url);

-- +goose Down
DROP INDEX sites_new_api_base_url_unique;
