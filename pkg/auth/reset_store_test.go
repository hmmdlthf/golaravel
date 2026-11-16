package auth

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/pkg/cache"
	"github.com/golaravel/golaravel/pkg/database"
)

func TestPasswordResetTokensSurviveAcrossBrokers(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, backend, _ := cache.NewSQLStore(db, database.SQLite)
	if err := backend.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Two brokers = two instances (or one before and one after a restart).
	a := NewPasswordResetBrokerWithStore(CacheResetTokenStore(store))
	b := NewPasswordResetBrokerWithStore(CacheResetTokenStore(store))
	token, err := a.CreateToken("Ann@Example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !b.ValidateToken("ann@example.com ", token) {
		t.Fatal("token not valid on the second broker (email normalised)")
	}
	if b.ValidateToken("ann@example.com", token+"x") {
		t.Fatal("wrong token accepted")
	}
	var raw []byte
	_ = db.QueryRow("SELECT value FROM cache").Scan(&raw)
	if string(raw) == "" || strings.Contains(string(raw), token) || strings.Contains(string(raw), "ann@example.com") {
		t.Fatalf("store must hold only hashes: %s", raw)
	}
	b.DeleteToken("ann@example.com")
	if a.ValidateToken("ann@example.com", token) {
		t.Fatal("token valid after delete")
	}

	short := NewPasswordResetBrokerWithStore(CacheResetTokenStore(store), 20*time.Millisecond)
	tok, _ := short.CreateToken("bob@example.com")
	time.Sleep(40 * time.Millisecond)
	if short.ValidateToken("bob@example.com", tok) {
		t.Fatal("expired token accepted")
	}
}
