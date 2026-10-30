package cache

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCache_BasicOperations(t *testing.T) {
	s := NewMemoryStore()

	// 1. Put and Get
	_ = s.Put("site", "GoLaravel", 1*time.Hour)
	if s.GetString("site") != "GoLaravel" {
		t.Fatalf("expected GoLaravel, got %s", s.GetString("site"))
	}

	// 2. Add (only if missing)
	added, _ := s.Add("site", "NewName", 1*time.Hour)
	if added {
		t.Fatal("expected Add to fail for existing key")
	}
	added2, _ := s.Add("new_key", "value", 1*time.Hour)
	if !added2 {
		t.Fatal("expected Add to succeed for new key")
	}

	// 3. Increment & Decrement
	val, _ := s.Increment("counter", 5)
	if val != 5 {
		t.Fatalf("expected 5, got %d", val)
	}
	val, _ = s.Increment("counter")
	if val != 6 {
		t.Fatalf("expected 6, got %d", val)
	}
	val, _ = s.Decrement("counter", 2)
	if val != 4 {
		t.Fatalf("expected 4, got %d", val)
	}

	// 4. Remember
	calls := 0
	compute := func() (any, error) {
		calls++
		return "computed_result", nil
	}
	res1, _ := s.Remember("cached_calc", 1*time.Hour, compute)
	res2, _ := s.Remember("cached_calc", 1*time.Hour, compute)
	if res1 != "computed_result" || res2 != "computed_result" || calls != 1 {
		t.Fatalf("expected compute called once, got %d calls", calls)
	}

	// 5. Pull (get & delete)
	pulled := s.Pull("site")
	if pulled != "GoLaravel" || s.GetString("site") != "" {
		t.Fatal("expected Pull to return value and delete key")
	}

	// 6. Forget and Flush
	_ = s.Put("temp", "val", 1*time.Hour)
	_ = s.Forget("temp")
	if _, ok := s.Get("temp"); ok {
		t.Fatal("expected temp to be forgotten")
	}

	_ = s.Put("a", "1", 1*time.Hour)
	_ = s.Put("b", "2", 1*time.Hour)
	_ = s.Flush()
	if _, ok := s.Get("a"); ok {
		t.Fatal("expected empty store after Flush")
	}
}

func TestCache_TaggedCache(t *testing.T) {
	s := NewMemoryStore()

	// 1. Put with multiple tags
	tagged := s.Tags("users", "billing")
	_ = tagged.Put("user_42", map[string]string{"name": "Alice"}, 1*time.Hour)

	// 2. Read back using same tags
	val, found := tagged.Get("user_42")
	if !found || val == nil {
		t.Fatal("expected to find tagged item user_42")
	}

	// 3. Read back with reversed tag order (order invariance)
	reversedTagged := s.Tags("billing", "users")
	val2, found2 := reversedTagged.Get("user_42")
	if !found2 || val2 == nil {
		t.Fatal("expected to find tagged item with reversed tag order")
	}

	// 4. Put item under different tag
	s.Tags("reports").Put("monthly_q1", "Q1 Financials", 1*time.Hour)

	// 5. Flush "users" tag -> must invalidate user_42, but NOT reports
	_ = s.Tags("users").Flush()

	_, foundAfterFlush := tagged.Get("user_42")
	if foundAfterFlush {
		t.Fatal("expected user_42 to be invalidated after flushing 'users' tag")
	}

	reportVal, reportFound := s.Tags("reports").Get("monthly_q1")
	if !reportFound || reportVal != "Q1 Financials" {
		t.Fatal("expected reports tag to remain unaffected by flushing 'users'")
	}

	// 6. Tagged Remember
	calcCalls := 0
	calcFn := func() (any, error) {
		calcCalls++
		return "expensive_report", nil
	}
	r1, _ := s.Tags("analytics").Remember("rep_1", 1*time.Hour, calcFn)
	r2, _ := s.Tags("analytics").Remember("rep_1", 1*time.Hour, calcFn)
	if r1 != "expensive_report" || r2 != "expensive_report" || calcCalls != 1 {
		t.Fatalf("expected Remember to compute only once, got %d calls", calcCalls)
	}

	// Flush analytics tag
	_ = s.Tags("analytics").Flush()
	r3, _ := s.Tags("analytics").Remember("rep_1", 1*time.Hour, calcFn)
	if calcCalls != 2 {
		t.Fatalf("expected Remember to recompute after tag flush, got %d calls", calcCalls)
	}
	_ = r3
}

func TestCache_AtomicLock(t *testing.T) {
	s := NewMemoryStore()

	lock1 := s.Lock("deploy-job", 1*time.Second)
	lock2 := s.Lock("deploy-job", 1*time.Second)

	// 1. Acquire lock 1
	acquired, err := lock1.Acquire()
	if err != nil || !acquired {
		t.Fatalf("expected lock1 acquire to succeed")
	}

	// 2. Lock 2 must fail to acquire while lock 1 is active
	acquired2, err := lock2.Acquire()
	if err != nil || acquired2 {
		t.Fatalf("expected lock2 acquire to fail while locked")
	}

	// 3. Release lock 1
	err = lock1.Release()
	if err != nil {
		t.Fatalf("failed releasing lock1: %v", err)
	}

	// 4. Lock 2 can now acquire
	acquired2b, err := lock2.Acquire()
	if err != nil || !acquired2b {
		t.Fatalf("expected lock2 to acquire after lock1 release")
	}

	// 5. Release by wrong owner must fail
	err = lock1.Release() // lock1 is not the current owner (lock2 is)
	if err != ErrLockNotOwned {
		t.Fatalf("expected ErrLockNotOwned, got %v", err)
	}
	_ = lock2.Release()

	// 6. Lock.Get(fn) callback execution with auto-release
	executed := false
	err = s.Lock("auto-release-task", 1*time.Second).Get(func() error {
		executed = true
		return nil
	})
	if err != nil || !executed {
		t.Fatalf("expected Lock.Get to execute callback successfully")
	}

	// Should be immediately re-acquirable because Get() auto-released
	reacquired, _ := s.Lock("auto-release-task", 1*time.Second).Acquire()
	if !reacquired {
		t.Fatal("expected lock to be released after Lock.Get")
	}

	// 7. Concurrent Goroutines with Lock.Block()
	var wg sync.WaitGroup
	sharedCounter := 0
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := s.Lock("shared-counter-lock", 2*time.Second)
			_ = l.Block(3*time.Second, func() error {
				current := sharedCounter
				time.Sleep(5 * time.Millisecond)
				sharedCounter = current + 1
				return nil
			})
		}()
	}

	wg.Wait()
	if sharedCounter != numGoroutines {
		t.Fatalf("race condition detected: expected counter=%d, got %d", numGoroutines, sharedCounter)
	}

	// 8. Error in callback propagated
	customErr := errors.New("something went wrong")
	err = s.Lock("err-task", 1*time.Second).Get(func() error {
		return customErr
	})
	if err != customErr {
		t.Fatalf("expected custom error returned, got: %v", err)
	}
}
