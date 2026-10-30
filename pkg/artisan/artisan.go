package artisan

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/golaravel/golaravel/pkg/queue"
	"github.com/golaravel/golaravel/pkg/router"
	"github.com/golaravel/golaravel/pkg/schedule"
	"github.com/golaravel/golaravel/pkg/telemetry"
)

// Version is the current version of GoLaravel and Artisan CLI.
const Version = "v1.0.0"

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

	if args[0] == "version" || args[0] == "--version" || args[0] == "-v" || args[0] == "-V" {
		fmt.Printf("GoLaravel Framework & Artisan CLI %s\n", Version)
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
		Name:        "version",
		Description: "Display the current GoLaravel and Artisan CLI version",
		Run: func(args []string) error {
			fmt.Printf("GoLaravel Framework & Artisan CLI %s\n", Version)
			return nil
		},
	})

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
		Name:        "make:crud",
		Description: "Scaffold full-stack CRUD (Schema, Requests, Controller, Views, Seeder)",
		Run:         makeCrud,
	})

	r.Register(&Command{
		Name:        "make:middleware",
		Description: "Create a new HTTP middleware",
		Run:         makeMiddleware,
	})

	r.Register(&Command{
		Name:        "make:limiter",
		Description: "Create a new named rate limiter (app/limiters/<name>_limiter.go)",
		Run:         makeLimiter,
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
		Name:        "make:notification",
		Description: "Create a multi-channel notification class (app/notifications/<name>.go)",
		Run:         makeNotification,
	})

	r.Register(&Command{
		Name:        "make:auth",
		Description: "Scaffold turnkey authentication workflows and TOTP 2FA controller",
		Run:         makeAuth,
	})

	r.Register(&Command{
		Name:        "make:oauth",
		Description: "Scaffold an OAuth2 social authentication controller (Usage: artisan make:oauth <Provider>)",
		Run:         makeOAuth,
	})

	r.Register(&Command{
		Name:        "make:scope",
		Description: "Create a global Ent query scope (app/scopes/<name>_scope.go)",
		Run:         makeScope,
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
		Name:        "make:factory",
		Description: "Create a new Ent model factory (database/factories/<name>_factory.go)",
		Run:         makeFactory,
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
		Description: "Launch background queue worker pool (--queue=..., --concurrency=..., --driver=...)",
		Run:         runQueueWork,
	})

	r.Register(&Command{
		Name:        "queue:failed",
		Description: "List all failed jobs in Dead-Letter Queue (DLQ)",
		Run:         runQueueFailed,
	})

	r.Register(&Command{
		Name:        "queue:retry",
		Description: "Retry a failed queue job (Usage: artisan queue:retry <id|all>)",
		Run:         runQueueRetry,
	})

	r.Register(&Command{
		Name:        "queue:flush",
		Description: "Flush all failed jobs from Dead-Letter Queue (DLQ)",
		Run:         runQueueFlush,
	})

	r.Register(&Command{
		Name:        "health",
		Description: "Run all subsystem health checks and display status report",
		Run:         runHealthCheck,
	})

	r.Register(&Command{
		Name:        "schedule:run",
		Description: "Run the scheduled tasks due right now",
		Run:         runScheduleRun,
	})

	r.Register(&Command{
		Name:        "schedule:work",
		Description: "Run the scheduled tasks worker daemon",
		Run:         runScheduleWork,
	})

	r.Register(&Command{
		Name:        "schedule:list",
		Description: "List all registered scheduled tasks",
		Run:         runScheduleList,
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
		return fmt.Errorf("missing mail name. Usage: artisan make:mail <Name> [--view]")
	}
	rawName := args[0]
	hasView := false
	for _, arg := range args[1:] {
		if arg == "--view" {
			hasView = true
		}
	}

	name := strings.Title(rawName)
	if strings.HasSuffix(name, "Mail") {
		name = strings.TrimSuffix(name, "Mail")
	}

	filename := strings.ToLower(name) + "_mail.go"
	dir := filepath.Join("app", "mail")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, filename)

	var content string
	if hasView {
		viewDir := filepath.Join("app", "views", "mail")
		_ = os.MkdirAll(viewDir, 0755)
		viewPath := filepath.Join(viewDir, strings.ToLower(name)+".templ")

		viewContent := fmt.Sprintf(`package mailviews

templ %sEmail(name string) {
	<div style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; max-width: 600px; margin: 0 auto; padding: 24px; border: 1px solid #e2e8f0; border-radius: 8px;">
		<h1 style="color: #0f172a; font-size: 20px; font-weight: 700; margin-bottom: 16px;">Hello { name }!</h1>
		<p style="color: #475569; font-size: 15px; line-height: 1.6;">Thank you for using GoLaravel.</p>
		<div style="margin-top: 24px; padding-top: 16px; border-top: 1px solid #e2e8f0; font-size: 13px; color: #94a3b8;">
			&copy; GoLaravel Framework. All rights reserved.
		</div>
	</div>
}
`, name)
		_ = os.WriteFile(viewPath, []byte(viewContent), 0644)
		fmt.Printf("\033[32mINFO\033[0m Mail View [%s] created successfully.\n", viewPath)

		content = fmt.Sprintf(`package mail

import (
	"context"

	"github.com/golaravel/golaravel/app/views/mailviews"
	"github.com/golaravel/golaravel/pkg/mail"
)

// %sMail represents the transactional email for %s.
type %sMail struct {
	Recipient string
	Name      string
}

// New%sMail initializes the mailable.
func New%sMail(recipient, name string) *%sMail {
	return &%sMail{
		Recipient: recipient,
		Name:      name,
	}
}

// Build compiles the message using the Templ view component.
func (m *%sMail) Build(ctx context.Context) (*mail.Message, error) {
	return mail.NewMessage().
		To(m.Recipient).
		Subject("%s").
		View(ctx, mailviews.%sEmail(m.Name)).
		Text("Hello " + m.Name + "!\nThank you for using GoLaravel."), nil
}
`, name, name, name, name, name, name, name, name, name, name)
	} else {
		content = fmt.Sprintf(`package mail

import (
	"context"

	"github.com/golaravel/golaravel/pkg/mail"
)

// %sMail represents the transactional email for %s.
type %sMail struct {
	Recipient string
	Name      string
}

// New%sMail initializes the mailable.
func New%sMail(recipient, name string) *%sMail {
	return &%sMail{
		Recipient: recipient,
		Name:      name,
	}
}

// Build compiles the email message.
func (m *%sMail) Build(ctx context.Context) (*mail.Message, error) {
	return mail.NewMessage().
		To(m.Recipient).
		Subject("%s").
		HTML("<h1>Hello " + m.Name + "!</h1><p>Thank you for using GoLaravel.</p>").
		Text("Hello " + m.Name + "!\nThank you for using GoLaravel."), nil
}
`, name, name, name, name, name, name, name, name, name)
	}

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

func makeNotification(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing notification name. Usage: artisan make:notification <Name>")
	}
	name := strings.Title(args[0])
	if !strings.HasSuffix(name, "Notification") {
		name += "Notification"
	}
	filename := strings.ToLower(strings.TrimSuffix(name, "Notification")) + "_notification.go"
	dir := filepath.Join("app", "notifications")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, filename)

	content := fmt.Sprintf(`package notifications

import (
	"fmt"

	"github.com/golaravel/golaravel/pkg/broadcast"
	"github.com/golaravel/golaravel/pkg/mail"
	"github.com/golaravel/golaravel/pkg/notification"
)

// %s delivers multi-channel alerts (mail, database, broadcast, slack, sms).
type %s struct {
	Title   string
	Message string
}

// New%s creates a new %s instance.
func New%s(title, message string) *%s {
	return &%s{
		Title:   title,
		Message: message,
	}
}

// Via determines which channels the notification should be delivered on.
func (n *%s) Via(notifiable notification.Notifiable) []string {
	return []string{"mail", "database", "broadcast"}
}

// ToMail constructs the email message.
func (n *%s) ToMail(notifiable notification.Notifiable) (*mail.Message, error) {
	return mail.NewMessage().
		Subject(n.Title).
		HTML(fmt.Sprintf("<h2>%%s</h2><p>%%s</p>", n.Title, n.Message)), nil
}

// ToDatabase constructs the in-app database alert record.
func (n *%s) ToDatabase(notifiable notification.Notifiable) (*notification.DatabaseMessage, error) {
	return &notification.DatabaseMessage{
		Type: "%s",
		Data: map[string]any{
			"title":   n.Title,
			"message": n.Message,
		},
	}, nil
}

// ToBroadcast constructs the real-time push event for WebSockets and SSE.
func (n *%s) ToBroadcast(notifiable notification.Notifiable) (*broadcast.Message, error) {
	return &broadcast.Message{
		Event: "notification.new",
		Data: map[string]any{
			"title":   n.Title,
			"message": n.Message,
		},
	}, nil
}
`, name, name, name, name, name, name, name, name, name, name, name, name)

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}
	fmt.Printf("\033[32mINFO\033[0m Notification [%s] created successfully.\n", targetPath)
	return nil
}

func makeAuth(args []string) error {
	dir := filepath.Join("app", "handlers")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, "auth_workflow_controller.go")

	content := `package handlers

import (
	"context"
	"fmt"

	"github.com/golaravel/golaravel/pkg/auth"
	"github.com/golaravel/golaravel/pkg/container"
	"github.com/golaravel/golaravel/pkg/router"
)

// RegisterAuthWorkflows scaffolds and mounts turnkey auth, password reset, and TOTP 2FA endpoints.
func RegisterAuthWorkflows(c *container.Container, r router.RouteRegistrar, secretKey string) *auth.WorkflowController {
	ctrl := auth.NewWorkflowController(auth.WorkflowConfig{
		SecretKey: secretKey,
		AppName:   "GoLaravel App",
		AppURL:    "http://localhost:8080",
		Callbacks: auth.WorkflowCallbacks{
			// Wire to your Ent client or custom database queries:
			FindByEmail: func(ctx context.Context, email string) (auth.AuthenticatableUser, error) {
				// e.g. return client.User.Query().Where(user.EmailEQ(email)).Only(ctx)
				return nil, fmt.Errorf("implement FindByEmail with your database client")
			},
			FindByID: func(ctx context.Context, id string) (auth.AuthenticatableUser, error) {
				// e.g. return client.User.Get(ctx, parsedID)
				return nil, fmt.Errorf("implement FindByID with your database client")
			},
		},
	})

	ctrl.RegisterRoutes(r, "/auth")
	return ctrl
}
`

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}
	fmt.Printf("\033[32mINFO\033[0m Turnkey Auth & TOTP 2FA scaffolding [%s] created successfully.\n", targetPath)
	return nil
}

func makeOAuth(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing provider name. Usage: artisan make:oauth <Provider> (e.g. GitHub, Google, Discord)")
	}

	raw := args[0]
	name := strings.ToUpper(raw[:1]) + raw[1:]
	slug := strings.ToLower(raw)

	dir := filepath.Join("app", "handlers")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	targetPath := filepath.Join(dir, fmt.Sprintf("%s_oauth_controller.go", slug))
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("OAuth controller already exists at %s", targetPath)
	}

	tmpl := `package handlers

import (
	"fmt"
	"net/http"

	"github.com/golaravel/golaravel/pkg/auth/oauth"
	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/router"
)

// {{NAME}}OAuthController handles OAuth2 authentication for {{NAME}}.
type {{NAME}}OAuthController struct {
	// Add your Ent database client or dependencies here
}

// New{{NAME}}OAuthController creates a new {{NAME}}OAuthController instance.
func New{{NAME}}OAuthController() *{{NAME}}OAuthController {
	return &{{NAME}}OAuthController{}
}

// RegisterRoutes registers the redirect and callback endpoints.
func (ctrl *{{NAME}}OAuthController) RegisterRoutes(r router.RouteRegistrar) {
	r.Get("/auth/{{SLUG}}/redirect", ctrl.Redirect)
	r.Get("/auth/{{SLUG}}/callback", ctrl.Callback)
}

// Redirect initiates the OAuth2 redirect to {{NAME}}.
func (ctrl *{{NAME}}OAuthController) Redirect(c *context.Context) error {
	return oauth.Driver("{{SLUG}}").Redirect(c)
}

// Callback handles the provider callback, synchronizes user in database, and issues session/token.
func (ctrl *{{NAME}}OAuthController) Callback(c *context.Context) error {
	socialUser, err := oauth.Driver("{{SLUG}}").User(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error":   "oauth_error",
			"message": err.Error(),
		})
	}

	// TODO: Find or create user in Ent database:
	// user, err := db.User.Query().Where(user.Email(socialUser.Email)).Only(c.Request.Context())
	// if ent.IsNotFound(err) {
	//     user, err = db.User.Create().
	//         SetEmail(socialUser.Email).
	//         SetName(socialUser.Name).
	//         SetAvatar(socialUser.Avatar).
	//         Save(c.Request.Context())
	// }

	return c.JSON(http.StatusOK, map[string]any{
		"message": fmt.Sprintf("Authenticated via %s successfully", "{{NAME}}"),
		"user":    socialUser,
	})
}
`
	content := strings.ReplaceAll(tmpl, "{{NAME}}", name)
	content = strings.ReplaceAll(content, "{{SLUG}}", slug)

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}

	fmt.Printf("\033[32mINFO\033[0m OAuth Controller [%s] created successfully.\n", targetPath)
	return nil
}

func makeScope(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing scope name. Usage: artisan make:scope <Name>")
	}
	name := strings.Title(args[0])
	if !strings.HasSuffix(name, "Scope") {
		name += "Scope"
	}
	filename := strings.ToLower(strings.TrimSuffix(name, "Scope")) + "_scope.go"
	dir := filepath.Join("app", "scopes")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, filename)

	content := fmt.Sprintf(`package scopes

import (
	"context"

	"github.com/golaravel/golaravel/pkg/privacy"
)

// %s enforces custom query constraints before execution.
type %s[Q any] struct{}

func New%s[Q any]() *%s[Q] {
	return &%s[Q]{}
}

// Apply adds constraints to the query.
func (s *%s[Q]) Apply(ctx context.Context, query Q) error {
	// Add custom query filters here (e.g. status, region, role)
	return nil
}
`, name, name, name, name, name, name)

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}
	fmt.Printf("\033[32mINFO\033[0m Scope [%s] created successfully.\n", targetPath)
	return nil
}

func runQueueWork(args []string) error {
	concurrency := 2
	queues := []string{"high", "default", "low"}
	driverName := "memory"

	for _, arg := range args {
		if strings.HasPrefix(arg, "--concurrency=") {
			if n, err := strconv.Atoi(strings.TrimPrefix(arg, "--concurrency=")); err == nil && n > 0 {
				concurrency = n
			}
		} else if strings.HasPrefix(arg, "--queue=") {
			raw := strings.TrimPrefix(arg, "--queue=")
			parts := strings.Split(raw, ",")
			var qList []string
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					qList = append(qList, p)
				}
			}
			if len(qList) > 0 {
				queues = qList
			}
		} else if strings.HasPrefix(arg, "--driver=") {
			driverName = strings.TrimPrefix(arg, "--driver=")
		}
	}

	q := queue.DefaultQueue()
	if driverName == "redis" {
		addr := os.Getenv("REDIS_HOST")
		if addr == "" {
			addr = "127.0.0.1"
		}
		port := os.Getenv("REDIS_PORT")
		if port == "" {
			port = "6379"
		}
		cfg := queue.RedisConfig{
			Addr:     addr + ":" + port,
			Password: os.Getenv("REDIS_PASSWORD"),
			Prefix:   "golaravel:queue:",
		}
		rd, err := queue.NewRedisDriver(cfg)
		if err != nil {
			return fmt.Errorf("failed to connect to Redis queue: %w", err)
		}
		q.SetDriver(rd)
		q.SetDLQ(queue.NewRedisFailedJobStore(cfg))
	}

	fmt.Printf("\033[32mINFO\033[0m Starting GoLaravel queue worker pool [Driver: %s, Concurrency: %d, Queues: %s]...\n",
		driverName, concurrency, strings.Join(queues, ", "))
	fmt.Println("Listening for background jobs [Ctrl+C to stop]...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q.StartWorkers(ctx, concurrency, queues...)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\n\033[33mWARN\033[0m Shutting down queue worker pool gracefully...")
	q.Stop()
	fmt.Println("\033[32mSUCCESS\033[0m Queue workers stopped.")
	return nil
}

func runQueueFailed(args []string) error {
	dlq := queue.DefaultQueue().DLQ()
	jobs, err := dlq.All(100, 0)
	if err != nil {
		return fmt.Errorf("failed to retrieve DLQ jobs: %w", err)
	}

	if len(jobs) == 0 {
		fmt.Println("\033[32mINFO\033[0m No failed jobs found in Dead-Letter Queue.")
		return nil
	}

	fmt.Printf("\n\033[33mDead-Letter Queue (%d failed jobs):\033[0m\n", len(jobs))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "  ID\tQUEUE\tTYPE\tFAILED AT\tERROR")
	fmt.Fprintln(w, "  --\t-----\t----\t---------\t-----")
	for _, j := range jobs {
		errMsg := j.Exception
		if len(errMsg) > 40 {
			errMsg = errMsg[:37] + "..."
		}
		fmt.Fprintf(w, "  \033[36m%s\033[0m\t%s\t\033[1;37m%s\033[0m\t%s\t\033[31m%s\033[0m\n",
			j.ID, j.Queue, j.Type, j.FailedAt.Format("2006-01-02 15:04:05"), errMsg)
	}
	_ = w.Flush()
	fmt.Println()
	return nil
}

func runQueueRetry(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing target job ID. Usage: artisan queue:retry <id|all>")
	}

	target := args[0]
	q := queue.DefaultQueue()

	if target == "all" {
		count, err := q.RetryAllFailed()
		if err != nil {
			return fmt.Errorf("failed to retry failed jobs: %w", err)
		}
		fmt.Printf("\033[32mSUCCESS\033[0m Retried %d failed job(s).\n", count)
		return nil
	}

	if err := q.RetryFailed(target); err != nil {
		return fmt.Errorf("failed to retry job [%s]: %w", target, err)
	}

	fmt.Printf("\033[32mSUCCESS\033[0m Failed job [%s] re-queued successfully.\n", target)
	return nil
}

func runQueueFlush(args []string) error {
	dlq := queue.DefaultQueue().DLQ()
	if err := dlq.Flush(); err != nil {
		return fmt.Errorf("failed to flush Dead-Letter Queue: %w", err)
	}
	fmt.Println("\033[32mSUCCESS\033[0m All failed jobs have been flushed from Dead-Letter Queue.")
	return nil
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

func makeCrud(args []string) error {
	return RunMakeCrud(args)
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

func makeLimiter(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing limiter name. Usage: artisan make:limiter <Name>")
	}
	name := strings.Title(args[0])
	if strings.HasSuffix(name, "Limiter") {
		name = strings.TrimSuffix(name, "Limiter")
	}

	filename := strings.ToLower(name) + "_limiter.go"
	dir := filepath.Join("app", "limiters")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, filename)

	slug := strings.ToLower(name)
	content := fmt.Sprintf(`package limiters

import (
	"github.com/golaravel/golaravel/pkg/auth"
	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/ratelimit"
)

// Register%sLimiter registers the "%s" named sliding-window rate limiter.
func Register%sLimiter() {
	ratelimit.For("%s", func(c *context.Context) *ratelimit.Limit {
		user := auth.User(c)

		// Tiered Throttling Strategy
		if user != nil {
			// Authenticated users get higher throughput based on their identifier
			return ratelimit.PerMinute(120).By(user.GetAuthIdentifier())
		}

		// Anonymous / Guest clients are throttled per IP address
		return ratelimit.PerMinute(30).By(c.ClientIP())
	})
}
`, name, slug, name, slug)

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return err
	}

	fmt.Printf("\033[32mINFO\033[0m Rate Limiter [%s] created successfully.\n", targetPath)
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

func runHealthCheck(args []string) error {
	health := telemetry.DefaultHealth()

	// If no checks registered yet, add standard memory & disk checks
	if _, ok := health.RunCheck(context.Background(), "memory"); !ok {
		health.Register(telemetry.MemoryCheck("memory", 512*1024*1024))
	}
	if _, ok := health.RunCheck(context.Background(), "disk"); !ok {
		health.Register(telemetry.DiskCheck("disk", ".", 500*1024*1024))
	}

	report := health.Run(context.Background())

	statusBadge := "\033[32m[HEALTHY]\033[0m"
	if report.Status == telemetry.StatusDegraded {
		statusBadge = "\033[33m[DEGRADED]\033[0m"
	} else if report.Status == telemetry.StatusUnhealthy {
		statusBadge = "\033[31m[UNHEALTHY]\033[0m"
	}

	fmt.Printf("\nGoLaravel System Health Status: %s (Uptime: %s)\n\n", statusBadge, report.Uptime)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "  SUBSYSTEM\tSTATUS\tLATENCY\tDETAILS")
	fmt.Fprintln(w, "  ---------\t------\t-------\t-------")

	var names []string
	for k := range report.Checks {
		names = append(names, k)
	}
	sort.Strings(names)

	for _, name := range names {
		res := report.Checks[name]
		badge := "\033[32mPASS\033[0m"
		if res.Status == telemetry.StatusDegraded {
			badge = "\033[33mWARN\033[0m"
		} else if res.Status == telemetry.StatusUnhealthy {
			badge = "\033[31mFAIL\033[0m"
		}

		detailStr := ""
		if res.Error != "" {
			detailStr = fmt.Sprintf("\033[31m%s\033[0m", res.Error)
		} else if len(res.Details) > 0 {
			var parts []string
			for k, v := range res.Details {
				parts = append(parts, fmt.Sprintf("%s=%v", k, v))
			}
			detailStr = strings.Join(parts, ", ")
			if len(detailStr) > 40 {
				detailStr = detailStr[:37] + "..."
			}
		}

		fmt.Fprintf(w, "  \033[1;37m%-10s\033[0m\t%s\t%.2fms\t%s\n",
			res.Name, badge, res.LatencyMS, detailStr)
	}
	_ = w.Flush()
	fmt.Println()

	if report.Status == telemetry.StatusUnhealthy {
		return fmt.Errorf("system health check failed with status %s", report.Status)
	}
	return nil
}

func runScheduleRun(args []string) error {
	now := time.Now()
	fmt.Printf("\n\033[1;36mRunning scheduled tasks [%s]...\033[0m\n", now.Format("2006-01-02 15:04:05"))

	results := schedule.RunDue(context.Background(), now)
	if len(results) == 0 {
		fmt.Println("No scheduled tasks are due to run.")
		fmt.Println()
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	var succeeded, failed int
	for _, res := range results {
		name := res.Event.GetName()
		if res.Error != nil {
			failed++
			fmt.Fprintf(w, "  \033[31m[%s]\033[0m %-30s \033[31mFAIL (%.3fs)\033[0m: %v\n",
				res.StartedAt.Format("15:04:05"), name, res.Duration.Seconds(), res.Error)
		} else {
			succeeded++
			fmt.Fprintf(w, "  \033[32m[%s]\033[0m %-30s \033[32mDONE (%.3fs)\033[0m\n",
				res.StartedAt.Format("15:04:05"), name, res.Duration.Seconds())
		}
	}
	_ = w.Flush()
	fmt.Printf("\nSummary: %d executed, %d succeeded, %d failed.\n\n", len(results), succeeded, failed)
	if failed > 0 {
		return fmt.Errorf("%d scheduled task(s) failed", failed)
	}
	return nil
}

func runScheduleWork(args []string) error {
	fmt.Println("\n\033[1;32mGoLaravel Schedule Worker Daemon started.\033[0m")
	fmt.Println("Running due tasks every minute. Press Ctrl+C to stop.")
	fmt.Println()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastMinute time.Time
	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nSchedule worker daemon gracefully stopped.")
			return nil
		case now := <-ticker.C:
			currentMinute := now.Truncate(time.Minute)
			if currentMinute.After(lastMinute) {
				lastMinute = currentMinute
				results := schedule.RunDue(ctx, now)
				for _, res := range results {
					if res.Error != nil {
						fmt.Printf("[%s] Task '%s' FAILED (%.3fs): %v\n",
							now.Format("15:04:05"), res.Event.GetName(), res.Duration.Seconds(), res.Error)
					} else {
						fmt.Printf("[%s] Task '%s' DONE (%.3fs)\n",
							now.Format("15:04:05"), res.Event.GetName(), res.Duration.Seconds())
					}
				}
			}
		}
	}
}

func runScheduleList(args []string) error {
	events := schedule.Events()
	fmt.Println("\n\033[1;37mGoLaravel Scheduled Tasks:\033[0m")
	if len(events) == 0 {
		fmt.Println("  No scheduled tasks registered.")
		fmt.Println()
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(w, "  TASK / COMMAND\tCRON\tNEXT DUE\tTIMEZONE\tOVERLAP\tONE SERVER")
	fmt.Fprintln(w, "  --------------\t----\t--------\t--------\t-------\t----------")

	now := time.Now()
	for _, e := range events {
		name := e.GetName()
		cron := e.GetCronExpression()
		if cron == "" {
			cron = "* * * * *"
		}
		next := e.Next(now)
		nextStr := "never"
		if !next.IsZero() {
			nextStr = next.Format("2006-01-02 15:04:05")
		}
		tz := e.GetTimezone().String()

		overlap := "\033[32mAllow\033[0m"
		if e.IsWithoutOverlapping() {
			overlap = "\033[33mBlock\033[0m"
		}

		oneServer := "\033[90mNo\033[0m"
		if e.IsOnOneServer() {
			oneServer = "\033[36mYes\033[0m"
		}

		fmt.Fprintf(w, "  \033[1;36m%-20s\033[0m\t%-12s\t%-19s\t%-12s\t%s\t%s\n",
			name, cron, nextStr, tz, overlap, oneServer)
	}
	_ = w.Flush()
	fmt.Println()
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
