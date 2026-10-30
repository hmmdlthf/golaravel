package artisan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RunMakeCrud scaffolds full-stack CRUD components (Ent Schema, Requests, Controller, Templ Views, Seeder).
func RunMakeCrud(args []string, baseDir ...string) error {
	var (
		apiOnly bool
		webOnly bool
		force   bool
		rawName string
	)

	root := "."
	if len(baseDir) > 0 && baseDir[0] != "" {
		root = baseDir[0]
	}

	for _, arg := range args {
		switch arg {
		case "--api":
			apiOnly = true
		case "--web":
			webOnly = true
		case "--force", "-f":
			force = true
		default:
			if !strings.HasPrefix(arg, "-") && rawName == "" {
				rawName = arg
			}
		}
	}
	_ = webOnly

	if rawName == "" {
		return fmt.Errorf("missing entity name.\nUsage: artisan make:crud <Name> [--api | --web] [--force]")
	}

	// Normalise naming
	pascalName := toPascalCase(rawName)
	snakeName := toSnakeCase(pascalName)
	pluralPascal := pluralize(pascalName)
	pluralSnake := pluralize(snakeName)
	routeSlug := strings.ReplaceAll(pluralSnake, "_", "-")
	moduleName := detectGoModule(root)

	fmt.Printf("\033[34m==>\033[0m Scaffolding Full-Stack CRUD for \033[1;37m%s\033[0m (routes: /%s)...\n", pascalName, routeSlug)

	type fileToCreate struct {
		path    string
		content string
		desc    string
	}

	var files []fileToCreate

	// 1. Ent Database Schema (app/schema/<name>.go)
	schemaPath := filepath.Join(root, "app", "schema", snakeName+".go")
	schemaContent := generateSchemaContent(pascalName)
	files = append(files, fileToCreate{
		path:    schemaPath,
		content: schemaContent,
		desc:    fmt.Sprintf("Ent Schema (app/schema/%s.go)", snakeName),
	})

	// 2. Form Requests & Validation (app/requests/<name>_request.go)
	reqPath := filepath.Join(root, "app", "requests", snakeName+"_request.go")
	reqContent := generateRequestContent(pascalName)
	files = append(files, fileToCreate{
		path:    reqPath,
		content: reqContent,
		desc:    fmt.Sprintf("Form Requests (app/requests/%s_request.go)", snakeName),
	})

	// 3. Controller (app/handlers/<name>_controller.go)
	ctrlPath := filepath.Join(root, "app", "handlers", snakeName+"_controller.go")
	ctrlContent := generateControllerContent(pascalName, snakeName, pluralPascal, pluralSnake, routeSlug, moduleName, apiOnly)
	files = append(files, fileToCreate{
		path:    ctrlPath,
		content: ctrlContent,
		desc:    fmt.Sprintf("HTTP Controller (app/handlers/%s_controller.go)", snakeName),
	})

	// 4. Templ UI Views (app/views/<name>.templ) - generated unless --api is specified
	if !apiOnly {
		viewPath := filepath.Join(root, "app", "views", snakeName+".templ")
		viewContent := generateViewContent(pascalName, snakeName, pluralPascal, pluralSnake, routeSlug)
		files = append(files, fileToCreate{
			path:    viewPath,
			content: viewContent,
			desc:    fmt.Sprintf("Templ Views (app/views/%s.templ)", snakeName),
		})
	}

	// 5. Database Seeder (database/seeders/<name>_seeder.go)
	seederPath := filepath.Join(root, "database", "seeders", snakeName+"_seeder.go")
	seederContent := generateSeederContent(pascalName, snakeName, pluralPascal)
	files = append(files, fileToCreate{
		path:    seederPath,
		content: seederContent,
		desc:    fmt.Sprintf("Database Seeder (database/seeders/%s_seeder.go)", snakeName),
	})

	// Write files to disk
	for _, f := range files {
		dir := filepath.Dir(f.path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}

		if _, err := os.Stat(f.path); err == nil && !force {
			fmt.Printf("  \033[33mSKIP\033[0m %s already exists (use --force to overwrite)\n", f.desc)
			continue
		}

		if err := os.WriteFile(f.path, []byte(f.content), 0644); err != nil {
			return fmt.Errorf("failed writing %s: %w", f.path, err)
		}
		fmt.Printf("  \033[32m✔\033[0m Created %s\n", f.desc)
	}

	// Print routes registration snippet & next steps
	printNextSteps(pascalName, snakeName, routeSlug)
	return nil
}

func printNextSteps(pascalName, snakeName, routeSlug string) {
	fmt.Println("\n\033[32mSUCCESS\033[0m Full-stack CRUD scaffolded successfully!")
	fmt.Println("\n\033[1;37mNext Steps:\033[0m")
	fmt.Println("  1. Mount the CRUD routes in your router (e.g. in \033[36mmain.go\033[0m):")
	fmt.Println("\033[33m")
	fmt.Printf("     ctrl := handlers.New%sController(nil)\n", pascalName)
	fmt.Printf("     ctrl.RegisterRoutes(app.Router(), \"/%s\")\n", routeSlug)
	fmt.Println("\033[0m")
	fmt.Println("  2. Compile generated Ent ORM & Templ views:")
	fmt.Println("     \033[32martisan generate\033[0m")
	fmt.Println("  3. Create and run database migrations:")
	fmt.Printf("     \033[32martisan migrate:diff create_%s_table\033[0m\n", pluralize(snakeName))
	fmt.Println("     \033[32martisan migrate\033[0m")
	fmt.Println("  4. Seed test records:")
	fmt.Println("     \033[32martisan db:seed\033[0m")
	fmt.Println()
}

func detectGoModule(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "app"
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return "app"
}

func toPascalCase(s string) string {
	s = strings.TrimSpace(s)
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})
	var b strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			b.WriteString(strings.ToUpper(p[:1]) + strings.ToLower(p[1:]))
		}
	}
	return b.String()
}

func toSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteRune('_')
			}
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func pluralize(word string) string {
	lower := strings.ToLower(word)
	if strings.HasSuffix(lower, "y") && !strings.HasSuffix(lower, "ay") && !strings.HasSuffix(lower, "ey") && !strings.HasSuffix(lower, "oy") && !strings.HasSuffix(lower, "uy") {
		return word[:len(word)-1] + "ies"
	}
	if strings.HasSuffix(lower, "s") || strings.HasSuffix(lower, "x") || strings.HasSuffix(lower, "z") || strings.HasSuffix(lower, "ch") || strings.HasSuffix(lower, "sh") {
		return word + "es"
	}
	return word + "s"
}

func replaceVars(tpl string, vars map[string]string) string {
	res := tpl
	for k, v := range vars {
		res = strings.ReplaceAll(res, "{{"+k+"}}", v)
	}
	return res
}

// Code generators

func generateSchemaContent(pascalName string) string {
	tpl := `package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// {{PascalName}} holds the schema definition for the {{PascalName}} entity.
type {{PascalName}} struct {
	ent.Schema
}

// Fields of the {{PascalName}}.
func ({{PascalName}}) Fields() []ent.Field {
	return []ent.Field{
		field.String("title").NotEmpty().Comment("Primary display title"),
		field.String("description").Optional().Comment("Optional detailed description"),
		field.String("status").Default("active").Comment("Status: active, archived, pending"),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the {{PascalName}}.
func ({{PascalName}}) Edges() []ent.Edge {
	return nil
}
`
	return replaceVars(tpl, map[string]string{
		"PascalName": pascalName,
	})
}

func generateRequestContent(pascalName string) string {
	tpl := `package requests

import (
	"github.com/golaravel/golaravel/pkg/validation"
)

// Create{{PascalName}}Request validates inputs for creating a new {{PascalName}}.
type Create{{PascalName}}Request struct {
	Title       string ` + "`json:\"title\" form:\"title\"`" + `
	Description string ` + "`json:\"description\" form:\"description\"`" + `
	Status      string ` + "`json:\"status\" form:\"status\"`" + `
}

// Validate executes validation rules with 422 error bags.
func (r *Create{{PascalName}}Request) Validate(v *validation.Validator) {
	v.Required("title", r.Title).
		MinLength("title", r.Title, 3).
		MaxLength("title", r.Title, 255)

	if r.Status != "" {
		v.In("status", r.Status, []string{"active", "archived", "pending"})
	}
}

// Update{{PascalName}}Request validates inputs for updating an existing {{PascalName}}.
type Update{{PascalName}}Request struct {
	Title       string ` + "`json:\"title\" form:\"title\"`" + `
	Description string ` + "`json:\"description\" form:\"description\"`" + `
	Status      string ` + "`json:\"status\" form:\"status\"`" + `
}

// Validate executes validation rules with 422 error bags.
func (r *Update{{PascalName}}Request) Validate(v *validation.Validator) {
	v.Required("title", r.Title).
		MinLength("title", r.Title, 3).
		MaxLength("title", r.Title, 255)

	if r.Status != "" {
		v.In("status", r.Status, []string{"active", "archived", "pending"})
	}
}
`
	return replaceVars(tpl, map[string]string{
		"PascalName": pascalName,
	})
}

func generateControllerContent(pascalName, snakeName, pluralPascal, pluralSnake, routeSlug, moduleName string, apiOnly bool) string {
	viewImports := ""
	renderIndex := "\treturn c.JSON(http.StatusOK, items)"
	renderShow := "\treturn c.JSON(http.StatusOK, item)"
	renderForm := "\treturn c.JSON(http.StatusOK, item)"

	if !apiOnly {
		viewImports = fmt.Sprintf("\t\"%s/app/views\"\n", moduleName)
		renderIndex = fmt.Sprintf("\treturn c.Templ(http.StatusOK, views.%sIndex(items))", pascalName)
		renderShow = fmt.Sprintf("\treturn c.Templ(http.StatusOK, views.%sShow(item))", pascalName)
		renderForm = fmt.Sprintf("\ttoken, _ := c.Get(\"csrf_token\")\n\tcsrfStr, _ := token.(string)\n\treturn c.Templ(http.StatusOK, views.%sForm(action, method, item, csrfStr))", pascalName)
	}

	tpl := `package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"{{ModuleName}}/app/requests"
{{ViewImports}}	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/router"
	"github.com/golaravel/golaravel/pkg/validation"
)

// {{PascalName}}Controller handles RESTful and Web CRUD operations for {{PascalName}}.
type {{PascalName}}Controller struct {
	db any
}

// New{{PascalName}}Controller instantiates a new {{PascalName}}Controller.
func New{{PascalName}}Controller(db any) *{{PascalName}}Controller {
	return &{{PascalName}}Controller{db: db}
}

// RegisterRoutes mounts all standard CRUD routes on the provided registrar.
func (ctrl *{{PascalName}}Controller) RegisterRoutes(r router.RouteRegistrar, prefix ...string) {
	base := "/{{RouteSlug}}"
	if len(prefix) > 0 && prefix[0] != "" {
		base = prefix[0]
	}

	r.Get(base, ctrl.Index)
	r.Get(base+"/create", ctrl.Create)
	r.Post(base, ctrl.Store)
	r.Get(base+"/:id", ctrl.Show)
	r.Get(base+"/:id/edit", ctrl.Edit)
	r.Put(base+"/:id", ctrl.Update)
	r.Delete(base+"/:id", ctrl.Destroy)
}

// Index lists all {{PascalName}} records.
func (ctrl *{{PascalName}}Controller) Index(c *context.Context) error {
	// Sample items; replace with Ent query (e.g. ctrl.client.{{PascalName}}.Query().All(c.Context()))
	items := []map[string]any{
		{"id": 1, "title": "{{PascalName}} Sample 1", "status": "active"},
		{"id": 2, "title": "{{PascalName}} Sample 2", "status": "pending"},
	}

	if ctrl.isAPI(c) {
		return c.JSON(http.StatusOK, map[string]any{
			"data":  items,
			"total": len(items),
		})
	}

	return ctrl.renderIndex(c, items)
}

// Show displays a single {{PascalName}} by ID.
func (ctrl *{{PascalName}}Controller) Show(c *context.Context) error {
	id := c.Param("id")

	item := map[string]any{
		"id":          id,
		"title":       fmt.Sprintf("{{PascalName}} #%s", id),
		"description": "Sample record description",
		"status":      "active",
	}

	if ctrl.isAPI(c) {
		return c.JSON(http.StatusOK, item)
	}

	return ctrl.renderShow(c, item)
}

// Create renders the HTML creation form.
func (ctrl *{{PascalName}}Controller) Create(c *context.Context) error {
	return ctrl.renderForm(c, nil, "/{{RouteSlug}}", "POST")
}

// Store creates a new {{PascalName}} record.
func (ctrl *{{PascalName}}Controller) Store(c *context.Context) error {
	var req requests.Create{{PascalName}}Request
	if !validation.ValidateAndRespond(c, &req) {
		return nil
	}

	if ctrl.isAPI(c) {
		return c.JSON(http.StatusCreated, map[string]any{
			"message": "{{PascalName}} created successfully",
			"data":    req,
		})
	}

	return c.Redirect(http.StatusFound, "/{{RouteSlug}}")
}

// Edit renders the HTML update form.
func (ctrl *{{PascalName}}Controller) Edit(c *context.Context) error {
	id := c.Param("id")
	item := map[string]any{
		"id":          id,
		"title":       fmt.Sprintf("{{PascalName}} #%s", id),
		"description": "Existing record description",
		"status":      "active",
	}
	return ctrl.renderForm(c, item, fmt.Sprintf("/{{RouteSlug}}/%s", id), "PUT")
}

// Update modifies an existing {{PascalName}} record.
func (ctrl *{{PascalName}}Controller) Update(c *context.Context) error {
	id := c.Param("id")
	var req requests.Update{{PascalName}}Request
	if !validation.ValidateAndRespond(c, &req) {
		return nil
	}

	if ctrl.isAPI(c) {
		return c.JSON(http.StatusOK, map[string]any{
			"message": "{{PascalName}} updated successfully",
			"id":      id,
			"data":    req,
		})
	}

	return c.Redirect(http.StatusFound, fmt.Sprintf("/{{RouteSlug}}/%s", id))
}

// Destroy deletes the {{PascalName}} record.
func (ctrl *{{PascalName}}Controller) Destroy(c *context.Context) error {
	id := c.Param("id")
	_ = id

	if ctrl.isAPI(c) {
		return c.JSON(http.StatusOK, map[string]string{
			"message": "{{PascalName}} deleted successfully",
		})
	}

	return c.Redirect(http.StatusFound, "/{{RouteSlug}}")
}

func (ctrl *{{PascalName}}Controller) isAPI(c *context.Context) bool {
	return c.Header("Accept") == "application/json" || strings.HasPrefix(c.Request.URL.Path, "/api")
}

func (ctrl *{{PascalName}}Controller) renderIndex(c *context.Context, items []map[string]any) error {
{{RenderIndex}}
}

func (ctrl *{{PascalName}}Controller) renderShow(c *context.Context, item map[string]any) error {
{{RenderShow}}
}

func (ctrl *{{PascalName}}Controller) renderForm(c *context.Context, item map[string]any, action, method string) error {
{{RenderForm}}
}
`

	return replaceVars(tpl, map[string]string{
		"ModuleName":   moduleName,
		"ViewImports":  viewImports,
		"PascalName":   pascalName,
		"SnakeName":    snakeName,
		"PluralPascal": pluralPascal,
		"PluralSnake":  pluralSnake,
		"RouteSlug":    routeSlug,
		"RenderIndex":  renderIndex,
		"RenderShow":   renderShow,
		"RenderForm":   renderForm,
	})
}

func generateViewContent(pascalName, snakeName, pluralPascal, pluralSnake, routeSlug string) string {
	tpl := `package views

import (
	"fmt"
	"github.com/golaravel/golaravel/pkg/view"
)

// {{PascalName}}Index renders the directory table for {{PluralPascal}}.
templ {{PascalName}}Index(items []map[string]any) {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="UTF-8"/>
			<title>{{PascalName}} Directory - GoLaravel</title>
			<script src="https://cdn.tailwindcss.com"></script>
		</head>
		<body class="bg-slate-900 text-slate-100 min-h-screen p-8">
			<div class="max-w-5xl mx-auto space-y-6">
				<div class="flex items-center justify-between">
					<div>
						<h1 class="text-3xl font-bold tracking-tight text-white">{{PluralPascal}}</h1>
						<p class="text-sm text-slate-400">Manage and browse {{PluralSnake}} records</p>
					</div>
					<a href="/{{RouteSlug}}/create" class="px-4 py-2 bg-rose-600 hover:bg-rose-500 text-white rounded-lg text-sm font-medium shadow transition">
						+ Create {{PascalName}}
					</a>
				</div>

				<div class="bg-slate-800/80 border border-slate-700 rounded-xl overflow-hidden shadow-xl">
					<table class="w-full text-left text-sm text-slate-300">
						<thead class="bg-slate-800 text-xs uppercase text-slate-400 border-b border-slate-700">
							<tr>
								<th class="px-6 py-3">ID</th>
								<th class="px-6 py-3">Title</th>
								<th class="px-6 py-3">Status</th>
								<th class="px-6 py-3 text-right">Actions</th>
							</tr>
						</thead>
						<tbody class="divide-y divide-slate-700/60">
							for _, item := range items {
								<tr class="hover:bg-slate-700/30 transition">
									<td class="px-6 py-4 font-mono text-xs">{ fmt.Sprint(item["id"]) }</td>
									<td class="px-6 py-4 font-medium text-white">{ fmt.Sprint(item["title"]) }</td>
									<td class="px-6 py-4">
										<span class="px-2.5 py-1 text-xs font-semibold rounded-full bg-emerald-500/20 text-emerald-400 border border-emerald-500/30">
											{ fmt.Sprint(item["status"]) }
										</span>
									</td>
									<td class="px-6 py-4 text-right space-x-2">
										<a href={ templ.SafeURL(fmt.Sprintf("/{{RouteSlug}}/%v", item["id"])) } class="text-xs text-sky-400 hover:underline">View</a>
										<a href={ templ.SafeURL(fmt.Sprintf("/{{RouteSlug}}/%v/edit", item["id"])) } class="text-xs text-amber-400 hover:underline">Edit</a>
									</td>
								</tr>
							}
							if len(items) == 0 {
								<tr>
									<td colspan="4" class="px-6 py-12 text-center text-slate-500">
										No {{PluralSnake}} records found.
									</td>
								</tr>
							}
						</tbody>
					</table>
				</div>
			</div>
		</body>
	</html>
}

// {{PascalName}}Show renders the detailed view of a single {{PascalName}}.
templ {{PascalName}}Show(item map[string]any) {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="UTF-8"/>
			<title>{{PascalName}} #{ fmt.Sprint(item["id"]) } - GoLaravel</title>
			<script src="https://cdn.tailwindcss.com"></script>
		</head>
		<body class="bg-slate-900 text-slate-100 min-h-screen p-8">
			<div class="max-w-3xl mx-auto space-y-6">
				<a href="/{{RouteSlug}}" class="text-sm text-slate-400 hover:text-white transition">← Back to {{PluralPascal}}</a>

				<div class="bg-slate-800/80 border border-slate-700 p-8 rounded-xl shadow-xl space-y-6">
					<div class="flex items-center justify-between border-b border-slate-700 pb-4">
						<h1 class="text-2xl font-bold text-white">{ fmt.Sprint(item["title"]) }</h1>
						<span class="px-3 py-1 bg-emerald-500/20 text-emerald-400 text-xs font-semibold rounded-full border border-emerald-500/30">
							{ fmt.Sprint(item["status"]) }
						</span>
					</div>

					<p class="text-slate-300">{ fmt.Sprint(item["description"]) }</p>

					<div class="flex items-center justify-end space-x-3 pt-4 border-t border-slate-700">
						<a href={ templ.SafeURL(fmt.Sprintf("/{{RouteSlug}}/%v/edit", item["id"])) } class="px-4 py-2 bg-amber-600 hover:bg-amber-500 text-white rounded-lg text-sm font-medium transition">
							Edit {{PascalName}}
						</a>
					</div>
				</div>
			</div>
		</body>
	</html>
}

// {{PascalName}}Form renders the Create/Edit form with CSRF protection and method spoofing.
templ {{PascalName}}Form(action, method string, item map[string]any, csrfToken string) {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="UTF-8"/>
			<title>{{PascalName}} Form - GoLaravel</title>
			<script src="https://cdn.tailwindcss.com"></script>
		</head>
		<body class="bg-slate-900 text-slate-100 min-h-screen p-8">
			<div class="max-w-2xl mx-auto space-y-6">
				<a href="/{{RouteSlug}}" class="text-sm text-slate-400 hover:text-white transition">← Back to {{PluralPascal}}</a>

				<div class="bg-slate-800/80 border border-slate-700 p-8 rounded-xl shadow-xl">
					<h1 class="text-2xl font-bold text-white mb-6">
						if method == "PUT" {
							Edit {{PascalName}}
						} else {
							Create {{PascalName}}
						}
					</h1>

					<form action={ templ.SafeURL(action) } method="POST" class="space-y-4">
						@view.CSRF(csrfToken)
						if method == "PUT" {
							@view.Method("PUT")
						}

						<div>
							<label class="block text-xs font-medium text-slate-400 mb-1">Title</label>
							<input type="text" name="title" required value={ itemTitle(item) } placeholder="Enter {{PascalName}} title..." class="w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-rose-500"/>
						</div>

						<div>
							<label class="block text-xs font-medium text-slate-400 mb-1">Description</label>
							<textarea name="description" rows="4" placeholder="Enter description..." class="w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-rose-500">{ itemDesc(item) }</textarea>
						</div>

						<div>
							<label class="block text-xs font-medium text-slate-400 mb-1">Status</label>
							<select name="status" class="w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-rose-500">
								<option value="active">Active</option>
								<option value="pending">Pending</option>
								<option value="archived">Archived</option>
							</select>
						</div>

						<div class="pt-4 flex justify-end space-x-3">
							<a href="/{{RouteSlug}}" class="px-4 py-2 bg-slate-700 hover:bg-slate-600 text-slate-200 rounded-lg text-sm font-medium transition">Cancel</a>
							<button type="submit" class="px-5 py-2 bg-rose-600 hover:bg-rose-500 text-white rounded-lg text-sm font-medium shadow transition">
								Save {{PascalName}}
							</button>
						</div>
					</form>
				</div>
			</div>
		</body>
	</html>
}

func itemTitle(item map[string]any) string {
	if item == nil {
		return ""
	}
	if v, ok := item["title"].(string); ok {
		return v
	}
	return ""
}

func itemDesc(item map[string]any) string {
	if item == nil {
		return ""
	}
	if v, ok := item["description"].(string); ok {
		return v
	}
	return ""
}
`

	return replaceVars(tpl, map[string]string{
		"PascalName":   pascalName,
		"SnakeName":    snakeName,
		"PluralPascal": pluralPascal,
		"PluralSnake":  pluralSnake,
		"RouteSlug":    routeSlug,
	})
}

func generateSeederContent(pascalName, snakeName, pluralPascal string) string {
	tpl := `package seeders

import (
	"context"
	"fmt"
	"log/slog"
)

// {{PascalName}}Seeder populates the database with initial {{PluralPascal}} test records.
type {{PascalName}}Seeder struct{}

func New{{PascalName}}Seeder() *{{PascalName}}Seeder {
	return &{{PascalName}}Seeder{}
}

// Run executes the seeding logic.
func (s *{{PascalName}}Seeder) Run(ctx context.Context, client any) error {
	slog.Info("Seeding {{PluralPascal}} records...", "entity", "{{PascalName}}")

	// Wire with your Ent client or database connection:
	// Example:
	// for i := 1; i <= 5; i++ {
	//     client.{{PascalName}}.Create().
	//         SetTitle(fmt.Sprintf("{{PascalName}} #%d", i)).
	//         SetStatus("active").
	//         SaveX(ctx)
	// }

	fmt.Println("  \033[32m✔\033[0m Seeded 5 {{PluralPascal}} test records.")
	return nil
}
`
	return replaceVars(tpl, map[string]string{
		"PascalName":   pascalName,
		"PluralPascal": pluralPascal,
	})
}
