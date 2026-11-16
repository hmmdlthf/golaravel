package integration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/auth"
	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/session"
)

func payload(uid string) []byte {
	return []byte(fmt.Sprintf(`{"id":"x","attributes":{%q:%q}}`, session.AuthUserKey, uid))
}

func exerciseUserSessions(t *testing.T, d session.UserSessionDriver) {
	t.Helper()
	s := suffix()
	a1, a2, a3, b1 := "ua1"+s+s, "ua2"+s+s, "ua3"+s+s, "ub1"+s+s
	for id, uid := range map[string]string{a1: "7", a2: "7", a3: "7", b1: "8"} {
		if err := d.WriteUser(id, payload(uid), uid); err != nil {
			t.Fatalf("write user: %v", err)
		}
	}
	if err := d.DestroyUser("7", a1); err != nil {
		t.Fatalf("destroy user: %v", err)
	}
	for id, want := range map[string]bool{a1: true, a2: false, a3: false, b1: true} {
		_, err := d.Read(id)
		if got := err == nil; got != want {
			t.Fatalf("session %s exists=%v, want %v (%v)", id, got, want, err)
		}
	}
	if err := d.DestroyUser("7"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read(a1); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("a1 survived: %v", err)
	}
	if _, err := d.Read(b1); err != nil {
		t.Fatalf("other user's session removed: %v", err)
	}
}

func TestSQLSessionUsers(t *testing.T) {
	for _, b := range sqlBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			table := "it_usess_" + suffix()
			dropTables(t, b.db, table)
			// A table made by an older version (no user_id) is upgraded.
			if _, err := b.db.Exec(fmt.Sprintf("CREATE TABLE %s (id %s PRIMARY KEY, payload %s NOT NULL, last_activity BIGINT NOT NULL)",
				table, b.dialect.KeyType(), b.dialect.BlobType())); err != nil {
				t.Fatal(err)
			}
			d, err := session.NewSQLDriver(b.db, b.dialect, session.SQLDriverOptions{Table: table})
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := d.Migrate(context.Background()); err != nil {
					t.Fatalf("migrate %d: %v", i, err)
				}
			}
			exerciseUserSessions(t, d)
			var n int
			if err := b.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE user_id = '8'", table)).Scan(&n); err != nil || n != 1 {
				t.Fatalf("user_id column: n=%d err=%v", n, err)
			}
		})
	}
}

func TestRedisSessionUsers(t *testing.T) {
	c := redisClient(t)
	d := session.NewRedisDriver(c, session.RedisDriverOptions{Prefix: "it:" + suffix() + ":session:"})
	session.NewManager(session.Config{Driver: d, SecretKey: "k", Lifetime: time.Minute})
	exerciseUserSessions(t, d)
}

func TestSQLResetTokenStore(t *testing.T) {
	for _, b := range sqlBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			table := "it_resets_" + suffix()
			dropTables(t, b.db, table)
			store, err := auth.NewSQLResetTokenStore(b.db, b.dialect, table)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := store.Migrate(context.Background()); err != nil {
					t.Fatalf("migrate %d: %v", i, err)
				}
			}
			exerciseResetStore(t, b.db, b.dialect, table, store)
		})
	}
}

func exerciseResetStore(t *testing.T, db *sql.DB, dialect database.Dialect, table string, store *auth.SQLResetTokenStore) {
	t.Helper()
	a := auth.NewPasswordResetBrokerWithStore(store)
	other := auth.NewPasswordResetBrokerWithStore(store)
	tok, err := a.CreateToken("Ann@Example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !other.ValidateToken("ann@example.com", tok) {
		t.Fatal("token not valid on a second broker")
	}
	tok2, _ := a.CreateToken("ann@example.com")
	if other.ValidateToken("ann@example.com", tok) || !other.ValidateToken("ann@example.com", tok2) {
		t.Fatal("a new token must replace the old one")
	}
	other.DeleteToken("ann@example.com")
	if a.ValidateToken("ann@example.com", tok2) {
		t.Fatal("token valid after delete")
	}
	if err := store.Put("old@example.com", "h", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.PruneExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := store.Get("old@example.com"); ok {
		t.Fatal("expired token not pruned")
	}
}
