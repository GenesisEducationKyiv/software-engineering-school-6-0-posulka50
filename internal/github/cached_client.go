package github

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

const cacheTTL = 10 * time.Minute

// CachedClient wraps a RepoChecker and caches repository existence checks in Redis.
type CachedClient struct {
	inner RepoChecker
	redis *redis.Client
}

// NewCachedClient creates a CachedClient that caches CheckRepo results using Redis.
func NewCachedClient(inner RepoChecker, redisClient *redis.Client) *CachedClient {
	return &CachedClient{inner: inner, redis: redisClient}
}

// CheckRepo checks the Redis cache first; on miss delegates to the inner RepoChecker and caches the result.
func (c *CachedClient) CheckRepo(ctx context.Context, owner, repo string) error {
	cacheKey := fmt.Sprintf("github:repo:%s/%s", owner, repo)

	if cached, err := c.redis.Get(ctx, cacheKey).Result(); err == nil {
		log.Printf("github: cache hit for repo %s/%s (%s)", owner, repo, cached)
		if cached == "notfound" {
			return ErrNotFound
		}
		return nil
	}

	err := c.inner.CheckRepo(ctx, owner, repo)

	switch {
	case err == nil:
		c.redis.Set(ctx, cacheKey, "exists", cacheTTL)
	case errors.Is(err, ErrNotFound):
		c.redis.Set(ctx, cacheKey, "notfound", cacheTTL)
	}

	return err
}
