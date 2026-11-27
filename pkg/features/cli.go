package features

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// CLI runs the features:* Artisan commands. Register them with:
//
//	cli := features.NewCLI(mgr, os.Stdout)
//	for _, c := range cli.Commands() {
//		registry.Register(&artisan.Command{Name: c.Name, Description: c.Description, Run: c.Run})
//	}
//
// The commands act on any tenant, so they run with tenancy bypassed.
type CLI struct {
	m   *Manager
	out io.Writer
}

// NewCLI returns a CLI writing to out (os.Stdout when nil).
func NewCLI(m *Manager, out io.Writer) *CLI {
	if out == nil {
		out = os.Stdout
	}
	return &CLI{m: m, out: out}
}

// CLICommand is an Artisan-compatible command.
type CLICommand struct {
	Name        string
	Description string
	Run         func(args []string) error
}

// Commands lists the features:* commands.
func (c *CLI) Commands() []CLICommand {
	return []CLICommand{
		{"features:list", "List features, or one tenant's values (--tenant=acme)", c.List},
		{"features:set", "Set a tenant's value: features:set <tenant> <key> <value>", c.Set},
		{"features:reset", "Reset a tenant's value to the default: features:reset <tenant> <key>", c.Reset},
		{"features:prune", "Delete stored values of features no longer registered (--dry-run)", c.Prune},
	}
}

// List prints every feature with its default, or, with --tenant, the
// tenant's values ("*" marks an override).
func (c *CLI) List(args []string) error {
	fs := flag.NewFlagSet("features:list", flag.ContinueOnError)
	fs.SetOutput(c.out)
	tenantID := fs.String("tenant", "", "show this tenant's values")
	if err := fs.Parse(args); err != nil {
		return err
	}
	w := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
	if *tenantID == "" {
		fmt.Fprintln(w, "KEY\tTYPE\tDEFAULT\tMANAGED BY\tOPTIONS")
		for _, meta := range c.m.Definitions() {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", meta.Key, meta.Type, show(meta.Default), meta.ManagedBy,
				strings.Join(meta.Options, ", "))
		}
		return w.Flush()
	}
	all, err := c.m.All(ForTenant(context.Background(), *tenantID))
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "KEY\tVALUE\tDEFAULT\tMANAGED BY")
	for _, r := range all {
		mark := ""
		if r.Overridden {
			mark = " *"
		}
		fmt.Fprintf(w, "%s\t%s%s\t%s\t%s\n", r.Key, show(r.Value), mark, show(r.Default), r.ManagedBy)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "\n* overridden for this tenant")
	return nil
}

// Set stores a tenant's value. The value is parsed like a form field:
// "true", "5", "exclusive", `{"color":"red"}`.
func (c *CLI) Set(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: features:set <tenant> <key> <value>")
	}
	if err := c.m.Set(ForTenant(context.Background(), args[0]), args[1], args[2]); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Set %s for tenant %s.\n", args[1], args[0])
	return nil
}

// Reset removes a tenant's value.
func (c *CLI) Reset(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: features:reset <tenant> <key>")
	}
	if err := c.m.Reset(ForTenant(context.Background(), args[0]), args[1]); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Reset %s for tenant %s to the default.\n", args[1], args[0])
	return nil
}

// Prune deletes stored values whose key is no longer registered.
func (c *CLI) Prune(args []string) error {
	fs := flag.NewFlagSet("features:prune", flag.ContinueOnError)
	fs.SetOutput(c.out)
	dry := fs.Bool("dry-run", false, "only list what would be deleted")
	if err := fs.Parse(args); err != nil {
		return err
	}
	stale, err := c.m.Prune(context.Background(), *dry)
	if err != nil {
		return err
	}
	verb := "Deleted"
	if *dry {
		verb = "Would delete"
	}
	for _, r := range stale {
		fmt.Fprintf(c.out, "  %s  %s\n", r.Tenant, r.Key)
	}
	fmt.Fprintf(c.out, "%s %d stored value(s) of unregistered features.\n", verb, len(stale))
	return nil
}

// StaleValue is a stored value whose feature is no longer registered.
type StaleValue struct{ Tenant, Key string }

// Prune deletes (or, with dryRun, only lists) stored values whose key is
// not registered, across all tenants. Register every feature first:
// values of features missing from this Manager are deleted.
func (m *Manager) Prune(ctx context.Context, dryRun bool) ([]StaleValue, error) {
	rows, err := m.store.rows(ctx)
	if err != nil {
		return nil, err
	}
	var stale []StaleValue
	for _, r := range rows {
		if m.lookup(r[1]) == nil {
			stale = append(stale, StaleValue{Tenant: r[0], Key: r[1]})
		}
	}
	if dryRun {
		return stale, nil
	}
	for _, s := range stale {
		if err := m.store.delete(ctx, s.Tenant, s.Key); err != nil {
			return nil, err
		}
		m.invalidate(ctx, s.Tenant)
	}
	return stale, nil
}

func show(v any) string {
	if s, ok := v.(string); ok {
		if s == "" {
			return `""`
		}
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
