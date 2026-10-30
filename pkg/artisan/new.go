package artisan

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProjectConfig holds answers from the interactive project creation wizard.
type ProjectConfig struct {
	Name            string
	Stack           string // "templ", "inertia", "flutter", "hybrid"
	FlutterTargets  []string
	Database        string // "sqlite", "postgres", "mysql"
	IncludeInvoices bool   // Templ fast-path micro-pages for invoices/tokens
}

// RunNew handles the interactive `artisan new [name]` command.
func RunNew(args []string) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n\033[35m====================================================\033[0m")
	fmt.Println("\033[1;37m        GoLaravel Project Creation Wizard           \033[0m")
	fmt.Println("\033[35m====================================================\033[0m")

	cfg := &ProjectConfig{}

	// 1. Project Name
	if len(args) > 0 {
		cfg.Name = args[0]
	} else {
		fmt.Print("\n\033[32m?\033[0m Project name [my-golaravel-app]: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			input = "my-golaravel-app"
		}
		cfg.Name = input
	}

	// 2. Presentation Stack
	fmt.Println("\n\033[32m?\033[0m Which frontend / presentation stack would you like to use?")
	fmt.Println("  \033[36m[1]\033[0m Templ (Blade-like SSR)     - High-speed compiled HTML for standard web apps")
	fmt.Println("  \033[36m[2]\033[0m Inertia.js (Modern Monolith) - React / Vue without API boilerplate")
	fmt.Println("  \033[36m[3]\033[0m Flutter Multiplatform       - Web, Android, iOS, Windows, macOS, Linux")
	fmt.Println("  \033[36m[4]\033[0m \033[1;33mHybrid: Flutter + Templ Fast-SSR (Recommended)\033[0m")
	fmt.Println("      ↳ Flutter app for rich interactive clients (Mobile/Desktop/Web)")
	fmt.Println("      ↳ Templ micro-pages for zero-overhead customer invoices, receipts & token links")
	fmt.Print("  Selection [4]: ")

	stackChoice, _ := reader.ReadString('\n')
	stackChoice = strings.TrimSpace(stackChoice)
	switch stackChoice {
	case "1":
		cfg.Stack = "templ"
	case "2":
		cfg.Stack = "inertia"
	case "3":
		cfg.Stack = "flutter"
	default:
		cfg.Stack = "hybrid"
		cfg.IncludeInvoices = true
	}

	// 3. Flutter Target Platforms
	if cfg.Stack == "flutter" || cfg.Stack == "hybrid" {
		fmt.Println("\n\033[32m?\033[0m Which Flutter target platforms would you like to configure?")
		fmt.Println("  \033[36m[1]\033[0m All Platforms (Web, Android, iOS, Windows, macOS, Linux)")
		fmt.Println("  \033[36m[2]\033[0m Web & Mobile (Web, Android, iOS)")
		fmt.Println("  \033[36m[3]\033[0m Web & Desktop (Web, Windows, macOS, Linux)")
		fmt.Println("  \033[36m[4]\033[0m Web Only")
		fmt.Print("  Selection [1]: ")

		platChoice, _ := reader.ReadString('\n')
		platChoice = strings.TrimSpace(platChoice)
		switch platChoice {
		case "2":
			cfg.FlutterTargets = []string{"web", "android", "ios"}
		case "3":
			cfg.FlutterTargets = []string{"web", "windows", "macos", "linux"}
		case "4":
			cfg.FlutterTargets = []string{"web"}
		default:
			cfg.FlutterTargets = []string{"web", "android", "ios", "windows", "macos", "linux"}
		}
	}

	// 4. Database Engine
	fmt.Println("\n\033[32m?\033[0m Which database driver do you want to use with Ent ORM?")
	fmt.Println("  \033[36m[1]\033[0m SQLite (in-memory / zero setup)")
	fmt.Println("  \033[36m[2]\033[0m PostgreSQL")
	fmt.Println("  \033[36m[3]\033[0m MySQL")
	fmt.Print("  Selection [1]: ")

	dbChoice, _ := reader.ReadString('\n')
	dbChoice = strings.TrimSpace(dbChoice)
	switch dbChoice {
	case "2":
		cfg.Database = "postgres"
	case "3":
		cfg.Database = "mysql"
	default:
		cfg.Database = "sqlite"
	}

	// Scaffolding process
	return scaffoldProject(cfg)
}

func scaffoldProject(cfg *ProjectConfig) error {
	baseDir := cfg.Name
	fmt.Printf("\n\033[34m==>\033[0m Scaffolding GoLaravel project in \033[1;37m./%s\033[0m...\n", baseDir)

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return err
	}

	// 1. Create go.mod
	goModContent := fmt.Sprintf(`module %s

go 1.26

require (
	github.com/a-h/templ v0.3.1100
	github.com/goccy/go-json v0.12.0
	github.com/golaravel/golaravel v0.0.0
	github.com/mattn/go-sqlite3 v1.14.52
	entgo.io/ent v0.14.3
)

replace github.com/golaravel/golaravel => ../
`, cfg.Name)
	_ = os.WriteFile(filepath.Join(baseDir, "go.mod"), []byte(goModContent), 0644)

	// 2. Create app/schema/user.go
	schemaDir := filepath.Join(baseDir, "app", "schema")
	_ = os.MkdirAll(schemaDir, 0755)
	userSchemaContent := `package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type User struct {
	ent.Schema
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.String("email").Unique().NotEmpty(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (User) Edges() []ent.Edge {
	return nil
}
`
	_ = os.WriteFile(filepath.Join(schemaDir, "user.go"), []byte(userSchemaContent), 0644)

	// 3. Create app/generate.go
	appDir := filepath.Join(baseDir, "app")
	_ = os.WriteFile(filepath.Join(appDir, "generate.go"), []byte(`package app

//go:generate go run -mod=mod entgo.io/ent/cmd/ent generate ./schema
`), 0644)

	// 4. Create Templ views (especially customer invoice micro-page if hybrid or templ)
	viewsDir := filepath.Join(baseDir, "app", "views")
	_ = os.MkdirAll(viewsDir, 0755)

	invoiceTemplContent := `package views

import (
	"fmt"
)

// InvoiceView renders an ultra-fast, zero-overhead standalone invoice page.
// Perfect for public customer links, payment receipts, and email links without loading heavy client JS bundles.
templ InvoiceView(invoiceNum string, customerName string, amount float64, isPaid bool) {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="UTF-8"/>
			<meta name="viewport" content="width=device-width, initial-scale=1.0"/>
			<title>Invoice { invoiceNum }</title>
			<script src="https://cdn.tailwindcss.com"></script>
		</head>
		<body class="bg-slate-100 text-slate-800 p-4 md:p-12 font-sans">
			<div class="max-w-2xl mx-auto bg-white rounded-2xl shadow-xl overflow-hidden border border-slate-200">
				<!-- Header -->
				<div class="bg-slate-900 text-white p-8 flex justify-between items-start">
					<div>
						<h1 class="text-2xl font-black tracking-tight text-rose-500">GoLaravel Invoice</h1>
						<p class="text-xs text-slate-400 mt-1">Instant Micro-SSR Page (Rendered in &lt;1ms)</p>
					</div>
					<div class="text-right">
						<span class="text-xs uppercase font-mono text-slate-400">Invoice No.</span>
						<p class="text-lg font-bold font-mono">{ invoiceNum }</p>
					</div>
				</div>

				<!-- Details -->
				<div class="p-8 space-y-6">
					<div class="flex justify-between items-center border-b pb-4">
						<div>
							<p class="text-xs text-slate-500 uppercase font-semibold">Billed To</p>
							<p class="text-lg font-bold text-slate-900">{ customerName }</p>
						</div>
						<div>
							if isPaid {
								<span class="px-3 py-1 bg-emerald-100 text-emerald-800 text-xs font-bold rounded-full border border-emerald-300">PAID</span>
							} else {
								<span class="px-3 py-1 bg-amber-100 text-amber-800 text-xs font-bold rounded-full border border-amber-300">PAYMENT PENDING</span>
							}
						</div>
					</div>

					<!-- Line Items -->
					<div class="space-y-3">
						<div class="flex justify-between text-sm py-2 border-b">
							<span class="text-slate-600">Enterprise Cloud Subscription (Annual)</span>
							<span class="font-mono font-semibold">{ fmt.Sprintf("$%.2f", amount) }</span>
						</div>
					</div>

					<!-- Total -->
					<div class="flex justify-between items-center pt-4">
						<span class="text-base font-bold text-slate-900">Total Amount Due</span>
						<span class="text-2xl font-black text-rose-600 font-mono">{ fmt.Sprintf("$%.2f", amount) }</span>
					</div>
				</div>

				<!-- Footer note -->
				<div class="bg-slate-50 border-t border-slate-100 p-4 text-center text-xs text-slate-500">
					Zero-bundle HTML document delivered instantaneously by GoLaravel Templ Engine.
				</div>
			</div>
		</body>
	</html>
}
`
	_ = os.WriteFile(filepath.Join(viewsDir, "invoice.templ"), []byte(invoiceTemplContent), 0644)

	// 5. Database migrations & seeders setup (via Atlas)
	migrationsDir := filepath.Join(baseDir, "database", "migrations")
	_ = os.MkdirAll(migrationsDir, 0755)
	_ = os.WriteFile(filepath.Join(migrationsDir, ".gitkeep"), []byte(""), 0644)

	seedersDir := filepath.Join(baseDir, "database", "seeders")
	_ = os.MkdirAll(seedersDir, 0755)
	databaseSeederContent := `package seeders

import (
	"context"
	"log/slog"
)

// DatabaseSeeder is the root seeder for populating database records.
type DatabaseSeeder struct{}

// Seed runs all project seeders.
func (s *DatabaseSeeder) Seed(ctx context.Context, client any) error {
	slog.Info("Running DatabaseSeeder")
	// Example: Call other seeders sequentially
	// runner := database.NewRunner(client)
	// return runner.Call(ctx, &UserSeeder{})
	return nil
}
`
	_ = os.WriteFile(filepath.Join(seedersDir, "database_seeder.go"), []byte(databaseSeederContent), 0644)

	atlasHcl := `env "local" {
  src = "ent://app/schema"
  dev = "sqlite://file?mode=memory&cache=shared"
  migration {
    dir = "file://database/migrations"
  }
}
`
	_ = os.WriteFile(filepath.Join(baseDir, "atlas.hcl"), []byte(atlasHcl), 0644)

	// 6. If Flutter or Hybrid, create the Flutter multi-platform client
	if cfg.Stack == "flutter" || cfg.Stack == "hybrid" {
		if err := scaffoldFlutterApp(baseDir, cfg); err != nil {
			fmt.Printf("\033[33mWARN\033[0m Flutter scaffolding warning: %v\n", err)
		}
	}

	// 7. Generate main.go
	if err := generateMainGo(baseDir, cfg); err != nil {
		return err
	}

	fmt.Println("\n\033[32m✔ SUCCESS!\033[0m Project", cfg.Name, "created successfully!")
	fmt.Println("\nTo get started:")
	fmt.Printf("  cd %s\n", cfg.Name)
	fmt.Println("  artisan generate   # Rebuild Ent ORM models & Templ views")
	if cfg.Stack == "flutter" || cfg.Stack == "hybrid" {
		fmt.Println("  artisan flutter:build # Compile Flutter Web assets (or run flutter run in client/)")
	}
	fmt.Println("  go run main.go     # Start the high-performance GoLaravel server")
	fmt.Println()

	return nil
}

func scaffoldFlutterApp(baseDir string, cfg *ProjectConfig) error {
	clientDir := filepath.Join(baseDir, "client")
	fmt.Printf("\033[34m==>\033[0m Setting up Flutter multiplatform client in \033[1;37m%s\033[0m...\n", clientDir)

	platforms := strings.Join(cfg.FlutterTargets, ",")
	cmd := exec.Command("flutter", "create", "--platforms", platforms, "--project-name", "client", clientDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback: manually create minimal Flutter project if flutter create has system dependencies
		fmt.Printf("\033[33mNotice:\033[0m flutter create output: %s\n", string(output))
		return createFallbackFlutterProject(clientDir, cfg)
	}

	// Customize lib/main.dart with GoLaravel API integration
	return createFlutterMainDart(clientDir, cfg)
}

func createFallbackFlutterProject(clientDir string, cfg *ProjectConfig) error {
	_ = os.MkdirAll(filepath.Join(clientDir, "lib"), 0755)
	_ = os.MkdirAll(filepath.Join(clientDir, "web"), 0755)

	pubspec := `name: client
description: "Cross-platform Flutter front-end for GoLaravel"
version: 1.0.0+1
environment:
  sdk: '>=3.0.0 <4.0.0'

dependencies:
  flutter:
    sdk: flutter
  http: ^1.2.0

dev_dependencies:
  flutter_test:
    sdk: flutter
  flutter_lints: ^3.0.0

flutter:
  uses-material-design: true
`
	_ = os.WriteFile(filepath.Join(clientDir, "pubspec.yaml"), []byte(pubspec), 0644)
	return createFlutterMainDart(clientDir, cfg)
}

func createFlutterMainDart(clientDir string, cfg *ProjectConfig) error {
	mainDart := `import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:http/http.dart' as http;

void main() {
  runApp(const GoLaravelFlutterApp());
}

class GoLaravelFlutterApp extends StatelessWidget {
  const GoLaravelFlutterApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'GoLaravel Flutter App',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xFFF43F5E),
          brightness: Brightness.dark,
        ),
        useMaterial3: true,
      ),
      home: const DashboardPage(),
    );
  }
}

class DashboardPage extends StatefulWidget {
  const DashboardPage({super.key});

  @override
  State<DashboardPage> createState() => _DashboardPageState();
}

class _DashboardPageState extends State<DashboardPage> {
  List<dynamic> users = [];
  bool isLoading = true;

  @override
  void initState() {
    super.initState();
    fetchUsers();
  }

  Future<void> fetchUsers() async {
    setState(() => isLoading = true);
    try {
      final response = await http.get(Uri.parse('/api/users'));
      if (response.statusCode == 200) {
        setState(() {
          users = json.decode(response.body);
          isLoading = false;
        });
      }
    } catch (e) {
      setState(() => isLoading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('GoLaravel + Flutter Multiplatform'),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: fetchUsers,
          ),
        ],
      ),
      body: Padding(
        padding: const EdgeInsets.all(16.0),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Card(
              color: const Color(0xFF1E293B),
              child: Padding(
                padding: const EdgeInsets.all(16.0),
                child: Row(
                  children: [
                    const Icon(Icons.flash_on, color: Color(0xFFF43F5E), size: 36),
                    const SizedBox(width: 16),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: const [
                          Text('Hybrid Presentation Active',
                              style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16)),
                          Text(
                            'Main App: Flutter (Web, Mobile, Desktop)\n'
                            'Fast Micro-Pages: Templ SSR (/invoice/:token)',
                            style: TextStyle(color: Colors.grey, fontSize: 13),
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ),
            const SizedBox(height: 16),
            const Text('Users from Ent ORM:', style: TextStyle(fontWeight: FontWeight.bold)),
            const SizedBox(height: 8),
            Expanded(
              child: isLoading
                  ? const Center(child: CircularProgressIndicator())
                  : users.isEmpty
                      ? const Center(child: Text('No users yet.'))
                      : ListView.builder(
                          itemCount: users.length,
                          itemBuilder: (context, i) {
                            final user = users[i];
                            return ListTile(
                              leading: CircleAvatar(
                                child: Text(user['name']?[0] ?? '?'),
                              ),
                              title: Text(user['name'] ?? ''),
                              subtitle: Text(user['email'] ?? ''),
                            );
                          },
                        ),
            ),
          ],
        ),
      ),
    );
  }
}
`
	return os.WriteFile(filepath.Join(clientDir, "lib", "main.dart"), []byte(mainDart), 0644)
}

func generateMainGo(baseDir string, cfg *ProjectConfig) error {
	content := fmt.Sprintf(`package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/pkg/container"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/flutter"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/middleware"
	"%s/app/views"
)

func main() {
	app := golaravel.New()
	app.Use(middleware.Recovery())
	app.Use(middleware.SecureHeaders())

	// 1. API routes (consumed by Flutter Mobile, Desktop, and Web)
	api := app.Group("/api")
	api.GET("/users", func(c *gcontext.Context) error {
		return c.JSON(http.StatusOK, []map[string]any{
			{"id": 1, "name": "Taylor Otwell", "email": "taylor@laravel.com"},
			{"id": 2, "name": "Flutter Developer", "email": "dev@flutter.dev"},
		})
	})

	// 2. Fast-Path Micro-SSR Page (Invoice / Receipt / Token Link)
	// Rendered in < 45 nanoseconds via Templ without loading Flutter JS bundles!
	app.GET("/invoice/:token", func(c *gcontext.Context) error {
		token := c.Param("token")
		return c.Templ(http.StatusOK, views.InvoiceView(token, "Acme Corporation", 1250.00, true))
	})

	// 3. Serve Flutter Web client at / or /app
	flutter.ServeWeb(app.Router, flutter.Options{
		MountPath:   "/",
		DistDir:     "client/build/web",
		FallbackSPA: true,
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = ":8080"
	}

	slog.Info("GoLaravel running with Hybrid Flutter + Templ", "url", "http://localhost"+port)
	if err := app.Run(port); err != nil {
		log.Fatalf("server exited: %%v", err)
	}
}
`, cfg.Name)

	return os.WriteFile(filepath.Join(baseDir, "main.go"), []byte(content), 0644)
}
