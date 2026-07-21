package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/posul/github-notifier/internal/subscription/domain"
)

// SubscriptionRepository is a PostgreSQL implementation of the subscription storage.
type SubscriptionRepository struct {
	db *pgxpool.Pool
}

func NewSubscriptionRepository(db *pgxpool.Pool) *SubscriptionRepository {
	return &SubscriptionRepository{db: db}
}

const subSelectCols = `
	s.id, s.repo_id, r.full_name, s.email, s.confirmed,
	s.confirm_token, s.unsubscribe_token, s.created_at, r.last_seen_tag`

const subFromJoin = `
	FROM subscriptions s
	INNER JOIN repositories r ON r.id = s.repo_id`

func (r *SubscriptionRepository) Create(ctx context.Context, sub *domain.Subscription) error {
	return insertSubscriptionRow(ctx, r.db, sub)
}

func insertSubscriptionRow(ctx context.Context, ex pgxExecer, sub *domain.Subscription) error {
	_, err := ex.Exec(ctx,
		`INSERT INTO subscriptions (id, repo_id, email, confirmed, confirm_token, unsubscribe_token, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		sub.ID, sub.RepoID, sub.Email, sub.Confirmed,
		sub.ConfirmToken, sub.UnsubscribeToken, sub.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrAlreadyExists
		}
		return fmt.Errorf("create subscription: %w", err)
	}
	return nil
}

// CreateWithSaga inserts the subscription row and its pending saga row in a
// single transaction. A crash between the two writes would otherwise orphan
// the subscription — no saga row exists to drive cleanup, and the sweeper
// (which only scans pending sagas) would never see the row. With the two
// writes joined, either both land (sweeper compensates on publish failure)
// or neither lands (clean retry).
func (r *SubscriptionRepository) CreateWithSaga(ctx context.Context, sub *domain.Subscription, s *domain.Saga) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := insertSubscriptionRow(ctx, tx, sub); err != nil {
		return err
	}
	if err := insertSagaRow(ctx, tx, s); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit subscription+saga: %w", err)
	}
	return nil
}

// GetByID fetches a single subscription by its surrogate id. Used by the
// saga orchestrator's sync-retry path to rebuild the confirmation URL from
// a stuck saga's subscription reference.
func (r *SubscriptionRepository) GetByID(ctx context.Context, id string) (*domain.Subscription, error) {
	var sub domain.Subscription
	err := r.db.QueryRow(ctx,
		`SELECT`+subSelectCols+subFromJoin+`
		 WHERE s.id = $1`, id,
	).Scan(&sub.ID, &sub.RepoID, &sub.Repo, &sub.Email, &sub.Confirmed,
		&sub.ConfirmToken, &sub.UnsubscribeToken, &sub.CreatedAt, &sub.LastSeenTag)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get by id: %w", err)
	}
	return &sub, nil
}

func (r *SubscriptionRepository) GetByConfirmToken(ctx context.Context, token string) (*domain.Subscription, error) {
	var sub domain.Subscription
	err := r.db.QueryRow(ctx,
		`SELECT`+subSelectCols+subFromJoin+`
		 WHERE s.confirm_token = $1`, token,
	).Scan(&sub.ID, &sub.RepoID, &sub.Repo, &sub.Email, &sub.Confirmed,
		&sub.ConfirmToken, &sub.UnsubscribeToken, &sub.CreatedAt, &sub.LastSeenTag)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get by confirm token: %w", err)
	}
	return &sub, nil
}

func (r *SubscriptionRepository) GetByUnsubscribeToken(ctx context.Context, token string) (*domain.Subscription, error) {
	var sub domain.Subscription
	err := r.db.QueryRow(ctx,
		`SELECT`+subSelectCols+subFromJoin+`
		 WHERE s.unsubscribe_token = $1`, token,
	).Scan(&sub.ID, &sub.RepoID, &sub.Repo, &sub.Email, &sub.Confirmed,
		&sub.ConfirmToken, &sub.UnsubscribeToken, &sub.CreatedAt, &sub.LastSeenTag)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get by unsubscribe token: %w", err)
	}
	return &sub, nil
}

func (r *SubscriptionRepository) GetByEmail(ctx context.Context, email string) ([]*domain.Subscription, error) {
	rows, err := r.db.Query(ctx,
		`SELECT`+subSelectCols+subFromJoin+`
		 WHERE s.email = $1 AND s.confirmed = true`, email,
	)
	if err != nil {
		return nil, fmt.Errorf("get by email: %w", err)
	}
	defer rows.Close()
	return scanSubscriptions(rows)
}

func (r *SubscriptionRepository) GetConfirmedByRepoID(ctx context.Context, repoID string) ([]*domain.Subscription, error) {
	rows, err := r.db.Query(ctx,
		`SELECT`+subSelectCols+subFromJoin+`
		 WHERE s.repo_id = $1 AND s.confirmed = true`, repoID,
	)
	if err != nil {
		return nil, fmt.Errorf("get confirmed by repo id: %w", err)
	}
	defer rows.Close()
	return scanSubscriptions(rows)
}

func (r *SubscriptionRepository) Confirm(ctx context.Context, id string) error {
	result, err := r.db.Exec(ctx, `UPDATE subscriptions SET confirmed = true WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("confirm subscription: %w", err)
	}
	if result.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *SubscriptionRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.Exec(ctx, `DELETE FROM subscriptions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	if result.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *SubscriptionRepository) ExistsByEmailAndRepoID(ctx context.Context, email, repoID string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM subscriptions WHERE email = $1 AND repo_id = $2)`,
		email, repoID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check exists: %w", err)
	}
	return exists, nil
}

func scanSubscriptions(rows pgx.Rows) ([]*domain.Subscription, error) {
	var subs []*domain.Subscription
	for rows.Next() {
		var sub domain.Subscription
		if err := rows.Scan(
			&sub.ID, &sub.RepoID, &sub.Repo, &sub.Email, &sub.Confirmed,
			&sub.ConfirmToken, &sub.UnsubscribeToken, &sub.CreatedAt, &sub.LastSeenTag,
		); err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		subs = append(subs, &sub)
	}
	return subs, rows.Err()
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
