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
	key := fmt.Sprintf("github:repo:%s/%s", owner, repo)

	if hit, err := c.readCache(ctx, key, owner, repo); hit {
		return err
	}

	err := c.inner.CheckRepo(ctx, owner, repo)
	c.writeCache(ctx, key, err)
	return err
}

func (c *CachedRepoChecker) readCache(ctx context.Context, key, owner, repo string) (hit bool, err error) {
	cached, redisErr := c.redis.Get(ctx, key).Result()
	if redisErr != nil {
		return false, nil
	}
	log.Printf("github: cache hit for repo %s/%s (%s)", owner, repo, cached)
	if cached == "notfound" {
		return true, ErrNotFound
	}
	return true, nil
}

func (c *CachedRepoChecker) writeCache(ctx context.Context, key string, err error) {
	switch {
	case err == nil:
		c.redis.Set(ctx, key, "exists", repoCheckCacheTTL)
	case errors.Is(err, ErrNotFound):
		c.redis.Set(ctx, key, "notfound", repoCheckCacheTTL)
	}
}
