package inflight

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestWaitReturnsImmediatelyWhenIdle(t *testing.T) {
	var tr Tracker
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := tr.Wait(ctx); err != nil {
		t.Fatalf("Wait on an idle tracker: %v", err)
	}
}

func TestWaitBlocksUntilWorkFinishes(t *testing.T) {
	var tr Tracker
	done := tr.Begin()
	if tr.Active() != 1 {
		t.Fatalf("Active = %d, want 1", tr.Active())
	}

	released := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(released)
		done()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tr.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	select {
	case <-released:
	default:
		t.Fatal("Wait returned before the work finished")
	}
	if tr.Active() != 0 {
		t.Fatalf("Active = %d after the work finished", tr.Active())
	}
}

func TestWaitReportsATimeout(t *testing.T) {
	var tr Tracker
	defer tr.Begin()()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := tr.Wait(ctx); err == nil {
		t.Fatal("Wait reported success while work was still in flight")
	}
}

func TestDoneIsIdempotent(t *testing.T) {
	var tr Tracker
	done := tr.Begin()
	done()
	done()
	if tr.Active() != 0 {
		t.Fatalf("Active = %d, want 0", tr.Active())
	}
}

func TestConcurrentWorkAndWaiters(t *testing.T) {
	var tr Tracker
	const n = 50

	var started sync.WaitGroup
	started.Add(n)
	for i := 0; i < n; i++ {
		done := tr.Begin()
		go func() {
			started.Done()
			time.Sleep(time.Millisecond)
			done()
		}()
	}
	started.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var waiters sync.WaitGroup
	for i := 0; i < 3; i++ {
		waiters.Add(1)
		go func() {
			defer waiters.Done()
			if err := tr.Wait(ctx); err != nil {
				t.Errorf("Wait: %v", err)
			}
		}()
	}
	waiters.Wait()

	if tr.Active() != 0 {
		t.Fatalf("Active = %d, want 0", tr.Active())
	}
}

func TestDurableWaitsForAFlushThatBeganAfterTheWrite(t *testing.T) {
	t.Cleanup(func() { sealing.Store(false); sealedAt.Store(0); lastWrite.Store(0) })
	if !Durable(time.Now()) {
		t.Fatal("without encryption at rest a write is durable at once")
	}
	RequireSeal()
	if !Durable(time.Now()) {
		t.Fatal("nothing written since boot, yet not durable")
	}
	Wrote()
	write := time.Now()
	if Durable(write) {
		t.Fatal("durable before any flush")
	}
	Sealed(write.Add(-time.Second).UnixNano())
	if Durable(write) {
		t.Fatal("durable after a flush that began before the write")
	}
	Sealed(write.Add(time.Nanosecond).UnixNano())
	if !Durable(write) {
		t.Fatal("not durable after a flush that began after the write")
	}
}
