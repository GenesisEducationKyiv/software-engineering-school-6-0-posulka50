package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/posul/github-notifier/internal/model"
)

var (
	// ErrNotFound is returned when a requested subscription does not exist.
	ErrNotFound = errors.New("not found")
	// ErrAlreadyExists is returned when a duplicate subscription is detected.
	ErrAlreadyExists = errors.New("already exists")
)

// SubscriptionRepository defines persistence operations for email subscriptions.
type SubscriptionRepository interface {
	Create(ctx context.Context, sub *model.Subscription) error
	GetByConfirmToken(ctx context.Context, token string) (*model.Subscription, error)
	GetByUnsubscribeToken(ctx context.Context, token string) (*model.Subscription, error)
	GetByEmail(ctx context.Context, email string) ([]*model.Subscription, error)
	GetConfirmedByRepoID(ctx context.Context, repoID string) ([]*model.Subscription, error)
	Confirm(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	ExistsByEmailAndRepoID(ctx context.Context, email, repoID string) (bool, error)
}

// PostgresRepository is a PostgreSQL implementation of SubscriptionRepository.
type PostgresRepository struct {
	db *pgxpool.Pool
}

// NewPostgresRepository creates a new PostgresRepository backed by the given connection pool.
func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: db}
}

const subSelectCols = `
	s.id, s.repo_id, r.full_name, s.email, s.confirmed,
	s.confirm_token, s.unsubscribe_token, s.created_at, r.last_seen_tag`

const subFromJoin = `
	FROM subscriptions s
	INNER JOIN repositories r ON r.id = s.repo_id`

// Create inserts a new subscription record.
func (r *PostgresRepository) Create(ctx context.Context, sub *model.Subscription) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO subscriptions (id, repo_id, email, confirmed, confirm_token, unsubscribe_token, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		sub.ID, sub.RepoID, sub.Email, sub.Confirmed,
		sub.ConfirmToken, sub.UnsubscribeToken, sub.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("create subscription: %w", err)
	}
	return nil
}

// GetByConfirmToken returns the subscription matching the given confirmation token.
func (r *PostgresRepository) GetByConfirmToken(ctx context.Context, token string) (*model.Subscription, error) {
	var sub model.Subscription
	err := r.db.QueryRow(ctx,
		`SELECT`+subSelectCols+subFromJoin+`
		 WHERE s.confirm_token = $1`, token,
	).Scan(&sub.ID, &sub.RepoID, &sub.Repo, &sub.Email, &sub.Confirmed,
		&sub.ConfirmToken, &sub.UnsubscribeToken, &sub.CreatedAt, &sub.LastSeenTag)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get by confirm token: %w", err)
	}
	return &sub, nil
}

// GetByUnsubscribeToken returns the subscription matching the given unsubscribe token.
func (r *PostgresRepository) GetByUnsubscribeToken(ctx context.Context, token string) (*model.Subscription, error) {
	var sub model.Subscription
	err := r.db.QueryRow(ctx,
		`SELECT`+subSelectCols+subFromJoin+`
		 WHERE s.unsubscribe_token = $1`, token,
	).Scan(&sub.ID, &sub.RepoID, &sub.Repo, &sub.Email, &sub.Confirmed,
		&sub.ConfirmToken, &sub.UnsubscribeToken, &sub.CreatedAt, &sub.LastSeenTag)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get by unsubscribe token: %w", err)
	}
	return &sub, nil
}

// GetByEmail returns all confirmed subscriptions for the given email address.
func (r *PostgresRepository) GetByEmail(ctx context.Context, email string) ([]*model.Subscription, error) {
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

// GetConfirmedByRepoID returns all confirmed subscriptions for the given repository ID.
func (r *PostgresRepository) GetConfirmedByRepoID(ctx context.Context, repoID string) ([]*model.Subscription, error) {
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

// Confirm marks the subscription with the given id as confirmed.
func (r *PostgresRepository) Confirm(ctx context.Context, id string) error {
	result, err := r.db.Exec(ctx, `UPDATE subscriptions SET confirmed = true WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("confirm subscription: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes the subscription with the given id.
func (r *PostgresRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.Exec(ctx, `DELETE FROM subscriptions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ExistsByEmailAndRepoID reports whether a subscription exists for the given email and repository.
func (r *PostgresRepository) ExistsByEmailAndRepoID(ctx context.Context, email, repoID string) (bool, error) {
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

func scanSubscriptions(rows pgx.Rows) ([]*model.Subscription, error) {
	var subs []*model.Subscription
	for rows.Next() {
		var sub model.Subscription
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
