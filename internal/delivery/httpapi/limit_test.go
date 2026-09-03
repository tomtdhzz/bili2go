package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLimiterAcquireUpToN(t *testing.T) {
	l := NewLimiter(2, 0) // no queueing
	r1, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r2, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Acquire(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("3rd acquire err = %v, want ErrBusy", err)
	}
	r1()
	r3, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("after release, acquire err = %v", err)
	}
	r2()
	r3()
}

func TestLimiterWaitsThenSucceeds(t *testing.T) {
	l := NewLimiter(1, 500*time.Millisecond)
	r1, _ := l.Acquire(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		r1()
	}()
	start := time.Now()
	r2, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire err = %v, want success after wait", err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatalf("acquire returned too fast; expected to wait for release")
	}
	r2()
}

func TestLimiterWaitTimeout(t *testing.T) {
	l := NewLimiter(1, 50*time.Millisecond)
	r1, _ := l.Acquire(context.Background())
	defer r1()
	start := time.Now()
	if _, err := l.Acquire(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatalf("returned before maxWait elapsed")
	}
}

func TestLimiterCtxCancel(t *testing.T) {
	l := NewLimiter(1, time.Second)
	r1, _ := l.Acquire(context.Background())
	defer r1()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if _, err := l.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestLimiterReleaseIdempotent(t *testing.T) {
	l := NewLimiter(1, 0)
	r1, _ := l.Acquire(context.Background())
	r1()
	r1() // second release must not free an extra slot or panic
	r2, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire err = %v", err)
	}
	if _, err := l.Acquire(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("slot count corrupted: 2nd acquire err = %v, want ErrBusy", err)
	}
	r2()
}
