package lazy

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGet_SingleInit(t *testing.T) {
	var calls atomic.Int32
	lz := New(func(ctx context.Context) (*string, error) {
		calls.Add(1)
		s := "hello"
		return &s, nil
	})

	v, err := lz.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if *v != "hello" {
		t.Fatalf("got %q, want %q", *v, "hello")
	}
	if calls.Load() != 1 {
		t.Fatalf("init called %d times, want 1", calls.Load())
	}

	// Second call — fast path, no re-init.
	v2, err := lz.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != v2 {
		t.Fatal("second Get returned different pointer")
	}
	if calls.Load() != 1 {
		t.Fatalf("init called %d times after second Get, want 1", calls.Load())
	}
}

func TestGet_ConcurrentDedup(t *testing.T) {
	var calls atomic.Int32
	lz := New(func(ctx context.Context) (*int, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		v := 42
		return &v, nil
	})

	var wg sync.WaitGroup
	const n = 50
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := lz.Get(context.Background())
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if *v != 42 {
				t.Errorf("got %d, want 42", *v)
			}
		}()
	}
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("init called %d times under %d concurrent callers, want 1", calls.Load(), n)
	}
}

func TestGet_RetryOnFailure(t *testing.T) {
	var attempts atomic.Int32
	transientErr := errors.New("temporary failure")

	lz := New(func(ctx context.Context) (*string, error) {
		n := attempts.Add(1)
		if n < 3 {
			return nil, transientErr
		}
		s := "recovered"
		return &s, nil
	})

	// Attempts 1-2 fail.
	for i := 0; i < 2; i++ {
		_, err := lz.Get(context.Background())
		if !errors.Is(err, transientErr) {
			t.Fatalf("attempt %d: got %v, want %v", i+1, err, transientErr)
		}
	}

	// Attempt 3 succeeds.
	v, err := lz.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if *v != "recovered" {
		t.Fatalf("got %q, want %q", *v, "recovered")
	}

	// Subsequent calls use cached value.
	v2, err := lz.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != v2 {
		t.Fatal("cached Get returned different pointer")
	}
}

func TestGet_ConcurrentAfterFailure(t *testing.T) {
	var attempts atomic.Int32
	transientErr := errors.New("temporary")

	lz := New(func(ctx context.Context) (*int, error) {
		n := attempts.Add(1)
		if n == 1 {
			return nil, transientErr
		}
		v := int(n)
		return &v, nil
	})

	// First attempt fails.
	_, err := lz.Get(context.Background())
	if err == nil {
		t.Fatal("expected error on first attempt")
	}

	// Concurrent retry succeeds.
	var wg sync.WaitGroup
	const n = 20
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := lz.Get(context.Background())
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if *v < 2 {
				t.Errorf("got %d, expected >= 2", *v)
			}
		}()
	}
	wg.Wait()
}

func TestGet_RaceDetector(t *testing.T) {
	lz := New(func(ctx context.Context) (*string, error) {
		s := "race-test"
		return &s, nil
	})

	var wg sync.WaitGroup
	const n = 100
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = lz.Get(context.Background())
		}()
	}
	wg.Wait()
}

func TestLoaded(t *testing.T) {
	lz := New(func(ctx context.Context) (*string, error) {
		s := "loaded"
		return &s, nil
	})

	if lz.Loaded() {
		t.Fatal("Loaded() true before Get")
	}

	_, err := lz.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if !lz.Loaded() {
		t.Fatal("Loaded() false after successful Get")
	}
}

func TestLoaded_AfterFailure(t *testing.T) {
	lz := New(func(ctx context.Context) (*string, error) {
		return nil, errors.New("fail")
	})

	_, _ = lz.Get(context.Background())

	if lz.Loaded() {
		t.Fatal("Loaded() true after failed Get")
	}
}
