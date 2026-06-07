package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/posul/github-notifier/internal/release/domain"
)

type RepoRepository struct {
	db *pgxpool.Pool
}

func NewRepoRepository(db *pgxpool.Pool) *RepoRepository {
	return &RepoRepository{db: db}
}

func (r *RepoRepository) GetOrCreate(ctx context.Context, fullName string) (*domain.Repository, error) {
	repo := &domain.Repository{
		ID:        uuid.New().String(),
		FullName:  fullName,
		CreatedAt: time.Now().UTC(),
	}
	err := r.db.QueryRow(ctx,
		`INSERT INTO repositories (id, full_name, created_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (full_name) DO UPDATE SET full_name = EXCLUDED.full_name
		 RETURNING id, full_name, last_seen_tag, created_at`,
		repo.ID, repo.FullName, repo.CreatedAt,
	).Scan(&repo.ID, &repo.FullName, &repo.LastSeenTag, &repo.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get or create repository: %w", err)
	}
	return repo, nil
}

func (r *RepoRepository) GetAllWithConfirmedSubscriptions(ctx context.Context) ([]*domain.Repository, error) {
	rows, err := r.db.Query(ctx,
		`SELECT r.id, r.full_name, r.last_seen_tag, r.created_at
		 FROM repositories r
		 WHERE EXISTS (
		     SELECT 1 FROM subscriptions s
		     WHERE s.repo_id = r.id AND s.confirmed = true
		 )`,
	)
	if err != nil {
		return nil, fmt.Errorf("get repos with confirmed subscriptions: %w", err)
	}
	defer rows.Close()

	var repos []*domain.Repository
	for rows.Next() {
		var repo domain.Repository
		if err := rows.Scan(&repo.ID, &repo.FullName, &repo.LastSeenTag, &repo.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan repository: %w", err)
		}
		repos = append(repos, &repo)
	}
	return repos, rows.Err()
}

func (r *RepoRepository) UpdateLastSeenTag(ctx context.Context, id string, tag string) error {
	_, err := r.db.Exec(ctx, `UPDATE repositories SET last_seen_tag = $1 WHERE id = $2`, tag, id)
	if err != nil {
		return fmt.Errorf("update last seen tag: %w", err)
	}
	return nil
}
