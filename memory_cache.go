package main

import (
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/semihalev/log"
)

// KeyNotFound type
type KeyNotFound struct {
	key string
}

// Error formats an error for the KeyNotFound type
func (e KeyNotFound) Error() string {
	return e.key + " " + "not found"
}

// KeyExpired type
type KeyExpired struct {
	Key string
}

// Error formats an error for the KeyExpired type
func (e KeyExpired) Error() string {
	return e.Key + " " + "expired"
}

// CacheIsFull type
type CacheIsFull struct {
}

// Error formats an error for the CacheIsFull type
func (e CacheIsFull) Error() string {
	return "Cache is Full"
}

// SerializerError type
type SerializerError struct {
}

// Error formats an error for the SerializerError type
func (e SerializerError) Error() string {
	return "Serializer error"
}

// Mesg represents a cache entry
type Mesg struct {
	Msg            *dns.Msg
	LastUpdateTime time.Time
}

// Cache interface
type Cache interface {
	Get(key string) (Msg *dns.Msg, err error)
	Set(key string, Msg *dns.Msg) error
	Exists(key string) bool
	Remove(key string)
	Length() int
}

// MemoryCache type
type MemoryCache struct {
	mu sync.RWMutex

	Backend  map[string]*Mesg
	Maxcount int
}

// NewMemoryCache return new cache
func NewMemoryCache(maxcount int) *MemoryCache {
	c := &MemoryCache{
		Backend:  make(map[string]*Mesg, maxcount),
		Maxcount: maxcount,
	}

	go c.run()

	return c
}

// Get returns the entry for a key or an error
func (c *MemoryCache) Get(key string) (*dns.Msg, error) {
	key = strings.ToLower(key)

	c.mu.RLock()
	mesg, ok := c.Backend[key]
	c.mu.RUnlock()

	if !ok {
		log.Debug("Cache miss", "key", key)
		return nil, KeyNotFound{key}
	}

	if mesg.Msg == nil {
		c.Remove(key)
		return nil, KeyNotFound{key}
	}

	//Truncate time to the second, so that subsecond queries won't keep moving
	//forward the last update time without touching the TTL
	now := WallClock.Now().Truncate(time.Second)
	elapsed := uint32(now.Sub(mesg.LastUpdateTime).Seconds())
	mesg.LastUpdateTime = now

	for _, answer := range mesg.Msg.Answer {
		if elapsed > answer.Header().Ttl {
			log.Debug("Cache expired", "key", key)
			c.Remove(key)
			return nil, KeyExpired{key}
		}
		answer.Header().Ttl -= elapsed
	}

	return mesg.Msg, nil
}

// Set sets a keys value to a Mesg
func (c *MemoryCache) Set(key string, msg *dns.Msg) error {
	key = strings.ToLower(key)

	if c.Full() && !c.Exists(key) {
		return CacheIsFull{}
	}

	mesg := Mesg{msg, WallClock.Now().Truncate(time.Second)}
	c.mu.Lock()
	c.Backend[key] = &mesg
	c.mu.Unlock()

	return nil
}

// Remove removes an entry from the cache
func (c *MemoryCache) Remove(key string) {
	key = strings.ToLower(key)

	c.mu.Lock()
	delete(c.Backend, key)
	c.mu.Unlock()
}

// Exists returns whether or not a key exists in the cache
func (c *MemoryCache) Exists(key string) bool {
	key = strings.ToLower(key)

	c.mu.RLock()
	_, ok := c.Backend[key]
	c.mu.RUnlock()
	return ok
}

// Length returns the caches length
func (c *MemoryCache) Length() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.Backend)
}

// Full returns whether or not the cache is full
func (c *MemoryCache) Full() bool {
	if c.Maxcount == 0 {
		return false
	}
	return c.Length() >= c.Maxcount
}

func (c *MemoryCache) run() {
	ticker := time.NewTicker(time.Hour)

	for range ticker.C {
		c.mu.Lock()
		for key, mesg := range c.Backend {
			if mesg.Msg == nil {
				delete(c.Backend, key)
			}

			now := WallClock.Now().Truncate(time.Second)
			elapsed := uint32(now.Sub(mesg.LastUpdateTime).Seconds())

			for _, answer := range mesg.Msg.Answer {
				if elapsed > answer.Header().Ttl {
					delete(c.Backend, key)
					break
				}
			}
		}
		c.mu.Unlock()
	}
}
