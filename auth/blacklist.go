package auth

import "sync"

// Blacklist holds revoked tokens. It is read by every authenticated request
// and written on logout, so access is guarded: concurrent writes to a plain
// map are a fatal runtime error in Go and would take the whole server down.
type Blacklist struct {
	mu        sync.RWMutex
	blacklist map[string]bool
}

func NewBlacklist() *Blacklist {
	return &Blacklist{
		blacklist: make(map[string]bool),
	}
}

func (b *Blacklist) Add(token string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.blacklist[token] = true
}

func (b *Blacklist) IsBlacklisted(token string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.blacklist[token]
	return ok
}
