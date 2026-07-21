package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/posul/github-notifier/internal/subscription/domain"
)

// SagaRepository persists Subscribe saga state in PostgreSQL.
type SagaRepository struct {
	db *pgxpool.Pool
}

func NewSagaRepository(db *pgxpool.Pool) *SagaRepository {
	return &SagaRepository{db: db}
}

const sagaSelectCols = `id, subscription_id, state, last_error, started_at, completed_at`

// pgxExecer is any pgx handle that can execute a statement — a pool, a
// connection, or a transaction. Lets insertSagaRow run under either the
// standalone Create path or the atomic CreateWithSaga transaction.
type pgxExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func insertSagaRow(ctx context.Context, ex pgxExecer, s *domain.Saga) error {
	_, err := ex.Exec(ctx,
		`INSERT INTO subscription_sagas (id, subscription_id, state, started_at)
		 VALUES ($1, $2, $3, $4)`,
		s.ID, s.SubscriptionID, string(s.State), s.StartedAt,
	)
	if err != nil {
		return fmt.Errorf("create saga: %w", err)
	}
	return nil
}

func (r *SagaRepository) Create(ctx context.Context, s *domain.Saga) error {
	return insertSagaRow(ctx, r.db, s)
}

func (r *SagaRepository) Get(ctx context.Context, id string) (*domain.Saga, error) {
	var s domain.Saga
	var state string
	err := r.db.QueryRow(ctx,
		`SELECT `+sagaSelectCols+` FROM subscription_sagas WHERE id = $1`, id,
	).Scan(&s.ID, &s.SubscriptionID, &state, &s.LastError, &s.StartedAt, &s.CompletedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get saga: %w", err)
	}
	s.State = domain.SagaState(state)
	return &s, nil
}

// markTerminal transitions a saga from pending to a terminal state. The
// WHERE state='pending' guard makes the update idempotent: a duplicate reply
// event (re-delivered by RabbitMQ) is a no-op rather than a corruption.
func (r *SagaRepository) markTerminal(ctx context.Context, id string, newState domain.SagaState, lastError *string) error {
	now := time.Now().UTC()
	result, err := r.db.Exec(ctx,
		`UPDATE subscription_sagas
		 SET state = $1, last_error = $2, completed_at = $3
		 WHERE id = $4 AND state = 'pending'`,
		string(newState), lastError, now, id,
	)
	if err != nil {
		return fmt.Errorf("mark saga %s: %w", newState, err)
	}
	if result.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *SagaRepository) MarkCompleted(ctx context.Context, id string) error {
	return r.markTerminal(ctx, id, domain.SagaStateCompleted, nil)
}

func (r *SagaRepository) MarkCompensated(ctx context.Context, id string, reason string) error {
	return r.markTerminal(ctx, id, domain.SagaStateCompensated, &reason)
}

func (r *SagaRepository) MarkTimedOut(ctx context.Context, id string) error {
	reason := "timeout"
	return r.markTerminal(ctx, id, domain.SagaStateTimedOut, &reason)
}

func (r *SagaRepository) GetPendingOlderThan(ctx context.Context, threshold time.Time) ([]*domain.Saga, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+sagaSelectCols+`
		 FROM subscription_sagas
		 WHERE state = 'pending' AND started_at < $1`, threshold,
	)
	if err != nil {
		return nil, fmt.Errorf("get pending sagas: %w", err)
	}
	defer rows.Close()

	var sagas []*domain.Saga
	for rows.Next() {
		var s domain.Saga
		var state string
		if err := rows.Scan(&s.ID, &s.SubscriptionID, &state, &s.LastError, &s.StartedAt, &s.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan saga: %w", err)
		}
		s.State = domain.SagaState(state)
		sagas = append(sagas, &s)
	}
	return sagas, rows.Err()
}
