package jssolver

import (
	"container/list"
	"sync"

	"github.com/ammyy9908/goyt/extractor/youtube"
)

// DefaultCacheCapacity is the default maximum number of challenge transformations held in memory.
const DefaultCacheCapacity = 1000

// MaxCacheKeyLength limits the maximum length of a cache key to prevent memory abuse.
const MaxCacheKeyLength = 4096

// MaxCacheValueLength limits the maximum length of a cached transformed value.
const MaxCacheValueLength = youtube.MaxTransformedValueLength

type cacheEntry struct {
	key   string
	value string
}

// BoundedChallengeCache is an in-memory, thread-safe LRU cache for solved challenges.
// Sensitive challenge parameters are never written to disk or job manifests.
type BoundedChallengeCache struct {
	mu        sync.Mutex
	capacity  int
	items     map[string]*list.Element
	evictList *list.List
}

// NewBoundedChallengeCache initializes a BoundedChallengeCache with the given capacity.
func NewBoundedChallengeCache(capacity int) *BoundedChallengeCache {
	if capacity <= 0 {
		capacity = DefaultCacheCapacity
	}
	return &BoundedChallengeCache{
		capacity:  capacity,
		items:     make(map[string]*list.Element),
		evictList: list.New(),
	}
}

// Get retrieves a cached transformation value if present.
func (c *BoundedChallengeCache) Get(key string) (string, bool) {
	if c == nil || key == "" {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.evictList.MoveToFront(elem)
		return elem.Value.(*cacheEntry).value, true
	}
	return "", false
}

// Set stores a transformation in the cache, evicting the least recently used item if at capacity.
// Rejects keys or values exceeding maximum size bounds.
func (c *BoundedChallengeCache) Set(key, value string) {
	if c == nil || key == "" || value == "" {
		return
	}
	if len(key) > MaxCacheKeyLength || len(value) > MaxCacheValueLength {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.evictList.MoveToFront(elem)
		elem.Value.(*cacheEntry).value = value
		return
	}

	for c.evictList.Len() >= c.capacity {
		oldest := c.evictList.Back()
		if oldest == nil {
			break
		}
		c.evictList.Remove(oldest)
		delete(c.items, oldest.Value.(*cacheEntry).key)
	}

	elem := c.evictList.PushFront(&cacheEntry{key: key, value: value})
	c.items[key] = elem
}

// Len returns the current number of cached items.
func (c *BoundedChallengeCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evictList.Len()
}
