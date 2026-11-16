package container

import (
	"sync"
	"testing"
)

type DatabaseService struct {
	DSN string
}

type UserRepository struct {
	DB *DatabaseService
}

func TestContainer_Instance(t *testing.T) {
	c := New()
	db := &DatabaseService{DSN: "postgres://localhost/test"}
	Instance(c, db)

	resolved, err := Resolve[*DatabaseService](c)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resolved.DSN != db.DSN {
		t.Fatalf("expected %s, got %s", db.DSN, resolved.DSN)
	}
}

func TestContainer_Singleton(t *testing.T) {
	c := New()
	callCount := 0

	Singleton(c, func(c *Container) (*DatabaseService, error) {
		callCount++
		return &DatabaseService{DSN: "singleton-db"}, nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db := MustResolve[*DatabaseService](c)
			if db.DSN != "singleton-db" {
				t.Errorf("unexpected DSN: %s", db.DSN)
			}
		}()
	}
	wg.Wait()

	if callCount != 1 {
		t.Fatalf("expected singleton factory to be called exactly once, called %d times", callCount)
	}
}

func TestContainer_Transient(t *testing.T) {
	c := New()
	counter := 0

	Bind(c, func(c *Container) (*DatabaseService, error) {
		counter++
		return &DatabaseService{DSN: "transient"}, nil
	})

	Resolve[*DatabaseService](c)
	Resolve[*DatabaseService](c)

	if counter != 2 {
		t.Fatalf("expected transient to be called twice, called %d times", counter)
	}
}

func TestContainer_Scoped(t *testing.T) {
	root := New()
	db := &DatabaseService{DSN: "root-db"}
	Instance(root, db)

	scoped := root.Scoped()
	resolvedDB := MustResolve[*DatabaseService](scoped)
	if resolvedDB != db {
		t.Fatalf("expected scoped container to inherit root dependency")
	}

	repo := &UserRepository{DB: db}
	Instance(scoped, repo)

	if !Has[*UserRepository](scoped) {
		t.Fatalf("expected scoped container to have UserRepository")
	}
	if Has[*UserRepository](root) {
		t.Fatalf("root container must not have scoped UserRepository")
	}
}

func TestContainer_NotFound(t *testing.T) {
	c := New()
	_, err := Resolve[*DatabaseService](c)
	if err == nil {
		t.Fatal("expected error for unregistered dependency")
	}
}

func BenchmarkContainer_Resolve_Singleton(b *testing.B) {
	c := New()
	db := &DatabaseService{DSN: "bench"}
	Instance(c, db)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = MustResolve[*DatabaseService](c)
	}
}

func TestSingletonWithNilValueIsBuiltOnce(t *testing.T) {
	c := New()
	calls := 0
	Singleton[*int](c, func(*Container) (*int, error) { calls++; return nil, nil })
	for i := 0; i < 3; i++ {
		if v, err := Resolve[*int](c); err != nil || v != nil {
			t.Fatalf("resolve: %v %v", v, err)
		}
	}
	if calls != 1 {
		t.Fatalf("factory ran %d times, want 1", calls)
	}
}
