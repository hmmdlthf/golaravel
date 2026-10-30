# Database Migrations & Seeding (via Atlas)

GoLaravel pairs **Ent ORM** with **Atlas** (from Ariga / Linux Foundation) to provide compile-time type-safety, versioned deterministic migrations, schema diffing, and hierarchical database seeders.

---

## 1. Overview & Architecture

- **Ent ORM (`app/schema/`)**: Pure Go schema definitions with zero runtime reflection.
- **Atlas Migrations (`database/migrations/`)**: Deterministic `.sql` migration files versioned by timestamp (`YYYYMMDDHHMMSS_<name>.sql`) and validated by cryptographic hash (`atlas.sum`).
- **Seeding Engine (`database/seeders/`)**: Type-safe hierarchical seeders executed sequentially with sub-millisecond execution times and timing logs.

---

## 2. Atlas Configuration (`atlas.hcl`)

GoLaravel projects define their Atlas migration configuration in `atlas.hcl`:

```hcl
env "local" {
  src = "ent://app/schema"
  dev = "sqlite://file?mode=memory&cache=shared"
  migration {
    dir = "file://database/migrations"
  }
}
```

For PostgreSQL or MySQL:

```hcl
env "local" {
  src = "ent://app/schema"
  dev = "docker://postgres/16/dev?search_path=public"
  migration {
    dir = "file://database/migrations"
  }
}
```

---

## 3. Migration Commands

### A. Generating Schema Diffs (`artisan migrate:diff`)

When you add or modify fields or edges in your Ent schemas (`app/schema/*.go`), compute the diff against the database state and generate an automatic versioned SQL migration:

```bash
artisan migrate:diff add_user_avatar
```

This inspects the Ent schema, runs the diff calculation against the dev database, and writes `database/migrations/<timestamp>_add_user_avatar.sql` and updates `atlas.sum`.

### B. Applying Migrations (`artisan migrate`)

To execute all pending migration files against your target database:

```bash
# Uses default DB_URL from environment or sqlite://database.sqlite
artisan migrate

# Specify database URL explicitly:
artisan migrate --url="postgres://user:pass@localhost:5432/myapp?sslmode=disable"

# Dry run (inspect SQL without applying):
artisan migrate --dry-run
```

### C. Migration Status (`artisan migrate:status`)

View pending and executed migrations:

```bash
artisan migrate:status
```

Output:
```text
Migration Status: OK
  -- Current Version: 20261030160000
  -- Next Version:    Already at latest version
  -- Executed Files:  1
  -- Pending Files:   0
```

### D. Rolling Back Migrations (`artisan migrate:rollback`)

Revert the last migration batch:

```bash
artisan migrate:rollback
```

### E. Creating Custom Migrations (`artisan make:migration`)

For custom raw SQL operations, stored procedures, or triggers:

```bash
artisan make:migration create_fulltext_indices
```

This creates an empty timestamped `.sql` file in `database/migrations/` and updates `atlas.sum`.

---

## 4. Database Seeding Engine

### A. Creating Seeders (`artisan make:seeder`)

Generate a new seeder class in `database/seeders/`:

```bash
artisan make:seeder UserSeeder
```

Generated file (`database/seeders/user_seeder.go`):

```go
package seeders

import (
	"context"
	"log/slog"

	"github.com/myproject/app"
	"github.com/golaravel/golaravel/pkg/hash"
)

type UserSeeder struct{}

func NewUserSeeder() *UserSeeder {
	return &UserSeeder{}
}

func (s *UserSeeder) Seed(ctx context.Context, client any) error {
	c := client.(*app.Client)
	slog.Info("Seeding initial users...")

	hashedPassword, _ := hash.Make("password123")
	_, err := c.User.Create().
		SetName("Administrator").
		SetEmail("admin@example.com").
		SetPassword(hashedPassword).
		SetRole("admin").
		Save(ctx)
	return err
}
```

### B. Hierarchical Orchestration (`DatabaseSeeder`)

Organize seeders sequentially inside `database/seeders/database_seeder.go`:

```go
package seeders

import (
	"context"
	"github.com/golaravel/golaravel/pkg/database"
)

type DatabaseSeeder struct{}

func NewDatabaseSeeder() *DatabaseSeeder {
	return &DatabaseSeeder{}
}

func (s *DatabaseSeeder) Seed(ctx context.Context, client any) error {
	runner := database.NewRunner(client)
	return runner.Call(ctx,
		NewUserSeeder(),
		NewInvoiceSeeder(),
		NewSettingSeeder(),
	)
}
```

### C. Running Seeders (`artisan db:seed`)

Execute the seeders against the database:

```bash
artisan db:seed
```

Output:
```text
==> Executing database seeders...
  Seeding: DatabaseSeeder...
  Seeding: UserSeeder...
  Seeded:  UserSeeder (46.896ms)
  Seeding: InvoiceSeeder...
  Seeded:  InvoiceSeeder (181µs)
  Seeded:  DatabaseSeeder (47.138ms)
INFO Database seeded successfully.
```

### D. In-App Programmatic Seeding

You can also run seeders programmatically in tests or during application bootstrap:

```go
runner := database.NewRunner(client)
if err := runner.Call(ctx, seeders.NewDatabaseSeeder()); err != nil {
    log.Fatalf("seeding failed: %v", err)
}
```
