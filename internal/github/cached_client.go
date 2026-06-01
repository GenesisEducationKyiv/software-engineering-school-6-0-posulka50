package github

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

const repoCheckCacheTTL = 10 * time.Minute

// CachedRepoChecker wraps a RepoChecker and caches repository existence checks in Redis.
type CachedRepoChecker struct {
	inner RepoChecker
	redis *redis.Client
}

func NewCachedRepoChecker(inner RepoChecker, redisClient *redis.Client) *CachedRepoChecker {
	return &CachedRepoChecker{inner: inner, redis: redisClient}
}

func (c *CachedRepoChecker) CheckRepo(ctx context.Context, owner, repo string) error {
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
		c.redis.Set(ctx, cacheKey, "exists", repoCheckCacheTTL)
	case errors.Is(err, ErrNotFound):
		c.redis.Set(ctx, cacheKey, "notfound", repoCheckCacheTTL)
	}

	return err
}
