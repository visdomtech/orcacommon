// Package lazy provides a generic thread-safe lazy initializer.
//
// Lazy[T] ensures that an expensive initialization function runs exactly once,
// even under concurrent access. It uses atomic.Pointer for lock-free reads
// after successful initialization and singleflight.Group to deduplicate
// concurrent initialization attempts.
//
// If initialization fails, the next Get call retries. There is no caching
// of failed attempts — every failure triggers a retry on the next access.
package lazy

import (
	"context"
	"sync/atomic"

	"golang.org/x/sync/singleflight"
)

// Lazy is a thread-safe lazy initializer for type T.
// The zero value is not usable; use New to create instances.
type Lazy[T any] struct {
	val   atomic.Pointer[T]
	group singleflight.Group
	init  func(ctx context.Context) (*T, error)
}

// New creates a Lazy that will call init on first Get.
// The init function should return a fully initialized T or an error.
// If init returns an error, it will be retried on the next Get call.
func New[T any](init func(ctx context.Context) (*T, error)) *Lazy[T] {
	return &Lazy[T]{init: init}
}

// Get returns the initialized value, calling init on first access.
// Concurrent callers are deduplicated via singleflight — only one init
// runs at a time; others wait for the result.
func (l *Lazy[T]) Get(ctx context.Context) (*T, error) {
	// Fast path: atomic read once successfully initialized (lock-free).
	if v := l.val.Load(); v != nil {
		return v, nil
	}

	// Slow path: deduplicate concurrent in-flight callers.
	result, err, _ := l.group.Do("init", func() (any, error) {
		// Double-check after entering singleflight.
		if v := l.val.Load(); v != nil {
			return v, nil
		}
		v, err := l.init(ctx)
		if err != nil {
			return nil, err
		}
		l.val.Store(v)
		return v, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*T), nil
}

// Loaded reports whether the value has been successfully initialized.
// It does not trigger initialization.
func (l *Lazy[T]) Loaded() bool {
	return l.val.Load() != nil
}
