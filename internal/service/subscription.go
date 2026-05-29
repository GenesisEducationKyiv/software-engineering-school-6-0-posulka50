package service

import (
	"errors"
	"strings"
)

var (
	ErrInvalidEmail  = errors.New("invalid email format")
	ErrInvalidRepo   = errors.New("invalid repository format, expected owner/repo")
	ErrRepoNotFound  = errors.New("repository not found on GitHub")
	ErrAlreadyExists = errors.New("email already subscribed to this repository")
	ErrNotFound      = errors.New("not found")
	ErrRateLimit     = errors.New("GitHub API rate limit exceeded, try again later")
)

func isValidEmail(addr string) bool {
	parts := strings.Split(addr, "@")
	if len(parts) != 2 {
		return false
	}
	local, domain := parts[0], parts[1]
	return len(local) > 0 && strings.Contains(domain, ".") && len(domain) > 2
}
