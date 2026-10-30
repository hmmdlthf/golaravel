package artisan

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/golaravel/golaravel/pkg/database"
)

func makeMigration(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing migration name. Usage: artisan make:migration <name>")
	}

	name := args[0]
	migrator := database.NewMigrator(database.MigratorConfig{
		Dir: filepath.Join("database", "migrations"),
	})

	filePath, err := migrator.Create(context.Background(), name)
	if err != nil {
		return err
	}

	fmt.Printf("\033[32mINFO\033[0m Migration [%s] created successfully.\n", filePath)
	return nil
}

func makeSeeder(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing seeder name. Usage: artisan make:seeder <Name>")
	}

	rawName := args[0]
	name := strings.Title(rawName)
	if !strings.HasSuffix(name, "Seeder") {
		name += "Seeder"
	}

	filename := strings.ToLower(strings.TrimSuffix(name, "Seeder")) + "_seeder.go"
	dir := filepath.Join("database", "seeders")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, filename)

	content := fmt.Sprintf(`package seeders

import (
	"context"
	"log/slog"
)

// %s populates database records.
type %s struct{}

// New%s creates an instance of %s.
func New%s() *%s {
	return &%s{}
}

// Seed populates the database using the provided ORM client.
func (s *%s) Seed(ctx context.Context, client any) error {
	slog.Info("Running seeder", "seeder", "%s")
	// Example with Ent ORM client:
	// c, ok := client.(*ent.Client)
	// if !ok { ... }
	// _, err := c.User.Create().SetName("...").Save(ctx)
	return nil
}
`, name, name, name, name, name, name, name, name, name)

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}

	fmt.Printf("\033[32mINFO\033[0m Seeder [%s] created successfully.\n", targetPath)
	return nil
}

func makeFactory(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing factory name. Usage: artisan make:factory <Name>")
	}

	rawName := args[0]
	name := strings.Title(rawName)
	if strings.HasSuffix(name, "Factory") {
		name = strings.TrimSuffix(name, "Factory")
	}

	filename := strings.ToLower(name) + "_factory.go"
	dir := filepath.Join("database", "factories")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, filename)

	content := fmt.Sprintf(`package factories

import (
	"context"
	"fmt"

	"github.com/golaravel/golaravel/pkg/database/factory"
)

// %sDef defines the generated attributes for %s.
type %sDef struct {
	Name  string
	Email string
}

// %sFactory creates a model factory for %s.
func %sFactory() *factory.Factory[%sDef] {
	return factory.New[%sDef]().
		Definition(func(f *factory.Faker) %sDef {
			return %sDef{
				Name:  f.Name(),
				Email: f.Email(),
			}
		}).
		Creator(func(ctx context.Context, client any, def %sDef) (any, error) {
			// Example Ent client creator:
			// c, ok := client.(*app.Client)
			// if !ok {
			//     return nil, fmt.Errorf("expected *app.Client, got %%T", client)
			// }
			// return c.%s.Create().
			//     SetName(def.Name).
			//     SetEmail(def.Email).
			//     Save(ctx)
			return &def, nil
		})
}
`, name, name, name, name, name, name, name, name, name, name, name, name)

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}

	fmt.Printf("\033[32mINFO\033[0m Factory [%s] created successfully.\n", targetPath)
	return nil
}

func runMigrate(args []string) error {
	ctx := context.Background()
	cfg := resolveMigratorConfig(args)

	migrator := database.NewMigrator(cfg)

	dryRun := hasFlag(args, "--dry-run")
	fmt.Printf("\033[34m==>\033[0m Running Atlas database migrations against \033[1;37m%s\033[0m...\n", cfg.URL)

	out, err := migrator.Apply(ctx, dryRun)
	if err != nil {
		return err
	}

	if out != "" {
		fmt.Println(out)
	} else {
		fmt.Println("\033[32mINFO\033[0m Database migrations applied successfully.")
	}
	return nil
}

func runMigrateDiff(args []string) error {
	ctx := context.Background()
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return fmt.Errorf("missing migration diff name. Usage: artisan migrate:diff <name>")
	}

	name := args[0]
	cfg := resolveMigratorConfig(args[1:])
	migrator := database.NewMigrator(cfg)

	fmt.Printf("\033[34m==>\033[0m Computing schema diff against Ent schema \033[1;37m%s\033[0m...\n", cfg.Schema)

	out, err := migrator.Diff(ctx, name)
	if err != nil {
		return err
	}

	fmt.Printf("\033[32mINFO\033[0m %s\n", out)
	return nil
}

func runMigrateStatus(args []string) error {
	ctx := context.Background()
	cfg := resolveMigratorConfig(args)
	migrator := database.NewMigrator(cfg)

	out, err := migrator.Status(ctx)
	if err != nil {
		return err
	}

	fmt.Println(out)
	return nil
}

func runMigrateRollback(args []string) error {
	ctx := context.Background()
	cfg := resolveMigratorConfig(args)
	migrator := database.NewMigrator(cfg)

	fmt.Printf("\033[34m==>\033[0m Rolling back migration on \033[1;37m%s\033[0m...\n", cfg.URL)

	out, err := migrator.Rollback(ctx, 1)
	if err != nil {
		return err
	}

	if out != "" {
		fmt.Println(out)
	} else {
		fmt.Println("\033[32mINFO\033[0m Rollback completed successfully.")
	}
	return nil
}

func runDbSeed(args []string) error {
	// Check if database/seeders/cmd or database/seeders runner exists
	var targetPkg string
	if _, err := os.Stat("database/seeders/cmd/main.go"); err == nil {
		targetPkg = "./database/seeders/cmd"
	} else if _, err := os.Stat("cmd/seed/main.go"); err == nil {
		targetPkg = "./cmd/seed"
	} else if _, err := os.Stat("database/seeders"); err == nil {
		targetPkg = "./database/seeders"
	} else {
		return fmt.Errorf("no seeders found in database/seeders/ or cmd/seed/")
	}

	fmt.Println("\033[34m==>\033[0m Executing database seeders...")
	cmdArgs := []string{"run", targetPkg}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command("go", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("seeding failed: %w", err)
	}

	fmt.Println("\033[32mINFO\033[0m Database seeded successfully.")
	return nil
}

func resolveMigratorConfig(args []string) database.MigratorConfig {
	cfg := database.DefaultMigratorConfig()

	// Check if database/migrations directory exists locally
	if _, err := os.Stat("database/migrations"); err == nil {
		cfg.Dir = "database/migrations"
	} else if _, err := os.Stat("example/database/migrations"); err == nil {
		cfg.Dir = "example/database/migrations"
	}

	// Schema detection
	if _, err := os.Stat("app/schema"); err == nil {
		cfg.Schema = "ent://app/schema"
	} else if _, err := os.Stat("example/app/schema"); err == nil {
		cfg.Schema = "ent://example/app/schema"
	}

	// Database URL override
	if envURL := os.Getenv("DATABASE_URL"); envURL != "" {
		cfg.URL = envURL
	} else if envURL := os.Getenv("DB_URL"); envURL != "" {
		cfg.URL = envURL
	}

	// CLI flags
	for _, arg := range args {
		if strings.HasPrefix(arg, "--url=") {
			cfg.URL = strings.TrimPrefix(arg, "--url=")
		} else if strings.HasPrefix(arg, "--dir=") {
			cfg.Dir = strings.TrimPrefix(arg, "--dir=")
		} else if strings.HasPrefix(arg, "--to=") {
			cfg.Schema = strings.TrimPrefix(arg, "--to=")
		} else if strings.HasPrefix(arg, "--dev-url=") {
			cfg.DevURL = strings.TrimPrefix(arg, "--dev-url=")
		}
	}

	return cfg
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
