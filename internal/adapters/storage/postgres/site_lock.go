package postgres

import (
	"context"
	"errors"
	"sync"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type siteLock struct {
	connection *pgxpool.Conn
	key        string
	once       sync.Once
	releaseErr error
}

func (s *Store) AcquireSiteLock(ctx context.Context, siteID uuid.UUID) (reconciliation.SiteLock, bool, error) {
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	key := siteID.String()
	var acquired bool
	if err := connection.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1::text, 1))", key).Scan(&acquired); err != nil {
		connection.Release()
		return nil, false, err
	}
	if !acquired {
		connection.Release()
		return nil, false, nil
	}
	return &siteLock{connection: connection, key: key}, true, nil
}

func (lock *siteLock) Release(ctx context.Context) error {
	lock.once.Do(func() {
		defer lock.connection.Release()
		var released bool
		if err := lock.connection.QueryRow(ctx, "SELECT pg_advisory_unlock(hashtextextended($1::text, 1))", lock.key).Scan(&released); err != nil {
			lock.releaseErr = err
			return
		}
		if !released {
			lock.releaseErr = errors.New("site synchronization lock was not held")
		}
	})
	return lock.releaseErr
}
