package artisan

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// make:syncable scaffolds an entity that offline clients sync (pkg/offline):
// an Ent schema with SyncableMixin, TenantMixin and SoftDeleteMixin, and an
// offline.Resource that loads, snapshots and applies it through the
// generated Ent client.
//
//	artisan make:syncable Product name:string price:int64 stock:counter

type syncField struct {
	Name    string // snake_case column
	Pascal  string // Ent's Go name
	Type    string // string, int, int64, float, bool, time, counter
	GoType  string
	EntCall string // field.String("name").Default("")
	Counter bool
}

var syncTypes = map[string]struct{ goType, ent string }{
	"string":  {"string", `field.String(%q).Default("")`},
	"text":    {"string", `field.Text(%q).Default("")`},
	"int":     {"int", `field.Int(%q).Default(0)`},
	"int64":   {"int64", `field.Int64(%q).Default(0)`},
	"float":   {"float64", `field.Float(%q).Default(0)`},
	"bool":    {"bool", `field.Bool(%q).Default(false)`},
	"time":    {"time.Time", `field.Time(%q).Optional().Nillable()`},
	"counter": {"float64", `field.Float(%q).Default(0)`},
}

var syncFieldName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Ent upper-cases these words in Go names (entc/gen's acronym list).
var entAcronyms = map[string]bool{
	"acl": true, "api": true, "ascii": true, "aws": true, "cpu": true, "css": true, "dns": true, "eof": true,
	"gb": true, "guid": true, "hcl": true, "html": true, "http": true, "https": true, "id": true, "ip": true,
	"json": true, "kb": true, "lhs": true, "mac": true, "mb": true, "qps": true, "ram": true, "rhs": true,
	"rpc": true, "sla": true, "smtp": true, "sql": true, "ssh": true, "sso": true, "tcp": true, "tls": true,
	"ttl": true, "udp": true, "ui": true, "uid": true, "uri": true, "url": true, "utf8": true, "uuid": true,
	"vm": true, "xml": true, "xmpp": true, "xsrf": true, "xss": true,
}

// syncTypeName turns "sale_line", "sale-line" or "SaleLine" into
// "SaleLine" (toPascalCase would give "Saleline" for the last one).
func syncTypeName(name string) string {
	if strings.ContainsAny(name, "_- ") {
		return entPascal(strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(name)))
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func entPascal(snake string) string {
	var b strings.Builder
	for _, p := range strings.Split(snake, "_") {
		if p == "" {
			continue
		}
		if entAcronyms[p] {
			b.WriteString(strings.ToUpper(p))
		} else {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return b.String()
}

var reservedSyncFields = map[string]bool{"id": true, "tenant_id": true, "deleted_at": true}

func parseSyncFields(specs []string) ([]syncField, error) {
	var out []syncField
	seen := map[string]bool{}
	for _, s := range specs {
		name, typ, ok := strings.Cut(s, ":")
		if !ok {
			typ = "string"
		}
		t, known := syncTypes[typ]
		switch {
		case !syncFieldName.MatchString(name):
			return nil, fmt.Errorf("field %q: names are snake_case", name)
		case !known:
			return nil, fmt.Errorf("field %q: unknown type %q (string, text, int, int64, float, bool, time, counter)", name, typ)
		case reservedSyncFields[name] || strings.HasPrefix(name, "sync_"):
			return nil, fmt.Errorf("field %q is added by the mixins", name)
		case seen[name]:
			return nil, fmt.Errorf("field %q given twice", name)
		}
		seen[name] = true
		out = append(out, syncField{Name: name, Pascal: entPascal(name), Type: typ, GoType: t.goType,
			EntCall: fmt.Sprintf(t.ent, name), Counter: typ == "counter"})
	}
	return out, nil
}

// RunMakeSyncable implements `artisan make:syncable <Name> [field:type ...] [--force]`.
func RunMakeSyncable(args []string, baseDir ...string) error {
	root := "."
	if len(baseDir) > 0 && baseDir[0] != "" {
		root = baseDir[0]
	}
	var name string
	var specs []string
	force := false
	for _, a := range args {
		switch {
		case a == "--force" || a == "-f":
			force = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %s", a)
		case name == "":
			name = a
		default:
			specs = append(specs, a)
		}
	}
	if name == "" {
		return fmt.Errorf("missing entity name.\nUsage: artisan make:syncable <Name> [field:type ...] [--force]\nTypes: string, text, int, int64, float, bool, time, counter")
	}
	fields, err := parseSyncFields(specs)
	if err != nil {
		return err
	}
	if len(fields) == 0 {
		// An empty Fields list would let clients set any column.
		return fmt.Errorf("give at least one field, e.g. artisan make:syncable %s name:string", name)
	}
	pascal := syncTypeName(name)
	snake := toSnakeCase(pascal)
	data := map[string]any{
		"Name":      pascal,
		"Snake":     snake,
		"Pkg":       strings.ToLower(pascal), // Ent's package for the entity
		"AppImport": appImportPath(root),
		"Fields":    fields,
	}

	files := []struct{ path, tpl, desc string }{
		{filepath.Join(root, "app", "schema", snake+".go"), syncSchemaTpl, "Ent schema"},
		{filepath.Join(root, "app", "synced", snake+".go"), syncResourceTpl, "offline.Resource"},
	}
	for _, f := range files {
		src, err := renderGo(f.tpl, data)
		if err != nil {
			return fmt.Errorf("make:syncable: %s: %w", f.desc, err)
		}
		if _, err := os.Stat(f.path); err == nil && !force {
			fmt.Printf("  \033[33mSKIP\033[0m %s already exists (use --force to overwrite)\n", f.path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(f.path, src, 0o644); err != nil {
			return err
		}
		fmt.Printf("  \033[32m✔\033[0m Created %s (%s)\n", f.path, f.desc)
	}

	patched, err := ensureExecQuery(filepath.Join(root, "app", "generate.go"))
	if err != nil {
		fmt.Printf("  \033[33mNOTE\033[0m add --feature sql/execquery to your ent generate command (%v)\n", err)
	} else if patched {
		fmt.Printf("  \033[32m✔\033[0m Enabled the Ent sql/execquery feature in app/generate.go\n")
	}

	fmt.Printf(`
Next steps:
  1. artisan generate
  2. Register the resource and the change hook where you build the engine:

       eng.Register(synced.%[1]s())
       client.%[1]s.Use(offline.ChangeHook(eng, %[2]q))
       client.%[1]s.Intercept(privacy.TenantScope(func(q *app.%[1]sQuery, t string) { q.Where(%[3]s.TenantID(t)) }))
       client.%[1]s.Use(privacy.TenantMutationHook())

  3. Add %[2]q to the client's entities list.
`, pascal, snake, strings.ToLower(pascal))
	return nil
}

// appImportPath is the import path of root/app: the module of the nearest
// go.mod at or above root, plus root's path inside that module.
func appImportPath(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return detectGoModule(root) + "/app"
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			rel, _ := filepath.Rel(dir, abs)
			p := detectGoModule(dir)
			if rel != "." {
				p += "/" + filepath.ToSlash(rel)
			}
			return p + "/app"
		}
		if filepath.Dir(dir) == dir {
			return detectGoModule(root) + "/app"
		}
	}
}

func renderGo(tpl string, data any) ([]byte, error) {
	t, err := template.New("").Parse(tpl)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	src, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%w\n%s", err, buf.String())
	}
	return src, nil
}

var entGenerateLine = regexp.MustCompile(`(?m)^(//go:generate .*entgo\.io/ent/cmd/ent generate)( .*)$`)

// ensureExecQuery adds --feature sql/execquery to app/generate.go's ent
// command, which the sync engine needs to share Ent's transaction.
func ensureExecQuery(path string) (bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if bytes.Contains(src, []byte("sql/execquery")) {
		return false, nil
	}
	if !entGenerateLine.Match(src) {
		return false, fmt.Errorf("no ent generate line in %s", path)
	}
	out := entGenerateLine.ReplaceAll(src, []byte("$1 --feature sql/execquery$2"))
	return true, os.WriteFile(path, out, 0o644)
}

const syncSchemaTpl = `// Code generated by artisan make:syncable. Edit freely.

package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"

	offline "github.com/golaravel/golaravel/pkg/offline/schema"
	"github.com/golaravel/golaravel/pkg/privacy"
)

// {{.Name}} is synced to offline clients. Ids are strings the clients
// generate; deletes leave a tombstone (deleted_at) devices can pull.
type {{.Name}} struct{ ent.Schema }

// Mixin of the {{.Name}}.
func ({{.Name}}) Mixin() []ent.Mixin {
	return []ent.Mixin{offline.SyncableMixin{}, privacy.TenantMixin{}, privacy.SoftDeleteMixin{}}
}

// Fields of the {{.Name}}.
func ({{.Name}}) Fields() []ent.Field {
	return []ent.Field{
{{- range .Fields}}
		{{.EntCall}},{{if .Counter}} // counter: devices send deltas{{end}}
{{- end}}
	}
}
`

const syncResourceTpl = `// Code generated by artisan make:syncable. Edit freely: add validation
// (return offline.Reject), an Authorize rule or another Resolver.

package synced

import (
	"context"
	"time"

	"{{.AppImport}}"
	"{{.AppImport}}/{{.Pkg}}"
	"github.com/golaravel/golaravel/pkg/offline"
	"github.com/golaravel/golaravel/pkg/privacy"
)

// {{.Name}} syncs app.{{.Name}} with offline clients:
//
//	eng.Register(synced.{{.Name}}())
//	client.{{.Name}}.Use(offline.ChangeHook(eng, "{{.Snake}}"))
func {{.Name}}() offline.Resource[*app.Tx] {
	return offline.Resource[*app.Tx]{
		Name:     "{{.Snake}}",
		Fields:   []string{ {{- range $i, $f := .Fields}}{{if $i}}, {{end}}"{{$f.Name}}"{{end -}} },
		Counters: []string{ {{- $n := 0}}{{range .Fields}}{{if .Counter}}{{if $n}}, {{end}}"{{.Name}}"{{$n = 1}}{{end}}{{end -}} },
		Load: func(ctx context.Context, tx *app.Tx, id string) (*offline.Record, error) {
			row, err := tx.{{.Name}}.Get(privacy.WithTrashed(ctx), id)
			if app.IsNotFound(err) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return {{.Name}}Record(row), nil
		},
		Snapshot: func(ctx context.Context, tx *app.Tx, after string, limit int) ([]*offline.Record, error) {
			rows, err := tx.{{.Name}}.Query().
				Where({{.Pkg}}.IDGT(after), {{.Pkg}}.DeletedAtIsNil()).
				Order({{.Pkg}}.ByID()).Limit(limit).All(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]*offline.Record, len(rows))
			for i, row := range rows {
				out[i] = {{.Name}}Record(row)
			}
			return out, nil
		},
		Apply: func(ctx context.Context, tx *app.Tx, w *offline.Write) error {
			var f struct {
{{- range .Fields}}
				{{.Pascal}} *{{.GoType}} ` + "`json:\"{{.Name}}\"`" + `
{{- end}}
			}
			if err := w.Decode(&f); err != nil {
				return offline.Reject("invalid", err)
			}
			switch {
			case w.Op == offline.OpCreate && w.Current == nil:
				c := tx.{{.Name}}.Create().SetID(w.EntityID).
					SetSyncVersion(w.Version).SetSyncHlc(w.HLC).SetSyncClocks(w.FieldClocks)
{{- range .Fields}}
{{- if .Counter}}
				var base{{.Pascal}} {{.GoType}}
				if f.{{.Pascal}} != nil {
					base{{.Pascal}} = *f.{{.Pascal}}
				}
				c.Set{{.Pascal}}(base{{.Pascal}} + w.Inc["{{.Name}}"])
{{- else}}
				if f.{{.Pascal}} != nil {
					c.Set{{.Pascal}}(*f.{{.Pascal}})
				}
{{- end}}
{{- end}}
				return c.Exec(ctx)
			case w.Op == offline.OpDelete:
				return tx.{{.Name}}.UpdateOneID(w.EntityID).SetDeletedAt(time.Now()).
					SetSyncVersion(w.Version).SetSyncHlc(w.HLC).Exec(ctx)
			default: // update, or a create that restores a deleted row
				u := tx.{{.Name}}.UpdateOneID(w.EntityID).ClearDeletedAt().
					SetSyncVersion(w.Version).SetSyncHlc(w.HLC).SetSyncClocks(w.FieldClocks)
{{- range .Fields}}
{{- if .Counter}}
				if f.{{.Pascal}} != nil {
					u.Set{{.Pascal}}(*f.{{.Pascal}} + w.Inc["{{.Name}}"])
				} else if d, ok := w.Inc["{{.Name}}"]; ok {
					u.Add{{.Pascal}}(d) // a delta, so concurrent sales add up
				}
{{- else}}
				if f.{{.Pascal}} != nil {
					u.Set{{.Pascal}}(*f.{{.Pascal}})
				}
{{- end}}
{{- end}}
				return u.Exec(ctx)
			}
		},
	}
}

// {{.Name}}Record is what clients receive for a {{.Snake}}.
func {{.Name}}Record(row *app.{{.Name}}) *offline.Record {
	return &offline.Record{
		ID: row.ID, Version: row.SyncVersion, HLC: row.SyncHlc, FieldClocks: row.SyncClocks,
		Deleted: row.DeletedAt != nil,
		Data: map[string]any{
{{- range .Fields}}
			"{{.Name}}": row.{{.Pascal}},
{{- end}}
		},
	}
}
`
