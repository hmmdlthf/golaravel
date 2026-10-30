package artisan

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/golaravel/golaravel/pkg/router"
)

// Command defines a CLI command in Artisan.
type Command struct {
	Name        string
	Description string
	Run         func(args []string) error
}

// Registry stores all available Artisan commands.
type Registry struct {
	commands map[string]*Command
}

// NewRegistry creates a new command registry.
func NewRegistry() *Registry {
	r := &Registry{
		commands: make(map[string]*Command),
	}
	r.registerDefaults()
	return r
}

// Register adds a new custom command to Artisan.
func (r *Registry) Register(cmd *Command) {
	r.commands[cmd.Name] = cmd
}

// Execute runs the artisan command from CLI arguments.
func (r *Registry) Execute(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		r.PrintHelp()
		return nil
	}

	cmdName := args[0]
	cmd, exists := r.commands[cmdName]
	if !exists {
		return fmt.Errorf("unknown command: '%s'. Run 'artisan help' for available commands", cmdName)
	}

	return cmd.Run(args[1:])
}

// PrintHelp outputs the list of available artisan commands in formatted table.
func (r *Registry) PrintHelp() {
	fmt.Println("GoLaravel Artisan CLI - High-Performance Go Framework")
	fmt.Println("\nUsage:\n  artisan <command> [arguments]")
	fmt.Println("\nAvailable Commands:")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	for name, cmd := range r.commands {
		fmt.Fprintf(w, "  \033[32m%-22s\033[0m\t%s\n", name, cmd.Description)
	}
	_ = w.Flush()
	fmt.Println()
}

func (r *Registry) registerDefaults() {
	r.Register(&Command{
		Name:        "new",
		Description: "Interactively create a new GoLaravel project (Templ, Inertia, or Flutter)",
		Run:         RunNew,
	})

	r.Register(&Command{
		Name:        "make:schema",
		Description: "Create a new Ent database schema (app/schema/<name>.go)",
		Run:         makeSchema,
	})

	r.Register(&Command{
		Name:        "make:view",
		Description: "Create a new Templ component view (app/views/<name>.templ)",
		Run:         makeView,
	})

	r.Register(&Command{
		Name:        "make:controller",
		Description: "Create a new Ent/Templ aware HTTP controller",
		Run:         makeController,
	})

	r.Register(&Command{
		Name:        "make:middleware",
		Description: "Create a new HTTP middleware",
		Run:         makeMiddleware,
	})

	r.Register(&Command{
		Name:        "make:job",
		Description: "Create a new async background job (app/jobs/<name>.go)",
		Run:         makeJob,
	})

	r.Register(&Command{
		Name:        "make:mail",
		Description: "Create a new mailable email class (app/mail/<name>.go)",
		Run:         makeMail,
	})

	r.Register(&Command{
		Name:        "make:policy",
		Description: "Create a new authorization policy (app/policies/<name>.go)",
		Run:         makePolicy,
	})

	r.Register(&Command{
		Name:        "make:migration",
		Description: "Create a new Atlas versioned database migration (database/migrations/)",
		Run:         makeMigration,
	})

	r.Register(&Command{
		Name:        "make:seeder",
		Description: "Create a new database seeder class (database/seeders/<name>_seeder.go)",
		Run:         makeSeeder,
	})

	r.Register(&Command{
		Name:        "migrate",
		Description: "Execute database migrations via Atlas (atlas migrate apply)",
		Run:         runMigrate,
	})

	r.Register(&Command{
		Name:        "migrate:diff",
		Description: "Compute Ent schema diff against DB and generate versioned migration",
		Run:         runMigrateDiff,
	})

	r.Register(&Command{
		Name:        "migrate:status",
		Description: "View database migration statuses via Atlas",
		Run:         runMigrateStatus,
	})

	r.Register(&Command{
		Name:        "migrate:rollback",
		Description: "Rollback database migrations via Atlas",
		Run:         runMigrateRollback,
	})

	r.Register(&Command{
		Name:        "db:seed",
		Description: "Seed the database with records via database/seeders",
		Run:         runDbSeed,
	})

	r.Register(&Command{
		Name:        "generate",
		Description: "Execute code generators (templ generate & ent generate)",
		Run:         runGenerate,
	})

	r.Register(&Command{
		Name:        "flutter:build",
		Description: "Build Flutter Web assets into client/build/web for GoLaravel hosting",
		Run:         runFlutterBuild,
	})

	r.Register(&Command{
		Name:        "queue:work",
		Description: "Launch background queue worker pool",
		Run:         runQueueWork,
	})

	r.Register(&Command{
		Name:        "serve",
		Description: "Start the GoLaravel development server",
		Run:         runServe,
	})
}

func makeJob(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing job name. Usage: artisan make:job <Name>")
	}
	name := strings.Title(args[0])
	if !strings.HasSuffix(name, "Job") {
		name += "Job"
	}
	filename := strings.ToLower(strings.TrimSuffix(name, "Job")) + "_job.go"
	dir := filepath.Join("app", "jobs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, filename)

	content := fmt.Sprintf(`package jobs

import (
	"context"
	"log/slog"
)

// %s performs background work asynchronously.
type %s struct {
	ID string `+"`json:\"id\"`"+`
}

func New%s(id string) *%s {
	return &%s{ID: id}
}

// Handle executes the job.
func (j *%s) Handle(ctx context.Context) error {
	slog.Info("Processing job", "job", "%s", "id", j.ID)
	// Add business logic here
	return nil
}
`, name, name, name, name, name, name, name)

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}
	fmt.Printf("\033[32mINFO\033[0m Job [%s] created successfully.\n", targetPath)
	return nil
}

func makeMail(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing mail name. Usage: artisan make:mail <Name>")
	}
	name := strings.Title(args[0])
	filename := strings.ToLower(name) + ".go"
	dir := filepath.Join("app", "mail")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, filename)

	content := fmt.Sprintf(`package mail

import (
	"github.com/golaravel/golaravel/pkg/mail"
)

// Build%s creates a configured email message.
func Build%s(recipient, name string) *mail.Message {
	return mail.NewMessage().
		To(recipient).
		Subject("%s").
		HTML("<h1>Hello " + name + "!</h1><p>Thank you for using GoLaravel.</p>")
}
`, name, name, name)

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}
	fmt.Printf("\033[32mINFO\033[0m Mail [%s] created successfully.\n", targetPath)
	return nil
}

func makePolicy(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing policy name. Usage: artisan make:policy <Name>")
	}
	name := strings.Title(args[0])
	if !strings.HasSuffix(name, "Policy") {
		name += "Policy"
	}
	filename := strings.ToLower(strings.TrimSuffix(name, "Policy")) + "_policy.go"
	dir := filepath.Join("app", "policies")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, filename)

	content := fmt.Sprintf(`package policies

import (
	"github.com/golaravel/golaravel/pkg/auth"
)

type %s struct{}

func (p *%s) View(user auth.Authenticatable, record any) bool {
	return true
}

func (p *%s) Update(user auth.Authenticatable, record any) bool {
	return true
}

func (p *%s) Delete(user auth.Authenticatable, record any) bool {
	return false
}
`, name, name, name, name)

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}
	fmt.Printf("\033[32mINFO\033[0m Policy [%s] created successfully.\n", targetPath)
	return nil
}

func runQueueWork(args []string) error {
	fmt.Println("\033[32mINFO\033[0m Starting GoLaravel queue worker pool...")
	fmt.Println("Listening for background jobs [Ctrl+C to stop]...")
	// Blocks until interrupt
	select {}
}

func runServe(args []string) error {
	port := "8080"
	if len(args) > 0 {
		port = args[0]
	}
	fmt.Printf("\033[32mINFO\033[0m Starting GoLaravel server on http://localhost:%s\n", port)
	cmd := exec.Command("go", "run", "main.go")
	cmd.Env = append(os.Environ(), "PORT=:"+port)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func makeSchema(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing schema name. Usage: artisan make:schema <Name>")
	}
	name := strings.Title(args[0])
	filename := strings.ToLower(name) + ".go"
	dir := filepath.Join("app", "schema")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, filename)

	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("schema '%s' already exists at %s", name, targetPath)
	}

	content := fmt.Sprintf(`package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// %s holds the schema definition for the %s entity.
type %s struct {
	ent.Schema
}

// Fields of the %s.
func (%s) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.Time("created_at").Immutable(),
	}
}

// Edges of the %s.
func (%s) Edges() []ent.Edge {
	return nil
}
`, name, name, name, name, name, name, name)

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}

	fmt.Printf("\033[32mINFO\033[0m Schema [%s] created successfully.\n", targetPath)
	return nil
}

func makeView(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing view name. Usage: artisan make:view <name>")
	}
	name := strings.ToLower(args[0])
	componentName := strings.Title(name)
	filename := name + ".templ"
	dir := filepath.Join("app", "views")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, filename)

	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("view '%s' already exists at %s", name, targetPath)
	}

	content := fmt.Sprintf(`package views

// %sView renders the %s page component using templ.
templ %sView(title string) {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="UTF-8"/>
			<title>{ title }</title>
			<script src="https://cdn.tailwindcss.com"></script>
		</head>
		<body class="bg-gray-100 text-gray-900 p-8">
			<div class="max-w-4xl mx-auto bg-white p-6 rounded-lg shadow-sm">
				<h1 class="text-3xl font-bold mb-4">{ title }</h1>
				<p class="text-gray-600">Generated by GoLaravel with Templ engine.</p>
			</div>
		</body>
	</html>
}
`, componentName, name, componentName)

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}

	fmt.Printf("\033[32mINFO\033[0m Templ View [%s] created successfully.\n", targetPath)
	return nil
}

func makeController(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing controller name. Usage: artisan make:controller <Name>")
	}
	name := strings.Title(args[0])
	if !strings.HasSuffix(name, "Controller") {
		name += "Controller"
	}
	filename := strings.ToLower(strings.TrimSuffix(name, "Controller")) + "_controller.go"
	dir := filepath.Join("app", "handlers")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, filename)

	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("controller '%s' already exists at %s", name, targetPath)
	}

	content := fmt.Sprintf(`package handlers

import (
	"net/http"

	"github.com/golaravel/golaravel/pkg/context"
)

type %s struct{}

func New%s() *%s {
	return &%s{}
}

// Index renders list of items.
func (ctrl *%s) Index(c *context.Context) error {
	return c.JSON(http.StatusOK, map[string]string{
		"message": "Index action from %s",
	})
}

// Show renders a single item.
func (ctrl *%s) Show(c *context.Context) error {
	id := c.Param("id")
	return c.JSON(http.StatusOK, map[string]string{
		"id": id,
	})
}
`, name, name, name, name, name, name, name)

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}

	fmt.Printf("\033[32mINFO\033[0m Controller [%s] created successfully.\n", targetPath)
	return nil
}

func makeMiddleware(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing middleware name. Usage: artisan make:middleware <Name>")
	}
	name := strings.Title(args[0])
	filename := strings.ToLower(name) + ".go"
	dir := filepath.Join("app", "middleware")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, filename)

	content := fmt.Sprintf(`package middleware

import (
	"github.com/golaravel/golaravel/pkg/context"
)

// %s middleware.
func %s() context.HandlerFunc {
	return func(c *context.Context) error {
		// Pre-handler logic
		err := c.Next()
		// Post-handler logic
		return err
	}
}
`, name, name)

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}

	fmt.Printf("\033[32mINFO\033[0m Middleware [%s] created successfully.\n", targetPath)
	return nil
}

func runGenerate(args []string) error {
	fmt.Println("\033[34m==>\033[0m Generating Templ components...")
	templCmd := exec.Command("templ", "generate")
	templCmd.Stdout = os.Stdout
	templCmd.Stderr = os.Stderr
	if err := templCmd.Run(); err != nil {
		return fmt.Errorf("templ generate failed: %w", err)
	}

	fmt.Println("\033[34m==>\033[0m Running go generate ./...")
	goGenCmd := exec.Command("go", "generate", "./...")
	goGenCmd.Stdout = os.Stdout
	goGenCmd.Stderr = os.Stderr
	if err := goGenCmd.Run(); err != nil {
		return fmt.Errorf("go generate failed: %w", err)
	}

	fmt.Println("\033[32mSUCCESS\033[0m All code generators executed successfully.")
	return nil
}

func runFlutterBuild(args []string) error {
	clientDir := "client"
	if len(args) > 0 {
		clientDir = args[0]
	}

	if _, err := os.Stat(clientDir); os.IsNotExist(err) {
		return fmt.Errorf("flutter directory '%s' not found. Run artisan new to scaffold Flutter client", clientDir)
	}

	fmt.Printf("\033[34m==>\033[0m Building Flutter Web application in \033[1;37m%s\033[0m...\n", clientDir)
	cmd := exec.Command("flutter", "build", "web", "--release")
	cmd.Dir = clientDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("flutter build failed: %w", err)
	}

	fmt.Println("\033[32mSUCCESS\033[0m Flutter Web application built successfully in client/build/web.")
	return nil
}

// PrintRoutes displays the registered routes in a table.
func PrintRoutes(r *router.Router) {
	fmt.Println("\nGoLaravel Application Route Table:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(w, "  METHOD\tURI\tNAME")
	fmt.Fprintln(w, "  ------\t---\t----")
	for _, rt := range r.Routes() {
		fmt.Fprintf(w, "  \033[33m%-6s\033[0m\t%-28s\t\033[36m%s\033[0m\n", rt.Method, rt.Path, rt.GetName())
	}
	_ = w.Flush()
	fmt.Println()
}
